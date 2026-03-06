package cmd

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type MessageCmd struct {
	List MessageListCmd `cmd:"" aliases:"read" help:"List messages in a channel."`
	Get  MessageGetCmd  `cmd:"" help:"Get specific messages by timestamp."`
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
	ctx := context.Background()

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return &output.Error{Err: "channel_not_found", Detail: "No channel matching '" + c.Channel + "'", Code: output.ExitGeneral}
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
			return api.ClassifyError(err)
		}
		oldest = ch.LastRead
	} else if c.After != "" {
		ts, err := parseTimestamp(c.After)
		if err != nil {
			return &output.Error{Err: "invalid_timestamp", Detail: "Cannot parse --after: " + c.After, Code: output.ExitGeneral}
		}
		oldest = ts
	}

	latest := ""
	if c.Before != "" {
		ts, err := parseTimestamp(c.Before)
		if err != nil {
			return &output.Error{Err: "invalid_timestamp", Detail: "Cannot parse --before: " + c.Before, Code: output.ExitGeneral}
		}
		latest = ts
	}

	cursor := c.Cursor

	for {
		resp, err := client.Bot().GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: channelID,
			Limit:     limit,
			Cursor:    cursor,
			Oldest:    oldest,
			Latest:    latest,
		})
		if err != nil {
			return api.ClassifyError(err)
		}

		for _, msg := range resp.Messages {
			if c.HasReplies && msg.ReplyCount == 0 {
				continue
			}
			if c.HasReactions && len(msg.Reactions) == 0 {
				continue
			}
			if err := p.PrintItem(messageToMap(msg)); err != nil {
				return err
			}
		}

		nextCursor := resp.ResponseMetaData.NextCursor
		if !c.All || nextCursor == "" {
			return p.PrintMeta(output.Meta{
				HasMore:    resp.HasMore,
				NextCursor: nextCursor,
			})
		}
		cursor = nextCursor
		// Clear oldest/latest for subsequent pages - cursor handles position.
		oldest = ""
		latest = ""
	}
}

type MessageGetCmd struct {
	Channel    string   `arg:"" required:"" help:"Channel ID or name."`
	Timestamps []string `arg:"" required:"" help:"Message timestamps."`
}

func (c *MessageGetCmd) Run(cli *CLI) error {
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

	errorCount := 0
	for _, ts := range c.Timestamps {
		resp, err := client.Bot().GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: channelID,
			Oldest:    ts,
			Latest:    ts,
			Inclusive: true,
			Limit:     1,
		})
		if err != nil {
			return api.ClassifyError(err)
		}

		if len(resp.Messages) == 0 {
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":  ts,
				"error":  "message_not_found",
				"detail": "No message at timestamp " + ts + " in " + c.Channel,
			}); err != nil {
				return err
			}
			continue
		}

		m := messageToMap(resp.Messages[0])
		m["input"] = ts
		if err := p.PrintItem(m); err != nil {
			return err
		}
	}

	meta := output.Meta{ErrorCount: errorCount}
	if err := p.PrintMeta(meta); err != nil {
		return err
	}
	if errorCount > 0 {
		return &output.Error{Err: "message_not_found", Code: output.ExitGeneral}
	}
	return nil
}

func messageToMap(msg slack.Message) map[string]any {
	data, _ := json.Marshal(msg)
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
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

	return "", &output.Error{Err: "invalid_timestamp", Detail: "Cannot parse timestamp: " + s, Code: output.ExitGeneral}
}
