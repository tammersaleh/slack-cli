package cmd

import (
	"context"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type ReactionCmd struct {
	List ReactionListCmd `cmd:"" help:"List reactions on a message."`
}

type ReactionListCmd struct {
	Channel    string   `arg:"" required:"" help:"Channel ID or name."`
	Timestamps []string `arg:"" required:"" help:"Message timestamps."`
}

func (c *ReactionListCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx := context.Background()
	errorCount := 0

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return &output.Error{Err: "channel_not_found", Detail: "No channel matching '" + c.Channel + "'", Code: output.ExitGeneral}
	}

	for _, ts := range c.Timestamps {
		item, err := client.Bot().GetReactionsContext(ctx, slack.ItemRef{
			Channel:   channelID,
			Timestamp: ts,
		}, slack.GetReactionsParameters{Full: true})
		if err != nil {
			oErr := cli.ClassifyError(err)
			// If it's a specific message error, inline it.
			if oErr.Code == output.ExitGeneral {
				errorCount++
				if err := p.PrintItem(map[string]any{
					"input":  ts,
					"error":  oErr.Err,
					"detail": oErr.Detail,
				}); err != nil {
					return err
				}
				continue
			}
			return oErr
		}

		for _, reaction := range item.Reactions {
			if err := p.PrintItem(map[string]any{
				"input": ts,
				"name":  reaction.Name,
				"count": reaction.Count,
				"users": reaction.Users,
			}); err != nil {
				return err
			}
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
