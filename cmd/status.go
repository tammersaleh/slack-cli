package cmd

import (
	"github.com/tammersaleh/slack-cli/internal/output"
)

type StatusCmd struct {
	Get StatusGetCmd `cmd:"" help:"Get user status."`
}

type StatusGetCmd struct {
	Users []string `arg:"" optional:"" help:"User IDs or emails (default: authenticated user)."`
}

func (c *StatusGetCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	r := cli.NewResolver(client)
	ctx, cancel := cli.Context()
	defer cancel()
	errorCount := 0

	users := c.Users
	if len(users) == 0 {
		// Default to authenticated user.
		auth, err := client.AuthTest(ctx)
		if err != nil {
			return cli.ClassifyError(err)
		}
		users = []string{auth.UserID}
	}

	// Every input resolves before the first row. A lookup that fails for a
	// reason other than the input (auth, a rate limit, --timeout) stops the
	// run with no rows; an input that matches no user keeps its own row.
	resolved, fatal := resolveInputs(ctx, users, r.ResolveUser, cli.userResolveError)
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

		user, err := r.FetchUser(ctx, userID)
		if err != nil {
			if abort := userLookupFailed(cli, p, input, err, &errorCount); abort != nil {
				return abort
			}
			continue
		}

		item := map[string]any{
			"user_id":           user.ID,
			"status_text":       user.Profile.StatusText,
			"status_emoji":      user.Profile.StatusEmoji,
			"status_expiration": user.Profile.StatusExpiration,
		}
		if len(c.Users) > 0 {
			item["input"] = input
		}
		if err := p.PrintItem(item); err != nil {
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
