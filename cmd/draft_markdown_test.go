package cmd_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/mdblocks"
	"github.com/tammersaleh/slack-cli/internal/output"
)

const sampleMarkdown = "# Status\n\nAll **green**.\n\n- one\n- two\n"

// expectedBlocks is what the converter produces for sampleMarkdown; the cmd
// tests assert the wire payload equals it byte for byte so a drift between
// the converter and the form field is visible.
func expectedBlocks(t *testing.T) string {
	t.Helper()
	b, err := mdblocks.Convert([]byte(sampleMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func captureCreate(t *testing.T, gotForm *url.Values) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr77777", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": json.RawMessage(gotForm.Get("blocks")), "destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	}
}

func TestDraftCreate_Markdown(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreate(t, &gotForm))

	out, err := runWithMockSessionStdin(t, sampleMarkdown, mux, "draft", "create", "C01ABC", "--markdown")
	if err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("blocks") != expectedBlocks(t) {
		t.Errorf("blocks field should be the converted Markdown\n got %s\nwant %s", gotForm.Get("blocks"), expectedBlocks(t))
	}
	if gotForm.Has("attachments") {
		t.Errorf("no attachments expected, got %q", gotForm.Get("attachments"))
	}
	if item := parseJSON(t, nonEmptyLines(out)[0]); item["id"] != "Dr77777" {
		t.Errorf("expected the created draft row, got %v", item)
	}
}

func TestDraftCreate_MarkdownWithTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.csv")
	if err := os.WriteFile(path, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreate(t, &gotForm))

	if _, err := runWithMockSessionStdin(t, sampleMarkdown, mux, "draft", "create", "C01ABC", "--markdown", "--table", path); err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("blocks") != expectedBlocks(t) {
		t.Errorf("blocks should be the converted Markdown, got %s", gotForm.Get("blocks"))
	}
	if !strings.Contains(gotForm.Get("attachments"), `"table"`) {
		t.Errorf("table attachment should ride along, got %q", gotForm.Get("attachments"))
	}
}

// --markdown with nothing on stdin and --table is an attachments-only draft,
// same as the JSON path.
func TestDraftCreate_MarkdownEmptyWithTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.csv")
	if err := os.WriteFile(path, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreate(t, &gotForm))
	if _, err := runWithMockSessionStdin(t, "  \n", mux, "draft", "create", "C01ABC", "--markdown", "--table", path); err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("blocks") != "[]" {
		t.Errorf("want blocks='[]', got %q", gotForm.Get("blocks"))
	}
}

func TestDraftCreate_MarkdownConversionErrorMakesNoCalls(t *testing.T) {
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected call to "+r.URL.Path, 500)
	})
	cases := map[string]struct {
		md     string
		detail string
	}{
		"symbolic mention": {"hi @alice\n", "line 1"},
		"horizontal rule":  {"a\n\n---\n", "line 3"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runWithMockSessionStdin(t, tc.md, h, "draft", "create", "C01ABC", "--markdown")
			var oe *output.Error
			if !errors.As(err, &oe) {
				t.Fatalf("want *output.Error, got %T: %v", err, err)
			}
			if oe.Err != "invalid_markdown" {
				t.Errorf("code: got %q want invalid_markdown (%s)", oe.Err, oe.Detail)
			}
			if !strings.Contains(oe.Detail, tc.detail) {
				t.Errorf("detail %q should mention %q", oe.Detail, tc.detail)
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("conversion failures must make no API calls, saw %d", calls.Load())
	}
}

func TestDraftCreate_PrintPayload(t *testing.T) {
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected call to "+r.URL.Path, 500)
	})
	path := filepath.Join(t.TempDir(), "t.csv")
	if err := os.WriteFile(path, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A name-shaped recipient would need channel resolution; preview must not
	// resolve it.
	out, err := runWithMockSessionStdin(t, sampleMarkdown, h, "draft", "create", "#general", "--markdown", "--print-payload", "--table", path)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Errorf("--print-payload must make no API calls, saw %d", calls.Load())
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("want one payload row plus _meta, got %d lines:\n%s", len(lines), out)
	}
	row := parseJSON(t, lines[0])
	blocks, _ := json.Marshal(row["blocks"])
	var want, got any
	_ = json.Unmarshal([]byte(expectedBlocks(t)), &want)
	_ = json.Unmarshal(blocks, &got)
	w, _ := json.Marshal(want)
	g, _ := json.Marshal(got)
	if string(w) != string(g) {
		t.Errorf("payload blocks\n got %s\nwant %s", g, w)
	}
	atts, _ := json.Marshal(row["attachments"])
	if !strings.Contains(string(atts), `"table"`) {
		t.Errorf("payload should include the --table attachment, got %s", atts)
	}
	if _, ok := parseJSON(t, lines[1])["_meta"]; !ok {
		t.Errorf("last line should be _meta, got %s", lines[1])
	}
}

func TestDraftCreate_PrintPayloadWithoutTableOmitsAttachments(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) })
	out, err := runWithMockSessionStdin(t, sampleMarkdown, h, "draft", "create", "C01ABC", "--markdown", "--print-payload")
	if err != nil {
		t.Fatal(err)
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	if _, has := row["attachments"]; has {
		t.Errorf("no attachments key expected without --table, got %v", row)
	}
}

func TestDraftCreate_PrintPayloadFlagErrors(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) })
	cases := map[string][]string{
		"without --markdown": {"draft", "create", "C01ABC", "--print-payload"},
		"with --at":          {"draft", "create", "C01ABC", "--markdown", "--print-payload", "--at", "2026-04-20"},
		"with --thread":      {"draft", "create", "C01ABC", "--markdown", "--print-payload", "--thread", "1713299000.123456"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runWithMockSessionStdin(t, sampleMarkdown, h, args...)
			var oe *output.Error
			if !errors.As(err, &oe) || oe.Err != "invalid_input" {
				t.Fatalf("want invalid_input, got %v", err)
			}
		})
	}
}

func TestDraftUpdate_MarkdownReplacesBlocksPreservesAttachments(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("old"),
		"attachments":     json.RawMessage(tableAttachmentJSON()),
		"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
	}
	var gotUpdate url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotUpdate, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": existing})
	})
	if _, err := runWithMockSessionStdin(t, sampleMarkdown, mux, "draft", "update", "Dr01234", "--markdown"); err != nil {
		t.Fatal(err)
	}
	if gotUpdate.Get("blocks") != expectedBlocks(t) {
		t.Errorf("blocks should be the converted Markdown, got %s", gotUpdate.Get("blocks"))
	}
	if !strings.Contains(gotUpdate.Get("attachments"), `"table"`) {
		t.Errorf("existing table attachment should be preserved, got %q", gotUpdate.Get("attachments"))
	}
}

func TestDraftUpdate_PrintPayloadMakesNoCalls(t *testing.T) {
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected call to "+r.URL.Path, 500)
	})
	out, err := runWithMockSessionStdin(t, sampleMarkdown, h, "draft", "update", "Dr01234", "--markdown", "--print-payload")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Errorf("--print-payload must not fetch the draft, saw %d calls", calls.Load())
	}
	if lines := nonEmptyLines(out); len(lines) != 2 {
		t.Errorf("want payload row plus _meta, got %d lines", len(lines))
	}

	_, err = runWithMockSessionStdin(t, sampleMarkdown, h, "draft", "update", "Dr01234", "--markdown", "--print-payload", "--clear-schedule")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_input" {
		t.Fatalf("--print-payload with a schedule flag: want invalid_input, got %v", err)
	}
}

// Without --markdown, Markdown on stdin is still rejected as not-JSON, with a
// hint pointing at the flag.
func TestDraftCreate_MarkdownWithoutFlagHints(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) })
	_, err := runWithMockSessionStdin(t, sampleMarkdown, h, "draft", "create", "C01ABC")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("want invalid_blocks, got %v", err)
	}
	if !strings.Contains(oe.Detail+oe.Hint, "--markdown") {
		t.Errorf("error should mention --markdown, got detail=%q hint=%q", oe.Detail, oe.Hint)
	}
}

func TestDraftCreate_PrintPayloadFlagErrors_MoreFlags(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) })
	cases := map[string][]string{
		"with --date-scheduled":        {"draft", "create", "C01ABC", "--markdown", "--print-payload", "--date-scheduled", "1713300000"},
		"with --thread --broadcast":    {"draft", "create", "C01ABC", "--markdown", "--print-payload", "--thread", "1713299000.123456", "--broadcast"},
		"update without --markdown":    {"draft", "update", "Dr01234", "--print-payload"},
		"update with --at":             {"draft", "update", "Dr01234", "--markdown", "--print-payload", "--at", "2026-04-20"},
		"update with --date-scheduled": {"draft", "update", "Dr01234", "--markdown", "--print-payload", "--date-scheduled", "1713300000"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runWithMockSessionStdin(t, sampleMarkdown, h, args...)
			var oe *output.Error
			if !errors.As(err, &oe) || oe.Err != "invalid_input" {
				t.Fatalf("want invalid_input, got %v", err)
			}
		})
	}
}

func TestDraftUpdate_MarkdownConversionErrorMakesNoCalls(t *testing.T) {
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected call to "+r.URL.Path, 500)
	})
	_, err := runWithMockSessionStdin(t, "a\n\n---\n", h, "draft", "update", "Dr01234", "--markdown")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_markdown" {
		t.Fatalf("want invalid_markdown, got %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("a conversion failure must not fetch or update the draft, saw %d calls", calls.Load())
	}
}

func TestDraftUpdate_MarkdownWithTableReplacesBoth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.csv")
	if err := os.WriteFile(path, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("old"),
		"attachments":     json.RawMessage(`[{"blocks":[{"type":"table","rows":[[{"type":"raw_text","text":"OLD"}]]}]}]`),
		"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
	}
	var gotUpdate url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotUpdate, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": existing})
	})
	if _, err := runWithMockSessionStdin(t, sampleMarkdown, mux, "draft", "update", "Dr01234", "--markdown", "--table", path); err != nil {
		t.Fatal(err)
	}
	if gotUpdate.Get("blocks") != expectedBlocks(t) {
		t.Errorf("blocks should be the converted Markdown, got %s", gotUpdate.Get("blocks"))
	}
	atts := gotUpdate.Get("attachments")
	if strings.Contains(atts, "OLD") || !strings.Contains(atts, `"table"`) {
		t.Errorf("--table should replace the old attachment with the new table, got %q", atts)
	}
}
