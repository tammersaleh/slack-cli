package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
)

func TestSkill_Output(t *testing.T) {
	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer

	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}

	kctx, err := parser.Parse([]string{"skill", "--binary", "/usr/local/bin/slack"})
	if err != nil {
		t.Fatal(err)
	}

	cli.SetOutput(&outBuf, &errBuf)
	if err := kctx.Run(&cli); err != nil {
		t.Fatal(err)
	}

	out := outBuf.String()

	// Frontmatter present.
	if !strings.HasPrefix(out, "---\n") {
		t.Error("expected YAML frontmatter")
	}
	if !strings.Contains(out, "name: slack-cli") {
		t.Error("expected skill name in frontmatter")
	}

	// Binary path in allowed-tools.
	if !strings.Contains(out, "Bash(/usr/local/bin/slack *)") {
		t.Error("expected binary path in allowed-tools")
	}

	// Key commands documented.
	for _, cmd := range []string{
		"slack message list",
		"slack search messages",
		"slack channel list",
		"slack user info",
		"slack saved list",
		"slack section list",
		"slack file list",
	} {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected %q in skill output", cmd)
		}
	}
}

// TestSkill_DiscoverabilityContent asserts the sections that exist to
// let agents self-recover from errors, compose commands, and understand
// the CLI's conventions without external help. If one of these goes
// missing, an agent is more likely to dead-end.
func TestSkill_DiscoverabilityContent(t *testing.T) {
	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	kctx, err := parser.Parse([]string{"skill"})
	if err != nil {
		t.Fatal(err)
	}
	cli.SetOutput(&outBuf, &errBuf)
	if err := kctx.Run(&cli); err != nil {
		t.Fatal(err)
	}
	out := outBuf.String()

	for _, want := range []string{
		// Errors catalog with hint field explained.
		"## Errors",
		"`hint`",
		"`channel_not_found`",
		"`draft_not_found`",
		"`invalid_timestamp`",
		// Exit codes documented so agents know what each non-zero code means.
		"### Exit codes",
		"2 - authentication",
		"3 - rate limited",
		// Workflows give composition examples.
		"## Workflows",
		// Search modifiers listed so agents don't have to guess.
		"`from:@user`",
		"`after:YYYY-MM-DD`",
		"`has:link`",
		// Channel types explained (mpim/im are not self-documenting).
		"`mpim`",
		"`im`",
		// User resolution gotcha - email + Grid + session token.
		"Enterprise Grid",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected skill output to contain %q for discoverability", want)
		}
	}
}

// TestSkill_DraftGuidance covers the known Drafts-panel rendering quirks
// that must be in the agent-facing skill. Missing any of these produces
// drafts that look fine on the wire but render wrong in Slack Desktop's
// Drafts compose editor.
func TestSkill_DraftGuidance(t *testing.T) {
	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	kctx, err := parser.Parse([]string{"skill"})
	if err != nil {
		t.Fatal(err)
	}
	cli.SetOutput(&outBuf, &errBuf)
	if err := kctx.Run(&cli); err != nil {
		t.Fatal(err)
	}
	out := outBuf.String()

	for _, want := range []string{
		// Non-rich_text blocks get stripped from the Drafts compose editor.
		"Drafts compose editor",
		// Cross-block absorption when multiple rich_text blocks are flattened.
		"flattens",
		// The working pattern for multi-paragraph prose with visual bullets.
		"containing one `rich_text_section`",
		// The literal bullet character so agents know what "visual bullets" means.
		"•",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected skill output to mention %q for draft rendering guidance", want)
		}
	}
}
