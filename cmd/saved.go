package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type SavedCmd struct {
	List   SavedListCmd   `cmd:"" help:"List saved-for-later items."`
	Counts SavedCountsCmd `cmd:"" help:"Show saved item counts."`
}

type SavedListCmd struct {
	Limit            int    `help:"Page size." default:"20"`
	Cursor           string `help:"Continue from previous page."`
	All              bool   `help:"Fetch all pages."`
	Enrich           bool   `help:"Fetch message text and channel names for each item."`
	IncludeCompleted bool   `help:"Include completed items."`
}

type SavedCountsCmd struct{}

// savedItem is a single item from the saved.list response.
type savedItem struct {
	ItemID        string `json:"item_id"`
	TS            string `json:"ts"`
	DateCreated   int64  `json:"date_created"`
	DateDue       int64  `json:"date_due,omitempty"`
	DateCompleted int64  `json:"date_completed,omitempty"`
	TodoState     string `json:"todo_state"`
}

// savedListResponse is the parsed saved.list API response.
type savedListResponse struct {
	SavedItems       []savedItem    `json:"saved_items"`
	Counts           map[string]any `json:"counts"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

func (c *SavedListCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	if err := requireSessionToken(client); err != nil {
		return err
	}

	cli.NewResolver(client) // populate resolver for output enrichment
	p := cli.NewPrinter()
	ctx := context.Background()

	limit := c.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	cursor := c.Cursor

	for {
		body := map[string]any{
			"count": limit,
		}
		if c.IncludeCompleted {
			body["include_completed"] = true
		}
		if cursor != "" {
			body["cursor"] = cursor
		}

		data, err := client.PostInternal(ctx, "saved.list", body)
		if err != nil {
			return cli.ClassifyError(err)
		}

		var resp savedListResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return &output.Error{Err: "parse_error", Detail: "Failed to parse saved.list response", Code: output.ExitGeneral}
		}

		var enrichment map[string]enrichedData
		if c.Enrich && len(resp.SavedItems) > 0 {
			enrichment = enrichItems(ctx, client, resp.SavedItems)
		}

		for _, item := range resp.SavedItems {
			m := formatSavedItem(item)
			if enrichment != nil {
				key := item.ItemID + ":" + item.TS
				if e, ok := enrichment[key]; ok {
					if e.ChannelName != "" {
						m["channel_name"] = e.ChannelName
					}
					if e.Text != "" {
						m["text"] = e.Text
					}
					if e.FromUser != "" {
						m["from_user"] = e.FromUser
					}
				}
			}
			if err := p.PrintItem(m); err != nil {
				return err
			}
		}

		nextCursor := resp.ResponseMetadata.NextCursor
		if !c.All || nextCursor == "" {
			return p.PrintMeta(output.Meta{
				HasMore:    nextCursor != "",
				NextCursor: nextCursor,
			})
		}
		cursor = nextCursor
	}
}

func (c *SavedCountsCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	if err := requireSessionToken(client); err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()

	data, err := client.PostInternal(ctx, "saved.list", map[string]any{
		"count": 1,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	var resp savedListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return &output.Error{Err: "parse_error", Detail: "Failed to parse saved.list response", Code: output.ExitGeneral}
	}

	if err := p.PrintItem(resp.Counts); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

func requireSessionToken(client *api.Client) error {
	if !strings.HasPrefix(client.Token(), "xoxc-") {
		return &output.Error{
			Err:    "session_token_required",
			Detail: "saved commands require a session token (xoxc-)",
			Hint:   "Run 'slack auth login --desktop' to authenticate with a session token",
			Code:   output.ExitAuth,
		}
	}
	return nil
}

func formatSavedItem(item savedItem) map[string]any {
	tsNoDot := strings.ReplaceAll(item.TS, ".", "")
	permalink := fmt.Sprintf("https://slack.com/archives/%s/p%s", item.ItemID, tsNoDot)

	m := map[string]any{
		"channel_id": item.ItemID,
		"message_ts": item.TS,
		"saved_at":   time.Unix(item.DateCreated, 0).UTC().Format(time.RFC3339),
		"todo_state": item.TodoState,
		"permalink":  permalink,
	}
	if item.DateDue > 0 {
		m["due"] = time.Unix(item.DateDue, 0).UTC().Format(time.RFC3339)
	}
	if item.DateCompleted > 0 {
		m["completed_at"] = time.Unix(item.DateCompleted, 0).UTC().Format(time.RFC3339)
	}
	return m
}

// enrichedData holds message text and channel name fetched per saved item.
type enrichedData struct {
	Text        string
	FromUser    string
	ChannelName string
}

const enrichConcurrency = 10

// enrichItems fetches message text and channel names concurrently.
func enrichItems(ctx context.Context, client *api.Client, items []savedItem) map[string]enrichedData {
	result := make(map[string]enrichedData)
	var mu sync.Mutex
	sem := make(chan struct{}, enrichConcurrency)
	var wg sync.WaitGroup

	for _, item := range items {
		wg.Add(1)
		go func(item savedItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			key := item.ItemID + ":" + item.TS
			var ed enrichedData

			// Fetch message text.
			resp, err := client.Bot().GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
				ChannelID: item.ItemID,
				Latest:    item.TS,
				Inclusive: true,
				Limit:     1,
			})
			if err == nil && len(resp.Messages) > 0 {
				ed.Text = resp.Messages[0].Text
				ed.FromUser = resp.Messages[0].User
			}

			// Fetch channel name.
			ch, err := client.Bot().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
				ChannelID: item.ItemID,
			})
			if err == nil && ch != nil {
				ed.ChannelName = ch.Name
			}

			mu.Lock()
			result[key] = ed
			mu.Unlock()
		}(item)
	}

	wg.Wait()
	return result
}
