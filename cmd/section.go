package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type SectionCmd struct {
	List     SectionListCmd     `cmd:"" help:"List sidebar sections."`
	Channels SectionChannelsCmd `cmd:"" help:"List channels in a section."`
	Find     SectionFindCmd     `cmd:"" help:"Find channels by name pattern."`
	Move     SectionMoveCmd     `cmd:"" help:"Move channels to a section."`
	Create   SectionCreateCmd   `cmd:"" help:"Create a sidebar section."`
}

// sectionData is the parsed response from users.channelSections.list.
type sectionData struct {
	ID         string   `json:"channel_section_id"`
	Name       string   `json:"name"`
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
	data, err := client.PostInternalForm(ctx, "users.channelSections.list", nil)
	if err != nil {
		return nil, err
	}
	var resp sectionsListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return resp.Sections, nil
}

// channelInfo holds resolved channel metadata.
type channelInfo struct {
	ID         string
	Name       string
	IsArchived bool
}

const sectionResolveConcurrency = 15

// resolveChannelNames concurrently resolves channel IDs to names.
func resolveChannelNames(ctx context.Context, client *api.Client, ids []string) map[string]channelInfo {
	result := make(map[string]channelInfo, len(ids))
	var mu sync.Mutex
	sem := make(chan struct{}, sectionResolveConcurrency)
	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			ch, err := client.Bot().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
				ChannelID: id,
			})
			if err != nil || ch == nil {
				return
			}

			mu.Lock()
			result[id] = channelInfo{ID: id, Name: ch.Name, IsArchived: ch.IsArchived}
			mu.Unlock()
		}(id)
	}

	wg.Wait()
	return result
}

// --- section list ---

type SectionListCmd struct{}

func (c *SectionListCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()

	sections, err := fetchSections(ctx, client)
	if err != nil {
		return cli.ClassifyError(err)
	}

	for _, s := range sections {
		if err := p.PrintItem(map[string]any{
			"id":            s.ID,
			"name":          s.Name,
			"type":          s.Type,
			"channel_count": len(s.ChannelIDs.IDs),
		}); err != nil {
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
	ctx := context.Background()

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
		return &output.Error{Err: "section_not_found", Detail: "No section with ID '" + c.SectionID + "'", Code: output.ExitGeneral}
	}

	names := resolveChannelNames(ctx, client, channelIDs)

	for _, id := range channelIDs {
		info, ok := names[id]
		if !ok {
			info = channelInfo{ID: id, Name: id}
		}
		if err := p.PrintItem(map[string]any{
			"id":          info.ID,
			"name":        info.Name,
			"is_archived": info.IsArchived,
		}); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
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
	ctx := context.Background()

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

	names := resolveChannelNames(ctx, client, allIDs)

	pattern := strings.ToLower(c.Pattern)
	for _, id := range allIDs {
		info, ok := names[id]
		if !ok {
			continue
		}
		if !strings.Contains(strings.ToLower(info.Name), pattern) {
			continue
		}
		ref := channelToSection[id]
		if err := p.PrintItem(map[string]any{
			"id":           info.ID,
			"name":         info.Name,
			"is_archived":  info.IsArchived,
			"section_name": ref.SectionName,
			"section_id":   ref.SectionID,
		}); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}

// --- section create ---

type SectionCreateCmd struct {
	Name string `arg:"" required:"" help:"Section name."`
}

func (c *SectionCreateCmd) Run(cli *CLI) error {
	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()

	id, err := createSection(ctx, client, c.Name)
	if err != nil {
		return cli.ClassifyError(err)
	}

	if err := p.PrintItem(map[string]any{
		"id":   id,
		"name": c.Name,
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

func createSection(ctx context.Context, client *api.Client, name string) (string, error) {
	data, err := client.PostInternalForm(ctx, "users.channelSections.create", map[string]string{
		"name": name,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		SectionID string `json:"channel_section_id"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	return resp.SectionID, nil
}

// --- section move ---

type SectionMoveCmd struct {
	Channels   string `required:"" help:"Comma-separated channel IDs to move."`
	Section    string `help:"Target section ID."`
	NewSection string `help:"Create a new section with this name and move channels to it."`
}

func (c *SectionMoveCmd) Run(cli *CLI) error {
	if c.Section == "" && c.NewSection == "" {
		return &output.Error{Err: "invalid_input", Detail: "one of --section or --new-section is required", Code: output.ExitGeneral}
	}
	if c.Section != "" && c.NewSection != "" {
		return &output.Error{Err: "invalid_input", Detail: "--section and --new-section are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()

	targetSectionID := c.Section
	targetSectionName := ""

	if c.NewSection != "" {
		id, err := createSection(ctx, client, c.NewSection)
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

	if targetSectionName == "" {
		for _, s := range sections {
			if s.ID == targetSectionID {
				targetSectionName = s.Name
				break
			}
		}
	}

	channelIDs := strings.Split(c.Channels, ",")
	for i := range channelIDs {
		channelIDs[i] = strings.TrimSpace(channelIDs[i])
	}

	moveSet := make(map[string]bool, len(channelIDs))
	for _, id := range channelIDs {
		moveSet[id] = true
	}

	// Build section updates: remove moved channels from current sections,
	// add them to the target.
	updates := make([]map[string]any, 0, len(sections)+1)
	for _, s := range sections {
		filtered := make([]string, 0)
		for _, ch := range s.ChannelIDs.IDs {
			if !moveSet[ch] {
				filtered = append(filtered, ch)
			}
		}
		if s.ID == targetSectionID {
			filtered = append(filtered, channelIDs...)
		}
		updates = append(updates, map[string]any{
			"channel_section_id": s.ID,
			"channel_ids_page":   map[string]any{"channel_ids": filtered},
		})
	}

	// If target section is new and not in the existing list, add it.
	found := false
	for _, s := range sections {
		if s.ID == targetSectionID {
			found = true
			break
		}
	}
	if !found {
		updates = append(updates, map[string]any{
			"channel_section_id": targetSectionID,
			"channel_ids_page":   map[string]any{"channel_ids": channelIDs},
		})
	}

	_, err = client.PostInternal(ctx, "users.channelSections.channels.bulkUpdate", map[string]any{
		"channel_sections": updates,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	result := map[string]any{
		"moved_count":    len(channelIDs),
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
