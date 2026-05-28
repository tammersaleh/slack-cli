package cmd

import (
	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type ReactionCmd struct {
	List ReactionListCmd `cmd:"" help:"List reactions on a message."`
}

type ReactionListCmd struct {
	Args []string `arg:"" optional:"" name:"args" help:"Channel + timestamps, or message URLs."`
}

func (ReactionListCmd) Help() string {
	return `List reactions on one or more messages. Two forms:

  slack reaction list <channel> <ts>...     # channel + timestamps
  slack reaction list <message-url>...        # Slack message permalinks

A message permalink carries its own channel, so several URLs may target
different channels. The two forms can't be mixed.`
}

func (c *ReactionListCmd) Run(cli *CLI) error {
	refs, err := parseMessageRefs(c.Args)
	if err != nil {
		return &output.Error{Err: "invalid_input", Detail: err.Error(), Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx, cancel := cli.Context()
	defer cancel()
	errorCount := 0

	channelIDs, cerr := resolveRefChannels(ctx, r, refs)
	if cerr != nil {
		return cerr
	}

	for _, ref := range refs {
		channelID := channelIDs[ref.channel]
		item, err := client.Bot().GetReactionsContext(ctx, slack.ItemRef{
			Channel:   channelID,
			Timestamp: ref.ts,
		}, slack.GetReactionsParameters{Full: true})
		if err != nil {
			oErr := cli.ClassifyError(err)
			// If it's a specific message error, inline it.
			if oErr.Code == output.ExitGeneral {
				errorCount++
				if err := p.PrintItem(map[string]any{
					"input":      ref.input,
					"channel_id": channelID,
					"error":      oErr.Err,
					"detail":     oErr.Detail,
				}); err != nil {
					return err
				}
				continue
			}
			return oErr
		}

		for _, reaction := range item.Reactions {
			if err := p.PrintItem(map[string]any{
				"input":      ref.input,
				"channel_id": channelID,
				"name":       reaction.Name,
				"count":      reaction.Count,
				"users":      reaction.Users,
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
