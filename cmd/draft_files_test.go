package cmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// fileUpload is what the mock's upload endpoint received for one file id.
type fileUpload struct {
	field    string // multipart field name
	filename string
	body     []byte
}

// draftFileStore models the upload flow and the draft writes that reference
// its files, as measured live (docs/draft-files-plan.md):
//
//   - files.getUploadURLExternal hands out an id and an upload_url on this
//     server, and rejects length=0.
//   - the upload POST stores the multipart file part.
//   - files.completeUploadExternal marks the id complete.
//   - drafts.create and drafts.update answer file_not_found for an id whose
//     upload was never completed, and otherwise {ok, draft, files} with the
//     ids in draft.file_ids and one file object per id that still exists.
//   - files.delete removes a completed file and answers file_not_found for an
//     id that was never completed.
//
// fail lets a test replace the nth (1-based) call to an endpoint.
type draftFileStore struct {
	t  *testing.T
	mu sync.Mutex

	calls     []string                       // endpoints in arrival order
	forms     map[string][]map[string]string // endpoint -> form of each call
	uploads   map[string]fileUpload
	lengths   map[string]string // id -> length sent to getUploadURLExternal
	names     map[string]string // id -> filename sent to getUploadURLExternal
	titles    map[string]string // id -> title sent to completeUploadExternal
	deleted   []string
	allocated int

	// existing files, for drafts that already reference some.
	seeded map[string]bool

	// drafts is what drafts.list answers, in order; listAfterWrite replaces
	// it once a draft write has been attempted (the re-fetch).
	drafts         []map[string]any
	listAfterWrite []map[string]any
	listFailsAfter bool
	writeAttempted bool

	fail map[string]func(n int, w http.ResponseWriter) bool

	// holdUpload, when set, makes the upload endpoint wait (without the
	// lock) until the first files.delete arrives, or three seconds.
	holdUpload  chan struct{}
	releaseOnce sync.Once
}

func newDraftFileStore(t *testing.T) *draftFileStore {
	return &draftFileStore{
		t:       t,
		forms:   map[string][]map[string]string{},
		uploads: map[string]fileUpload{},
		lengths: map[string]string{},
		names:   map[string]string{},
		titles:  map[string]string{},
		seeded:  map[string]bool{},
		fail:    map[string]func(int, http.ResponseWriter) bool{},
	}
}

// failNth makes the nth call to endpoint answer with the given status and
// body instead of the modelled behavior.
func (s *draftFileStore) failNth(endpoint string, nth, status int, body string) {
	s.fail[endpoint] = func(n int, w http.ResponseWriter) bool {
		if n != nth {
			return false
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return true
	}
}

// failAll makes every call to endpoint answer with the given status and body.
func (s *draftFileStore) failAll(endpoint string, status int, body string) {
	s.fail[endpoint] = func(_ int, w http.ResponseWriter) bool {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return true
	}
}

func (s *draftFileStore) count(endpoint string) int {
	n := 0
	for _, c := range s.calls {
		if c == endpoint {
			n++
		}
	}
	return n
}

// record notes the call and runs any override. It returns true when the
// override answered.
func (s *draftFileStore) record(endpoint string, form map[string]string, w http.ResponseWriter) bool {
	s.calls = append(s.calls, endpoint)
	s.forms[endpoint] = append(s.forms[endpoint], form)
	if f := s.fail[endpoint]; f != nil {
		return f(s.count(endpoint), w)
	}
	return false
}

func formOf(r *http.Request) map[string]string {
	_ = r.ParseForm()
	m := map[string]string{}
	for k := range r.PostForm {
		m[k] = r.PostForm.Get(k)
	}
	return m
}

func (s *draftFileStore) fileExists(id string) bool {
	if s.seeded[id] {
		return true
	}
	_, done := s.titles[id]
	if !done {
		return false
	}
	for _, d := range s.deleted {
		if d == id {
			return false
		}
	}
	return true
}

func (s *draftFileStore) fileObject(id string) map[string]any {
	up := s.uploads[id]
	return map[string]any{
		"id": id, "name": s.names[id], "title": s.titles[id], "size": len(up.body),
		"filetype": "", "mimetype": "", "mode": "hosted", "is_public": false,
	}
}

// writeDraft answers a drafts.create or drafts.update the way Slack does for
// file ids: file_not_found for one that does not exist, else the draft and the
// file objects.
func (s *draftFileStore) writeDraft(w http.ResponseWriter, id string, form map[string]string) {
	var ids []string
	if err := json.Unmarshal([]byte(form["file_ids"]), &ids); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_arguments"})
		return
	}
	files := []map[string]any{}
	for _, fid := range ids {
		if !s.fileExists(fid) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "file_not_found"})
			return
		}
		if s.seeded[fid] {
			files = append(files, map[string]any{"id": fid, "name": "seeded.pdf", "title": "seeded.pdf", "size": 600})
		} else {
			files = append(files, s.fileObject(fid))
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"draft": map[string]any{
			"id": id, "client_msg_id": "uuid-new", "date_created": 1713300000,
			"last_updated_ts": "1713300000.1234567",
			"blocks":          json.RawMessage(form["blocks"]),
			"file_ids":        ids,
			"destinations":    json.RawMessage(form["destinations"]),
		},
		"files": files,
	})
}

func (s *draftFileStore) mux() *http.ServeMux {
	mux := http.NewServeMux()
	handle := func(endpoint string, fn func(w http.ResponseWriter, r *http.Request, form map[string]string)) {
		mux.HandleFunc("/api/"+endpoint, func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			form := formOf(r)
			if s.record(endpoint, form, w) {
				return
			}
			fn(w, r, form)
		})
	}
	handle("files.getUploadURLExternal", func(w http.ResponseWriter, r *http.Request, form map[string]string) {
		if form["length"] == "0" || form["length"] == "" || form["filename"] == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_arguments"})
			return
		}
		s.allocated++
		id := fmt.Sprintf("F%02dABC", s.allocated)
		s.lengths[id] = form["length"]
		s.names[id] = form["filename"]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "file_id": id, "upload_url": "http://" + r.Host + "/upload/" + id,
		})
	})
	mux.HandleFunc("/upload/", func(w http.ResponseWriter, r *http.Request) {
		if s.holdUpload != nil {
			select {
			case <-s.holdUpload:
			case <-time.After(3 * time.Second):
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/upload/")
		mr, err := r.MultipartReader()
		if err != nil {
			s.t.Errorf("upload for %s is not multipart: %v", id, err)
			http.Error(w, "bad", 400)
			return
		}
		var up fileUpload
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FileName() == "" {
				continue
			}
			up.field, up.filename = part.FormName(), part.FileName()
			up.body, _ = io.ReadAll(part)
		}
		// Recorded after the body is drained so a failing override still
		// leaves the client a complete request.
		if s.record("upload", nil, w) {
			return
		}
		s.uploads[id] = up
		_, _ = fmt.Fprintf(w, "OK - %d", len(up.body))
	})
	handle("files.completeUploadExternal", func(w http.ResponseWriter, r *http.Request, form map[string]string) {
		var files []struct{ ID, Title string }
		_ = json.Unmarshal([]byte(form["files"]), &files)
		out := []map[string]any{}
		for _, f := range files {
			if _, uploaded := s.uploads[f.ID]; !uploaded {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "file_not_found"})
				return
			}
			s.titles[f.ID] = f.Title
			out = append(out, map[string]any{"id": f.ID, "title": f.Title})
		}
		if form["channel_id"] != "" {
			s.t.Errorf("completeUploadExternal must not share the file to a channel, got channel_id=%q", form["channel_id"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "files": out})
	})
	handle("files.delete", func(w http.ResponseWriter, r *http.Request, form map[string]string) {
		if s.holdUpload != nil {
			s.releaseOnce.Do(func() { close(s.holdUpload) })
		}
		id := form["file"]
		if !s.fileExists(id) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "file_not_found"})
			return
		}
		s.deleted = append(s.deleted, id)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	handle("drafts.list", func(w http.ResponseWriter, r *http.Request, form map[string]string) {
		drafts := s.drafts
		if s.writeAttempted && (s.listAfterWrite != nil || s.listFailsAfter) {
			if s.listFailsAfter {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "internal_error"})
				return
			}
			drafts = s.listAfterWrite
		}
		if drafts == nil {
			drafts = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "drafts": drafts, "files": []any{}})
	})
	// writeAttempted is set before the override runs, so a failed write still
	// switches drafts.list to the re-fetch answer.
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.writeAttempted = true
		form := formOf(r)
		if s.record("drafts.create", form, w) {
			return
		}
		s.writeDraft(w, "DrNEW", form)
	})
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.writeAttempted = true
		form := formOf(r)
		if s.record("drafts.update", form, w) {
			return
		}
		s.writeDraft(w, form["draft_id"], form)
	})
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls = append(s.calls, "users.list")
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "members": []any{}})
	})
	return mux
}

// lastForm is the form of the most recent call to endpoint.
func (s *draftFileStore) lastForm(endpoint string) map[string]string {
	s.t.Helper()
	f := s.forms[endpoint]
	if len(f) == 0 {
		s.t.Fatalf("no %s call was made; calls: %v", endpoint, s.calls)
	}
	return f[len(f)-1]
}

// runDraftFiles runs a command against the store with a session token and
// returns stdout, stderr, and the error.
func runDraftFiles(t *testing.T, s *draftFileStore, stdin string, args ...string) mockResult {
	t.Helper()
	srv := httptest.NewServer(s.mux())
	defer srv.Close()

	isolateTestEnv(t)
	t.Setenv("SLACK_TOKEN", "xoxc-test")
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return mockResult{err: err}
	}
	cli.SetOutput(&outBuf, &errBuf)
	if stdin != "" {
		cli.SetInput(strings.NewReader(stdin))
	}
	runErr := kctx.Run(&cli)
	return mockResult{stdout: outBuf.String(), stderr: errBuf.String(), err: runErr}
}

// tempFile writes content under a fresh directory and returns the path.
func tempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertStoreCalls(t *testing.T, s *draftFileStore, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	got := s.calls
	if got == nil {
		got = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func assertDeleted(t *testing.T, s *draftFileStore, want ...string) {
	t.Helper()
	var got []string
	for _, f := range s.forms["files.delete"] {
		got = append(got, f["file"])
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files.delete called for %v, want %v", got, want)
	}
}

func draftErr(t *testing.T, err error, wantCode string) *output.Error {
	t.Helper()
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error %q, got %T: %v", wantCode, err, err)
	}
	if oErr.Err != wantCode {
		t.Fatalf("error = %q (%s), want %q", oErr.Err, oErr.Detail, wantCode)
	}
	return oErr
}

// firstRow parses the first stdout line and checks a trailer follows it.
func firstRow(t *testing.T, out string) map[string]any {
	t.Helper()
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected one row and a trailer, got %d lines: %s", len(lines), out)
	}
	if _, ok := parseJSON(t, lines[1])["_meta"]; !ok {
		t.Fatalf("second line is not a trailer: %s", lines[1])
	}
	return parseJSON(t, lines[0])
}

var uploadSteps = []string{"files.getUploadURLExternal", "upload", "files.completeUploadExternal"}

func steps(n int, then ...string) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, uploadSteps...)
	}
	return append(out, then...)
}

// --- create ---

func TestDraftCreateFile_OneFile(t *testing.T) {
	s := newDraftFileStore(t)
	content := "%PDF-1.4 not really\x00\x01 a pdf\n"
	path := tempFile(t, "report.pdf", content)

	r := runDraftFiles(t, s, richTextBlocksJSON("see attached"), "draft", "create", "C01ABC", "--file", path)
	if r.err != nil {
		t.Fatal(r.err)
	}
	assertStoreCalls(t, s, steps(1, "drafts.create")...)

	if got := s.lastForm("files.getUploadURLExternal"); got["filename"] != "report.pdf" || got["length"] != fmt.Sprint(len(content)) {
		t.Errorf("getUploadURLExternal form = %v, want filename report.pdf and length %d", got, len(content))
	}
	up := s.uploads["F01ABC"]
	if up.field != "file" || up.filename != "report.pdf" || string(up.body) != content {
		t.Errorf("upload = field %q, filename %q, %d bytes; want field file, report.pdf, the file's %d bytes intact",
			up.field, up.filename, len(up.body), len(content))
	}
	if s.titles["F01ABC"] != "report.pdf" {
		t.Errorf("completeUploadExternal title = %q, want the base name", s.titles["F01ABC"])
	}

	form := s.lastForm("drafts.create")
	if form["file_ids"] != `["F01ABC"]` {
		t.Errorf("drafts.create file_ids = %q", form["file_ids"])
	}
	if form["blocks"] != richTextBlocksJSON("see attached") {
		t.Errorf("drafts.create blocks = %q", form["blocks"])
	}

	row := firstRow(t, r.stdout)
	if !reflect.DeepEqual(row["file_ids"], []any{"F01ABC"}) {
		t.Errorf("row file_ids = %v", row["file_ids"])
	}
	want := []any{map[string]any{"id": "F01ABC", "name": "report.pdf", "title": "report.pdf", "size": float64(len(content))}}
	if !reflect.DeepEqual(row["files"], want) {
		t.Errorf("row files = %v, want %v (empty filetype and mimetype omitted)", row["files"], want)
	}
	assertDeleted(t, s)
}

func TestDraftCreateFile_TwoFilesKeepFlagOrder(t *testing.T) {
	s := newDraftFileStore(t)
	b := tempFile(t, "b.txt", "second letter, first flag")
	a := tempFile(t, "a.txt", "first letter, second flag")

	r := runDraftFiles(t, s, richTextBlocksJSON("two"), "draft", "create", "C01ABC", "--file", b, "--file", a)
	if r.err != nil {
		t.Fatal(r.err)
	}
	assertStoreCalls(t, s, steps(2, "drafts.create")...)
	if s.names["F01ABC"] != "b.txt" || s.names["F02ABC"] != "a.txt" {
		t.Errorf("upload order = %v, want b.txt then a.txt", s.names)
	}
	if string(s.uploads["F01ABC"].body) != "second letter, first flag" || string(s.uploads["F02ABC"].body) != "first letter, second flag" {
		t.Errorf("each upload must carry its own file's bytes, got %q and %q", s.uploads["F01ABC"].body, s.uploads["F02ABC"].body)
	}
	if got := s.lastForm("drafts.create")["file_ids"]; got != `["F01ABC","F02ABC"]` {
		t.Errorf("file_ids = %q, want flag order", got)
	}
	row := firstRow(t, r.stdout)
	files, _ := row["files"].([]any)
	if len(files) != 2 || files[0].(map[string]any)["name"] != "b.txt" || files[1].(map[string]any)["name"] != "a.txt" {
		t.Errorf("row files = %v, want b.txt then a.txt", row["files"])
	}
}

// A comma in a path is part of the path, not a separator.
func TestDraftCreateFile_PathWithComma(t *testing.T) {
	s := newDraftFileStore(t)
	path := tempFile(t, "a,b.txt", "x")
	r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "draft", "create", "C01ABC", "--file", path)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if s.names["F01ABC"] != "a,b.txt" || s.count("files.getUploadURLExternal") != 1 {
		t.Errorf("names = %v, want one upload named a,b.txt", s.names)
	}
}

func TestDraftCreateFile_FileOnly(t *testing.T) {
	for name, stdin := range map[string]string{"no stdin": "", "blank stdin": "  \n", "empty blocks array": "[]"} {
		t.Run(name, func(t *testing.T) {
			s := newDraftFileStore(t)
			path := tempFile(t, "notes.txt", "just the file")
			r := runDraftFiles(t, s, stdin, "draft", "create", "C01ABC", "--file", path)
			if r.err != nil {
				t.Fatal(r.err)
			}
			form := s.lastForm("drafts.create")
			if form["blocks"] != "[]" || form["file_ids"] != `["F01ABC"]` {
				t.Errorf("drafts.create blocks = %q, file_ids = %q; want [] and the file", form["blocks"], form["file_ids"])
			}
			if _, has := form["attachments"]; has {
				t.Errorf("no attachments param expected, got %q", form["attachments"])
			}
		})
	}
}

func TestDraftCreateFile_WithMarkdown(t *testing.T) {
	s := newDraftFileStore(t)
	path := tempFile(t, "notes.txt", "x")
	r := runDraftFiles(t, s, "# Title\n\nbody\n", "draft", "create", "C01ABC", "--markdown", "--file", path)
	if r.err != nil {
		t.Fatal(r.err)
	}
	form := s.lastForm("drafts.create")
	if !strings.Contains(form["blocks"], `"header"`) || form["file_ids"] != `["F01ABC"]` {
		t.Errorf("blocks = %q, file_ids = %q", form["blocks"], form["file_ids"])
	}
}

func TestDraftCreateFile_WithTable(t *testing.T) {
	s := newDraftFileStore(t)
	path := tempFile(t, "notes.txt", "x")
	table := tempFile(t, "t.csv", "a,b\n1,2\n")
	r := runDraftFiles(t, s, "", "draft", "create", "C01ABC", "--table", table, "--file", path)
	if r.err != nil {
		t.Fatal(r.err)
	}
	form := s.lastForm("drafts.create")
	if !strings.Contains(form["attachments"], `"table"`) || form["file_ids"] != `["F01ABC"]` || form["blocks"] != "[]" {
		t.Errorf("attachments = %q, file_ids = %q, blocks = %q", form["attachments"], form["file_ids"], form["blocks"])
	}
}

func TestDraftCreateFile_NoFlagStillSendsEmptyFileIDs(t *testing.T) {
	s := newDraftFileStore(t)
	r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "draft", "create", "C01ABC")
	if r.err != nil {
		t.Fatal(r.err)
	}
	assertStoreCalls(t, s, "drafts.create")
	if got := s.lastForm("drafts.create")["file_ids"]; got != "[]" {
		t.Errorf("file_ids = %q, want [] (Slack rejects the param omitted)", got)
	}
	if !strings.Contains(r.stdout, `"file_ids":[]`) {
		t.Errorf("row must carry file_ids as [], got %s", r.stdout)
	}
	if _, has := firstRow(t, r.stdout)["files"]; has {
		t.Errorf("no files key expected on a draft without files: %s", r.stdout)
	}
}

func TestDraftFile_InvalidPathsMakeNoRequest(t *testing.T) {
	dir := t.TempDir()
	empty := tempFile(t, "empty.txt", "")
	good := tempFile(t, "good.txt", "x")
	missing := filepath.Join(dir, "missing.pdf")

	paths := map[string]struct {
		flags []string
		named string
	}{
		"missing":             {[]string{"--file", missing}, missing},
		"directory":           {[]string{"--file", dir}, dir},
		"empty file":          {[]string{"--file", empty}, empty},
		"second path invalid": {[]string{"--file", good, "--file", missing}, missing},
	}
	// A root user can read a mode-000 file, so the case only holds otherwise.
	if os.Geteuid() != 0 {
		unreadable := tempFile(t, "secret.txt", "x")
		if err := os.Chmod(unreadable, 0); err != nil {
			t.Fatal(err)
		}
		paths["unreadable"] = struct {
			flags []string
			named string
		}{[]string{"--file", unreadable}, unreadable}
	}
	commands := map[string][]string{
		"create": {"draft", "create", "C01ABC"},
		"update": {"draft", "update", "Dr01234"},
	}
	for cname, base := range commands {
		for pname, tt := range paths {
			t.Run(cname+" "+pname, func(t *testing.T) {
				s := newDraftFileStore(t)
				s.drafts = []map[string]any{liveDraft(nil)}
				r := runDraftFiles(t, s, richTextBlocksJSON("hi"), append(append([]string{}, base...), tt.flags...)...)
				oErr := draftErr(t, r.err, "invalid_input")
				if !strings.Contains(oErr.Detail, tt.named) {
					t.Errorf("detail %q does not name the path %q", oErr.Detail, tt.named)
				}
				assertStoreCalls(t, s)
				if r.stdout != "" {
					t.Errorf("expected no stdout, got %q", r.stdout)
				}
			})
		}
	}
}

func TestDraftCreateFile_UnresolvedRecipientUploadsNothing(t *testing.T) {
	s := newDraftFileStore(t)
	path := tempFile(t, "notes.txt", "x")
	r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "draft", "create", "@nobody", "--file", path)
	assertOutputErrorCode(t, r.err, "user_not_found")
	assertStoreCalls(t, s, "users.list")
}

func TestDraftCreateFile_InputErrorsUploadNothing(t *testing.T) {
	path := tempFile(t, "notes.txt", "x")
	cases := map[string]struct {
		stdin string
		args  []string
		code  string
	}{
		"bad timestamp":            {richTextBlocksJSON("hi"), []string{"C01ABC", "--at", "not a time"}, "invalid_timestamp"},
		"mixed recipients":         {richTextBlocksJSON("hi"), []string{"C01ABC", "U01XYZ"}, "invalid_input"},
		"invalid blocks":           {`[{"type":"section"}]`, []string{"C01ABC"}, "invalid_blocks"},
		"print-payload":            {"hello\n", []string{"C01ABC", "--markdown", "--print-payload"}, "invalid_input"},
		"broadcast without thread": {richTextBlocksJSON("hi"), []string{"C01ABC", "--broadcast"}, "invalid_input"},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			s := newDraftFileStore(t)
			args := append([]string{"draft", "create"}, tt.args...)
			r := runDraftFiles(t, s, tt.stdin, append(args, "--file", path)...)
			assertOutputErrorCode(t, r.err, tt.code)
			assertStoreCalls(t, s)
			if r.stdout != "" {
				t.Errorf("expected no stdout, got %q", r.stdout)
			}
		})
	}
}

// Without --file, an empty create is still missing_blocks, and the message
// now names the flag.
func TestDraftCreate_MissingBlocksNamesFileFlag(t *testing.T) {
	s := newDraftFileStore(t)
	r := runDraftFiles(t, s, "  ", "draft", "create", "C01ABC")
	oErr := draftErr(t, r.err, "missing_blocks")
	if !strings.Contains(oErr.Detail, "--file") {
		t.Errorf("detail %q does not mention --file", oErr.Detail)
	}
	r = runDraftFiles(t, newDraftFileStore(t), "[]", "draft", "create", "C01ABC")
	oErr = draftErr(t, r.err, "invalid_blocks")
	if !strings.Contains(oErr.Detail, "file") {
		t.Errorf("detail %q does not mention a file as renderable content", oErr.Detail)
	}
}

// An upload that fails partway deletes every id handed out so far, completed
// or not, and writes no draft.
func TestDraftCreateFile_UploadFailureDeletesAllocatedFiles(t *testing.T) {
	cases := map[string]struct {
		endpoint    string
		nth, status int
		body        string
		wantCode    string
		wantDeletes []string
		wantCalls   []string
	}{
		"second getUploadURLExternal refused": {
			"files.getUploadURLExternal", 2, 200, `{"ok":false,"error":"internal_error"}`, "internal_error",
			[]string{"F01ABC"},
			steps(1, "files.getUploadURLExternal", "files.delete"),
		},
		"second upload POST fails": {
			"upload", 2, 500, "boom", "http_error",
			[]string{"F01ABC", "F02ABC"},
			steps(1, "files.getUploadURLExternal", "upload", "files.delete", "files.delete"),
		},
		"first completeUploadExternal refused": {
			"files.completeUploadExternal", 1, 200, `{"ok":false,"error":"internal_error"}`, "internal_error",
			[]string{"F01ABC"},
			steps(1, "files.delete"),
		},
		"second completeUploadExternal refused": {
			"files.completeUploadExternal", 2, 200, `{"ok":false,"error":"internal_error"}`, "internal_error",
			[]string{"F01ABC", "F02ABC"},
			steps(2, "files.delete", "files.delete"),
		},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			s := newDraftFileStore(t)
			s.failNth(tt.endpoint, tt.nth, tt.status, tt.body)
			a := tempFile(t, "a.txt", "aaa")
			b := tempFile(t, "b.txt", "bbb")

			r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "draft", "create", "C01ABC", "--file", a, "--file", b)
			oErr := draftErr(t, r.err, tt.wantCode)
			assertStoreCalls(t, s, tt.wantCalls...)
			assertDeleted(t, s, tt.wantDeletes...)
			if s.count("drafts.create") != 0 {
				t.Error("no draft may be written after a failed upload")
			}
			// Every delete either removed the file or found none, so nothing
			// is left to report.
			if strings.Contains(oErr.Detail, "F01ABC") || strings.Contains(oErr.Detail, "F02ABC") {
				t.Errorf("detail %q names file ids though cleanup left nothing behind", oErr.Detail)
			}
			if r.stdout != "" {
				t.Errorf("expected no stdout, got %q", r.stdout)
			}
		})
	}
}

// When the command's own deadline is what failed the upload, the cleanup still
// runs: it has its own deadline.
func TestDraftCreateFile_CleanupOutlivesTheCommandTimeout(t *testing.T) {
	s := newDraftFileStore(t)
	s.holdUpload = make(chan struct{})
	path := tempFile(t, "a.txt", "aaa")

	r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "--timeout", "300ms", "draft", "create", "C01ABC", "--file", path)
	assertOutputErrorCode(t, r.err, "timeout")
	assertDeleted(t, s, "F01ABC")
	if s.count("drafts.create") != 0 {
		t.Error("no draft may be written after a failed upload")
	}
}

// A cleanup that itself fails names the files it could not delete.
func TestDraftCreateFile_FailedCleanupNamesTheFiles(t *testing.T) {
	s := newDraftFileStore(t)
	s.failNth("files.getUploadURLExternal", 2, 200, `{"ok":false,"error":"internal_error"}`)
	s.failAll("files.delete", 200, `{"ok":false,"error":"cant_delete_file"}`)
	a := tempFile(t, "a.txt", "aaa")
	b := tempFile(t, "b.txt", "bbb")

	r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "draft", "create", "C01ABC", "--file", a, "--file", b)
	oErr := draftErr(t, r.err, "internal_error")
	assertDeleted(t, s, "F01ABC")
	if !strings.Contains(oErr.Detail, "F01ABC") {
		t.Errorf("detail %q does not name the file left behind", oErr.Detail)
	}
}

// What a failed drafts.create means for the uploaded files depends on whether
// Slack refused the request or the outcome is unknown.
func TestDraftCreateFile_FailedCreate(t *testing.T) {
	cases := map[string]struct {
		status      int
		body        string
		header      map[string]string
		wantCode    string
		wantDeletes []string
		wantIDs     bool // ids named in the detail
	}{
		"refused":            {200, `{"ok":false,"error":"restricted_action"}`, nil, "restricted_action", []string{"F01ABC", "F02ABC"}, false},
		"auth refused":       {200, `{"ok":false,"error":"invalid_auth"}`, nil, "invalid_auth", []string{"F01ABC", "F02ABC"}, false},
		"500":                {500, "boom", nil, "http_error", nil, true},
		"rate limited":       {429, "", map[string]string{"Retry-After": "1"}, "rate_limited", nil, true},
		"body rate limit":    {200, `{"ok":false,"error":"ratelimited"}`, nil, "rate_limited", nil, true},
		"ok but unparseable": {200, `{"ok":true,"draft":"nope"}`, nil, "parse_error", nil, false},
		"200 with html":      {200, "<html>portal</html>", nil, "parse_error", nil, true},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			s := newDraftFileStore(t)
			s.fail["drafts.create"] = func(_ int, w http.ResponseWriter) bool {
				for k, v := range tt.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
				return true
			}
			a := tempFile(t, "a.txt", "aaa")
			b := tempFile(t, "b.txt", "bbb")

			r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "draft", "create", "C01ABC", "--file", a, "--file", b)
			oErr := draftErr(t, r.err, tt.wantCode)
			assertDeleted(t, s, tt.wantDeletes...)
			named := strings.Contains(oErr.Detail, "F01ABC") && strings.Contains(oErr.Detail, "F02ABC")
			if named != tt.wantIDs {
				t.Errorf("detail %q: ids named = %v, want %v", oErr.Detail, named, tt.wantIDs)
			}
			if s.count("drafts.create") != 1 {
				t.Errorf("drafts.create calls = %d, want 1 (never retried)", s.count("drafts.create"))
			}
			if r.stdout != "" {
				t.Errorf("expected no stdout, got %q", r.stdout)
			}
		})
	}
}

// A refused create whose cleanup also fails names the files.
func TestDraftCreateFile_RefusedCreateFailedCleanup(t *testing.T) {
	s := newDraftFileStore(t)
	s.failAll("drafts.create", 200, `{"ok":false,"error":"restricted_action"}`)
	s.failNth("files.delete", 2, 500, "boom")
	a := tempFile(t, "a.txt", "aaa")
	b := tempFile(t, "b.txt", "bbb")

	r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "draft", "create", "C01ABC", "--file", a, "--file", b)
	oErr := draftErr(t, r.err, "restricted_action")
	assertDeleted(t, s, "F01ABC", "F02ABC")
	if strings.Contains(oErr.Detail, "F01ABC") || !strings.Contains(oErr.Detail, "F02ABC") {
		t.Errorf("detail %q: want only F02ABC, the one whose delete failed", oErr.Detail)
	}
}

// A create without --file keeps its error untouched and never calls
// files.delete.
func TestDraftCreate_FailedCreateWithoutFiles(t *testing.T) {
	s := newDraftFileStore(t)
	s.failAll("drafts.create", 500, "boom")
	r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "draft", "create", "C01ABC")
	oErr := draftErr(t, r.err, "http_error")
	assertStoreCalls(t, s, "drafts.create")
	if strings.Contains(oErr.Detail, "file") {
		t.Errorf("detail %q mentions files on a draft with none", oErr.Detail)
	}
}

func TestDraftCreateFile_Trace(t *testing.T) {
	s := newDraftFileStore(t)
	path := tempFile(t, "notes.txt", "x")
	r := runDraftFiles(t, s, richTextBlocksJSON("hi"), "--trace", "draft", "create", "C01ABC", "--file", path)
	if r.err != nil {
		t.Fatal(r.err)
	}
	var got []string
	for _, line := range nonEmptyLines(r.stderr) {
		ev := parseJSON(t, line)
		if ev["kind"] == "call" {
			got = append(got, ev["endpoint"].(string))
		}
	}
	want := []string{"files.getUploadURLExternal", "files.upload", "files.completeUploadExternal"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("call trace events = %v, want %v", got, want)
	}
}

// --- update ---

// fileDraft is a live draft that already references one file.
func fileDraft(extra map[string]any) map[string]any {
	d := liveDraft(map[string]any{"file_ids": []string{"F09OLD"}})
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func newUpdateStore(t *testing.T, draft map[string]any) *draftFileStore {
	s := newDraftFileStore(t)
	s.seeded["F09OLD"] = true
	s.drafts = []map[string]any{draft}
	return s
}

func TestDraftUpdateFile_NoFlagPreserves(t *testing.T) {
	s := newUpdateStore(t, fileDraft(nil))
	r := runDraftFiles(t, s, richTextBlocksJSON("new"), "draft", "update", "Dr01234")
	if r.err != nil {
		t.Fatal(r.err)
	}
	assertStoreCalls(t, s, "drafts.list", "drafts.update")
	if got := s.lastForm("drafts.update")["file_ids"]; got != `["F09OLD"]` {
		t.Errorf("file_ids = %q, want the existing list", got)
	}
	row := firstRow(t, r.stdout)
	if !reflect.DeepEqual(row["file_ids"], []any{"F09OLD"}) {
		t.Errorf("row file_ids = %v", row["file_ids"])
	}
	files, _ := row["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["name"] != "seeded.pdf" {
		t.Errorf("row files = %v, want the drafts.update response's file object", row["files"])
	}
}

func TestDraftUpdateFile_FileReplaces(t *testing.T) {
	s := newUpdateStore(t, fileDraft(nil))
	path := tempFile(t, "new.txt", "new file")
	// --file alone is a valid update: no stdin, no schedule flag.
	r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
	if r.err != nil {
		t.Fatal(r.err)
	}
	assertStoreCalls(t, s, append([]string{"drafts.list"}, steps(1, "drafts.update")...)...)
	form := s.lastForm("drafts.update")
	if form["file_ids"] != `["F01ABC"]` {
		t.Errorf("file_ids = %q, want only the new file", form["file_ids"])
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(form["blocks"]), &blocks); err != nil || len(blocks) != 1 {
		t.Errorf("blocks = %q, want the existing body preserved", form["blocks"])
	}
	// The displaced file is not the CLI's to delete.
	assertDeleted(t, s)
	row := firstRow(t, r.stdout)
	if !reflect.DeepEqual(row["file_ids"], []any{"F01ABC"}) {
		t.Errorf("row file_ids = %v", row["file_ids"])
	}
}

func TestDraftUpdateFile_NoFilesClears(t *testing.T) {
	s := newUpdateStore(t, fileDraft(nil))
	// --no-files alone is a valid update.
	r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--no-files")
	if r.err != nil {
		t.Fatal(r.err)
	}
	assertStoreCalls(t, s, "drafts.list", "drafts.update")
	if got := s.lastForm("drafts.update")["file_ids"]; got != "[]" {
		t.Errorf("file_ids = %q, want []", got)
	}
	if !strings.Contains(r.stdout, `"file_ids":[]`) {
		t.Errorf("row must carry file_ids as [], got %s", r.stdout)
	}
	assertDeleted(t, s)
}

func TestDraftUpdateFile_FlagConflicts(t *testing.T) {
	path := tempFile(t, "new.txt", "x")
	cases := map[string]struct {
		stdin string
		args  []string
	}{
		"--file with --no-files":      {richTextBlocksJSON("x"), []string{"--file", path, "--no-files"}},
		"--print-payload with --file": {"hello\n", []string{"--markdown", "--print-payload", "--file", path}},
		"--print-payload --no-files":  {"hello\n", []string{"--markdown", "--print-payload", "--no-files"}},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			s := newUpdateStore(t, fileDraft(nil))
			r := runDraftFiles(t, s, tt.stdin, append([]string{"draft", "update", "Dr01234"}, tt.args...)...)
			assertOutputErrorCode(t, r.err, "invalid_input")
			assertStoreCalls(t, s)
			if r.stdout != "" {
				t.Errorf("expected no stdout, got %q", r.stdout)
			}
		})
	}
}

// The renderable-content rule runs on the effective draft, files included.
func TestDraftUpdateFile_RenderableContentCountsFiles(t *testing.T) {
	fileOnly := func() map[string]any { return fileDraft(map[string]any{"blocks": []any{}}) }
	path := tempFile(t, "new.txt", "x")

	t.Run("--no-files on a file-only draft is refused before any write", func(t *testing.T) {
		s := newUpdateStore(t, fileOnly())
		r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--no-files")
		assertOutputErrorCode(t, r.err, "invalid_blocks")
		assertStoreCalls(t, s, "drafts.list")
	})
	t.Run("emptying the blocks of a draft with no files is refused", func(t *testing.T) {
		s := newUpdateStore(t, liveDraft(nil))
		r := runDraftFiles(t, s, "[]", "draft", "update", "Dr01234")
		assertOutputErrorCode(t, r.err, "invalid_blocks")
		assertStoreCalls(t, s, "drafts.list")
	})
	t.Run("schedule-only update of a file-only draft keeps the files", func(t *testing.T) {
		s := newUpdateStore(t, fileOnly())
		r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--at", "2026-04-20")
		if r.err != nil {
			t.Fatal(r.err)
		}
		form := s.lastForm("drafts.update")
		if form["file_ids"] != `["F09OLD"]` || form["blocks"] != "[]" || form["date_scheduled"] == "0" {
			t.Errorf("form = %v", form)
		}
	})
	t.Run("replacing the blocks of a draft with files by [] succeeds", func(t *testing.T) {
		s := newUpdateStore(t, fileDraft(nil))
		r := runDraftFiles(t, s, "[]", "draft", "update", "Dr01234")
		if r.err != nil {
			t.Fatal(r.err)
		}
		form := s.lastForm("drafts.update")
		if form["file_ids"] != `["F09OLD"]` || form["blocks"] != "[]" {
			t.Errorf("form = %v", form)
		}
	})
	t.Run("--no-files with a new body on a file-only draft succeeds", func(t *testing.T) {
		s := newUpdateStore(t, fileOnly())
		r := runDraftFiles(t, s, richTextBlocksJSON("now text"), "draft", "update", "Dr01234", "--no-files")
		if r.err != nil {
			t.Fatal(r.err)
		}
		if got := s.lastForm("drafts.update")["file_ids"]; got != "[]" {
			t.Errorf("file_ids = %q", got)
		}
	})
	t.Run("--file with blocks [] on a draft with no files succeeds", func(t *testing.T) {
		s := newUpdateStore(t, liveDraft(nil))
		r := runDraftFiles(t, s, "[]", "draft", "update", "Dr01234", "--file", path)
		if r.err != nil {
			t.Fatal(r.err)
		}
		form := s.lastForm("drafts.update")
		if form["file_ids"] != `["F01ABC"]` || form["blocks"] != "[]" {
			t.Errorf("form = %v", form)
		}
	})
	t.Run("a refused effective draft uploads nothing", func(t *testing.T) {
		// The existing blocks cannot be round-tripped (a section block), so
		// the rule fails after the lookup and before the upload.
		bad := liveDraft(map[string]any{"blocks": []map[string]any{{"type": "section"}}})
		s := newUpdateStore(t, bad)
		r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
		assertOutputErrorCode(t, r.err, "invalid_blocks")
		assertStoreCalls(t, s, "drafts.list")
	})
}

func TestDraftUpdateFile_SentDraftUploadsNothing(t *testing.T) {
	s := newUpdateStore(t, fileDraft(map[string]any{"is_sent": true}))
	path := tempFile(t, "new.txt", "x")
	r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
	assertOutputErrorCode(t, r.err, "draft_already_sent")
	assertStoreCalls(t, s, "drafts.list")
}

func TestDraftUpdateFile_MissingDraftUploadsNothing(t *testing.T) {
	s := newDraftFileStore(t)
	path := tempFile(t, "new.txt", "x")
	r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
	assertOutputErrorCode(t, r.err, "draft_not_found")
	assertStoreCalls(t, s, "drafts.list")
}

// A tombstoned draft's replacement carries its files across.
func TestDraftUpdateFile_TombstoneReplacementCarriesFiles(t *testing.T) {
	path := tempFile(t, "new.txt", "x")
	cases := map[string]struct {
		args    []string
		wantIDs string
		calls   []string
	}{
		"existing files": {[]string{"--at", "2026-04-20"}, `["F09OLD"]`, []string{"drafts.list", "drafts.create"}},
		"replaced files": {[]string{"--file", path}, `["F01ABC"]`, append([]string{"drafts.list"}, steps(1, "drafts.create")...)},
		"cleared files":  {[]string{"--no-files"}, `[]`, []string{"drafts.list", "drafts.create"}},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			s := newUpdateStore(t, fileDraft(map[string]any{"is_deleted": true}))
			r := runDraftFiles(t, s, "", append([]string{"draft", "update", "Dr01234"}, tt.args...)...)
			if r.err != nil {
				t.Fatal(r.err)
			}
			assertStoreCalls(t, s, tt.calls...)
			if got := s.lastForm("drafts.create")["file_ids"]; got != tt.wantIDs {
				t.Errorf("replacement file_ids = %q, want %q", got, tt.wantIDs)
			}
			row := firstRow(t, r.stdout)
			if row["id"] != "DrNEW" || row["replaced_draft_id"] != "Dr01234" {
				t.Errorf("row = %v, want the replacement naming the old id", row)
			}
			assertDeleted(t, s)
		})
	}
}

// drafts.update refused: the re-fetch runs first, and decides both the
// outcome and what happens to the files this run uploaded.
func TestDraftUpdateFile_RefusedUpdate(t *testing.T) {
	cases := map[string]struct {
		refetch     []map[string]any
		listFails   bool
		wantErr     string // "" means the replacement succeeded
		wantDeletes []string
		wantCreates int
	}{
		"tombstoned: replacement reuses the uploads": {[]map[string]any{fileDraft(map[string]any{"is_deleted": true})}, false, "", nil, 1},
		"sent: uploads deleted":                      {[]map[string]any{fileDraft(map[string]any{"is_sent": true})}, false, "draft_already_sent", []string{"F01ABC"}, 0},
		"still live: uploads deleted":                {[]map[string]any{fileDraft(nil)}, false, "draft_update_invalid", []string{"F01ABC"}, 0},
		"gone from the list: uploads deleted":        {[]map[string]any{}, false, "draft_update_invalid", []string{"F01ABC"}, 0},
		"re-fetch fails: uploads deleted":            {nil, true, "draft_update_invalid", []string{"F01ABC"}, 0},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			s := newUpdateStore(t, fileDraft(nil))
			s.failAll("drafts.update", 200, `{"ok":false,"error":"draft_update_invalid"}`)
			s.listAfterWrite, s.listFailsAfter = tt.refetch, tt.listFails
			path := tempFile(t, "new.txt", "x")

			r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
			if tt.wantErr == "" {
				if r.err != nil {
					t.Fatal(r.err)
				}
				if got := s.lastForm("drafts.create")["file_ids"]; got != `["F01ABC"]` {
					t.Errorf("replacement file_ids = %q, want the file uploaded for the update", got)
				}
				if s.count("files.getUploadURLExternal") != 1 {
					t.Errorf("uploads = %d, want 1 (the replacement must not upload again)", s.count("files.getUploadURLExternal"))
				}
			} else {
				oErr := draftErr(t, r.err, tt.wantErr)
				if strings.Contains(oErr.Detail, "F01ABC") {
					t.Errorf("detail %q names a file the cleanup removed", oErr.Detail)
				}
			}
			assertDeleted(t, s, tt.wantDeletes...)
			if s.count("drafts.create") != tt.wantCreates {
				t.Errorf("drafts.create calls = %d, want %d", s.count("drafts.create"), tt.wantCreates)
			}
			// The re-fetch precedes any cleanup.
			var order []string
			for _, c := range s.calls {
				if c == "drafts.list" || c == "files.delete" {
					order = append(order, c)
				}
			}
			wantOrder := []string{"drafts.list", "drafts.list"}
			for range tt.wantDeletes {
				wantOrder = append(wantOrder, "files.delete")
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Errorf("list/delete order = %v, want %v", order, wantOrder)
			}
		})
	}
}

// An update whose outcome is unknown leaves the uploads in place and names
// them: the draft may now reference them.
func TestDraftUpdateFile_UnknownOutcomeKeepsFiles(t *testing.T) {
	cases := map[string]struct {
		status   int
		header   map[string]string
		body     string
		wantCode string
	}{
		"rate limited": {429, map[string]string{"Retry-After": "1"}, "", "rate_limited"},
		"500":          {500, nil, "boom", "http_error"},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			s := newUpdateStore(t, fileDraft(nil))
			s.fail["drafts.update"] = func(_ int, w http.ResponseWriter) bool {
				for k, v := range tt.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
				return true
			}
			path := tempFile(t, "new.txt", "x")
			r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
			oErr := draftErr(t, r.err, tt.wantCode)
			assertDeleted(t, s)
			if !strings.Contains(oErr.Detail, "F01ABC") {
				t.Errorf("detail %q does not name the uploaded file", oErr.Detail)
			}
			if s.count("drafts.create") != 0 {
				t.Errorf("no replacement expected, got %d creates", s.count("drafts.create"))
			}
		})
	}
}

// A 500 from drafts.update is not systemic, so the re-fetch runs. The update
// may have applied, so whatever the re-fetch shows, nothing is deleted.
func TestDraftUpdateFile_ServerErrorThenChangedState(t *testing.T) {
	t.Run("sent: files kept and named", func(t *testing.T) {
		s := newUpdateStore(t, fileDraft(nil))
		s.failAll("drafts.update", 500, "boom")
		s.listAfterWrite = []map[string]any{fileDraft(map[string]any{"is_sent": true})}
		path := tempFile(t, "new.txt", "x")

		r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
		oErr := draftErr(t, r.err, "draft_already_sent")
		assertDeleted(t, s)
		if !strings.Contains(oErr.Detail, "F01ABC") {
			t.Errorf("detail %q does not name the uploaded file", oErr.Detail)
		}
		if s.count("drafts.create") != 0 {
			t.Errorf("no replacement expected, got %d creates", s.count("drafts.create"))
		}
	})
	t.Run("tombstoned: replacement reuses the uploads", func(t *testing.T) {
		s := newUpdateStore(t, fileDraft(nil))
		s.failAll("drafts.update", 500, "boom")
		s.listAfterWrite = []map[string]any{fileDraft(map[string]any{"is_deleted": true})}
		path := tempFile(t, "new.txt", "x")

		r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
		if r.err != nil {
			t.Fatal(r.err)
		}
		assertDeleted(t, s)
		if got := s.lastForm("drafts.create")["file_ids"]; got != `["F01ABC"]` {
			t.Errorf("replacement file_ids = %q, want the file uploaded for the update", got)
		}
		if s.count("files.getUploadURLExternal") != 1 {
			t.Errorf("uploads = %d, want 1", s.count("files.getUploadURLExternal"))
		}
	})
}

// An update that preserved existing files and failed names none of them: the
// CLI uploaded nothing.
func TestDraftUpdateFile_FailedUpdateWithoutUploads(t *testing.T) {
	s := newUpdateStore(t, fileDraft(nil))
	s.failAll("drafts.update", 500, "boom")
	r := runDraftFiles(t, s, richTextBlocksJSON("new"), "draft", "update", "Dr01234")
	oErr := draftErr(t, r.err, "http_error")
	assertDeleted(t, s)
	if strings.Contains(oErr.Detail, "F09OLD") {
		t.Errorf("detail %q names a file this run did not upload", oErr.Detail)
	}
}

// A replacement create that fails is treated like any create.
func TestDraftUpdateFile_FailedReplacement(t *testing.T) {
	cases := map[string]struct {
		status      int
		body        string
		wantCode    string
		wantDeletes []string
		wantIDs     bool
	}{
		"refused": {200, `{"ok":false,"error":"restricted_action"}`, "restricted_action", []string{"F01ABC"}, false},
		"500":     {500, "boom", "http_error", nil, true},
	}
	for name, tt := range cases {
		for _, race := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s race=%v", name, race), func(t *testing.T) {
				// race=false: the lookup already shows the tombstone.
				// race=true: drafts.update is refused and the re-fetch shows it.
				first := fileDraft(map[string]any{"is_deleted": true})
				if race {
					first = fileDraft(nil)
				}
				s := newUpdateStore(t, first)
				s.failAll("drafts.update", 200, `{"ok":false,"error":"draft_update_invalid"}`)
				s.listAfterWrite = []map[string]any{fileDraft(map[string]any{"is_deleted": true})}
				s.failAll("drafts.create", tt.status, tt.body)
				path := tempFile(t, "new.txt", "x")

				r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
				oErr := draftErr(t, r.err, tt.wantCode)
				assertDeleted(t, s, tt.wantDeletes...)
				if named := strings.Contains(oErr.Detail, "F01ABC"); named != tt.wantIDs {
					t.Errorf("detail %q: id named = %v, want %v", oErr.Detail, named, tt.wantIDs)
				}
				if r.stdout != "" {
					t.Errorf("expected no stdout, got %q", r.stdout)
				}
			})
		}
	}
}

// A failed upload on update deletes what it allocated and never writes.
func TestDraftUpdateFile_UploadFailure(t *testing.T) {
	s := newUpdateStore(t, fileDraft(nil))
	s.failNth("files.completeUploadExternal", 1, 200, `{"ok":false,"error":"internal_error"}`)
	path := tempFile(t, "new.txt", "x")
	r := runDraftFiles(t, s, "", "draft", "update", "Dr01234", "--file", path)
	assertOutputErrorCode(t, r.err, "internal_error")
	assertDeleted(t, s, "F01ABC")
	if s.count("drafts.update") != 0 || s.count("drafts.create") != 0 {
		t.Errorf("no draft write expected, calls: %v", s.calls)
	}
}

// --- rows ---

func TestDraftList_FileRows(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"has_more":false,
"drafts":[
 {"id":"Dr01","last_updated_ts":"1.1","blocks":[],"destinations":[{"channel_id":"C01ABC"}],"file_ids":["F02DEF","F01ABC"]},
 {"id":"Dr02","last_updated_ts":"1.1","blocks":[],"destinations":[{"channel_id":"C01ABC"}],"file_ids":["F01ABC","F03GONE"]},
 {"id":"Dr03","last_updated_ts":"1.1","blocks":[],"destinations":[{"channel_id":"C01ABC"}],"file_ids":["F03GONE"]},
 {"id":"Dr04","last_updated_ts":"1.1","blocks":[],"destinations":[{"channel_id":"C01ABC"}],"file_ids":[]},
 {"id":"Dr05","last_updated_ts":"1.1","blocks":[],"destinations":[{"channel_id":"C01ABC"}],"file_ids":null},
 {"id":"Dr06","last_updated_ts":"1.1","blocks":[],"destinations":[{"channel_id":"C01ABC"}]}
],
"files":[
 {"id":"F01ABC","name":"a.pdf","title":"Quarterly","size":600,"filetype":"pdf","mimetype":"application/pdf","mode":"hosted","url_private":"https://files.example.com/a.pdf","shares":{}},
 {"id":"F02DEF","name":"b.txt","title":"","size":29,"filetype":"","mimetype":""}
]}`)
	})
	out, err := runWithMockSession(t, mux, "draft", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 7 {
		t.Fatalf("expected 6 rows and a trailer, got %d lines", len(lines))
	}
	a := map[string]any{"id": "F01ABC", "name": "a.pdf", "title": "Quarterly", "size": float64(600), "filetype": "pdf", "mimetype": "application/pdf"}
	b := map[string]any{"id": "F02DEF", "name": "b.txt", "size": float64(29)}
	want := []struct {
		ids   []any
		files any // nil means the key is absent
	}{
		{[]any{"F02DEF", "F01ABC"}, []any{b, a}}, // file_ids order, not the files array's
		{[]any{"F01ABC", "F03GONE"}, []any{a}},   // a deleted file stays in file_ids only
		{[]any{"F03GONE"}, nil},
		{[]any{}, nil},
		{[]any{}, nil}, // null from Slack
		{[]any{}, nil}, // key absent from Slack
	}
	for i, w := range want {
		row := parseJSON(t, lines[i])
		ids, isArray := row["file_ids"].([]any)
		if !isArray {
			t.Errorf("row %d: file_ids = %v (%T), want an array, never null or absent", i, row["file_ids"], row["file_ids"])
			continue
		}
		if len(ids) != len(w.ids) || (len(ids) > 0 && !reflect.DeepEqual(ids, w.ids)) {
			t.Errorf("row %d: file_ids = %v, want %v", i, ids, w.ids)
		}
		files, has := row["files"]
		if w.files == nil {
			if has {
				t.Errorf("row %d: files = %v, want the key absent", i, files)
			}
			continue
		}
		if !reflect.DeepEqual(files, w.files) {
			t.Errorf("row %d: files = %v, want %v", i, files, w.files)
		}
	}
}

// The active request's rows join against that response's files, not the
// first page's.
func TestDraftList_FileRowsFromTheActiveAnswer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("is_active") == "true" {
			_, _ = io.WriteString(w, `{"ok":true,"has_more":false,
"drafts":[{"id":"Dr02","last_updated_ts":"1.1","blocks":[],"destinations":[],"file_ids":["F02DEF"]}],
"files":[{"id":"F02DEF","name":"b.txt","size":29}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"has_more":true,
"drafts":[{"id":"Dr01","last_updated_ts":"1.1","blocks":[],"destinations":[],"file_ids":["F01ABC"]}],
"files":[{"id":"F01ABC","name":"a.pdf","size":600}]}`)
	})
	out, err := runWithMockSession(t, mux, "draft", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows and a trailer, got %s", out)
	}
	for i, name := range []string{"a.pdf", "b.txt"} {
		files, _ := parseJSON(t, lines[i])["files"].([]any)
		if len(files) != 1 || files[0].(map[string]any)["name"] != name {
			t.Errorf("row %d files = %v, want %s", i, files, name)
		}
	}
}
