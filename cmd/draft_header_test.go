package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// Header blocks are the one non-rich_text top-level type a draft keeps. The
// composer itself emits them (measured 2026-11-06, docs/header-blocks-plan.md),
// so the validator accepts the shape it stores and rejects the shapes Slack
// answers invalid_message for.

const headerSection = `{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"para\n"}]}]}`
const headerList = `{"type":"rich_text","elements":[{"type":"rich_text_list","style":"bullet","indent":0,"elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"item"}]}]}]}`

func TestValidateBlockShapes_AcceptsHeaders(t *testing.T) {
	cases := []struct {
		name, blocks string
	}{
		{"composer shape, level 1", `[{"type":"header","level":1,"text":{"type":"plain_text","text":"Big","emoji":true}}]`},
		{"level 3", `[{"type":"header","level":3,"text":{"type":"plain_text","text":"Small","emoji":true}}]`},
		{"level 4 has no upper bound", `[{"type":"header","level":4,"text":{"type":"plain_text","text":"Deep","emoji":true}}]`},
		// JSON 1.0 and 1e0 decode to the same float64 as 1; the check is
		// "integer-valued", not "spelled as an integer".
		{"level 1.0 is integer-valued", `[{"type":"header","level":1.0,"text":{"type":"plain_text","text":"One","emoji":true}}]`},
		{"level 2e0 is integer-valued", `[{"type":"header","level":2e0,"text":{"type":"plain_text","text":"Two","emoji":true}}]`},
		{"level omitted", `[{"type":"header","text":{"type":"plain_text","text":"No level","emoji":true}}]`},
		{"emoji omitted", `[{"type":"header","level":2,"text":{"type":"plain_text","text":"No emoji"}}]`},
		{"server-assigned block_id passes through", `[{"type":"header","block_id":"abc12","level":1,"text":{"type":"plain_text","text":"Hi","emoji":true}}]`},
		{"header between rich_text blocks", `[` + headerSection + `,{"type":"header","level":2,"text":{"type":"plain_text","text":"Mid","emoji":true}},` + headerList + `]`},
		{"header before rich_text", `[{"type":"header","level":1,"text":{"type":"plain_text","text":"Hi","emoji":true}},` + headerList + `]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hasBody, err := validateBlockShapes([]byte(tc.blocks))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !hasBody {
				t.Error("a header is renderable content; hasBody should be true")
			}
		})
	}
}

func TestValidateBlockShapes_RejectsMalformedHeaders(t *testing.T) {
	cases := []struct {
		name, blocks, detail string
	}{
		// Each expected fragment is unique to one branch of validateHeaderBlock,
		// so a row cannot pass by hitting a neighbouring rejection.
		{"missing text", `[{"type":"header","level":1}]`, `without a "text" object`},
		{"text is a string", `[{"type":"header","level":1,"text":"Hi"}]`, `without a "text" object`},
		{"mrkdwn text", `[{"type":"header","level":1,"text":{"type":"mrkdwn","text":"*Hi*"}}]`, `only "plain_text"`},
		{"empty text", `[{"type":"header","level":1,"text":{"type":"plain_text","text":""}}]`, "empty text.text"},
		{"text.text missing", `[{"type":"header","level":1,"text":{"type":"plain_text"}}]`, "empty text.text"},
		{"text.text not a string", `[{"type":"header","level":1,"text":{"type":"plain_text","text":7}}]`, "empty text.text"},
		{"level zero", `[{"type":"header","level":0,"text":{"type":"plain_text","text":"Hi"}}]`, "level must be an integer >= 1"},
		{"level negative", `[{"type":"header","level":-1,"text":{"type":"plain_text","text":"Hi"}}]`, "level must be an integer >= 1"},
		{"level fractional", `[{"type":"header","level":1.5,"text":{"type":"plain_text","text":"Hi"}}]`, "level must be an integer >= 1"},
		{"level string", `[{"type":"header","level":"2","text":{"type":"plain_text","text":"Hi"}}]`, "level must be an integer >= 1"},
		{"level null", `[{"type":"header","level":null,"text":{"type":"plain_text","text":"Hi"}}]`, "level must be an integer >= 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateBlockShapes([]byte(tc.blocks))
			if err == nil {
				t.Fatal("expected invalid_blocks")
			}
			var oe *output.Error
			if !errors.As(err, &oe) {
				t.Fatalf("want *output.Error, got %T: %v", err, err)
			}
			if oe.Err != "invalid_blocks" {
				t.Errorf("err = %q, want invalid_blocks", oe.Err)
			}
			if !strings.Contains(oe.Detail, "blocks[0]") {
				t.Errorf("detail should name the block index, got %q", oe.Detail)
			}
			if !strings.Contains(oe.Detail, tc.detail) {
				t.Errorf("detail %q should mention %q", oe.Detail, tc.detail)
			}
		})
	}
}

// A header is an absorption boundary. Desktop renders
// [section without "\n", header, list] as a paragraph, a heading, and a
// bullet with no glue (inspected 2026-11-06), so the section-before-container
// state resets at a valid header. Without a header between them the rule
// still applies.
func TestValidateBlockShapes_HeaderResetsAbsorption(t *testing.T) {
	unterminated := `{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"no newline"}]}]}`
	header := `{"type":"header","level":2,"text":{"type":"plain_text","text":"Mid","emoji":true}}`

	if _, err := validateBlockShapes([]byte(`[` + unterminated + `,` + header + `,` + headerList + `]`)); err != nil {
		t.Fatalf("unterminated section, header, list should pass: a header breaks absorption, got %v", err)
	}

	if _, err := validateBlockShapes([]byte(`[` + headerSection + `,` + header + `,` + headerList + `]`)); err != nil {
		t.Fatalf("terminated section before a header then a list should pass, got %v", err)
	}

	// Same section and list in one rich_text with no header between them:
	// still rejected.
	oneBlock := `{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"no newline"}]},{"type":"rich_text_list","style":"bullet","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"item"}]}]}]}`
	_, err := validateBlockShapes([]byte(`[` + oneBlock + `]`))
	if err == nil {
		t.Fatal("expected invalid_blocks: unterminated section directly before a list with no header between")
	}
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("want invalid_blocks *output.Error, got %v", err)
	}
	if !strings.Contains(oe.Detail, `\n`) {
		t.Errorf("detail should name the trailing-newline fix, got %q", oe.Detail)
	}

	// Across two rich_text blocks with no header between them: still rejected
	// (Desktop flattens adjacent rich_text blocks).
	if _, err := validateBlockShapes([]byte(`[` + unterminated + `,` + headerList + `]`)); err == nil {
		t.Fatal("expected invalid_blocks: unterminated section then list across adjacent rich_text blocks")
	}
}

// Everything that is not rich_text or header keeps the generic rejection,
// whose message now names both allowed types.
func TestValidateBlockShapes_StillRejectsOtherTypes(t *testing.T) {
	for _, blocks := range []string{
		`[{"type":"section","text":{"type":"mrkdwn","text":"x"}}]`,
		`[{"type":"divider"}]`,
		`[{"type":"context","elements":[]}]`,
	} {
		_, err := validateBlockShapes([]byte(blocks))
		var oe *output.Error
		if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
			t.Errorf("%s: expected invalid_blocks, got %v", blocks, err)
			continue
		}
		if !strings.Contains(oe.Detail, "rich_text and header") {
			t.Errorf("%s: generic rejection should name both allowed types, got %q", blocks, oe.Detail)
		}
	}
}
