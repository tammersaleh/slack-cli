package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/output"
)

func mustParse(t *testing.T, args ...string) (*cmd.CLI, *kong.Context) {
	t.Helper()
	var cli cmd.CLI
	parser, err := kong.New(&cli,
		kong.Name("slack"),
		kong.Exit(func(int) { t.Fatal("unexpected exit") }),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	return &cli, ctx
}

func TestGlobalFlags_Defaults(t *testing.T) {
	cli, _ := mustParse(t, "user", "list")

	if cli.Fields != "" {
		t.Errorf("expected default fields empty, got %q", cli.Fields)
	}
	if cli.Quiet {
		t.Error("expected quiet to default to false")
	}
}

func TestGlobalFlags_Override(t *testing.T) {
	cli, _ := mustParse(t,
		"--workspace", "myteam",
		"--fields", "id,name",
		"--quiet",
		"user", "list",
	)

	if cli.Workspace != "myteam" {
		t.Errorf("expected workspace 'myteam', got %q", cli.Workspace)
	}
	if cli.Fields != "id,name" {
		t.Errorf("expected fields 'id,name', got %q", cli.Fields)
	}
	if !cli.Quiet {
		t.Error("expected quiet to be true")
	}
}

func TestGlobalFlags_ShortFlags(t *testing.T) {
	cli, _ := mustParse(t, "-w", "myteam", "-q", "user", "list")

	if cli.Workspace != "myteam" {
		t.Errorf("expected workspace 'myteam', got %q", cli.Workspace)
	}
	if !cli.Quiet {
		t.Error("expected quiet to be true")
	}
}

func TestGlobalFlags_EnvVars(t *testing.T) {
	t.Setenv("SLACK_WORKSPACE", "envteam")

	cli, _ := mustParse(t, "user", "list")

	if cli.Workspace != "envteam" {
		t.Errorf("expected workspace 'envteam' from env, got %q", cli.Workspace)
	}
}

func TestGlobalFlags_FlagsOverrideEnv(t *testing.T) {
	t.Setenv("SLACK_WORKSPACE", "envteam")

	cli, _ := mustParse(t, "--workspace", "flagteam", "user", "list")

	if cli.Workspace != "flagteam" {
		t.Errorf("expected workspace 'flagteam' from flag, got %q", cli.Workspace)
	}
}

func TestParsedFields(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{"empty", "", nil},
		{"single", "id", []string{"id"}},
		{"multiple", "id,name,email", []string{"id", "name", "email"}},
		{"with spaces", "id, name , email", []string{"id", "name", "email"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := &cmd.CLI{Fields: tt.input}
			got := cli.ParsedFields()
			if tt.expect == nil && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
			if len(got) != len(tt.expect) {
				t.Errorf("expected %d fields, got %d", len(tt.expect), len(got))
				return
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("field %d: got %q, want %q", i, got[i], tt.expect[i])
				}
			}
		})
	}
}

func TestSubcommands_Exist(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"auth login", []string{"auth", "login"}},
		{"auth login desktop", []string{"auth", "login", "--desktop"}},
		{"auth logout", []string{"auth", "logout"}},
		{"auth status", []string{"auth", "status"}},
		{"channel list", []string{"channel", "list"}},
		{"channel info", []string{"channel", "info", "C123"}},
		{"channel members", []string{"channel", "members", "C123"}},
		{"message list", []string{"message", "list", "C123"}},
		{"message read alias", []string{"message", "read", "C123"}},
		{"message get", []string{"message", "get", "C123", "1234567890.123456"}},
		{"thread list", []string{"thread", "list", "C123", "1234567890.123456"}},
		{"thread read alias", []string{"thread", "read", "C123", "1234567890.123456"}},
		{"user list", []string{"user", "list"}},
		{"user info", []string{"user", "info", "U123"}},
		{"reaction list", []string{"reaction", "list", "C123", "1234567890.123456"}},
		{"search messages", []string{"search", "messages", "test query"}},
		{"search files", []string{"search", "files", "test query"}},
		{"message permalink", []string{"message", "permalink", "C123", "1234.5678"}},
		{"saved list", []string{"saved", "list"}},
		{"saved counts", []string{"saved", "counts"}},
		{"section list", []string{"section", "list"}},
		{"section channels", []string{"section", "channels", "S123"}},
		{"section find", []string{"section", "find", "pattern"}},
		{"section create", []string{"section", "create", "name"}},
		{"file list", []string{"file", "list"}},
		{"file info", []string{"file", "info", "F123"}},
		{"file download", []string{"file", "download", "F123"}},
		{"pin list", []string{"pin", "list", "C123"}},
		{"bookmark list", []string{"bookmark", "list", "C123"}},
		{"draft list", []string{"draft", "list"}},
		{"draft create", []string{"draft", "create", "C123"}},
		{"draft update", []string{"draft", "update", "Dr123"}},
		{"draft delete", []string{"draft", "delete", "Dr123"}},
		{"status get", []string{"status", "get"}},
		{"presence get", []string{"presence", "get", "U123"}},
		{"dnd info", []string{"dnd", "info", "U123"}},
		{"usergroup list", []string{"usergroup", "list"}},
		{"usergroup members", []string{"usergroup", "members", "S123"}},
		{"emoji list", []string{"emoji", "list"}},
		{"workspace info", []string{"workspace-info", "info"}},
		{"cache info", []string{"cache", "info"}},
		{"cache clear", []string{"cache", "clear"}},
		{"version", []string{"version"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ctx := mustParse(t, tt.args...)
			if ctx.Command() == "" {
				t.Error("expected a command to be selected")
			}
		})
	}
}

func TestChannelList_Defaults(t *testing.T) {
	cli, _ := mustParse(t, "channel", "list")
	if cli.Channel.List.Limit != 100 {
		t.Errorf("expected default limit 100, got %d", cli.Channel.List.Limit)
	}
	if cli.Channel.List.Type != "all" {
		t.Errorf("expected default type 'all', got %q", cli.Channel.List.Type)
	}
	if !cli.Channel.List.ExcludeArchived {
		t.Error("expected exclude-archived to default to true")
	}
}

func TestMessageList_Defaults(t *testing.T) {
	cli, _ := mustParse(t, "message", "list", "#general")
	if cli.Message.List.Limit != 20 {
		t.Errorf("expected default limit 20, got %d", cli.Message.List.Limit)
	}
}

func TestThreadList_Defaults(t *testing.T) {
	cli, _ := mustParse(t, "thread", "list", "#general", "1234.5678")
	if cli.Thread.List.Limit != 50 {
		t.Errorf("expected default limit 50, got %d", cli.Thread.List.Limit)
	}
}

func TestUnknownCommand(t *testing.T) {
	var cli cmd.CLI
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parser.Parse([]string{"bogus"})
	if err == nil {
		t.Error("expected error for unknown command, got nil")
	}
}

// --timeout is documented as the overall command timeout, so a command that
// makes a preliminary call before listing must share one deadline across both.
// cli.Context() derives a fresh deadline from context.Background() on every
// call, so calling it twice hands --has-unread a second full budget.
//
// Detecting that requires each call to fit the timeout on its own while the two
// together do not: a preliminary call that simply outlasts the deadline fails
// under either scheme, which is why the obvious version of this test passes
// even with the bug present. So client.counts spends part of the budget and
// succeeds, then the listing needs more than what a shared deadline has left.
// With one context the command must fail; with two it would sail through on a
// fresh budget.
//
// The margins are deliberately loose in both directions - 1s of a 3s budget for
// the first call, then a 2.5s listing that cannot fit the ~2s remainder but fits
// a fresh 3s easily. Tighter numbers work locally but can flake under -race or
// parallel load, where request overhead alone could push the first call past the
// deadline and fail the test for the wrong reason.
func TestTimeout_SharedAcrossPreliminaryCall(t *testing.T) {
	var listCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/client.counts", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(1 * time.Second):
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"channels": []map[string]any{{"id": "C01", "has_unreads": true}},
		})
	})
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		listCalls.Add(1)
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2500 * time.Millisecond):
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          []map[string]any{{"id": "C01", "name": "general"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	isolateTestEnv(t)
	t.Setenv("SLACK_TOKEN", "xoxc-test")
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	kctx, err := parser.Parse([]string{"--timeout", "3s", "channel", "list", "--has-unread"})
	if err != nil {
		t.Fatal(err)
	}
	cli.SetOutput(&outBuf, &errBuf)
	runErr := kctx.Run(&cli)

	// The counts call consumed 1s of a 3s budget, so the 2.5s listing cannot
	// finish inside what remains - unless it was handed a fresh deadline.
	if runErr == nil {
		t.Fatalf("expected the shared deadline to fail the command; --timeout is not bounding the whole run (stdout %q)", outBuf.String())
	}
	assertTimeoutError(t, runErr)
	if got := listCalls.Load(); got != 1 {
		t.Errorf("listing attempted %d times, want 1", got)
	}
}

// TestTimeout_EndToEnd wires --timeout through a real command and confirms
// the deadline cancels the in-flight API call. The handler blocks for 2s
// so the 100ms timeout has to propagate for the client to give up early.
// We can't just time the test: runWithMockFull defers srv.Close which
// waits for the handler to drain, masking fast client returns. Assert on
// the classified error instead - a propagated deadline surfaces as the
// "timeout" code, with the stdlib wording kept in the detail.
func TestTimeout_EndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})

	r := runWithMockFull(t, mux, "--timeout", "100ms", "channel", "list")
	if r.err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	assertTimeoutError(t, r.err)
}

// assertTimeoutError pins both halves of a classified timeout: the stable code
// a consumer branches on, and the stdlib wording that has to survive in the
// detail so replacing the raw text does not lose what it said. Checking only
// the code would pass against a classifier that dropped the detail; checking
// only the text would pass against one that never assigned a code.
func assertTimeoutError(t *testing.T, err error) {
	t.Helper()
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected an *output.Error, got %#v", err)
	}
	if oErr.Err != "timeout" {
		t.Errorf("expected error code 'timeout', got %q", oErr.Err)
	}
	if !strings.Contains(oErr.Detail, "deadline exceeded") {
		t.Errorf("expected the detail to keep the deadline wording, got %q", oErr.Detail)
	}
}

func TestTrace_EmitsPageEvents(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01", "name": "foo", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01", "name": "foo"},
		})
	})

	// Capture stderr to see the trace output.
	r := runWithMockFull(t, mux, "--trace", "channel", "info", "foo")
	if r.err != nil {
		t.Fatal(r.err)
	}

	// Trace events are one JSON per line on stderr.
	lines := nonEmptyLines(r.stderr)
	if len(lines) == 0 {
		t.Fatalf("expected trace events on stderr, got none")
	}

	// First line should be a page event for conversations.list (name resolution).
	evt := parseJSON(t, lines[0])
	if evt["kind"] != "page" {
		t.Errorf("expected first event kind='page', got %q", evt["kind"])
	}
	if evt["endpoint"] != "conversations.list" {
		t.Errorf("expected endpoint='conversations.list', got %q", evt["endpoint"])
	}

	// Trace output must stay on stderr; stdout must carry only the JSONL
	// data plus _meta, with no 'kind' field leaking into it.
	for _, line := range nonEmptyLines(r.stdout) {
		item := parseJSON(t, line)
		if _, hasKind := item["kind"]; hasKind {
			t.Errorf("trace event leaked into stdout: %s", line)
		}
	}
}

func TestTrace_DisabledByDefault(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01", "name": "foo", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01", "name": "foo"},
		})
	})

	r := runWithMockFull(t, mux, "channel", "info", "foo")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.stderr != "" {
		t.Errorf("expected empty stderr without --trace, got: %s", r.stderr)
	}
}

func TestContext_NoTimeout(t *testing.T) {
	cli := &cmd.CLI{}
	ctx, cancel := cli.Context()
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Error("expected no deadline when --timeout is unset")
	}
}

func TestContext_WithTimeout(t *testing.T) {
	cli := &cmd.CLI{Timeout: 5 * time.Second}
	ctx, cancel := cli.Context()
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected a deadline when --timeout is set")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 5*time.Second {
		t.Errorf("unexpected remaining time: %v", remaining)
	}
}

func TestContext_TimeoutFires(t *testing.T) {
	cli := &cmd.CLI{Timeout: 10 * time.Millisecond}
	ctx, cancel := cli.Context()
	defer cancel()

	select {
	case <-ctx.Done():
		if !errorsIs(ctx.Err(), context.DeadlineExceeded) {
			t.Errorf("expected DeadlineExceeded, got %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("timeout did not fire within 1s")
	}
}

func errorsIs(err, target error) bool {
	return err == target
}

func TestNoCommand(t *testing.T) {
	var cli cmd.CLI
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parser.Parse([]string{})
	if err == nil {
		t.Error("expected error when no command given, got nil")
	}
}
