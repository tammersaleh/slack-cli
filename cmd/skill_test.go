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
		"slack user manager-chain",
		"slack saved list",
		"slack section list",
		"slack file list",
	} {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected %q in skill output", cmd)
		}
	}
}

// TestSkill_AntiGuessGrammar guards the fix for issues/skill-not-triggering.md:
// the frontmatter description (the only text always in the skill catalog) must
// steer the model away from Slack Web-API method names, and the body must carry
// the corrective mapping table for the recurring wrong guesses.
func TestSkill_AntiGuessGrammar(t *testing.T) {
	out := readSkill(t)

	// The always-in-context description must tell the model to load before
	// running slack and that this CLI is not the Slack Web API.
	desc := frontmatterField(t, out, "description")
	for _, want := range []string{
		"BEFORE running",
		"slack thread list",
		"conversations.replies",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("expected description to contain %q, got %q", want, desc)
		}
	}

	// The body table must pair each recurring wrong guess with the right
	// command on the SAME row. Checking substring membership over the whole
	// doc is not enough: the right-hand commands and two of the wrong guesses
	// already appear elsewhere (command reference, the description), so a
	// scrambled or truncated table would still pass. Scope to the Grammar
	// section and require both halves on one table row.
	grammar := sectionBody(t, out, "## Grammar")
	for _, pair := range [][2]string{
		{"conversations replies <channel> <ts>", "slack thread list <channel> <ts>"},
		{"channel find <name>", "slack channel list --query <name>"},
		{"message read <channel> <ts>", "slack message get <channel> <ts>"},
		{"message list <channel> --thread <ts>", "slack thread list <channel> <ts>"},
	} {
		if !rowPairs(grammar, pair[0], pair[1]) {
			t.Errorf("Grammar table missing row mapping %q -> %q", pair[0], pair[1])
		}
	}
}

// sectionBody returns the lines of a Markdown section (from its `## Heading`
// up to the next `## ` heading or EOF).
func sectionBody(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("no %q section", heading)
	}
	rest := doc[start+len(heading):]
	before, _, _ := strings.Cut(rest, "\n## ")
	return before
}

// rowPairs reports whether a single line in body contains both substrings -
// i.e. they sit on the same table row.
func rowPairs(body, wrong, right string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.Contains(line, wrong) && strings.Contains(line, right) {
			return true
		}
	}
	return false
}

// frontmatterField extracts a top-level `key: value` from the YAML frontmatter.
func frontmatterField(t *testing.T, doc, key string) string {
	t.Helper()
	end := strings.Index(doc[4:], "\n---")
	if !strings.HasPrefix(doc, "---\n") || end < 0 {
		t.Fatal("no frontmatter block")
	}
	fm := doc[4 : end+4]
	for line := range strings.SplitSeq(fm, "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("frontmatter has no %q field", key)
	return ""
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
		// --full is the steer for any person lookup beyond a name.
		"profile.fields: []",
		"custom_fields",
		"value_name",
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
		// data_table (interactive variant) is NOT draftable - the skill must
		// warn so agents don't reach for it.
		"data_table",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected skill output to mention %q for draft rendering guidance", want)
		}
	}
}
