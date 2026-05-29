package cmd_test

import (
	"os"
	"strings"
	"testing"
)

const skillPath = "../skills/slack-cli/SKILL.md"

func readSkill(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read %s: %v", skillPath, err)
	}
	return string(data)
}

func TestSkill_Frontmatter(t *testing.T) {
	out := readSkill(t)

	if !strings.HasPrefix(out, "---\n") {
		t.Error("expected YAML frontmatter")
	}
	if !strings.Contains(out, "name: slack-cli") {
		t.Error("expected skill name in frontmatter")
	}
	// PATH-relative binary so a single checked-in SKILL.md works on every
	// agent host.
	if !strings.Contains(out, "Bash(slack *)") {
		t.Error("expected allowed-tools to invoke 'slack' via PATH")
	}

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
	out := readSkill(t)

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
	out := readSkill(t)

	for _, want := range []string{
		// Non-rich_text blocks get stripped from the Drafts compose editor.
		"Drafts compose editor",
		// Cross-block absorption when multiple rich_text blocks are flattened.
		"flattens",
		// The canonical Slack-editor shape: one top-level rich_text.
		"One top-level `rich_text`",
		// The absorption rule's fix: trailing newline on the heading section.
		"must end with `\\n`",
		// The literal bullet fallback is still documented as an escape hatch.
		"•",
		// markdown block fails hard at drafts.create - the highest-value warning.
		"internal_error",
		// section+mrkdwn looks like it works but tombstones into a leaked draft.
		"draft_delete_invalid",
		// Tables are draftable via attachments (not top-level blocks). The
		// skill must show the attachment shape and the --table helper, plus
		// the inline monospace fallback. Assert content, not heading level.
		"Tabular data",
		"attachments[].blocks[]",
		"--table FILE",
		"monospace ASCII",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected skill output to mention %q for draft rendering guidance", want)
		}
	}
}
