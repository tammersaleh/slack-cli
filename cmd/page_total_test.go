package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The trailer of every page-number command carries Slack's own count of the
// result set, so zero rows beside a nonzero total cannot read as "nothing
// matched".
func TestPageTotal_InTrailer(t *testing.T) {
	filesList := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"files":  []map[string]any{{"id": "F01ABC", "name": "file.pdf"}},
			"paging": map[string]any{"count": 1, "total": 41, "page": 1, "pages": 41},
		})
	}
	searchFiles := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(searchFilesResponse(
			[]map[string]any{fileMatch("F01ABC", "a.pdf", "A", "pdf", "https://x/a")}, 1, 41, 41))
	}
	searchMessages := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(searchResponse(
			[]map[string]any{searchMatch("1.1", "msg", "U01XYZ", "alice", "C01ABC", "general", "https://x/1")}, 1, 41, 41))
	}

	tests := []struct {
		name     string
		endpoint string
		handler  http.HandlerFunc
		session  bool
		args     []string
	}{
		{"search messages", "search.messages", searchMessages, false, []string{"search", "messages", "deploy", "--limit", "1"}},
		{"search files", "search.files", searchFiles, false, []string{"search", "files", "report", "--limit", "1"}},
		{"file list", "files.list", filesList, false, []string{"file", "list", "--limit", "1"}},
		{"list find", "search.files", searchFiles, true, []string{"list", "find", "Launch", "--limit", "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/"+tt.endpoint, tt.handler)
			t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
			run := runWithMock
			if tt.session {
				run = runWithMockSession
			}
			out, err := run(t, mux, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			meta := metaOf(t, out)
			if meta["total"] != float64(41) {
				t.Errorf("_meta.total = %v, want 41 (trailer %v)", meta["total"], meta)
			}
			if meta["has_more"] != true || meta["next_cursor"] != "2" {
				t.Errorf("trailer = %v, want has_more=true next_cursor=2", meta)
			}
		})
	}
}

// An empty result set says so explicitly: total is emitted as 0, not omitted.
func TestPageTotal_ZeroIsEmitted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(searchResponse(nil, 1, 0, 0))
	})
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "messages", "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if got := nonEmptyLines(out); len(got) != 1 || got[0] != `{"_meta":{"has_more":false,"total":0}}` {
		t.Errorf("output = %q, want the trailer with total 0", got)
	}
}

// The case the field exists for: Slack answers a page with no matches while
// reporting a nonzero total. The rows are missing and the trailer shows it.
func TestPageTotal_NonzeroBesideZeroRows(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(searchResponse(nil, 1, 1, 1357))
	})
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "messages", "deploy", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if got := nonEmptyLines(out); len(got) != 1 || got[0] != `{"_meta":{"has_more":false,"total":1357}}` {
		t.Errorf("output = %q, want the trailer with total 1357", got)
	}
}

// The total rides the failure trailer too, and it is the first page's: the
// number the walk set out to fetch, not whatever a later page reported.
func TestPageTotal_SurvivesMidWalkFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("page") {
		case "", "1":
			_ = json.NewEncoder(w).Encode(searchResponse(
				[]map[string]any{searchMatch("1.1", "msg1", "U01XYZ", "alice", "C01ABC", "general", "https://x/1")}, 1, 3, 3))
		case "2":
			_ = json.NewEncoder(w).Encode(searchResponse(
				[]map[string]any{searchMatch("2.2", "msg2", "U01XYZ", "alice", "C01ABC", "general", "https://x/2")}, 2, 4, 4))
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "fatal_error"})
		}
	})
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "messages", "deploy", "--all", "--limit", "1")
	if err == nil {
		t.Fatal("expected the page 3 failure to surface")
	}
	if rows := len(nonEmptyLines(out)) - 1; rows != 2 {
		t.Fatalf("expected 2 rows before the failure, got %d:\n%s", rows, out)
	}
	meta := metaOf(t, out)
	if meta["error"] != "fatal_error" || meta["next_cursor"] != "3" {
		t.Errorf("trailer = %v, want error=fatal_error next_cursor=3", meta)
	}
	if meta["total"] != float64(3) {
		t.Errorf("_meta.total = %v, want 3 (the first page's)", meta["total"])
	}
}

// No page arrived, so there is no total to report. The key is absent rather
// than 0, which would claim Slack said nothing matched.
func TestPageTotal_AbsentWhenNoPageArrived(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "fatal_error"})
	})
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "messages", "deploy")
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, ok := metaOf(t, out)["total"]; ok {
		t.Errorf("total must be absent when no page arrived:\n%s", out)
	}
}

// --trace records the total Slack sent with each page, so a result can be
// reconstructed after the output file is gone. A failed attempt carries none.
func TestPageTotal_InTraceEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("page") {
		case "", "1":
			_ = json.NewEncoder(w).Encode(searchResponse(
				[]map[string]any{searchMatch("1.1", "msg1", "U01XYZ", "alice", "C01ABC", "general", "https://x/1")}, 1, 3, 3))
		case "2":
			_ = json.NewEncoder(w).Encode(searchResponse(
				[]map[string]any{searchMatch("2.2", "msg2", "U01XYZ", "alice", "C01ABC", "general", "https://x/2")}, 2, 4, 4))
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "fatal_error"})
		}
	})
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	r := runWithMockFull(t, mux, "--trace", "search", "messages", "deploy", "--all", "--limit", "1")

	var pages []map[string]any
	for _, line := range nonEmptyLines(r.stderr) {
		if evt := parseJSON(t, line); evt["kind"] == "page" {
			pages = append(pages, evt)
		}
	}
	if len(pages) != 3 {
		t.Fatalf("expected 3 page events, got %d:\n%s", len(pages), r.stderr)
	}
	if pages[0]["total"] != float64(3) || pages[1]["total"] != float64(4) {
		t.Errorf("page totals = %v, %v, want 3 then 4", pages[0]["total"], pages[1]["total"])
	}
	if _, ok := pages[2]["total"]; ok {
		t.Errorf("a failed page must carry no total, got %v", pages[2]["total"])
	}
}
