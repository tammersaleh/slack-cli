package resolve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
)

// traceEvents decodes a JSON-lines trace buffer.
func traceEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("trace line is not JSON: %q", line)
		}
		events = append(events, ev)
	}
	return events
}

// countEvents counts trace events of one kind for one endpoint.
func countEvents(events []map[string]any, kind, endpoint string) int {
	n := 0
	for _, ev := range events {
		if ev["kind"] == kind && ev["endpoint"] == endpoint {
			n++
		}
	}
	return n
}

func writeRateLimited(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
}

// rateLimitFirst answers the first n requests with a body-level rate limit and
// hands the rest to next. It counts every request.
func rateLimitFirst(n int32, calls *atomic.Int32, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= n {
			writeRateLimited(w)
			return
		}
		next(w, r)
	}
}

// The users.info fallback used to be a raw slack-go call: no trace event, so
// --trace under-reported a stale-cache run by one request per distinct user.
func TestLookupUser_FallbackIsTraced(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", usersInfoHandler(t, testUsers()))
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	var trace bytes.Buffer
	ctx := api.WithTracer(context.Background(), api.NewJSONLinesTracer(&trace))
	if _, found, err := r.LookupUser(ctx, "U001"); err != nil || !found {
		t.Fatalf("found=%v err=%v, want the user", found, err)
	}

	events := traceEvents(t, &trace)
	if got := countEvents(events, "call", "users.info"); got != 1 {
		t.Errorf("got %d users.info call events, want 1: %s", got, trace.String())
	}
}

// A rate limit on the fallback used to be a silent miss. It is now retried.
func TestLookupUser_RetriesRateLimit(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", rateLimitFirst(1, &calls, usersInfoHandler(t, testUsers())))
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	var trace bytes.Buffer
	ctx := api.WithTracer(context.Background(), api.NewJSONLinesTracer(&trace))
	user, found, err := r.LookupUser(ctx, "U001")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v, want the user after one retry", found, err)
	}
	if user.Name != "alice" {
		t.Errorf("got name=%q, want alice", user.Name)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("got %d users.info requests, want 2 (limited, then answered)", got)
	}
	if got := countEvents(traceEvents(t, &trace), "retry", "users.info"); got != 1 {
		t.Errorf("got %d retry events, want 1: %s", got, trace.String())
	}
}

// A miss carries the reason, and the reason is memoized: a wide output naming
// one unresolvable user costs one request, not one per row.
func TestLookupUser_FailureIsMemoizedWithItsError(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", rateLimitFirst(0, &calls, usersInfoHandler(t, testUsers())))
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())
	ctx := context.Background()

	for i := range 3 {
		_, found, err := r.LookupUser(ctx, "U999")
		if found {
			t.Fatalf("lookup %d: found an unknown user", i)
		}
		if code, ok := api.SlackErrorCode(err); !ok || code != "user_not_found" {
			t.Fatalf("lookup %d: err=%v, want Slack's user_not_found", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("got %d users.info requests for 3 lookups of one bad id, want 1", got)
	}

	// The memo is per ID: another user still resolves.
	if _, found, err := r.LookupUser(ctx, "U001"); err != nil || !found {
		t.Errorf("found=%v err=%v, want a different user to resolve", found, err)
	}
}

// Enrichment is the caller the memo exists for.
func TestEnrich_UnresolvableUserCostsOneRequest(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", rateLimitFirst(0, &calls, usersInfoHandler(t, testUsers())))
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	for range 5 {
		row := map[string]any{"user": "U999"}
		r.Enrich(context.Background(), row)
		if _, ok := row["user_name"]; ok {
			t.Fatal("an unresolvable user must not gain a user_name")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("got %d users.info requests for 5 rows, want 1", got)
	}
}

// Lookups run in the printer's emit path. Once users.info has exhausted its
// retries, a later ID must not start another run of Retry-After sleeps.
func TestLookupUser_ExhaustedRateLimitStopsLaterLookups(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeRateLimited(w)
	})
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())
	ctx := context.Background()

	_, found, err := r.LookupUser(ctx, "U001")
	var exhausted *api.RateLimitExhaustedError
	if found || !errors.As(err, &exhausted) {
		t.Fatalf("found=%v err=%v, want a RateLimitExhaustedError", found, err)
	}
	afterFirst := calls.Load()
	if afterFirst != 5 {
		t.Fatalf("got %d users.info requests, want 5 attempts", afterFirst)
	}

	for _, id := range []string{"U001", "U002", "U003"} {
		_, found, err := r.LookupUser(ctx, id)
		if found || !errors.As(err, &exhausted) {
			t.Errorf("%s: found=%v err=%v, want the exhausted rate limit", id, found, err)
		}
	}
	if got := calls.Load(); got != afterFirst {
		t.Errorf("got %d more users.info requests after exhaustion, want 0", got-afterFirst)
	}
}

// FetchUser is for a command whose answer is the user. A best-effort lookup
// that failed earlier - a memoized error for the same ID, or the stop an
// exhausted rate limit set - must not answer for it.
func TestFetchUser_IsNotRefusedByEarlierLookupFailures(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var healthy atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if !healthy.Load() {
			writeRateLimited(w)
			return
		}
		usersInfoHandler(t, testUsers())(w, req)
	})
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())
	ctx := context.Background()

	// A fatal_error memo for U002, then the stop.
	r.mu.Lock()
	r.failedUsers = map[string]error{"U002": slack.SlackErrorResponse{Err: "fatal_error"}}
	r.mu.Unlock()
	if _, found, _ := r.LookupUser(ctx, "U001"); found {
		t.Fatal("a rate-limited lookup must miss")
	}
	if _, found, _ := r.LookupUser(ctx, "U003"); found {
		t.Fatal("LookupUser must stay stopped")
	}
	stopped := calls.Load()

	healthy.Store(true)
	for _, id := range []string{"U001", "U002"} {
		user, err := r.FetchUser(ctx, id)
		if err != nil || user.ID != id {
			t.Errorf("FetchUser(%s) = %q, %v, want the user", id, user.ID, err)
		}
	}
	if got := calls.Load() - stopped; got != 2 {
		t.Errorf("got %d users.info requests from two FetchUser calls, want 2", got)
	}
}

// A dead context says nothing about the user. Memoizing it would make a later
// lookup on a live context miss.
func TestLookupUser_DeadContextIsNotMemoized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", usersInfoHandler(t, testUsers()))
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if _, found, err := r.LookupUser(dead, "U001"); found || !errors.Is(err, context.Canceled) {
		t.Fatalf("found=%v err=%v, want context.Canceled", found, err)
	}
	if _, found, err := r.LookupUser(context.Background(), "U001"); err != nil || !found {
		t.Errorf("found=%v err=%v, want the user on a live context", found, err)
	}
}

func TestLookupChannel_FallbackIsTraced(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.info", conversationsInfoHandler(t, map[string]string{"C01ABC": "general"}))
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	var trace bytes.Buffer
	ctx := api.WithTracer(context.Background(), api.NewJSONLinesTracer(&trace))
	if name, found := r.LookupChannel(ctx, "C01ABC"); !found || name != "general" {
		t.Fatalf("got %q found=%v, want general", name, found)
	}
	if got := countEvents(traceEvents(t, &trace), "call", "conversations.info"); got != 1 {
		t.Errorf("got %d conversations.info call events, want 1: %s", got, trace.String())
	}
}

func TestLookupChannel_RetriesRateLimit(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.info",
		rateLimitFirst(1, &calls, conversationsInfoHandler(t, map[string]string{"C01ABC": "general"})))
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	name, found := r.LookupChannel(context.Background(), "C01ABC")
	if !found || name != "general" {
		t.Fatalf("got %q found=%v, want general after one retry", name, found)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("got %d conversations.info requests, want 2", got)
	}
}

func TestLookupChannel_ExhaustedRateLimitStopsLaterLookups(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeRateLimited(w)
	})
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())
	ctx := context.Background()

	if _, found := r.LookupChannel(ctx, "C01ABC"); found {
		t.Fatal("a rate-limited lookup must miss")
	}
	afterFirst := calls.Load()
	if afterFirst != 5 {
		t.Fatalf("got %d conversations.info requests, want 5 attempts", afterFirst)
	}
	for _, id := range []string{"C01ABC", "C02DEF"} {
		if _, found := r.LookupChannel(ctx, id); found {
			t.Errorf("%s: found after the method was rate limited out", id)
		}
	}
	if got := calls.Load(); got != afterFirst {
		t.Errorf("got %d more conversations.info requests after exhaustion, want 0", got-afterFirst)
	}
}

func TestLookupChannel_DeadContextIsNotMemoized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.info", conversationsInfoHandler(t, map[string]string{"C01ABC": "general"}))
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if _, found := r.LookupChannel(dead, "C01ABC"); found {
		t.Fatal("a dead context must miss")
	}
	if name, found := r.LookupChannel(context.Background(), "C01ABC"); !found || name != "general" {
		t.Errorf("got %q found=%v, want general on a live context", name, found)
	}
}

// usersListPages serves users.list as fixed pages keyed by cursor, recording
// the cursor and limit of every request. pages[""] is the first page; each
// page names the cursor of the next.
type usersListPages struct {
	pages   map[string]usersPage
	cursors []string
	limits  []string
	// failOnce maps a cursor to a response sent the first time it is asked for.
	failOnce map[string]string
}

type usersPage struct {
	users []slack.User
	next  string
}

func (p *usersListPages) handler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	cursor := r.FormValue("cursor")
	p.cursors = append(p.cursors, cursor)
	p.limits = append(p.limits, r.FormValue("limit"))
	if code, ok := p.failOnce[cursor]; ok {
		delete(p.failOnce, cursor)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
		return
	}
	page := p.pages[cursor]
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":                true,
		"members":           page.users,
		"response_metadata": map[string]string{"next_cursor": page.next},
	})
}

func twoUserPages() *usersListPages {
	return &usersListPages{pages: map[string]usersPage{
		"":   {users: []slack.User{{ID: "U01XYZ", Name: "alice"}}, next: "c2"},
		"c2": {users: []slack.User{{ID: "U02MGR", Name: "bob"}}},
	}}
}

// The directory load used to run inside slack-go's GetUsersContext, which
// emits nothing: a ten-minute walk produced zero trace lines.
func TestResolveUser_BulkLoadIsTraced(t *testing.T) {
	pages := twoUserPages()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", pages.handler)
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	var trace bytes.Buffer
	ctx := api.WithTracer(context.Background(), api.NewJSONLinesTracer(&trace))
	id, err := r.ResolveUser(ctx, "@bob")
	if err != nil {
		t.Fatal(err)
	}
	if id != "U02MGR" {
		t.Errorf("got %q, want U02MGR (a user on the second page)", id)
	}
	if got := countEvents(traceEvents(t, &trace), "page", "users.list"); got != 2 {
		t.Errorf("got %d users.list page events, want 2: %s", got, trace.String())
	}
	for i, limit := range pages.limits {
		if limit != "200" {
			t.Errorf("request %d sent limit=%q, want 200", i, limit)
		}
	}
}

// slack-go's paginator carries its cursor in the value. A retried page must
// repeat the same cursor, not restart at page 1.
func TestResolveUser_BulkLoadRetryRepeatsThePage(t *testing.T) {
	t.Parallel()
	pages := twoUserPages()
	pages.failOnce = map[string]string{"c2": "ratelimited"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", pages.handler)
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	for _, name := range []string{"@alice", "@bob"} {
		if _, err := r.ResolveUser(context.Background(), name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	want := []string{"", "c2", "c2"}
	if strings.Join(pages.cursors, ",") != strings.Join(want, ",") {
		t.Errorf("got cursors %q, want %q", pages.cursors, want)
	}
}

// A load that fails partway writes nothing, so the next run does not start
// from half a directory.
func TestResolveUser_FailedBulkLoadWritesNoCache(t *testing.T) {
	pages := twoUserPages()
	pages.failOnce = map[string]string{"c2": "fatal_error"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", pages.handler)
	r := NewResolver(newTestClient(t, mux), "T01ABC", t.TempDir())

	if _, err := r.ResolveUser(context.Background(), "@alice"); err == nil {
		t.Fatal("a name on page 1 resolved from a load that failed on page 2")
	}
	if fc, err := r.loadUserFileCache(); err == nil && fc != nil {
		t.Errorf("a failed load wrote a file cache holding %d users", len(fc.Users))
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.users != nil {
		t.Errorf("a failed load left %d users in memory", len(r.users))
	}
}
