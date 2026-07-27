package cmd

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
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
	HasUnread        bool   `help:"Only conversations with unread messages. Reads unread state from the internal client.counts endpoint, so it needs a session token."`
	Query            string `help:"Filter by name substring (client-side). Searches every page by default; with --include-non-member or --cursor it searches only the page fetched. The trailer's filter_exhaustive reports which happened."`
}

func (ChannelListCmd) Help() string {
	return `List channels you're a member of by default. Returns all channel types
(public, private, mpim, im); narrow with --type. Add --include-non-member
to expand to channels you haven't joined.

--include-non-member is much slower: it lists every channel in the
workspace, which on a large Enterprise Grid org is thousands of channels
across hundreds of rate-limited requests. The default reads only your own
conversations.

Channel types:
  public    regular #channels everyone can see
  private   invitation-only channels
  mpim      multi-party DM (group DM with 3+ people)
  im        1:1 DM (always included regardless of --include-non-member;
            IMs don't have a member concept on the Slack API)
  all       all of the above (default)

--query is a client-side name filter. On the default path it searches
every page, so a match is never missed. Under --include-non-member or
--cursor it searches only the page fetched. Either way the _meta trailer's
filter_exhaustive says whether the search covered everything - treat zero
matches with filter_exhaustive:false as "unknown", not "absent".

Examples:

  slack channel list --query ext-                  # searches every page you're in
  slack channel list --type public                 # only public channels
  slack channel list --include-non-member --limit 200`
}

func (c *ChannelListCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	// One context for the whole command: cli.Context() derives a fresh deadline
	// from context.Background() on every call, so calling it twice would give
	// --has-unread twice the timeout the caller asked for.
	ctx, cancel := cli.Context()
	defer cancel()

	// Unread state comes from an internal endpoint that only accepts a session
	// token, so fetch it first: --has-unread must fail before any listing
	// rather than stream rows it cannot filter.
	var unread map[string]unreadState
	if c.HasUnread {
		sessionClient, err := cli.NewSessionClient()
		if err != nil {
			return err
		}
		// NewClient below overwrites the auth method this captured, and the
		// re-auth hint on a counts failure has to describe the session token.
		sessionAuthMethod := cli.authMethod
		unread, err = fetchUnreadState(ctx, sessionClient)
		if err != nil {
			cli.authMethod = sessionAuthMethod
			return cli.ClassifyError(err)
		}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()

	types := channelTypes(c.Type)
	cursor := c.Cursor
	limit := c.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	src := c.source(ctx, client, types, limit)

	// --query and --has-unread both filter client-side, so a single page turns
	// "exists on a later page" into "does not exist" - and the
	// channel_not_found hint points callers straight at --query. Both search
	// every page by default now that the member-only path costs ~19 requests
	// and a few seconds.
	//
	// Two paths can't be widened silently: --include-non-member walks the whole
	// workspace (minutes), and --cursor is a resume point that --all is not
	// allowed to combine with. Those report the truth in the trailer instead.
	all := c.All
	var opts []streamOption
	if c.Query != "" || c.HasUnread {
		if !c.IncludeNonMember && cursor == "" {
			all = true
		}
		opts = append(opts, withClientSideFilter())
	}

	return streamPages(ctx, cli, p, src.endpoint, cursor, all, src.fetch, func(channels []slack.Channel) error {
		for _, ch := range channels {
			// A conversation absent from client.counts has no unread badge:
			// Slack only reports DMs the user has open, and a closed DM is not
			// unread. Absent means excluded, same as has_unreads=false.
			state, counted := unread[ch.ID]
			if c.HasUnread && !(counted && state.HasUnreads) {
				continue
			}
			if c.Query != "" && !strings.Contains(strings.ToLower(ch.Name), strings.ToLower(c.Query)) {
				continue
			}
			m := channelToMap(ch)
			src.normalize(ch, m)
			if counted {
				state.addTo(m)
			}
			if err := p.PrintItem(m); err != nil {
				return err
			}
		}
		return nil
	}, opts...)
}

// unreadState is one conversation's unread state as client.counts reports it.
type unreadState struct {
	HasUnreads   bool   `json:"has_unreads"`
	MentionCount int    `json:"mention_count"`
	LastRead     string `json:"last_read"`
}

// addTo copies the unread fields onto an output row, so a caller filtering on
// unread state can see the state it filtered on rather than infer it.
func (u unreadState) addTo(m map[string]any) {
	m["has_unreads"] = u.HasUnreads
	m["mention_count"] = u.MentionCount
	if u.LastRead != "" {
		m["last_read"] = u.LastRead
	}
}

// clientCountsResponse is the parsed client.counts response. Slack splits
// conversations across buckets by kind; all three carry the same row shape.
type clientCountsResponse struct {
	Channels []struct {
		ID string `json:"id"`
		unreadState
	} `json:"channels"`
	MPIMs []struct {
		ID string `json:"id"`
		unreadState
	} `json:"mpims"`
	IMs []struct {
		ID string `json:"id"`
		unreadState
	} `json:"ims"`
}

// fetchUnreadState reads every conversation's unread state in one request.
//
// No list endpoint returns unread information - `conversations.list` and
// `users.conversations` both omit `unread_count` entirely (raw-JSON checked),
// which is why the old `--has-unread` filtered on a field that was always the
// zero value and silently matched nothing. The undocumented internal
// `client.counts` endpoint is what Slack's own clients read for unread badges.
//
// Measured against an Enterprise Grid org: every public and private channel the
// user is in appears in `channels` (1168 of 1168), and `has_unreads` agreed with
// `latest > last_read` on all 1194 rows checked. DMs appear only when open - 22
// of 271 group DMs and 64 of 350 direct DMs - so an absent DM means no unread
// badge rather than unknown, which matches what the Slack sidebar shows.
//
// The endpoint needs a session token, and on Enterprise Grid the org (E-prefix)
// context: the workspace token returns team_is_restricted. NewSessionClient
// already prefers SLACK_WORKSPACE_ORG for exactly this reason.
func fetchUnreadState(ctx context.Context, client *api.Client) (map[string]unreadState, error) {
	data, err := client.PostInternal(ctx, "client.counts", map[string]any{
		"thread_counts_by_channel": true,
		"org_wide_aware":           true,
		"include_file_channels":    true,
	})
	if err != nil {
		return nil, err
	}

	var resp clientCountsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, &output.Error{
			Err:      "parse_error",
			Detail:   "Failed to parse client.counts response",
			Endpoint: "client.counts",
			Code:     output.ExitGeneral,
		}
	}

	state := make(map[string]unreadState, len(resp.Channels)+len(resp.MPIMs)+len(resp.IMs))
	for _, row := range resp.Channels {
		state[row.ID] = row.unreadState
	}
	for _, row := range resp.MPIMs {
		state[row.ID] = row.unreadState
	}
	for _, row := range resp.IMs {
		state[row.ID] = row.unreadState
	}
	return state, nil
}

// channelSource is where a listing is read from. It binds the endpoint name
// (which feeds --trace and the endpoint field on rate-limit errors) to the
// fetch that produces the rows and to the normalization those particular rows
// need, so the three can't drift apart.
type channelSource struct {
	endpoint string
	fetch    api.PageFunc[slack.Channel]
	// normalize adjusts one output row for facts about its source. It is pure:
	// the emit callback that calls it may not make network calls or fail.
	normalize func(ch slack.Channel, m map[string]any)
}

// source picks which endpoint the listing is read from, and that choice is the
// whole of the member-only filter: users.conversations returns just the
// conversations the authed user is in, while conversations.list returns every
// channel in the workspace and is only what --include-non-member asks for.
//
// The default used to read conversations.list and drop non-member channels in
// the emit callback, which meant paginating every channel in the workspace to
// print the user's own. Measured on a large Enterprise Grid org: the whole
// workspace took 178 Tier-2 requests and 8 minutes to yield the roughly third
// of it the user was in, which this endpoint returns in 10 Tier-3 requests and
// 3 seconds.
//
// No client-side membership check remains on either path. users.conversations
// is already member-scoped, and it omits is_member from every row it returns -
// including public channels the user is plainly in - so a filter on that field
// would drop the entire result set.
func (c *ChannelListCmd) source(ctx context.Context, client *api.Client, types []string, limit int) channelSource {
	if c.IncludeNonMember {
		return channelSource{
			endpoint: "conversations.list",
			fetch: func(cursor string) ([]slack.Channel, string, error) {
				return client.Bot().GetConversationsContext(ctx, &slack.GetConversationsParameters{
					Types:           types,
					Limit:           limit,
					ExcludeArchived: c.ExcludeArchived,
					Cursor:          cursor,
				})
			},
			// Slack sends membership and member counts on this endpoint;
			// report them as they arrive.
			normalize: func(slack.Channel, map[string]any) {},
		}
	}
	return channelSource{
		endpoint: "users.conversations",
		fetch: func(cursor string) ([]slack.Channel, string, error) {
			// An empty UserID means the authed user.
			return client.Bot().GetConversationsForUserContext(ctx, &slack.GetConversationsForUserParameters{
				Types:           types,
				Limit:           limit,
				ExcludeArchived: c.ExcludeArchived,
				Cursor:          cursor,
			})
		},
		normalize: normalizeUsersConversations,
	}
}

// normalizeUsersConversations repairs the two fields users.conversations leaves
// out of every row, which slack-go would otherwise decode to a zero value and
// the printer would emit as fact.
//
// is_member: absent on the wire for every type here, so it marshals as false
// even for channels the user is plainly in. Membership is the property that put
// a conversation in this result set, so report it as true rather than emit a
// false negative. That covers group DMs too - being in one is a real membership,
// and conversations.list is documented to report it as such.
//
// 1:1 DMs are the exception and keep false. Slack sends no is_member for an im
// on either endpoint, so there is no membership value to report and false is
// what this command has always emitted for them.
//
// num_members: absent on the wire, and unlike membership there is nothing to
// infer it from. Drop the key rather than claim every channel has zero members.
// `channel info` still reports it.
func normalizeUsersConversations(ch slack.Channel, m map[string]any) {
	if !ch.IsIM {
		m["is_member"] = true
	}
	delete(m, "num_members")
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
	fetch := func(cursor string) ([]string, string, error) {
		return client.Bot().GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{
			ChannelID: channelID,
			Limit:     limit,
			Cursor:    cursor,
		})
	}

	return streamPages(ctx, cli, p, "conversations.members", c.Cursor, c.All, fetch, func(members []string) error {
		for _, uid := range members {
			if err := p.PrintItem(map[string]any{"user_id": uid}); err != nil {
				return err
			}
		}
		return nil
	})
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

  slack channel managers C01ABC
  slack channel managers #approvals`
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
