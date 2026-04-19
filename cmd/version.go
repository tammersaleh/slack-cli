package cmd

import (
	"github.com/tammersaleh/slack-cli/internal/output"
)

// Version is set at build time via -ldflags.
var Version = "dev"

type VersionCmd struct{}

func (c *VersionCmd) Run(cli *CLI) error {
	p := cli.NewPrinter()
	if err := p.PrintItem(map[string]any{
		"version": Version,
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}
