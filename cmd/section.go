package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type SectionCmd struct {
	List     SectionListCmd     `cmd:"" help:"List sidebar sections."`
	Channels SectionChannelsCmd `cmd:"" help:"List channels in a section. A channel Slack cannot show keeps its row with enrich_error and no name (exit 1)."`
	Find     SectionFindCmd     `cmd:"" help:"Find sectioned channels by name. A channel whose name cannot be read is emitted with enrich_error whatever the pattern (exit 1)."`
	Move     SectionMoveCmd     `cmd:"" help:"Move channels to a section."`
	Create   SectionCreateCmd   `cmd:"" help:"Create a sidebar section."`
	Rename   SectionRenameCmd   `cmd:"" help:"Rename a sidebar section, keeping its ID and emoji."`
	Emoji    SectionEmojiCmd    `cmd:"" help:"Set a sidebar section's emoji, keeping its ID and name."`
}

// sectionData is the parsed response from users.channelSections.list.
type sectionData struct {
	ID         string   `json:"channel_section_id"`
	Name       string   `json:"name"`
	Emoji      string   `json:"emoji"`
	Type       string   `json:"type"`
	ChannelIDs struct {
		IDs []string `json:"channel_ids"`
	} `json:"channel_ids_page"`
}

type sectionsListResponse struct {
	Sections []sectionData `json:"channel_sections"`
}

// fetchSections calls users.channelSections.list and returns parsed sections.
func fetchSections(ctx context.Context, client *api.Client) ([]sectionData, error) {
	data, err := client.RetryInternalForm(ctx, "users.channelSections.list", nil)
	if err != nil {
		return nil, err
	}
	var resp sectionsListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return resp.Sections, nil
}

// channelOutcome is one channel's conversations.info lookup, held at the
// channel's input position. resolved is tracked on its own rather than
// inferred from a non-empty name: a DM resolves with no name at all.
type channelOutcome struct {
	resolved   bool
	name       string
	isArchived bool
	errCode    string // stable code when this one channel could not be read
}

// itemLocalChannelInfoErrors are the conversations.info errors that describe
// one channel - it is gone, or this token cannot see it - rather than the run.
// They mark their own row and let the others through.
//
// Applied fail-closed, like itemLocalEnrichErrors: anything absent here,
// including a code Slack has not invented yet, fails the command. Every error
// used to be dropped, so a rate limit and a deleted channel both printed the
// channel ID as its name.
var itemLocalChannelInfoErrors = map[string]bool{
	"channel_not_found": true,
}

const sectionResolveConcurrency = 15

// lookupSectionChannels reads conversations.info for every id, bounded to
// sectionResolveConcurrency in flight, and returns outcomes indexed by input
// position. Each lookup goes through api.Call, so a rate limit is retried
// with Retry-After before it counts as a failure.
//
// A non-nil error voids the whole read - the caller must emit no rows. It is
// returned unclassified.
func lookupSectionChannels(ctx context.Context, cli *CLI, client *api.Client, ids []string) ([]channelOutcome, error) {
	outcomes := make([]channelOutcome, len(ids))

	err := runBounded(ctx, len(ids), sectionResolveConcurrency, func(ctx context.Context, i int) error {
		var ch *slack.Channel
		err := api.Call(ctx, "conversations.info", func() error {
			var err error
			ch, err = client.Bot().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
				ChannelID: ids[i],
			})
			return err
		})
		if err != nil {
			if code := cli.ClassifyError(err).Err; itemLocalChannelInfoErrors[code] {
				outcomes[i] = channelOutcome{errCode: code}
				return nil
			}
			return err
		}
		if ch == nil {
			return fmt.Errorf("conversations.info returned no channel for %s", ids[i])
		}
		outcomes[i] = channelOutcome{resolved: true, name: ch.Name, isArchived: ch.IsArchived}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Fail closed on a channel with neither a result nor a marker, which
	// would print as a blank row at exit 0. Unreachable today.
	for i := range outcomes {
		if !outcomes[i].resolved && outcomes[i].errCode == "" {
			return nil, fmt.Errorf("no conversations.info outcome for %s", ids[i])
		}
	}
	return outcomes, nil
}

// sectionChannelRow is the row section channels and section find share. A
// channel that could not be read carries enrich_error and neither name nor
// is_archived: nothing is known about it but its ID. A conversation that
// resolved without a name (a DM) keeps name as the empty string.
func sectionChannelRow(id string, o channelOutcome) map[string]any {
	if o.errCode != "" {
		return map[string]any{"id": id, "enrich_error": o.errCode}
	}
	return map[string]any{"id": id, "name": o.name, "is_archived": o.isArchived}
}

// finishSectionRows prints the trailer for a channels or find run and turns
// marked rows into exit 1. The rows are already on stdout, so the failure is
// an *output.ExitError: the markers are the report.
func finishSectionRows(p *output.Printer, unresolved int) error {
	if err := p.PrintMeta(output.Meta{ErrorCount: unresolved}); err != nil {
		return err
	}
	if unresolved > 0 {
		return &output.ExitError{Code: output.ExitGeneral}
	}
	return nil
}

// --- section list ---

type SectionListCmd struct{}

func (c *SectionListCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	sections, err := fetchSections(ctx, client)
	if err != nil {
		return cli.ClassifyError(err)
	}

	for _, s := range sections {
		if err := p.PrintItem(sectionRow(s)); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}

// --- section channels ---

type SectionChannelsCmd struct {
	SectionID string `arg:"" required:"" help:"Section ID."`
}

func (c *SectionChannelsCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	sections, err := fetchSections(ctx, client)
	if err != nil {
		return cli.ClassifyError(err)
	}

	var channelIDs []string
	for _, s := range sections {
		if s.ID == c.SectionID {
			channelIDs = s.ChannelIDs.IDs
			break
		}
	}

	if channelIDs == nil {
		return output.SectionNotFound(c.SectionID)
	}

	// Every lookup finishes before the first row prints, so a failed read
	// emits no rows at all rather than a prefix that looks complete.
	outcomes, err := lookupSectionChannels(ctx, cli, client, channelIDs)
	if err != nil {
		return abortWithTrailer(p, cli.ClassifyError(err), 0)
	}

	unresolved := 0
	for i, id := range channelIDs {
		if outcomes[i].errCode != "" {
			unresolved++
		}
		if err := p.PrintItem(sectionChannelRow(id, outcomes[i])); err != nil {
			return err
		}
	}

	return finishSectionRows(p, unresolved)
}

// --- section find ---

type SectionFindCmd struct {
	Pattern string `arg:"" required:"" help:"Name substring to search for (case-insensitive)."`
}

func (c *SectionFindCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	sections, err := fetchSections(ctx, client)
	if err != nil {
		return cli.ClassifyError(err)
	}

	// Build channel -> section map and collect all channel IDs.
	type sectionRef struct {
		SectionID   string
		SectionName string
	}
	channelToSection := make(map[string]sectionRef)
	var allIDs []string
	for _, s := range sections {
		for _, id := range s.ChannelIDs.IDs {
			channelToSection[id] = sectionRef{SectionID: s.ID, SectionName: s.Name}
			allIDs = append(allIDs, id)
		}
	}

	outcomes, err := lookupSectionChannels(ctx, cli, client, allIDs)
	if err != nil {
		return abortWithTrailer(p, cli.ClassifyError(err), 0)
	}

	pattern := strings.ToLower(c.Pattern)
	unresolved := 0
	for i, id := range allIDs {
		o := outcomes[i]
		// The match is on the name. A channel whose name could not be read
		// cannot be ruled out, so it is emitted with its marker whatever the
		// pattern, rather than dropped as a non-match.
		if o.errCode != "" {
			unresolved++
		} else if !strings.Contains(strings.ToLower(o.name), pattern) {
			continue
		}
		ref := channelToSection[id]
		row := sectionChannelRow(id, o)
		row["section_name"] = ref.SectionName
		row["section_id"] = ref.SectionID
		if err := p.PrintItem(row); err != nil {
			return err
		}
	}

	return finishSectionRows(p, unresolved)
}

// sectionRow is the JSONL shape shared by section list, rename, and emoji.
func sectionRow(s sectionData) map[string]any {
	return map[string]any{
		"id":            s.ID,
		"name":          s.Name,
		"emoji":         s.Emoji,
		"type":          s.Type,
		"channel_count": len(s.ChannelIDs.IDs),
	}
}

// findSection returns the section with the given id, or nil.
func findSection(sections []sectionData, id string) *sectionData {
	for i := range sections {
		if sections[i].ID == id {
			return &sections[i]
		}
	}
	return nil
}

// --- section create ---

type SectionCreateCmd struct {
	Name  string `arg:"" required:"" help:"Section name."`
	Emoji string `help:"Emoji name as 'section list' shows it (heart, wave::skin-tone-2, a custom emoji). One wrapping pair of colons is stripped; anything Slack rejects is emoji_invalid."`
}

func (c *SectionCreateCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	id, emoji, err := createSection(ctx, client, c.Name, c.Emoji)
	if err != nil {
		return cli.ClassifyError(err)
	}

	if err := p.PrintItem(map[string]any{
		"id":    id,
		"name":  c.Name,
		"emoji": emoji,
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// createSection calls users.channelSections.create and returns the new id
// and the emoji as sent (after normalizeEmojiArg).
func createSection(ctx context.Context, client *api.Client, name, emoji string) (string, string, error) {
	emoji = normalizeEmojiArg(emoji)
	// Slack rejects the call with invalid_arguments when the emoji key is
	// absent (verified live 2026-10-16); an empty value is accepted.
	data, err := client.PostInternalForm(ctx, "users.channelSections.create", map[string]string{
		"name":  name,
		"emoji": emoji,
	})
	if err != nil {
		return "", "", err
	}
	var resp struct {
		SectionID string `json:"channel_section_id"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", err
	}
	return resp.SectionID, emoji, nil
}

// normalizeEmojiArg turns the way people type an emoji (":heart:",
// ":wave::skin-tone-2:") into the bare name Slack's section endpoints accept
// ("heart", "wave::skin-tone-2"). Verified live 2026-10-16: the colon-wrapped
// form is emoji_invalid on both create and set, and the bare form is stored
// verbatim with no alias normalization. Exactly one wrapping pair is removed,
// and only when at least one character sits between them - ":" and "::" go
// through verbatim so a malformed value gets Slack's emoji_invalid instead
// of silently clearing the emoji. Case, whitespace, unknown names, and
// unicode are untouched; the server is the validator. Applied once per
// user-supplied value: inside createSection (shared by create and move) and
// in SectionEmojiCmd.Run - never to a value read back from the server.
func normalizeEmojiArg(s string) string {
	if len(s) >= 3 && s[0] == ':' && s[len(s)-1] == ':' {
		return s[1 : len(s)-1]
	}
	return s
}

// sectionMismatchError reports that users.channelSections.set answered ok
// but the re-fetched section does not hold the requested state. after is nil
// when the section is gone.
type sectionMismatchError struct {
	after *sectionData
}

func (e *sectionMismatchError) Error() string { return e.detail() }

func (e *sectionMismatchError) detail() string {
	if e.after == nil {
		return "section missing after users.channelSections.set"
	}
	return "section did not match the requested state after users.channelSections.set (name=" + strconv.Quote(e.after.Name) + ", emoji=" + strconv.Quote(e.after.Emoji) + ")"
}

// setSection writes name and emoji to a section via users.channelSections.set
// and returns the section as Slack holds it afterwards. The endpoint clears
// any key omitted from the form, so both fields are always sent; it also
// returns a bare {"ok":true} regardless, so the re-fetch is the only evidence
// the write took. A mismatch is a *sectionMismatchError; callers map it to
// their own code. Values are sent verbatim - rename passes an emoji read
// back from the server, so user input is normalized by the caller - and the
// returned row is the re-fetched data, never the request.
func setSection(ctx context.Context, client *api.Client, id, name, emoji string) (*sectionData, error) {
	if _, err := client.RetryInternalForm(ctx, "users.channelSections.set", map[string]string{
		"channel_section_id": id,
		"name":               name,
		"emoji":              emoji,
	}); err != nil {
		return nil, err
	}

	after, err := fetchSections(ctx, client)
	if err != nil {
		return nil, err
	}
	updated := findSection(after, id)
	if updated == nil || updated.Name != name || updated.Emoji != emoji {
		return nil, &sectionMismatchError{after: updated}
	}
	return updated, nil
}

// classifySetError maps a setSection failure: a mismatch becomes the
// caller's own code (rename_failed, emoji_failed), anything else is the
// usual Slack/network classification.
func (cli *CLI) classifySetError(err error, code, input string) error {
	var mm *sectionMismatchError
	if errors.As(err, &mm) {
		return &output.Error{Err: code, Detail: mm.detail(), Input: input, Code: output.ExitGeneral}
	}
	return cli.ClassifyError(err)
}

// --- section rename ---

type SectionRenameCmd struct {
	ID   string `arg:"" required:"" help:"Section ID."`
	Name string `arg:"" required:"" help:"New section name."`
}

func (c *SectionRenameCmd) Run(cli *CLI) error {
	// Slack stores an empty name as "" and answers ok, so reject locally
	// before any API call. The original (untrimmed) name is what gets sent.
	if strings.TrimSpace(c.Name) == "" {
		return &output.Error{Err: "invalid_input", Detail: "section name must not be blank", Input: c.Name, Code: output.ExitGeneral}
	}

	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	sections, err := fetchSections(ctx, client)
	if err != nil {
		return cli.ClassifyError(err)
	}
	current := findSection(sections, c.ID)
	if current == nil {
		return output.SectionNotFound(c.ID)
	}

	// The current emoji rides along with the new name (omitted keys clear).
	updated, err := setSection(ctx, client, c.ID, c.Name, current.Emoji)
	if err != nil {
		return cli.classifySetError(err, "rename_failed", c.ID)
	}

	if err := p.PrintItem(sectionRow(*updated)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// --- section emoji ---

type SectionEmojiCmd struct {
	ID    string `arg:"" required:"" help:"Section ID."`
	Emoji string `arg:"" required:"" help:"Emoji name as 'section list' shows it (heart, wave::skin-tone-2, a custom emoji). One wrapping pair of colons is stripped; anything Slack rejects is emoji_invalid. An empty value (\"\") clears the emoji."`
}

func (c *SectionEmojiCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	sections, err := fetchSections(ctx, client)
	if err != nil {
		return cli.ClassifyError(err)
	}
	current := findSection(sections, c.ID)
	if current == nil {
		return output.SectionNotFound(c.ID)
	}

	// The current name rides along with the new emoji (omitted keys clear).
	updated, err := setSection(ctx, client, c.ID, current.Name, normalizeEmojiArg(c.Emoji))
	if err != nil {
		return cli.classifySetError(err, "emoji_failed", c.ID)
	}

	if err := p.PrintItem(sectionRow(*updated)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// --- section move ---

type SectionMoveCmd struct {
	Channels   string `required:"" help:"Comma-separated channel IDs to move."`
	Section    string `help:"Target section ID."`
	NewSection string `help:"Create a new section with this name and move channels to it."`
	// Emoji is a pointer so --emoji "" is distinguishable from no flag: the
	// flag belongs to the section --new-section creates, and even an empty
	// value is invalid_input alongside --section.
	Emoji *string `help:"Emoji for the section --new-section creates. Emoji name as 'section list' shows it (heart, wave::skin-tone-2, a custom emoji). One wrapping pair of colons is stripped; anything Slack rejects is emoji_invalid."`
}

func (c *SectionMoveCmd) Run(cli *CLI) error {
	if c.Section == "" && c.NewSection == "" {
		return &output.Error{Err: "invalid_input", Detail: "one of --section or --new-section is required", Code: output.ExitGeneral}
	}
	if c.Section != "" && c.NewSection != "" {
		return &output.Error{Err: "invalid_input", Detail: "--section and --new-section are mutually exclusive", Code: output.ExitGeneral}
	}
	if c.Emoji != nil && c.NewSection == "" {
		return &output.Error{Err: "invalid_input", Detail: "--emoji requires --new-section", Code: output.ExitGeneral}
	}

	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	targetSectionID := c.Section
	targetSectionName := ""

	if c.NewSection != "" {
		emoji := ""
		if c.Emoji != nil {
			emoji = *c.Emoji
		}
		id, _, err := createSection(ctx, client, c.NewSection, emoji)
		if err != nil {
			return cli.ClassifyError(err)
		}
		targetSectionID = id
		targetSectionName = c.NewSection
	}

	// Get current sections to build the update payload.
	sections, err := fetchSections(ctx, client)
	if err != nil {
		return cli.ClassifyError(err)
	}

	// Resolve and validate the target for the --section path. A typo'd id would
	// otherwise silently no-op (the same failure class as this bug). The
	// --new-section path already has both id and name from createSection.
	if c.NewSection == "" {
		target := findSection(sections, targetSectionID)
		if target == nil {
			return output.SectionNotFound(targetSectionID)
		}
		targetSectionName = target.Name
	}

	channelIDs := normalizeChannelIDs(c.Channels)
	if len(channelIDs) == 0 {
		return &output.Error{Err: "invalid_input", Detail: "no channel IDs provided", Code: output.ExitGeneral}
	}
	moveSet := make(map[string]bool, len(channelIDs))
	for _, id := range channelIDs {
		moveSet[id] = true
	}

	// The bulkUpdate endpoint takes form-encoded insert/remove operations, not
	// a full channel_ids_page replacement. Moving an already-sectioned channel
	// requires both: insert into the target AND remove from its current
	// section - insert alone is silently ignored by Slack.
	removeBySource := make(map[string][]string)
	for _, s := range sections {
		if s.ID == targetSectionID {
			continue
		}
		for _, ch := range s.ChannelIDs.IDs {
			if moveSet[ch] {
				removeBySource[s.ID] = append(removeBySource[s.ID], ch)
			}
		}
	}

	insert := []map[string]any{{
		"channel_section_id": targetSectionID,
		"channel_ids":        channelIDs,
	}}
	params := map[string]string{"insert": mustJSONString(insert)}

	if len(removeBySource) > 0 {
		sourceIDs := make([]string, 0, len(removeBySource))
		for sid := range removeBySource {
			sourceIDs = append(sourceIDs, sid)
		}
		sort.Strings(sourceIDs)
		remove := make([]map[string]any, 0, len(sourceIDs))
		for _, sid := range sourceIDs {
			remove = append(remove, map[string]any{
				"channel_section_id": sid,
				"channel_ids":        removeBySource[sid],
			})
		}
		params["remove"] = mustJSONString(remove)
	}

	if _, err = client.RetryInternalForm(ctx, "users.channelSections.channels.bulkUpdate", params); err != nil {
		return cli.ClassifyError(err)
	}

	// The endpoint returns a bare {"ok":true} even when it changes nothing, so
	// re-fetch and count only channels now in the target and nowhere else.
	after, err := fetchSections(ctx, client)
	if err != nil {
		return cli.ClassifyError(err)
	}
	moved := countMoved(after, channelIDs, targetSectionID)

	result := map[string]any{
		"moved_count":    moved,
		"target_section": targetSectionName,
	}
	if c.NewSection != "" {
		result["target_section_id"] = targetSectionID
	}
	if err := p.PrintItem(result); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// normalizeChannelIDs splits a comma-separated list, trims each entry, drops
// empties, and dedupes while preserving first-seen order.
func normalizeChannelIDs(s string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, part := range strings.Split(s, ",") {
		id := strings.TrimSpace(part)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// countMoved reports how many of the given channels are now in the target
// section and in no other section - the only state that counts as a
// successful move.
func countMoved(sections []sectionData, channelIDs []string, targetSectionID string) int {
	moved := 0
	for _, id := range channelIDs {
		inTarget, elsewhere := false, false
		for _, s := range sections {
			for _, ch := range s.ChannelIDs.IDs {
				if ch != id {
					continue
				}
				if s.ID == targetSectionID {
					inTarget = true
				} else {
					elsewhere = true
				}
			}
		}
		if inTarget && !elsewhere {
			moved++
		}
	}
	return moved
}

// mustJSONString marshals v to a JSON string. The inputs here are plain
// maps/slices of strings, which never fail to marshal.
func mustJSONString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
