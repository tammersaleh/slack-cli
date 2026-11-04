package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// Slack Lists writes: list create/update and the column commands. Same
// transport as the reads (session client, PostInternalForm, org token on
// Grid). Every row comes from the server's returned list object, never from
// the request, and every input is validated before the first call.

type ListCreateCmd struct {
	Name        string   `arg:"" required:"" help:"List title."`
	Description string   `help:"List description."`
	Icon        *string  `help:"Icon emoji name (rocket or :rocket:)."`
	Column      []string `help:"Column to add after creation, NAME:TYPE[:CHOICE,CHOICE...]; repeatable, applied in order." placeholder:"SPEC" sep:"none"`
}

type ListUpdateCmd struct {
	List        string  `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
	Name        *string `help:"New title."`
	Description *string `help:"New description (empty clears it)."`
	Icon        *string `help:"Icon emoji name (rocket or :rocket:)."`
}

type ListColumnCmd struct {
	Add        ListColumnAddCmd        `cmd:"" help:"Add columns (NAME:TYPE[:CHOICE,CHOICE...])."`
	Rename     ListColumnRenameCmd     `cmd:"" help:"Rename a column, keeping its id, key, type, and choices."`
	SetChoices ListColumnSetChoicesCmd `cmd:"" name:"set-choices" help:"Replace a select column's choices (whole set)."`
	Delete     ListColumnDeleteCmd     `cmd:"" help:"Delete a column."`
}

type ListColumnAddCmd struct {
	List  string   `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
	Specs []string `arg:"" required:"" name:"spec" help:"NAME:TYPE[:CHOICE,CHOICE...]; choices only for select and multi_select."`
}

type ListColumnRenameCmd struct {
	List   string `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
	Column string `arg:"" required:"" help:"Column key, id, or display name."`
	Name   string `arg:"" required:"" help:"New column name."`
}

type ListColumnSetChoicesCmd struct {
	List    string   `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
	Column  string   `arg:"" required:"" help:"Select column key, id, or display name."`
	Choices []string `arg:"" required:"" name:"choice" help:"Complete new set of choice labels, in order."`
}

type ListColumnDeleteCmd struct {
	List   string `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
	Column string `arg:"" required:"" help:"Column key, id, or display name."`
}

// listInfoRow is the list info row shape, shared by create and update so a
// write's success row reads exactly like a subsequent list info.
func listInfoRow(file *listFile) map[string]any {
	return map[string]any{
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
}

// decodeListResponse parses the {"ok":true,"list":{...}} envelope the lists
// write methods return. A list without an id or metadata is a parse_error:
// nothing downstream can render a row from it.
func decodeListResponse(method string, data json.RawMessage) (*listFile, error) {
	var resp struct {
		List listFile `json:"list"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || resp.List.ID == "" || resp.List.Metadata == nil {
		return nil, parseError(fmt.Sprintf("%s response did not contain a list object", method))
	}
	return &resp.List, nil
}

// columnByID finds a column in a returned list, for the row after a write
// that targeted it by id.
func columnByID(method string, file *listFile, id string) (*listColumn, error) {
	for i := range file.Metadata.Schema {
		if file.Metadata.Schema[i].ID == id {
			return &file.Metadata.Schema[i], nil
		}
	}
	return nil, parseError(fmt.Sprintf("%s response list has no column %s", method, id))
}

// columnRow is a list schema row plus the list it belongs to.
func columnRow(listID string, col listColumn) map[string]any {
	row := schemaRow(col)
	row["list_id"] = listID
	return row
}

// columnCreateParams is the lists.columns.create form for one spec; plain
// columns send no options key.
func columnCreateParams(listID string, spec columnSpec, optionsJSON string) map[string]string {
	params := map[string]string{"list_id": listID, "type": spec.Type, "name": spec.Name}
	if optionsJSON != "" {
		params["options"] = optionsJSON
	}
	return params
}

// parseColumnSpecs parses every spec and builds its options payload up
// front, so a bad spec costs no call.
func parseColumnSpecs(specs []string) ([]columnSpec, []string, error) {
	parsed := make([]columnSpec, 0, len(specs))
	options := make([]string, 0, len(specs))
	for _, raw := range specs {
		spec, err := parseColumnSpec(raw)
		if err != nil {
			return nil, nil, err
		}
		opts, err := newColumnOptionsJSON(spec)
		if err != nil {
			return nil, nil, err
		}
		parsed = append(parsed, spec)
		options = append(options, opts)
	}
	return parsed, options, nil
}

// classifyWriteError turns any failure after a write into the error the
// partial row reports: an *output.Error (a decode failure, say) is already
// classified, anything else is a Slack or transport error.
func classifyWriteError(cli *CLI, err error) *output.Error {
	var oErr *output.Error
	if errors.As(err, &oErr) {
		return oErr
	}
	return cli.ClassifyError(err)
}

// printPartialRow emits an item-local failure row and returns the exit code
// the classified error carries, without a stderr error: the row on stdout
// is the report. Used when earlier writes in the same command already took,
// so the caller must learn what exists even though the command failed.
func printPartialRow(cli *CLI, p *output.Printer, row map[string]any, err error) error {
	oErr := classifyWriteError(cli, err)
	row["error"] = oErr.Err
	if oErr.Detail != "" {
		row["detail"] = oErr.Detail
	}
	if err := p.PrintItem(row); err != nil {
		return err
	}
	if err := p.PrintMeta(output.Meta{}); err != nil {
		return err
	}
	return &output.ExitError{Code: oErr.Code}
}

// --- list create ---

func (c *ListCreateCmd) Run(cli *CLI) error {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		// Slack accepts an empty name and titles the list "list".
		return &output.Error{Err: "invalid_input", Detail: "list name must not be blank", Input: c.Name, Code: output.ExitGeneral}
	}
	specs, options, err := parseColumnSpecs(c.Column)
	if err != nil {
		return err
	}
	icon := ""
	if c.Icon != nil {
		var iErr *output.Error
		if icon, iErr = normalizeIconArg(*c.Icon); iErr != nil {
			return iErr
		}
	}

	s, err := newListSession(cli)
	if err != nil {
		return err
	}
	defer s.cancel()
	client, p, ctx := s.client, s.p, s.ctx

	data, err := client.PostInternalForm(ctx, "lists.create", map[string]string{"name": c.Name, "description": c.Description})
	if err != nil {
		return cli.ClassifyError(err)
	}
	file, err := decodeListResponse("lists.create", data)
	if err != nil {
		return err
	}

	// From here the list exists, so every failure - Slack's or a response
	// this code cannot read - is reported on a row naming the list and what
	// remains to be done; the list is never deleted.
	listID := file.ID
	for i, spec := range specs {
		if file, err = createColumn(ctx, client, listID, spec, options[i]); err != nil {
			row := map[string]any{
				"id":            listID,
				"failed_step":   "column",
				"failed_column": spec.Name,
				"hint":          createRecoveryHint(listID, c.Column[i:], icon),
			}
			return printPartialRow(cli, p, row, err)
		}
	}
	if icon != "" {
		data, err := client.PostInternalForm(ctx, "lists.update", map[string]string{"id": listID, "icon": icon})
		if err == nil {
			file, err = decodeListResponse("lists.update", data)
		}
		if err != nil {
			row := map[string]any{
				"id":          listID,
				"failed_step": "icon",
				"hint":        createRecoveryHint(listID, nil, icon),
			}
			return printPartialRow(cli, p, row, err)
		}
	}

	if err := p.PrintItem(listInfoRow(file)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// createColumn runs one lists.columns.create and returns the list as the
// server now holds it. A response without a list object is an error like
// any other: the caller cannot tell whether the column exists.
func createColumn(ctx context.Context, client *api.Client, listID string, spec columnSpec, optionsJSON string) (*listFile, error) {
	data, err := client.PostInternalForm(ctx, "lists.columns.create", columnCreateParams(listID, spec, optionsJSON))
	if err != nil {
		return nil, err
	}
	return decodeListResponse("lists.columns.create", data)
}

// createRecoveryHint names the commands that finish a partially created
// list: the column specs not yet added (the failed one first) and the icon.
func createRecoveryHint(listID string, remainingSpecs []string, icon string) string {
	var steps []string
	if len(remainingSpecs) > 0 {
		quoted := make([]string, len(remainingSpecs))
		for i, s := range remainingSpecs {
			quoted[i] = "'" + s + "'"
		}
		steps = append(steps, "slack list column add "+listID+" "+strings.Join(quoted, " "))
	}
	if icon != "" {
		steps = append(steps, "slack list update "+listID+" --icon "+icon)
	}
	return "The list was created. Finish it with: " + strings.Join(steps, " ; ")
}

// --- list update ---

func (c *ListUpdateCmd) Run(cli *CLI) error {
	id, idErr := fileIDFromArg(c.List)
	if idErr != nil {
		return idErr
	}
	if c.Name == nil && c.Description == nil && c.Icon == nil {
		return &output.Error{Err: "invalid_input", Detail: "at least one of --name, --description, --icon is required", Code: output.ExitGeneral}
	}
	params := map[string]string{"id": id}
	if c.Name != nil {
		if strings.TrimSpace(*c.Name) == "" {
			return &output.Error{Err: "invalid_input", Detail: "list name must not be blank", Input: *c.Name, Code: output.ExitGeneral}
		}
		params["name"] = *c.Name
	}
	if c.Description != nil {
		params["description"] = *c.Description
	}
	if c.Icon != nil {
		icon, iErr := normalizeIconArg(*c.Icon)
		if iErr != nil {
			return iErr
		}
		params["icon"] = icon
	}

	s, err := newListSession(cli)
	if err != nil {
		return err
	}
	defer s.cancel()

	data, err := s.client.PostInternalForm(s.ctx, "lists.update", params)
	if err != nil {
		return cli.ClassifyError(err)
	}
	file, err := decodeListResponse("lists.update", data)
	if err != nil {
		return err
	}
	if err := s.p.PrintItem(listInfoRow(file)); err != nil {
		return err
	}
	return s.p.PrintMeta(output.Meta{})
}

// --- list column add ---

func (c *ListColumnAddCmd) Run(cli *CLI) error {
	specs, options, err := parseColumnSpecs(c.Specs)
	if err != nil {
		return err
	}
	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()
	client, p, file, ctx := s.client, s.p, s.file, s.ctx

	known := make(map[string]bool, len(file.Metadata.Schema)+len(specs))
	for _, col := range file.Metadata.Schema {
		known[col.ID] = true
	}
	listID := file.ID
	for i, spec := range specs {
		// Rows for earlier specs are already on stdout, so any failure here
		// - Slack's, or a response this code cannot read - is a row too,
		// and the trailer still closes the stream.
		col, err := addColumn(ctx, client, listID, known, spec, options[i])
		if err != nil {
			row := map[string]any{"list_id": listID, "name": spec.Name, "type": spec.Type}
			return printPartialRow(cli, p, row, err)
		}
		known[col.ID] = true
		if err := p.PrintItem(columnRow(listID, *col)); err != nil {
			return err
		}
	}
	return p.PrintMeta(output.Meta{})
}

// addColumn creates one column and returns it as the server returned it.
func addColumn(ctx context.Context, client *api.Client, listID string, known map[string]bool, spec columnSpec, optionsJSON string) (*listColumn, error) {
	file, err := createColumn(ctx, client, listID, spec, optionsJSON)
	if err != nil {
		return nil, err
	}
	return newColumnIn(file, known, spec)
}

// newColumnIn picks the column a lists.columns.create response added: the
// one id not seen before. Anything other than exactly one new id (none, or
// several because another editor added a column meanwhile) is an error
// rather than a guess by name, since a guess could name an older column.
func newColumnIn(file *listFile, known map[string]bool, spec columnSpec) (*listColumn, error) {
	var fresh []*listColumn
	for i := range file.Metadata.Schema {
		if !known[file.Metadata.Schema[i].ID] {
			fresh = append(fresh, &file.Metadata.Schema[i])
		}
	}
	if len(fresh) != 1 {
		return nil, parseError(fmt.Sprintf("lists.columns.create response has %d unseen columns, cannot identify %q", len(fresh), spec.Name))
	}
	return fresh[0], nil
}

// --- list column rename ---

func (c *ListColumnRenameCmd) Run(cli *CLI) error {
	if strings.TrimSpace(c.Name) == "" {
		return &output.Error{Err: "invalid_input", Detail: "column name must not be blank", Input: c.Name, Code: output.ExitGeneral}
	}
	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()
	col, cErr := resolveColumn(s.file.Metadata.Schema, c.Column)
	if cErr != nil {
		return cErr
	}
	return updateColumn(cli, s, col.ID, map[string]string{"list_id": s.file.ID, "id": col.ID, "name": c.Name})
}

// updateColumn runs lists.columns.update and emits the targeted column as the
// server returned it.
func updateColumn(cli *CLI, s *listSession, colID string, params map[string]string) error {
	data, err := s.client.PostInternalForm(s.ctx, "lists.columns.update", params)
	if err != nil {
		return cli.ClassifyError(err)
	}
	file, err := decodeListResponse("lists.columns.update", data)
	if err != nil {
		return err
	}
	col, err := columnByID("lists.columns.update", file, colID)
	if err != nil {
		return err
	}
	if err := s.p.PrintItem(columnRow(file.ID, *col)); err != nil {
		return err
	}
	return s.p.PrintMeta(output.Meta{})
}

// --- list column set-choices ---

func (c *ListColumnSetChoicesCmd) Run(cli *CLI) error {
	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()
	col, cErr := resolveColumn(s.file.Metadata.Schema, c.Column)
	if cErr != nil {
		return cErr
	}
	if !isSelectType(col.Type) {
		return &output.Error{
			Err:    "invalid_input",
			Detail: fmt.Sprintf("column %q is a %s column; choices apply to select and multi_select columns", col.Name, col.Type),
			Input:  c.Column,
			Code:   output.ExitGeneral,
		}
	}
	var existing []listChoice
	if col.Options != nil {
		existing = col.Options.Choices
	}
	choices, err := mergeChoices(existing, c.Choices)
	if err != nil {
		return err
	}
	return updateColumn(cli, s, col.ID, map[string]string{
		"list_id": s.file.ID,
		"id":      col.ID,
		"options": choicesOptionsJSON(*col, choices),
	})
}

// --- list column delete ---

func (c *ListColumnDeleteCmd) Run(cli *CLI) error {
	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()
	col, cErr := resolveColumn(s.file.Metadata.Schema, c.Column)
	if cErr != nil {
		return cErr
	}
	if _, err := s.client.PostInternalForm(s.ctx, "lists.columns.delete", map[string]string{"list_id": s.file.ID, "id": col.ID}); err != nil {
		return cli.ClassifyError(err)
	}
	// lists.columns.delete answers a bare {ok}, so the row is the column as
	// resolved before the call plus the outcome.
	row := map[string]any{
		"list_id":   s.file.ID,
		"column_id": col.ID,
		"key":       col.Key,
		"name":      col.Name,
		"type":      col.Type,
		"status":    "deleted",
	}
	if err := s.p.PrintItem(row); err != nil {
		return err
	}
	return s.p.PrintMeta(output.Meta{})
}
