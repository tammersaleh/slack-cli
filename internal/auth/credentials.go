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
	BotToken   string `json:"bot_token"`
	UserToken  string `json:"user_token,omitempty"`
	Cookie     string `json:"cookie,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"`
	TeamID     string `json:"team_id"`
	TeamName   string `json:"team_name"`
	UserID     string `json:"user_id"`
}

// ResolvedCredentials holds the resolved token, cookie, and auth method
// for a single workspace.
type ResolvedCredentials struct {
	BotToken   string
	UserToken  string
	Cookie     string
	AuthMethod string
	TeamID     string
}

// DefaultCredentialsPath returns ~/.config/slack-cli/credentials.json.
func DefaultCredentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config directory: %w", err)
	}
	return filepath.Join(dir, "slack-cli", "credentials.json"), nil
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

// ResolveCredentials returns the resolved credentials for the given workspace.
// SLACK_TOKEN, SLACK_USER_TOKEN, and SLACK_COOKIE env vars take precedence
// over stored credentials. If workspace is empty and only one workspace is
// stored, it is used automatically.
func ResolveCredentials(path string, workspace string) (*ResolvedCredentials, error) {
	envBot := os.Getenv("SLACK_TOKEN")
	envUser := os.Getenv("SLACK_USER_TOKEN")
	envCookie := os.Getenv("SLACK_COOKIE")
	if envBot != "" {
		return &ResolvedCredentials{
			BotToken:  envBot,
			UserToken: envUser,
			Cookie:    envCookie,
		}, nil
	}

	creds, err := LoadCredentials(path)
	if err != nil {
		return nil, err
	}

	if len(creds.Workspaces) == 0 {
		return nil, fmt.Errorf("no credentials found; run 'slack auth login' first")
	}

	if workspace == "" {
		if len(creds.Workspaces) == 1 {
			for key, ws := range creds.Workspaces {
				return &ResolvedCredentials{
					BotToken:   ws.BotToken,
					UserToken:  ws.UserToken,
					Cookie:     ws.Cookie,
					AuthMethod: ws.AuthMethod,
					TeamID:     key,
				}, nil
			}
		}
		return nil, fmt.Errorf("multiple workspaces configured; use --workspace to select one")
	}

	ws, ok := creds.Workspaces[workspace]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found in credentials", workspace)
	}
	return &ResolvedCredentials{
		BotToken:   ws.BotToken,
		UserToken:  ws.UserToken,
		Cookie:     ws.Cookie,
		AuthMethod: ws.AuthMethod,
		TeamID:     workspace,
	}, nil
}
