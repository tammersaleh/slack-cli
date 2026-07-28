package cmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// The enrich tests below use one topology throughout:
//
//	rootTS   - a normal channel message; conversations.history returns it.
//	replyTS  - a thread reply under parentTS; conversations.history returns an
//	           empty page for it, which is what Slack really does.
//
// conversations.replies always prepends the thread parent regardless of the
// oldest/latest window, so the reply fixtures return [parent, reply] to keep
// the scan-for-target behavior under test.
const (
	enrichChannel = "C01ABC"
	rootTS        = "1709251200.000100"
	parentTS      = "1709251200.000200"
	replyTS       = "1709251200.000300"
)

func tsToPathSegment(ts string) string {
	return "p" + strings.ReplaceAll(ts, ".", "")
}

// savedItemsFor builds saved.list rows for the given timestamps.
func savedItemsFor(ts ...string) []map[string]any {
	items := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		items = append(items, map[string]any{
			"item_id":      enrichChannel,
			"ts":           t,
			"date_created": 1709251200,
			"todo_state":   "uncompleted",
		})
	}
	return items
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

func slackErr(w http.ResponseWriter, code string) {
	writeJSON(w, map[string]any{"ok": false, "error": code})
}

// enrichMock is a configurable stand-in for the endpoints enrichment touches.
// Zero value serves the topology above; fields override individual behaviors.
type enrichMock struct {
	// historyFor returns the messages conversations.history reports for a ts.
	// A nil result means an empty page, which drives the reply fallback.
	historyFor func(ts string) []map[string]any
	// historyErr, when it returns a non-empty code, fails the history call.
	historyErr func(ts string) string
	// repliesErr fails conversations.replies with the given code.
	repliesErr func(threadTS string) string
	// repliesParentOnly returns a replies page holding only the thread parent.
	repliesParentOnly bool
	// permalinkErr fails chat.getPermalink with the given code.
	permalinkErr func(ts string) string
	// permalinkLink overrides the permalink URL chat.getPermalink returns.
	permalinkLink func(ts string) string
	// gate, when non-nil, is called at the top of each history handler so a
	// test can control ordering.
	gate func(ts string)
	// afterRespond, when non-nil, is called once a history response has been
	// fully written.
	afterRespond func(ts string)
	// savedList overrides the saved.list handler, for multi-page tests.
	savedList http.HandlerFunc

	historyCalls   atomic.Int64
	permalinkCalls atomic.Int64
	repliesCalls   atomic.Int64
	infoCalls      atomic.Int64
}

// defaultHistory implements the topology: rootTS resolves, everything else
// comes back as an empty page the way a thread reply does.
func defaultHistory(ts string) []map[string]any {
	if ts == rootTS {
		return []map[string]any{{"text": "root message", "user": "U01XYZ", "ts": rootTS}}
	}
	return nil
}

func (m *enrichMock) mux(t *testing.T, items []map[string]any, nextCursor string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	savedList := m.savedList
	if savedList == nil {
		savedList = savedListHandler(t, items, map[string]any{"total": len(items)}, nextCursor)
	}
	mux.HandleFunc("/api/saved.list", savedList)

	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ts := r.FormValue("latest")
		if m.gate != nil {
			m.gate(ts)
		}
		m.historyCalls.Add(1)
		if m.historyErr != nil {
			if code := m.historyErr(ts); code != "" {
				slackErr(w, code)
				return
			}
		}
		hf := m.historyFor
		if hf == nil {
			hf = defaultHistory
		}
		msgs := hf(ts)
		if msgs == nil {
			msgs = []map[string]any{}
		}
		writeJSON(w, map[string]any{"ok": true, "messages": msgs, "has_more": false})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if m.afterRespond != nil {
			m.afterRespond(ts)
		}
	})

	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ts := r.FormValue("message_ts")
		m.permalinkCalls.Add(1)
		if m.permalinkErr != nil {
			if code := m.permalinkErr(ts); code != "" {
				slackErr(w, code)
				return
			}
		}
		// Only replyTS is a reply; anything else gets a root permalink with no
		// thread_ts, which findThreadReply reads as "not a reply".
		link := fmt.Sprintf("https://acme.slack.com/archives/%s/%s?cid=%s",
			enrichChannel, tsToPathSegment(ts), enrichChannel)
		if ts == replyTS {
			link = fmt.Sprintf("https://acme.slack.com/archives/%s/%s?thread_ts=%s&cid=%s",
				enrichChannel, tsToPathSegment(ts), parentTS, enrichChannel)
		}
		if m.permalinkLink != nil {
			link = m.permalinkLink(ts)
		}
		writeJSON(w, map[string]any{"ok": true, "permalink": link})
	})

	mux.HandleFunc("/api/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		threadTS := r.FormValue("ts")
		m.repliesCalls.Add(1)
		if m.repliesErr != nil {
			if code := m.repliesErr(threadTS); code != "" {
				slackErr(w, code)
				return
			}
		}
		// Parent first, target second - Slack always prepends the parent.
		msgs := []map[string]any{
			{"text": "thread parent", "user": "U02MGR", "ts": parentTS, "thread_ts": parentTS},
			{"text": "the reply", "user": "U01XYZ", "ts": replyTS, "thread_ts": parentTS},
		}
		if m.repliesParentOnly {
			msgs = msgs[:1]
		}
		writeJSON(w, map[string]any{"ok": true, "messages": msgs, "has_more": false})
	})

	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		m.infoCalls.Add(1)
		_ = r.ParseForm()
		writeJSON(w, map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": enrichChannel, "name": "general", "is_channel": true},
		})
	})

	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		t.Error("conversations.list must not be called during enrichment")
		slackErr(w, "unexpected_call")
	})

	return mux
}

// assertFatalPage checks that a page which failed systemically emitted no rows
// and still wrote exactly one trailer carrying the code.
//
// Checking "every line contains _meta" is not enough - that passes on empty
// output, so it would also pass if the trailer were missing entirely.
func assertFatalPage(t *testing.T, out string, wantCode string, wantRows int) map[string]any {
	t.Helper()
	rows, meta := rowsAndMeta(t, out)
	if len(rows) != wantRows {
		t.Errorf("expected %d rows before the fatal page, got %d:\n%s", wantRows, len(rows), out)
	}
	if meta["error"] != wantCode {
		t.Errorf("_meta.error = %v, want %q", meta["error"], wantCode)
	}
	return meta
}

// assertExitError requires the partial-failure exit type specifically. Asserting
// only "not *output.Error" would pass for any unrelated error.
func assertExitError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a nonzero exit")
	}
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", exitErr.Code, output.ExitGeneral)
	}
	var oErr *output.Error
	if errors.As(err, &oErr) {
		t.Errorf("item-local failure must not also be *output.Error (stderr JSON): %v", err)
	}
}

// rowsAndMeta splits enrich output into item rows and the trailer.
func rowsAndMeta(t *testing.T, out string) ([]map[string]any, map[string]any) {
	t.Helper()
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		t.Fatalf("no output")
	}
	rows := make([]map[string]any, 0, len(lines)-1)
	for _, l := range lines[:len(lines)-1] {
		rows = append(rows, parseJSON(t, l))
	}
	last := parseJSON(t, lines[len(lines)-1])
	meta, ok := last["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("last line is not a _meta trailer: %s", lines[len(lines)-1])
	}
	return rows, meta
}

// TestSavedListEnrich_ThreadReply is the reported bug: conversations.history
// never returns thread replies, so a saved reply used to emit no text at all
// and the command still exited 0.
func TestSavedListEnrich_ThreadReply(t *testing.T) {
	m := &enrichMock{}
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(replyTS), ""), "saved", "list", "--enrich")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d:\n%s", len(rows), out)
	}
	// The reply's text, not the thread parent's - a parent/target mixup
	// produces plausible-looking output, so assert on both text and from_user.
	if rows[0]["text"] != "the reply" {
		t.Errorf("text = %q, want %q (thread parent leaked in?)", rows[0]["text"], "the reply")
	}
	if rows[0]["from_user"] != "U01XYZ" {
		t.Errorf("from_user = %q, want U01XYZ", rows[0]["from_user"])
	}
	if _, bad := rows[0]["enrich_error"]; bad {
		t.Errorf("resolved reply must not carry enrich_error: %v", rows[0]["enrich_error"])
	}
	if got := m.repliesCalls.Load(); got != 1 {
		t.Errorf("conversations.replies calls = %d, want 1", got)
	}
}

// TestSavedListEnrich_RootMessageSkipsFallback keeps the fast path intact: a
// message conversations.history can see must not cost a permalink lookup.
func TestSavedListEnrich_RootMessageSkipsFallback(t *testing.T) {
	m := &enrichMock{}
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(rootTS), ""), "saved", "list", "--enrich")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 1 || rows[0]["text"] != "root message" {
		t.Fatalf("expected the root message, got %v", rows)
	}
	if got := m.permalinkCalls.Load(); got != 0 {
		t.Errorf("chat.getPermalink calls = %d, want 0 for a root message", got)
	}
	if got := m.repliesCalls.Load(); got != 0 {
		t.Errorf("conversations.replies calls = %d, want 0 for a root message", got)
	}
}

// TestSavedListEnrich_IgnoresNonTargetHistoryMessage pins exact-ts matching.
// A nonempty history page that doesn't contain the target must not be used;
// trusting Messages[0] would attach a different message's text to the row.
func TestSavedListEnrich_IgnoresNonTargetHistoryMessage(t *testing.T) {
	m := &enrichMock{
		historyFor: func(ts string) []map[string]any {
			return []map[string]any{{"text": "some other message", "user": "U09OTH", "ts": "1709251200.999999"}}
		},
	}
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(replyTS), ""), "saved", "list", "--enrich")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows, _ := rowsAndMeta(t, out)
	if rows[0]["text"] == "some other message" {
		t.Fatal("attached a message whose ts does not match the saved item")
	}
	if rows[0]["text"] != "the reply" {
		t.Errorf("text = %q, want the fallback result", rows[0]["text"])
	}
}

// TestSavedListEnrich_EmptyTextIsSuccess covers block-only and bot messages:
// a successfully fetched message with no text is resolved, not an error. The
// old code inferred success from a nonempty field, which conflates the two.
func TestSavedListEnrich_EmptyTextIsSuccess(t *testing.T) {
	m := &enrichMock{
		historyFor: func(ts string) []map[string]any {
			return []map[string]any{{"text": "", "user": "", "ts": ts, "subtype": "bot_message"}}
		},
	}
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(rootTS), ""), "saved", "list", "--enrich")
	if err != nil {
		t.Fatalf("a resolved message with empty text is not a failure: %v", err)
	}

	rows, _ := rowsAndMeta(t, out)
	if _, bad := rows[0]["enrich_error"]; bad {
		t.Errorf("empty text must not produce enrich_error: %v", rows[0]["enrich_error"])
	}
	if got := m.permalinkCalls.Load(); got != 0 {
		t.Errorf("an exact-ts match must not trigger the fallback (got %d permalink calls)", got)
	}
}

// TestSavedListEnrich_NotAReplyGetsMissMarker covers findThreadReply returning
// (nil, nil): history saw nothing and the ts isn't a reply either. That is a
// real miss and must be marked, not emitted as a silently unenriched row.
func TestSavedListEnrich_NotAReplyGetsMissMarker(t *testing.T) {
	m := &enrichMock{}
	// parentTS is neither returned by history nor flagged as a reply by the
	// permalink fixture.
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(parentTS), ""), "saved", "list", "--enrich")
	assertExitError(t, err)

	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 1 {
		t.Fatalf("the row must still be emitted, got %d rows", len(rows))
	}
	if rows[0]["enrich_error"] != "message_not_found" {
		t.Errorf("enrich_error = %q, want message_not_found", rows[0]["enrich_error"])
	}
}

// TestSavedListEnrich_ItemLocalErrorsStayInline pins the partial-failure
// contract: a per-item Slack error marks its own row, every other row still
// resolves, output completes with a trailer, and the exit is ExitError with no
// stderr JSON.
func TestSavedListEnrich_ItemLocalErrorsStayInline(t *testing.T) {
	for _, code := range []string{"not_in_channel", "channel_not_found", "message_not_found", "thread_not_found"} {
		t.Run(code, func(t *testing.T) {
			m := &enrichMock{
				historyErr: func(ts string) string {
					if ts == replyTS {
						return code
					}
					return ""
				},
			}
			out, err := runWithMockSession(t, m.mux(t, savedItemsFor(rootTS, replyTS), ""), "saved", "list", "--enrich")
			assertExitError(t, err)

			rows, meta := rowsAndMeta(t, out)
			if len(rows) != 2 {
				t.Fatalf("expected both rows, got %d:\n%s", len(rows), out)
			}
			if rows[0]["text"] != "root message" {
				t.Errorf("the healthy row must still enrich, got %v", rows[0]["text"])
			}
			if rows[1]["enrich_error"] != code {
				t.Errorf("enrich_error = %q, want %q", rows[1]["enrich_error"], code)
			}
			// The saved item itself was retrieved fine, so the row keeps its
			// base fields and the stream is not reported as truncated.
			if rows[1]["message_ts"] != replyTS {
				t.Errorf("failed row lost its base fields: %v", rows[1])
			}
			if meta["error"] != nil {
				t.Errorf("_meta.error must stay empty for item-local failures, got %v", meta["error"])
			}
		})
	}
}

// TestSavedListEnrich_SystemicErrorsAreFatal covers the fail-closed rule. An
// error that isn't an enumerated item-local code aborts the page: no rows are
// emitted, and the real cause survives (a sibling's context.Canceled must not
// replace it).
func TestSavedListEnrich_SystemicErrorsAreFatal(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{"missing scope", "missing_scope"},
		{"revoked token", "token_revoked"},
		// Not a rate-limit test: an ok:false body yields a SlackErrorResponse,
		// never a *slack.RateLimitedError, so no 429 / Retry-After / FetchPage
		// retry path is exercised here. It belongs in this table only as
		// "another unallowlisted code aborts".
		{"ratelimited string", "ratelimited"},
		// Fail-closed: an unenumerated code must abort rather than degrade
		// into a silently unenriched row.
		{"unknown code", "some_future_slack_error"},
		// The live Grid failure. Deliberately not item-local: it reports a
		// wrong token context, and inlining it would disguise a fixable
		// misconfiguration as a set of unreadable channels.
		{"wrong token context", "enterprise_is_restricted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &enrichMock{
				historyErr: func(ts string) string {
					if ts == replyTS {
						return tc.code
					}
					return ""
				},
			}
			out, err := runWithMockSession(t, m.mux(t, savedItemsFor(rootTS, replyTS), ""), "saved", "list", "--enrich")
			if err == nil {
				t.Fatalf("expected a fatal error for %s", tc.code)
			}
			var oErr *output.Error
			if !errors.As(err, &oErr) {
				t.Fatalf("systemic failure must be *output.Error (stderr), got %T: %v", err, err)
			}
			if oErr.Err != tc.code {
				t.Errorf("error code = %q, want %q (original cause lost?)", oErr.Err, tc.code)
			}

			// Page-atomic: the failed page emits zero rows but still writes a
			// trailer carrying the code.
			meta := assertFatalPage(t, out, tc.code, 0)
			// The error was handed to streamPages unclassified, so a first-page
			// failure stays resumable with an empty resume cursor.
			if meta["has_more"] != true {
				t.Errorf("_meta.has_more = %v, want true (enrich errors must stay resumable)", meta["has_more"])
			}
		})
	}
}

// TestSavedListEnrich_PreservesInputOrderWhenResponsesAreInverted withholds
// item 1's history response until item 2's has been written and flushed. Rows
// must still come out in saved.list order, each carrying its own result.
//
// Scope, precisely: this inverts *server response* order, not worker completion
// order - after the later response flushes, its client goroutine can still be
// descheduled and the earlier worker can store first. Forcing true completion
// inversion would need an injected lookup func, which is not worth production
// indirection. DuplicateItemsResolveIndependently is the deterministic guard
// against the old collapsing map; this one is a concurrency stress test.
//
// Releasing the gate at handler *entry*, as the first version did, proved
// nothing: it fired before the response was written or parsed, so both handlers
// then raced and item 1 could still finish first.
func TestSavedListEnrich_PreservesInputOrderWhenResponsesAreInverted(t *testing.T) {
	secondResponded := make(chan struct{})

	m := &enrichMock{
		historyFor: func(ts string) []map[string]any {
			return []map[string]any{{"text": "text for " + ts, "user": "U01XYZ", "ts": ts}}
		},
	}
	m.afterRespond = func(ts string) {
		if ts == replyTS {
			close(secondResponded)
		}
	}
	m.gate = func(ts string) {
		if ts != rootTS {
			return
		}
		// Bounded so a scheduling change fails the test instead of hanging it.
		select {
		case <-secondResponded:
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for the second item's response; order was not inverted")
		}
	}

	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(rootTS, replyTS), ""), "saved", "list", "--enrich")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0]["message_ts"] != rootTS || rows[1]["message_ts"] != replyTS {
		t.Fatalf("rows are out of saved.list order: %v, %v", rows[0]["message_ts"], rows[1]["message_ts"])
	}
	// Each row must carry its own lookup result, not its neighbor's.
	if rows[0]["text"] != "text for "+rootTS {
		t.Errorf("row 0 text = %q, want text for %s", rows[0]["text"], rootTS)
	}
	if rows[1]["text"] != "text for "+replyTS {
		t.Errorf("row 1 text = %q, want text for %s", rows[1]["text"], replyTS)
	}
}

// TestSavedListEnrich_DuplicateItemsResolveIndependently guards the switch off
// a channel:ts keyed map, where duplicate saved rows overwrote each other
// last-writer-wins.
//
// Two rows with the SAME channel and ts get deliberately different outcomes -
// the first history call succeeds, the second fails. The old map would collapse
// them onto one key and emit two successes or two failures depending on which
// worker wrote last; indexed outcomes give exactly one of each. Giving both
// duplicates identical mock results, as this test first did, could not tell the
// two implementations apart at all.
//
// Which row gets which outcome is left unasserted: the inputs are identical, so
// worker scheduling decides, and pinning it would make the test flaky.
func TestSavedListEnrich_DuplicateItemsResolveIndependently(t *testing.T) {
	var calls atomic.Int64
	m := &enrichMock{
		historyErr: func(ts string) string {
			if calls.Add(1) == 2 {
				return "not_in_channel"
			}
			return ""
		},
	}

	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(rootTS, rootTS), ""), "saved", "list", "--enrich")
	assertExitError(t, err)

	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d:\n%s", len(rows), out)
	}

	successes, failures := 0, 0
	for _, row := range rows {
		switch {
		case row["enrich_error"] == "not_in_channel":
			failures++
		case row["text"] == "root message":
			successes++
		default:
			t.Errorf("row is neither resolved nor marked: %v", row)
		}
	}
	if successes != 1 || failures != 1 {
		t.Errorf("got %d resolved and %d marked rows, want exactly 1 of each "+
			"(duplicate keys collapsed onto one outcome?)", successes, failures)
	}
}

// TestSavedListEnrich_DoesNotDuplicateResolverInfoLookup pins the removal of
// the redundant per-item channel lookup. channel_name still lands, from the
// output pipeline's resolver, which makes the same conversations.info call one
// layer down and memoizes it - so enrichment must not repeat it per item.
//
// This asserts request volume, not which layer spends it: a command-local
// once-per-channel cache would also pass. Volume is the contract that matters.
func TestSavedListEnrich_DoesNotDuplicateResolverInfoLookup(t *testing.T) {
	m := &enrichMock{}
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(rootTS, replyTS), ""), "saved", "list", "--enrich")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows, _ := rowsAndMeta(t, out)
	// channel_name still lands, via the resolver rather than enrichment.
	for i, row := range rows {
		if row["channel_name"] != "general" {
			t.Errorf("row %d channel_name = %q, want general", i, row["channel_name"])
		}
	}
	// Exactly one shared resolver lookup for the single distinct channel, not
	// one per saved item.
	if got := m.infoCalls.Load(); got != 1 {
		t.Errorf("conversations.info calls = %d, want exactly 1 (per-item lookup not removed?)", got)
	}
}

// TestSavedListEnrich_MessageLookupRequestCounts pins the message-lookup call
// budget: exactly one history call per item, and a permalink+replies pair only
// for the items history cannot see.
//
// This counts message lookups only. It is not the command's total request count
// - the output resolver makes its own conversations.info/users.info calls
// during emit, which this deliberately ignores.
func TestSavedListEnrich_MessageLookupRequestCounts(t *testing.T) {
	m := &enrichMock{}
	items := savedItemsFor(rootTS, rootTS, replyTS, replyTS)
	if _, err := runWithMockSession(t, m.mux(t, items, ""), "saved", "list", "--enrich"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := m.historyCalls.Load(); got != 4 {
		t.Errorf("conversations.history calls = %d, want 4 (one per item)", got)
	}
	if got := m.permalinkCalls.Load(); got != 2 {
		t.Errorf("chat.getPermalink calls = %d, want 2 (one per history miss)", got)
	}
	if got := m.repliesCalls.Load(); got != 2 {
		t.Errorf("conversations.replies calls = %d, want 2 (one per history miss)", got)
	}
}

// TestSavedListEnrich_PermalinkDriftIsFatal covers a URL-shaped permalink that
// fails validation - here the cid names a different channel than the path. That
// is drift in a shape we depend on, not a missing message, so it must abort
// rather than quietly mark the row not-found.
//
// A permalink that isn't URL-shaped at all is a different case: slackurl
// reports matched=false with no error, so findThreadReply reads it as "not a
// reply" and the row gets a message_not_found marker. Marked and nonzero, so
// not silent.
func TestSavedListEnrich_PermalinkDriftIsFatal(t *testing.T) {
	m := &enrichMock{
		permalinkLink: func(ts string) string {
			return fmt.Sprintf("https://acme.slack.com/archives/%s/%s?thread_ts=%s&cid=C99ZZZ",
				enrichChannel, tsToPathSegment(ts), parentTS)
		},
	}

	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(replyTS), ""), "saved", "list", "--enrich")
	if err == nil {
		t.Fatal("expected a fatal error for an unparseable permalink")
	}
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("permalink drift must be *output.Error, got %T: %v", err, err)
	}
	// Assert the specific classification, not merely "some error": an unrelated
	// setup or auth failure would otherwise satisfy this test.
	if oErr.Err != "unknown_error" {
		t.Errorf("error code = %q, want unknown_error (a parse failure is not a Slack error)", oErr.Err)
	}
	if !strings.Contains(oErr.Detail, "parse permalink") {
		t.Errorf("detail = %q, want it to name the permalink parse failure", oErr.Detail)
	}
	assertFatalPage(t, out, "unknown_error", 0)
}

// TestSavedListEnrich_FatalPageAfterPartialPage covers precedence across
// pages: page 1 has an item-local failure, page 2 fails systemically. Page 1
// stays on stdout, page 2 emits nothing, and the fatal error wins over the
// accumulated partial status.
func TestSavedListEnrich_FatalPageAfterPartialPage(t *testing.T) {
	var page atomic.Int64
	m := &enrichMock{
		historyErr: func(ts string) string {
			// Page 1: replyTS is an item-local miss. Page 2: systemic.
			if page.Load() == 1 {
				if ts == replyTS {
					return "not_in_channel"
				}
				return ""
			}
			return "missing_scope"
		},
	}

	m.savedList = func(w http.ResponseWriter, r *http.Request) {
		n := page.Add(1)
		next := ""
		if n == 1 {
			next = "page2"
		}
		writeJSON(w, map[string]any{
			"ok":                true,
			"saved_items":       savedItemsFor(rootTS, replyTS),
			"counts":            map[string]any{"total": 2},
			"response_metadata": map[string]string{"next_cursor": next},
		})
	}

	out, err := runWithMockSession(t, m.mux(t, nil, ""), "saved", "list", "--enrich", "--all")
	if err == nil {
		t.Fatal("expected the fatal page-2 error")
	}
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("the fatal error must win over the partial exit, got %T: %v", err, err)
	}
	if oErr.Err != "missing_scope" {
		t.Errorf("error = %q, want missing_scope", oErr.Err)
	}

	// Page 1's two rows survive; page 2 contributes nothing.
	meta := assertFatalPage(t, out, "missing_scope", 2)
	// The trailer's cursor is the whole point of the precedence rule: it must
	// name the page that failed, not the one after it, or a caller resuming
	// from it skips page 2 entirely.
	if meta["has_more"] != true {
		t.Errorf("_meta.has_more = %v, want true", meta["has_more"])
	}
	if meta["next_cursor"] != "page2" {
		t.Errorf("_meta.next_cursor = %v, want page2 (the failed page)", meta["next_cursor"])
	}

	rows, _ := rowsAndMeta(t, out)
	if rows[1]["enrich_error"] != "not_in_channel" {
		t.Errorf("page 1's inline error was lost: %v", rows[1])
	}
}

// TestSavedListEnrich_FallbackErrors covers errors raised by the fallback path
// itself rather than by conversations.history. That path is new and is exactly
// where the live Grid failure happened, so each leg needs its own case.
func TestSavedListEnrich_FallbackErrors(t *testing.T) {
	cases := []struct {
		name     string
		permErr  string
		replErr  string
		wantCode string
		fatal    bool
	}{
		{name: "permalink message_not_found", permErr: "message_not_found", wantCode: "message_not_found"},
		{name: "replies thread_not_found", replErr: "thread_not_found", wantCode: "thread_not_found"},
		{name: "permalink restricted is fatal", permErr: "enterprise_is_restricted", wantCode: "enterprise_is_restricted", fatal: true},
		{name: "replies missing_scope is fatal", replErr: "missing_scope", wantCode: "missing_scope", fatal: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &enrichMock{}
			if tc.permErr != "" {
				m.permalinkErr = func(string) string { return tc.permErr }
			}
			if tc.replErr != "" {
				m.repliesErr = func(string) string { return tc.replErr }
			}

			out, err := runWithMockSession(t, m.mux(t, savedItemsFor(replyTS), ""), "saved", "list", "--enrich")

			if tc.fatal {
				var oErr *output.Error
				if !errors.As(err, &oErr) {
					t.Fatalf("expected a fatal *output.Error, got %T: %v", err, err)
				}
				if oErr.Err != tc.wantCode {
					t.Errorf("error = %q, want %q", oErr.Err, tc.wantCode)
				}
				assertFatalPage(t, out, tc.wantCode, 0)
				return
			}

			assertExitError(t, err)
			rows, _ := rowsAndMeta(t, out)
			if len(rows) != 1 {
				t.Fatalf("expected the row to survive, got %d rows", len(rows))
			}
			if rows[0]["enrich_error"] != tc.wantCode {
				t.Errorf("enrich_error = %v, want %q", rows[0]["enrich_error"], tc.wantCode)
			}
		})
	}
}

// TestSavedListEnrich_HistoryErrorSkipsFallback pins that the fallback runs only
// after a *successful* history response. Running it after a history failure
// would report a second, less specific error for the same item.
func TestSavedListEnrich_HistoryErrorSkipsFallback(t *testing.T) {
	m := &enrichMock{
		historyErr: func(string) string { return "not_in_channel" },
	}
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(replyTS), ""), "saved", "list", "--enrich")
	assertExitError(t, err)

	rows, _ := rowsAndMeta(t, out)
	if rows[0]["enrich_error"] != "not_in_channel" {
		t.Errorf("enrich_error = %v, want not_in_channel", rows[0]["enrich_error"])
	}
	if got := m.permalinkCalls.Load(); got != 0 {
		t.Errorf("chat.getPermalink calls = %d, want 0 after a history error", got)
	}
	if got := m.repliesCalls.Load(); got != 0 {
		t.Errorf("conversations.replies calls = %d, want 0 after a history error", got)
	}
}

// TestSavedListEnrich_ParentOnlyRepliesIsAMiss covers a discovered thread whose
// replies page contains only the prepended parent and not the target. That is a
// miss, not a success carrying the parent's text.
func TestSavedListEnrich_ParentOnlyRepliesIsAMiss(t *testing.T) {
	m := &enrichMock{repliesParentOnly: true}
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(replyTS), ""), "saved", "list", "--enrich")
	assertExitError(t, err)

	rows, _ := rowsAndMeta(t, out)
	if rows[0]["enrich_error"] != "message_not_found" {
		t.Errorf("enrich_error = %v, want message_not_found", rows[0]["enrich_error"])
	}
	if rows[0]["text"] == "thread parent" {
		t.Error("the thread parent's text was attached to the saved item")
	}
}

// TestSavedListEnrich_ErrorMarkerSurvivesFieldFilter pins that --fields cannot
// strip the marker. A filtered row that looks whole is exactly the failure this
// change exists to prevent, and the exit code alone cannot say which row broke.
func TestSavedListEnrich_ErrorMarkerSurvivesFieldFilter(t *testing.T) {
	m := &enrichMock{
		historyErr: func(string) string { return "not_in_channel" },
	}
	out, err := runWithMockSession(t, m.mux(t, savedItemsFor(rootTS), ""), "--fields", "text", "saved", "list", "--enrich")
	assertExitError(t, err)

	rows, _ := rowsAndMeta(t, out)
	if rows[0]["enrich_error"] != "not_in_channel" {
		t.Errorf("--fields=text stripped the failure marker: %v", rows[0])
	}
}

// runWithTwoCredentials drives the CLI against two *distinct* stored tokens: an
// org (E-prefix) session credential and a workspace (T-prefix) one.
//
// This exists because the ordinary helpers set SLACK_TOKEN, which short-circuits
// ResolveCredentials and hands both NewSessionClient and NewClient the same
// token - so no single-token test can observe which client makes which call.
// That blind spot is why the wrong-client bug reached a live run with a green
// suite. Here the credentials file is real and the config dir is redirected, so
// the two clients genuinely differ.
func runWithTwoCredentials(t *testing.T, handler http.Handler, args ...string) (string, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	isolateTestEnv(t)

	// os.UserConfigDir reads HOME on darwin and XDG_CONFIG_HOME elsewhere; set
	// both so the credentials file lands in the temp dir on either.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	creds := map[string]any{"workspaces": map[string]any{
		orgWorkspaceID: map[string]any{
			"bot_token": orgToken, "cookie": "d=xyz",
			"auth_method": "desktop", "team_id": orgWorkspaceID, "team_name": "Acme Org",
		},
		teamWorkspaceID: map[string]any{
			"bot_token": teamToken, "cookie": "d=xyz",
			// Deliberately not "desktop": with both credentials sharing an auth
			// method, the two possible re-auth hints are identical and the
			// per-stage authMethod switching cannot be observed at all.
			"auth_method": "oauth", "team_id": teamWorkspaceID, "team_name": "Acme",
		},
	}}

	for _, dir := range []string{
		filepath.Join(home, "Library", "Application Support", "slack-cli"),
		filepath.Join(home, ".config", "slack-cli"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(creds)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// SLACK_TOKEN must stay unset here, or both clients collapse onto it.
	t.Setenv("SLACK_TOKEN", "")
	t.Setenv("SLACK_WORKSPACE", teamWorkspaceID)
	t.Setenv("SLACK_WORKSPACE_ORG", orgWorkspaceID)
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return "", err
	}
	cli.SetOutput(&outBuf, &errBuf)
	runErr := kctx.Run(&cli)
	return outBuf.String(), runErr
}

const (
	orgWorkspaceID  = "E01ORG"
	teamWorkspaceID = "T01ABC"
	orgToken        = "xoxc-org-session"
	teamToken       = "xoxc-team-workspace"
)

// requestToken pulls the token slack-go sent, whether as a form value or bearer.
func requestToken(r *http.Request) string {
	if t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); t != "" {
		return t
	}
	_ = r.ParseForm()
	return r.FormValue("token")
}

// TestSavedListEnrich_TokenRouting is the regression test for the bug the mocks
// could not see: saved.list must go out on the org session token while every
// message lookup uses the workspace token.
//
// chat.getPermalink here rejects the org token with enterprise_is_restricted,
// exactly as the live Grid org does. A one-client implementation fails this test
// for the same reason it failed live; the fixed one resolves the reply.
func TestSavedListEnrich_TokenRouting(t *testing.T) {
	seen := map[string]string{}
	var mu sync.Mutex
	record := func(endpoint string, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen[endpoint] = requestToken(r)
	}

	m := &enrichMock{}
	mux := m.mux(t, savedItemsFor(replyTS), "")

	wrapped := http.NewServeMux()
	wrapped.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		endpoint := strings.TrimPrefix(r.URL.Path, "/api/")
		tok := requestToken(r)
		record(endpoint, r)

		// The org context permits the internal endpoint and refuses permalinks.
		if endpoint == "chat.getPermalink" && tok == orgToken {
			slackErr(w, "enterprise_is_restricted")
			return
		}
		if endpoint == "saved.list" && tok != orgToken {
			slackErr(w, "team_is_restricted")
			return
		}
		mux.ServeHTTP(w, r)
	})

	out, err := runWithTwoCredentials(t, wrapped, "saved", "list", "--enrich")
	if err != nil {
		t.Fatalf("unexpected error (wrong client for some endpoint?): %v", err)
	}

	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 1 || rows[0]["text"] != "the reply" {
		t.Fatalf("the reply did not resolve: %v", rows)
	}

	mu.Lock()
	defer mu.Unlock()
	for endpoint, want := range map[string]string{
		"saved.list":            orgToken,
		"conversations.history": teamToken,
		"chat.getPermalink":     teamToken,
		"conversations.replies": teamToken,
	} {
		got, ok := seen[endpoint]
		if !ok {
			t.Errorf("%s was never called", endpoint)
			continue
		}
		if got != want {
			t.Errorf("%s used the wrong token: got %q, want %q", endpoint, got, want)
		}
	}
}

// TestSavedListEnrich_AuthHintFollowsTheFailingCredential pins per-stage auth
// provenance. cli.authMethod is one ambient value, so a command holding two
// differently-authenticated clients must set it per stage: a revoked workspace
// token must not tell the user to repair the session credential.
//
// The stored credentials use different auth methods on purpose - with both set
// to "desktop" the two hints are identical and deleting the switching would
// leave this test green.
func TestSavedListEnrich_AuthHintFollowsTheFailingCredential(t *testing.T) {
	cases := []struct {
		name       string
		failSaved  bool
		wantDetail string
		// The desktop hint names --desktop; the OAuth one does not.
		wantDesktopHint bool
	}{
		{name: "session credential fails", failSaved: true, wantDetail: "invalid_auth", wantDesktopHint: true},
		{name: "workspace credential fails", failSaved: false, wantDetail: "token_revoked", wantDesktopHint: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &enrichMock{}
			inner := m.mux(t, savedItemsFor(rootTS), "")

			mux := http.NewServeMux()
			mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
				endpoint := strings.TrimPrefix(r.URL.Path, "/api/")
				if tc.failSaved && endpoint == "saved.list" {
					slackErr(w, "invalid_auth")
					return
				}
				if !tc.failSaved && endpoint == "conversations.history" {
					slackErr(w, "token_revoked")
					return
				}
				inner.ServeHTTP(w, r)
			})

			_, err := runWithTwoCredentials(t, mux, "saved", "list", "--enrich")
			var oErr *output.Error
			if !errors.As(err, &oErr) {
				t.Fatalf("expected *output.Error, got %T: %v", err, err)
			}
			if oErr.Err != tc.wantDetail {
				t.Fatalf("error = %q, want %q", oErr.Err, tc.wantDetail)
			}
			gotDesktop := strings.Contains(oErr.Hint, "--desktop")
			if gotDesktop != tc.wantDesktopHint {
				t.Errorf("hint = %q; wanted a --desktop hint: %v (auth provenance followed the wrong credential)",
					oErr.Hint, tc.wantDesktopHint)
			}
		})
	}
}
