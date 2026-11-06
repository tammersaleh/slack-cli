package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// Divider blocks are the composer's horizontal rule. The composer stores
// them as bare {"type":"divider"} top-level blocks between rich_text runs
// (measured 2026-11-06, docs/divider-blocks-plan.md), a divider-only draft
// survives reconciliation, and consecutive dividers are stored as-is.

const divider = `{"type":"divider"}`
const dividerHeader = `{"type":"header","level":2,"text":{"type":"plain_text","text":"Mid","emoji":true}}`

func TestValidateBlockShapes_AcceptsDividers(t *testing.T) {
	cases := []struct {
		name, blocks string
	}{
		{"divider only", `[` + divider + `]`},
		{"leading divider", `[` + divider + `,` + headerSection + `]`},
		{"trailing divider", `[` + headerSection + `,` + divider + `]`},
		{"consecutive dividers", `[` + headerSection + `,` + divider + `,` + divider + `,` + headerList + `]`},
		{"composer shape: rich_text, divider, rich_text, divider, rich_text", `[` + headerSection + `,` + divider + `,` + headerSection + `,` + divider + `,` + headerList + `]`},
		{"measured mixed sequence", `[` + divider + `,` + headerSection + `,` + divider + `,` + divider + `,` + dividerHeader + `,` + divider + `]`},
		{"server-assigned block_id passes through", `[{"type":"divider","block_id":"abc12"}]`},
		{"unknown extra field passes through", `[{"type":"divider","future":{"x":1}}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hasBody, err := validateBlockShapes([]byte(tc.blocks))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !hasBody {
				t.Error("a divider is renderable content; hasBody should be true")
			}
		})
	}
}

// The generic rejection names every allowed top-level type.
func TestValidateBlockShapes_GenericRejectionNamesAllThreeTypes(t *testing.T) {
	for _, blocks := range []string{
		`[{"type":"section","text":{"type":"mrkdwn","text":"x"}}]`,
		`[{"type":"context","elements":[]}]`,
		`[` + divider + `,{"type":"actions","elements":[]}]`,
	} {
		_, err := validateBlockShapes([]byte(blocks))
		var oe *output.Error
		if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
			t.Errorf("%s: expected invalid_blocks, got %v", blocks, err)
			continue
		}
		if !strings.Contains(oe.Detail, "rich_text, header, and divider") {
			t.Errorf("%s: generic rejection should name all three allowed types, got %q", blocks, oe.Detail)
		}
	}
}

func TestErrNoRenderableContent_NamesDivider(t *testing.T) {
	var oe *output.Error
	if !errors.As(errNoRenderableContent(), &oe) {
		t.Fatal("want *output.Error")
	}
	if !strings.Contains(oe.Detail, "divider") {
		t.Errorf("renderable-content rejection should name divider as an option, got %q", oe.Detail)
	}
}

// Whether a divider is an absorption boundary in Desktop is pending a check
// (docs/divider-blocks-plan.md). Until it lands, the validator carries the
// section-before-container state across a divider, so
// [section without "\n", divider, list] is rejected exactly like the same
// section and list with nothing between them (the control case), while a
// terminated section before a divider and a list passes.
func TestValidateBlockShapes_DividerCarriesAbsorptionState(t *testing.T) {
	unterminated := `{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"no newline"}]}]}`

	if _, err := validateBlockShapes([]byte(`[` + headerSection + `,` + divider + `,` + headerList + `]`)); err != nil {
		t.Fatalf("terminated section, divider, list should pass, got %v", err)
	}

	var oe *output.Error
	_, err := validateBlockShapes([]byte(`[` + unterminated + `,` + divider + `,` + headerList + `]`))
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("unterminated section, divider, list: want invalid_blocks (pending Desktop check), got %v", err)
	}
	if !strings.Contains(oe.Detail, "blocks[0].elements[0]") {
		t.Errorf("detail should name the section at blocks[0].elements[0], got %q", oe.Detail)
	}

	// Control: the same section and list with nothing between them.
	_, err = validateBlockShapes([]byte(`[` + unterminated + `,` + headerList + `]`))
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("unterminated section directly before list: want invalid_blocks, got %v", err)
	}
}
