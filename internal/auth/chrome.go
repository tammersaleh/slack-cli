package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ChromeLoginOptions configures ChromeLogin behavior.
type ChromeLoginOptions struct {
	// Port connects to an existing Chrome debug instance instead of
	// launching a new one. Zero means launch a new instance.
	Port int

	// ProfileDir overrides the Chrome profile directory. Empty uses the
	// default (~/.config/slack-cli/chrome-profile/).
	ProfileDir string

	// StatusFunc is called with status messages for the user.
	StatusFunc func(string)
}

// ChromeLogin extracts Slack credentials from Chrome via CDP.
//
// If Port is set, connects to an existing Chrome debug instance.
// Otherwise, launches a new Chrome window with a dedicated profile,
// navigates to app.slack.com, and waits for the user to sign in.
//
// Returns credentials for all workspaces found in the Slack session.
func ChromeLogin(ctx context.Context, opts ChromeLoginOptions) ([]WorkspaceCredentials, error) {
	status := opts.StatusFunc
	if status == nil {
		status = func(string) {}
	}

	var allocCtx context.Context
	var cancel context.CancelFunc

	if opts.Port > 0 {
		// Connect to existing Chrome instance.
		allocCtx, cancel = chromedp.NewRemoteAllocator(ctx, fmt.Sprintf("ws://127.0.0.1:%d", opts.Port))
	} else {
		// Launch a new Chrome instance with a dedicated profile.
		profileDir := opts.ProfileDir
		if profileDir == "" {
			configDir, err := os.UserConfigDir()
			if err != nil {
				return nil, fmt.Errorf("cannot determine config directory: %w", err)
			}
			profileDir = filepath.Join(configDir, "slack-cli", "chrome-profile")
		}

		allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", false),
			chromedp.UserDataDir(profileDir),
		)
		allocCtx, cancel = chromedp.NewExecAllocator(ctx, allocOpts...)
	}
	defer cancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()

	// Navigate to Slack.
	status("Opening Slack in Chrome...")
	if err := chromedp.Run(taskCtx, chromedp.Navigate("https://app.slack.com")); err != nil {
		return nil, fmt.Errorf("navigating to Slack: %w", err)
	}

	status("Sign in to Slack in the browser window. Waiting...")

	// Poll for credentials.
	var cookie string
	var teams map[string]chromeTeam

	deadline := time.After(5 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("timed out waiting for Slack sign-in (5 minutes)")
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			c, t, err := extractCredentials(taskCtx)
			if err != nil {
				// Not ready yet, keep polling.
				continue
			}
			if c != "" && len(t) > 0 {
				cookie = c
				teams = t
				goto done
			}
		}
	}

done:
	status(fmt.Sprintf("Found %d workspace(s). Validating...", len(teams)))

	var results []WorkspaceCredentials
	for teamID, team := range teams {
		ws, err := validateChromeCredentials(ctx, team.Token, cookie)
		if err != nil {
			status(fmt.Sprintf("Warning: workspace %s failed validation: %v", teamID, err))
			continue
		}
		ws.Cookie = cookie
		ws.AuthMethod = "chrome"
		results = append(results, *ws)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no valid workspaces found after validation")
	}

	return results, nil
}

// chromeTeam represents a team entry from Slack's localConfig_v2.
type chromeTeam struct {
	Token string `json:"token"`
}

// extractCredentials attempts to read the d cookie and tokens from the
// current Chrome page via CDP.
func extractCredentials(ctx context.Context) (cookie string, teams map[string]chromeTeam, err error) {
	// Get the d cookie.
	cookies, err := network.GetCookies().WithURLs([]string{"https://app.slack.com"}).Do(cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Target))
	if err != nil {
		return "", nil, err
	}

	for _, c := range cookies {
		if c.Name == "d" && c.Value != "" {
			cookie = c.Value
			break
		}
	}
	if cookie == "" {
		return "", nil, fmt.Errorf("d cookie not found")
	}

	// Get tokens from localStorage.
	var teamsJSON string
	err = chromedp.Run(ctx, chromedp.Evaluate(`
		(() => {
			try {
				const raw = localStorage.getItem('localConfig_v2');
				if (!raw) return '';
				const config = JSON.parse(raw);
				if (!config.teams) return '';
				return JSON.stringify(config.teams);
			} catch (e) {
				return '';
			}
		})()
	`, &teamsJSON))
	if err != nil {
		return "", nil, err
	}
	if teamsJSON == "" {
		return "", nil, fmt.Errorf("localConfig_v2 not found or empty")
	}

	teams = make(map[string]chromeTeam)
	if err := json.Unmarshal([]byte(teamsJSON), &teams); err != nil {
		return "", nil, fmt.Errorf("parsing teams from localConfig_v2: %w", err)
	}

	// Filter to only xoxc- tokens.
	for k, t := range teams {
		if !strings.HasPrefix(t.Token, "xoxc-") {
			delete(teams, k)
		}
	}

	if len(teams) == 0 {
		return "", nil, fmt.Errorf("no xoxc- tokens found in localConfig_v2")
	}

	return cookie, teams, nil
}

// validateChromeCredentials calls auth.test with the given token and cookie
// to get workspace metadata.
func validateChromeCredentials(ctx context.Context, token, cookie string) (*WorkspaceCredentials, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/auth.test", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "d="+cookie)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
		URL    string `json:"url"`
		Team   string `json:"team"`
		TeamID string `json:"team_id"`
		User   string `json:"user"`
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("auth.test failed: %s", result.Error)
	}

	return &WorkspaceCredentials{
		BotToken: token,
		TeamID:   result.TeamID,
		TeamName: result.Team,
		UserID:   result.UserID,
	}, nil
}
