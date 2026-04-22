package cmd

import (
	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type PinCmd struct {
	List PinListCmd `cmd:"" help:"List pinned items in a channel."`
}

type PinListCmd struct {
	Channel string `arg:"" required:"" help:"Channel ID or name."`
}

func (c *PinListCmd) Run(cli *CLI) error {
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

	items, _, err := client.Bot().ListPinsContext(ctx, channelID)
	if err != nil {
		return cli.ClassifyError(err)
	}

	for _, item := range items {
		if err := p.PrintItem(pinItemToMap(item)); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}

func pinItemToMap(item slack.Item) map[string]any { return toMap(item) }
