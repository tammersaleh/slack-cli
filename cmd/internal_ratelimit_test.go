package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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

// A single-shot internal call is not retried, but a rate limit on it reports
// rate_limited at exit 3 with Slack's Retry-After in the detail, so a caller
// pacing a bulk driver has the server's number to wait on.
func TestSectionList_RateLimitedIsExit3WithRetryAfter(t *testing.T) {
	var calls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.list", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
	})

	_, err := runWithMockSession(t, mux, "section", "list")
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
		t.Errorf("requests = %d, want 1 (single-shot calls are not retried)", got)
	}
}
