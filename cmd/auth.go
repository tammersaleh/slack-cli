package cmd

import (
	"context"
	"fmt"

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
	Chrome       bool   `help:"Authenticate by extracting credentials from Chrome."`
	ChromePort   int    `help:"Connect to existing Chrome debug instance on this port." default:"0"`
	ClientID     string `env:"SLACK_CLIENT_ID" help:"Slack app client ID."`
	ClientSecret string `env:"SLACK_CLIENT_SECRET" help:"Slack app client secret."`
}

func (c *AuthLoginCmd) Run(cli *CLI) error {
	if c.Chrome || c.ChromePort > 0 {
		return c.runChrome(cli)
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

func (c *AuthLoginCmd) runChrome(cli *CLI) error {
	p := cli.NewPrinter()
	ctx := context.Background()

	workspaces, err := auth.ChromeLogin(ctx, auth.ChromeLoginOptions{
		Port: c.ChromePort,
		StatusFunc: func(msg string) {
			p.PrintError(&output.Error{Err: "status", Detail: msg})
		},
	})
	if err != nil {
		return &output.Error{
			Err:    "chrome_auth_failed",
			Detail: err.Error(),
			Hint:   "Make sure you're signed in to Slack in the Chrome window",
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

	return p.PrintMeta(output.Meta{})
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
	ctx := context.Background()
	for _, ws := range creds.Workspaces {
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
