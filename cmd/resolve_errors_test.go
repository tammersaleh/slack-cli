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
		mux   func(t *testing.T) *http.ServeMux // the command's own endpoints, when it needs any
	}{
		{name: "channel managers", args: []string{"channel", "managers", "some-name"}},
		{name: "draft create", stdin: richTextBlocksJSON("hello"), args: []string{"draft", "create", "some-name"}},
		{name: "list record add", args: []string{"list", "record", "add", "F01ABC", "Room=#some-name"}, mux: func(t *testing.T) *http.ServeMux {
			s := newRecordStore(t)
			s.setSchema(append(s.schema(), map[string]any{"id": "Col01LLL", "name": "Room", "key": "Col01LLM", "type": "channel", "is_primary_column": false}))
			return s.mux()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			if tt.mux != nil {
				mux = tt.mux(t)
			}
			channelListsAnswer(mux, slackFailure("token_revoked"))

			_, err := runWithTwoCredentialsStdin(t, tt.stdin, mux, tt.args...)
			oErr := assertOutputError(t, err, "token_revoked", output.ExitAuth)
			if oErr.Hint == "" || strings.Contains(oErr.Hint, "--desktop") {
				t.Errorf("hint = %q, want the workspace (oauth) credential's re-auth hint", oErr.Hint)
			}
		})
	}
}

// userDirectory serves users.list with h and counts every other request, so a
// test can assert a command stopped before asking Slack anything else.
func userDirectory(h http.HandlerFunc, others *atomic.Int64) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", h)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		others.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "unexpected_request"})
	})
	return mux
}

func emptyUserDirectory(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":                true,
		"members":           []any{},
		"response_metadata": map[string]string{"next_cursor": ""},
	})
}

// userRowCommands print one row per user input. Each resolves every input
// before its first row.
var userRowCommands = [][]string{
	{"user", "info"},
	{"user", "manager-chain"},
	{"status", "get"},
	{"presence", "get"},
	{"dnd", "info"},
}

// The reported bug on the user side: ResolveUser discarded the directory
// walk's error, so a bad token read as user_not_found, one row per name. A
// failed walk stops the run before any row, including the row for the ID
// that needed no lookup.
func TestUserRows_DirectoryFailureAbortsBeforeAnyRow(t *testing.T) {
	for _, args := range userRowCommands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var others atomic.Int64
			mux := userDirectory(slackFailure("invalid_auth"), &others)

			r := runWithMockFull(t, mux, append(args, "U01XYZ", "@alice", "@bob")...)
			assertFatalAbort(t, r.err, r.stdout, "invalid_auth", output.ExitAuth, 0)
			if got := others.Load(); got != 0 {
				t.Errorf("requests besides users.list = %d, want 0", got)
			}
		})
	}
}

// A name absent from a directory that loaded is still that input's own
// user_not_found row.
func TestUserRows_CleanMissKeepsItsRow(t *testing.T) {
	for _, args := range userRowCommands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var others atomic.Int64
			mux := userDirectory(emptyUserDirectory, &others)

			r := runWithMockFull(t, mux, append(args, "@nobody")...)
			assertExitError(t, r.err)
			rows, meta := rowsAndMeta(t, r.stdout)
			if len(rows) != 1 || rows[0]["error"] != "user_not_found" || rows[0]["input"] != "@nobody" {
				t.Errorf("rows = %v, want one user_not_found row for @nobody", rows)
			}
			if meta["error_count"] != float64(1) {
				t.Errorf("_meta.error_count = %v, want 1", meta["error_count"])
			}
		})
	}
}

// A directory walk that exhausts its retries on a page is rate_limited, exit
// 3, naming users.list.
func TestUserInfo_ExhaustedDirectoryLoadIsRateLimited(t *testing.T) {
	var others, listCalls atomic.Int64
	mux := userDirectory(func(w http.ResponseWriter, _ *http.Request) {
		listCalls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}, &others)

	r := runWithMockFull(t, mux, "user", "info", "@alice")
	assertFatalAbort(t, r.err, r.stdout, "rate_limited", output.ExitRateLimit, 0)
	var oErr *output.Error
	if errors.As(r.err, &oErr) && oErr.Endpoint != "users.list" {
		t.Errorf("endpoint = %q, want users.list", oErr.Endpoint)
	}
	if got := listCalls.Load(); got != 5 {
		t.Errorf("users.list requests = %d, want 5 attempts", got)
	}
}

// Commands where a user that does not resolve is fatal. The directory failing
// is reported as itself, and nothing else is asked of Slack.
func TestUserName_DirectoryFailureIsNotUserNotFound(t *testing.T) {
	tests := []struct {
		name    string
		session bool
		stdin   string
		args    []string
	}{
		{name: "channel invite", args: []string{"channel", "invite", "C01ABC", "@alice"}},
		{name: "file list --user", args: []string{"file", "list", "--user", "@alice"}},
		{name: "search messages", args: []string{"search", "messages", "from:@alice deploy"}},
		{name: "search messages quoted", args: []string{"search", "messages", `from:"Alice Adams" deploy`}},
		{name: "search files", args: []string{"search", "files", "from:@alice deploy"}},
		{name: "draft create", session: true, stdin: richTextBlocksJSON("hi"), args: []string{"draft", "create", "@alice", "@bob"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SLACK_USER_TOKEN", "xoxp-test") // search needs one
			var others atomic.Int64
			mux := userDirectory(slackFailure("invalid_auth"), &others)

			var err error
			if tt.session {
				_, err = runWithMockSessionStdin(t, tt.stdin, mux, tt.args...)
			} else {
				_, err = runWithMock(t, mux, tt.args...)
			}
			oErr := assertOutputError(t, err, "invalid_auth", output.ExitAuth)
			if !strings.Contains(oErr.Hint, "auth login") {
				t.Errorf("hint = %q, want the re-auth hint", oErr.Hint)
			}
			if got := others.Load(); got != 0 {
				t.Errorf("requests besides users.list = %d, want 0", got)
			}
		})
	}
}

// The same commands against a directory that loaded without the name.
func TestUserName_CleanMissIsUserNotFound(t *testing.T) {
	tests := []struct {
		name    string
		session bool
		stdin   string
		args    []string
	}{
		{name: "channel invite", args: []string{"channel", "invite", "C01ABC", "@alice"}},
		{name: "file list --user", args: []string{"file", "list", "--user", "@alice"}},
		{name: "search messages", args: []string{"search", "messages", "from:@alice deploy"}},
		{name: "draft create", session: true, stdin: richTextBlocksJSON("hi"), args: []string{"draft", "create", "@alice"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SLACK_USER_TOKEN", "xoxp-test") // search needs one
			var others atomic.Int64
			mux := userDirectory(emptyUserDirectory, &others)

			var err error
			if tt.session {
				_, err = runWithMockSessionStdin(t, tt.stdin, mux, tt.args...)
			} else {
				_, err = runWithMock(t, mux, tt.args...)
			}
			_ = assertOutputError(t, err, "user_not_found", output.ExitGeneral)
			if got := others.Load(); got != 0 {
				t.Errorf("requests besides users.list = %d, want 0", got)
			}
		})
	}
}

// The up-front pass keeps input order: a miss between two IDs prints between
// their rows. Every per-ID call fails with a per-row error here, so each
// input's row carries its input.
func TestUserRows_MissKeepsItsPlaceBetweenOtherInputs(t *testing.T) {
	for _, args := range userRowCommands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var others atomic.Int64
			mux := userDirectory(emptyUserDirectory, &others)

			r := runWithMockFull(t, mux, append(args, "U01XYZ", "@nobody", "U02MGR")...)
			assertExitError(t, r.err)
			rows, _ := rowsAndMeta(t, r.stdout)
			var inputs []string
			for _, row := range rows {
				inputs = append(inputs, row["input"].(string))
			}
			if strings.Join(inputs, " ") != "U01XYZ @nobody U02MGR" {
				t.Fatalf("row inputs = %v, want input order:\n%s", inputs, r.stdout)
			}
			if rows[1]["error"] != "user_not_found" {
				t.Errorf("row 2 = %v, want user_not_found", rows[1])
			}
		})
	}
}
