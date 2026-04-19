package cmd

import (
	"github.com/tammersaleh/slack-cli/internal/output"
)

type DndCmd struct {
	Info DndInfoCmd `cmd:"" help:"Get Do Not Disturb info."`
}

type DndInfoCmd struct {
	Users []string `arg:"" required:"" help:"User IDs or emails."`
}

func (c *DndInfoCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx, cancel := cli.Context()
	defer cancel()
	errorCount := 0

	for _, input := range c.Users {
		userID, err := r.ResolveUser(ctx, input)
		if err != nil {
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":  input,
				"error":  "user_not_found",
				"detail": "No user matching '" + input + "'",
			}); err != nil {
				return err
			}
			continue
		}

		dnd, err := client.Bot().GetDNDInfoContext(ctx, &userID)
		if err != nil {
			oErr := cli.ClassifyError(err)
			if oErr.Code != output.ExitGeneral {
				return oErr
			}
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":  input,
				"error":  oErr.Err,
				"detail": oErr.Detail,
			}); err != nil {
				return err
			}
			continue
		}

		if err := p.PrintItem(map[string]any{
			"input":             input,
			"user_id":           userID,
			"dnd_enabled":       dnd.Enabled,
			"next_dnd_start_ts": dnd.NextStartTimestamp,
			"next_dnd_end_ts":   dnd.NextEndTimestamp,
			"snooze_enabled":    dnd.SnoozeEnabled,
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
