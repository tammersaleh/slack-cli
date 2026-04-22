package cmd

import (
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
	ctx, cancel := cli.Context()
	defer cancel()

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return output.ChannelNotFound(c.Channel)
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

func bookmarkToMap(bm slack.Bookmark) map[string]any { return toMap(bm) }
