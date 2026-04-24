package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type DraftCmd struct {
	List   DraftListCmd   `cmd:"" help:"List drafts."`
	Create DraftCreateCmd `cmd:"" help:"Create a new draft."`
	Update DraftUpdateCmd `cmd:"" help:"Update an existing draft."`
	Delete DraftDeleteCmd `cmd:"" help:"Delete drafts."`
}

type DraftListCmd struct {
	Active          bool `help:"Exclude scheduled drafts (active only)."`
	Limit           int  `help:"Page size." default:"50"`
	IncludeSent     bool `help:"Include drafts already sent (suppressed by default)."`
	IncludeDeleted  bool `help:"Include drafts marked deleted server-side (suppressed by default)."`
}

// draftData is the parsed draft object from Slack responses.
type draftData struct {
	ID                  string          `json:"id"`
	ClientMsgID         string          `json:"client_msg_id"`
	UserID              string          `json:"user_id"`
	TeamID              string          `json:"team_id"`
	DateCreated         int64           `json:"date_created"`
	DateScheduled       int64           `json:"date_scheduled"`
	LastUpdatedTS       string          `json:"last_updated_ts"`
	ClientLastUpdatedTS string          `json:"client_last_updated_ts"`
	Blocks              json.RawMessage `json:"blocks"`
	FileIDs             []string        `json:"file_ids"`
	IsFromComposer      bool            `json:"is_from_composer"`
	IsDeleted           bool            `json:"is_deleted"`
	IsSent              bool            `json:"is_sent"`
	Destinations        []destination   `json:"destinations"`
}

type destination struct {
	ChannelID string   `json:"channel_id,omitempty"`
	UserIDs   []string `json:"user_ids,omitempty"`
	ThreadTS  string   `json:"thread_ts,omitempty"`
	Broadcast bool     `json:"broadcast,omitempty"`
}

// normalizeDestinationsForWrite strips user_ids from destinations that also
// have a channel_id. Slack's drafts.list response includes both for
// single-recipient DMs, but drafts.update / drafts.create reject the combo
// with "both_user_ids_and_channel_id_provided_in_destination". channel_id
// wins since it's the canonical identifier once the conversation exists.
func normalizeDestinationsForWrite(dests []destination) []destination {
	out := make([]destination, len(dests))
	for i, d := range dests {
		out[i] = d
		if d.ChannelID != "" {
			out[i].UserIDs = nil
		}
	}
	return out
}

type draftsListResponse struct {
	Drafts  []draftData `json:"drafts"`
	HasMore bool        `json:"has_more"`
}

func (c *DraftListCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}
	ctx, cancel := cli.Context()
	defer cancel()
	p := cli.NewPrinter()

	limit := c.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	drafts, hasMore, err := fetchDrafts(ctx, client, c.Active, limit)
	if err != nil {
		return cli.ClassifyError(err)
	}
	for _, d := range drafts {
		if d.IsSent && !c.IncludeSent {
			continue
		}
		if d.IsDeleted && !c.IncludeDeleted {
			continue
		}
		if err := p.PrintItem(formatDraft(d)); err != nil {
			return err
		}
	}
	return p.PrintMeta(output.Meta{HasMore: hasMore})
}

func fetchDrafts(ctx context.Context, client *api.Client, activeOnly bool, limit int) ([]draftData, bool, error) {
	params := map[string]string{
		"limit": strconv.Itoa(limit),
	}
	if activeOnly {
		params["is_active"] = "true"
	}
	data, err := client.PostInternalForm(ctx, "drafts.list", params)
	if err != nil {
		return nil, false, err
	}
	var resp draftsListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, false, err
	}
	return resp.Drafts, resp.HasMore, nil
}

type DraftCreateCmd struct {
	Channel       string `arg:"" required:"" help:"Channel ID or name."`
	Thread        string `help:"Thread timestamp to reply to."`
	Broadcast     bool   `help:"With --thread, also post to channel."`
	At            string `help:"Schedule send at RFC 3339 timestamp or YYYY-MM-DD."`
	DateScheduled int64  `help:"Schedule send at Unix epoch (alternative to --at)."`
}

func (DraftCreateCmd) Help() string {
	return `Block Kit JSON is piped on stdin (required). Only rich_text
top-level blocks are allowed - Slack Desktop's Drafts compose editor
silently strips section / divider / header / context blocks when the
user opens the draft. Run 'slack skill' for the full Block Kit shape.

Minimal invocation:

  echo '[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello"}]}]}]' \
    | slack draft create '#general'

Thread reply:

  slack draft create '#general' --thread 1713299000.123456 < blocks.json

Scheduled send:

  slack draft create '#general' --at 2026-04-20T09:00:00-07:00 < blocks.json`
}

func (c *DraftCreateCmd) Run(cli *CLI) error {
	if c.Broadcast && c.Thread == "" {
		return &output.Error{Err: "invalid_input", Detail: "--broadcast requires --thread", Code: output.ExitGeneral}
	}
	if c.At != "" && c.DateScheduled > 0 {
		return &output.Error{Err: "invalid_input", Detail: "--at and --date-scheduled are mutually exclusive", Code: output.ExitGeneral}
	}

	blocksJSON, err := readBlocksRequired(cli)
	if err != nil {
		return err
	}

	scheduled := c.DateScheduled
	if c.At != "" {
		epoch, err := parseScheduledTime(c.At)
		if err != nil {
			return output.InvalidTimestamp("--at", c.At)
		}
		scheduled = epoch
	}

	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}
	r := cli.NewResolver(client)
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	channelID, err := r.ResolveChannel(ctx, c.Channel)
	if err != nil {
		return output.ChannelNotFound(c.Channel)
	}

	dest := destination{ChannelID: channelID}
	if c.Thread != "" {
		dest.ThreadTS = c.Thread
		dest.Broadcast = c.Broadcast
	}
	destsJSON, err := json.Marshal([]destination{dest})
	if err != nil {
		return &output.Error{Err: "encode_error", Detail: err.Error(), Code: output.ExitGeneral}
	}

	params := map[string]string{
		"blocks":           blocksJSON,
		"destinations":     string(destsJSON),
		"client_msg_id":    uuid.NewString(),
		"file_ids":         "[]",
		"is_from_composer": "true",
	}
	if scheduled > 0 {
		params["date_scheduled"] = strconv.FormatInt(scheduled, 10)
	}

	data, err := client.PostInternalForm(ctx, "drafts.create", params)
	if err != nil {
		return cli.ClassifyError(err)
	}

	var resp struct {
		Draft draftData `json:"draft"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return &output.Error{Err: "parse_error", Detail: "Failed to parse drafts.create response", Code: output.ExitGeneral}
	}
	if err := p.PrintItem(formatDraft(resp.Draft)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

type DraftUpdateCmd struct {
	DraftID       string `arg:"" required:"" help:"Draft ID to update."`
	At            string `help:"Reschedule send at RFC 3339 timestamp or YYYY-MM-DD."`
	DateScheduled int64  `help:"Reschedule send at Unix epoch (use --clear-schedule to remove)."`
	ClearSchedule bool   `help:"Remove scheduled send."`
}

func (DraftUpdateCmd) Help() string {
	return `Replace a draft's blocks and/or reschedule it. Block Kit JSON
on stdin is optional: if piped, it replaces the draft's 'blocks';
if omitted, existing blocks are preserved (useful for schedule-only
updates). Same rich_text-only rule as draft create.

If Slack Desktop has already tombstoned the draft (is_deleted=true),
the CLI transparently creates a replacement at the same destination
and emits the new draft ID to stdout, with a warning on stderr.

Examples:

  slack draft update Dr01234 < blocks.json           # replace content
  slack draft update Dr01234 --at 2026-04-22         # reschedule only
  slack draft update Dr01234 --clear-schedule        # drop scheduled send
  slack draft update Dr01234 --clear-schedule < blocks.json  # both`
}

func (c *DraftUpdateCmd) Run(cli *CLI) error {
	scheduledFlags := 0
	if c.At != "" {
		scheduledFlags++
	}
	if c.DateScheduled > 0 {
		scheduledFlags++
	}
	if c.ClearSchedule {
		scheduledFlags++
	}
	if scheduledFlags > 1 {
		return &output.Error{Err: "invalid_input", Detail: "--at, --date-scheduled, and --clear-schedule are mutually exclusive", Code: output.ExitGeneral}
	}

	// Blocks come from stdin; empty means "keep existing blocks".
	newBlocks, err := readBlocksIfProvided(cli)
	if err != nil {
		return err
	}
	if newBlocks == "" && scheduledFlags == 0 {
		return &output.Error{Err: "invalid_input", Detail: "No update specified: pipe Block Kit JSON on stdin, or pass --at / --date-scheduled / --clear-schedule", Code: output.ExitGeneral}
	}

	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	existing, err := findDraftByID(ctx, client, c.DraftID)
	if err != nil {
		if oe, ok := err.(*output.Error); ok {
			return oe
		}
		return cli.ClassifyError(err)
	}

	intent, err := c.buildIntent(existing, newBlocks)
	if err != nil {
		return err
	}

	// Skip the doomed update when we already know the draft is tombstoned.
	// Desktop's Drafts-panel reconciliation tombstones any server draft
	// whose blocks lack a rich_text body (see validateBlocksShape), and
	// a second Slack client (browser, another Desktop) can tombstone one
	// out of band too. Once tombstoned, drafts.update returns
	// draft_update_invalid and drafts.delete returns draft_delete_invalid.
	// The only path forward is to create a fresh draft at the same destination.
	if existing.IsDeleted {
		warnTombstoned(p.Err, c.DraftID)
		return c.createReplacement(ctx, cli, client, p, intent)
	}

	params := map[string]string{
		"draft_id":               existing.ID,
		"client_last_updated_ts": padDraftTS(existing.LastUpdatedTS),
		"blocks":                 intent.blocks,
		"destinations":           intent.destinations,
		"client_msg_id":          existing.ClientMsgID,
		"file_ids":               intent.fileIDs,
		"is_from_composer":       "true", // match drafts.create; heal old drafts stuck at false
		"date_scheduled":         strconv.FormatInt(intent.scheduled, 10),
	}

	data, err := client.PostInternalForm(ctx, "drafts.update", params)
	if err != nil {
		// The list might say is_deleted=false while the API already treats
		// the draft as tombstoned - race between our list and update.
		if isDraftTombstoneError(err) {
			warnTombstoned(p.Err, c.DraftID)
			return c.createReplacement(ctx, cli, client, p, intent)
		}
		return cli.ClassifyError(err)
	}
	var resp struct {
		Draft draftData `json:"draft"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return &output.Error{Err: "parse_error", Detail: "Failed to parse drafts.update response", Code: output.ExitGeneral}
	}
	if err := p.PrintItem(formatDraft(resp.Draft)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// draftWriteIntent holds the pre-serialized form fields shared between an
// in-place update and a replacement create. Built once per command.
type draftWriteIntent struct {
	blocks       string // JSON-encoded Block Kit array
	destinations string // JSON-encoded destinations array (normalized)
	fileIDs      string // JSON-encoded file_ids array
	scheduled    int64
}

func (c *DraftUpdateCmd) buildIntent(existing *draftData, newBlocks string) (*draftWriteIntent, error) {
	blocksJSON := string(existing.Blocks)
	if newBlocks != "" {
		blocksJSON = newBlocks
	} else if err := validateBlocksShape([]byte(blocksJSON)); err != nil {
		// A schedule-only update would round-trip the existing blocks to
		// drafts.update. If those blocks can't survive Desktop's
		// reconciliation, skip the doomed call and tell the caller to
		// pipe a replacement body.
		return nil, err
	}

	destsJSON, err := json.Marshal(normalizeDestinationsForWrite(existing.Destinations))
	if err != nil {
		return nil, &output.Error{Err: "encode_error", Detail: err.Error(), Code: output.ExitGeneral}
	}

	scheduled := existing.DateScheduled
	switch {
	case c.ClearSchedule:
		scheduled = 0
	case c.At != "":
		epoch, err := parseScheduledTime(c.At)
		if err != nil {
			return nil, output.InvalidTimestamp("--at", c.At)
		}
		scheduled = epoch
	case c.DateScheduled > 0:
		scheduled = c.DateScheduled
	}

	fileIDs := "[]"
	if len(existing.FileIDs) > 0 {
		b, err := json.Marshal(existing.FileIDs)
		if err != nil {
			return nil, &output.Error{Err: "encode_error", Detail: err.Error(), Code: output.ExitGeneral}
		}
		fileIDs = string(b)
	}

	return &draftWriteIntent{
		blocks:       blocksJSON,
		destinations: string(destsJSON),
		fileIDs:      fileIDs,
		scheduled:    scheduled,
	}, nil
}

func (c *DraftUpdateCmd) createReplacement(ctx context.Context, cli *CLI, client *api.Client, p *output.Printer, intent *draftWriteIntent) error {
	params := map[string]string{
		"blocks":           intent.blocks,
		"destinations":     intent.destinations,
		"client_msg_id":    uuid.NewString(),
		"file_ids":         "[]", // file attachments don't carry across to replacement
		"is_from_composer": "true",
	}
	if intent.scheduled > 0 {
		params["date_scheduled"] = strconv.FormatInt(intent.scheduled, 10)
	}

	data, err := client.PostInternalForm(ctx, "drafts.create", params)
	if err != nil {
		return cli.ClassifyError(err)
	}
	var resp struct {
		Draft draftData `json:"draft"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return &output.Error{Err: "parse_error", Detail: "Failed to parse drafts.create response", Code: output.ExitGeneral}
	}
	if err := p.PrintItem(formatDraft(resp.Draft)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// isDraftTombstoneError reports whether err signals an is_deleted draft.
func isDraftTombstoneError(err error) bool {
	var s slack.SlackErrorResponse
	if errors.As(err, &s) {
		return s.Err == "draft_update_invalid" || s.Err == "draft_delete_invalid"
	}
	return false
}

// warnTombstoned writes a structured warning to stderr when a tombstoned
// draft forces auto-replacement. Not a fatal error - the replacement draft
// still lands on stdout as the command's normal output.
func warnTombstoned(w io.Writer, draftID string) {
	_, _ = fmt.Fprintf(w,
		`{"warning":"draft_tombstoned","draft_id":%q,"detail":"Draft was tombstoned by Slack Desktop (likely viewed there). Creating replacement at same destination - new draft ID on stdout."}`+"\n",
		draftID,
	)
}

type DraftDeleteCmd struct {
	DraftIDs []string `arg:"" required:"" name:"draft-id" help:"Draft IDs to delete."`
}

func (c *DraftDeleteCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	drafts, _, err := fetchDrafts(ctx, client, false, 100)
	if err != nil {
		return cli.ClassifyError(err)
	}
	byID := make(map[string]*draftData, len(drafts))
	for i := range drafts {
		byID[drafts[i].ID] = &drafts[i]
	}

	errorCount := 0
	for _, id := range c.DraftIDs {
		d, ok := byID[id]
		if !ok {
			errorCount++
			if err := p.PrintItem(output.DraftNotFound(id).AsItem()); err != nil {
				return err
			}
			continue
		}
		params := map[string]string{
			"draft_id":               d.ID,
			"client_last_updated_ts": padDraftTS(d.LastUpdatedTS),
		}
		if _, err := client.PostInternalForm(ctx, "drafts.delete", params); err != nil {
			oErr := cli.ClassifyError(err)
			if oErr.Code != output.ExitGeneral {
				return oErr
			}
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":  id,
				"error":  oErr.Err,
				"detail": oErr.Detail,
			}); err != nil {
				return err
			}
			continue
		}
		if err := p.PrintItem(map[string]any{"id": id, "deleted": true}); err != nil {
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

// findDraftByID fetches all drafts and returns the one with the given ID.
// Returns a draft_not_found error if no match. The caller is responsible
// for wrapping fetch errors via cli.ClassifyError.
func findDraftByID(ctx context.Context, client *api.Client, id string) (*draftData, error) {
	drafts, _, err := fetchDrafts(ctx, client, false, 100)
	if err != nil {
		return nil, err
	}
	for i := range drafts {
		if drafts[i].ID == id {
			return &drafts[i], nil
		}
	}
	return nil, output.DraftNotFound(id)
}

// padDraftTS normalises a Slack timestamp to exactly 7 decimal places.
// drafts.update and drafts.delete reject unpadded or over-long timestamps
// with conflicts. Shorter fractions are right-padded with zeros; longer
// fractions are truncated.
func padDraftTS(ts string) string {
	i := strings.Index(ts, ".")
	if i < 0 {
		return ts
	}
	frac := ts[i+1:]
	switch {
	case len(frac) < 7:
		frac += strings.Repeat("0", 7-len(frac))
	case len(frac) > 7:
		frac = frac[:7]
	}
	return ts[:i+1] + frac
}

// readBlocksRequired reads Block Kit JSON from stdin and returns it as a
// string. Fails with missing_blocks if stdin is a TTY (nothing piped) or if
// the input is empty.
func readBlocksRequired(cli *CLI) (string, error) {
	s, err := readBlocksIfProvided(cli)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", output.MissingBlocks()
	}
	return s, nil
}

// readBlocksIfProvided reads Block Kit JSON from stdin if any is piped in.
// Returns "" with no error when stdin is a TTY (interactive - nothing piped).
// Runs validateBlocksShape, which checks the JSON shape and enforces that
// at least one block is a rich_text with non-empty elements (anything less
// gets tombstoned by Desktop). Deeper block-level semantics surface from
// Slack's API as invalid_blocks.
func readBlocksIfProvided(cli *CLI) (string, error) {
	// In tests, cli.in is overridden with a bytes.Reader - always try to read.
	// In production, skip the read if stdin is a terminal.
	if cli.in == nil {
		info, err := os.Stdin.Stat()
		if err != nil {
			return "", &output.Error{Err: "stdin_error", Detail: err.Error(), Code: output.ExitGeneral}
		}
		if (info.Mode() & os.ModeCharDevice) != 0 {
			return "", nil
		}
	}
	raw, err := io.ReadAll(cli.Stdin())
	if err != nil {
		return "", &output.Error{Err: "stdin_error", Detail: err.Error(), Code: output.ExitGeneral}
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", nil
	}
	if err := validateBlocksShape([]byte(trimmed)); err != nil {
		return "", err
	}
	return trimmed, nil
}

// validateBlocksShape is the local gate before shipping draft blocks to
// Slack. Beyond basic JSON shape, it enforces that every top-level block
// is a non-empty rich_text. Slack Desktop's Drafts-panel reconciliation
// tombstones drafts without rich_text content, and its compose editor
// silently strips any non-rich_text top-level block when the user opens
// the draft to edit - so section/divider/header/context alongside
// rich_text is still data loss.
func validateBlocksShape(raw []byte) error {
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return &output.Error{
			Err:    "invalid_blocks",
			Detail: "blocks input is not a JSON array of objects: " + err.Error(),
			Code:   output.ExitGeneral,
		}
	}
	if len(arr) == 0 {
		return &output.Error{Err: "invalid_blocks", Detail: "blocks array is empty", Code: output.ExitGeneral}
	}
	hasRichText := false
	// Track the last recognized container type seen across the flattened
	// element stream. Desktop merges adjacent top-level rich_text blocks
	// before rendering, so absorption reaches across block boundaries.
	// Unrecognized/untyped elements don't reset this - they'd be invisible
	// to Desktop's flattener too.
	var prevElem string
	var prevBlock, prevIdx int
	for i, block := range arr {
		t, ok := block["type"].(string)
		if !ok || t == "" {
			return &output.Error{
				Err:    "invalid_blocks",
				Detail: fmt.Sprintf("blocks[%d] is missing a string \"type\" field", i),
				Code:   output.ExitGeneral,
			}
		}
		if t != "rich_text" {
			return &output.Error{
				Err:    "invalid_blocks",
				Detail: fmt.Sprintf("blocks[%d] is %q; drafts must contain only rich_text top-level blocks. Slack Desktop's Drafts compose editor strips non-rich_text blocks when the user opens the draft. See the skill docs for the rich_text shape.", i, t),
				Code:   output.ExitGeneral,
			}
		}
		elems, _ := block["elements"].([]any)
		if len(elems) > 0 {
			hasRichText = true
		}
		for j, elem := range elems {
			em, ok := elem.(map[string]any)
			if !ok {
				continue
			}
			et, _ := em["type"].(string)
			if et == "" {
				continue
			}
			if et == "rich_text_list" && prevElem == "rich_text_section" {
				return &output.Error{
					Err:    "invalid_blocks",
					Detail: fmt.Sprintf("blocks[%d].elements[%d] is a rich_text_list directly after the rich_text_section at blocks[%d].elements[%d]; Slack flattens adjacent top-level rich_text blocks and absorbs the preceding section into the first bullet. Insert a rich_text_quote or rich_text_preformatted between them, or collapse into a single rich_text_section with inline \"\\n\" and literal \"•\" markers. See the skill docs' Layout quirks section.", i, j, prevBlock, prevIdx),
					Code:   output.ExitGeneral,
				}
			}
			prevElem, prevBlock, prevIdx = et, i, j
		}
	}
	if !hasRichText {
		return &output.Error{
			Err:    "invalid_blocks",
			Detail: "drafts must include at least one rich_text block with non-empty elements. Slack Desktop tombstones drafts without rich_text content. See the skill docs for the rich_text shape.",
			Code:   output.ExitGeneral,
		}
	}
	return nil
}

// parseScheduledTime parses a date string into Unix seconds.
// Accepts RFC 3339, "YYYY-MM-DDTHH:MM:SS", or "YYYY-MM-DD".
func parseScheduledTime(s string) (int64, error) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, fmt.Errorf("unparseable time: %s", s)
}

func formatDraft(d draftData) map[string]any {
	m := map[string]any{
		"id":               d.ID,
		"client_msg_id":    d.ClientMsgID,
		"destinations":     d.Destinations,
		"date_created":     d.DateCreated,
		"date_created_iso": time.Unix(d.DateCreated, 0).UTC().Format(time.RFC3339),
		"date_scheduled":   d.DateScheduled,
		"last_updated_ts":  d.LastUpdatedTS,
		"blocks":           d.Blocks,
	}
	if d.DateScheduled > 0 {
		m["date_scheduled_iso"] = time.Unix(d.DateScheduled, 0).UTC().Format(time.RFC3339)
	}
	return m
}
