package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tammersaleh/slack-cli/internal/output"
	"github.com/tammersaleh/slack-cli/internal/resolve"
)

// Slack Lists record writes. Same transport as the other list commands
// (session client, PostInternalForm). lists.records.create addresses columns
// by key and takes scalars; lists.cells.update addresses them by id and
// takes typed objects. Every assignment is resolved, converted, and
// validated before the first call, because Slack drops unknown keys and
// stores out-of-range select values without complaint.

type ListRecordCmd struct {
	Add    ListRecordAddCmd    `cmd:"" help:"Add a record (COLUMN=VALUE... or a JSON object on stdin)."`
	Update ListRecordUpdateCmd `cmd:"" help:"Update a record's cells (COLUMN=VALUE... or a JSON object on stdin)."`
}

type ListRecordAddCmd struct {
	List        string   `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
	Assignments []string `arg:"" optional:"" name:"assignment" help:"COLUMN=VALUE; column is a key, id, or display name. Omit to read {\"Column\":value} JSON from stdin."`
}

type ListRecordUpdateCmd struct {
	List        string   `arg:"" required:"" help:"List file ID (F...) or /lists/ URL."`
	Record      string   `arg:"" required:"" help:"Record ID (Rec...)."`
	Assignments []string `arg:"" optional:"" name:"assignment" help:"COLUMN=VALUE; an empty value (or JSON null) clears a select or user cell."`
}

// readAssignments reads piped stdin and merges it with the positional pairs.
func readAssignments(cli *CLI, pairs []string) ([]assignment, error) {
	stdin, err := readPipedStdin(cli)
	if err != nil {
		return nil, err
	}
	return parseAssignments(pairs, stdin)
}

// resolveCells turns assignments into validated cell writes against the
// session's schema. Column resolution is resolveColumn's tiered match; two
// assignments landing on one column are invalid_input. User and channel
// names resolve before conversion; a miss is user_not_found /
// channel_not_found with no write made.
//
// User names stay on the session resolver (users.list works on the org
// token, and that is the directory scope every other command uses). A
// channel *name* cannot: on Enterprise Grid the session client carries the
// org token, and users.conversations and conversations.list are both
// enterprise_is_restricted there, so a #name could never resolve and would
// surface as channel_not_found. Name-shaped channel values therefore go
// through a resolver on the workspace client (NewClient), built only when
// one is needed so ids and URLs keep working on an org-only credential -
// the same conditional split draft create uses. NewClient overwrites
// cli.authMethod, so the session's is restored for the write's error hint.
func resolveCells(cli *CLI, s *listSession, assigns []assignment, forUpdate bool) ([]cellWrite, error) {
	schema := s.file.Metadata.Schema
	var channelResolver *resolve.Resolver
	workspaceResolver := func() (*resolve.Resolver, error) {
		if channelResolver == nil {
			sessionAuthMethod := cli.authMethod
			pub, err := cli.NewClient()
			if err != nil {
				return nil, err
			}
			cli.authMethod = sessionAuthMethod
			channelResolver = cli.newResolver(pub)
		}
		return channelResolver, nil
	}
	cells := make([]cellWrite, 0, len(assigns))
	seen := make(map[string]string, len(assigns)) // column id -> first input naming it
	for _, a := range assigns {
		col, cErr := resolveColumn(schema, a.Column)
		if cErr != nil {
			return nil, cErr
		}
		if prev, dup := seen[col.ID]; dup {
			return nil, assignError(a.Column, fmt.Sprintf("%q and %q both name column %s", prev, a.Column, col.ID))
		}
		seen[col.ID] = a.Column

		v := a.Value
		if !isClear(v) {
			switch col.Type {
			case "user":
				if input, ok := v.(string); ok {
					id, err := s.resolver.ResolveUser(s.ctx, input)
					if err != nil {
						return nil, userResolveError(input, err)
					}
					v = id
				}
			case "channel":
				if input, ok := v.(string); ok {
					r := s.resolver
					if resolve.ChannelInputNeedsLookup(input) {
						var err error
						if r, err = workspaceResolver(); err != nil {
							return nil, err
						}
					}
					id, err := r.ResolveChannel(s.ctx, input)
					if err != nil {
						return nil, channelResolveError(input, err)
					}
					v = id
				}
			}
		}
		cw, vErr := convertCell(*col, v, forUpdate)
		if vErr != nil {
			return nil, vErr
		}
		cells = append(cells, cw)
	}
	return cells, nil
}

// decodeRecordResponse pulls the record object out of a lists.records.*
// envelope and joins it to the schema.
func decodeRecordResponse(method string, data json.RawMessage, rr *recordRenderer) (map[string]any, error) {
	var resp struct {
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Record) == 0 || string(resp.Record) == "null" {
		return nil, parseError(fmt.Sprintf("%s response did not contain a record object", method))
	}
	rec, err := decodeJSONMap(resp.Record)
	if err != nil {
		return nil, parseError(fmt.Sprintf("Failed to parse the %s record", method))
	}
	return rr.render(rec)
}

// --- list record add ---

func (c *ListRecordAddCmd) Run(cli *CLI) error {
	assigns, err := readAssignments(cli, c.Assignments)
	if err != nil {
		return err
	}
	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()
	cells, err := resolveCells(cli, s, assigns, false)
	if err != nil {
		return err
	}

	data, err := s.client.PostInternalForm(s.ctx, "lists.records.create", map[string]string{
		"list_id": s.file.ID,
		"fields":  createFieldsJSON(cells),
	})
	if err != nil {
		return cli.ClassifyError(err)
	}
	// The record exists from here; a response this code cannot read is a
	// partial row naming the list, not a stderr-only failure.
	row, err := decodeRecordResponse("lists.records.create", data, newRecordRenderer(s.file.Metadata.Schema, false))
	if err != nil {
		return printPartialRow(cli, s.p, map[string]any{"list_id": s.file.ID}, err)
	}
	if err := s.p.PrintItem(row); err != nil {
		return err
	}
	return s.p.PrintMeta(output.Meta{})
}

// --- list record update ---

func (c *ListRecordUpdateCmd) Run(cli *CLI) error {
	recID := strings.TrimSpace(c.Record)
	if !recordIDRE.MatchString(recID) {
		return &output.Error{
			Err:    "invalid_input",
			Detail: fmt.Sprintf("%q is not a record id (Rec followed by at least 8 uppercase letters or digits)", c.Record),
			Hint:   "Run slack list records <list> to find record ids.",
			Input:  c.Record,
			Code:   output.ExitGeneral,
		}
	}
	assigns, err := readAssignments(cli, c.Assignments)
	if err != nil {
		return err
	}
	s, err := openListSession(cli, c.List)
	if err != nil {
		return err
	}
	defer s.cancel()
	cells, err := resolveCells(cli, s, assigns, true)
	if err != nil {
		return err
	}

	if _, err := s.client.PostInternalForm(s.ctx, "lists.cells.update", map[string]string{
		"list_id": s.file.ID,
		"cells":   updateCellsJSON(recID, cells),
	}); err != nil {
		return cli.ClassifyError(err)
	}
	// cells.update echoes only the changed cells, so the full record comes
	// from a re-read. The write already landed, so a failure here is a
	// partial row carrying the record id, never a stderr-only error.
	row, err := rereadRecord(s, recID)
	if err != nil {
		return printPartialRow(cli, s.p, map[string]any{"id": recID, "list_id": s.file.ID}, err)
	}
	if err := s.p.PrintItem(row); err != nil {
		return err
	}
	return s.p.PrintMeta(output.Meta{})
}

// rereadRecord fetches one record through lists.records.info and renders it
// against the schema read at session open.
func rereadRecord(s *listSession, recID string) (map[string]any, error) {
	data, err := s.client.PostInternalForm(s.ctx, "lists.records.info", map[string]string{"list_id": s.file.ID, "id": recID})
	if err != nil {
		return nil, err
	}
	return decodeRecordResponse("lists.records.info", data, newRecordRenderer(s.file.Metadata.Schema, false))
}
