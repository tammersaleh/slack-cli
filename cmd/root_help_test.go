package cmd_test

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
)

// Workspace selection is by team ID only (ResolveCredentials looks the value
// up as the credentials map key), so the flag help must not promise names.
func TestWorkspaceFlagHelp_IDOnly(t *testing.T) {
	var cli cmd.CLI
	parser, err := kong.New(&cli, kong.Name("slack"))
	if err != nil {
		t.Fatal(err)
	}
	var help string
	for _, f := range parser.Model.Flags {
		if f.Name == "workspace" {
			help = f.Help
		}
	}
	if help == "" {
		t.Fatal("no --workspace flag found")
	}
	for _, phrase := range []string{"name or id", "by name", "or name"} {
		if strings.Contains(strings.ToLower(help), phrase) {
			t.Errorf("--workspace help promises name matching, which does not exist: %q", help)
		}
	}
	if !strings.Contains(help, "team ID") {
		t.Errorf("--workspace help should say the value is a team ID: %q", help)
	}
}

// Slack's per-emoji reaction filters aren't guessable from the generic
// has:reaction, so the search help must name them.
func TestSearchMessagesHelp_EmojiReactionModifiers(t *testing.T) {
	help := cmd.SearchMessagesCmd{}.Help()
	for _, want := range []string{"has::eyes:", "has:my::eyes:"} {
		if !strings.Contains(help, want) {
			t.Errorf("search messages help should mention %q", want)
		}
	}
}
