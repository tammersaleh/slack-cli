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
	Enrich           bool   `help:"Fetch message text and sender for each item."`
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

	// saved.list is an internal endpoint and needs a session (xoxc-) token,
	// which on Enterprise Grid has to be the org (E-prefix) context. Build it
	// first so a bot token fails fast, before any other work.
	sessionClient, err := cli.NewSessionClient()
	if err != nil {
		return err
	}
	// The two credentials can carry different auth methods, and cli.authMethod is
	// a single ambient value ClassifyError reads to pick its re-auth hint. Keep
	// both and set whichever matches the stage that may surface a raw error,
	// otherwise a revoked workspace token tells the user to repair the session
	// credential. Safe without locking: the stages are synchronous and
	// enrichItems joins its workers before returning.
	sessionAuthMethod := cli.authMethod
	publicAuthMethod := sessionAuthMethod

	// Message lookups need the workspace (T-prefix) token: chat.getPermalink is
	// enterprise_is_restricted on the org context the session client targets, so
	// running the reply fallback there costs every saved thread reply its text
	// (measured 13 of 35 items on a Grid org). conversations.history and
	// conversations.info both work on the org token, which is why one client
	// sufficed until the fallback existed.
	//
	// Built only for --enrich. Requiring a workspace credential for a bare
	// `saved list` would break a topology that used to work - an org session
	// credential alone - to buy nothing, since the org token resolves channel
	// names perfectly well. When it is built it must precede NewResolver, so the
	// resolver keys its cache on the workspace team id.
	client := sessionClient
	if c.Enrich {
		client, err = cli.NewClient()
		if err != nil {
			return err
		}
		publicAuthMethod = cli.authMethod
	}

	cli.NewResolver(client) // populate resolver for output enrichment
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	limit := c.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Set when any row got an item-local enrich marker, so the command can
	// exit nonzero once the whole stream has been written.
	partialEnrich := false

	// Parsing and --enrich lookups happen here rather than while printing:
	// a page is built completely before its first line reaches stdout, so a
	// failure can't leave half a page written.
	fetch := func(cursor string) ([]map[string]any, string, error) {
		body := map[string]any{
			"count": limit,
		}
		if c.IncludeCompleted {
			body["include_completed"] = true
		}
		if cursor != "" {
			body["cursor"] = cursor
		}

		cli.authMethod = sessionAuthMethod
		data, err := sessionClient.PostInternal(ctx, "saved.list", body)
		if err != nil {
			return nil, "", err
		}

		var resp savedListResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, "", &output.Error{Err: "parse_error", Detail: "Failed to parse saved.list response", Code: output.ExitGeneral}
		}

		// Everything past this point runs on the public client, including the
		// resolver's name lookups during emit.
		cli.authMethod = publicAuthMethod

		var outcomes []enrichOutcome
		if c.Enrich && len(resp.SavedItems) > 0 {
			outcomes, err = enrichItems(ctx, cli, client, resp.SavedItems)
			if err != nil {
				return nil, "", err
			}
		}

		rows := make([]map[string]any, 0, len(resp.SavedItems))
		for i, item := range resp.SavedItems {
			m := formatSavedItem(item)
			if outcomes != nil {
				o := outcomes[i]
				if o.errCode != "" {
					// The saved item itself arrived fine - only the content
					// lookup failed - so the row keeps its base fields and
					// carries a marker rather than replacing them.
					m["enrich_error"] = o.errCode
					partialEnrich = true
				} else {
					// Emitted only when non-empty, but driven by o.resolved:
					// a block-only or bot message legitimately has neither,
					// and that is a success, not a miss.
					if o.text != "" {
						m["text"] = o.text
					}
					if o.fromUser != "" {
						m["from_user"] = o.fromUser
					}
				}
			}
			rows = append(rows, m)
		}
		return rows, resp.ResponseMetadata.NextCursor, nil
	}

	if err := streamPages(ctx, cli, p, "saved.list", c.Cursor, c.All, fetch, func(rows []map[string]any) error {
		for _, m := range rows {
			if err := p.PrintItem(m); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Reported only after the stream finished cleanly. Returning this from the
	// fetch closure would suppress the very page holding the marked rows, and
	// a fatal page must outrank an earlier page's partial status.
	if partialEnrich {
		return &output.ExitError{Code: output.ExitGeneral}
	}
	return nil
}

func (c *SavedCountsCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

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

// enrichOutcome is one saved item's content lookup, held at the item's input
// position. resolved is tracked on its own rather than inferred from a
// non-empty text: a block-only or bot message has neither text nor user and is
// still a successful lookup.
type enrichOutcome struct {
	resolved bool
	text     string
	fromUser string
	errCode  string // stable code when this one item could not be looked up
}

// itemLocalEnrichErrors are the Slack errors that describe a single saved item
// - a message that is gone, a conversation this token cannot read - rather than
// the run as a whole. They mark their own row and let the rest of the page
// through.
//
// The membership test is applied fail-closed: anything absent here, including a
// code Slack has not invented yet, aborts the page. The inverse arrangement
// (enumerate the fatal codes, inline the rest) is how this bug reappears under
// a new code, which is exactly what it did - every error used to be swallowed,
// so a rate limit and a deleted message both read as "no text".
var itemLocalEnrichErrors = map[string]bool{
	"message_not_found": true,
	"thread_not_found":  true,
	"not_in_channel":    true,
	"channel_not_found": true,
}

const enrichConcurrency = 10

// enrichItems looks up the message behind every saved item, bounded to
// enrichConcurrency in-flight items. Results come back indexed by input
// position, so a row's content cannot depend on which worker finished first.
//
// Note this bounds concurrent work, not request rate: it is not a rate limiter
// and a cap of one would still spend a hundred requests inside a minute.
//
// Nothing retries an individual lookup. slack-go only retries inside its own
// GetAll* helpers, and conversations.history, chat.getPermalink and
// conversations.replies all go through its one-shot postMethod/getMethod. A 429
// here becomes a systemic error, which means api.FetchPage retries the whole
// fetch closure with Retry-After backoff - so rate-limited enrichment does get
// retried, but at page granularity: saved.list and every already-successful
// lookup on the page are redone, up to maxAttempts times, and the resulting
// RateLimitExhaustedError names saved.list rather than the method that was
// actually limited. Coarse but output-correct, since the abandoned attempt
// emits nothing. Per-call retry needs the per-method coordinator noted below.
//
// A non-nil error means the page is void - the caller must emit none of it.
// The error is returned unclassified so streamPages can decide resumability;
// classification here is only used to sort item-local from systemic.
func enrichItems(ctx context.Context, cli *CLI, client *api.Client, items []savedItem) ([]enrichOutcome, error) {
	outcomes := make([]enrichOutcome, len(items))

	// A systemic failure voids the page, so the first one cancels its siblings
	// rather than letting them spend requests on output nobody will see.
	pageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		fatalOnce sync.Once
		fatalErr  error
	)
	// Keeps the first substantive cause. Siblings woken by the cancel report
	// context.Canceled, which must not be allowed to overwrite it.
	failPage := func(err error) {
		fatalOnce.Do(func() {
			fatalErr = err
			cancel()
		})
	}

	sem := make(chan struct{}, enrichConcurrency)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		go func(i int, item savedItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-pageCtx.Done():
				return
			}
			defer func() { <-sem }()

			// A select with both cases ready can pick the slot over a done
			// context, so re-check before spending a request.
			if pageCtx.Err() != nil {
				return
			}

			msg, err := lookupSavedMessage(pageCtx, client, item)
			if err != nil {
				if code := cli.ClassifyError(err).Err; itemLocalEnrichErrors[code] {
					outcomes[i] = enrichOutcome{errCode: code}
					return
				}
				failPage(err)
				return
			}
			if msg == nil {
				// History saw nothing and it is not a reply either.
				outcomes[i] = enrichOutcome{errCode: "message_not_found"}
				return
			}
			outcomes[i] = enrichOutcome{resolved: true, text: msg.Text, fromUser: msg.User}
		}(i, item)
	}

	// Always join before reading outcomes: returning while workers are live
	// would leak goroutines still mutating the slice and still calling Slack.
	wg.Wait()

	if fatalErr != nil {
		return nil, fatalErr
	}
	// The caller's own deadline expiring is not a page of missing messages.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Fail closed on the fourth state. Every item must have ended up either
	// resolved or marked; neither means a blank, unmarked row at exit 0, which
	// is the exact defect this rewrite exists to remove. Unreachable today - a
	// worker only skips its lookup when pageCtx is done, and both causes of
	// that are checked above - so this guards a future edit, not a known path.
	for i := range outcomes {
		if !outcomes[i].resolved && outcomes[i].errCode == "" {
			return nil, fmt.Errorf("enrichment produced no outcome for item %d (%s at %s)",
				i, items[i].ItemID, items[i].TS)
		}
	}
	return outcomes, nil
}

// lookupSavedMessage resolves the message a saved item points at.
//
// conversations.history is the fast path, but it never returns thread replies -
// a saved reply comes back as an empty page. So a successful history response
// that lacks the exact target falls back to the permalink + conversations.replies
// walk that message get uses.
//
// Returns (nil, nil) when the timestamp names no message and is not a reply.
func lookupSavedMessage(ctx context.Context, client *api.Client, item savedItem) (*slack.Message, error) {
	resp, err := client.Bot().GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
		ChannelID: item.ItemID,
		Oldest:    item.TS,
		Latest:    item.TS,
		Inclusive: true,
		Limit:     1,
	})
	if err != nil {
		// A history failure says nothing about whether this is a reply, and
		// the fallback would report a second, less specific error.
		return nil, err
	}

	// Match the exact timestamp instead of trusting Messages[0]. The window
	// should only ever hold the target, and attaching a neighbouring message's
	// text to a saved item is a wrong answer that reads as a right one.
	if msg := messageWithTS(resp.Messages, item.TS); msg != nil {
		return msg, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// saved.list carries no thread parent, so findThreadReply discovers it via
	// chat.getPermalink.
	return findThreadReply(ctx, client, item.ItemID, item.TS, "")
}

// messageWithTS returns the message with exactly this timestamp, or nil.
func messageWithTS(msgs []slack.Message, ts string) *slack.Message {
	for i := range msgs {
		if msgs[i].Timestamp == ts {
			return &msgs[i]
		}
	}
	return nil
}
