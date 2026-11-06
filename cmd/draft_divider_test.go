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
