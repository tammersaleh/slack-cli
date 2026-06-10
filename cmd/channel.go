package cmd

import (
	"encoding/json"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type ChannelCmd struct {
	List     ChannelListCmd     `cmd:"" help:"List channels."`
	Info     ChannelInfoCmd     `cmd:"" help:"Show channel details."`
	Members  ChannelMembersCmd  `cmd:"" help:"List channel members."`
	Managers ChannelManagersCmd `cmd:"" help:"List a channel's Channel Managers."`
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
			if err := p.PrintItem(channelResolveError(input, err).AsItem()); err != nil {
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
		return channelResolveError(c.Channel, err)
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

type ChannelManagersCmd struct {
	Channel string `arg:"" required:"" help:"Channel ID or name."`
}

func (ChannelManagersCmd) Help() string {
	return `List a channel's Channel Managers - the users under "Managed by" in the
channel's About tab. Emits one JSONL row per assigned user, carrying the
verbatim role_id (Rl0A is the Channel Manager role). A channel with no
managers emits zero rows; that is success, not an error.

Requires a session token (xoxc-); uses an undocumented internal API. On
Enterprise Grid the org token (SLACK_WORKSPACE_ORG) backs the internal call,
while channel-name resolution uses the workspace token.

Examples:

  slack channel managers C0A065FTV4H
  slack channel managers #sa-approvals`
}

// roleEntityAssignmentsResponse is the parsed admin.roles.entity.listAssignments
// response. Each assignment scopes a role to the channel entity; for a channel
// the assignments are its "Managed by" entries.
type roleEntityAssignmentsResponse struct {
	RoleAssignments []struct {
		RoleID string   `json:"role_id"`
		Users  []string `json:"users"`
	} `json:"role_assignments"`
}

func (c *ChannelManagersCmd) Run(cli *CLI) error {
	// The internal endpoint needs a session (xoxc-) token. Build that client
	// first so a non-session token fails fast, before any channel resolution.
	sessionClient, err := cli.NewSessionClient()
	if err != nil {
		return err
	}
	// NewClient below overwrites the auth method captured by NewSessionClient.
	// The only auth-classified call here is the internal endpoint on the
	// session client, so its re-auth hint must reflect the session token.
	sessionAuthMethod := cli.authMethod

	// Channel-name resolution and output enrichment use the workspace
	// (T-prefix) token: conversations.list is restricted on the org context
	// that the session client targets on Enterprise Grid.
	client, err := cli.NewClient()
	if err != nil {
		return err
	}
	cli.authMethod = sessionAuthMethod
	r := cli.NewResolver(client)
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return channelResolveError(c.Channel, err)
	}

	data, err := sessionClient.PostInternalForm(ctx, "admin.roles.entity.listAssignments", map[string]string{
		"entity_id": channelID,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	var resp roleEntityAssignmentsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return &output.Error{Err: "parse_error", Detail: "Failed to parse admin.roles.entity.listAssignments response", Code: output.ExitGeneral}
	}

	// Emit every assignment's users with the verbatim role_id. We don't filter
	// to a known channel-manager role ID: if Slack's ID ever differs from the
	// observed Rl0A, a hard filter would silently report no managers.
	for _, a := range resp.RoleAssignments {
		for _, uid := range a.Users {
			if err := p.PrintItem(map[string]any{
				"user_id": uid,
				"role_id": a.RoleID,
			}); err != nil {
				return err
			}
		}
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
