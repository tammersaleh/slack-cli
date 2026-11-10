package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// saved list is the one paginated internal-API command, so a rate limit on
// saved.list must be retried by the page retry like any public page. Both wire
// shapes: an HTTP 429 with no Retry-After (the retry floors the wait), and a
// 200 whose envelope says ratelimited.
func TestSavedList_RateLimitedPageIsRetried(t *testing.T) {
	shapes := map[string]func(w http.ResponseWriter){
		"http 429": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusTooManyRequests)
		},
		"ratelimited envelope": func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
		},
	}
	items := []map[string]any{{
		"item_id": "C01ABC", "ts": "1709251200.000100", "date_created": 1709251200, "todo_state": "uncompleted",
	}}
	ok := savedListHandler(t, items, map[string]any{"uncompleted": 1, "completed": 0, "total": 1}, "")

	for name, limit := range shapes {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("/api/saved.list", func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					limit(w)
					return
				}
				ok(w, r)
			})

			out, err := runWithMockSession(t, mux, "saved", "list")
			if err != nil {
				t.Fatalf("a transient rate limit must be recovered, got %T: %v", err, err)
			}
			if got := calls.Load(); got != 2 {
				t.Errorf("saved.list requests = %d, want 2", got)
			}
			rows, meta := rowsAndMeta(t, out)
			if len(rows) != 1 {
				t.Fatalf("expected 1 row, got %d:\n%s", len(rows), out)
			}
			if meta["error"] != nil || meta["has_more"] != false {
				t.Errorf("_meta = %v, want a clean complete trailer", meta)
			}
		})
	}
}

// rateLimitOnce wraps a handler so each listed /api/ method answers 429 with
// no Retry-After the first time it is called, then passes through.
func rateLimitOnce(inner http.Handler, calls *sync.Map, methods ...string) http.Handler {
	limited := map[string]bool{}
	for _, m := range methods {
		limited[m] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/api/")
		n, _ := calls.LoadOrStore(method, new(atomic.Int64))
		if n.(*atomic.Int64).Add(1) == 1 && limited[method] {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func callCount(calls *sync.Map, method string) int64 {
	n, ok := calls.Load(method)
	if !ok {
		return 0
	}
	return n.(*atomic.Int64).Load()
}

// section move is the command that hit this in the field: a bulk sidebar
// reconciliation rate-limited partway and every later move failed. The write
// is idempotent (an insert of a channel already in the target is ignored, and
// the command verifies the result by re-reading), so it retries.
func TestSectionMove_RateLimitedWriteIsRetried(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Customers", []string{"C01", "C03"})
	store.addSection("S02DEF", "Archive", nil)
	var gotInsert, gotRemove []bulkOp
	var calls sync.Map
	h := rateLimitOnce(store.mux(&gotInsert, &gotRemove), &calls, "users.channelSections.channels.bulkUpdate")

	out, err := runWithMockSession(t, h, "section", "move", "--channels", "C01", "--section", "S02DEF")
	if err != nil {
		t.Fatalf("a transient rate limit must be recovered, got %T: %v", err, err)
	}
	if got := callCount(&calls, "users.channelSections.channels.bulkUpdate"); got != 2 {
		t.Errorf("bulkUpdate requests = %d, want 2", got)
	}
	if got := store.sectionOf("C01"); got != "S02DEF" {
		t.Errorf("C01 should be in S02DEF, got %q", got)
	}
	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 1 || rows[0]["moved_count"] != float64(1) {
		t.Errorf("expected moved_count 1, got:\n%s", out)
	}
}

// Internal reads retry too.
func TestSectionList_RateLimitedReadIsRetried(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Customers", []string{"C01"})
	var gotInsert, gotRemove []bulkOp
	var calls sync.Map
	h := rateLimitOnce(store.mux(&gotInsert, &gotRemove), &calls, "users.channelSections.list")

	out, err := runWithMockSession(t, h, "section", "list")
	if err != nil {
		t.Fatalf("a transient rate limit must be recovered, got %T: %v", err, err)
	}
	if got := callCount(&calls, "users.channelSections.list"); got != 2 {
		t.Errorf("list requests = %d, want 2", got)
	}
	if rows, _ := rowsAndMeta(t, out); len(rows) != 1 {
		t.Errorf("expected 1 section row, got:\n%s", out)
	}
}

// A write that is not idempotent is not retried: section create called twice
// could make two sections. A rate limit on it reports rate_limited at exit 3
// with Slack's Retry-After in the detail, so a caller pacing a bulk driver has
// the server's number to wait on.
func TestSectionCreate_RateLimitedIsExit3WithRetryAfter(t *testing.T) {
	var calls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.create", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
	})

	_, err := runWithMockSession(t, mux, "section", "create", "Archive")
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "rate_limited" || oErr.Code != output.ExitRateLimit {
		t.Errorf("got %q exit %d, want rate_limited exit %d", oErr.Err, oErr.Code, output.ExitRateLimit)
	}
	if !strings.Contains(oErr.Detail, "30s") {
		t.Errorf("detail %q should carry the 30s Retry-After", oErr.Detail)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1 (a non-idempotent write is not retried)", got)
	}
}
