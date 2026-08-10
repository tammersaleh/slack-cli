package cmd_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSavedListEnrich_RateLimitedLookupRetriesTheWholePage pins the retry
// granularity of --enrich: page, not per-item.
//
// Nothing retries an individual message lookup. slack-go v0.18.0 retries only
// inside its own GetAll* helpers, and conversations.history, chat.getPermalink
// and conversations.replies all go through one-shot postMethod/getMethod. So a
// 429 on a lookup is a systemic error (it is deliberately absent from
// itemLocalEnrichErrors), enrichItems returns it raw, and because enrichItems
// runs *inside* the fetch closure api.FetchPage wraps, FetchPage retries the
// entire closure with Retry-After backoff - saved.list included. The two
// saved.list requests asserted below are that amplification. It is coarse but
// output-correct, and it is what CLAUDE.md, SPEC.md and the enrichItems doc
// comment all describe, so a future change that "fixes" the duplicate
// saved.list call should fail here and be a deliberate decision.
//
// The 429 must be a real HTTP status *with* a Retry-After header: that is the
// only shape slack-go turns into a *slack.RateLimitedError, and only that type
// enters fetchWithRetry's retry branch. An {"ok":false,"error":"ratelimited"}
// body becomes a slack.SlackErrorResponse and aborts the page instead - that
// string form is covered by TestSavedListEnrich_SystemicErrorsAreFatal.
//
// Retry-After: 1 is the smallest value slack-go can parse, so this test costs
// about a second of wall clock. Worth it - the retry path is otherwise
// completely untested. Exhaustion after maxAttempts is not retested here;
// internal/api/paginate_test.go already covers it.
func TestSavedListEnrich_RateLimitedLookupRetriesTheWholePage(t *testing.T) {
	m := &enrichMock{}
	// One item at a root ts, so conversations.history alone resolves it and the
	// permalink/replies fallback never runs. Keeps the call counts unambiguous.
	inner := m.mux(t, savedItemsFor(rootTS), "")

	var retrySavedListCalls, retryHistoryCalls atomic.Int64
	// True if the second attempt began before the first one had failed, which
	// would mean the two attempts overlapped rather than being sequential.
	var retryOverlapped atomic.Bool

	wrapped := http.NewServeMux()
	wrapped.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/") {
		case "saved.list":
			if retrySavedListCalls.Add(1) == 2 && retryHistoryCalls.Load() == 0 {
				retryOverlapped.Store(true)
			}
		case "conversations.history":
			if retryHistoryCalls.Add(1) == 1 {
				// Retry-After is mandatory: without it slack-go returns a
				// header-parse error and never builds a RateLimitedError.
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
		}
		inner.ServeHTTP(w, r)
	})

	out, err := runWithMockSession(t, wrapped, "saved", "list", "--enrich")
	// Not fatal: if the 429 stopped being systemic, the request counts below
	// say so far more precisely than the exit status does.
	if err != nil {
		t.Errorf("a transient 429 must be recovered by the page retry, got %T: %v", err, err)
	}

	if got := retrySavedListCalls.Load(); got != 2 {
		t.Errorf("saved.list requests = %d, want 2 - the rate-limited page is refetched "+
			"whole, including the internal call that was never limited", got)
	}
	if got := retryHistoryCalls.Load(); got != 2 {
		t.Errorf("conversations.history requests = %d, want 2 (the 429 plus the retry)", got)
	}
	if retryOverlapped.Load() {
		t.Error("the retry attempt started before the first attempt failed")
	}

	rows, meta := rowsAndMeta(t, out)
	// Exactly one row: the abandoned first attempt emits nothing, because a
	// page is built completely before its first line reaches stdout. A per-item
	// retry, or an emit-as-you-go fetch, would duplicate this row.
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d:\n%s", len(rows), out)
	}
	if rows[0]["text"] != "root message" {
		t.Errorf("text = %q, want %q", rows[0]["text"], "root message")
	}
	if _, bad := rows[0]["enrich_error"]; bad {
		t.Errorf("a recovered 429 must leave no marker on the row: %v", rows[0]["enrich_error"])
	}
	// The trailer must look like an ordinary complete page. A recovered retry
	// that still reported an error or a resume cursor would tell a caller to
	// refetch data it already has.
	if meta["error"] != nil {
		t.Errorf("_meta.error = %v, want empty after a recovered retry", meta["error"])
	}
	if meta["has_more"] != false {
		t.Errorf("_meta.has_more = %v, want false", meta["has_more"])
	}
}
