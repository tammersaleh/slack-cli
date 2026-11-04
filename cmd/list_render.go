package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// Pure helpers for Slack Lists: the files.info shapes the list commands decode,
// the schema-to-record join, and per-column-type rendering. No network here, so
// list records can call renderRecord inside its fetch closure and fail a page
// before its first PrintItem.

// listColumn is one entry of file.list_metadata.schema.
type listColumn struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Key             string             `json:"key"`
	Type            string             `json:"type"`
	IsPrimaryColumn bool               `json:"is_primary_column"`
	Options         *listColumnOptions `json:"options"`
}

// listColumnOptions is the subset of column options the CLI reads. Plain
// columns carry null options; select columns carry format and choices. Every
// other key (default_value, and whatever Slack adds) is kept raw in Extra so
// set-choices can send the options object back whole instead of stripping
// keys this code does not model.
type listColumnOptions struct {
	Format  string
	Choices []listChoice
	Extra   map[string]json.RawMessage
}

func (o *listColumnOptions) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*o = listColumnOptions{Extra: map[string]json.RawMessage{}}
	for k, v := range raw {
		switch k {
		case "format":
			if err := json.Unmarshal(v, &o.Format); err != nil {
				return err
			}
		case "choices":
			if err := json.Unmarshal(v, &o.Choices); err != nil {
				return err
			}
		default:
			o.Extra[k] = v
		}
	}
	return nil
}

type listChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Color string `json:"color"`
}

// listFile is the raw files.info file object for a list. slack-go's File
// struct drops list_metadata and list_limits, so this is decoded by hand.
// Limits stay a map: the command passes them through rather than fabricating
// zero values for keys Slack did not send.
type listFile struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Filetype  string `json:"filetype"`
	User      string `json:"user"`
	Created   int64  `json:"created"`
	Updated   int64  `json:"updated"`
	Permalink string `json:"permalink"`
	Metadata  *struct {
		Description string       `json:"description"`
		Icon        string       `json:"icon"`
		Schema      []listColumn `json:"schema"`
	} `json:"list_metadata"`
	Limits map[string]any `json:"list_limits"`
}

// listLimits is the typed view of the limits block the records trailer needs.
type listLimits struct {
	RowCount         int `json:"row_count"`
	ArchivedRowCount int `json:"archived_row_count"`
}

// decodeJSONMap decodes a JSON object keeping numbers as json.Number, so a
// timestamp or a decimal re-encodes exactly as Slack sent it.
func decodeJSONMap(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// columnKeys assigns each schema column the key its value sits under in the
// row's fields object: the display name, or the column id when the name is
// empty. A name already taken probes name_2, name_3, ... until free, so a
// column literally named "Foo_2" keeps its own key when it precedes the second
// "Foo", which then moves on to "Foo_3". Assignment is in schema order with no
// reservation pass, so a "Foo_2" that follows two "Foo"s becomes "Foo_2_2".
// Keys are positional - keys[i] belongs to schema[i].
func columnKeys(schema []listColumn) []string {
	keys := make([]string, len(schema))
	used := make(map[string]bool, len(schema))
	for i, c := range schema {
		base := c.Name
		if base == "" {
			base = c.ID
		}
		key := base
		for n := 2; used[key]; n++ {
			key = base + "_" + strconv.Itoa(n)
		}
		used[key] = true
		keys[i] = key
	}
	return keys
}

// recordMetaKeys are the record keys copied to the top of a row. Everything
// else on the record (platform_refs, view_positions, list_id) is omitted.
var recordMetaKeys = []string{"id", "created_by", "updated_by", "date_created", "updated_timestamp", "position", "thread_ts"}

// recordRenderer joins lists.records.list records to one list's schema. Built
// once per walk: the schema, the deduped keys, and the column_id index are
// fixed for every page.
type recordRenderer struct {
	schema []listColumn
	keys   []string
	index  map[string]int
	raw    bool
}

func newRecordRenderer(schema []listColumn, raw bool) *recordRenderer {
	index := make(map[string]int, len(schema))
	for i, c := range schema {
		index[c.ID] = i
	}
	return &recordRenderer{schema: schema, keys: columnKeys(schema), index: index, raw: raw}
}

func parseError(detail string) *output.Error {
	return &output.Error{Err: "parse_error", Detail: detail, Code: output.ExitGeneral}
}

// render joins one record. Every column is present under fields, null when
// the record has no value for it. A field whose column_id is not in the schema
// is a list_schema_mismatch: the schema and the page were read by two calls and
// an edit landed between them. A fields entry that is not an object, or a
// fields value that is not an array, is a parse_error rather than a blank row,
// so a shape change fails loudly instead of emitting rows that look complete.
func (rr *recordRenderer) render(rec map[string]any) (map[string]any, error) {
	row := make(map[string]any, len(recordMetaKeys)+2)
	for _, k := range recordMetaKeys {
		if v, ok := rec[k]; ok {
			row[k] = v
		}
	}
	recID, _ := rec["id"].(string)

	fields := make(map[string]any, len(rr.schema))
	for _, k := range rr.keys {
		fields[k] = nil
	}
	var rawFields []any
	if v, present := rec["fields"]; present && v != nil {
		arr, ok := v.([]any)
		if !ok {
			return nil, parseError(fmt.Sprintf("record %s: fields is not an array", recID))
		}
		rawFields = arr
	}
	for _, f := range rawFields {
		field, ok := f.(map[string]any)
		if !ok {
			return nil, parseError(fmt.Sprintf("record %s: a fields entry is not an object", recID))
		}
		colID, _ := field["column_id"].(string)
		i, ok := rr.index[colID]
		if !ok {
			return nil, &output.Error{
				Err:    "list_schema_mismatch",
				Detail: fmt.Sprintf("record %s has a field for column %q that is not in the list schema", recID, colID),
				Hint:   "The list was edited between the schema read and this page. Rerun the command.",
				Code:   output.ExitGeneral,
			}
		}
		fields[rr.keys[i]] = renderFieldValue(rr.schema[i], field)
	}
	row["fields"] = fields
	if rr.raw {
		if rawFields == nil {
			rawFields = []any{}
		}
		row["raw_fields"] = rawFields
	}
	return row, nil
}

// renderFieldValue reduces one typed field object to the value a caller wants
// to read, by column type. Unknown types pass the field through minus the two
// join keys, so nothing is lost for a type this table has not met.
func renderFieldValue(col listColumn, field map[string]any) any {
	switch col.Type {
	case "text", "rich_text":
		// Only the rendered text. `value` on these columns is the block
		// array as a JSON string, which is not a value anyone wants keyed
		// by column name; --raw still carries it.
		return field["text"]
	case "select", "multi_select":
		vals, ok := field["select"].([]any)
		if !ok {
			return nil
		}
		labels := make([]any, len(vals))
		for i, v := range vals {
			labels[i] = choiceLabel(col, v)
		}
		return labels
	case "user", "created_by", "todo_assignee":
		return field["user"]
	case "channel":
		return field["channel"]
	case "email":
		return field["email"]
	case "date":
		return firstOf(field["date"])
	case "created_time":
		return firstOf(field["timestamp"])
	case "number":
		return firstOf(field["number"])
	case "checkbox", "todo_completed":
		return field["checkbox"]
	case "link":
		links, ok := field["link"].([]any)
		if !ok {
			return nil
		}
		urls := make([]any, 0, len(links))
		for _, l := range links {
			m, ok := l.(map[string]any)
			if !ok {
				urls = append(urls, l)
				continue
			}
			// Reads emit camelCase; the write API spells it original_url.
			// Accept both so a shape change does not silently blank a column.
			if u, ok := m["originalUrl"]; ok {
				urls = append(urls, u)
			} else {
				urls = append(urls, m["original_url"])
			}
		}
		return urls
	case "message":
		msgs, ok := field["message"].([]any)
		if !ok {
			return nil
		}
		out := make([]any, 0, len(msgs))
		for _, msg := range msgs {
			m, ok := msg.(map[string]any)
			if !ok {
				out = append(out, msg)
				continue
			}
			out = append(out, map[string]any{
				"permalink":  m["value"],
				"channel_id": m["channel_id"],
				"ts":         m["ts"],
				"thread_ts":  m["thread_ts"],
			})
		}
		return out
	default:
		rest := make(map[string]any, len(field))
		for k, v := range field {
			if k != "key" && k != "column_id" {
				rest[k] = v
			}
		}
		return rest
	}
}

// choiceLabel maps a select value to its label, or returns the value itself
// when the schema has no such choice.
func choiceLabel(col listColumn, v any) any {
	if col.Options == nil {
		return v
	}
	s, ok := v.(string)
	if !ok {
		return v
	}
	for _, c := range col.Options.Choices {
		if c.Value == s {
			return c.Label
		}
	}
	return v
}

// firstOf returns the first element of a JSON array, or nil when the value is
// not a non-empty array.
func firstOf(v any) any {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	return arr[0]
}

// schemaRow is one list schema output row.
func schemaRow(c listColumn) map[string]any {
	row := map[string]any{
		"column_id":         c.ID,
		"key":               c.Key,
		"name":              c.Name,
		"type":              c.Type,
		"is_primary_column": c.IsPrimaryColumn,
	}
	if c.Options != nil {
		if c.Options.Format != "" {
			row["format"] = c.Options.Format
		}
		if c.Options.Choices != nil {
			row["choices"] = c.Options.Choices
		}
	}
	return row
}
