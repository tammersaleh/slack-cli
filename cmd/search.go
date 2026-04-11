package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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

	p := cli.NewPrinter()
	ctx := context.Background()

	limit := c.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return &output.Error{Err: "invalid_input", Detail: "--limit must be between 1 and 100", Code: output.ExitGeneral}
	}

	page := 1
	if c.Cursor != "" {
		decoded, err := base64.StdEncoding.DecodeString(c.Cursor)
		if err != nil {
			return &output.Error{Err: "invalid_cursor", Detail: "Cannot decode cursor", Code: output.ExitGeneral}
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 || parts[0] != "page" {
			return &output.Error{Err: "invalid_cursor", Detail: "Malformed cursor", Code: output.ExitGeneral}
		}
		page, err = strconv.Atoi(parts[1])
		if err != nil || page < 1 {
			return &output.Error{Err: "invalid_cursor", Detail: "Invalid page in cursor", Code: output.ExitGeneral}
		}
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
				nextCursor = base64.StdEncoding.EncodeToString(
					[]byte(fmt.Sprintf("page:%d", msgs.Paging.Page+1)),
				)
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

	p := cli.NewPrinter()
	ctx := context.Background()

	limit := c.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return &output.Error{Err: "invalid_input", Detail: "--limit must be between 1 and 100", Code: output.ExitGeneral}
	}

	page := 1
	if c.Cursor != "" {
		decoded, err := base64.StdEncoding.DecodeString(c.Cursor)
		if err != nil {
			return &output.Error{Err: "invalid_cursor", Detail: "Cannot decode cursor", Code: output.ExitGeneral}
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 || parts[0] != "page" {
			return &output.Error{Err: "invalid_cursor", Detail: "Malformed cursor", Code: output.ExitGeneral}
		}
		page, err = strconv.Atoi(parts[1])
		if err != nil || page < 1 {
			return &output.Error{Err: "invalid_cursor", Detail: "Invalid page in cursor", Code: output.ExitGeneral}
		}
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
				nextCursor = base64.StdEncoding.EncodeToString(
					[]byte(fmt.Sprintf("page:%d", files.Paging.Page+1)),
				)
			}
			return p.PrintMeta(output.Meta{
				HasMore:    hasMore,
				NextCursor: nextCursor,
			})
		}

		page++
	}
}

func fileToMap(f slack.File) map[string]any {
	data, _ := json.Marshal(f)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func searchMessageToMap(msg slack.SearchMessage) map[string]any {
	data, _ := json.Marshal(msg)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}
