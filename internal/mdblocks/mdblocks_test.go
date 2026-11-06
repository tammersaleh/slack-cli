package mdblocks_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/mdblocks"
)

// TestGolden converts every testdata/*.md and compares the result, byte for
// byte after canonical re-indentation, with the sibling .json file. Exact
// JSON is the point: a shape-only check cannot tell reordered containers,
// lost styles, or a wrong list depth from a right answer.
func TestGolden(t *testing.T) {
	inputs, err := filepath.Glob("testdata/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no golden inputs found")
	}
	for _, in := range inputs {
		name := strings.TrimSuffix(filepath.Base(in), ".md")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(in, ".md") + ".json")
			if err != nil {
				t.Fatal(err)
			}
			got, err := mdblocks.Convert(src)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if g, w := canonical(t, got), canonical(t, want); g != w {
				t.Errorf("mismatch\n--- got ---\n%s\n--- want ---\n%s", g, w)
			}
		})
	}
}

// TestGolden_TopLevelShape pins the block-level invariants independent of
// what each fixture expects: a source with no heading or thematic break
// converts to exactly one rich_text block (Desktop flattens adjacent ones);
// otherwise every top-level block is a header, a divider, or a rich_text, no
// rich_text is empty, and no two rich_text blocks are adjacent (each run of
// containers is one block).
func TestGolden_TopLevelShape(t *testing.T) {
	inputs, err := filepath.Glob("testdata/*.md")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no golden inputs: %v", err)
	}
	for _, in := range inputs {
		t.Run(strings.TrimSuffix(filepath.Base(in), ".md"), func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := mdblocks.Convert(src)
			if err != nil {
				t.Fatal(err)
			}
			var blocks []struct {
				Type     string            `json:"type"`
				Elements []json.RawMessage `json:"elements"`
			}
			if err := json.Unmarshal(got, &blocks); err != nil {
				t.Fatal(err)
			}
			if len(blocks) == 0 {
				t.Fatal("no blocks")
			}
			boundaries := 0
			for i, b := range blocks {
				switch b.Type {
				case "header", "divider":
					boundaries++
				case "rich_text":
					if len(b.Elements) == 0 {
						t.Errorf("blocks[%d] is an empty rich_text", i)
					}
					if i > 0 && blocks[i-1].Type == "rich_text" {
						t.Errorf("blocks[%d] and blocks[%d] are adjacent rich_text blocks", i-1, i)
					}
				default:
					t.Errorf("blocks[%d] has type %q; only header, divider, and rich_text are allowed", i, b.Type)
				}
			}
			if boundaries == 0 && len(blocks) != 1 {
				t.Errorf("no header or divider, want exactly one rich_text block, got %d", len(blocks))
			}
		})
	}
}

// Headings become header blocks whose text is plain_text, so inline
// formatting flattens and anything plain_text cannot carry is an error.
func TestConvert_HeadingErrors(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		line   int
		detail string
	}{
		{"link", "# See [docs](https://example.com)\n", 1, "link"},
		{"bare url", "# Visit https://example.com now\n", 1, "link"},
		{"email", "# Mail alice@example.com\n", 1, "link"},
		{"angle autolink", "# <https://example.com>\n", 1, "link"},
		{"user mention", "# Hi <@U01XYZ>\n", 1, "mention"},
		{"channel mention", "# In <#C01ABC>\n", 1, "channel"},
		{"broadcast", "# Hey @here\n", 1, "broadcast"},
		{"symbolic mention", "# Hi @alice\n", 1, "<@U"},
		{"image", "# ![alt](https://example.com/a.png)\n", 1, "image"},
		{"inline html", "# a <b>bold</b> tag\n", 1, "HTML"},
		{"empty atx heading", "intro\n\n#\n", 3, "empty heading"},
		{"empty atx heading with trailing hashes", "intro\n\n## ##\n", 3, "empty heading"},
		{"error on a later heading line", "# ok\n\ntext\n\n## see https://example.com\n", 5, "link"},
		// A Setext heading spans lines; the error names the inline's line.
		{"link on the second line of a setext heading", "Title line\nsee https://example.com\n====\n", 2, "link"},
		{"mention on the second line of a setext heading", "Title line\nfor <@U01XYZ>\n====\n", 2, "mention"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mdblocks.Convert([]byte(tc.src))
			if err == nil {
				t.Fatal("expected error")
			}
			var me *mdblocks.Error
			if !errors.As(err, &me) {
				t.Fatalf("want *mdblocks.Error, got %T: %v", err, err)
			}
			if me.Line != tc.line {
				t.Errorf("line: got %d want %d (%v)", me.Line, tc.line, err)
			}
			if !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("detail %q does not mention %q", err.Error(), tc.detail)
			}
		})
	}
}

// A heading inside a quote or a list item has no shape (a header block is
// top-level only) and keeps the container's existing rejection.
func TestConvert_HeadingInsideContainerIsRejected(t *testing.T) {
	cases := []struct {
		name, src, detail string
		line              int
	}{
		{"quote", "> # quoted heading\n", "quote may hold", 1},
		{"list", "- item\n\n  # heading in item\n", "list item may hold", 3},
		{"thematic break in a list item", "- item\n\n  ***\n", "list item may hold", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mdblocks.Convert([]byte(tc.src))
			var me *mdblocks.Error
			if !errors.As(err, &me) {
				t.Fatalf("want *mdblocks.Error, got %T: %v", err, err)
			}
			if me.Line != tc.line {
				t.Errorf("line: got %d want %d (%v)", me.Line, tc.line, err)
			}
			if !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("detail %q does not mention %q", err.Error(), tc.detail)
			}
		})
	}
}

func TestConvert_Deterministic(t *testing.T) {
	src, _ := os.ReadFile("testdata/tokens.md")
	first, err := mdblocks.Convert(src)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, _ := mdblocks.Convert(src)
		if !bytes.Equal(first, again) {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestConvert_Errors(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		line   int
		detail string
	}{
		{"empty", "   \n\n", 0, "no content"},
		{"image", "text\n\n![alt](https://example.com/a.png)\n", 3, "image"},
		{"html block", "para\n\n<div>x</div>\n", 3, "HTML"},
		{"inline html", "a <b>bold</b> tag\n", 1, "HTML"},
		{"table", "a\n\n| a | b |\n|---|---|\n| 1 | 2 |\n", 3, "table"},
		{"nested quote", "> outer\n> > inner\n", 2, "quote inside a quote"},
		{"nested quote after a quoted list", "> - a\n>\n> > inner\n", 3, "quote inside a quote"},
		{"too deep", "- a\n  - b\n    - c\n      - d\n", 4, "nest"},
		{"loose item", "- a\n\n  second paragraph\n- b\n", 3, "one paragraph"},
		{"empty item", "- a\n-\n- c\n", 2, "empty list item"},
		{"item with only a nested list", "- a\n-\n  - child\n", 3, "own text"},
		{"empty quote", "a\n\n>\n", 3, "empty quote"},
		{"empty first item", "-\n- b\n", 1, "empty list item"},
		{"empty nested item", "- a\n\n  -\n", 3, "empty list item"},
		{"empty quote after blank lines", "\n\n>\n", 3, "empty quote"},
		{"empty fenced code block", "a\n\n```\n```\n", 3, "empty code block"},
		{"empty fenced code block in a quote", "> a\n>\n> ```\n> ```\n", 3, "empty code block"},
		{"quote holding only an empty code block", "> ```\n> ```\n", 1, "empty code block"},
		{"empty list item in a quote", "> - a\n> -\n", 2, "empty list item"},
		{"too deep inside a quote", "> - a\n>   - b\n>     - c\n>       - d\n", 4, "nest"},
		{"thematic break in a quote", "> a\n>\n> ---\n", 3, "paragraphs, lists, and code blocks"},
		{"relative link", "see [x](/docs)\n", 1, "link"},
		{"unsafe scheme", "see [x](javascript:alert(1))\n", 1, "link"},
		{"symbolic mention", "hi @alice, welcome\n", 1, "<@U"},
		{"angle mention with a name", "a <@alice> b\n", 1, "<@U"},
		{"mention in link text", "[hi <@U01XYZ>](https://example.com)\n", 1, "link text"},
		{"image in link text", "[![alt](https://example.com/i.png)](https://example.com)\n", 1, "image"},
		{"angle mention with a label", "a <@U01XYZ|alice> b\n", 1, "<@U"},
		{"plural broadcast is a mention", "tell @channels\n", 1, "<@U"},
		{"inline error after a sibling on a later line", "a\nsee <ftp://example.com>\n", 2, "link"},
		{"inline error with no text of its own", "x ![](https://example.com/i.png)\n", 1, "image"},
		{"bad mention on a later line", "first\nsecond @bob\n", 2, "<@U"},
		{"error inside the second list item", "- ok\n- see ![](https://example.com/i.png)\n", 2, "image"},
		{"empty-label link on a later line", "a\nsee [](/docs)\n", 2, "link"},
		{"block after a fenced code block", "```\ncode\n```\n\n![](https://example.com/i.png)\n", 5, "image"},
		// A thematic break owns no source segment, so a segment-less node
		// after one must still be attributed to its own line, not the rule's.
		{"empty heading after a divider", "intro\n\n---\n\n#\n", 5, "empty heading"},
		{"empty heading after a leading divider", "---\n\n#\n", 3, "empty heading"},
		{"empty code block after a divider", "---\n\n```\n```\n", 3, "empty code block"},
		{"empty quote after a divider", "a\n\n***\n\n>\n", 5, "empty quote"},
		{"empty item after two dividers", "a\n\n---\n\n___\n\n-\n", 7, "empty list item"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mdblocks.Convert([]byte(tc.src))
			if err == nil {
				t.Fatal("expected error")
			}
			var me *mdblocks.Error
			if !errors.As(err, &me) {
				t.Fatalf("want *mdblocks.Error, got %T: %v", err, err)
			}
			if tc.line > 0 && me.Line != tc.line {
				t.Errorf("line: got %d want %d (%v)", me.Line, tc.line, err)
			}
			if !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("detail %q does not mention %q", err.Error(), tc.detail)
			}
		})
	}
}

// Not-a-token cases that must stay literal text, pinned individually so a
// regression names the exact boundary rule it broke.
func TestConvert_LiteralBoundaries(t *testing.T) {
	cases := map[string]string{
		"time is not emoji":            "at 10:30:45 sharp",
		"colon space":                  "note: done",
		"word-glued colon":             "ratio:x: fine",
		"escaped broadcast is literal": "say \\@here loudly",
		"unterminated angle":           "a <@U01XYZ with no close",
		"empty angle brackets":         "a <> b",
		"emoji right boundary":         ":wave:s is a word",
		"at glued to a word":           "foo@here stays",
		"at before space":              "lonely @ sign",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := mdblocks.Convert([]byte(src))
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			want := `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":` +
				mustJSON(strings.ReplaceAll(src, "\\@", "@")) + `}]}]}]`
			if canonical(t, got) != canonical(t, []byte(want)) {
				t.Errorf("got %s\nwant %s", got, want)
			}
		})
	}
}

// An email address is linkified (Slack would do the same on send), and it is
// never read as a mention.
func TestConvert_EmailIsALinkNotAMention(t *testing.T) {
	got, err := mdblocks.Convert([]byte("mail alice@example.com now"))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[
		{"type":"text","text":"mail "},
		{"type":"link","url":"mailto:alice@example.com","text":"alice@example.com"},
		{"type":"text","text":" now"}]}]}]`
	if canonical(t, got) != canonical(t, []byte(want)) {
		t.Errorf("got %s", got)
	}
}

// Edge cases that must round-trip exactly, each pinned to the rule it
// exercises.
func TestConvert_EdgeCases(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"literal backslash before a two-space break", "foo\\  \nbar", `[{"type":"text","text":"foo\\\nbar"}]`},
		{"numeric reference is not re-decoded", "a &#38;amp; b", `[{"type":"text","text":"a &amp; b"}]`},
		{"double backslash", "a \\\\ b", `[{"type":"text","text":"a \\ b"}]`},
		{"trailing lone backslash at end of document", "a \\", `[{"type":"text","text":"a \\"}]`},
		{"hex reference", "&#x41;", `[{"type":"text","text":"A"}]`},
		{"unknown entity stays literal", "&nosuch; and & alone", `[{"type":"text","text":"&nosuch; and & alone"}]`},
		{"over-long numeric reference stays literal", "&#0000000065; &#x0000041;", `[{"type":"text","text":"&#0000000065; &#x0000041;"}]`},
		{"degenerate references stay literal", "&; &#; &#x; &", `[{"type":"text","text":"&; &#; &#x; &"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mdblocks.Convert([]byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			want := `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":` + tc.want + `}]}]`
			if canonical(t, got) != canonical(t, []byte(want)) {
				t.Errorf("got  %s\nwant %s", got, want)
			}
		})
	}
}

func TestConvert_ZeroStartClampsOffset(t *testing.T) {
	got, err := mdblocks.Convert([]byte("0. zero\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"rich_text","elements":[
		{"type":"rich_text_list","style":"ordered","indent":0,"offset":0,"elements":[
			{"type":"rich_text_section","elements":[{"type":"text","text":"zero"}]}]}]}]`
	if canonical(t, got) != canonical(t, []byte(want)) {
		t.Errorf("got %s", got)
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func canonical(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	out, _ := json.MarshalIndent(v, "", " ")
	return string(out)
}

// Goldmark parses `Title\n---` as a Setext H2 and `Title\n\n---` as a
// paragraph followed by a thematic break, so the blank line decides between
// a header and a divider.
func TestConvert_SetextVersusDivider(t *testing.T) {
	setext, err := mdblocks.Convert([]byte("Title\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"level":2,"text":{"emoji":true,"text":"Title","type":"plain_text"},"type":"header"}]`; canonical(t, setext) != canonical(t, []byte(want)) {
		t.Errorf("Title\\n--- should be a Setext H2 header, got %s", setext)
	}

	divided, err := mdblocks.Convert([]byte("Title\n\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"Title\n"}]}]},{"type":"divider"}]`
	if canonical(t, divided) != canonical(t, []byte(want)) {
		t.Errorf("Title, blank, --- should be a paragraph then a divider, got %s", divided)
	}
}
