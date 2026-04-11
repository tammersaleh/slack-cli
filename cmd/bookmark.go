package cmd

import (
	"context"
	"encoding/json"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type BookmarkCmd struct {
	List BookmarkListCmd `cmd:"" help:"List bookmarks in a channel."`
}

type BookmarkListCmd struct {
	Channel string `arg:"" required:"" help:"Channel ID or name."`
}

func (c *BookmarkListCmd) Run(cli *CLI) error {
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

	bookmarks, err := client.Bot().ListBookmarksContext(ctx, channelID)
	if err != nil {
		return cli.ClassifyError(err)
	}

	for _, bm := range bookmarks {
		if err := p.PrintItem(bookmarkToMap(bm)); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}

func bookmarkToMap(bm slack.Bookmark) map[string]any {
	data, _ := json.Marshal(bm)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}
