package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/auth"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type AuthCmd struct {
	Login  AuthLoginCmd  `cmd:"" help:"Authenticate with a Slack workspace."`
	Logout AuthLogoutCmd `cmd:"" help:"Remove stored credentials."`
	Status AuthStatusCmd `cmd:"" help:"Show current authentication state."`
}

type AuthLoginCmd struct {
	Desktop      bool   `help:"Extract credentials from Slack Desktop app."`
	ClientID     string `env:"SLACK_CLIENT_ID" help:"Slack app client ID."`
	ClientSecret string `env:"SLACK_CLIENT_SECRET" help:"Slack app client secret."`
}

func (c *AuthLoginCmd) Run(cli *CLI) error {
	if c.Desktop {
		return c.runDesktop(cli)
	}
	return c.runOAuth(cli)
}

func (c *AuthLoginCmd) runOAuth(cli *CLI) error {
	if c.ClientID == "" || c.ClientSecret == "" {
		return &output.Error{
			Err:    "missing_client_credentials",
			Detail: "SLACK_CLIENT_ID and SLACK_CLIENT_SECRET must be set",
			Hint:   "See README for Slack app setup instructions",
			Code:   output.ExitGeneral,
		}
	}

	ws, err := auth.Login(context.Background(), c.ClientID, c.ClientSecret)
	if err != nil {
		return err
	}
	ws.AuthMethod = "oauth"

	return saveAndPrintWorkspaces(cli, []auth.WorkspaceCredentials{*ws})
}

func (c *AuthLoginCmd) runDesktop(cli *CLI) error {
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	workspaces, err := auth.DesktopLogin(ctx, auth.DesktopLoginOptions{
		HTTPClient: api.ChromeTLSClient(),
		StatusFunc: func(msg string) {
			_ = p.PrintError(&output.Error{Err: "status", Detail: msg})
		},
	})
	if err != nil {
		return &output.Error{
			Err:    "desktop_auth_failed",
			Detail: err.Error(),
			Hint:   "Make sure Slack Desktop is installed and you're signed in",
			Code:   output.ExitGeneral,
		}
	}

	return saveAndPrintWorkspaces(cli, workspaces)
}

func saveAndPrintWorkspaces(cli *CLI, workspaces []auth.WorkspaceCredentials) error {
	path, err := auth.DefaultCredentialsPath()
	if err != nil {
		return err
	}
	creds, err := auth.LoadCredentials(path)
	if err != nil {
		return err
	}

	p := cli.NewPrinter()

	sortWorkspaces(workspaces)
	for _, ws := range workspaces {
		creds.Workspaces[ws.TeamID] = ws
		if err := p.PrintItem(map[string]any{
			"team_id":        ws.TeamID,
			"team_name":      ws.TeamName,
			"user_id":        ws.UserID,
			"auth_method":    ws.AuthMethod,
			"has_bot_token":  ws.BotToken != "",
			"has_user_token": ws.UserToken != "",
		}); err != nil {
			return err
		}
	}

	if err := auth.SaveCredentials(path, creds); err != nil {
		return err
	}

	if err := p.PrintMeta(output.Meta{}); err != nil {
		return err
	}

	// Read the env directly rather than cli.Workspace: kong merges --workspace
	// into that field, and a one-shot flag on login is not the persistent
	// default these hints describe.
	printWorkspaceHints(p.Err, os.Getenv("SLACK_WORKSPACE"), os.Getenv("SLACK_WORKSPACE_ORG"), creds.Workspaces)
	return nil
}

// sortWorkspaces orders by team name (case-insensitive), then team ID.
// DesktopLogin builds its result from a map, so without this the row order
// and every hint derived from it changed run to run.
func sortWorkspaces(ws []auth.WorkspaceCredentials) {
	sort.SliceStable(ws, func(i, j int) bool {
		a, b := strings.ToLower(ws[i].TeamName), strings.ToLower(ws[j].TeamName)
		if a != b {
			return a < b
		}
		return ws[i].TeamID < ws[j].TeamID
	})
}

// isOrgWorkspace reports whether a team ID names an Enterprise Grid org
// context rather than a workspace inside it.
func isOrgWorkspace(teamID string) bool {
	return strings.HasPrefix(teamID, "E")
}

// printWorkspaceHints writes SLACK_WORKSPACE / SLACK_WORKSPACE_ORG guidance
// to w after login. current and currentOrg are the env values already in
// effect; saved is every stored workspace, not just the ones this login
// produced, since a re-login may add workspaces without changing which one
// the user selects by default. Matching is by credentials map key - the same
// exact lookup ResolveCredentials performs - so the hint never affirms a
// value that a later command would reject. For SLACK_WORKSPACE it also never
// calls stale a value ResolveCredentials would accept: an E-org id there is
// a real, if limited, selection, and the org-only fallback below depends on
// it. SLACK_WORKSPACE_ORG is stricter - see the E-prefix check below.
//
// A value that names a saved workspace is reported as set and gets no export
// suggestion - the old unconditional hint read as "you have no workspace
// set" when the user plainly did. A value that names nothing saved is called
// out as stale. An unset value gets every candidate listed, because picking
// one for the user was arbitrary.
func printWorkspaceHints(w io.Writer, current, currentOrg string, saved map[string]auth.WorkspaceCredentials) {
	if len(saved) == 0 {
		return
	}

	var regular, orgs []auth.WorkspaceCredentials
	for key, ws := range saved {
		ws.TeamID = key // the map key is the canonical ID ResolveCredentials looks up
		if isOrgWorkspace(key) {
			orgs = append(orgs, ws)
		} else {
			regular = append(regular, ws)
		}
	}
	sortWorkspaces(regular)
	sortWorkspaces(orgs)

	// SLACK_WORKSPACE accepts any saved key. With only org-level credentials
	// stored, those are the candidates - the previous hint fell back the same
	// way, and dropping it would leave an org-only setup with no default.
	candidates := regular
	if len(candidates) == 0 {
		candidates = orgs
	}

	fmt.Fprintln(w)
	if ws, ok := saved[current]; ok && current != "" {
		fmt.Fprintf(w, "SLACK_WORKSPACE is set to %s  # %s\n", current, ws.TeamName)
	} else {
		if current != "" {
			fmt.Fprintf(w, "SLACK_WORKSPACE=%s does not match any saved workspace. Set one of:\n", current)
		} else {
			fmt.Fprintln(w, "Set your default workspace:")
		}
		fmt.Fprintln(w)
		for _, ws := range candidates {
			fmt.Fprintf(w, "  export SLACK_WORKSPACE=%s  # %s\n", ws.TeamID, ws.TeamName)
		}
	}

	// Unlike SLACK_WORKSPACE, the org selector must name an org context: a
	// T-prefixed id there is a real credential but the wrong kind, and the
	// internal APIs answer team_is_restricted on it.
	if ws, ok := saved[currentOrg]; ok && isOrgWorkspace(currentOrg) {
		fmt.Fprintf(w, "SLACK_WORKSPACE_ORG is set to %s  # %s (org)\n", currentOrg, ws.TeamName)
	} else if currentOrg != "" || len(orgs) > 0 {
		fmt.Fprintln(w)
		if currentOrg != "" {
			fmt.Fprintf(w, "SLACK_WORKSPACE_ORG=%s does not match any saved org.\n", currentOrg)
		} else {
			fmt.Fprintln(w, "Enterprise Grid detected. Internal APIs (saved items, sidebar")
			fmt.Fprintln(w, "sections) require the org-level token:")
		}
		if len(orgs) > 0 {
			fmt.Fprintln(w)
			for _, ws := range orgs {
				fmt.Fprintf(w, "  export SLACK_WORKSPACE_ORG=%s  # %s (org)\n", ws.TeamID, ws.TeamName)
			}
		}
	}
	fmt.Fprintln(w)
}

type AuthLogoutCmd struct {
	Workspace string `arg:"" optional:"" help:"Workspace to log out of (default: current)."`
}

func (c *AuthLogoutCmd) Run(cli *CLI) error {
	path, err := auth.DefaultCredentialsPath()
	if err != nil {
		return err
	}
	creds, err := auth.LoadCredentials(path)
	if err != nil {
		return err
	}

	workspace := c.Workspace
	if workspace == "" {
		workspace = cli.Workspace
	}

	if workspace == "" {
		if len(creds.Workspaces) == 1 {
			for name := range creds.Workspaces {
				workspace = name
			}
		} else if len(creds.Workspaces) > 1 {
			return &output.Error{
				Err:    "not_authed",
				Detail: "Multiple workspaces configured; specify which to log out of",
				Code:   output.ExitAuth,
			}
		} else {
			return &output.Error{
				Err:    "not_authed",
				Detail: "No credentials found",
				Hint:   "Run 'slack auth login' first",
				Code:   output.ExitAuth,
			}
		}
	}

	ws, ok := creds.Workspaces[workspace]
	if !ok {
		return &output.Error{
			Err:    "not_authed",
			Detail: fmt.Sprintf("Workspace %q not found", workspace),
			Code:   output.ExitAuth,
		}
	}

	delete(creds.Workspaces, workspace)
	if err := auth.SaveCredentials(path, creds); err != nil {
		return err
	}

	p := cli.NewPrinter()
	if err := p.PrintItem(map[string]any{
		"team_id":   workspace,
		"team_name": ws.TeamName,
		"status":    "logged_out",
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

type AuthStatusCmd struct{}

func (c *AuthStatusCmd) Run(cli *CLI) error {
	path, err := auth.DefaultCredentialsPath()
	if err != nil {
		return err
	}
	creds, err := auth.LoadCredentials(path)
	if err != nil {
		return err
	}

	if len(creds.Workspaces) == 0 {
		return &output.Error{
			Err:    "not_authed",
			Detail: "No credentials found",
			Hint:   "Run 'slack auth login' or set SLACK_TOKEN",
			Code:   output.ExitAuth,
		}
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	workspaces := creds.Workspaces
	if cli.Workspace != "" {
		ws, ok := creds.Workspaces[cli.Workspace]
		if !ok {
			return &output.Error{
				Err:    "not_authed",
				Detail: fmt.Sprintf("Workspace %q not found", cli.Workspace),
				Code:   output.ExitAuth,
			}
		}
		workspaces = map[string]auth.WorkspaceCredentials{cli.Workspace: ws}
	}

	for _, ws := range workspaces {
		item := map[string]any{
			"team_id":        ws.TeamID,
			"team_name":      ws.TeamName,
			"user_id":        ws.UserID,
			"auth_method":    ws.AuthMethod,
			"has_bot_token":  ws.BotToken != "",
			"has_user_token": ws.UserToken != "",
		}

		if ws.BotToken != "" {
			var opts []api.Option
			if ws.Cookie != "" {
				opts = append(opts, api.WithCookie(ws.Cookie))
			}
			if cli.APIBaseURL != "" {
				opts = append(opts, api.WithAPIURL(cli.APIBaseURL))
			}
			client := api.New(ws.BotToken, opts...)
			result, err := client.AuthTest(ctx)
			if err != nil {
				item["error"] = err.Error()
			} else {
				item["user"] = result.User
				item["url"] = result.URL
			}
		}

		if err := p.PrintItem(item); err != nil {
			return err
		}
	}
	return p.PrintMeta(output.Meta{})
}
