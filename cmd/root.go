package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/auth"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type CLI struct {
	Workspace string `short:"w" help:"Select workspace (name or ID)." env:"SLACK_WORKSPACE"`
	Fields    string `help:"Comma-separated list of top-level fields to include." env:"SLACK_FIELDS"`
	Quiet     bool   `short:"q" help:"Suppress stdout output (exit code and stderr only)."`
	Verbose   bool   `short:"v" help:"Log diagnostics to stderr as JSON."`

	Auth     AuthCmd     `cmd:"" help:"Manage authentication."`
	Channel  ChannelCmd  `cmd:"" help:"Read channel information."`
	Message  MessageCmd  `cmd:"" help:"Read messages."`
	Thread   ThreadCmd   `cmd:"" help:"Read thread replies."`
	User     UserCmd     `cmd:"" help:"Read user information."`
	Reaction ReactionCmd `cmd:"" help:"Read reactions."`
}

// ParsedFields returns the --fields value split into individual field names.
func (c *CLI) ParsedFields() []string {
	if c.Fields == "" {
		return nil
	}
	parts := strings.Split(c.Fields, ",")
	fields := make([]string, 0, len(parts))
	for _, f := range parts {
		f = strings.TrimSpace(f)
		if f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

// NewPrinter creates a Printer configured from global CLI flags.
func (c *CLI) NewPrinter() *output.Printer {
	return &output.Printer{
		Out:    os.Stdout,
		Err:    os.Stderr,
		Quiet:  c.Quiet,
		Fields: c.ParsedFields(),
	}
}

// NewClient creates an API client by resolving tokens from credentials or env vars.
func (c *CLI) NewClient() (*api.Client, error) {
	path, err := auth.DefaultCredentialsPath()
	if err != nil {
		return nil, err
	}

	bot, user, err := auth.ResolveToken(path, c.Workspace)
	if err != nil {
		return nil, err
	}

	var opts []api.Option
	if user != "" {
		opts = append(opts, api.WithUserToken(user))
	}
	return api.New(bot, opts...), nil
}

// Stub subcommands. Each will be fleshed out in its own commit.

type AuthCmd struct {
	Login  AuthLoginCmd  `cmd:"" help:"Authenticate with a Slack workspace."`
	Logout AuthLogoutCmd `cmd:"" help:"Remove stored credentials."`
	Status AuthStatusCmd `cmd:"" help:"Show current authentication state."`
}

type AuthLoginCmd struct {
	ClientID     string `env:"SLACK_CLIENT_ID" help:"Slack app client ID."`
	ClientSecret string `env:"SLACK_CLIENT_SECRET" help:"Slack app client secret."`
}

func (c *AuthLoginCmd) Run(cli *CLI) error {
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

	path, err := auth.DefaultCredentialsPath()
	if err != nil {
		return err
	}
	creds, err := auth.LoadCredentials(path)
	if err != nil {
		return err
	}

	creds.Workspaces[ws.TeamID] = *ws
	if err := auth.SaveCredentials(path, creds); err != nil {
		return err
	}

	p := cli.NewPrinter()
	if err := p.PrintItem(map[string]any{
		"team_id":        ws.TeamID,
		"team_name":      ws.TeamName,
		"user_id":        ws.UserID,
		"has_bot_token":  ws.BotToken != "",
		"has_user_token": ws.UserToken != "",
	}); err != nil {
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
				Err:  "not_authed",
				Detail: "No credentials found",
				Hint: "Run 'slack auth login' first",
				Code: output.ExitAuth,
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
			Err:  "not_authed",
			Detail: "No credentials found",
			Hint: "Run 'slack auth login' or set SLACK_TOKEN",
			Code: output.ExitAuth,
		}
	}

	p := cli.NewPrinter()
	ctx := context.Background()
	for _, ws := range creds.Workspaces {
		item := map[string]any{
			"team_id":        ws.TeamID,
			"team_name":      ws.TeamName,
			"user_id":        ws.UserID,
			"has_bot_token":  ws.BotToken != "",
			"has_user_token": ws.UserToken != "",
		}

		if ws.BotToken != "" {
			client := api.New(ws.BotToken)
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

type ChannelCmd struct {
	List    ChannelListCmd    `cmd:"" help:"List channels."`
	Info    ChannelInfoCmd    `cmd:"" help:"Show channel details."`
	Members ChannelMembersCmd `cmd:"" help:"List channel members."`
}

type ChannelListCmd struct{}

func (c *ChannelListCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type ChannelInfoCmd struct {
	Channel []string `arg:"" help:"Channel ID or name."`
}

func (c *ChannelInfoCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type ChannelMembersCmd struct {
	Channel string `arg:"" help:"Channel ID or name."`
}

func (c *ChannelMembersCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type MessageCmd struct {
	List MessageListCmd `cmd:"" aliases:"read" help:"List messages in a channel."`
	Get  MessageGetCmd  `cmd:"" help:"Get specific messages by timestamp."`
}

type MessageListCmd struct {
	Channel string `arg:"" help:"Channel ID or name."`
}

func (c *MessageListCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type MessageGetCmd struct {
	Channel    string   `arg:"" help:"Channel ID or name."`
	Timestamps []string `arg:"" help:"Message timestamps."`
}

func (c *MessageGetCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type ThreadCmd struct {
	List ThreadListCmd `cmd:"" aliases:"read" help:"List thread replies."`
}

type ThreadListCmd struct {
	Channel   string `arg:"" help:"Channel ID or name."`
	Timestamp string `arg:"" help:"Parent message timestamp."`
}

func (c *ThreadListCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type UserCmd struct {
	List UserListCmd `cmd:"" help:"List users."`
	Info UserInfoCmd `cmd:"" help:"Show user details."`
}

type UserListCmd struct{}

func (c *UserListCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type UserInfoCmd struct {
	Users []string `arg:"" help:"User ID or email."`
}

func (c *UserInfoCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type ReactionCmd struct {
	List ReactionListCmd `cmd:"" help:"List reactions on a message."`
}

type ReactionListCmd struct {
	Channel    string   `arg:"" help:"Channel ID or name."`
	Timestamps []string `arg:"" help:"Message timestamps."`
}

func (c *ReactionListCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}
