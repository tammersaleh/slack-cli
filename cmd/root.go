package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/auth"
	"github.com/tammersaleh/slack-cli/internal/output"
	"github.com/tammersaleh/slack-cli/internal/resolve"
)

type CLI struct {
	Workspace    string        `short:"w" help:"Select workspace (name or ID)." env:"SLACK_WORKSPACE"`
	WorkspaceOrg string        `hidden:"" env:"SLACK_WORKSPACE_ORG" help:"Enterprise Grid org workspace for internal APIs."`
	Fields       string        `help:"Comma-separated list of top-level fields to include." env:"SLACK_FIELDS"`
	Quiet        bool          `short:"q" help:"Suppress stdout output (exit code and stderr only)."`
	Timeout      time.Duration `help:"Overall command timeout (e.g. 30s, 2m). Zero means no timeout." env:"SLACK_TIMEOUT"`
	Trace        bool          `help:"Emit structured diagnostics (endpoint, latency, retries) to stderr as JSONL."`
	APIBaseURL   string        `hidden:"" env:"SLACK_API_URL" help:"Override Slack API base URL (for testing)."`

	// stdout/stderr/stdin overrides for testing.
	out io.Writer
	err io.Writer
	in  io.Reader

	// Set by NewClient from resolved credentials.
	authMethod string
	teamID     string
	resolver   *resolve.Resolver

	// ctx is the current command context, captured by Context() so the
	// printer's enrichment closure can honor --timeout / --trace on the
	// users.info fallback path. Nil before Context() is called.
	ctx context.Context

	Auth      AuthCmd      `cmd:"" help:"Manage authentication."`
	Bookmark  BookmarkCmd  `cmd:"" aliases:"bookmarks" help:"Channel bookmarks."`
	Cache     CacheCmd     `cmd:"" help:"Cache management."`
	Channel   ChannelCmd   `cmd:"" aliases:"channels" help:"Read channel information."`
	Dnd       DndCmd       `cmd:"" help:"Do Not Disturb info."`
	Draft     DraftCmd     `cmd:"" aliases:"drafts" help:"Stage draft messages (requires session token)."`
	Emoji     EmojiCmd     `cmd:"" aliases:"emojis" help:"Custom emoji."`
	File      FileCmd      `cmd:"" aliases:"files" help:"File operations."`
	Message   MessageCmd   `cmd:"" aliases:"messages" help:"Read messages."`
	Pin       PinCmd       `cmd:"" aliases:"pins" help:"Pinned items."`
	Presence  PresenceCmd  `cmd:"" help:"User presence."`
	Reaction  ReactionCmd  `cmd:"" aliases:"reactions" help:"Read reactions."`
	Saved     SavedCmd     `cmd:"" help:"Saved-for-later items (requires session token)."`
	Search    SearchCmd    `cmd:"" help:"Search messages and files."`
	Skill     SkillCmd     `cmd:"" help:"Print Claude skill file to stdout."`
	Section   SectionCmd   `cmd:"" aliases:"sections" help:"Manage sidebar sections (requires session token)."`
	Status    StatusCmd    `cmd:"" help:"User status."`
	Thread    ThreadCmd    `cmd:"" aliases:"threads" help:"Read thread replies."`
	User      UserCmd      `cmd:"" aliases:"users" help:"Read user information."`
	Usergroup UsergroupCmd `cmd:"" aliases:"usergroups" help:"User groups."`
	Version   VersionCmd   `cmd:"" help:"Show version."`
	WorkspaceInfo WorkspaceCmd `cmd:"" help:"Workspace info."`
}

// Context returns a fresh context honoring --timeout and --trace. Caller must
// call cancel - use defer - to release resources even if no deadline fires.
// When --trace is set the context carries a JSON-lines tracer that emits
// events to the CLI's configured stderr writer. The returned context is
// also stashed on the CLI so printer enrichment can reuse it for any
// users.info fallback triggered during PrintItem.
func (c *CLI) Context() (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if c.Trace {
		w := c.err
		if w == nil {
			w = os.Stderr
		}
		ctx = api.WithTracer(ctx, api.NewJSONLinesTracer(w))
	}
	if c.Timeout > 0 {
		ctx, cancel := context.WithTimeout(ctx, c.Timeout)
		c.ctx = ctx
		return ctx, cancel
	}
	c.ctx = ctx
	return ctx, func() {}
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

// SetInput overrides stdin for testing.
func (c *CLI) SetInput(in io.Reader) {
	c.in = in
}

// Stdin returns the stdin reader, defaulting to os.Stdin.
func (c *CLI) Stdin() io.Reader {
	if c.in != nil {
		return c.in
	}
	return os.Stdin
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
		EnrichFunc: func(m map[string]any) {
			if c.resolver == nil {
				return
			}
			// Use the command's context so enrichment API calls
			// (users.info fallback in LookupUser) honor --timeout
			// and --trace. Fall back to Background when NewPrinter
			// is used before Context() - currently only auth login.
			ctx := c.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			c.resolver.Enrich(ctx, m)
		},
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
	c.teamID = rc.TeamID

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
// The resolver is also stored on CLI for use by NewPrinter's enrichment.
func (c *CLI) NewResolver(client *api.Client) *resolve.Resolver {
	cacheDir := ""
	if dir, err := os.UserConfigDir(); err == nil {
		cacheDir = filepath.Join(dir, "slack-cli", "cache")
	}
	r := resolve.NewResolver(client, c.teamID, cacheDir)
	c.resolver = r
	return r
}

// toMap converts any struct to a map[string]any via JSON round-trip.
func toMap(v any) map[string]any {
	data, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

// NewSessionClient creates an API client for commands that use internal APIs.
// Uses WorkspaceOrg if set (for Enterprise Grid), otherwise falls back to Workspace.
func (c *CLI) NewSessionClient() (*api.Client, error) {
	workspace := c.Workspace
	if c.WorkspaceOrg != "" {
		workspace = c.WorkspaceOrg
	}

	path, err := auth.DefaultCredentialsPath()
	if err != nil {
		return nil, err
	}

	rc, err := auth.ResolveCredentials(path, workspace)
	if err != nil {
		hint := "Run 'slack auth login --desktop' or set SLACK_WORKSPACE_ORG"
		return nil, &output.Error{
			Err:    "not_authed",
			Detail: err.Error(),
			Hint:   hint,
			Code:   output.ExitAuth,
		}
	}

	c.authMethod = rc.AuthMethod
	c.teamID = rc.TeamID

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

	client := api.New(rc.BotToken, opts...)
	if err := requireSessionToken(client); err != nil {
		return nil, err
	}
	return client, nil
}

// requireSessionToken returns an error if the client's token is not an xoxc- session token.
func requireSessionToken(client *api.Client) error {
	if !strings.HasPrefix(client.Token(), "xoxc-") {
		return &output.Error{
			Err:    "session_token_required",
			Detail: "This command requires a session token (xoxc-)",
			Hint:   "Run 'slack auth login --desktop' to authenticate with a session token",
			Code:   output.ExitAuth,
		}
	}
	return nil
}
