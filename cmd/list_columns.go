package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// Pure helpers for the Slack Lists write commands: the --column SPEC grammar,
// choice construction, the whole-set choice replacement set-choices does, and
// column resolution against a schema. No network here, so every command can
// validate all of its input before its first call.

// listColumnTypes is the lists.columns.create type enum as verified live
// (docs/lists-plan.md). A type outside it is rejected locally because Slack
// answers invalid_arguments with no pointer to the offending value.
var listColumnTypes = map[string]bool{
	"text": true, "rich_text": true, "select": true, "multi_select": true,
	"date": true, "user": true, "checkbox": true, "number": true,
	"link": true, "email": true, "channel": true,
}

// choicePalette is Slack's select-choice color list. New choices take colors
// round-robin by position, which is what the Slack MCP does too.
var choicePalette = []string{"blue", "green", "yellow", "red", "purple", "orange", "cyan", "pink", "indigo", "brown", "gray"}

// columnSpec is one parsed NAME:TYPE[:CHOICE,CHOICE...] argument.
type columnSpec struct {
	Name   string
	Type   string
	Labels []string
}

func isSelectType(t string) bool { return t == "select" || t == "multi_select" }

// selectFormat is the options.format a select type carries.
func selectFormat(t string) string {
	if t == "multi_select" {
		return "multi_select"
	}
	return "single_select"
}

func specError(spec, detail string) *output.Error {
	return &output.Error{
		Err:    "invalid_input",
		Detail: detail,
		Hint:   "Column spec is NAME:TYPE[:CHOICE,CHOICE...]; types: " + strings.Join(sortedKeys(listColumnTypes), ", ") + ".",
		Input:  spec,
		Code:   output.ExitGeneral,
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parseColumnSpec parses NAME:TYPE[:CHOICE,CHOICE...]. Name and labels are
// trimmed; the type is lowercased. A column name cannot contain a colon.
func parseColumnSpec(spec string) (columnSpec, error) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) < 2 {
		return columnSpec{}, specError(spec, "column spec must be NAME:TYPE[:CHOICE,CHOICE...]")
	}
	cs := columnSpec{Name: strings.TrimSpace(parts[0]), Type: strings.ToLower(strings.TrimSpace(parts[1]))}
	if cs.Name == "" {
		return columnSpec{}, specError(spec, "column name must not be blank")
	}
	if !listColumnTypes[cs.Type] {
		return columnSpec{}, specError(spec, fmt.Sprintf("unknown column type %q", cs.Type))
	}
	if len(parts) == 3 {
		if !isSelectType(cs.Type) {
			return columnSpec{}, specError(spec, "only select and multi_select columns take choices")
		}
		for _, raw := range strings.Split(parts[2], ",") {
			label := strings.TrimSpace(raw)
			if label == "" {
				return columnSpec{}, specError(spec, "empty choice label")
			}
			cs.Labels = append(cs.Labels, label)
		}
	}
	return cs, nil
}

// slugifyLabel lowercases a label and collapses every run of characters
// outside [a-z0-9] into one underscore. It can return "" for a label with no
// ASCII alphanumerics; callers substitute a positional value then.
func slugifyLabel(label string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range strings.ToLower(label) {
		alnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !alnum {
			pendingSep = b.Len() > 0
			continue
		}
		if pendingSep {
			b.WriteByte('_')
			pendingSep = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func choiceError(detail string) *output.Error {
	return &output.Error{Err: "invalid_input", Detail: detail, Code: output.ExitGeneral}
}

// buildChoices makes the choice list for a brand-new select column: value is
// the slugified label (or choice_N when the label slugs to nothing), color is
// palette[position]. Labels must be unique and so must the values they slug
// to, since Slack keys a cell's selection by value.
func buildChoices(labels []string) ([]listChoice, error) {
	return mergeChoices(nil, labels)
}

// mergeChoices builds the replacement choice list set-choices sends. A label
// matching an existing choice exactly (case-sensitive) keeps that choice's
// value and color, so records holding it stay valid; every other label is a
// new choice with a slug value and a palette color by its position. A
// renamed label is therefore remove-plus-add; the command cannot infer
// renames. Duplicate labels and value collisions (new slug against new slug,
// or against a preserved value) are rejected rather than silently merged.
func mergeChoices(existing []listChoice, labels []string) ([]listChoice, error) {
	if len(labels) == 0 {
		return nil, choiceError("at least one choice is required")
	}
	byLabel := make(map[string]listChoice, len(existing))
	for _, c := range existing {
		byLabel[c.Label] = c
	}
	out := make([]listChoice, 0, len(labels))
	seenLabel := make(map[string]bool, len(labels))
	owner := make(map[string]string, len(labels)) // value -> label that produced it
	for i, label := range labels {
		if seenLabel[label] {
			return nil, choiceError(fmt.Sprintf("duplicate choice label %q", label))
		}
		seenLabel[label] = true
		c, ok := byLabel[label]
		if !ok {
			value := slugifyLabel(label)
			if value == "" {
				value = fmt.Sprintf("choice_%d", i+1)
			}
			c = listChoice{Value: value, Label: label, Color: choicePalette[i%len(choicePalette)]}
		}
		if prev, dup := owner[c.Value]; dup {
			return nil, choiceError(fmt.Sprintf("choice labels %q and %q collide on value %q", prev, label, c.Value))
		}
		owner[c.Value] = label
		out = append(out, c)
	}
	return out, nil
}

// choicesOptionsJSON is the options payload for lists.columns.update when
// replacing a select column's choices: the column's existing option keys
// (Slack adds ones this code does not model), with format filled from the
// type when absent, choices replaced, and a default_value that names a
// removed choice dropped rather than sent stale.
func choicesOptionsJSON(col listColumn, choices []listChoice) string {
	opts := map[string]any{}
	extra := map[string]json.RawMessage{}
	format := ""
	if col.Options != nil {
		extra = col.Options.Extra
		format = col.Options.Format
	}
	for k, v := range extra {
		opts[k] = v
	}
	if format == "" {
		format = selectFormat(col.Type)
	}
	if choices == nil {
		choices = []listChoice{}
	}
	if dv, ok := extra["default_value"]; ok {
		var s string
		keep := false
		if json.Unmarshal(dv, &s) == nil {
			for _, c := range choices {
				if c.Value == s {
					keep = true
					break
				}
			}
		}
		if !keep {
			delete(opts, "default_value")
		}
	}
	opts["format"] = format
	opts["choices"] = choices
	data, _ := json.Marshal(opts) // map[string]any of JSON-safe values cannot fail
	return string(data)
}

// newColumnOptionsJSON is the options payload for lists.columns.create, or ""
// when the column type takes none (plain columns send no options key).
func newColumnOptionsJSON(spec columnSpec) (string, error) {
	if !isSelectType(spec.Type) {
		return "", nil
	}
	// A select with no choices yet is sent with an empty list; the server is
	// the validator for whether that is allowed.
	choices := []listChoice{}
	if len(spec.Labels) != 0 {
		var err error
		if choices, err = buildChoices(spec.Labels); err != nil {
			return "", err
		}
	}
	return choicesOptionsJSON(listColumn{Type: spec.Type}, choices), nil
}

// resolveColumn finds one schema column for a caller's reference. An exact
// column id or key match wins outright, since those are Slack-unique; only
// when neither matches is the input compared case-insensitively to display
// names, which may collide (ambiguous_column). No match is column_not_found.
// Nothing here touches the network, so a resolution failure costs no write.
func resolveColumn(schema []listColumn, ref string) (*listColumn, *output.Error) {
	if ref != "" {
		for i := range schema {
			if schema[i].ID == ref || schema[i].Key == ref {
				return &schema[i], nil
			}
		}
	}
	var matches []*listColumn
	for i := range schema {
		if ref != "" && strings.EqualFold(schema[i].Name, ref) {
			matches = append(matches, &schema[i])
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, &output.Error{
			Err:    "column_not_found",
			Detail: fmt.Sprintf("no column with id, key, or name %q", ref),
			Hint:   "Run slack list schema <list> to see the columns.",
			Input:  ref,
			Code:   output.ExitGeneral,
		}
	}
	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = m.ID
	}
	return nil, &output.Error{
		Err:    "ambiguous_column",
		Detail: fmt.Sprintf("%d columns are named %q: %s", len(matches), ref, strings.Join(ids, ", ")),
		Hint:   "Use the column id or key instead of the name.",
		Input:  ref,
		Code:   output.ExitGeneral,
	}
}

// normalizeIconArg accepts an emoji name with or without wrapping colons
// (rocket, :rocket:, :wave::skin-tone-2:) and returns the bare name. Slack
// stores it with colons either way. Blank, whitespace, or a lone colon pair
// is rejected locally since what Slack does with them is unverified.
func normalizeIconArg(s string) (string, *output.Error) {
	bad := func(detail string) *output.Error {
		return &output.Error{Err: "invalid_input", Detail: detail, Input: s, Code: output.ExitGeneral}
	}
	name := s
	if len(name) >= 2 && name[0] == ':' && name[len(name)-1] == ':' {
		name = name[1 : len(name)-1]
	}
	if name == "" || strings.TrimSpace(name) == "" {
		return "", bad("icon must be an emoji name such as rocket or :rocket:")
	}
	if strings.ContainsAny(name, " \t\n") || strings.HasPrefix(name, ":") || strings.HasSuffix(name, ":") {
		return "", bad(fmt.Sprintf("icon %q is not an emoji name such as rocket or :rocket:", s))
	}
	return name, nil
}
