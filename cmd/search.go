package cmd

import (
	"strconv"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type SearchCmd struct {
	Messages SearchMessagesCmd `cmd:"" help:"Search messages."`
	Files    SearchFilesCmd    `cmd:"" help:"Search files."`
}

type SearchMessagesCmd struct {
	Query   string `arg:"" required:"" help:"Search query (supports Slack search modifiers)."`
	Limit   int    `help:"Page size." default:"20"`
	Cursor  string `help:"Continue from previous page."`
	All     bool   `help:"Fetch all pages."`
	Sort    string `help:"Sort order: timestamp, score." default:"timestamp" enum:"timestamp,score"`
	SortDir string `help:"Sort direction: asc, desc." default:"desc" name:"sort-dir" enum:"asc,desc"`
}

func (SearchMessagesCmd) Help() string {
	return `Search messages with Slack's query modifiers. Requires a user
token (xoxp-) since bot tokens can't search. Combine modifiers freely:

  in:#channel          only this channel (also 'in:@user' for DMs)
  from:@user           posted by this user
  to:@user             DMs to this user
  after:YYYY-MM-DD     / before:YYYY-MM-DD / on:YYYY-MM-DD / during:month
  has:link             / has:pin / has:reaction / has:file / has:image
  is:thread            / is:saved / is:dm / is:mpdm

Example:

  slack search messages "deploy blocker in:#general from:@alice after:2026-01-01"

Each hit includes channel{id,name}, user, ts, text, and permalink.
Page with --cursor (a raw page number, pass-through from _meta.next_cursor)
or use --all to fetch every page.`
}

func (c *SearchMessagesCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	if client.User() == nil {
		return &output.Error{
			Err:    "missing_user_token",
			Detail: "Search requires a user token",
			Hint:   "Set SLACK_USER_TOKEN or re-run 'slack auth login'",
			Code:   output.ExitAuth,
		}
	}

	cli.NewResolver(client) // populate resolver for output enrichment
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	limit := c.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return &output.Error{Err: "invalid_input", Detail: "--limit must be between 1 and 100", Code: output.ExitGeneral}
	}

	page, err := parsePageCursor(c.Cursor)
	if err != nil {
		return err
	}

	for {
		params := slack.SearchParameters{
			Sort:          c.Sort,
			SortDirection: c.SortDir,
			Count:         limit,
			Page:          page,
		}

		msgs, err := client.User().SearchMessagesContext(ctx, c.Query, params)
		if err != nil {
			return cli.ClassifyError(err)
		}

		for _, match := range msgs.Matches {
			if err := p.PrintItem(searchMessageToMap(match)); err != nil {
				return err
			}
		}

		hasMore := msgs.Paging.Page < msgs.Paging.Pages
		if !c.All || !hasMore {
			nextCursor := ""
			if hasMore {
				nextCursor = strconv.Itoa(msgs.Paging.Page + 1)
			}
			return p.PrintMeta(output.Meta{
				HasMore:    hasMore,
				NextCursor: nextCursor,
			})
		}

		page++
	}
}

type SearchFilesCmd struct {
	Query   string `arg:"" required:"" help:"Search query (supports Slack search modifiers)."`
	Limit   int    `help:"Page size." default:"20"`
	Cursor  string `help:"Continue from previous page."`
	All     bool   `help:"Fetch all pages."`
	Sort    string `help:"Sort order: timestamp, score." default:"timestamp" enum:"timestamp,score"`
	SortDir string `help:"Sort direction: asc, desc." default:"desc" name:"sort-dir" enum:"asc,desc"`
}

func (c *SearchFilesCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	if client.User() == nil {
		return &output.Error{
			Err:    "missing_user_token",
			Detail: "Search requires a user token",
			Hint:   "Set SLACK_USER_TOKEN or re-run 'slack auth login'",
			Code:   output.ExitAuth,
		}
	}

	cli.NewResolver(client) // populate resolver for output enrichment
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	limit := c.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return &output.Error{Err: "invalid_input", Detail: "--limit must be between 1 and 100", Code: output.ExitGeneral}
	}

	page, err := parsePageCursor(c.Cursor)
	if err != nil {
		return err
	}

	for {
		params := slack.SearchParameters{
			Sort:          c.Sort,
			SortDirection: c.SortDir,
			Count:         limit,
			Page:          page,
		}

		files, err := client.User().SearchFilesContext(ctx, c.Query, params)
		if err != nil {
			return cli.ClassifyError(err)
		}

		for _, f := range files.Matches {
			if err := p.PrintItem(fileToMap(f)); err != nil {
				return err
			}
		}

		hasMore := files.Paging.Page < files.Paging.Pages
		if !c.All || !hasMore {
			nextCursor := ""
			if hasMore {
				nextCursor = strconv.Itoa(files.Paging.Page + 1)
			}
			return p.PrintMeta(output.Meta{
				HasMore:    hasMore,
				NextCursor: nextCursor,
			})
		}

		page++
	}
}

func fileToMap(f slack.File) map[string]any          { return toMap(f) }
func searchMessageToMap(msg slack.SearchMessage) map[string]any { return toMap(msg) }

// parsePageCursor parses a numeric page cursor ("2", "3", etc). Empty cursor
// returns page 1. Used by commands that wrap Slack's page-number APIs
// (search.messages, search.files, files.list) - cursors emitted by those
// commands are raw page numbers, not opaque tokens, for parity with
// cursor-based commands that pass Slack's tokens through unchanged.
func parsePageCursor(cursor string) (int, error) {
	if cursor == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(cursor)
	if err != nil || page < 1 {
		return 0, &output.Error{Err: "invalid_cursor", Detail: "Cursor must be a positive page number", Code: output.ExitGeneral}
	}
	return page, nil
}
