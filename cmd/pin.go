package cmd

import (
	"context"
	"encoding/json"

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
	ctx := context.Background()

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return &output.Error{Err: "channel_not_found", Detail: "No channel matching '" + c.Channel + "'", Code: output.ExitGeneral}
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

func pinItemToMap(item slack.Item) map[string]any {
	data, _ := json.Marshal(item)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}
