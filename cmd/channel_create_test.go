package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/auth"
	"github.com/tammersaleh/slack-cli/internal/confirm"
)

// runWithMockAndConfirmer runs a CLI command against a mock Slack API
// server with credentials seeded into a temp HOME so the workspace name
// is resolvable. The provided Confirmer is installed via SetConfirmer
// before kctx.Run.
//
// Returns full mockResult (stdout, stderr, err) so tests can assert on
// both data and diagnostics.
func runWithMockAndConfirmer(t *testing.T, handler http.Handler, cf confirm.Confirmer, workspaceName string, args ...string) mockResult {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	isolateTestEnv(t)

	// Seed real credentials so ResolveCredentials returns a TeamName.
	// SLACK_TOKEN / SLACK_USER_TOKEN env vars would short-circuit
	// credentials lookup and drop TeamName, which is exactly the field
	// we need for the reason string. isolateTestEnv intentionally leaves
	// these alone (other tests depend on that), so we clear them here.
	// Unset (not blank) so other env-driven flags don't trip on empty
	// strings - same pattern as isolateTestEnv.
	for _, v := range []string{"SLACK_TOKEN", "SLACK_USER_TOKEN"} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	creds := &auth.Credentials{
		Workspaces: map[string]auth.WorkspaceCredentials{
			"T123": {
				BotToken:   "xoxb-test",
				AuthMethod: "oauth",
				TeamID:     "T123",
				TeamName:   workspaceName,
				UserID:     "U456",
			},
		},
	}
	credsPath, err := auth.DefaultCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveCredentials(credsPath, creds); err != nil {
		t.Fatal(err)
	}

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
	cli.SetConfirmer(cf)

	runErr := kctx.Run(&cli)
	return mockResult{stdout: outBuf.String(), stderr: errBuf.String(), err: runErr}
}

// orderingConfirmer records the call order between Confirm() and the
// Slack API handler. Used by TestChannelCreate_ConfirmBeforeAPI to prove
// the gate runs first. Holds a pointer to the test's shared mutex and
// events slice so all appends are serialized through one lock.
type orderingConfirmer struct {
	mu     *sync.Mutex
	events *[]string
}

func (o *orderingConfirmer) Confirm(_ context.Context, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	*o.events = append(*o.events, "confirm")
	return nil
}

func TestChannelCreate_ApprovalPublic(t *testing.T) {
	var apiHit atomic.Int32
	var gotName, gotIsPrivate string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.create", func(w http.ResponseWriter, r *http.Request) {
		apiHit.Add(1)
		_ = r.ParseForm()
		gotName = r.FormValue("name")
		gotIsPrivate = r.FormValue("is_private")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channel": map[string]any{
				"id":         "C01NEW",
				"name":       "newchan",
				"is_channel": true,
				"is_private": false,
			},
		})
	})

	rec := &confirm.Recorder{}
	r := runWithMockAndConfirmer(t, mux, rec, "Acme Corp", "channel", "create", "newchan")
	if r.err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", r.err, r.stderr)
	}

	if apiHit.Load() != 1 {
		t.Fatalf("conversations.create called %d times, want 1", apiHit.Load())
	}
	if gotName != "newchan" {
		t.Errorf("name = %q, want %q", gotName, "newchan")
	}
	if gotIsPrivate != "false" {
		t.Errorf("is_private = %q, want %q", gotIsPrivate, "false")
	}

	reasons := rec.Reasons()
	if len(reasons) != 1 {
		t.Fatalf("Confirm called %d times, want 1", len(reasons))
	}
	wantSubstrs := []string{"public channel", "#newchan", "Acme Corp"}
	for _, sub := range wantSubstrs {
		if !strings.Contains(reasons[0], sub) {
			t.Errorf("reason missing %q\ngot: %s", sub, reasons[0])
		}
	}
	// The reason MUST NOT contain the opaque TeamID even though it's
	// stored alongside TeamName. This is a security-relevant assertion:
	// the user needs to recognize the workspace from the prompt.
	if strings.Contains(reasons[0], "T123") {
		t.Errorf("reason must not contain workspace ID 'T123'; got: %s", reasons[0])
	}

	lines := nonEmptyLines(r.stdout)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (channel + meta), got %d:\n%s", len(lines), r.stdout)
	}
	ch := parseJSON(t, lines[0])
	if ch["id"] != "C01NEW" {
		t.Errorf("channel id = %q, want %q", ch["id"], "C01NEW")
	}
}

func TestChannelCreate_ConfirmBeforeAPI(t *testing.T) {
	// Codex flagged that the previous tests don't prove ordering: a
	// regression that called the API and only then prompted would still
	// pass them. Record the order of events explicitly through a single
	// shared mutex so both producers serialize their appends.
	var (
		mu     sync.Mutex
		events []string
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.create", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		events = append(events, "api")
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01NEW", "name": "newchan"},
		})
	})

	cf := &orderingConfirmer{mu: &mu, events: &events}
	r := runWithMockAndConfirmer(t, mux, cf, "Acme Corp", "channel", "create", "newchan")
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 events (confirm, api); got %v", events)
	}
	if events[0] != "confirm" || events[1] != "api" {
		t.Errorf("expected order [confirm, api], got %v", events)
	}
}

func TestChannelCreate_PrivateFlag(t *testing.T) {
	var gotIsPrivate string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotIsPrivate = r.FormValue("is_private")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channel": map[string]any{"id": "C02", "name": "secret", "is_private": true},
		})
	})

	rec := &confirm.Recorder{}
	r := runWithMockAndConfirmer(t, mux, rec, "Acme Corp", "channel", "create", "secret", "--private")
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}
	if gotIsPrivate != "true" {
		t.Errorf("is_private = %q, want %q", gotIsPrivate, "true")
	}
	reasons := rec.Reasons()
	if len(reasons) != 1 {
		t.Fatalf("Confirm called %d times, want 1", len(reasons))
	}
	if !strings.Contains(reasons[0], "private channel") {
		t.Errorf("reason should mention 'private channel'; got: %s", reasons[0])
	}
	if strings.Contains(reasons[0], "public channel") {
		t.Errorf("reason for private channel should not say 'public'; got: %s", reasons[0])
	}
}

func TestChannelCreate_DenialBlocksAPIAndOutput(t *testing.T) {
	// Track every request to the mock server, not just conversations.create:
	// "denial means no Slack call" should be enforced broadly, not narrowly.
	var totalRequests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		totalRequests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"id": "C00"}})
	})

	// Recorder with Deny=true: returns ErrConfirmDenied AND records the
	// prompt happened, so we can prove the gate ran.
	rec := &confirm.Recorder{Deny: true}
	r := runWithMockAndConfirmer(t, mux, rec, "Acme Corp", "channel", "create", "newchan")
	if r.err == nil {
		t.Fatal("expected error from denial, got nil")
	}
	if !errors.Is(r.err, confirm.ErrConfirmDenied) {
		t.Errorf("got error %v, want ErrConfirmDenied", r.err)
	}
	if totalRequests.Load() != 0 {
		t.Errorf("mock server hit %d times despite denial; want 0", totalRequests.Load())
	}
	if reasons := rec.Reasons(); len(reasons) != 1 {
		t.Errorf("Confirm called %d times before denial; want 1", len(reasons))
	}
	// Denial must produce ZERO data on stdout - not just "no channel id".
	// PrintMeta would also be inappropriate since the operation never
	// happened. nonEmptyLines accounts for trailing newlines.
	if lines := nonEmptyLines(r.stdout); len(lines) != 0 {
		t.Errorf("denial should produce no stdout output; got %d lines:\n%s", len(lines), r.stdout)
	}
}

func TestChannelCreate_StripsLeadingHash(t *testing.T) {
	var gotName string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotName = r.FormValue("name")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channel": map[string]any{"id": "C03", "name": gotName},
		})
	})

	rec := &confirm.Recorder{}
	r := runWithMockAndConfirmer(t, mux, rec, "Acme Corp", "channel", "create", "#hashed")
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}
	if gotName != "hashed" {
		t.Errorf("expected leading '#' stripped, sent name=%q to API", gotName)
	}
	reasons := rec.Reasons()
	// Reason string formats the channel name with `#` regardless of
	// whether the user passed a leading `#`. This verifies the format
	// path rebuilds the prefix, not that the input is preserved.
	if len(reasons) != 1 || !strings.Contains(reasons[0], "#hashed") {
		t.Errorf("reason should format channel name as '#hashed'; got %v", reasons)
	}
}

func TestChannelCreate_NilConfirmerErrors(t *testing.T) {
	// If main.go ever forgets SetConfirmer, channel create must fail
	// with a clear error rather than panic or skip the gate.
	var apiHit atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.create", func(w http.ResponseWriter, r *http.Request) {
		apiHit.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"id": "Cnil"}})
	})

	r := runWithMockAndConfirmer(t, mux, nil, "Acme Corp", "channel", "create", "newchan")
	if r.err == nil {
		t.Fatal("expected error when no Confirmer is set, got nil")
	}
	if apiHit.Load() != 0 {
		t.Errorf("nil-confirmer path should not hit conversations.create; got %d calls", apiHit.Load())
	}
	if !strings.Contains(r.err.Error(), "confirm_misconfigured") {
		t.Errorf("expected confirm_misconfigured error, got: %v", r.err)
	}
}

func TestChannelCreate_RefusesWithoutWorkspaceName(t *testing.T) {
	// WorkspaceName must NOT silently substitute the TeamID when no
	// team_name is on file. The biometric prompt is the only defense
	// against a confused/malicious caller; an opaque ID defeats it.
	// The credentials seeded here have no TeamName so this regression
	// is what the test guards.
	var apiHit atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		apiHit.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"id": "C00"}})
	})

	// Empty workspaceName string = no team_name in seeded creds.
	r := runWithMockAndConfirmer(t, mux, confirm.AlwaysApprove{}, "", "channel", "create", "newchan")
	if r.err == nil {
		t.Fatal("expected error when no workspace name available, got nil")
	}
	if !strings.Contains(r.err.Error(), "workspace_name_unresolved") {
		t.Errorf("expected workspace_name_unresolved error, got: %v", r.err)
	}
	if apiHit.Load() != 0 {
		t.Errorf("missing-workspace-name path should not hit Slack; got %d calls", apiHit.Load())
	}
}

func TestChannelCreate_NameTakenErrorPropagates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.create", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "name_taken"})
	})

	r := runWithMockAndConfirmer(t, mux, confirm.AlwaysApprove{}, "Acme Corp", "channel", "create", "general")
	if r.err == nil {
		t.Fatal("expected error from name_taken, got nil")
	}
	if !strings.Contains(r.err.Error(), "name_taken") {
		t.Errorf("expected name_taken in error, got: %v", r.err)
	}
}
