package cmd

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type ChannelCmd struct {
	List    ChannelListCmd    `cmd:"" help:"List channels."`
	Info    ChannelInfoCmd    `cmd:"" help:"Show channel details."`
	Members ChannelMembersCmd `cmd:"" help:"List channel members."`
}

type ChannelListCmd struct {
	Limit            int    `help:"Page size." default:"100"`
	Cursor           string `help:"Continue from previous page."`
	All              bool   `help:"Fetch all pages."`
	Type             string `help:"Channel type: public, private, mpim, im, all." default:"public" enum:"public,private,mpim,im,all"`
	ExcludeArchived  bool   `help:"Exclude archived channels." default:"true" negatable:""`
	IncludeNonMember bool   `help:"Include channels the user hasn't joined."`
	HasUnread        bool   `help:"Only channels with unread messages."`
	Query            string `help:"Filter by name substring (client-side)."`
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
	ctx := context.Background()

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
			if !c.IncludeNonMember && !ch.IsMember {
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
	ctx := context.Background()
	errorCount := 0

	for _, input := range c.Channels {
		channelID, err := r.ResolveChannel(ctx, input)
		if err != nil {
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":  input,
				"error":  "channel_not_found",
				"detail": "No channel matching '" + input + "'",
			}); err != nil {
				return err
			}
			continue
		}

		ch, err := client.Bot().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
			ChannelID:         channelID,
			IncludeNumMembers: true,
		})
		if err != nil {
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":  input,
				"error":  "channel_not_found",
				"detail": "No channel matching '" + input + "'",
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
		return &output.Error{Err: "channel_not_found", Code: output.ExitGeneral}
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
	ctx := context.Background()

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return &output.Error{Err: "channel_not_found", Detail: "No channel matching '" + c.Channel + "'", Code: output.ExitGeneral}
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
			if err := p.PrintItem(map[string]string{"id": uid}); err != nil {
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

func channelToMap(ch slack.Channel) map[string]any {
	// Marshal via JSON to get a flat map with correct field names.
	data, _ := json.Marshal(ch)
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}
