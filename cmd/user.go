package cmd

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type UserCmd struct {
	List UserListCmd `cmd:"" help:"List users."`
	Info UserInfoCmd `cmd:"" help:"Show user details."`
}

type UserListCmd struct {
	Limit    int    `help:"Page size." default:"100"`
	Cursor   string `help:"Continue from previous page."`
	All      bool   `help:"Fetch all pages."`
	Query    string `help:"Filter by name or email substring (client-side)."`
	Presence bool   `help:"Include presence information."`
}

func (c *UserListCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()

	limit := c.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	// slack-go's users.list uses its own pagination. We use GetUsersPaginated
	// for single-page control.
	pag := client.Bot().GetUsersPaginated(
		slack.GetUsersOptionLimit(limit),
		slack.GetUsersOptionPresence(c.Presence),
	)

	if c.Cursor != "" {
		pag.Cursor = c.Cursor
	}

	for {
		var fetchErr error
		pag, fetchErr = pag.Next(ctx)
		if pag.Done(fetchErr) {
			return p.PrintMeta(output.Meta{})
		}
		if fetchErr != nil {
			return cli.ClassifyError(fetchErr)
		}

		for _, user := range pag.Users {
			if c.Query != "" && !matchesUserQuery(user, c.Query) {
				continue
			}
			if err := p.PrintItem(userToMap(user)); err != nil {
				return err
			}
		}

		nextCursor := pag.Cursor
		if !c.All || nextCursor == "" {
			return p.PrintMeta(output.Meta{
				HasMore:    nextCursor != "",
				NextCursor: nextCursor,
			})
		}
	}
}

type UserInfoCmd struct {
	Users []string `arg:"" required:"" help:"User ID or email."`
}

func (c *UserInfoCmd) Run(cli *CLI) error {
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

		user, err := client.Bot().GetUserInfoContext(ctx, userID)
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

		m := userToMap(*user)
		m["input"] = input
		if err := p.PrintItem(m); err != nil {
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

func userToMap(u slack.User) map[string]any {
	data, _ := json.Marshal(u)
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

func matchesUserQuery(u slack.User, query string) bool {
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(u.Name), q) ||
		strings.Contains(strings.ToLower(u.RealName), q) ||
		strings.Contains(strings.ToLower(u.Profile.Email), q) ||
		strings.Contains(strings.ToLower(u.Profile.DisplayName), q)
}
