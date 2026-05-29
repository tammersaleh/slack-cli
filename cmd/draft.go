package cmd

import (
	"bytes"
	"context"
	"encoding/csv"
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

// skillHint is the recovery pointer for draft block/attachment validation
// rejections.
// Agents read it from the JSON error on stderr; humans see it via --help
// text. Points at the install command rather than a doc URL because the
// SKILL.md is meant to live on the agent host, not be fetched ad-hoc.
const skillHint = "For the rich_text shape, load the slack-cli skill (install: 'skills add tammersaleh/slack-cli -g -y')."

type DraftListCmd struct {
	Active         bool `help:"Exclude scheduled drafts (active only)."`
	Limit          int  `help:"Page size." default:"50"`
	IncludeSent    bool `help:"Include drafts already sent (suppressed by default)."`
	IncludeDeleted bool `help:"Include drafts marked deleted server-side (suppressed by default)."`
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
	Attachments         json.RawMessage `json:"attachments,omitempty"`
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
	Table         string `help:"Build a table from a CSV/TSV file and attach it (first row bold)."`
	NoHeader      bool   `help:"With --table, treat the first row as data, not a bold header."`
}

func (DraftCreateCmd) Help() string {
	return `Pipe Block Kit JSON on stdin: either a bare array of top-level
blocks, or an object {"blocks":[...],"attachments":[...]}.

Top-level blocks must be rich_text - Slack's draft compose editor strips
section / divider / header / context and bare table blocks when the user
opens the draft. A table goes in an attachment instead: put a "table"
block in attachments[].blocks[], or use --table to build one from CSV/TSV.

Minimal invocation:

  echo '[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello"}]}]}]' \
    | slack draft create '#general'

With a table (the table block lives in an attachment):

  slack draft create '#general' < payload.json   # {"blocks":[...],"attachments":[{"blocks":[{"type":"table",...}]}]}
  slack draft create '#general' --table report.tsv   # build the table from a file (first row bold; --no-header to disable)

Thread reply / scheduled send:

  slack draft create '#general' --thread 1713299000.123456 < blocks.json
  slack draft create '#general' --at 2026-04-20T09:00:00-07:00 < blocks.json

For the full Block Kit shape, load the slack-cli skill
(install: 'skills add tammersaleh/slack-cli -g -y').`
}

func (c *DraftCreateCmd) Run(cli *CLI) error {
	if c.Broadcast && c.Thread == "" {
		return &output.Error{Err: "invalid_input", Detail: "--broadcast requires --thread", Code: output.ExitGeneral}
	}
	if c.At != "" && c.DateScheduled > 0 {
		return &output.Error{Err: "invalid_input", Detail: "--at and --date-scheduled are mutually exclusive", Code: output.ExitGeneral}
	}

	payload, err := readDraftPayload(cli)
	if err != nil {
		return err
	}
	if payload, err = applyTableFlag(payload, c.Table, !c.NoHeader); err != nil {
		return err
	}
	if payload.empty() {
		return output.MissingBlocks()
	}
	if !payload.hasRenderableContent() {
		return errNoRenderableContent()
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
		return channelResolveError(c.Channel, err)
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
		"blocks":           payload.blocksOrEmpty(),
		"destinations":     string(destsJSON),
		"client_msg_id":    uuid.NewString(),
		"file_ids":         "[]",
		"is_from_composer": "true",
	}
	if payload.hasAttachments {
		params["attachments"] = payload.attachments
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
	Table         string `help:"Replace attachments with a table built from a CSV/TSV file (first row bold)."`
	NoHeader      bool   `help:"With --table, treat the first row as data, not a bold header."`
}

func (DraftUpdateCmd) Help() string {
	return `Update a draft's content and/or reschedule it. Optional Block Kit
JSON on stdin: a bare blocks array, or an object {"blocks":...,
"attachments":...}. Each field is independent - a provided field replaces,
an absent one is preserved; {"attachments":[]} clears attachments. --table
replaces the attachments with a table built from a CSV/TSV file. Same
rich_text-only rule for top-level blocks as draft create.

If Slack Desktop has already tombstoned the draft (is_deleted=true), the
CLI transparently creates a replacement at the same destination - carrying
its blocks and attachments - and emits the new draft ID to stdout.

Examples:

  slack draft update Dr01234 < blocks.json           # replace blocks
  echo '{"attachments":[{"blocks":[{"type":"table","rows":[...]}]}]}' | slack draft update Dr01234
  slack draft update Dr01234 --table report.tsv      # replace attachments with a table
  slack draft update Dr01234 --at 2026-04-22         # reschedule only
  slack draft update Dr01234 --clear-schedule        # drop scheduled send`
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

	// stdin payload (blocks and/or attachments); absent fields keep their
	// existing values.
	payload, err := readDraftPayload(cli)
	if err != nil {
		return err
	}
	if payload, err = applyTableFlag(payload, c.Table, !c.NoHeader); err != nil {
		return err
	}
	if payload.empty() && scheduledFlags == 0 {
		return &output.Error{Err: "invalid_input", Detail: "No update specified: pipe Block Kit JSON (blocks and/or attachments) on stdin, pass --table, or pass --at / --date-scheduled / --clear-schedule", Code: output.ExitGeneral}
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

	intent, err := c.buildIntent(existing, payload)
	if err != nil {
		return err
	}

	// Skip the doomed update when we already know the draft is tombstoned.
	// Desktop's Drafts-panel reconciliation tombstones any server draft
	// whose blocks lack a rich_text body (see validateBlockShapes), and
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
	if intent.attachments != "" {
		params["attachments"] = intent.attachments
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
	attachments  string // JSON-encoded attachments array; "" omits the param
	destinations string // JSON-encoded destinations array (normalized)
	fileIDs      string // JSON-encoded file_ids array
	scheduled    int64
}

func (c *DraftUpdateCmd) buildIntent(existing *draftData, payload draftPayload) (*draftWriteIntent, error) {
	// Tri-state per field: a provided field replaces, an absent one preserves
	// the existing value. Provided fields were shape-checked at parse time.
	blocksJSON := string(existing.Blocks)
	if payload.hasBlocks {
		blocksJSON = payload.blocks
	}
	if blocksJSON == "" || blocksJSON == "null" {
		blocksJSON = "[]"
	}

	attachmentsJSON := string(existing.Attachments)
	if payload.hasAttachments {
		attachmentsJSON = payload.attachments
	}
	// Don't echo a server-sent null back as the attachments param; treat it
	// as "none" so it's omitted (preserve-nothing) rather than sent.
	if attachmentsJSON == "null" {
		attachmentsJSON = ""
	}

	// Renderable-content rule on the EFFECTIVE draft. Re-running
	// validateBlockShapes also guards a schedule-only update that would
	// round-trip existing blocks unable to survive reconciliation. Existing
	// attachments are detected leniently (round-tripped verbatim, not
	// re-validated against the supported subset).
	hasBody, err := validateBlockShapes([]byte(blocksJSON))
	if err != nil {
		return nil, err
	}
	if !hasBody && !attachmentsContainTable([]byte(attachmentsJSON)) {
		return nil, errNoRenderableContent()
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
		attachments:  attachmentsJSON,
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
		"file_ids":         "[]", // uploaded files don't carry across to a replacement
		"is_from_composer": "true",
	}
	// Block attachments (e.g. a table) DO carry across - dropping them here
	// would silently lose the table on the very path meant to preserve drafts.
	if intent.attachments != "" {
		params["attachments"] = intent.attachments
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

// draftPayload is the parsed stdin payload for a draft write. stdin may be a
// bare JSON array (the legacy "just blocks" form) or an object
// {"blocks":[...],"attachments":[...]}. Tables live in attachments[].blocks[],
// never top-level blocks (the compose editor strips them there). Presence is
// tracked per field so `draft update` can tell "replace this" from "leave it
// alone".
type draftPayload struct {
	blocks         string // JSON array; meaningful only when hasBlocks
	hasBlocks      bool
	attachments    string // JSON array; meaningful only when hasAttachments
	hasAttachments bool
}

func (p draftPayload) empty() bool { return !p.hasBlocks && !p.hasAttachments }

// blocksOrEmpty returns the blocks JSON to send, defaulting to an empty array
// when only attachments were provided (an attachments-only table draft).
func (p draftPayload) blocksOrEmpty() string {
	if p.hasBlocks {
		return p.blocks
	}
	return "[]"
}

// hasRenderableContent reports whether a freshly-provided payload carries a
// non-empty rich_text body or a table attachment. A draft with neither gets
// tombstoned by Slack's reconciliation pass. Inputs are already shape-valid.
func (p draftPayload) hasRenderableContent() bool {
	if p.hasBlocks {
		if has, _ := validateBlockShapes([]byte(p.blocks)); has {
			return true
		}
	}
	return p.hasAttachments && attachmentsContainTable([]byte(p.attachments))
}

// errNoRenderableContent is the shared "draft would tombstone" rejection.
func errNoRenderableContent() error {
	return &output.Error{
		Err:    "invalid_blocks",
		Detail: "a draft needs a non-empty rich_text block or a table attachment - Slack tombstones drafts with no renderable content.",
		Hint:   skillHint,
		Code:   output.ExitGeneral,
	}
}

// applyTableFlag folds a --table CSV/TSV file into the payload as a table
// attachment. It is mutually exclusive with stdin-provided attachments to
// avoid silent merge/ordering questions.
func applyTableFlag(p draftPayload, file string, header bool) (draftPayload, error) {
	if file == "" {
		return p, nil
	}
	if p.hasAttachments {
		return p, &output.Error{Err: "invalid_input", Detail: "--table and stdin attachments are mutually exclusive; provide one or the other", Code: output.ExitGeneral}
	}
	att, err := buildTableAttachment(file, header)
	if err != nil {
		return p, err
	}
	p.attachments, p.hasAttachments = att, true
	return p, nil
}

// buildTableAttachment reads a CSV/TSV file and renders it as a single table
// block wrapped in an attachment. The delimiter is auto-detected (tab vs
// comma) from the first line. With header, the first row's cells are bold
// rich_text; all other cells are plain raw_text - matching what Slack's own
// composer produces when you paste a table.
func buildTableAttachment(path string, header bool) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", &output.Error{Err: "invalid_input", Detail: "reading --table file: " + err.Error(), Code: output.ExitGeneral}
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // strip UTF-8 BOM
	delim := ','
	firstLine, _, _ := bytes.Cut(data, []byte{'\n'})
	if bytes.IndexByte(firstLine, '\t') >= 0 {
		delim = '\t'
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delim
	r.FieldsPerRecord = -1 // tolerate ragged rows
	records, err := r.ReadAll()
	if err != nil {
		return "", &output.Error{Err: "invalid_input", Detail: "parsing --table file: " + err.Error(), Code: output.ExitGeneral}
	}
	if len(records) == 0 {
		return "", &output.Error{Err: "invalid_input", Detail: "--table file has no rows", Code: output.ExitGeneral}
	}
	rows := make([][]map[string]any, len(records))
	for i, rec := range records {
		cells := make([]map[string]any, len(rec))
		for j, field := range rec {
			if header && i == 0 {
				cells[j] = boldTableCell(field)
			} else {
				cells[j] = plainTableCell(field)
			}
		}
		rows[i] = cells
	}
	table := map[string]any{"type": "table", "rows": rows}
	att := []map[string]any{{"blocks": []map[string]any{table}, "fallback": "[table]"}}
	b, err := json.Marshal(att)
	if err != nil {
		return "", &output.Error{Err: "encode_error", Detail: err.Error(), Code: output.ExitGeneral}
	}
	return string(b), nil
}

// plainTableCell is a raw_text table cell.
func plainTableCell(s string) map[string]any {
	return map[string]any{"type": "raw_text", "text": s}
}

// boldTableCell is a rich_text table cell with bold text, for header rows.
func boldTableCell(s string) map[string]any {
	return map[string]any{"type": "rich_text", "elements": []map[string]any{
		{"type": "rich_text_section", "elements": []map[string]any{
			{"type": "text", "text": s, "style": map[string]any{"bold": true}},
		}},
	}}
}

// readDraftPayload reads and shape-validates the stdin payload, if any. Returns
// an empty payload when stdin is a TTY or nothing is piped. Shape validation
// runs here; the renderable-content rule is applied by the caller (create
// requires content in what's provided; update may inherit it from the existing
// draft).
func readDraftPayload(cli *CLI) (draftPayload, error) {
	// In tests, cli.in is overridden with a bytes.Reader - always try to read.
	// In production, skip the read if stdin is a terminal.
	if cli.in == nil {
		info, err := os.Stdin.Stat()
		if err != nil {
			return draftPayload{}, &output.Error{Err: "stdin_error", Detail: err.Error(), Code: output.ExitGeneral}
		}
		if (info.Mode() & os.ModeCharDevice) != 0 {
			return draftPayload{}, nil
		}
	}
	raw, err := io.ReadAll(cli.Stdin())
	if err != nil {
		return draftPayload{}, &output.Error{Err: "stdin_error", Detail: err.Error(), Code: output.ExitGeneral}
	}
	if strings.TrimSpace(string(raw)) == "" {
		return draftPayload{}, nil
	}
	return parseDraftPayload(raw)
}

// parseDraftPayload accepts either a bare blocks array or a {blocks,
// attachments} object and shape-validates whatever is present.
func parseDraftPayload(raw []byte) (draftPayload, error) {
	var p draftPayload
	switch firstJSONToken(raw) {
	case '[':
		p.blocks, p.hasBlocks = string(raw), true
	case '{':
		var obj struct {
			Blocks      *json.RawMessage `json:"blocks"`
			Attachments *json.RawMessage `json:"attachments"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return p, &output.Error{Err: "invalid_blocks", Detail: "stdin is not valid JSON: " + err.Error(), Code: output.ExitGeneral}
		}
		// A literal JSON null for either field is treated as absent (preserve
		// on update), not as a value to send.
		if obj.Blocks != nil && string(*obj.Blocks) != "null" {
			p.blocks, p.hasBlocks = string(*obj.Blocks), true
		}
		if obj.Attachments != nil && string(*obj.Attachments) != "null" {
			p.attachments, p.hasAttachments = string(*obj.Attachments), true
		}
	default:
		return p, &output.Error{
			Err:    "invalid_blocks",
			Detail: "stdin must be a JSON array of blocks or an object with \"blocks\" and/or \"attachments\". Tables go in attachments (or use --table); prose goes in top-level rich_text blocks.",
			Hint:   skillHint,
			Code:   output.ExitGeneral,
		}
	}
	if p.hasBlocks {
		if _, err := validateBlockShapes([]byte(p.blocks)); err != nil {
			return p, err
		}
	}
	if p.hasAttachments {
		if _, err := validateAttachmentBlocks([]byte(p.attachments)); err != nil {
			return p, err
		}
	}
	return p, nil
}

// firstJSONToken returns the first non-whitespace byte of raw, or 0 if none.
func firstJSONToken(raw []byte) byte {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b
		}
	}
	return 0
}

// validateBlockShapes is the local gate for a draft's top-level blocks. It
// enforces:
//
//  1. Every top-level block is rich_text. Slack Desktop's Drafts compose
//     editor silently strips every other top-level block type when the user
//     opens the draft, and table/data_table belong in attachments (the only
//     place a draft preserves them).
//
//  2. A rich_text_section directly before a rich_text_list,
//     rich_text_preformatted, or rich_text_quote must terminate its element
//     stream with a text inline whose `text` ends with "\n", or the following
//     container absorbs it. The check spans top-level rich_text boundaries
//     because Desktop flattens adjacent rich_text blocks before rendering.
//
// It returns whether a non-empty rich_text body is present so the caller can
// apply the renderable-content rule (a draft needs a body or a table
// attachment, else reconciliation tombstones it).
func validateBlockShapes(raw []byte) (hasRichTextBody bool, err error) {
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return false, &output.Error{
			Err:    "invalid_blocks",
			Detail: "blocks is not a JSON array of objects: " + err.Error(),
			Code:   output.ExitGeneral,
		}
	}
	// Track the last recognized container type seen across the flattened
	// element stream. Desktop merges adjacent top-level rich_text blocks
	// before rendering, so absorption reaches across block boundaries.
	var prevElem string
	var prevBlock, prevIdx int
	var prevSection map[string]any
	for i, block := range arr {
		t, ok := block["type"].(string)
		if !ok || t == "" {
			return false, &output.Error{
				Err:    "invalid_blocks",
				Detail: fmt.Sprintf("blocks[%d] is missing a string \"type\" field", i),
				Code:   output.ExitGeneral,
			}
		}
		if t != "rich_text" {
			// table/data_table are uniquely seductive: unlike section/divider,
			// drafts.create returns ok and stores them verbatim, so they feel
			// supported as top-level blocks. They are not - the compose editor
			// strips them on open. A `table` IS preserved in
			// attachments[].blocks[] (verified end-to-end 2026-05-29, see
			// docs/draft-messages.md; data_table is the same block class), so
			// steer the caller there rather than at the generic message.
			if t == "table" || t == "data_table" {
				return false, &output.Error{
					Err:    "invalid_blocks",
					Detail: fmt.Sprintf("blocks[%d] is a %q block. A table in top-level blocks is stripped by Slack's draft compose editor. Put the table in an attachment instead - pipe {\"blocks\":[...],\"attachments\":[{\"blocks\":[<table>]}]} - or use --table to build one from CSV/TSV.", i, t),
					Hint:   skillHint,
					Code:   output.ExitGeneral,
				}
			}
			return false, &output.Error{
				Err:    "invalid_blocks",
				Detail: fmt.Sprintf("blocks[%d] is %q; drafts must contain only rich_text top-level blocks. Slack Desktop's Drafts compose editor strips non-rich_text blocks when the user opens the draft.", i, t),
				Hint:   skillHint,
				Code:   output.ExitGeneral,
			}
		}
		elems, _ := block["elements"].([]any)
		if len(elems) > 0 {
			hasRichTextBody = true
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
			if absorbingContainerType(et) && prevElem == "rich_text_section" && !sectionEndsWithNewline(prevSection) {
				return false, &output.Error{
					Err:    "invalid_blocks",
					Detail: fmt.Sprintf("blocks[%d].elements[%d] is a %s directly after the rich_text_section at blocks[%d].elements[%d]; Slack absorbs the section into the following container (heading glues onto the first bullet / merges into the code block / glues into the quote) unless the section's element stream ends with a text inline whose text ends with \"\\n\". Append {\"type\":\"text\",\"text\":\"\\n\"} (or extend the existing trailing text element with \"\\n\") to the section's elements.", i, j, et, prevBlock, prevIdx),
					Hint:   skillHint,
					Code:   output.ExitGeneral,
				}
			}
			if et == "rich_text_section" {
				prevSection = em
			} else {
				prevSection = nil
			}
			prevElem, prevBlock, prevIdx = et, i, j
		}
	}
	return hasRichTextBody, nil
}

// validateAttachmentBlocks shape-checks CALLER-SUPPLIED attachments. Each
// attachment's blocks must be table or data_table - the only block types
// verified to survive a draft round-trip in attachments (table end-to-end;
// data_table is the same block class, API-confirmed). Other attachment shapes
// Slack itself round-trips are NOT validated here; this gate only constrains
// what the caller hands us. Returns whether a table is present for the
// renderable-content rule.
func validateAttachmentBlocks(raw []byte) (hasTable bool, err error) {
	var arr []struct {
		Blocks []map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return false, &output.Error{
			Err:    "invalid_blocks",
			Detail: "attachments is not a JSON array of objects: " + err.Error(),
			Code:   output.ExitGeneral,
		}
	}
	for i, att := range arr {
		if len(att.Blocks) == 0 {
			return false, &output.Error{
				Err:    "invalid_blocks",
				Detail: fmt.Sprintf("attachments[%d] has no blocks; a draft attachment must hold a table / data_table block. Put prose in top-level rich_text blocks; only the table belongs in the attachment.", i),
				Hint:   skillHint,
				Code:   output.ExitGeneral,
			}
		}
		for j, block := range att.Blocks {
			t, _ := block["type"].(string)
			if t != "table" && t != "data_table" {
				return false, &output.Error{
					Err:    "invalid_blocks",
					Detail: fmt.Sprintf("attachments[%d].blocks[%d] is %q; draft attachments support only table / data_table blocks. Put prose in top-level rich_text blocks; only the table belongs in the attachment.", i, j, t),
					Hint:   skillHint,
					Code:   output.ExitGeneral,
				}
			}
			hasTable = true
		}
	}
	return hasTable, nil
}

// attachmentsContainTable reports whether an attachments array carries a
// table/data_table block. Unlike validateAttachmentBlocks it does not reject
// anything - it is used to detect renderable content in EXISTING attachments
// (which we round-trip verbatim rather than re-validate).
func attachmentsContainTable(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var arr []struct {
		Blocks []map[string]any `json:"blocks"`
	}
	if json.Unmarshal(raw, &arr) != nil {
		return false
	}
	for _, att := range arr {
		for _, block := range att.Blocks {
			if t, _ := block["type"].(string); t == "table" || t == "data_table" {
				return true
			}
		}
	}
	return false
}

// absorbingContainerType reports whether a rich_text element type
// absorbs a preceding rich_text_section's content when that section
// does not terminate with a newline. Confirmed by reproduction against
// Slack's drafts.create + compose editor: list, preformatted, and
// quote all exhibit the behavior.
func absorbingContainerType(t string) bool {
	switch t {
	case "rich_text_list", "rich_text_preformatted", "rich_text_quote":
		return true
	}
	return false
}

// sectionEndsWithNewline reports whether the given rich_text_section's
// element stream terminates with a text inline whose `text` ends with
// "\n". Trailing empty text inlines are ignored. Non-text trailing
// inlines (link, emoji, user, channel, broadcast) do not satisfy the
// rule by themselves - callers must append a final text inline ending
// in "\n".
func sectionEndsWithNewline(section map[string]any) bool {
	if section == nil {
		return false
	}
	elems, _ := section["elements"].([]any)
	for i := len(elems) - 1; i >= 0; i-- {
		em, ok := elems[i].(map[string]any)
		if !ok {
			return false
		}
		if t, _ := em["type"].(string); t != "text" {
			return false
		}
		text, _ := em["text"].(string)
		if text == "" {
			continue
		}
		return strings.HasSuffix(text, "\n")
	}
	return false
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
	if len(d.Attachments) > 0 {
		m["attachments"] = d.Attachments
	}
	if d.DateScheduled > 0 {
		m["date_scheduled_iso"] = time.Unix(d.DateScheduled, 0).UTC().Format(time.RFC3339)
	}
	return m
}
