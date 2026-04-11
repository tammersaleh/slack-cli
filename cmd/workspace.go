package cmd

import (
	"context"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type WorkspaceCmd struct {
	Info WorkspaceInfoCmd `cmd:"" help:"Show workspace information."`
}

type WorkspaceInfoCmd struct{}

func (c *WorkspaceInfoCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()

	info, err := client.Bot().GetTeamInfoContext(ctx)
	if err != nil {
		return cli.ClassifyError(err)
	}

	if err := p.PrintItem(teamToMap(*info)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

func teamToMap(t slack.TeamInfo) map[string]any { return toMap(t) }
