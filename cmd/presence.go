package cmd

import (
	"context"

	"github.com/tammersaleh/slack-cli/internal/output"
)

type PresenceCmd struct {
	Get PresenceGetCmd `cmd:"" help:"Get user presence."`
}

type PresenceGetCmd struct {
	Users []string `arg:"" required:"" help:"User IDs or emails."`
}

func (c *PresenceGetCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx := context.Background()
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

		presence, err := client.Bot().GetUserPresenceContext(ctx, userID)
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
			"input":       input,
			"user_id":     userID,
			"presence":    presence.Presence,
			"online":      presence.Online,
			"auto_away":   presence.AutoAway,
			"manual_away": presence.ManualAway,
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
