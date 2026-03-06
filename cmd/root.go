package cmd

import (
	"io"
	"os"
	"strings"

	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/auth"
	"github.com/tammersaleh/slack-cli/internal/output"
	"github.com/tammersaleh/slack-cli/internal/resolve"
)

type CLI struct {
	Workspace  string `short:"w" help:"Select workspace (name or ID)." env:"SLACK_WORKSPACE"`
	Fields     string `help:"Comma-separated list of top-level fields to include." env:"SLACK_FIELDS"`
	Quiet      bool   `short:"q" help:"Suppress stdout output (exit code and stderr only)."`
	APIBaseURL string `hidden:"" env:"SLACK_API_URL" help:"Override Slack API base URL (for testing)."`

	// stdout/stderr overrides for testing.
	out io.Writer
	err io.Writer

	// authMethod is set by NewClient from resolved credentials.
	authMethod string

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

// SetOutput overrides stdout/stderr for testing.
func (c *CLI) SetOutput(out, errW io.Writer) {
	c.out = out
	c.err = errW
}

// SetAuthMethod overrides the auth method for testing ClassifyError hints.
func (c *CLI) SetAuthMethod(method string) {
	c.authMethod = method
}

// NewPrinter creates a Printer configured from global CLI flags.
func (c *CLI) NewPrinter() *output.Printer {
	out := io.Writer(os.Stdout)
	errW := io.Writer(os.Stderr)
	if c.out != nil {
		out = c.out
	}
	if c.err != nil {
		errW = c.err
	}
	return &output.Printer{
		Out:    out,
		Err:    errW,
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

	rc, err := auth.ResolveCredentials(path, c.Workspace)
	if err != nil {
		hint := "Run 'slack auth login' or set SLACK_TOKEN"
		return nil, &output.Error{
			Err:    "not_authed",
			Detail: err.Error(),
			Hint:   hint,
			Code:   output.ExitAuth,
		}
	}

	c.authMethod = rc.AuthMethod

	var opts []api.Option
	if rc.UserToken != "" {
		opts = append(opts, api.WithUserToken(rc.UserToken))
	}
	if rc.Cookie != "" {
		opts = append(opts, api.WithCookie(rc.Cookie))
	}
	if c.APIBaseURL != "" {
		opts = append(opts, api.WithAPIURL(c.APIBaseURL))
	}
	return api.New(rc.BotToken, opts...), nil
}

// ClassifyError wraps api.ClassifyError and adds an auth hint based on
// the authentication method used for this session.
func (c *CLI) ClassifyError(err error) *output.Error {
	oErr := api.ClassifyError(err)
	if oErr.Code == output.ExitAuth && oErr.Hint == "" {
		switch c.authMethod {
		case "desktop":
			oErr.Hint = "Run 'slack auth login --desktop' to re-authenticate"
		case "oauth":
			oErr.Hint = "Run 'slack auth login' to re-authenticate"
		default:
			oErr.Hint = "Run 'slack auth login' or set SLACK_TOKEN"
		}
	}
	return oErr
}

// NewResolver creates a channel/user name resolver from the API client.
func (c *CLI) NewResolver(client *api.Client) *resolve.Resolver {
	return resolve.NewResolver(client)
}
