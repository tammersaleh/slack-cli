package cmd

import (
	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type ThreadCmd struct {
	List ThreadListCmd `cmd:"" aliases:"read" help:"List thread replies."`
}

type ThreadListCmd struct {
	Args   []string `arg:"" optional:"" name:"args" help:"Channel + parent timestamp, or a message URL."`
	Limit  int      `help:"Page size." default:"50"`
	Cursor string   `help:"Continue from previous page."`
	All    bool     `help:"Fetch all pages."`
}

func (ThreadListCmd) Help() string {
	return `List replies in a thread. Two forms:

  slack thread list <channel> <parent-ts>   # channel + parent timestamp
  slack thread list <message-url>             # a Slack message permalink

A link to any reply works too: its thread_ts resolves to the parent, so
you get the whole thread.`
}

func (c *ThreadListCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	ref, err := parseThreadRef(c.Args)
	if err != nil {
		return &output.Error{Err: "invalid_input", Detail: err.Error(), Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx, cancel := cli.Context()
	defer cancel()

	channelID, err := r.ResolveChannel(ctx, ref.channel)
	if err != nil {
		return channelResolveError(ref.channel, err)
	}

	limit := c.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// The not-found checks describe the thread as a whole, so they run
	// against the first page only - a later page is a continuation, not a
	// verdict on whether the thread exists. Running them here, before any
	// message is printed, keeps a not-found from cutting a stream in half.
	firstPage := true

	fetch := func(cursor string) ([]slack.Message, string, error) {
		msgs, hasMore, nextCursor, err := client.Bot().GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: ref.ts,
			Limit:     limit,
			Cursor:    cursor,
		})
		if err != nil {
			return nil, "", err
		}

		if firstPage {
			firstPage = false
			if len(msgs) == 0 {
				return nil, "", output.ThreadNotFoundNoMessage(ref.channel, ref.ts)
			}
			// Slack returns the parent as the sole message when there are no replies.
			if len(msgs) == 1 && msgs[0].ReplyCount == 0 {
				return nil, "", output.ThreadNotFoundNoReplies(ref.ts)
			}
		}

		if !hasMore {
			nextCursor = ""
		}
		return msgs, nextCursor, nil
	}

	return streamPages(ctx, cli, p, "conversations.replies", c.Cursor, c.All, fetch, func(msgs []slack.Message) error {
		for _, msg := range msgs {
			m := messageToMap(msg)
			m["channel_id"] = channelID
			if err := p.PrintItem(m); err != nil {
				return err
			}
		}
		return nil
	})
}
