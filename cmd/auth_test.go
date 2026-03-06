package cmd_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	slack "github.com/slack-go/slack"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/auth"
)

// runWithMockCookie runs a CLI command with a credentials file containing
// a Chrome-authed workspace (xoxc- token + cookie) against a mock server.
func runWithMockCookie(t *testing.T, handler http.Handler, token, cookie string, args ...string) (string, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Override HOME so DefaultCredentialsPath() points to our temp dir.
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write credentials where DefaultCredentialsPath() will find them.
	creds := &auth.Credentials{
		Workspaces: map[string]auth.WorkspaceCredentials{
			"T123": {
				BotToken:   token,
				Cookie:     cookie,
				AuthMethod: "chrome",
				TeamID:     "T123",
				TeamName:   "Test Team",
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
		return "", err
	}

	cli.SetOutput(&outBuf, &errBuf)
	runErr := kctx.Run(&cli)
	return outBuf.String(), runErr
}

func TestClassifyError_AuthHints(t *testing.T) {
	tests := []struct {
		name       string
		authMethod string
		wantHint   string
	}{
		{"chrome", "chrome", "Run 'slack auth login --chrome' to re-authenticate"},
		{"oauth", "oauth", "Run 'slack auth login' to re-authenticate"},
		{"default", "", "Run 'slack auth login' or set SLACK_TOKEN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a CLI with the given auth method by running NewClient
			// against a mock that returns an auth error.
			var cli cmd.CLI
			cli.SetAuthMethod(tt.authMethod)

			oErr := cli.ClassifyError(slack.SlackErrorResponse{Err: "invalid_auth"})
			if oErr.Hint != tt.wantHint {
				t.Errorf("got hint %q, want %q", oErr.Hint, tt.wantHint)
			}
		})
	}
}

func TestAuthStatus_ChromeWorkspace(t *testing.T) {
	mux := http.NewServeMux()
	var gotCookie string
	mux.HandleFunc("/api/auth.test", func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     "https://test.slack.com/",
			"team":    "Test Team",
			"team_id": "T123",
			"user":    "tammer",
			"user_id": "U456",
		})
	})

	out, err := runWithMockCookie(t, mux, "xoxc-chrome-token", "xoxd-test-cookie", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}

	if gotCookie != "d=xoxd-test-cookie" {
		t.Errorf("expected cookie header %q, got %q", "d=xoxd-test-cookie", gotCookie)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 workspace + meta), got %d:\n%s", len(lines), out)
	}

	ws := parseJSON(t, lines[0])
	if ws["user"] != "tammer" {
		t.Errorf("expected user='tammer', got %q", ws["user"])
	}
	if ws["auth_method"] != "chrome" {
		t.Errorf("expected auth_method='chrome', got %q", ws["auth_method"])
	}
}
