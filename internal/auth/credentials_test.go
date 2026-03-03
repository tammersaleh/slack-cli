package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCredentials_MissingFile(t *testing.T) {
	creds, err := LoadCredentials(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if len(creds.Workspaces) != 0 {
		t.Errorf("expected empty workspaces, got %d", len(creds.Workspaces))
	}
}

func TestLoadCredentials_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	data := Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"myteam": {
				BotToken: "xoxb-123",
				TeamID:   "T123",
				TeamName: "My Team",
				UserID:   "U456",
			},
		},
	}
	raw, _ := json.Marshal(data)
	os.WriteFile(path, raw, 0600)

	creds, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	ws, ok := creds.Workspaces["myteam"]
	if !ok {
		t.Fatal("missing workspace 'myteam'")
	}
	if ws.BotToken != "xoxb-123" {
		t.Errorf("got bot_token=%q, want %q", ws.BotToken, "xoxb-123")
	}
	if ws.TeamName != "My Team" {
		t.Errorf("got team_name=%q, want %q", ws.TeamName, "My Team")
	}
}

func TestLoadCredentials_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	os.WriteFile(path, []byte("{bad json"), 0600)

	_, err := LoadCredentials(path)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestSaveCredentials_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-aaa", UserToken: "xoxp-bbb", TeamID: "T1", TeamName: "Team 1", UserID: "U1"},
		},
	}

	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	ws := loaded.Workspaces["team1"]
	if ws.BotToken != "xoxb-aaa" {
		t.Errorf("got bot_token=%q, want %q", ws.BotToken, "xoxb-aaa")
	}
	if ws.UserToken != "xoxp-bbb" {
		t.Errorf("got user_token=%q, want %q", ws.UserToken, "xoxp-bbb")
	}
}

func TestSaveCredentials_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{Workspaces: map[string]WorkspaceCredentials{}}
	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("got permissions %o, want 0600", info.Mode().Perm())
	}
}

func TestSaveCredentials_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{Workspaces: map[string]WorkspaceCredentials{}}
	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should exist: %v", err)
	}
}

func TestResolveToken_EnvVarOverride(t *testing.T) {
	t.Setenv("SLACK_TOKEN", "xoxb-env")
	t.Setenv("SLACK_USER_TOKEN", "xoxp-env")

	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	// Even with a credentials file, env vars win.
	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-stored", UserToken: "xoxp-stored"},
		},
	}
	SaveCredentials(path, creds)

	bot, user, err := ResolveToken(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if bot != "xoxb-env" {
		t.Errorf("got bot=%q, want %q", bot, "xoxb-env")
	}
	if user != "xoxp-env" {
		t.Errorf("got user=%q, want %q", user, "xoxp-env")
	}
}

func TestResolveToken_EnvVarPartialOverride(t *testing.T) {
	t.Setenv("SLACK_TOKEN", "xoxb-env")
	// SLACK_USER_TOKEN not set

	bot, user, err := ResolveToken(filepath.Join(t.TempDir(), "creds.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	if bot != "xoxb-env" {
		t.Errorf("got bot=%q, want %q", bot, "xoxb-env")
	}
	if user != "" {
		t.Errorf("got user=%q, want empty", user)
	}
}

func TestResolveToken_SingleWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"onlyteam": {BotToken: "xoxb-only", UserToken: "xoxp-only"},
		},
	}
	SaveCredentials(path, creds)

	bot, user, err := ResolveToken(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if bot != "xoxb-only" {
		t.Errorf("got bot=%q, want %q", bot, "xoxb-only")
	}
	if user != "xoxp-only" {
		t.Errorf("got user=%q, want %q", user, "xoxp-only")
	}
}

func TestResolveToken_NamedWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-1"},
			"team2": {BotToken: "xoxb-2"},
		},
	}
	SaveCredentials(path, creds)

	bot, _, err := ResolveToken(path, "team2")
	if err != nil {
		t.Fatal(err)
	}
	if bot != "xoxb-2" {
		t.Errorf("got bot=%q, want %q", bot, "xoxb-2")
	}
}

func TestResolveToken_MultipleWorkspacesNoSelection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-1"},
			"team2": {BotToken: "xoxb-2"},
		},
	}
	SaveCredentials(path, creds)

	_, _, err := ResolveToken(path, "")
	if err == nil {
		t.Error("expected error when multiple workspaces and none selected")
	}
}

func TestResolveToken_UnknownWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-1"},
		},
	}
	SaveCredentials(path, creds)

	_, _, err := ResolveToken(path, "nonexistent")
	if err == nil {
		t.Error("expected error for unknown workspace")
	}
}

func TestResolveToken_NoCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	_, _, err := ResolveToken(path, "")
	if err == nil {
		t.Error("expected error when no credentials exist")
	}
}
