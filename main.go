package main

import (
	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
)

func main() {
	var cli cmd.CLI
	ctx := kong.Parse(&cli,
		kong.Name("slack"),
		kong.Description("Read-only CLI for Slack."),
		kong.UsageOnError(),
	)
	err := ctx.Run(&cli)
	ctx.FatalIfErrorf(err)
}
