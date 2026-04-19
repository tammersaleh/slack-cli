package cmd

import (
	"sort"
	"strings"

	"github.com/tammersaleh/slack-cli/internal/output"
)

type EmojiCmd struct {
	List EmojiListCmd `cmd:"" help:"List custom emoji."`
}

type EmojiListCmd struct {
	Query string `help:"Filter by name substring (client-side)."`
}

func (c *EmojiListCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	emoji, err := client.Bot().GetEmojiContext(ctx)
	if err != nil {
		return cli.ClassifyError(err)
	}

	// Sort for deterministic output.
	names := make([]string, 0, len(emoji))
	for name := range emoji {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if c.Query != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(c.Query)) {
			continue
		}
		if err := p.PrintItem(map[string]any{
			"name": name,
			"url":  emoji[name],
		}); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}
