package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
	"github.com/tammersaleh/slack-cli/internal/slackurl"
)

type MessageCmd struct {
	List      MessageListCmd      `cmd:"" aliases:"read" help:"List messages in a channel."`
	Get       MessageGetCmd       `cmd:"" help:"Get specific messages by timestamp."`
	Permalink MessagePermalinkCmd `cmd:"" help:"Get permalinks for messages."`
}

type MessageListCmd struct {
	Channel      string `arg:"" required:"" help:"Channel ID or name."`
	Limit        int    `help:"Page size." default:"20"`
	Cursor       string `help:"Continue from previous page."`
	All          bool   `help:"Fetch all pages."`
	After        string `help:"Messages after this time (Unix timestamp or ISO 8601)."`
	Before       string `help:"Messages before this time (Unix timestamp or ISO 8601)."`
	Unread       bool   `help:"Messages since the last-read position."`
	HasReplies   bool   `help:"Only messages with thread replies (client-side filter)."`
	HasReactions bool   `help:"Only messages with reactions (client-side filter)."`
}

func (MessageListCmd) Help() string {
	return `List messages from a channel, newest first. Channel accepts an ID
(C.../G.../D...) or a #name resolved from the local cache.

Time bounds accept RFC 3339 ('2026-04-20T09:00:00-07:00'), date-only
(YYYY-MM-DD), or a raw Slack ts ('1713300000.123456'). --unread uses
the channel's last_read marker (mutually exclusive with --after).

Examples:

  slack message list '#general' --limit 50
  slack message list '#general' --after 2026-04-01 --before 2026-04-15
  slack message list '#general' --unread
  slack message list '#general' --has-replies   # find threads to explore`
}

func (c *MessageListCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}
	if c.Unread && c.After != "" {
		return &output.Error{Err: "invalid_input", Detail: "--unread and --after are mutually exclusive", Code: output.ExitGeneral}
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
		limit = 20
	}

	oldest := ""
	if c.Unread {
		// Fetch channel info to get last_read.
		ch, err := client.Bot().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channelID})
		if err != nil {
			return cli.ClassifyError(err)
		}
		oldest = ch.LastRead
	} else if c.After != "" {
		ts, err := parseTimestamp(c.After)
		if err != nil {
			return output.InvalidTimestamp("--after", c.After)
		}
		oldest = ts
	}

	latest := ""
	if c.Before != "" {
		ts, err := parseTimestamp(c.Before)
		if err != nil {
			return output.InvalidTimestamp("--before", c.Before)
		}
		latest = ts
	}

	// Time bounds apply to the first page only; after that the cursor
	// carries the position. The flag flips on success, not on request, so
	// a retried first page still sends its bounds.
	boundsSent := false

	fetch := func(cursor string) ([]slack.Message, string, error) {
		oldestArg, latestArg := oldest, latest
		if boundsSent {
			oldestArg, latestArg = "", ""
		}
		resp, err := client.Bot().GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: channelID,
			Limit:     limit,
			Cursor:    cursor,
			Oldest:    oldestArg,
			Latest:    latestArg,
		})
		if err != nil {
			return nil, "", err
		}
		boundsSent = true
		return resp.Messages, resp.ResponseMetaData.NextCursor, nil
	}

	return streamPages(ctx, cli, p, "conversations.history", c.Cursor, c.All, fetch, func(msgs []slack.Message) error {
		for _, msg := range msgs {
			if c.HasReplies && msg.ReplyCount == 0 {
				continue
			}
			if c.HasReactions && len(msg.Reactions) == 0 {
				continue
			}
			m := messageToMap(msg)
			m["channel_id"] = channelID
			if err := p.PrintItem(m); err != nil {
				return err
			}
		}
		return nil
	})
}

type MessageGetCmd struct {
	Args []string `arg:"" optional:"" name:"args" help:"Channel + timestamps, or message URLs."`
}

func (MessageGetCmd) Help() string {
	return `Get one or more messages. Two forms:

  slack message get <channel> <ts>...      # channel + timestamps
  slack message get <message-url>...        # Slack message permalinks

Channel accepts an ID (C.../G.../D...) or a #name. A message permalink
carries its own channel, so several URLs may target different channels in
one call. The two forms can't be mixed.

Examples:

  slack message get '#general' 1709251200.000100
  slack message get https://acme.slack.com/archives/C01ABC/p1709251200000100`
}

func (c *MessageGetCmd) Run(cli *CLI) error {
	refs, err := parseMessageRefs(c.Args)
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

	channelIDs, cerr := resolveRefChannels(ctx, r, refs)
	if cerr != nil {
		return cerr
	}

	errorCount := 0
	for _, ref := range refs {
		channelID := channelIDs[ref.channel]
		resp, err := client.Bot().GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: channelID,
			Oldest:    ref.ts,
			Latest:    ref.ts,
			Inclusive: true,
			Limit:     1,
		})
		if err != nil {
			oErr := cli.ClassifyError(err)
			if oErr.Code != output.ExitGeneral {
				return oErr
			}
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":      ref.input,
				"channel_id": channelID,
				"error":      oErr.Err,
				"detail":     oErr.Detail,
			}); err != nil {
				return err
			}
			continue
		}

		msgs := resp.Messages
		if len(msgs) == 0 {
			// conversations.history omits thread replies; fall back to a
			// permalink+replies lookup before declaring the ts missing.
			reply, ferr := findThreadReply(ctx, client, channelID, ref.ts, ref.threadTS)
			if ferr != nil {
				oErr := cli.ClassifyError(ferr)
				if !isMessageMiss(oErr) {
					return oErr
				}
				// A semantic miss just means "not a reply either" - fall
				// through to the message_not_found row below.
			} else if reply != nil {
				msgs = []slack.Message{*reply}
			}
		}

		if len(msgs) == 0 {
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":      ref.input,
				"channel_id": channelID,
				"error":      "message_not_found",
				"detail":     "No message at timestamp " + ref.ts + " in " + channelID,
			}); err != nil {
				return err
			}
			continue
		}

		m := messageToMap(msgs[0])
		m["input"] = ref.input
		m["channel_id"] = channelID
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

func messageToMap(msg slack.Message) map[string]any { return toMap(msg) }

// findThreadReply resolves a ts that conversations.history can't see - a thread
// reply - by discovering its parent thread and fetching the reply directly.
//
// knownThreadTS short-circuits the parent lookup when the caller already has it
// (a reply permalink carries thread_ts). Otherwise chat.getPermalink is asked
// for the ts; its permalink embeds thread_ts when the ts is a reply.
//
// Returns (nil, nil) when the ts is not a reply (no thread_ts) or the parent
// thread has no such message. A non-nil error is a live API failure or a
// permalink Slack returned that we couldn't parse (drift); the caller
// classifies it.
func findThreadReply(ctx context.Context, client *api.Client, channelID, ts, knownThreadTS string) (*slack.Message, error) {
	threadTS := knownThreadTS
	if threadTS == "" {
		permalink, err := client.Bot().GetPermalinkContext(ctx, &slack.PermalinkParameters{Channel: channelID, Ts: ts})
		if err != nil {
			return nil, err
		}
		ref, _, err := slackurl.Parse(permalink)
		if err != nil {
			return nil, fmt.Errorf("parse permalink %q: %w", permalink, err)
		}
		if ref.ThreadTS == "" {
			return nil, nil // not a threaded reply
		}
		threadTS = ref.ThreadTS
	}

	// conversations.replies takes the parent ts; the oldest/latest window
	// narrows the returned page to just the target. Slack always prepends the
	// thread parent regardless of the window, so scan for the matching ts
	// rather than assuming a single-message response.
	msgs, _, _, err := client.Bot().GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Oldest:    ts,
		Latest:    ts,
		Inclusive: true,
		Limit:     2,
	})
	if err != nil {
		return nil, err
	}
	for i := range msgs {
		if msgs[i].Timestamp == ts {
			return &msgs[i], nil
		}
	}
	return nil, nil
}

// isMessageMiss reports whether a classified error is a semantic "this ts isn't
// there" signal (as opposed to auth/rate/scope/network/drift), so the caller
// can collapse it into a message_not_found row instead of aborting.
func isMessageMiss(oErr *output.Error) bool {
	return oErr.Err == "message_not_found" || oErr.Err == "thread_not_found"
}

// parseTimestamp accepts a Unix timestamp string or ISO 8601 datetime.
func parseTimestamp(s string) (string, error) {
	// If it looks like a Slack timestamp (digits.digits), pass through.
	if strings.Contains(s, ".") {
		parts := strings.SplitN(s, ".", 2)
		if _, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
			return s, nil
		}
	}

	// Pure unix timestamp (no dot).
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return s + ".000000", nil
	}

	// Try ISO 8601 formats.
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return strconv.FormatInt(t.Unix(), 10) + ".000000", nil
		}
	}

	return "", fmt.Errorf("unparseable timestamp: %s", s)
}
