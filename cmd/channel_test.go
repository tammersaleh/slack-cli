package cmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// mockResult holds the captured output from a CLI command run against a mock server.
type mockResult struct {
	stdout string
	stderr string
	err    error
}

// runWithMockFull runs a CLI command against a mock Slack API server.
// Returns stdout, stderr, and any error from the command.
// isolateTestEnv clears Slack-related env vars that could leak from the
// developer's shell into tests (e.g. SLACK_WORKSPACE set by direnv).
// Tokens are left alone - helpers and tests set SLACK_TOKEN /
// SLACK_USER_TOKEN explicitly for the scenario under test.
func isolateTestEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"SLACK_COOKIE",
		"SLACK_WORKSPACE",
		"SLACK_WORKSPACE_ORG",
		"SLACK_FIELDS",
		"SLACK_CACHE_TTL",
		"SLACK_CLIENT_ID",
		"SLACK_CLIENT_SECRET",
		"SLACK_SAFE_STORAGE_PASSWORD",
	} {
		t.Setenv(v, "")
	}
}

func runWithMockFull(t *testing.T, handler http.Handler, args ...string) mockResult {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	isolateTestEnv(t)
	t.Setenv("SLACK_TOKEN", "xoxb-test")
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer

	parser, err := kong.New(&cli,
		kong.Name("slack"),
		kong.Exit(func(int) {}),
	)
	if err != nil {
		t.Fatal(err)
	}

	kctx, err := parser.Parse(args)
	if err != nil {
		return mockResult{err: err}
	}

	cli.SetOutput(&outBuf, &errBuf)

	runErr := kctx.Run(&cli)
	return mockResult{stdout: outBuf.String(), stderr: errBuf.String(), err: runErr}
}

// runWithMock runs a CLI command against a mock Slack API server.
// Returns stdout content and any error from the command.
func runWithMock(t *testing.T, handler http.Handler, args ...string) (string, error) {
	t.Helper()
	r := runWithMockFull(t, handler, args...)
	return r.stdout, r.err
}

// emptyMux returns a ServeMux with no handlers for commands that don't call APIs.
func emptyMux() *http.ServeMux {
	return http.NewServeMux()
}

func nonEmptyLines(s string) []string {
	var result []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func parseJSON(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", line, err)
	}
	return m
}

// channelListEndpoints are the two endpoints `channel list` can source from:
// users.conversations for the member-only default, conversations.list when
// --include-non-member asks for the org-wide list.
var channelListEndpoints = []string{"users.conversations", "conversations.list"}

// listMux serves one page of channels at endpoint and fails the test if the
// command calls the other channel-list endpoint instead. Which endpoint a
// command hits is the behavior under test here, not an implementation detail:
// sourcing the member-only default from conversations.list means paginating
// every channel in the org to emit the user's own.
func listMux(t *testing.T, endpoint string, channels []map[string]any) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/"+endpoint, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          channels,
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	for _, other := range channelListEndpoints {
		if other == endpoint {
			continue
		}
		mux.HandleFunc("/api/"+other, func(w http.ResponseWriter, _ *http.Request) {
			t.Errorf("command called %s, want %s", other, endpoint)
			w.WriteHeader(http.StatusInternalServerError)
		})
	}
	return mux
}

func TestChannelList_PaginationFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		ok   bool
	}{
		{"defaults", []string{"channel", "list"}, true},
		{"with limit", []string{"channel", "list", "--limit", "50"}, true},
		{"with cursor", []string{"channel", "list", "--cursor", "abc"}, true},
		{"with all", []string{"channel", "list", "--all"}, true},
		{"with type", []string{"channel", "list", "--type", "private"}, true},
		{"with query", []string{"channel", "list", "--query", "eng"}, true},
		{"invalid type", []string{"channel", "list", "--type", "bogus"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cli cmd.CLI
			parser, _ := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
			_, err := parser.Parse(tt.args)
			if tt.ok && err != nil {
				t.Errorf("expected success, got: %v", err)
			}
			if !tt.ok && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestChannelList_DefaultTypeIsAll(t *testing.T) {
	// Default --type should request all four channel kinds, not just
	// public_channel, on either endpoint.
	for _, tc := range []struct {
		name     string
		endpoint string
		args     []string
	}{
		{"member-only default", "users.conversations", []string{"channel", "list"}},
		{"include non-member", "conversations.list", []string{"channel", "list", "--include-non-member"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotTypes string
			mux := http.NewServeMux()
			mux.HandleFunc("/api/"+tc.endpoint, func(w http.ResponseWriter, r *http.Request) {
				gotTypes = r.FormValue("types")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":                true,
					"channels":          []map[string]any{},
					"response_metadata": map[string]string{"next_cursor": ""},
				})
			})

			if _, err := runWithMock(t, mux, tc.args...); err != nil {
				t.Fatal(err)
			}

			want := "public_channel,private_channel,mpim,im"
			if gotTypes != want {
				t.Errorf("default types = %q, want %q", gotTypes, want)
			}
		})
	}
}

func TestChannelList_MemberOnlyDefaultUsesUsersConversations(t *testing.T) {
	// The member-only default must source from users.conversations, which
	// returns only the user's own conversations. conversations.list returns
	// every channel in the org, so filtering it client-side costs one request
	// per org page to emit the user's handful - ~35 minutes on a large
	// Enterprise Grid org.
	mux := listMux(t, "users.conversations", []map[string]any{
		{"id": "C01", "name": "general", "is_channel": true},
		{"id": "C02", "name": "random", "is_channel": true},
	})

	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 channels + meta, got %d lines:\n%s", len(lines), out)
	}
}

func TestChannelList_IncludeNonMemberUsesConversationsList(t *testing.T) {
	// --include-non-member genuinely needs the org-wide list, which
	// users.conversations cannot provide.
	mux := listMux(t, "conversations.list", []map[string]any{
		{"id": "C01", "name": "general", "is_channel": true, "is_member": true},
		{"id": "C02", "name": "external", "is_channel": true, "is_member": false},
	})

	out, err := runWithMock(t, mux, "channel", "list", "--include-non-member")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 channels + meta, got %d lines:\n%s", len(lines), out)
	}
}

func TestChannelList_UsersConversationsRowsSurviveFalseIsMember(t *testing.T) {
	// users.conversations reports is_member=false on every conversation it
	// returns, including public channels the user is plainly in (verified live
	// against a Grid workspace). Everything it returns is a conversation the
	// user is in by definition, so no client-side member filter may run on
	// this path - one would drop the entire result set.
	mux := listMux(t, "users.conversations", []map[string]any{
		{"id": "C01", "name": "general", "is_channel": true, "is_member": false},
		{"id": "G02", "name": "secret", "is_group": true, "is_private": true, "is_member": false},
		{"id": "D03", "is_im": true, "is_member": false, "user": "U01"},
	})

	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 4 {
		t.Fatalf("expected 3 channels + meta, got %d lines:\n%s", len(lines), out)
	}
	for i, wantID := range []string{"C01", "G02", "D03"} {
		if got := parseJSON(t, lines[i])["id"]; got != wantID {
			t.Errorf("row %d id = %q, want %q", i, got, wantID)
		}
	}
}

func TestChannelList_MockAPI(t *testing.T) {
	mux := listMux(t, "users.conversations", []map[string]any{
		{"id": "C01", "name": "general", "is_channel": true, "num_members": 10},
		{"id": "C02", "name": "random", "is_channel": true, "num_members": 5},
	})

	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 channels + meta), got %d:\n%s", len(lines), out)
	}

	ch := parseJSON(t, lines[0])
	if ch["name"] != "general" {
		t.Errorf("expected first channel 'general', got %q", ch["name"])
	}

	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != false {
		t.Error("expected has_more=false")
	}
}

func TestChannelList_QueryFilter(t *testing.T) {
	mux := listMux(t, "users.conversations", []map[string]any{
		{"id": "C01", "name": "general", "is_channel": true},
		{"id": "C02", "name": "engineering", "is_channel": true},
		{"id": "C03", "name": "random", "is_channel": true},
	})

	out, err := runWithMock(t, mux, "channel", "list", "--query", "eng")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 1 matching channel + _meta
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 channel + meta), got %d:\n%s", len(lines), out)
	}
}

// pagedQueryMux serves three pages where the only match for "needle" is on page
// two, and counts requests. A client-side --query that stops after page one
// reports zero matches for a channel that exists.
//
// The third page matters: it means a walk that should have stopped at one page
// is detectable by request count. With only two pages, resuming from page two
// makes exactly one request whether or not the command widened the walk, so a
// test asserting "one request" would pass with the guard removed.
func pagedQueryMux(t *testing.T, endpoint string) (*http.ServeMux, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	pages := map[string]struct {
		channels []map[string]any
		next     string
	}{
		"":            {[]map[string]any{{"id": "C01", "name": "general"}}, "page2cursor"},
		"page2cursor": {[]map[string]any{{"id": "C02", "name": "needle-channel"}}, "page3cursor"},
		"page3cursor": {[]map[string]any{{"id": "C03", "name": "random"}}, ""},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/"+endpoint, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		page, ok := pages[r.FormValue("cursor")]
		if !ok {
			t.Errorf("unexpected cursor %q", r.FormValue("cursor"))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          page.channels,
			"response_metadata": map[string]string{"next_cursor": page.next},
		})
	})
	return mux, &calls
}

func TestChannelList_QuerySearchesEveryPageOnDefaultPath(t *testing.T) {
	// A client-side --query over page one only turns "exists on page 2" into
	// "does not exist" - a silent false negative on the most common lookup
	// path, and the channel_not_found hint recommends this very command.
	// Walking every page is affordable on the member-only default now that it
	// reads users.conversations, so --query does that rather than lie.
	mux, calls := pagedQueryMux(t, "users.conversations")

	out, err := runWithMock(t, mux, "channel", "list", "--query", "needle")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected the page-2 match + meta, got %d lines:\n%s", len(lines), out)
	}
	if got := parseJSON(t, lines[0])["name"]; got != "needle-channel" {
		t.Errorf("expected the match from page 2, got %q", got)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("made %d requests, want 3 - --query must not stop at page one", got)
	}

	meta := parseJSON(t, lines[1])["_meta"].(map[string]any)
	if meta["filter_exhaustive"] != true {
		t.Errorf("expected filter_exhaustive=true after searching every page, got %v", meta["filter_exhaustive"])
	}
	if meta["has_more"] != false {
		t.Errorf("expected has_more=false, got %v", meta["has_more"])
	}
}

func TestChannelList_QueryDoesNotImplyAllForNonMemberScan(t *testing.T) {
	// --include-non-member reads conversations.list, a whole-workspace walk
	// that costs minutes. Searching every page there is not something to do
	// behind the caller's back, so that path stays one page and says so.
	mux, calls := pagedQueryMux(t, "conversations.list")

	out, err := runWithMock(t, mux, "channel", "list", "--query", "needle", "--include-non-member")
	if err != nil {
		t.Fatal(err)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("made %d requests, want 1 - the org-wide scan must stay opt-in", got)
	}
	meta := metaTrailer(t, out)
	if meta["filter_exhaustive"] != false {
		t.Errorf("expected filter_exhaustive=false on a partial search, got %v", meta["filter_exhaustive"])
	}
	if meta["has_more"] != true {
		t.Errorf("expected has_more=true, got %v", meta["has_more"])
	}
}

func TestChannelList_FilterExhaustiveAbsentWithoutQuery(t *testing.T) {
	// The marker describes a --query filter. With no query there is no filter
	// to describe, so the key must not appear at all.
	mux, _ := pagedListMux(t, "users.conversations")

	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}
	meta := metaTrailer(t, out)
	if _, ok := meta["filter_exhaustive"]; ok {
		t.Errorf("expected no filter_exhaustive key without --query, got %v", meta["filter_exhaustive"])
	}
}

func TestChannelList_QueryNotExhaustiveWhenCursorGiven(t *testing.T) {
	// --cursor is a resume point and is mutually exclusive with --all, so
	// --query cannot widen it to a full walk. Resuming at page two leaves a
	// third page unfetched: one request proves the walk stayed narrow, and the
	// trailer must admit the search was partial.
	mux, calls := pagedQueryMux(t, "users.conversations")

	out, err := runWithMock(t, mux, "channel", "list", "--query", "needle", "--cursor", "page2cursor")
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d requests, want 1 - a resume must not widen into a full walk", got)
	}
	meta := metaTrailer(t, out)
	if meta["filter_exhaustive"] != false {
		t.Errorf("expected filter_exhaustive=false with a page left unfetched, got %v", meta["filter_exhaustive"])
	}
	if meta["has_more"] != true {
		t.Errorf("expected has_more=true with a page left unfetched, got %v", meta["has_more"])
	}
}

// unreadMux serves a member-only channel list plus a client.counts response,
// and counts calls to each. countRows maps a conversation id to its
// has_unreads/mention_count state; ids absent from it are absent from
// client.counts entirely, which is how Slack reports a DM you don't have open.
func unreadMux(t *testing.T, channels []map[string]any, counts map[string]any) (*http.ServeMux, *atomic.Int32) {
	t.Helper()
	var countsCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          channels,
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/client.counts", func(w http.ResponseWriter, _ *http.Request) {
		countsCalls.Add(1)
		payload := map[string]any{"ok": true}
		for k, v := range counts {
			payload[k] = v
		}
		_ = json.NewEncoder(w).Encode(payload)
	})
	return mux, &countsCalls
}

func TestChannelList_HasUnreadFiltersOnClientCounts(t *testing.T) {
	// --has-unread used to filter on slack.Channel.UnreadCount, which no list
	// endpoint populates, so it silently matched nothing and read as "you have
	// no unread channels". Unread state comes from the internal client.counts
	// endpoint instead - one request for the whole workspace.
	mux, countsCalls := unreadMux(t,
		[]map[string]any{
			{"id": "C01", "name": "unread-channel", "is_channel": true},
			{"id": "C02", "name": "read-channel", "is_channel": true},
			{"id": "D03", "is_im": true, "user": "U01"},
			{"id": "D04", "is_im": true, "user": "U02"},
		},
		map[string]any{
			"channels": []map[string]any{
				{"id": "C01", "has_unreads": true, "mention_count": 2, "last_read": "1709251200.000100"},
				{"id": "C02", "has_unreads": false, "mention_count": 0, "last_read": "1709251300.000200"},
			},
			// D04 is deliberately absent: a DM with no entry has no unread
			// badge, so it must not match.
			"ims": []map[string]any{
				{"id": "D03", "has_unreads": true, "mention_count": 0, "last_read": "1709251400.000300"},
			},
		})

	out, err := runWithMockSession(t, mux, "channel", "list", "--has-unread")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected the 2 unread conversations + meta, got %d lines:\n%s", len(lines), out)
	}
	for i, wantID := range []string{"C01", "D03"} {
		if got := parseJSON(t, lines[i])["id"]; got != wantID {
			t.Errorf("row %d id = %q, want %q", i, got, wantID)
		}
	}
	if got := countsCalls.Load(); got != 1 {
		t.Errorf("called client.counts %d times, want 1 for the whole listing", got)
	}
}

func TestChannelList_HasUnreadSearchesEveryPage(t *testing.T) {
	// --has-unread is a client-side filter like --query, so stopping at page one
	// would report a handful of unread channels when the workspace has many -
	// wrong in the same way, and just as quiet about it.
	var listCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		listCalls.Add(1)
		_ = r.ParseForm()
		if r.FormValue("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				"channels":          []map[string]any{{"id": "C01", "name": "read-channel", "is_channel": true}},
				"response_metadata": map[string]string{"next_cursor": "page2cursor"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          []map[string]any{{"id": "C02", "name": "unread-on-page-two", "is_channel": true}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/client.counts", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01", "has_unreads": false},
				{"id": "C02", "has_unreads": true},
			},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "list", "--has-unread")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected the page-two unread channel + meta, got %d lines:\n%s", len(lines), out)
	}
	if got := parseJSON(t, lines[0])["name"]; got != "unread-on-page-two" {
		t.Errorf("expected the match from page two, got %q", got)
	}
	if got := listCalls.Load(); got != 2 {
		t.Errorf("fetched %d pages, want 2 - --has-unread must not stop at page one", got)
	}
	meta := metaTrailer(t, out)
	if meta["filter_exhaustive"] != true {
		t.Errorf("expected filter_exhaustive=true after searching every page, got %v", meta["filter_exhaustive"])
	}
}

func TestChannelList_HasUnreadReportsUnreadState(t *testing.T) {
	// A caller that filtered on unread state should be able to see it. The rows
	// carry the fields the filter used rather than making the caller guess why
	// a channel matched.
	mux, _ := unreadMux(t,
		[]map[string]any{{"id": "C01", "name": "unread-channel", "is_channel": true}},
		map[string]any{"channels": []map[string]any{
			{"id": "C01", "has_unreads": true, "mention_count": 3, "last_read": "1709251200.000100"},
		}})

	out, err := runWithMockSession(t, mux, "channel", "list", "--has-unread")
	if err != nil {
		t.Fatal(err)
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	if row["has_unreads"] != true {
		t.Errorf("has_unreads = %v, want true", row["has_unreads"])
	}
	if row["mention_count"] != float64(3) {
		t.Errorf("mention_count = %v, want 3", row["mention_count"])
	}
	if row["last_read"] != "1709251200.000100" {
		t.Errorf("last_read = %v, want the value from client.counts", row["last_read"])
	}
}

func TestChannelList_WithoutHasUnreadSkipsClientCounts(t *testing.T) {
	// client.counts is an extra request and needs a session token. A plain
	// listing must not pay for it.
	mux, countsCalls := unreadMux(t,
		[]map[string]any{{"id": "C01", "name": "general", "is_channel": true}},
		map[string]any{"channels": []map[string]any{}})

	if _, err := runWithMockSession(t, mux, "channel", "list"); err != nil {
		t.Fatal(err)
	}
	if got := countsCalls.Load(); got != 0 {
		t.Errorf("called client.counts %d times without --has-unread, want 0", got)
	}
}

func TestChannelList_HasUnreadRequiresSessionToken(t *testing.T) {
	// client.counts is an internal endpoint that only accepts a session
	// (xoxc-) token. With a bot token the command must say so rather than
	// filter on nothing and report an empty workspace.
	mux, countsCalls := unreadMux(t,
		[]map[string]any{{"id": "C01", "name": "general", "is_channel": true}},
		map[string]any{"channels": []map[string]any{}})

	r := runWithMockFull(t, mux, "channel", "list", "--has-unread")
	if r.err == nil {
		t.Fatal("expected --has-unread with a bot token to fail loudly")
	}
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected an *output.Error, got %#v", r.err)
	}
	if oErr.Err != "session_token_required" {
		t.Errorf("error = %q, want session_token_required", oErr.Err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("exit code = %d, want %d", oErr.Code, output.ExitAuth)
	}
	if got := countsCalls.Load(); got != 0 {
		t.Errorf("called client.counts %d times with an unusable token, want 0", got)
	}
	if strings.TrimSpace(r.stdout) != "" {
		t.Errorf("expected no stdout rows when the filter cannot run, got %q", r.stdout)
	}
}

// pagedListMux serves two cursor-linked pages at endpoint and counts the
// requests, so a test can assert both the page walk and that no extra request
// was made.
func pagedListMux(t *testing.T, endpoint string) (*http.ServeMux, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/"+endpoint, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.FormValue("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				"channels":          []map[string]any{{"id": "C01", "name": "page1"}},
				"response_metadata": map[string]string{"next_cursor": "page2cursor"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          []map[string]any{{"id": "C02", "name": "page2"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	return mux, &calls
}

func TestChannelList_Pagination(t *testing.T) {
	mux, calls := pagedListMux(t, "users.conversations")

	// Without --all, should return first page only with has_more=true.
	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
	meta := parseJSON(t, lines[1])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != true {
		t.Error("expected has_more=true for first page")
	}
	if m["next_cursor"] != "page2cursor" {
		t.Errorf("expected next_cursor='page2cursor', got %q", m["next_cursor"])
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d requests without --all, want 1", got)
	}
}

func TestChannelList_AllPages(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		args     []string
	}{
		{"member-only default", "users.conversations", []string{"channel", "list", "--all"}},
		{"include non-member", "conversations.list", []string{"channel", "list", "--all", "--include-non-member"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, calls := pagedListMux(t, tc.endpoint)

			out, err := runWithMock(t, mux, tc.args...)
			if err != nil {
				t.Fatal(err)
			}

			lines := nonEmptyLines(out)
			// 2 channels from 2 pages + _meta
			if len(lines) != 3 {
				t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
			}
			meta := parseJSON(t, lines[2])
			m := meta["_meta"].(map[string]any)
			if m["has_more"] != false {
				t.Error("expected has_more=false after all pages")
			}
			if got := calls.Load(); got != 2 {
				t.Errorf("made %d requests, want 2", got)
			}
		})
	}
}

func TestChannelList_UsersConversationsNormalizesMissingFields(t *testing.T) {
	// users.conversations omits is_member and num_members from the wire
	// entirely (verified against a live Grid workspace). slack-go decodes both
	// to zero values, so without repair every default row would claim
	// is_member=false and num_members=0.
	//
	// Membership is what put a conversation in this result set, so it is
	// reported as true - group DMs included. Member counts cannot be inferred,
	// so the key is dropped rather than reported as zero.
	mux := listMux(t, "users.conversations", []map[string]any{
		{"id": "C01", "name": "general", "is_channel": true},
		{"id": "G02", "name": "secret", "is_group": true, "is_private": true},
		{"id": "C03", "name": "mpdm-a--b--c-1", "is_mpim": true, "is_private": true},
		{"id": "D04", "is_im": true, "user": "U01"},
	})

	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 5 {
		t.Fatalf("expected 4 rows + meta, got %d lines:\n%s", len(lines), out)
	}
	for i, want := range []struct {
		id       string
		isMember any
	}{
		{"C01", true},  // public channel
		{"G02", true},  // private channel
		{"C03", true},  // mpim - being in a group DM is a real membership
		{"D04", false}, // im - Slack reports no membership for 1:1 DMs
	} {
		row := parseJSON(t, lines[i])
		if row["id"] != want.id {
			t.Fatalf("row %d id = %q, want %q", i, row["id"], want.id)
		}
		if row["is_member"] != want.isMember {
			t.Errorf("%s is_member = %v, want %v", want.id, row["is_member"], want.isMember)
		}
		if got, ok := row["num_members"]; ok {
			t.Errorf("%s reported num_members=%v; the endpoint does not return it, so the key must be absent", want.id, got)
		}
	}
}

func TestChannelList_ConversationsListReportsFieldsVerbatim(t *testing.T) {
	// The --include-non-member path reads conversations.list, which does send
	// is_member and num_members. Those must pass through untouched - the
	// normalization above is specific to what users.conversations omits.
	mux := listMux(t, "conversations.list", []map[string]any{
		{"id": "C01", "name": "general", "is_channel": true, "is_member": true, "num_members": 42},
		{"id": "C02", "name": "external", "is_channel": true, "is_member": false, "num_members": 7},
	})

	out, err := runWithMock(t, mux, "channel", "list", "--include-non-member")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d lines:\n%s", len(lines), out)
	}
	for i, want := range []struct {
		id         string
		isMember   bool
		numMembers float64
	}{
		{"C01", true, 42},
		{"C02", false, 7},
	} {
		row := parseJSON(t, lines[i])
		if row["is_member"] != want.isMember {
			t.Errorf("%s is_member = %v, want %v", want.id, row["is_member"], want.isMember)
		}
		if row["num_members"] != want.numMembers {
			t.Errorf("%s num_members = %v, want %v", want.id, row["num_members"], want.numMembers)
		}
	}
}

func TestChannelList_IMsAreReturnedByType(t *testing.T) {
	// IMs have no "member" concept - Slack reports is_member=false for them on
	// every endpoint. `--type im` must still return DMs.
	mux := listMux(t, "users.conversations", []map[string]any{
		{"id": "D01", "is_im": true, "is_member": false, "user": "U01"},
		{"id": "D02", "is_im": true, "is_member": false, "user": "U02"},
	})

	out, err := runWithMock(t, mux, "channel", "list", "--type", "im")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 IM rows + meta, got %d lines:\n%s", len(lines), out)
	}
	first := parseJSON(t, lines[0])
	if first["id"] != "D01" {
		t.Errorf("expected first id=D01, got %q", first["id"])
	}
}

func TestChannelList_IncludeNonMemberEmitsEveryRow(t *testing.T) {
	// --include-non-member asks for the org-wide list, so nothing about
	// membership is filtered out of what conversations.list returns.
	mux := listMux(t, "conversations.list", []map[string]any{
		{"id": "C01", "name": "general", "is_channel": true, "is_member": true},
		{"id": "C02", "name": "external", "is_channel": true, "is_member": false},
	})

	out, err := runWithMock(t, mux, "channel", "list", "--include-non-member")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 channels + meta, got %d lines:\n%s", len(lines), out)
	}
	if got := parseJSON(t, lines[1])["name"]; got != "external" {
		t.Errorf("expected non-member channel emitted, got second row name=%q", got)
	}
}

func TestChannelInfo_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"channels": []map[string]any{{"id": "C01", "name": "general"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01", "name": "general", "is_channel": true, "num_members": 42},
		})
	})

	out, err := runWithMock(t, mux, "channel", "info", "#general")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	ch := parseJSON(t, lines[0])
	if ch["input"] != "#general" {
		t.Errorf("expected input='#general', got %q", ch["input"])
	}
}

func TestChannelInfo_InlineError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"channels": []map[string]any{{"id": "C01", "name": "general"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01", "name": "general"},
		})
	})

	out, err := runWithMock(t, mux, "channel", "info", "#general", "#nonexistent")
	// Should return error since one channel failed.
	if err == nil {
		t.Fatal("expected error for partial failure")
	}

	lines := nonEmptyLines(out)
	// 1 success + 1 error + _meta = 3 lines
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}

	errLine := parseJSON(t, lines[1])
	if errLine["error"] != "channel_not_found" {
		t.Errorf("expected error='channel_not_found', got %q", errLine["error"])
	}

	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["error_count"] != float64(1) {
		t.Errorf("expected error_count=1, got %v", m["error_count"])
	}
}

func TestChannelMembers_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.members", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"members":          []string{"U01", "U02", "U03"},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "channel", "members", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (3 members + meta), got %d:\n%s", len(lines), out)
	}

	member := parseJSON(t, lines[0])
	if member["user_id"] != "U01" {
		t.Errorf("expected first member 'U01', got %q", member["user_id"])
	}
}

func TestChannelManagers_Basic(t *testing.T) {
	var gotEntityID string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin.roles.entity.listAssignments", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotEntityID = r.FormValue("entity_id")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"role_assignments": []map[string]any{
				{"role_id": "Rl0A", "users": []string{"U01XYZ", "U02ABC"}},
			},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "managers", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	if gotEntityID != "C01ABC" {
		t.Errorf("expected entity_id='C01ABC' in request, got %q", gotEntityID)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 managers + meta), got %d:\n%s", len(lines), out)
	}

	first := parseJSON(t, lines[0])
	if first["user_id"] != "U01XYZ" {
		t.Errorf("expected first user_id='U01XYZ', got %q", first["user_id"])
	}
	if first["role_id"] != "Rl0A" {
		t.Errorf("expected role_id='Rl0A', got %q", first["role_id"])
	}
	second := parseJSON(t, lines[1])
	if second["user_id"] != "U02ABC" {
		t.Errorf("expected second user_id='U02ABC', got %q", second["user_id"])
	}
}

// TestChannelManagers_EmitsAllRoles verifies the command does not hard-filter
// to one role ID: every returned assignment is emitted, one row per user,
// carrying its own role_id. Guards against silently dropping managers if
// Slack's channel-manager role ID ever differs from the observed Rl0A.
func TestChannelManagers_EmitsAllRoles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin.roles.entity.listAssignments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"role_assignments": []map[string]any{
				{"role_id": "Rl0A", "users": []string{"U01"}},
				{"role_id": "Rl99", "users": []string{"U02"}},
				{"role_id": "RlEMPTY", "users": []string{}},
			},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "managers", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 users + meta; empty assignment skipped), got %d:\n%s", len(lines), out)
	}
	if r0 := parseJSON(t, lines[0]); r0["user_id"] != "U01" || r0["role_id"] != "Rl0A" {
		t.Errorf("row 0 = %v, want U01/Rl0A", r0)
	}
	if r1 := parseJSON(t, lines[1]); r1["user_id"] != "U02" || r1["role_id"] != "Rl99" {
		t.Errorf("row 1 = %v, want U02/Rl99", r1)
	}
}

func TestChannelManagers_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin.roles.entity.listAssignments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"role_assignments": []map[string]any{},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "managers", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (meta only), got %d:\n%s", len(lines), out)
	}
	meta := parseJSON(t, lines[0])["_meta"].(map[string]any)
	if meta["error_count"] != nil {
		t.Errorf("empty managers must not be an error, got error_count=%v", meta["error_count"])
	}
}

// TestChannelManagers_ByName exercises the two-client split: a channel *name*
// is resolved via conversations.list (public client), and the resolved ID is
// forwarded as entity_id to the internal endpoint (session client).
func TestChannelManagers_ByName(t *testing.T) {
	var gotEntityID string
	listCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		listCalled = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01ABC", "name": "approvals", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/admin.roles.entity.listAssignments", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotEntityID = r.FormValue("entity_id")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"role_assignments": []map[string]any{
				{"role_id": "Rl0A", "users": []string{"U01XYZ"}},
			},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "managers", "#approvals")
	if err != nil {
		t.Fatal(err)
	}

	if !listCalled {
		t.Error("expected conversations.list to be called to resolve the channel name")
	}
	if gotEntityID != "C01ABC" {
		t.Errorf("expected resolved entity_id='C01ABC', got %q", gotEntityID)
	}
	if first := parseJSON(t, nonEmptyLines(out)[0]); first["user_id"] != "U01XYZ" {
		t.Errorf("expected manager 'U01XYZ', got %q", first["user_id"])
	}
}

func TestChannelManagers_SessionTokenRequired(t *testing.T) {
	// A regular xoxb- token must be rejected as session_token_required (exit 2)
	// before any API call - and as *output.Error so main.go prints it to stderr.
	mux := http.NewServeMux()
	r := runWithMockFull(t, mux, "channel", "managers", "C01ABC")
	if r.err == nil {
		t.Fatal("expected error for non-session token")
	}
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", r.err, r.err)
	}
	if oErr.Err != "session_token_required" {
		t.Errorf("expected error 'session_token_required', got %q", oErr.Err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("expected exit code %d (auth), got %d", output.ExitAuth, oErr.Code)
	}
}

func TestChannelInfo_PartialFailure_NoStderr(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"channels": []map[string]any{{"id": "C01", "name": "general"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01", "name": "general"},
		})
	})

	r := runWithMockFull(t, mux, "channel", "info", "#general", "#nonexistent")
	if r.err == nil {
		t.Fatal("expected error for partial failure")
	}

	// The returned error must NOT be *output.Error, because main.go prints
	// those to stderr. Per-item errors belong on stdout only.
	var oErr *output.Error
	if errors.As(r.err, &oErr) {
		t.Errorf("partial failure should not return *output.Error (would be printed to stderr), got: %v", r.err)
	}

	lines := nonEmptyLines(r.stdout)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), r.stdout)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
