package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// failAfterFirstPage serves one good page, then fails every later page. The
// failure is a plain API error rather than a 429 so the command gives up
// immediately - retry timing is exercised separately, in the tests that are
// actually about retrying.
func failAfterFirstPage(itemsKey string, items any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				itemsKey:            items,
				"response_metadata": map[string]string{"next_cursor": "page2cursor"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "internal_error"})
	}
}

// rateLimitAfterFirstPage serves one good page, then answers every later page
// with 429. Retry-After 0 keeps the retry floor at one second per wait.
func rateLimitAfterFirstPage(calls *int, itemsKey string, items any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*calls++
		_ = r.ParseForm()
		if r.FormValue("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				itemsKey:            items,
				"response_metadata": map[string]string{"next_cursor": "page2cursor"},
			})
			return
		}
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}
}

func onePageOfChannels() []map[string]any {
	return []map[string]any{{"id": "C01", "name": "page1"}}
}

// metaTrailer returns the _meta object from the last stdout line, failing the
// test if stdout does not end with one.
func metaTrailer(t *testing.T, stdout string) map[string]any {
	t.Helper()
	lines := nonEmptyLines(stdout)
	if len(lines) == 0 {
		t.Fatalf("expected at least a _meta trailer, got empty stdout")
	}
	meta, ok := parseJSON(t, lines[len(lines)-1])["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("stdout must end with a _meta trailer, got %q", lines[len(lines)-1])
	}
	return meta
}

// A failure partway through --all must still terminate stdout with a trailer
// marking the results incomplete. Without it, a truncated listing is
// indistinguishable from a complete one to anything reading stdout alone.
func TestChannelList_TruncationIsVisibleOnStdout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", failAfterFirstPage("channels", onePageOfChannels()))

	r := runWithMockFull(t, mux, "channel", "list", "--all")

	lines := nonEmptyLines(r.stdout)
	if len(lines) != 2 {
		t.Fatalf("expected the page-1 channel plus a trailer, got %d lines:\n%s", len(lines), r.stdout)
	}
	if id := parseJSON(t, lines[0])["id"]; id != "C01" {
		t.Errorf("expected the fetched page to stay on stdout, got %v", id)
	}

	meta := metaTrailer(t, r.stdout)
	if meta["error"] != "internal_error" {
		t.Errorf("expected _meta.error='internal_error', got %v", meta["error"])
	}
	if meta["has_more"] != true {
		t.Errorf("expected _meta.has_more=true on a truncated stream, got %v", meta["has_more"])
	}
	if meta["next_cursor"] != "page2cursor" {
		t.Errorf("expected _meta.next_cursor to be the page that failed, got %v", meta["next_cursor"])
	}

	// The command still returns a structured error, which main renders to
	// stderr and turns into the exit code.
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected an *output.Error, got %#v", r.err)
	}
	if oErr.Err != "internal_error" {
		t.Errorf("expected error 'internal_error', got %q", oErr.Err)
	}
	if oErr.Code != output.ExitGeneral {
		t.Errorf("expected exit code %d, got %d", output.ExitGeneral, oErr.Code)
	}
}

// A --query walk cut short by a failure has not searched every page, so it must
// not claim it did. Zero matches from a truncated search is the false negative
// the marker exists to expose.
func TestChannelList_QueryNotExhaustiveWhenTruncated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", failAfterFirstPage("channels", onePageOfChannels()))

	r := runWithMockFull(t, mux, "channel", "list", "--query", "needle")
	if r.err == nil {
		t.Fatal("expected the page-2 failure to be reported")
	}

	meta := metaTrailer(t, r.stdout)
	if meta["filter_exhaustive"] != false {
		t.Errorf("expected filter_exhaustive=false on a truncated search, got %v", meta["filter_exhaustive"])
	}
	if meta["error"] != "internal_error" {
		t.Errorf("expected the failure in the trailer, got %v", meta["error"])
	}
}

// A failure on the very first page has no cursor to resume from, but must
// still mark the stream truncated rather than reporting a complete empty set.
func TestChannelList_TruncationOnFirstPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "internal_error"})
	})

	r := runWithMockFull(t, mux, "channel", "list", "--all")

	if lines := nonEmptyLines(r.stdout); len(lines) != 1 {
		t.Fatalf("expected only a trailer, got %d lines:\n%s", len(lines), r.stdout)
	}
	meta := metaTrailer(t, r.stdout)
	if meta["has_more"] != true {
		t.Errorf("expected has_more=true, got %v", meta["has_more"])
	}
	if meta["error"] != "internal_error" {
		t.Errorf("expected error='internal_error', got %v", meta["error"])
	}
	if _, ok := meta["next_cursor"]; ok {
		t.Errorf("expected no next_cursor when the first page failed, got %v", meta["next_cursor"])
	}
}

// A rate-limited page is retried rather than ending the command on the first
// 429. maxAttempts in internal/api is 5, so page 2 is requested five times:
// six requests in total.
func TestChannelList_RetriesRateLimitedPage(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", rateLimitAfterFirstPage(&calls, "channels", onePageOfChannels()))

	r := runWithMockFull(t, mux, "channel", "list", "--all")

	if calls != 6 {
		t.Errorf("expected 1 good page + 5 attempts at the rate-limited page, got %d requests", calls)
	}

	meta := metaTrailer(t, r.stdout)
	if meta["error"] != "rate_limited" {
		t.Errorf("expected _meta.error='rate_limited', got %v", meta["error"])
	}
	if meta["next_cursor"] != "page2cursor" {
		t.Errorf("expected the failed page as the resume cursor, got %v", meta["next_cursor"])
	}

	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected an *output.Error, got %#v", r.err)
	}
	if oErr.Code != output.ExitRateLimit {
		t.Errorf("expected exit code %d, got %d", output.ExitRateLimit, oErr.Code)
	}
	if !strings.Contains(oErr.Detail, "5 attempts") {
		t.Errorf("expected the detail to report attempts, got %q", oErr.Detail)
	}
}

// A rate limit that clears must not truncate at all: the command retries the
// page, gets it, and finishes with a normal complete trailer.
func TestChannelList_RecoversFromTransientRateLimit(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		switch {
		case r.FormValue("cursor") == "":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				"channels":          onePageOfChannels(),
				"response_metadata": map[string]string{"next_cursor": "page2cursor"},
			})
		case calls == 2:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				"channels":          []map[string]any{{"id": "C02", "name": "page2"}},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		}
	})

	r := runWithMockFull(t, mux, "channel", "list", "--all")
	if r.err != nil {
		t.Fatalf("a retried rate limit must not fail the command: %v (stderr %s)", r.err, r.stderr)
	}

	lines := nonEmptyLines(r.stdout)
	if len(lines) != 3 {
		t.Fatalf("expected both channels plus a trailer, got %d lines:\n%s", len(lines), r.stdout)
	}
	meta := metaTrailer(t, r.stdout)
	if meta["has_more"] != false || meta["error"] != nil {
		t.Errorf("expected a clean complete trailer, got %v", meta)
	}
}

// A retry must repeat the same request, not restart the walk. users.list is
// the case worth pinning: its slack-go paginator carries cursor state, so a
// careless adapter silently rewinds to page 1.
func TestUserList_RetryRepeatsTheSameCursor(t *testing.T) {
	var cursors []string
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		cursors = append(cursors, r.FormValue("cursor"))
		switch {
		case r.FormValue("cursor") == "":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				"members":           []map[string]any{{"id": "U01", "name": "alice"}},
				"response_metadata": map[string]string{"next_cursor": "page2cursor"},
			})
		case calls == 2:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				"members":           []map[string]any{{"id": "U02", "name": "bob"}},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		}
	})

	r := runWithMockFull(t, mux, "user", "list", "--all")
	if r.err != nil {
		t.Fatalf("unexpected error: %v (stderr %s)", r.err, r.stderr)
	}

	want := []string{"", "page2cursor", "page2cursor"}
	if strings.Join(cursors, ",") != strings.Join(want, ",") {
		t.Errorf("got cursor sequence %v, want %v", cursors, want)
	}
	if !strings.Contains(r.stdout, `"id":"U02"`) {
		t.Errorf("expected the retried page's user, got:\n%s", r.stdout)
	}
}

// Every cursor-paginated command marks truncation the same way.
func TestPaginatedCommands_MarkTruncation(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		itemsKey string
		items    any
		args     []string
	}{
		{
			name: "channel members", path: "/api/conversations.members",
			itemsKey: "members", items: []string{"U01"},
			args: []string{"channel", "members", "C01ABC", "--all"},
		},
		{
			name: "message list", path: "/api/conversations.history",
			itemsKey: "messages", items: []map[string]any{{"type": "message", "text": "m", "ts": "1709251200.000100"}},
			args: []string{"message", "list", "C01ABC", "--all"},
		},
		{
			name: "user list", path: "/api/users.list",
			itemsKey: "members", items: []map[string]any{{"id": "U01", "name": "alice"}},
			args: []string{"user", "list", "--all"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(tt.path, failAfterFirstPage(tt.itemsKey, tt.items))
			// Channel-name resolution isn't under test; the args use IDs.
			mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": true, "channel": map[string]any{"id": "C01ABC", "name": "general"},
				})
			})

			r := runWithMockFull(t, mux, tt.args...)

			if len(nonEmptyLines(r.stdout)) < 2 {
				t.Fatalf("expected the first page plus a trailer, got:\n%s (stderr %s)", r.stdout, r.stderr)
			}
			meta := metaTrailer(t, r.stdout)
			if meta["error"] != "internal_error" {
				t.Errorf("expected _meta.error='internal_error', got %v", meta["error"])
			}
			if meta["has_more"] != true {
				t.Errorf("expected _meta.has_more=true, got %v", meta["has_more"])
			}
			if meta["next_cursor"] != "page2cursor" {
				t.Errorf("expected _meta.next_cursor='page2cursor', got %v", meta["next_cursor"])
			}
		})
	}
}

// The page-number commands resume by page number, so even a first-page
// failure has a usable resume point.
func TestPageNumberCommands_MarkTruncation(t *testing.T) {
	tests := []struct {
		name string
		path string
		args []string
	}{
		{"file list", "/api/files.list", []string{"file", "list", "--all"}},
		{"search messages", "/api/search.messages", []string{"search", "messages", "hello", "--all"}},
		{"search files", "/api/search.files", []string{"search", "files", "hello", "--all"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(tt.path, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "internal_error"})
			})

			t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
			r := runWithMockFull(t, mux, tt.args...)

			if lines := nonEmptyLines(r.stdout); len(lines) != 1 {
				t.Fatalf("expected only a trailer, got %d lines:\n%s (stderr %s)", len(lines), r.stdout, r.stderr)
			}
			meta := metaTrailer(t, r.stdout)
			if meta["error"] != "internal_error" {
				t.Errorf("expected _meta.error='internal_error', got %v", meta["error"])
			}
			if meta["next_cursor"] != "1" {
				t.Errorf("expected _meta.next_cursor='1', got %v", meta["next_cursor"])
			}
		})
	}
}

// --quiet suppresses stdout entirely, trailer included; the exit code is the
// only signal, as documented.
func TestTruncation_QuietSuppressesTrailer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", failAfterFirstPage("channels", onePageOfChannels()))

	r := runWithMockFull(t, mux, "--quiet", "channel", "list", "--all")
	if strings.TrimSpace(r.stdout) != "" {
		t.Errorf("expected no stdout under --quiet, got %q", r.stdout)
	}
	if r.err == nil {
		t.Error("expected the command to still fail")
	}
}

// A thread that doesn't exist is a verdict on the request, not a page that
// went missing: the trailer says the stream is over with nothing to resume.
func TestThreadList_NotFoundTrailerIsTerminal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []map[string]any{}})
	})

	r := runWithMockFull(t, mux, "thread", "list", "C01ABC", "1709251200.000100")
	if r.err == nil {
		t.Fatal("expected thread_not_found to be fatal")
	}

	meta := metaTrailer(t, r.stdout)
	if meta["error"] != "thread_not_found" {
		t.Errorf("expected _meta.error='thread_not_found', got %v", meta["error"])
	}
	if meta["has_more"] != false {
		t.Errorf("expected has_more=false on a terminal outcome, got %v", meta["has_more"])
	}
	if _, ok := meta["next_cursor"]; ok {
		t.Errorf("expected no resume cursor, got %v", meta["next_cursor"])
	}
}
