package cmd

import (
	"context"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type MessagePermalinkCmd struct {
	Channel    string   `arg:"" required:"" help:"Channel ID or name."`
	Timestamps []string `arg:"" required:"" help:"Message timestamps."`
}

func (c *MessagePermalinkCmd) Run(cli *CLI) error {
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
		permalink, err := client.Bot().GetPermalinkContext(ctx, &slack.PermalinkParameters{
			Channel: channelID,
			Ts:      ts,
		})
		if err != nil {
			oErr := cli.ClassifyError(err)
			if oErr.Code != output.ExitGeneral {
				return oErr
			}
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

		if err := p.PrintItem(map[string]any{
			"input":     ts,
			"channel":   channelID,
			"ts":        ts,
			"permalink": permalink,
		}); err != nil {
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
