package cmd

import (
	"context"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type ThreadCmd struct {
	List ThreadListCmd `cmd:"" aliases:"read" help:"List thread replies."`
}

type ThreadListCmd struct {
	Channel   string `arg:"" required:"" help:"Channel ID or name."`
	Timestamp string `arg:"" required:"" help:"Parent message timestamp."`
	Limit     int    `help:"Page size." default:"50"`
	Cursor    string `help:"Continue from previous page."`
	All       bool   `help:"Fetch all pages."`
}

func (c *ThreadListCmd) Run(cli *CLI) error {
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
		limit = 50
	}
	cursor := c.Cursor

	for {
		msgs, hasMore, nextCursor, err := client.Bot().GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: c.Timestamp,
			Limit:     limit,
			Cursor:    cursor,
		})
		if err != nil {
			return cli.ClassifyError(err)
		}

		if len(msgs) == 0 {
			return &output.Error{Err: "thread_not_found", Detail: "No message at timestamp " + c.Timestamp, Code: output.ExitGeneral}
		}

		// Slack returns the parent as the sole message when there are no replies.
		if len(msgs) == 1 && msgs[0].ReplyCount == 0 {
			return &output.Error{Err: "thread_not_found", Detail: "Message has no replies", Code: output.ExitGeneral}
		}

		for _, msg := range msgs {
			if err := p.PrintItem(messageToMap(msg)); err != nil {
				return err
			}
		}

		if !c.All || !hasMore || nextCursor == "" {
			return p.PrintMeta(output.Meta{
				HasMore:    hasMore && nextCursor != "",
				NextCursor: nextCursor,
			})
		}
		cursor = nextCursor
	}
}
