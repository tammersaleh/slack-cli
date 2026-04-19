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
