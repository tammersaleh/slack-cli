package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/auth"
	"github.com/tammersaleh/slack-cli/internal/confirm"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// These tests cover the structured-error contract in Run() - the
// confirm_denied / confirm_unsupported / output.Error branches at the
// top of cmd/slack/main.go. We invoke Run() directly so the test
// drives the real error-encoding code, not a copy in a helper.

// returnErrConfirmer is a Confirmer that returns whatever error it was
// constructed with. Used to inject specific sentinel-wrapped errors
// (e.g. ErrUnsupported with a custom message) into Run().
type returnErrConfirmer struct{ err error }

func (r returnErrConfirmer) Confirm(_ context.Context, _ string) error { return r.err }

// seedCredentials writes credentials.json into a temp HOME and points
// SLACK_API_URL at srvURL. Unsets Slack-related env vars first so the
// test is hermetic against the developer's shell - any new SLACK_*
// global flag should be added to this list.
//
// We use Unsetenv (not Setenv "") because kong treats SLACK_TIMEOUT=""
// as a parse error ("invalid duration"). t.Setenv with empty string
// would expose tests to that landmine.
func seedCredentials(t *testing.T, srvURL, teamName string) {
	t.Helper()
	for _, v := range []string{
		"SLACK_TOKEN",
		"SLACK_USER_TOKEN",
		"SLACK_COOKIE",
		"SLACK_WORKSPACE",
		"SLACK_WORKSPACE_ORG",
		"SLACK_FIELDS",
		"SLACK_TIMEOUT",
		"SLACK_CACHE_TTL",
		"SLACK_API_URL",
	} {
		// t.Setenv is the standard way to scope env changes to the
		// test; combine with explicit Unsetenv to clear instead of
		// blank, so a kong flag that does NumericValue parsing
		// doesn't trip on the empty string.
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
				TeamName:   teamName,
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

	t.Setenv("SLACK_API_URL", srvURL+"/api/")
}

func TestRun_ConfirmDeniedEmitsStructuredJSON(t *testing.T) {
	var apiHit atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		apiHit.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	seedCredentials(t, srv.URL, "Acme Corp")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"channel", "create", "newchan"}, &stdout, &stderr, confirm.AlwaysDeny{})

	if code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", code, output.ExitGeneral)
	}
	if apiHit.Load() != 0 {
		t.Errorf("denial should not hit Slack; got %d requests", apiHit.Load())
	}

	var env map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\nstderr=%s", err, stderr.String())
	}
	if env["error"] != "confirm_denied" {
		t.Errorf("error = %q, want %q\nstderr=%s", env["error"], "confirm_denied", stderr.String())
	}
	if _, ok := env["detail"]; !ok {
		t.Errorf("expected 'detail' field; got: %s", stderr.String())
	}
}

func TestRun_ConfirmUnsupportedEmitsStructuredJSON(t *testing.T) {
	// Inject a Confirmer that returns the same wrapping shape the
	// real darwin / stub paths use (fmt.Errorf("%w: ...", ErrUnsupported)).
	// This validates the errors.Is branch in Run() without needing the
	// real biometric backend.
	cf := returnErrConfirmer{
		err: fmt.Errorf("%w: biometric confirmation requires macOS Touch ID", confirm.ErrUnsupported),
	}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	seedCredentials(t, srv.URL, "Acme Corp")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"channel", "create", "newchan"}, &stdout, &stderr, cf)

	if code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", code, output.ExitGeneral)
	}

	var env map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\nstderr=%s", err, stderr.String())
	}
	if env["error"] != "confirm_unsupported" {
		t.Errorf("error = %q, want %q", env["error"], "confirm_unsupported")
	}
	hint, ok := env["hint"].(string)
	if !ok || hint == "" {
		t.Errorf("expected non-empty 'hint' field, got: %v", env["hint"])
	}
	if !strings.Contains(hint, "no bypass") {
		t.Errorf("hint should reaffirm there's no bypass; got: %s", hint)
	}
}

func TestRun_StructuredOutputErrorPreserved(t *testing.T) {
	// Confirms an *output.Error returned by command Run is rendered
	// with the full JSON envelope (error + detail + hint), not just
	// the error field. Regression guard for the oErr branch in Run().
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Empty teamName -> channel create returns workspace_name_unresolved.
	seedCredentials(t, srv.URL, "")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"channel", "create", "newchan"}, &stdout, &stderr, confirm.AlwaysApprove{})

	if code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", code, output.ExitGeneral)
	}
	var env map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\nstderr=%s", err, stderr.String())
	}
	if env["error"] != "workspace_name_unresolved" {
		t.Errorf("error = %q, want workspace_name_unresolved", env["error"])
	}
	if detail, _ := env["detail"].(string); detail == "" {
		t.Errorf("expected non-empty 'detail'; got: %s", stderr.String())
	}
	if hint, _ := env["hint"].(string); hint == "" {
		t.Errorf("expected non-empty 'hint' (output.Error carries one); got: %s", stderr.String())
	}
}

func TestRun_HelpPrintsToStdoutAndExitsZero(t *testing.T) {
	// kong's help flag writes usage to stdout and calls our exit
	// override. Run must observe the override and return 0 rather
	// than treating the resulting "expected <subcommand>" parseErr
	// as a real failure.
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--help"}, &stdout, &stderr, confirm.AlwaysApprove{})
	if code != 0 {
		t.Errorf("exit code = %d, want 0\nstderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("expected 'Usage:' on stdout for --help; got:\nstdout=%s\nstderr=%s",
			stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("--help should not write to stderr; got: %s", stderr.String())
	}
}

func TestRun_ParseErrorEmitsStructuredEnvelope(t *testing.T) {
	// Unknown flag is a genuine parse error. Per SPEC.md, stdout is
	// for data and stderr is for diagnostics. So:
	//   - stdout MUST be empty (no usage spam, no nothing)
	//   - stderr MUST be exactly one JSON line (no plain-text preamble)
	//   - the JSON envelope must include a hint pointing at --help
	//   - exit code 1
	var stdout, stderr bytes.Buffer
	code := Run([]string{"channel", "create", "--definitely-not-a-flag", "x"}, &stdout, &stderr, confirm.AlwaysApprove{})
	if code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", code, output.ExitGeneral)
	}

	// stdout must be empty - usage / diagnostics never go to stdout.
	if stdout.Len() != 0 {
		t.Errorf("stdout must be empty on parse error; got: %q", stdout.String())
	}

	// stderr must be exactly one JSON line.
	stderrLines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
	if len(stderrLines) != 1 {
		t.Errorf("expected exactly one stderr line; got %d:\n%s", len(stderrLines), stderr.String())
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(stderrLines[0]), &env); err != nil {
		t.Fatalf("stderr is not pure JSON: %v\nstderr=%s", err, stderr.String())
	}
	if env["error"] != "invalid_input" {
		t.Errorf("error = %q, want invalid_input\nstderr=%s", env["error"], stderr.String())
	}
	if detail, _ := env["detail"].(string); !strings.Contains(detail, "unknown flag") {
		t.Errorf("detail should mention 'unknown flag'; got: %v", env["detail"])
	}
	if hint, _ := env["hint"].(string); !strings.Contains(hint, "--help") {
		t.Errorf("hint should point at --help; got: %v", env["hint"])
	}
}

func TestRun_GenericErrorFallthrough(t *testing.T) {
	// A non-sentinel error should still produce structured JSON with
	// error="general_error". Without this branch test, a refactor that
	// reorders the errors.Is checks could silently drop the catch-all.
	cf := returnErrConfirmer{err: fmt.Errorf("something else entirely")}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	seedCredentials(t, srv.URL, "Acme Corp")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"channel", "create", "newchan"}, &stdout, &stderr, cf)
	if code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", code, output.ExitGeneral)
	}
	var env map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\nstderr=%s", err, stderr.String())
	}
	if env["error"] != "general_error" {
		t.Errorf("error = %q, want general_error", env["error"])
	}
}
