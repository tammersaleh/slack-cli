package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Credentials struct {
	Workspaces map[string]WorkspaceCredentials `json:"workspaces"`
}

type WorkspaceCredentials struct {
	BotToken  string `json:"bot_token"`
	UserToken string `json:"user_token,omitempty"`
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
	UserID    string `json:"user_id"`
}

// DefaultCredentialsPath returns ~/.config/slack-cli/credentials.json.
func DefaultCredentialsPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "slack-cli", "credentials.json")
}

// LoadCredentials reads and parses the credentials file.
// Returns empty Credentials if the file does not exist.
func LoadCredentials(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Credentials{Workspaces: map[string]WorkspaceCredentials{}}, nil
		}
		return nil, err
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	if creds.Workspaces == nil {
		creds.Workspaces = map[string]WorkspaceCredentials{}
	}
	return &creds, nil
}

// SaveCredentials writes credentials to the given path with 0600 permissions.
// Creates parent directories if needed.
func SaveCredentials(path string, creds *Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// ResolveToken returns the bot and user tokens for the given workspace.
// SLACK_TOKEN and SLACK_USER_TOKEN env vars take precedence over stored
// credentials. If workspace is empty and only one workspace is stored,
// it is used automatically.
func ResolveToken(path string, workspace string) (bot, user string, err error) {
	envBot := os.Getenv("SLACK_TOKEN")
	envUser := os.Getenv("SLACK_USER_TOKEN")
	if envBot != "" {
		return envBot, envUser, nil
	}

	creds, err := LoadCredentials(path)
	if err != nil {
		return "", "", err
	}

	if len(creds.Workspaces) == 0 {
		return "", "", fmt.Errorf("no credentials found; run 'slack auth login' first")
	}

	if workspace == "" {
		if len(creds.Workspaces) == 1 {
			for _, ws := range creds.Workspaces {
				return ws.BotToken, ws.UserToken, nil
			}
		}
		return "", "", fmt.Errorf("multiple workspaces configured; use --workspace to select one")
	}

	ws, ok := creds.Workspaces[workspace]
	if !ok {
		return "", "", fmt.Errorf("workspace %q not found in credentials", workspace)
	}
	return ws.BotToken, ws.UserToken, nil
}
