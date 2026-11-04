package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// Pure helpers for list record writes: the COLUMN=VALUE / stdin-JSON input
// grammar and the per-column-type conversion into the two write payloads.
// lists.records.create takes one scalar per column addressed by key;
// lists.cells.update takes one typed object per column addressed by id. Slack
// silently drops an unknown key and stores an out-of-range select value, so
// every assignment is fully validated here before the first call.

// recordIDRE is Slack's own shape check on row_id: a mismatch is
// invalid_arguments before any lookup, so the CLI rejects it locally.
var recordIDRE = regexp.MustCompile(`^Rec[A-Z0-9]{8,}$`)

// jsonNumberRE is JSON number syntax, which is what a number cell accepts.
// strconv.ParseFloat alone would admit hex floats, "Inf", and "NaN".
var jsonNumberRE = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// assignment is one COLUMN=VALUE pair or one stdin object member. Value is a
// string for a positional pair; for stdin it is whatever JSON held: string,
// bool, json.Number, nil, or []any (multi_select only).
type assignment struct {
	Column string
	Value  any
}

func assignError(input, detail string) *output.Error {
	return &output.Error{
		Err:    "invalid_input",
		Detail: detail,
		Hint:   "Pass COLUMN=VALUE pairs (split on the first '='), or pipe a JSON object {\"Column\":value} on stdin; not both.",
		Input:  input,
		Code:   output.ExitGeneral,
	}
}

// parseAssignments merges the two input forms. Positional pairs and stdin
// are mutually exclusive, and at least one assignment is required because
// Slack happily creates an empty record. stdin keys keep source order so the
// payload order matches what the caller wrote.
func parseAssignments(pairs []string, stdin []byte) ([]assignment, error) {
	hasStdin := len(strings.TrimSpace(string(stdin))) > 0
	if len(pairs) > 0 && hasStdin {
		return nil, assignError("", "COLUMN=VALUE arguments and a stdin JSON object are mutually exclusive")
	}
	var out []assignment
	switch {
	case hasStdin:
		dec := json.NewDecoder(strings.NewReader(string(stdin)))
		dec.UseNumber()
		tok, err := dec.Token()
		if err != nil || tok != json.Delim('{') {
			return nil, assignError("", "stdin must be a JSON object mapping column to value")
		}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, assignError("", "stdin is not valid JSON: "+err.Error())
			}
			key, _ := keyTok.(string)
			var v any
			if err := dec.Decode(&v); err != nil {
				return nil, assignError(key, "stdin is not valid JSON: "+err.Error())
			}
			out = append(out, assignment{Column: key, Value: v})
		}
		if _, err := dec.Token(); err != nil {
			return nil, assignError("", "stdin is not valid JSON: "+err.Error())
		}
		// Anything after the object would be silently lost otherwise - a
		// second object, or stray text, is the data-loss class this command
		// exists to prevent.
		if _, err := dec.Token(); err != io.EOF {
			return nil, assignError("", "stdin must hold exactly one JSON object")
		}
	default:
		for _, pair := range pairs {
			col, val, ok := strings.Cut(pair, "=")
			if !ok {
				return nil, assignError(pair, fmt.Sprintf("%q is not COLUMN=VALUE", pair))
			}
			if strings.TrimSpace(col) == "" {
				return nil, assignError(pair, "column name must not be blank")
			}
			out = append(out, assignment{Column: col, Value: val})
		}
	}
	if len(out) == 0 {
		return nil, assignError("", "at least one column assignment is required")
	}
	return out, nil
}

// cellWrite is one validated assignment ready for either write payload.
// Scalar is the lists.records.create value; Typed is the lists.cells.update
// value under TypedKey. A clear has no Scalar, since add rejects it.
type cellWrite struct {
	Col      listColumn
	Scalar   any
	TypedKey string
	Typed    any
}

// createField is one lists.records.create fields entry.
type createField struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// createFieldsJSON builds the fields form value, addressed by column key.
func createFieldsJSON(cells []cellWrite) string {
	fields := make([]createField, len(cells))
	for i, c := range cells {
		fields[i] = createField{Key: c.Col.Key, Value: c.Scalar}
	}
	data, _ := json.Marshal(fields) // JSON-safe scalars cannot fail
	return string(data)
}

// updateCellsJSON builds the cells form value, addressed by column id. Keys
// are sorted by encoding/json, so the payload is deterministic.
func updateCellsJSON(recordID string, cells []cellWrite) string {
	out := make([]map[string]any, len(cells))
	for i, c := range cells {
		out[i] = map[string]any{"row_id": recordID, "column_id": c.Col.ID, c.TypedKey: c.Typed}
	}
	data, _ := json.Marshal(out)
	return string(data)
}

// richTextNode is one node of the rich_text tree lists.cells.update wants
// for text columns; field order matches Slack's own encoding.
type richTextNode struct {
	Type     string         `json:"type"`
	Elements []richTextNode `json:"elements,omitempty"`
	Text     string         `json:"text,omitempty"`
}

// richTextBlocks wraps plain text in a one-section rich_text block array.
func richTextBlocks(text string) []richTextNode {
	return []richTextNode{{
		Type: "rich_text",
		Elements: []richTextNode{{
			Type:     "rich_text_section",
			Elements: []richTextNode{{Type: "text", Text: text}},
		}},
	}}
}

func valueError(col listColumn, input, detail string) *output.Error {
	return &output.Error{
		Err:    "invalid_input",
		Detail: fmt.Sprintf("column %q (%s): %s", col.Name, col.Type, detail),
		Input:  input,
		Code:   output.ExitGeneral,
	}
}

// valueText renders a stdin or positional value for error messages.
func valueText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	default:
		data, _ := json.Marshal(x)
		return string(data)
	}
}

// isClear reports whether the caller asked to empty the cell: stdin null or
// an empty positional value (positional input cannot spell null).
func isClear(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

// stringValue requires a string; stdin may carry other JSON types, which are
// a type mismatch for every column but checkbox and number.
func stringValue(col listColumn, v any) (string, *output.Error) {
	s, ok := v.(string)
	if !ok {
		return "", valueError(col, valueText(v), "value must be a string")
	}
	return s, nil
}

// convertCell validates one assignment against its column and produces both
// payload shapes. forUpdate allows clearing select, multi_select, and user
// cells (the shapes verified live); clearing any other type is unverified
// and rejected, as is clearing anything on add (omit the column instead).
// Resolution of user and channel names happens in the caller, which hands
// the resolved id in; this function is pure.
func convertCell(col listColumn, v any, forUpdate bool) (cellWrite, *output.Error) {
	cw := cellWrite{Col: col}
	if isClear(v) {
		if !forUpdate {
			return cw, valueError(col, valueText(v), "empty value; omit the column to leave it unset")
		}
		switch col.Type {
		case "select", "multi_select":
			cw.TypedKey, cw.Typed = "select", []any{}
		case "user":
			cw.TypedKey, cw.Typed = "user", []any{}
		default:
			return cw, valueError(col, valueText(v), "clearing this column type is not supported (only select, multi_select, and user cells can be cleared)")
		}
		return cw, nil
	}

	switch col.Type {
	case "text", "rich_text":
		s, err := stringValue(col, v)
		if err != nil {
			return cw, err
		}
		cw.Scalar, cw.TypedKey, cw.Typed = s, "rich_text", richTextBlocks(s)
	case "select", "multi_select":
		values, err := selectValues(col, v, forUpdate)
		if err != nil {
			return cw, err
		}
		cw.Scalar, cw.TypedKey, cw.Typed = values[0], "select", values
	case "checkbox":
		b, ok := v.(bool)
		if s, isStr := v.(string); isStr {
			switch s {
			case "true":
				b, ok = true, true
			case "false":
				b, ok = false, true
			}
		}
		if !ok {
			return cw, valueError(col, valueText(v), "value must be true or false")
		}
		cw.Scalar, cw.TypedKey, cw.Typed = b, "checkbox", b
	case "number":
		var text string
		switch x := v.(type) {
		case json.Number:
			text = x.String()
		case string:
			text = strings.TrimSpace(x)
		default:
			return cw, valueError(col, valueText(v), "value must be a number")
		}
		if !jsonNumberRE.MatchString(text) {
			return cw, valueError(col, valueText(v), "value must be a decimal number")
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return cw, valueError(col, valueText(v), "value must be a finite number")
		}
		n := json.Number(text)
		cw.Scalar, cw.TypedKey, cw.Typed = n, "number", []any{n}
	case "date":
		s, err := stringValue(col, v)
		if err != nil {
			return cw, err
		}
		if _, perr := time.Parse("2006-01-02", s); perr != nil {
			return cw, valueError(col, s, "value must be a calendar date formatted YYYY-MM-DD")
		}
		cw.Scalar, cw.TypedKey, cw.Typed = s, "date", []any{s}
	case "user":
		// v is the resolved id; the caller resolved names before calling.
		s, err := stringValue(col, v)
		if err != nil {
			return cw, err
		}
		cw.Scalar, cw.TypedKey, cw.Typed = s, "user", []any{s}
	case "channel":
		s, err := stringValue(col, v)
		if err != nil {
			return cw, err
		}
		cw.Scalar, cw.TypedKey, cw.Typed = s, "channel", []any{s}
	case "email":
		s, err := stringValue(col, v)
		if err != nil {
			return cw, err
		}
		if !isEmailShaped(s) {
			return cw, valueError(col, s, "value must be an email address")
		}
		cw.Scalar, cw.TypedKey, cw.Typed = s, "email", []any{s}
	case "link":
		s, err := stringValue(col, v)
		if err != nil {
			return cw, err
		}
		u, perr := url.Parse(s)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return cw, valueError(col, s, "value must be an http(s) URL")
		}
		cw.Scalar, cw.TypedKey, cw.Typed = s, "link", []any{map[string]any{"original_url": s}}
	default:
		return cw, valueError(col, valueText(v), "this column type cannot be written")
	}
	return cw, nil
}

// isEmailShaped is the minimal check: one '@' with something on both sides
// and no whitespace. Slack is the validator beyond that.
func isEmailShaped(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && at < len(s)-1 && strings.LastIndex(s, "@") == at && !strings.ContainsAny(s, " \t\n")
}

// selectValues maps a caller's choice names to choice values. A positional
// value is one choice for select, or comma-separated choices for
// multi_select; stdin may give a string or, for multi_select, an array of
// strings. Each name matches a choice value exactly, else a label exactly,
// else a label case-insensitively. Unknown names are invalid_choice, since
// lists.records.create stores them as-is. multi_select on add is rejected:
// the create API takes one scalar, and sending one value of several is
// unverified. Duplicate names naming the same choice collapse to one.
func selectValues(col listColumn, v any, forUpdate bool) ([]any, *output.Error) {
	multi := col.Type == "multi_select"
	if multi && !forUpdate {
		return nil, valueError(col, valueText(v), "multi_select values cannot be set on add; add the record, then use list record update")
	}
	var names []string
	switch x := v.(type) {
	case string:
		if multi {
			names = strings.Split(x, ",")
		} else {
			names = []string{x}
		}
	case []any:
		if !multi {
			return nil, valueError(col, valueText(v), "a single-select column takes one value, not an array")
		}
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, valueError(col, valueText(v), "array values must be strings")
			}
			names = append(names, s)
		}
	default:
		return nil, valueError(col, valueText(v), "value must be a string")
	}
	var choices []listChoice
	if col.Options != nil {
		choices = col.Options.Choices
	}
	values := make([]any, 0, len(names))
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		value, ok := matchChoice(choices, name)
		if !ok {
			return nil, &output.Error{
				Err:    "invalid_choice",
				Detail: fmt.Sprintf("column %q has no choice %q", col.Name, name),
				Hint:   "Run slack list schema <list> to see the choices (label or value both work).",
				Input:  valueText(v),
				Code:   output.ExitGeneral,
			}
		}
		if !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, valueError(col, valueText(v), "at least one choice is required")
	}
	return values, nil
}

// matchChoice finds a choice by value, then exact label, then
// case-insensitive label (first match in schema order).
func matchChoice(choices []listChoice, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	for _, c := range choices {
		if c.Value == name {
			return c.Value, true
		}
	}
	for _, c := range choices {
		if c.Label == name {
			return c.Value, true
		}
	}
	for _, c := range choices {
		if strings.EqualFold(c.Label, name) {
			return c.Value, true
		}
	}
	return "", false
}
