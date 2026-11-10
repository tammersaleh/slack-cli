package cmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// wrappingPage models what Slack's page-number APIs do past page 100, measured
// live 2026-11-10 on search.messages, search.files, and files.list: page 100
// answers normally and still reports more pages, and any page above 100 answers
// as page 1. It returns the page Slack would report serving.
func wrappingPage(requested string) int {
	page, err := strconv.Atoi(requested)
	if err != nil || page < 1 || page > 100 {
		return 1
	}
	return page
}

func assertPageLimit(t *testing.T, err error, code string) {
	t.Helper()
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != code {
		t.Errorf("error = %q, want %q", oErr.Err, code)
	}
	if oErr.Code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", oErr.Code, output.ExitGeneral)
	}
	if oErr.Hint == "" {
		t.Error("the page limit error must carry a hint naming the way around it")
	}
}

// The trailer after a refused page is terminal: no cursor, since the only one
// to offer is the page Slack would answer with page 1's rows.
func assertTerminalTrailer(t *testing.T, out, code string) {
	t.Helper()
	meta := metaOf(t, out)
	if meta["error"] != code {
		t.Errorf("_meta.error = %v, want %q", meta["error"], code)
	}
	if meta["has_more"] != false {
		t.Errorf("_meta.has_more = %v, want false", meta["has_more"])
	}
	if _, ok := meta["next_cursor"]; ok {
		t.Errorf("_meta must carry no resume cursor, got %v", meta["next_cursor"])
	}
}

// Every command on a page-number API refuses a page above 100 before calling
// Slack. Without the guard each of these exits 0 with page 1's rows.
func TestPageLimit_CursorAbove100MakesNoRequest(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		session  bool
		args     []string
		code     string
	}{
		{"search messages", "search.messages", false, []string{"search", "messages", "deploy", "--cursor", "101"}, "search_page_limit"},
		{"search files", "search.files", false, []string{"search", "files", "report", "--cursor", "101"}, "search_page_limit"},
		{"file list", "files.list", false, []string{"file", "list", "--cursor", "101"}, "file_page_limit"},
		{"list find", "search.files", true, []string{"list", "find", "Launch", "--cursor", "250"}, "search_page_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/api/"+tt.endpoint, func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
			t.Setenv("SLACK_USER_TOKEN", "xoxp-test")

			run := runWithMock
			if tt.session {
				run = runWithMockSession
			}
			out, err := run(t, mux, tt.args...)
			assertPageLimit(t, err, tt.code)
			if calls != 0 {
				t.Errorf("%s was called %d times, want 0", tt.endpoint, calls)
			}
			if lines := nonEmptyLines(out); len(lines) != 1 {
				t.Fatalf("expected the trailer alone, got %d lines:\n%s", len(lines), out)
			}
			assertTerminalTrailer(t, out, tt.code)
		})
	}
}

// An --all walk over a result set deeper than 100 pages emits pages 1-100 once
// each and stops. Before the guard it requested page 101, which Slack answers
// with page 1, so the output ended with a duplicate of the first page.
func TestSearchMessages_AllStopsAtPage100(t *testing.T) {
	var requested []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		requested = append(requested, r.Form.Get("page"))
		page := wrappingPage(r.Form.Get("page"))
		_ = json.NewEncoder(w).Encode(searchResponse(
			[]map[string]any{
				searchMatch(fmt.Sprintf("%d.000100", 1709251200+page), fmt.Sprintf("msg%d", page), "U01XYZ", "alice", "C01ABC", "general", "https://x/p"),
			},
			page, 150, 150,
		))
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "messages", "deploy", "--all", "--limit", "1")
	assertPageLimit(t, err, "search_page_limit")

	if len(requested) != 100 || requested[99] != "100" {
		t.Errorf("requested %d pages ending at %q, want 100 ending at page 100", len(requested), requested[len(requested)-1])
	}
	lines := nonEmptyLines(out)
	if len(lines) != 101 {
		t.Fatalf("expected 100 rows + trailer, got %d lines", len(lines))
	}
	seen := map[string]bool{}
	for _, line := range lines[:100] {
		text, _ := parseJSON(t, line)["text"].(string)
		if seen[text] {
			t.Errorf("row %q emitted twice", text)
		}
		seen[text] = true
	}
	assertTerminalTrailer(t, out, "search_page_limit")
}

// Page 100 itself is served, and its trailer still names page 101: more
// results exist and the caller who asks for them gets the page limit error
// rather than a has_more:false that reads as a complete result.
func TestSearchMessages_Page100IsServed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_ = json.NewEncoder(w).Encode(searchResponse(
			[]map[string]any{searchMatch("1709251200.000100", "msg100", "U01XYZ", "alice", "C01ABC", "general", "https://x/p")},
			wrappingPage(r.Form.Get("page")), 150, 150,
		))
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "messages", "deploy", "--cursor", "100", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if meta := metaOf(t, out); meta["has_more"] != true || meta["next_cursor"] != "101" {
		t.Errorf("trailer = %v, want has_more=true next_cursor=101", meta)
	}
}
