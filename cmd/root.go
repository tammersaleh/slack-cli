package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/auth"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type CLI struct {
	Workspace string `short:"w" help:"Select workspace (name or ID)." env:"SLACK_WORKSPACE"`
	Format    string `short:"f" enum:"json,text" default:"json" help:"Output format." env:"SLACK_FORMAT"`
	Quiet     bool   `short:"q" help:"Suppress non-essential output."`
	Verbose   bool   `short:"v" help:"Include extra diagnostic info on stderr."`
	NoPager   bool   `help:"Disable automatic paging of long output."`
	Limit     uint   `help:"Max items to return for list commands (0 = all)." default:"0"`
	Raw       bool   `help:"Output raw API responses without transformation."`

	Auth     AuthCmd     `cmd:"" help:"Manage authentication."`
	Channel  ChannelCmd  `cmd:"" help:"Read channel information."`
	Message  MessageCmd  `cmd:"" help:"Read messages."`
	Thread   ThreadCmd   `cmd:"" help:"Read thread replies."`
	User     UserCmd     `cmd:"" help:"Read user information."`
	Reaction ReactionCmd `cmd:"" help:"Read reactions."`
}

// Stub subcommands. Each will be fleshed out in later issues.

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
		return fmt.Errorf("SLACK_CLIENT_ID and SLACK_CLIENT_SECRET must be set; see README for setup instructions")
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

	p := &output.Printer{Out: os.Stdout, Err: os.Stderr, Quiet: cli.Quiet}
	return p.Print(map[string]string{
		"status":    "authenticated",
		"workspace": ws.TeamName,
		"team_id":   ws.TeamID,
		"user_id":   ws.UserID,
	})
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

	// If still empty, and only one workspace, use that.
	if workspace == "" {
		if len(creds.Workspaces) == 1 {
			for name := range creds.Workspaces {
				workspace = name
			}
		} else if len(creds.Workspaces) > 1 {
			return fmt.Errorf("multiple workspaces configured; specify which to log out of")
		} else {
			return fmt.Errorf("no credentials found")
		}
	}

	if _, ok := creds.Workspaces[workspace]; !ok {
		return fmt.Errorf("workspace %q not found", workspace)
	}

	delete(creds.Workspaces, workspace)
	if err := auth.SaveCredentials(path, creds); err != nil {
		return err
	}

	p := &output.Printer{Out: os.Stdout, Err: os.Stderr, Quiet: cli.Quiet}
	return p.Print(map[string]string{
		"status":    "logged out",
		"workspace": workspace,
	})
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
		return fmt.Errorf("not authenticated; run 'slack auth login' first")
	}

	type workspaceStatus struct {
		TeamName     string `json:"team_name"`
		TeamID       string `json:"team_id"`
		User         string `json:"user"`
		UserID       string `json:"user_id"`
		URL          string `json:"url,omitempty"`
		HasBotToken  bool   `json:"has_bot_token"`
		HasUserToken bool   `json:"has_user_token"`
		TokenValid   bool   `json:"token_valid"`
		Error        string `json:"error,omitempty"`
	}

	ctx := context.Background()
	statuses := []workspaceStatus{}
	for _, ws := range creds.Workspaces {
		status := workspaceStatus{
			TeamName:     ws.TeamName,
			TeamID:       ws.TeamID,
			UserID:       ws.UserID,
			HasBotToken:  ws.BotToken != "",
			HasUserToken: ws.UserToken != "",
		}

		if ws.BotToken != "" {
			client := api.New(ws.BotToken)
			result, err := client.AuthTest(ctx)
			if err != nil {
				status.Error = err.Error()
			} else {
				status.TokenValid = true
				status.User = result.User
				status.URL = result.URL
			}
		}

		statuses = append(statuses, status)
	}

	p := &output.Printer{Out: os.Stdout, Err: os.Stderr, Quiet: cli.Quiet}
	if len(statuses) == 1 {
		return p.Print(statuses[0])
	}
	return p.Print(statuses)
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
	Channel string `arg:"" help:"Channel ID or name."`
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
	Get  MessageGetCmd  `cmd:"" help:"Get a single message by timestamp."`
}

type MessageListCmd struct {
	Channel string `arg:"" help:"Channel ID or name."`
}

func (c *MessageListCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type MessageGetCmd struct {
	Channel   string `arg:"" help:"Channel ID or name."`
	Timestamp string `arg:"" help:"Message timestamp."`
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
	User string `arg:"" help:"User ID, @name, or email."`
}

func (c *UserInfoCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}

type ReactionCmd struct {
	List ReactionListCmd `cmd:"" help:"List reactions on a message."`
}

type ReactionListCmd struct {
	Channel   string `arg:"" help:"Channel ID or name."`
	Timestamp string `arg:"" help:"Message timestamp."`
}

func (c *ReactionListCmd) Run(cli *CLI) error {
	return fmt.Errorf("not implemented")
}
