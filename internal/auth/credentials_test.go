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
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}

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
	if err := os.WriteFile(path, []byte("{bad json"), 0600); err != nil {
		t.Fatal(err)
	}

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

func TestResolveCredentials_EnvVarOverride(t *testing.T) {
	t.Setenv("SLACK_TOKEN", "xoxb-env")
	t.Setenv("SLACK_USER_TOKEN", "xoxp-env")

	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-stored", UserToken: "xoxp-stored"},
		},
	}
	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	rc, err := ResolveCredentials(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.BotToken != "xoxb-env" {
		t.Errorf("got bot=%q, want %q", rc.BotToken, "xoxb-env")
	}
	if rc.UserToken != "xoxp-env" {
		t.Errorf("got user=%q, want %q", rc.UserToken, "xoxp-env")
	}
	if rc.AuthMethod != "" {
		t.Errorf("expected empty auth_method for env var, got %q", rc.AuthMethod)
	}
}

func TestResolveCredentials_EnvVarWithCookie(t *testing.T) {
	t.Setenv("SLACK_TOKEN", "xoxc-env")
	t.Setenv("SLACK_COOKIE", "xoxd-cookie")

	rc, err := ResolveCredentials(filepath.Join(t.TempDir(), "creds.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.BotToken != "xoxc-env" {
		t.Errorf("got bot=%q, want %q", rc.BotToken, "xoxc-env")
	}
	if rc.Cookie != "xoxd-cookie" {
		t.Errorf("got cookie=%q, want %q", rc.Cookie, "xoxd-cookie")
	}
	if rc.UserToken != "xoxc-env" {
		t.Errorf("got user=%q, want %q; xoxc- token should auto-promote", rc.UserToken, "xoxc-env")
	}
}

func TestResolveCredentials_SingleWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"onlyteam": {BotToken: "xoxb-only", UserToken: "xoxp-only", AuthMethod: "oauth"},
		},
	}
	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	rc, err := ResolveCredentials(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.BotToken != "xoxb-only" {
		t.Errorf("got bot=%q, want %q", rc.BotToken, "xoxb-only")
	}
	if rc.UserToken != "xoxp-only" {
		t.Errorf("got user=%q, want %q", rc.UserToken, "xoxp-only")
	}
	if rc.AuthMethod != "oauth" {
		t.Errorf("got auth_method=%q, want %q", rc.AuthMethod, "oauth")
	}
}

func TestResolveCredentials_ChromeWorkspaceWithCookie(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {
				BotToken:   "xoxc-chrome",
				Cookie:     "xoxd-stored",
				AuthMethod: "chrome",
				TeamID:     "T1",
				TeamName:   "Chrome Team",
			},
		},
	}
	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	rc, err := ResolveCredentials(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.BotToken != "xoxc-chrome" {
		t.Errorf("got bot=%q, want %q", rc.BotToken, "xoxc-chrome")
	}
	if rc.Cookie != "xoxd-stored" {
		t.Errorf("got cookie=%q, want %q", rc.Cookie, "xoxd-stored")
	}
	if rc.AuthMethod != "chrome" {
		t.Errorf("got auth_method=%q, want %q", rc.AuthMethod, "chrome")
	}
}

func TestResolveCredentials_NamedWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-1"},
			"team2": {BotToken: "xoxb-2"},
		},
	}
	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	rc, err := ResolveCredentials(path, "team2")
	if err != nil {
		t.Fatal(err)
	}
	if rc.BotToken != "xoxb-2" {
		t.Errorf("got bot=%q, want %q", rc.BotToken, "xoxb-2")
	}
}

func TestResolveCredentials_MultipleWorkspacesNoSelection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-1"},
			"team2": {BotToken: "xoxb-2"},
		},
	}
	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveCredentials(path, "")
	if err == nil {
		t.Error("expected error when multiple workspaces and none selected")
	}
}

func TestResolveCredentials_UnknownWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {BotToken: "xoxb-1"},
		},
	}
	if err := SaveCredentials(path, creds); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveCredentials(path, "nonexistent")
	if err == nil {
		t.Error("expected error for unknown workspace")
	}
}

func TestResolveCredentials_NoCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	_, err := ResolveCredentials(path, "")
	if err == nil {
		t.Error("expected error when no credentials exist")
	}
}

func TestResolveCredentials_XoxcAutoPromotesToUserToken(t *testing.T) {
	// xoxc- tokens are user/session tokens and should be usable as UserToken
	// for operations like search that require client.User().
	t.Run("env var", func(t *testing.T) {
		t.Setenv("SLACK_TOKEN", "xoxc-session")
		t.Setenv("SLACK_COOKIE", "xoxd-cookie")

		rc, err := ResolveCredentials(filepath.Join(t.TempDir(), "creds.json"), "")
		if err != nil {
			t.Fatal(err)
		}
		if rc.UserToken != "xoxc-session" {
			t.Errorf("got user=%q, want %q; xoxc- bot token should auto-promote to user token", rc.UserToken, "xoxc-session")
		}
	})

	t.Run("single workspace", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials.json")

		creds := &Credentials{
			Workspaces: map[string]WorkspaceCredentials{
				"team1": {BotToken: "xoxc-desktop", Cookie: "xoxd-cookie", AuthMethod: "desktop"},
			},
		}
		if err := SaveCredentials(path, creds); err != nil {
			t.Fatal(err)
		}

		rc, err := ResolveCredentials(path, "")
		if err != nil {
			t.Fatal(err)
		}
		if rc.UserToken != "xoxc-desktop" {
			t.Errorf("got user=%q, want %q; xoxc- bot token should auto-promote to user token", rc.UserToken, "xoxc-desktop")
		}
	})

	t.Run("named workspace", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials.json")

		creds := &Credentials{
			Workspaces: map[string]WorkspaceCredentials{
				"team1": {BotToken: "xoxc-desktop", Cookie: "xoxd-cookie", AuthMethod: "desktop"},
				"team2": {BotToken: "xoxb-bot"},
			},
		}
		if err := SaveCredentials(path, creds); err != nil {
			t.Fatal(err)
		}

		rc, err := ResolveCredentials(path, "team1")
		if err != nil {
			t.Fatal(err)
		}
		if rc.UserToken != "xoxc-desktop" {
			t.Errorf("got user=%q, want %q; xoxc- bot token should auto-promote to user token", rc.UserToken, "xoxc-desktop")
		}
	})

	t.Run("no promotion when explicit user token set", func(t *testing.T) {
		t.Setenv("SLACK_TOKEN", "xoxc-session")
		t.Setenv("SLACK_USER_TOKEN", "xoxp-explicit")

		rc, err := ResolveCredentials(filepath.Join(t.TempDir(), "creds.json"), "")
		if err != nil {
			t.Fatal(err)
		}
		if rc.UserToken != "xoxp-explicit" {
			t.Errorf("got user=%q, want %q; explicit user token should not be overwritten", rc.UserToken, "xoxp-explicit")
		}
	})

	t.Run("no promotion for xoxb tokens", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials.json")

		creds := &Credentials{
			Workspaces: map[string]WorkspaceCredentials{
				"team1": {BotToken: "xoxb-bot", AuthMethod: "oauth"},
			},
		}
		if err := SaveCredentials(path, creds); err != nil {
			t.Fatal(err)
		}

		rc, err := ResolveCredentials(path, "")
		if err != nil {
			t.Fatal(err)
		}
		if rc.UserToken != "" {
			t.Errorf("got user=%q, want empty; xoxb- tokens should not promote", rc.UserToken)
		}
	})
}

func TestSaveCredentials_RoundTripWithChromeFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	creds := &Credentials{
		Workspaces: map[string]WorkspaceCredentials{
			"team1": {
				BotToken:   "xoxc-chrome",
				Cookie:     "xoxd-cookie",
				AuthMethod: "chrome",
				TeamID:     "T1",
				TeamName:   "Team 1",
				UserID:     "U1",
			},
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
	if ws.Cookie != "xoxd-cookie" {
		t.Errorf("got cookie=%q, want %q", ws.Cookie, "xoxd-cookie")
	}
	if ws.AuthMethod != "chrome" {
		t.Errorf("got auth_method=%q, want %q", ws.AuthMethod, "chrome")
	}
}
