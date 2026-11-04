package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// Slack Lists. Every command here rides the session client and form-encoded
// internal calls: files.info is read as raw JSON because slack-go's File drops
// list_metadata and list_limits, and lists.records.list is internal. On
// Enterprise Grid the org token serves all of it, so there is no two-client
// split (verified live 2026-11-04, see docs/lists-plan.md).

type ListCmd struct {
	Info    ListInfoCmd    `cmd:"" help:"Show a list's title, description, icon, and limits."`
	Schema  ListSchemaCmd  `cmd:"" help:"Show a list's columns, one row per column."`
	Records ListRecordsCmd `cmd:"" help:"Read a list's records with column values keyed by column name."`
	Find    ListFindCmd    `cmd:"" help:"Find lists by title (one row per match, never picks one)."`
}

type ListInfoCmd struct {
	List string `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
}

type ListSchemaCmd struct {
	List string `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
}

type ListRecordsCmd struct {
	List   string `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
	Limit  int    `help:"Page size (Slack rejects 1)." default:"100"`
	Cursor string `help:"Continue from previous page."`
	All    bool   `help:"Fetch all pages."`
	View   string `help:"View ID to read through (view_id)."`
	Raw    bool   `help:"Add raw_fields with the original field objects."`
}

type ListFindCmd struct {
	Title  string `arg:"" required:"" help:"List title to search for."`
	Limit  int    `help:"Page size." default:"20"`
	Cursor string `help:"Continue from previous page."`
	All    bool   `help:"Fetch all pages."`
}

// fetchListFile reads files.info for a list as raw JSON. A file that is not a
// list is a not_a_list error rather than an empty schema, since every list
// command would otherwise emit rows that look complete and hold nothing.
func fetchListFile(ctx context.Context, cli *CLI, client *api.Client, id string) (*listFile, error) {
	data, err := client.PostInternalForm(ctx, "files.info", map[string]string{"file": id})
	if err != nil {
		return nil, cli.ClassifyError(err)
	}
	var resp struct {
		File listFile `json:"file"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, &output.Error{Err: "parse_error", Detail: "Failed to parse files.info response", Code: output.ExitGeneral}
	}
	if resp.File.Metadata == nil {
		return nil, &output.Error{
			Err:    "not_a_list",
			Detail: fmt.Sprintf("%s is a %q file, not a Slack List", id, resp.File.Filetype),
			Input:  id,
			Code:   output.ExitGeneral,
		}
	}
	return &resp.File, nil
}

// listSession is what the three single-list commands share: the session
// client, the printer, the list's files.info object, and a context honoring
// --timeout. Callers must defer cancel.
type listSession struct {
	client *api.Client
	p      *output.Printer
	file   *listFile
	ctx    context.Context
	cancel context.CancelFunc
}

// openListSession resolves the list argument, builds the session client, and
// reads files.info. The URL check runs first so a wrong-kind URL costs no
// credential lookup and no API call.
func openListSession(cli *CLI, input string) (*listSession, error) {
	id, idErr := fileIDFromArg(input)
	if idErr != nil {
		return nil, idErr
	}
	client, err := cli.NewSessionClient()
	if err != nil {
		return nil, err
	}
	cli.NewResolver(client) // populate resolver for output enrichment
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()

	file, err := fetchListFile(ctx, cli, client, id)
	if err != nil {
		cancel()
		return nil, err
	}
	return &listSession{client: client, p: p, file: file, ctx: ctx, cancel: cancel}, nil
}

func (c *ListInfoCmd) Run(cli *CLI) error {
	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()

	file := s.file
	row := map[string]any{
		"id":          file.ID,
		"title":       file.Title,
		"description": file.Metadata.Description,
		"icon":        file.Metadata.Icon,
		"user":        file.User,
		"created":     file.Created,
		"updated":     file.Updated,
		"permalink":   file.Permalink,
		"limits":      file.Limits,
	}
	if err := s.p.PrintItem(row); err != nil {
		return err
	}
	return s.p.PrintMeta(output.Meta{})
}

func (c *ListSchemaCmd) Run(cli *CLI) error {
	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()

	for _, col := range s.file.Metadata.Schema {
		if err := s.p.PrintItem(schemaRow(col)); err != nil {
			return err
		}
	}
	return s.p.PrintMeta(output.Meta{})
}

// recordsListResponse is the parsed lists.records.list envelope. Records stay
// raw so renderRecord can pass metadata through untouched.
type recordsListResponse struct {
	Records          []json.RawMessage `json:"records"`
	Limits           *listLimits       `json:"limits"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// withListLimits copies the latest limits block into the trailer, so a caller
// can see that archived rows exist even though the walk never shows them.
// Applied to every trailer, failures included, since the limits came with a
// page that was read successfully.
func withListLimits(limits **listLimits) streamOption {
	return func(m *output.Meta) {
		if l := *limits; l != nil {
			rc, arc := l.RowCount, l.ArchivedRowCount
			m.RowCount = &rc
			m.ArchivedRowCount = &arc
		}
	}
}

func (c *ListRecordsCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}
	limit := c.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit == 1 {
		return &output.Error{Err: "invalid_input", Detail: "--limit must be at least 2 (Slack rejects a page size of 1)", Code: output.ExitGeneral}
	}

	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()
	client, p, file, ctx := s.client, s.p, s.file, s.ctx

	rr := newRecordRenderer(file.Metadata.Schema, c.Raw)
	var limits *listLimits

	// Decode and join inside the fetch so a schema mismatch fails the page
	// before any of it reaches stdout; emit below is output-only.
	fetch := func(cursor string) ([]map[string]any, string, error) {
		params := map[string]string{
			"list_id": file.ID,
			"limit":   strconv.Itoa(limit),
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if c.View != "" {
			params["view_id"] = c.View
		}
		data, err := client.PostInternalForm(ctx, "lists.records.list", params)
		if err != nil {
			return nil, "", err
		}
		var resp recordsListResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, "", &output.Error{Err: "parse_error", Detail: "Failed to parse lists.records.list response", Code: output.ExitGeneral}
		}
		if resp.Limits != nil {
			limits = resp.Limits
		}

		rows := make([]map[string]any, 0, len(resp.Records))
		for _, rawRec := range resp.Records {
			rec, err := decodeJSONMap(rawRec)
			if err != nil {
				return nil, "", parseError("Failed to parse a lists.records.list record")
			}
			row, err := rr.render(rec)
			if err != nil {
				return nil, "", err
			}
			rows = append(rows, row)
		}
		return rows, resp.ResponseMetadata.NextCursor, nil
	}

	return streamPages(ctx, cli, p, "lists.records.list", c.Cursor, c.All, fetch, func(rows []map[string]any) error {
		for _, row := range rows {
			if err := p.PrintItem(row); err != nil {
				return err
			}
		}
		return nil
	}, withListLimits(&limits))
}

// searchFilesResponse is the subset of the search.files envelope list find
// reads. Matches stay typed as slack.File since only five fields are emitted.
type searchFilesResponse struct {
	Files struct {
		Matches []slack.File `json:"matches"`
		Paging  slack.Paging `json:"paging"`
	} `json:"files"`
}

func (c *ListFindCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}
	title := strings.TrimSpace(c.Title)
	if title == "" {
		return &output.Error{Err: "invalid_input", Detail: "title must not be blank", Code: output.ExitGeneral}
	}
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

	client, err := cli.NewSessionClient()
	if err != nil {
		return err
	}
	cli.NewResolver(client) // populate resolver for output enrichment
	p := cli.NewPrinter()
	ctx, cancel := cli.Context()
	defer cancel()

	// Slack search has no escape for a quote inside a quoted phrase, and Go's
	// %q would insert backslashes Slack reads literally. Quotes in the title
	// become spaces, which keeps the rest of the phrase matching.
	query := `type:list "` + strings.ReplaceAll(title, `"`, " ") + `"`

	fetch := pageCursorFetch(func(page int) ([]slack.File, *slack.Paging, error) {
		data, err := client.PostInternalForm(ctx, "search.files", map[string]string{
			"query": query,
			"count": strconv.Itoa(limit),
			"page":  strconv.Itoa(page),
		})
		if err != nil {
			return nil, nil, err
		}
		var resp searchFilesResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, nil, &output.Error{Err: "parse_error", Detail: "Failed to parse search.files response", Code: output.ExitGeneral}
		}
		return resp.Files.Matches, &resp.Files.Paging, nil
	})

	return streamPages(ctx, cli, p, "search.files", strconv.Itoa(page), c.All, fetch, func(matches []slack.File) error {
		for _, f := range matches {
			row := map[string]any{
				"id":        f.ID,
				"title":     f.Title,
				"user":      f.User,
				"created":   int64(f.Created),
				"permalink": f.Permalink,
			}
			if err := p.PrintItem(row); err != nil {
				return err
			}
		}
		return nil
	})
}
