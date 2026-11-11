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

// channelListsAnswer serves both channel-list endpoints a name lookup walks
// (users.conversations, then conversations.list) with the same handler.
func channelListsAnswer(mux *http.ServeMux, h http.HandlerFunc) {
	for _, endpoint := range channelListEndpoints {
		mux.HandleFunc("/api/"+endpoint, h)
	}
}

func slackFailure(code string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
	}
}

func emptyChannelPage(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":                true,
		"channels":          []any{},
		"response_metadata": map[string]string{"next_cursor": ""},
	})
}

func assertOutputError(t *testing.T, err error, wantCode string, wantExit int) *output.Error {
	t.Helper()
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != wantCode || oErr.Code != wantExit {
		t.Fatalf("got %q exit %d, want %q exit %d", oErr.Err, oErr.Code, wantCode, wantExit)
	}
	return oErr
}

// channelNameCommands is every command that resolves one channel name and
// treats a failure as fatal. "some-name" is the channel in each.
var channelNameCommands = [][]string{
	{"channel", "members", "some-name"},
	{"channel", "invite", "some-name", "U01XYZ"},
	{"channel", "kick", "some-name", "U01XYZ"},
	{"message", "list", "some-name"},
	{"message", "get", "some-name", "1709251200.000100"},
	{"message", "permalink", "some-name", "1709251200.000100"},
	{"reaction", "list", "some-name", "1709251200.000100"},
	{"thread", "list", "some-name", "1709251200.000100"},
	{"pin", "list", "some-name"},
	{"bookmark", "list", "some-name"},
}

// The reported bug: a channel-name lookup that failed for a reason other than
// the name came back as channel_not_found, exit 1, with a hint to go browse
// the channel list. A bad token must exit 2 under its own code.
func TestChannelName_AuthFailureIsNotChannelNotFound(t *testing.T) {
	for _, args := range append(channelNameCommands, []string{"file", "list", "--channel", "some-name"}) {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			mux := http.NewServeMux()
			channelListsAnswer(mux, slackFailure("invalid_auth"))

			r := runWithMockFull(t, mux, args...)
			oErr := assertOutputError(t, r.err, "invalid_auth", output.ExitAuth)
			if !strings.Contains(oErr.Hint, "auth login") {
				t.Errorf("hint = %q, want the re-auth hint", oErr.Hint)
			}
			if r.stdout != "" {
				t.Errorf("stdout must be empty, got:\n%s", r.stdout)
			}
		})
	}
}

// The other half: a name absent from an exhausted walk is still
// channel_not_found, exit 1, with the input and the discovery hint.
func TestChannelName_CleanMissIsChannelNotFound(t *testing.T) {
	for _, args := range channelNameCommands {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			mux := http.NewServeMux()
			channelListsAnswer(mux, emptyChannelPage)

			r := runWithMockFull(t, mux, args...)
			oErr := assertOutputError(t, r.err, "channel_not_found", output.ExitGeneral)
			if oErr.Input != "some-name" || !strings.Contains(oErr.Hint, "channel list") {
				t.Errorf("input %q hint %q, want the input and the channel list hint", oErr.Input, oErr.Hint)
			}
		})
	}
}

// A Slack refusal that is not an auth error keeps its own code too. This is
// the Enterprise Grid org-token case: both list endpoints answer
// enterprise_is_restricted, a token-context problem the user can fix.
func TestChannelName_SlackRefusalKeepsItsCode(t *testing.T) {
	mux := http.NewServeMux()
	channelListsAnswer(mux, slackFailure("enterprise_is_restricted"))

	r := runWithMockFull(t, mux, "channel", "members", "some-name")
	_ = assertOutputError(t, r.err, "enterprise_is_restricted", output.ExitGeneral)
}

// A rate limit that outlasts its retries on the org walk is rate_limited,
// exit 3, naming the endpoint. The member scan answers cleanly so the test
// waits out one run of retries, not two.
func TestChannelName_ExhaustedRateLimitIsRateLimited(t *testing.T) {
	var orgCalls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", emptyChannelPage)
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, _ *http.Request) {
		orgCalls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	r := runWithMockFull(t, mux, "channel", "members", "some-name")
	oErr := assertOutputError(t, r.err, "rate_limited", output.ExitRateLimit)
	if oErr.Endpoint != "conversations.list" {
		t.Errorf("endpoint = %q, want conversations.list", oErr.Endpoint)
	}
	if got := orgCalls.Load(); got != 5 {
		t.Errorf("conversations.list requests = %d, want 5 attempts", got)
	}
}

// file list --channel is lenient on purpose: a name that matches nothing
// passes through to files.list as typed.
func TestFileList_UnresolvedChannelNamePassesThrough(t *testing.T) {
	var gotChannel string
	mux := http.NewServeMux()
	channelListsAnswer(mux, emptyChannelPage)
	mux.HandleFunc("/api/files.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.FormValue("channel")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "files": []any{}, "paging": map[string]any{"page": 1, "pages": 1}})
	})

	if _, err := runWithMock(t, mux, "file", "list", "--channel", "some-name"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotChannel != "some-name" {
		t.Errorf("files.list channel = %q, want the name as typed", gotChannel)
	}
}

// channel info resolves every name before its first row. A lookup that fails
// for a whole-command reason stops the run with no rows: not a row for the ID
// that needed no lookup, and not one channel_not_found row per name.
func TestChannelInfo_ResolveFailureAbortsBeforeAnyRow(t *testing.T) {
	var infoCalls atomic.Int64
	mux := http.NewServeMux()
	channelListsAnswer(mux, slackFailure("invalid_auth"))
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, _ *http.Request) {
		infoCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"id": "C01ABC", "name": "general"}})
	})

	r := runWithMockFull(t, mux, "channel", "info", "C01ABC", "some-name", "other-name")
	assertFatalAbort(t, r.err, r.stdout, "invalid_auth", output.ExitAuth, 0)
	if got := infoCalls.Load(); got != 0 {
		t.Errorf("conversations.info requests = %d, want 0", got)
	}
}

// A name that matches nothing keeps its per-input row, in input order, and
// the inputs around it still get theirs.
func TestChannelInfo_CleanMissKeepsItsRow(t *testing.T) {
	mux := http.NewServeMux()
	channelListsAnswer(mux, emptyChannelPage)
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.FormValue("channel")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"id": id, "name": "n-" + id}})
	})

	r := runWithMockFull(t, mux, "channel", "info", "C01ABC", "some-name", "C02DEF")
	assertExitError(t, r.err)
	rows, meta := rowsAndMeta(t, r.stdout)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3:\n%s", len(rows), r.stdout)
	}
	if rows[0]["id"] != "C01ABC" || rows[2]["id"] != "C02DEF" {
		t.Errorf("rows out of input order: %v", rows)
	}
	if rows[1]["error"] != "channel_not_found" || rows[1]["input"] != "some-name" {
		t.Errorf("row 2 = %v, want channel_not_found for some-name", rows[1])
	}
	if meta["error_count"] != float64(1) {
		t.Errorf("_meta.error_count = %v, want 1", meta["error_count"])
	}
}

// Two-client commands resolve a channel name on the workspace credential and
// make their internal call on the session credential. A failed lookup must
// carry the workspace credential's re-auth hint (oauth here), not the session
// credential's (desktop, which names --desktop).
func TestChannelName_AuthHintNamesTheResolvingCredential(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
		args  []string
		extra func(mux *http.ServeMux)
	}{
		{name: "channel managers", args: []string{"channel", "managers", "some-name"}},
		{name: "draft create", stdin: richTextBlocksJSON("hello"), args: []string{"draft", "create", "some-name"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			channelListsAnswer(mux, slackFailure("token_revoked"))

			_, err := runWithTwoCredentialsStdin(t, tt.stdin, mux, tt.args...)
			oErr := assertOutputError(t, err, "token_revoked", output.ExitAuth)
			if oErr.Hint == "" || strings.Contains(oErr.Hint, "--desktop") {
				t.Errorf("hint = %q, want the workspace (oauth) credential's re-auth hint", oErr.Hint)
			}
		})
	}
}
