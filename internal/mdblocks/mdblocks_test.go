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

// TestGolden_SingleTopLevelBlock pins the "exactly one rich_text" rule Desktop
// flattening forces, independent of what each fixture expects.
func TestGolden_SingleTopLevelBlock(t *testing.T) {
	inputs, _ := filepath.Glob("testdata/*.md")
	for _, in := range inputs {
		src, _ := os.ReadFile(in)
		got, err := mdblocks.Convert(src)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var blocks []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(got, &blocks); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if len(blocks) != 1 || blocks[0].Type != "rich_text" {
			t.Errorf("%s: want exactly one rich_text block, got %d (%v)", in, len(blocks), blocks)
		}
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
		{"thematic break", "above\n\n---\n\nbelow\n", 3, "horizontal rule"},
		{"image", "text\n\n![alt](https://example.com/a.png)\n", 3, "image"},
		{"html block", "para\n\n<div>x</div>\n", 3, "HTML"},
		{"inline html", "a <b>bold</b> tag\n", 1, "HTML"},
		{"table", "a\n\n| a | b |\n|---|---|\n| 1 | 2 |\n", 3, "table"},
		{"nested quote", "> outer\n> > inner\n", 2, "quote"},
		{"list in quote", "> a\n> - b\n", 2, "quote"},
		{"too deep", "- a\n  - b\n    - c\n      - d\n", 4, "nest"},
		{"loose item", "- a\n\n  second paragraph\n- b\n", 3, "one paragraph"},
		{"empty item", "- a\n-\n- c\n", 2, "empty list item"},
		{"item with only a nested list", "- a\n-\n  - child\n", 3, "own text"},
		{"empty quote", "a\n\n>\n", 3, "empty quote"},
		{"empty first item", "-\n- b\n", 1, "empty list item"},
		{"empty nested item", "- a\n\n  -\n", 3, "empty list item"},
		{"empty quote after blank lines", "\n\n>\n", 3, "empty quote"},
		{"empty fenced code block", "a\n\n```\n```\n", 3, "empty code block"},
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
		{"block after a fenced code block", "```\ncode\n```\n\n---\n", 5, "horizontal rule"},
		{"empty-label link on a later line", "a\nsee [](/docs)\n", 2, "link"},
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
