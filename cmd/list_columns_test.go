package cmd

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

func wantInvalidInput(t *testing.T, err error, detailPart string) {
	t.Helper()
	var oe *output.Error
	if !errors.As(err, &oe) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oe.Err != "invalid_input" {
		t.Fatalf("code = %q, want invalid_input (%s)", oe.Err, oe.Detail)
	}
	if !strings.Contains(oe.Detail, detailPart) {
		t.Errorf("detail %q does not mention %q", oe.Detail, detailPart)
	}
}

func TestParseColumnSpec(t *testing.T) {
	tests := []struct {
		spec   string
		want   columnSpec
		errHas string
	}{
		{spec: "Owner:user", want: columnSpec{Name: "Owner", Type: "user"}},
		{spec: " Due date : DATE ", want: columnSpec{Name: "Due date", Type: "date"}},
		{spec: "Status:select:To do,Done", want: columnSpec{Name: "Status", Type: "select", Labels: []string{"To do", "Done"}}},
		{spec: "Tags:multi_select: a , b ", want: columnSpec{Name: "Tags", Type: "multi_select", Labels: []string{"a", "b"}}},
		{spec: "Status:select", want: columnSpec{Name: "Status", Type: "select"}},
		{spec: "Owner", errHas: "NAME:TYPE"},
		{spec: ":user", errHas: "name"},
		{spec: "Owner:person", errHas: "unknown column type"},
		{spec: "Owner:user:alice", errHas: "only select and multi_select"},
		{spec: "Status:select:a,,b", errHas: "empty choice"},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := parseColumnSpec(tt.spec)
			if tt.errHas != "" {
				wantInvalidInput(t, err, tt.errHas)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSlugifyLabel(t *testing.T) {
	tests := map[string]string{
		"Done":            "done",
		"To do":           "to_do",
		"  In  Progress ": "in_progress",
		"Won't fix!":      "won_t_fix",
		"P0 / Blocker":    "p0_blocker",
		"ÄÖ":              "",
	}
	for in, want := range tests {
		if got := slugifyLabel(in); got != want {
			t.Errorf("slugifyLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildChoices(t *testing.T) {
	labels := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	got, err := buildChoices(labels)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 {
		t.Fatalf("got %d choices", len(got))
	}
	if got[0].Color != "blue" || got[10].Color != "gray" || got[11].Color != "blue" {
		t.Errorf("palette did not wrap: %v %v %v", got[0].Color, got[10].Color, got[11].Color)
	}
	if got[0].Value != "a" || got[0].Label != "a" {
		t.Errorf("choice 0 = %+v", got[0])
	}

	// An unsluggable label falls back to its position so the choice still
	// carries a non-empty value.
	got, err = buildChoices([]string{"🔥", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Value != "choice_1" || got[0].Label != "🔥" {
		t.Errorf("fallback value = %+v", got[0])
	}

	_, err = buildChoices([]string{"Done", "Done"})
	wantInvalidInput(t, err, "duplicate choice label")
	_, err = buildChoices([]string{"To do", "to-do"})
	wantInvalidInput(t, err, "collide")
}

func TestMergeChoices(t *testing.T) {
	existing := []listChoice{
		{Value: "OptAAA", Label: "To do", Color: "gray"},
		{Value: "done", Label: "Done", Color: "green"},
	}
	got, err := mergeChoices(existing, []string{"Done", "Blocked", "To do", "to do"})
	if err != nil {
		t.Fatal(err)
	}
	want := []listChoice{
		{Value: "done", Label: "Done", Color: "green"},     // preserved
		{Value: "blocked", Label: "Blocked", Color: "green"}, // new, palette[1]
		{Value: "OptAAA", Label: "To do", Color: "gray"},   // preserved, opaque id kept
		{Value: "to_do", Label: "to do", Color: "red"},      // case differs: a new choice, palette[3]
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}

	_, err = mergeChoices(existing, []string{"Done", "Done"})
	wantInvalidInput(t, err, "duplicate choice label")

	// A new label whose slug equals a preserved value is a collision, not a
	// silent overwrite of the surviving choice.
	_, err = mergeChoices(existing, []string{"Done", "DONE"})
	wantInvalidInput(t, err, "collide")

	_, err = mergeChoices(existing, nil)
	wantInvalidInput(t, err, "at least one")
}

func TestResolveColumn(t *testing.T) {
	schema := []listColumn{
		{ID: "Col01AAA", Key: "name", Name: "Name", Type: "text"},
		{ID: "Col01CCC", Key: "Col01BBB", Name: "Status", Type: "select"},
		{ID: "Col01HHH", Key: "Col01HHK", Name: "Name", Type: "text"},
		{ID: "Col01III", Key: "Col01IIK", Name: "Col01CCC", Type: "text"},
	}
	tests := []struct {
		in     string
		wantID string
		code   string
	}{
		{in: "Col01CCC", wantID: "Col01CCC"}, // id wins over a column named Col01CCC
		{in: "Col01BBB", wantID: "Col01CCC"},
		{in: "name", wantID: "Col01AAA"}, // key wins over the two display names
		{in: "status", wantID: "Col01CCC"},
		{in: "STATUS", wantID: "Col01CCC"},
		{in: "Name", code: "ambiguous_column"},
		{in: "Owner", code: "column_not_found"},
		{in: "", code: "column_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := resolveColumn(schema, tt.in)
			if tt.code != "" {
				if err == nil || err.Err != tt.code {
					t.Fatalf("err = %v, want %s", err, tt.code)
				}
				if err.Input != tt.in {
					t.Errorf("Input = %q, want %q", err.Input, tt.in)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tt.wantID {
				t.Errorf("resolved %s, want %s", got.ID, tt.wantID)
			}
		})
	}
}

func TestColumnOptions_PreserveUnknownKeys(t *testing.T) {
	var col listColumn
	raw := `{"id":"Col01CCC","name":"Status","key":"Col01BBB","type":"select","options":{"format":"single_select","default_value":"todo","choices":[{"value":"todo","label":"To do","color":"gray"}]}}`
	if err := json.Unmarshal([]byte(raw), &col); err != nil {
		t.Fatal(err)
	}
	if col.Options.Format != "single_select" || len(col.Options.Choices) != 1 {
		t.Fatalf("typed fields lost: %+v", col.Options)
	}

	// default_value survives when its choice survives...
	kept := choicesOptionsJSON(col, []listChoice{{Value: "todo", Label: "To do", Color: "gray"}, {Value: "done", Label: "Done", Color: "green"}})
	if kept != `{"choices":[{"value":"todo","label":"To do","color":"gray"},{"value":"done","label":"Done","color":"green"}],"default_value":"todo","format":"single_select"}` {
		t.Errorf("options = %s", kept)
	}
	// ...and is dropped when the choice it names is gone.
	dropped := choicesOptionsJSON(col, []listChoice{{Value: "done", Label: "Done", Color: "green"}})
	if strings.Contains(dropped, "default_value") {
		t.Errorf("stale default_value kept: %s", dropped)
	}

	// A select column with null options still gets a format from its type.
	plain := listColumn{Type: "multi_select"}
	if got := choicesOptionsJSON(plain, nil); got != `{"choices":[],"format":"multi_select"}` {
		t.Errorf("options = %s", got)
	}
}

func TestNormalizeIconArg(t *testing.T) {
	for in, want := range map[string]string{"rocket": "rocket", ":rocket:": "rocket", ":wave::skin-tone-2:": "wave::skin-tone-2"} {
		got, err := normalizeIconArg(in)
		if err != nil || got != want {
			t.Errorf("normalizeIconArg(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "  ", "::", "two words", ":rocket"} {
		if _, err := normalizeIconArg(in); err == nil {
			t.Errorf("normalizeIconArg(%q) accepted", in)
		}
	}
}
