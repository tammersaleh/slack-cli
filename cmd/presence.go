package cmd

import (
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
	ctx, cancel := cli.Context()
	defer cancel()
	errorCount := 0

	// Every input resolves before the first row. A lookup that fails for a
	// reason other than the input (auth, a rate limit, --timeout) stops the
	// run with no rows; an input that matches no user keeps its own row.
	resolved, fatal := resolveInputs(ctx, c.Users, r.ResolveUser, cli.userResolveError)
	if fatal != nil {
		return abortWithTrailer(p, fatal, 0)
	}

	for _, u := range resolved {
		input, userID := u.input, u.id
		if u.miss != nil {
			errorCount++
			if err := p.PrintItem(u.miss.AsItem()); err != nil {
				return err
			}
			continue
		}

		presence, err := client.Bot().GetUserPresenceContext(ctx, userID)
		if err != nil {
			oErr := cli.ClassifyError(err)
			if isSystemicErr(oErr) {
				return abortWithTrailer(p, oErr, errorCount)
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
