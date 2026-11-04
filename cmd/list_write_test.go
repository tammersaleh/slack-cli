package cmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// listWriteStore is a stateful mock of the lists.* write endpoints. It holds
// one list as a decoded files.info object, applies each write to it the way
// Slack does (create assigns ids, update patches fields, columns append or
// patch or vanish), records every form it saw in order, and answers every
// mutating method with {"ok":true,"list":<the list>} - except delete, which
// is a bare {ok}. Failures are injected per method, optionally per call
// ordinal ("lists.columns.create#2" fails the second column create only).
type listWriteStore struct {
	t       *testing.T
	file    map[string]any
	nextCol int
	calls   []storeCall
	fail    map[string]string
	bareOK  map[string]bool // method (or method#N) answers {"ok":true} with no list
	count   map[string]int
}

type storeCall struct {
	method string
	form   url.Values
}

func newListWriteStore(t *testing.T) *listWriteStore {
	t.Helper()
	return &listWriteStore{t: t, nextCol: 2, fail: map[string]string{}, bareOK: map[string]bool{}, count: map[string]int{}}
}

// seed loads the shared nine-column fixture as the existing list.
func (s *listWriteStore) seed() *listWriteStore {
	var m map[string]any
	if err := json.Unmarshal([]byte(listSchemaJSON), &m); err != nil {
		s.t.Fatal(err)
	}
	s.file = m
	s.nextCol = 20
	return s
}

func (s *listWriteStore) schema() []any {
	md, _ := s.file["list_metadata"].(map[string]any)
	cols, _ := md["schema"].([]any)
	return cols
}

func (s *listWriteStore) setSchema(cols []any) {
	md, _ := s.file["list_metadata"].(map[string]any)
	md["schema"] = cols
}

func (s *listWriteStore) methods() []string {
	out := make([]string, len(s.calls))
	for i, c := range s.calls {
		out[i] = c.method
	}
	return out
}

func (s *listWriteStore) writeList(method string, w http.ResponseWriter) {
	if s.bareOK[method] || s.bareOK[fmt.Sprintf("%s#%d", method, s.count[method])] {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	body, err := json.Marshal(map[string]any{"ok": true, "list": s.file})
	if err != nil {
		s.t.Fatal(err)
	}
	_, _ = w.Write(body)
}

// handle records the call and returns false after writing the injected
// failure, if any.
func (s *listWriteStore) handle(method string, w http.ResponseWriter, r *http.Request) bool {
	_ = r.ParseForm()
	s.calls = append(s.calls, storeCall{method: method, form: r.PostForm})
	s.count[method]++
	code, ok := s.fail[method]
	if !ok {
		code, ok = s.fail[fmt.Sprintf("%s#%d", method, s.count[method])]
	}
	if ok {
		_, _ = fmt.Fprintf(w, `{"ok":false,"error":%q,"response_metadata":{"messages":["[ERROR] detail for %s"]}}`, code, method)
		return false
	}
	return true
}

func (s *listWriteStore) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files.info", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("files.info", w, r) {
			return
		}
		if s.file == nil || r.FormValue("file") != s.file["id"] {
			_, _ = w.Write([]byte(`{"ok":false,"error":"file_not_found"}`))
			return
		}
		body, _ := json.Marshal(map[string]any{"ok": true, "file": s.file})
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/api/lists.create", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("lists.create", w, r) {
			return
		}
		s.file = map[string]any{
			"id": "F01ABC", "title": r.FormValue("name"), "name": "list", "filetype": "list",
			"user": "U01XYZ", "created": 1709251200, "updated": 1709251200,
			"permalink": "https://acme.slack.com/lists/T01ABC/F01ABC",
			"list_metadata": map[string]any{
				"description": r.FormValue("description"), "icon": "",
				"schema": []any{map[string]any{"id": "Col01AAA", "name": "Name", "key": "name", "type": "text", "is_primary_column": true, "options": map[string]any{"format": "text"}}},
			},
			"list_limits": map[string]any{"row_count": 0, "archived_row_count": 0},
		}
		s.writeList("lists.create", w)
	})
	mux.HandleFunc("/api/lists.update", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("lists.update", w, r) {
			return
		}
		if r.FormValue("id") != s.file["id"] {
			_, _ = w.Write([]byte(`{"ok":false,"error":"list_not_found"}`))
			return
		}
		md, _ := s.file["list_metadata"].(map[string]any)
		if v, ok := r.PostForm["name"]; ok {
			s.file["title"] = v[0]
		}
		if v, ok := r.PostForm["description"]; ok {
			md["description"] = v[0]
		}
		if v, ok := r.PostForm["icon"]; ok {
			// Slack stores the icon with colons whether or not the caller sent them.
			md["icon"] = ":" + strings.Trim(v[0], ":") + ":"
		}
		s.writeList("lists.update", w)
	})
	mux.HandleFunc("/api/lists.columns.create", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("lists.columns.create", w, r) {
			return
		}
		if r.FormValue("list_id") != s.file["id"] {
			_, _ = w.Write([]byte(`{"ok":false,"error":"list_not_found"}`))
			return
		}
		id := fmt.Sprintf("Col%02dNEW", s.nextCol)
		s.nextCol++
		col := map[string]any{"id": id, "key": "K" + id, "name": r.FormValue("name"), "type": r.FormValue("type"), "is_primary_column": false, "options": nil}
		if opts, ok := r.PostForm["options"]; ok {
			var o any
			if err := json.Unmarshal([]byte(opts[0]), &o); err != nil {
				_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
				return
			}
			col["options"] = o
		}
		s.setSchema(append(s.schema(), col))
		s.writeList("lists.columns.create", w)
	})
	mux.HandleFunc("/api/lists.columns.update", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("lists.columns.update", w, r) {
			return
		}
		for _, c := range s.schema() {
			col := c.(map[string]any)
			if col["id"] != r.FormValue("id") {
				continue
			}
			if v, ok := r.PostForm["name"]; ok {
				col["name"] = v[0]
			}
			if v, ok := r.PostForm["options"]; ok {
				var o any
				if err := json.Unmarshal([]byte(v[0]), &o); err != nil {
					_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
					return
				}
				col["options"] = o
			}
			s.writeList("lists.columns.update", w)
			return
		}
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
	})
	mux.HandleFunc("/api/lists.columns.delete", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("lists.columns.delete", w, r) {
			return
		}
		kept := []any{}
		found := false
		for _, c := range s.schema() {
			if c.(map[string]any)["id"] == r.FormValue("id") {
				found = true
				continue
			}
			kept = append(kept, c)
		}
		if !found {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
			return
		}
		s.setSchema(kept)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return mux
}

func rowsOf(t *testing.T, out string) []map[string]any {
	t.Helper()
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		t.Fatal("no output")
	}
	metaOf(t, out)
	rows := make([]map[string]any, 0, len(lines)-1)
	for _, l := range lines[:len(lines)-1] {
		rows = append(rows, parseJSON(t, l))
	}
	return rows
}

func formOnly(t *testing.T, form url.Values, want map[string]string) {
	t.Helper()
	delete(form, "token")
	got := map[string]string{}
	for k, v := range form {
		if len(v) != 1 {
			t.Errorf("form key %q sent %d times", k, len(v))
		}
		got[k] = v[0]
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("form = %v\nwant   %v", got, want)
	}
}

// --- list create ---

func TestListCreate_FullFlow(t *testing.T) {
	s := newListWriteStore(t)
	out, err := runWithMockSession(t, s.mux(), "list", "create", "Launch tasks",
		"--description", "Everything before launch", "--icon", ":rocket:",
		"--column", "Owner:user", "--column", "Status:select:To do,Done")
	if err != nil {
		t.Fatal(err)
	}

	wantMethods := []string{"lists.create", "lists.columns.create", "lists.columns.create", "lists.update"}
	if fmt.Sprint(s.methods()) != fmt.Sprint(wantMethods) {
		t.Fatalf("calls = %v, want %v", s.methods(), wantMethods)
	}
	formOnly(t, s.calls[0].form, map[string]string{"name": "Launch tasks", "description": "Everything before launch"})
	// A plain column sends no options key at all.
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "type": "user", "name": "Owner"})
	formOnly(t, s.calls[2].form, map[string]string{"list_id": "F01ABC", "type": "select", "name": "Status",
		"options": `{"choices":[{"value":"to_do","label":"To do","color":"blue"},{"value":"done","label":"Done","color":"green"}],"format":"single_select"}`})
	formOnly(t, s.calls[3].form, map[string]string{"id": "F01ABC", "icon": "rocket"})

	rows := rowsOf(t, out)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1:\n%s", len(rows), out)
	}
	row := rows[0]
	// The row is the final server state (icon stored with colons by the
	// server, title from lists.create), not an echo of the flags.
	if row["id"] != "F01ABC" || row["title"] != "Launch tasks" || row["icon"] != ":rocket:" || row["description"] != "Everything before launch" {
		t.Errorf("row = %v", row)
	}
	if _, ok := row["limits"]; !ok {
		t.Error("row lacks limits (should match list info's shape)")
	}
	if _, ok := row["error"]; ok {
		t.Error("success row carries an error key")
	}
}

func TestListCreate_NoColumnsNoIcon(t *testing.T) {
	s := newListWriteStore(t)
	out, err := runWithMockSession(t, s.mux(), "list", "create", "Bare")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(s.methods()) != "[lists.create]" {
		t.Errorf("calls = %v", s.methods())
	}
	formOnly(t, s.calls[0].form, map[string]string{"name": "Bare", "description": ""})
	if rows := rowsOf(t, out); rows[0]["title"] != "Bare" || rows[0]["icon"] != "" {
		t.Errorf("row = %v", rows[0])
	}
}

func TestListCreate_LocalValidationMakesNoCalls(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"blank name", []string{"  "}},
		{"bad spec", []string{"X", "--column", "Owner"}},
		{"unknown type", []string{"X", "--column", "Owner:person"}},
		{"choices on text", []string{"X", "--column", "Owner:text:a,b"}},
		{"duplicate label", []string{"X", "--column", "S:select:a,a"}},
		{"slug collision", []string{"X", "--column", "S:select:To do,to-do"}},
		{"blank icon", []string{"X", "--icon", ""}},
		{"icon with space", []string{"X", "--icon", "two words"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newListWriteStore(t)
			out, err := runWithMockSession(t, s.mux(), append([]string{"list", "create"}, tt.args...)...)
			assertOutputErrorCode(t, err, "invalid_input")
			if out != "" {
				t.Errorf("stdout = %q", out)
			}
			if len(s.calls) != 0 {
				t.Errorf("calls = %v, want none", s.methods())
			}
		})
	}
}

func TestListCreate_CreateFailureIsFatal(t *testing.T) {
	s := newListWriteStore(t)
	s.fail["lists.create"] = "invalid_arguments"
	out, err := runWithMockSession(t, s.mux(), "list", "create", "X", "--column", "Owner:user")
	assertOutputErrorCode(t, err, "invalid_arguments")
	var oe *output.Error
	_ = errors.As(err, &oe)
	// Slack's response_metadata.messages are the only documentation the
	// lists API has; they must reach the caller.
	if !strings.Contains(oe.Detail, "[ERROR] detail for lists.create") {
		t.Errorf("detail = %q, want the response_metadata message", oe.Detail)
	}
	if out != "" {
		t.Errorf("stdout = %q", out)
	}
	if fmt.Sprint(s.methods()) != "[lists.create]" {
		t.Errorf("calls = %v", s.methods())
	}
}

func TestListCreate_PartialColumnFailure(t *testing.T) {
	s := newListWriteStore(t)
	s.fail["lists.columns.create#2"] = "invalid_arguments"
	out, err := runWithMockSession(t, s.mux(), "list", "create", "X", "--icon", "rocket",
		"--column", "Owner:user", "--column", "Status:select:a", "--column", "Due:date")
	assertExitError(t, err)

	// Stops at the failure: no third column, no icon, and never a delete.
	wantMethods := []string{"lists.create", "lists.columns.create", "lists.columns.create"}
	if fmt.Sprint(s.methods()) != fmt.Sprint(wantMethods) {
		t.Errorf("calls = %v, want %v", s.methods(), wantMethods)
	}

	rows := rowsOf(t, out)
	if len(rows) != 1 {
		t.Fatalf("rows = %d:\n%s", len(rows), out)
	}
	row := rows[0]
	if row["id"] != "F01ABC" || row["error"] != "invalid_arguments" || row["failed_step"] != "column" || row["failed_column"] != "Status" {
		t.Errorf("partial row = %v", row)
	}
	// The failed spec comes first, the untouched one follows, and the icon
	// step is still pending.
	wantHint := "The list was created. Finish it with: slack list column add F01ABC 'Status:select:a' 'Due:date' ; slack list update F01ABC --icon rocket"
	if row["hint"] != wantHint {
		t.Errorf("hint = %q\nwant   %q", row["hint"], wantHint)
	}
	if detail, _ := row["detail"].(string); !strings.Contains(detail, "[ERROR] detail for lists.columns.create") {
		t.Errorf("detail = %q", detail)
	}
	if meta := metaOf(t, out); meta["has_more"] != false {
		t.Errorf("trailer = %v, want has_more:false (the row is complete, the list is partial)", meta)
	}
}

func TestListCreate_PartialIconFailure(t *testing.T) {
	s := newListWriteStore(t)
	s.fail["lists.update"] = "invalid_arguments"
	out, err := runWithMockSession(t, s.mux(), "list", "create", "X", "--icon", "rocket", "--column", "Owner:user")
	assertExitError(t, err)
	row := rowsOf(t, out)[0]
	if row["failed_step"] != "icon" || row["id"] != "F01ABC" {
		t.Errorf("partial row = %v", row)
	}
	if _, ok := row["failed_column"]; ok {
		t.Error("failed_column set on an icon failure")
	}
	if want := "The list was created. Finish it with: slack list update F01ABC --icon rocket"; row["hint"] != want {
		t.Errorf("hint = %q, want %q", row["hint"], want)
	}
}

func TestListCreate_FirstColumnFailureListsEverySpec(t *testing.T) {
	s := newListWriteStore(t)
	s.fail["lists.columns.create#1"] = "invalid_arguments"
	out, err := runWithMockSession(t, s.mux(), "list", "create", "X", "--column", "Owner:user", "--column", "Due:date")
	assertExitError(t, err)
	row := rowsOf(t, out)[0]
	if row["failed_column"] != "Owner" {
		t.Errorf("row = %v", row)
	}
	if want := "The list was created. Finish it with: slack list column add F01ABC 'Owner:user' 'Due:date'"; row["hint"] != want {
		t.Errorf("hint = %q, want %q", row["hint"], want)
	}
	if fmt.Sprint(s.methods()) != "[lists.create lists.columns.create]" {
		t.Errorf("calls = %v", s.methods())
	}
}

// A write that answers ok without a list object is still a failure after
// the list exists, so it must be a partial row naming the id, not a stderr
// error that loses it.
func TestListCreate_UnreadableResponseAfterCreateIsPartial(t *testing.T) {
	tests := []struct {
		name   string
		bare   string
		step   string
		column any
	}{
		{"column create", "lists.columns.create#2", "column", "Due"},
		{"icon update", "lists.update", "icon", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newListWriteStore(t)
			s.bareOK[tt.bare] = true
			out, err := runWithMockSession(t, s.mux(), "list", "create", "X", "--icon", "rocket", "--column", "Owner:user", "--column", "Due:date")
			assertExitError(t, err)
			rows := rowsOf(t, out)
			if len(rows) != 1 {
				t.Fatalf("rows = %d:\n%s", len(rows), out)
			}
			row := rows[0]
			if row["id"] != "F01ABC" || row["error"] != "parse_error" || row["failed_step"] != tt.step || row["failed_column"] != tt.column {
				t.Errorf("row = %v", row)
			}
			if _, ok := row["hint"]; !ok {
				t.Error("no recovery hint")
			}
		})
	}
}

func TestListCreate_AuthFailureAfterCreateKeepsAuthExit(t *testing.T) {
	s := newListWriteStore(t)
	s.fail["lists.columns.create"] = "invalid_auth"
	_, err := runWithMockSession(t, s.mux(), "list", "create", "X", "--column", "Owner:user")
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != output.ExitAuth {
		t.Fatalf("err = %T %v, want ExitError with code %d", err, err, output.ExitAuth)
	}
}

// --- list update ---

func TestListUpdate(t *testing.T) {
	s := newListWriteStore(t).seed()
	out, err := runWithMockSession(t, s.mux(), "list", "update", "https://acme.slack.com/lists/T01ABC/F01ABC", "--name", "Renamed", "--icon", "wrench")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(s.methods()) != "[lists.update]" {
		t.Fatalf("calls = %v (no files.info needed: lists.update returns the list)", s.methods())
	}
	formOnly(t, s.calls[0].form, map[string]string{"id": "F01ABC", "name": "Renamed", "icon": "wrench"})
	row := rowsOf(t, out)[0]
	if row["title"] != "Renamed" || row["icon"] != ":wrench:" || row["description"] != "Everything before launch" {
		t.Errorf("row = %v", row)
	}
}

func TestListUpdate_ClearDescription(t *testing.T) {
	s := newListWriteStore(t).seed()
	out, err := runWithMockSession(t, s.mux(), "list", "update", "F01ABC", "--description", "")
	if err != nil {
		t.Fatal(err)
	}
	formOnly(t, s.calls[0].form, map[string]string{"id": "F01ABC", "description": ""})
	if row := rowsOf(t, out)[0]; row["description"] != "" {
		t.Errorf("row = %v", row)
	}
}

func TestListUpdate_LocalValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no flags", []string{"F01ABC"}},
		{"blank name", []string{"F01ABC", "--name", " "}},
		{"blank icon", []string{"F01ABC", "--icon", ""}},
		{"wrong url kind", []string{"https://acme.slack.com/archives/C01ABC", "--name", "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newListWriteStore(t).seed()
			out, err := runWithMockSession(t, s.mux(), append([]string{"list", "update"}, tt.args...)...)
			assertOutputErrorCode(t, err, "invalid_input")
			if out != "" || len(s.calls) != 0 {
				t.Errorf("stdout = %q, calls = %v", out, s.methods())
			}
		})
	}
}

func TestListUpdate_SlackError(t *testing.T) {
	s := newListWriteStore(t).seed()
	out, err := runWithMockSession(t, s.mux(), "list", "update", "F09ZZZ", "--name", "x")
	assertOutputErrorCode(t, err, "list_not_found")
	if out != "" {
		t.Errorf("stdout = %q", out)
	}
}

// --- list column add ---

func TestListColumnAdd(t *testing.T) {
	s := newListWriteStore(t).seed()
	out, err := runWithMockSession(t, s.mux(), "list", "column", "add", "F01ABC", "Tags:multi_select:a,b", "Notes:rich_text")
	if err != nil {
		t.Fatal(err)
	}
	wantMethods := []string{"files.info", "lists.columns.create", "lists.columns.create"}
	if fmt.Sprint(s.methods()) != fmt.Sprint(wantMethods) {
		t.Fatalf("calls = %v", s.methods())
	}
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "type": "multi_select", "name": "Tags",
		"options": `{"choices":[{"value":"a","label":"a","color":"blue"},{"value":"b","label":"b","color":"green"}],"format":"multi_select"}`})
	formOnly(t, s.calls[2].form, map[string]string{"list_id": "F01ABC", "type": "rich_text", "name": "Notes"})

	rows := rowsOf(t, out)
	if len(rows) != 2 {
		t.Fatalf("rows = %d:\n%s", len(rows), out)
	}
	// Each row is the new column as the server returned it (server-assigned
	// id and key), in list schema's shape plus list_id.
	if rows[0]["list_id"] != "F01ABC" || rows[0]["column_id"] != "Col20NEW" || rows[0]["key"] != "KCol20NEW" || rows[0]["name"] != "Tags" || rows[0]["type"] != "multi_select" || rows[0]["format"] != "multi_select" {
		t.Errorf("row 0 = %v", rows[0])
	}
	if choices, _ := rows[0]["choices"].([]any); len(choices) != 2 {
		t.Errorf("row 0 choices = %v", rows[0]["choices"])
	}
	if rows[1]["column_id"] != "Col21NEW" || rows[1]["name"] != "Notes" {
		t.Errorf("row 1 = %v", rows[1])
	}
	if _, ok := rows[1]["choices"]; ok {
		t.Error("plain column row carries choices")
	}
}

func TestListColumnAdd_PartialFailure(t *testing.T) {
	s := newListWriteStore(t).seed()
	s.fail["lists.columns.create#2"] = "invalid_arguments"
	out, err := runWithMockSession(t, s.mux(), "list", "column", "add", "F01ABC", "A:text", "B:text", "C:text")
	assertExitError(t, err)
	if s.count["lists.columns.create"] != 2 {
		t.Errorf("columns.create called %d times, want 2 (stop at the failure)", s.count["lists.columns.create"])
	}
	rows := rowsOf(t, out)
	if len(rows) != 2 {
		t.Fatalf("rows = %d:\n%s", len(rows), out)
	}
	if rows[0]["name"] != "A" || rows[0]["error"] != nil {
		t.Errorf("row 0 = %v", rows[0])
	}
	if rows[1]["error"] != "invalid_arguments" || rows[1]["name"] != "B" || rows[1]["list_id"] != "F01ABC" {
		t.Errorf("row 1 = %v", rows[1])
	}
	if _, ok := rows[1]["column_id"]; ok {
		t.Error("failed row claims a column_id")
	}
}

func TestListColumnAdd_UnreadableResponseIsPartialRow(t *testing.T) {
	s := newListWriteStore(t).seed()
	s.bareOK["lists.columns.create#2"] = true
	out, err := runWithMockSession(t, s.mux(), "list", "column", "add", "F01ABC", "A:text", "B:text", "C:text")
	assertExitError(t, err)
	rows := rowsOf(t, out) // also asserts the trailer closed the stream
	if len(rows) != 2 || rows[0]["name"] != "A" || rows[1]["error"] != "parse_error" || rows[1]["name"] != "B" {
		t.Errorf("rows = %v", rows)
	}
	if s.count["lists.columns.create"] != 2 {
		t.Errorf("columns.create called %d times", s.count["lists.columns.create"])
	}
}

// When the response holds no single unseen column id (here another column
// of the same name and type already existed and the mock returns no new
// one), the command must not guess by name - that could report an older
// column as the one just created.
func TestListColumnAdd_DoesNotGuessByName(t *testing.T) {
	s := newListWriteStore(t).seed()
	// Make the mock "create" nothing: answer with the unchanged list.
	mux := s.mux()
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/api/lists.columns.create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.calls = append(s.calls, storeCall{method: "lists.columns.create", form: r.PostForm})
		body, _ := json.Marshal(map[string]any{"ok": true, "list": s.file})
		_, _ = w.Write(body)
	})
	mux2.Handle("/", mux)
	out, err := runWithMockSession(t, mux2, "list", "column", "add", "F01ABC", "Status:select:a")
	assertExitError(t, err)
	rows := rowsOf(t, out)
	if len(rows) != 1 || rows[0]["error"] != "parse_error" || rows[0]["name"] != "Status" {
		t.Errorf("rows = %v", rows)
	}
	if _, ok := rows[0]["column_id"]; ok {
		t.Errorf("row names a column id it cannot know: %v", rows[0])
	}
}

func TestListColumnAdd_LocalValidationMakesNoCalls(t *testing.T) {
	s := newListWriteStore(t).seed()
	out, err := runWithMockSession(t, s.mux(), "list", "column", "add", "F01ABC", "A:text", "B:nope")
	assertOutputErrorCode(t, err, "invalid_input")
	if out != "" || len(s.calls) != 0 {
		t.Errorf("stdout = %q, calls = %v (specs must all parse before files.info)", out, s.methods())
	}
}

// --- list column rename ---

func TestListColumnRename(t *testing.T) {
	for _, ref := range []string{"Col01CCC", "Col01BBB", "status", "STATUS"} {
		t.Run(ref, func(t *testing.T) {
			s := newListWriteStore(t).seed()
			out, err := runWithMockSession(t, s.mux(), "list", "column", "rename", "F01ABC", ref, "State")
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(s.methods()) != "[files.info lists.columns.update]" {
				t.Fatalf("calls = %v", s.methods())
			}
			// id is the column id even when the caller gave the key; no options key.
			formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "id": "Col01CCC", "name": "State"})
			row := rowsOf(t, out)[0]
			if row["list_id"] != "F01ABC" || row["column_id"] != "Col01CCC" || row["name"] != "State" || row["key"] != "Col01BBB" || row["format"] != "single_select" {
				t.Errorf("row = %v", row)
			}
		})
	}
}

func TestListColumnRename_ResolutionFailuresMakeNoWrites(t *testing.T) {
	tests := []struct {
		ref, code string
	}{
		{"Name", "ambiguous_column"},
		{"Owner2", "column_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			s := newListWriteStore(t).seed()
			out, err := runWithMockSession(t, s.mux(), "list", "column", "rename", "F01ABC", tt.ref, "X")
			assertOutputErrorCode(t, err, tt.code)
			var oe *output.Error
			_ = errors.As(err, &oe)
			if oe.Input != tt.ref {
				t.Errorf("Input = %q", oe.Input)
			}
			if out != "" || fmt.Sprint(s.methods()) != "[files.info]" {
				t.Errorf("stdout = %q, calls = %v", out, s.methods())
			}
		})
	}
}

func TestListColumnRename_BlankNameMakesNoCalls(t *testing.T) {
	s := newListWriteStore(t).seed()
	_, err := runWithMockSession(t, s.mux(), "list", "column", "rename", "F01ABC", "Status", " ")
	assertOutputErrorCode(t, err, "invalid_input")
	if len(s.calls) != 0 {
		t.Errorf("calls = %v", s.methods())
	}
}

// --- list column set-choices ---

func TestListColumnSetChoices(t *testing.T) {
	s := newListWriteStore(t).seed()
	out, err := runWithMockSession(t, s.mux(), "list", "column", "set-choices", "F01ABC", "Status", "Done", "Blocked", "To do")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(s.methods()) != "[files.info lists.columns.update]" {
		t.Fatalf("calls = %v", s.methods())
	}
	// done and todo keep their values and colors; Blocked is new and takes
	// the palette color for position 1; default_value survives because todo
	// survives; no name key is sent.
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "id": "Col01CCC",
		"options": `{"choices":[{"value":"done","label":"Done","color":"green"},{"value":"blocked","label":"Blocked","color":"green"},{"value":"todo","label":"To do","color":"gray"}],"default_value":"todo","format":"single_select"}`})
	row := rowsOf(t, out)[0]
	choices, _ := row["choices"].([]any)
	if len(choices) != 3 || row["column_id"] != "Col01CCC" || row["name"] != "Status" {
		t.Errorf("row = %v", row)
	}
}

func TestListColumnSetChoices_DropsStaleDefault(t *testing.T) {
	s := newListWriteStore(t).seed()
	_, err := runWithMockSession(t, s.mux(), "list", "column", "set-choices", "F01ABC", "Status", "Done")
	if err != nil {
		t.Fatal(err)
	}
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "id": "Col01CCC",
		"options": `{"choices":[{"value":"done","label":"Done","color":"green"}],"format":"single_select"}`})
}

func TestListColumnSetChoices_LocalFailuresMakeNoWrites(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code string
	}{
		{"not a select column", []string{"Owner", "a"}, "invalid_input"},
		{"duplicate label", []string{"Status", "Done", "Done"}, "invalid_input"},
		{"slug collides with preserved value", []string{"Status", "Done", "DONE"}, "invalid_input"},
		{"unknown column", []string{"Nope", "a"}, "column_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newListWriteStore(t).seed()
			out, err := runWithMockSession(t, s.mux(), append([]string{"list", "column", "set-choices", "F01ABC"}, tt.args...)...)
			assertOutputErrorCode(t, err, tt.code)
			if out != "" || fmt.Sprint(s.methods()) != "[files.info]" {
				t.Errorf("stdout = %q, calls = %v", out, s.methods())
			}
		})
	}
}

// --- list column delete ---

func TestListColumnDelete(t *testing.T) {
	s := newListWriteStore(t).seed()
	out, err := runWithMockSession(t, s.mux(), "list", "column", "delete", "F01ABC", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(s.methods()) != "[files.info lists.columns.delete]" {
		t.Fatalf("calls = %v", s.methods())
	}
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "id": "Col01DDD"})
	row := rowsOf(t, out)[0]
	if row["list_id"] != "F01ABC" || row["column_id"] != "Col01DDD" || row["key"] != "Col01DDK" || row["name"] != "Owner" || row["status"] != "deleted" {
		t.Errorf("row = %v", row)
	}
	if len(s.schema()) != 8 {
		t.Errorf("mock schema has %d columns, want 8", len(s.schema()))
	}
}

func TestListColumnDelete_SlackErrorIsFatal(t *testing.T) {
	s := newListWriteStore(t).seed()
	s.fail["lists.columns.delete"] = "invalid_arguments"
	out, err := runWithMockSession(t, s.mux(), "list", "column", "delete", "F01ABC", "Owner")
	assertOutputErrorCode(t, err, "invalid_arguments")
	if out != "" {
		t.Errorf("stdout = %q", out)
	}
}

// --- shared ---

func TestListColumn_WrongURLKindMakesNoCalls(t *testing.T) {
	const msgURL = "https://acme.slack.com/archives/C01ABC/p1709251200000100"
	for _, args := range [][]string{
		{"list", "column", "add", msgURL, "A:text"},
		{"list", "column", "rename", msgURL, "Status", "X"},
		{"list", "column", "set-choices", msgURL, "Status", "a"},
		{"list", "column", "delete", msgURL, "Status"},
	} {
		t.Run(args[2], func(t *testing.T) {
			s := newListWriteStore(t).seed()
			out, err := runWithMockSession(t, s.mux(), args...)
			assertOutputErrorCode(t, err, "invalid_input")
			if out != "" || len(s.calls) != 0 {
				t.Errorf("stdout = %q, calls = %v", out, s.methods())
			}
		})
	}
}

func TestListWrites_RequireSessionToken(t *testing.T) {
	for _, args := range [][]string{
		{"list", "create", "X"},
		{"list", "update", "F01ABC", "--name", "x"},
		{"list", "column", "add", "F01ABC", "A:text"},
		{"list", "column", "delete", "F01ABC", "A"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newListWriteStore(t).seed()
			_, err := runWithMock(t, s.mux(), args...)
			assertOutputErrorCode(t, err, "session_token_required")
			if len(s.calls) != 0 {
				t.Errorf("calls = %v", s.methods())
			}
		})
	}
}
