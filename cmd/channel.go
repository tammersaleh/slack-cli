package cmd

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type ChannelCmd struct {
	List    ChannelListCmd    `cmd:"" help:"List channels."`
	Info    ChannelInfoCmd    `cmd:"" help:"Show channel details."`
	Members ChannelMembersCmd `cmd:"" help:"List channel members."`
	Create  ChannelCreateCmd  `cmd:"" help:"Create a channel (gated by Touch ID)."`
}

type ChannelListCmd struct {
	Limit            int    `help:"Page size." default:"100"`
	Cursor           string `help:"Continue from previous page."`
	All              bool   `help:"Fetch all pages."`
	Type             string `help:"Channel type: public, private, mpim, im, all." default:"all" enum:"public,private,mpim,im,all"`
	ExcludeArchived  bool   `help:"Exclude archived channels." default:"true" negatable:""`
	IncludeNonMember bool   `help:"Include channels the user hasn't joined."`
	HasUnread        bool   `help:"Only channels with unread messages."`
	Query            string `help:"Filter by name substring (client-side)."`
}

func (ChannelListCmd) Help() string {
	return `List channels you're a member of by default. Returns all channel types
(public, private, mpim, im); narrow with --type. Add --include-non-member
to expand to channels you haven't joined.

Channel types:
  public    regular #channels everyone can see
  private   invitation-only channels
  mpim      multi-party DM (group DM with 3+ people)
  im        1:1 DM (always included regardless of --include-non-member;
            IMs don't have a member concept on the Slack API)
  all       all of the above (default)

Examples:

  slack channel list --query ext-                  # filter by name substring
  slack channel list --type public                 # only public channels
  slack channel list --include-non-member --limit 200
  slack channel list --type private --has-unread   # only unread private channels`
}

func (c *ChannelListCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	types := channelTypes(c.Type)
	cursor := c.Cursor
	limit := c.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	for {
		channels, nextCursor, err := client.Bot().GetConversationsContext(ctx, &slack.GetConversationsParameters{
			Types:           types,
			Limit:           limit,
			ExcludeArchived: c.ExcludeArchived,
			Cursor:          cursor,
		})
		if err != nil {
			return cli.ClassifyError(err)
		}

		for _, ch := range channels {
			// IMs don't have a "member" concept - is_member is always
			// false, so the member-only default would hide every DM.
			// Include them unconditionally.
			if !ch.IsIM && !c.IncludeNonMember && !ch.IsMember {
				continue
			}
			if c.HasUnread && ch.UnreadCount == 0 {
				continue
			}
			if c.Query != "" && !strings.Contains(strings.ToLower(ch.Name), strings.ToLower(c.Query)) {
				continue
			}
			if err := p.PrintItem(channelToMap(ch)); err != nil {
				return err
			}
		}

		if !c.All || nextCursor == "" {
			return p.PrintMeta(output.Meta{
				HasMore:    nextCursor != "",
				NextCursor: nextCursor,
			})
		}
		cursor = nextCursor
	}
}

type ChannelInfoCmd struct {
	Channels []string `arg:"" required:"" help:"Channel ID or name."`
}

func (c *ChannelInfoCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx, cancel := cli.Context()
	defer cancel()
	errorCount := 0

	for _, input := range c.Channels {
		channelID, err := r.ResolveChannel(ctx, input)
		if err != nil {
			errorCount++
			if err := p.PrintItem(output.ChannelNotFound(input).AsItem()); err != nil {
				return err
			}
			continue
		}

		ch, err := client.Bot().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
			ChannelID:         channelID,
			IncludeNumMembers: true,
		})
		if err != nil {
			oErr := cli.ClassifyError(err)
			if oErr.Code != output.ExitGeneral {
				return oErr
			}
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":  input,
				"error":  oErr.Err,
				"detail": oErr.Detail,
			}); err != nil {
				return err
			}
			continue
		}

		m := channelToMap(*ch)
		m["input"] = input
		if err := p.PrintItem(m); err != nil {
			return err
		}
	}

	meta := output.Meta{ErrorCount: errorCount}
	if err := p.PrintMeta(meta); err != nil {
		return err
	}
	if errorCount > 0 {
		return &output.ExitError{Code: output.ExitGeneral}
	}
	return nil
}

type ChannelMembersCmd struct {
	Channel string `arg:"" required:"" help:"Channel ID or name."`
	Limit   int    `help:"Page size." default:"100"`
	Cursor  string `help:"Continue from previous page."`
	All     bool   `help:"Fetch all pages."`
}

func (c *ChannelMembersCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx, cancel := cli.Context()
	defer cancel()

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return output.ChannelNotFound(c.Channel)
	}

	limit := c.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	cursor := c.Cursor

	for {
		members, nextCursor, err := client.Bot().GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{
			ChannelID: channelID,
			Limit:     limit,
			Cursor:    cursor,
		})
		if err != nil {
			return cli.ClassifyError(err)
		}

		for _, uid := range members {
			if err := p.PrintItem(map[string]any{"user_id": uid}); err != nil {
				return err
			}
		}

		if !c.All || nextCursor == "" {
			return p.PrintMeta(output.Meta{
				HasMore:    nextCursor != "",
				NextCursor: nextCursor,
			})
		}
		cursor = nextCursor
	}
}

type ChannelCreateCmd struct {
	Name    string `arg:"" required:"" help:"Channel name (without #)."`
	Private bool   `help:"Create a private channel." default:"false"`
}

func (ChannelCreateCmd) Help() string {
	return `Create a channel. Gated by macOS Touch ID confirmation.

Slack's channel-naming rules apply: lowercase, no spaces, no '#' prefix
(a leading '#' is stripped automatically). Length and special-character
restrictions are enforced by Slack, not the CLI.

The confirmation prompt shows the resolved workspace name, channel
visibility, and channel name. There is no '--no-confirm' flag and no
environment variable bypass: the gate exists to keep an agent from
creating channels without an explicit human touch.

Examples:

  slack channel create newchan
  slack channel create newchan --private`
}

func (c *ChannelCreateCmd) Run(cli *CLI) error {
	name := strings.TrimPrefix(c.Name, "#")
	if name == "" {
		return &output.Error{
			Err:    "invalid_name",
			Detail: "Channel name is required",
			Code:   output.ExitGeneral,
		}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	visibility := "public"
	if c.Private {
		visibility = "private"
	}
	workspace := cli.WorkspaceName()
	if workspace == "" {
		// Refuse to build a prompt the user can't verify. The
		// biometric prompt is the human's only defense against a
		// rogue caller; an opaque "this workspace" or a TeamID
		// defeats it. Concrete cause: stored credentials lack
		// team_name, or auth came via SLACK_TOKEN env var which
		// bypasses the credentials file. Re-run `slack auth login`
		// (or login --desktop) to populate team_name.
		return &output.Error{
			Err:    "workspace_name_unresolved",
			Detail: "Cannot build biometric prompt without a workspace name",
			Hint:   "Re-run 'slack auth login' so the credentials file records team_name. SLACK_TOKEN env var auth does not carry a workspace name.",
			Code:   output.ExitGeneral,
		}
	}
	reason := fmt.Sprintf("Create %s channel #%s in %s", visibility, name, workspace)

	ctx, cancel := cli.Context()
	defer cancel()

	if err := cli.Confirm(ctx, reason); err != nil {
		return err
	}

	ch, err := client.Bot().CreateConversationContext(ctx, slack.CreateConversationParams{
		ChannelName: name,
		IsPrivate:   c.Private,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	p := cli.NewPrinter()
	if err := p.PrintItem(channelToMap(*ch)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

func channelTypes(t string) []string {
	switch t {
	case "private":
		return []string{"private_channel"}
	case "mpim":
		return []string{"mpim"}
	case "im":
		return []string{"im"}
	case "all":
		return []string{"public_channel", "private_channel", "mpim", "im"}
	default:
		return []string{"public_channel"}
	}
}

func channelToMap(ch slack.Channel) map[string]any { return toMap(ch) }
