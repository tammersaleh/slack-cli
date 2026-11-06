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
		{"missing text", `[{"type":"header","level":1}]`, "text"},
		{"text is a string", `[{"type":"header","level":1,"text":"Hi"}]`, "text"},
		{"mrkdwn text", `[{"type":"header","level":1,"text":{"type":"mrkdwn","text":"*Hi*"}}]`, "plain_text"},
		{"empty text", `[{"type":"header","level":1,"text":{"type":"plain_text","text":""}}]`, "empty"},
		{"text.text missing", `[{"type":"header","level":1,"text":{"type":"plain_text"}}]`, "text"},
		{"level zero", `[{"type":"header","level":0,"text":{"type":"plain_text","text":"Hi"}}]`, "level"},
		{"level negative", `[{"type":"header","level":-1,"text":{"type":"plain_text","text":"Hi"}}]`, "level"},
		{"level fractional", `[{"type":"header","level":1.5,"text":{"type":"plain_text","text":"Hi"}}]`, "level"},
		{"level string", `[{"type":"header","level":"2","text":{"type":"plain_text","text":"Hi"}}]`, "level"},
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

// A header is not an absorption boundary: whether Desktop flattens rich_text
// blocks across a header is unverified, so the section-before-container rule
// still applies through one. Only the trailing "\n" clears it.
func TestValidateBlockShapes_HeaderDoesNotResetAbsorption(t *testing.T) {
	unterminated := `{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"no newline"}]}]}`
	header := `{"type":"header","level":2,"text":{"type":"plain_text","text":"Mid","emoji":true}}`

	_, err := validateBlockShapes([]byte(`[` + unterminated + `,` + header + `,` + headerList + `]`))
	if err == nil {
		t.Fatal("expected invalid_blocks: the section before the header lacks a trailing newline")
	}
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("want invalid_blocks *output.Error, got %v", err)
	}
	if !strings.Contains(oe.Detail, `\n`) {
		t.Errorf("detail should name the trailing-newline fix, got %q", oe.Detail)
	}

	if _, err := validateBlockShapes([]byte(`[` + headerSection + `,` + header + `,` + headerList + `]`)); err != nil {
		t.Fatalf("terminated section before a header then a list should pass, got %v", err)
	}
}

// Everything that is not rich_text or header keeps its rejection.
func TestValidateBlockShapes_StillRejectsOtherTypes(t *testing.T) {
	for _, blocks := range []string{
		`[{"type":"section","text":{"type":"mrkdwn","text":"x"}}]`,
		`[{"type":"divider"}]`,
		`[{"type":"context","elements":[]}]`,
	} {
		if _, err := validateBlockShapes([]byte(blocks)); err == nil {
			t.Errorf("%s: expected invalid_blocks", blocks)
		}
	}
}
