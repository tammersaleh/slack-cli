package cmd_test

import (
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
)

func mustParse(t *testing.T, args ...string) (*cmd.CLI, *kong.Context) {
	t.Helper()
	var cli cmd.CLI
	parser, err := kong.New(&cli,
		kong.Name("slack"),
		kong.Exit(func(int) { t.Fatal("unexpected exit") }),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	return &cli, ctx
}

func TestGlobalFlags_Defaults(t *testing.T) {
	cli, _ := mustParse(t, "user", "list")

	if cli.Format != "json" {
		t.Errorf("expected default format 'json', got %q", cli.Format)
	}
	if cli.Limit != 0 {
		t.Errorf("expected default limit 0, got %d", cli.Limit)
	}
	if cli.Quiet {
		t.Error("expected quiet to default to false")
	}
	if cli.Verbose {
		t.Error("expected verbose to default to false")
	}
	if cli.Raw {
		t.Error("expected raw to default to false")
	}
	if cli.NoPager {
		t.Error("expected no-pager to default to false")
	}
}

func TestGlobalFlags_Override(t *testing.T) {
	cli, _ := mustParse(t,
		"--format", "text",
		"--workspace", "myteam",
		"--quiet",
		"--verbose",
		"--no-pager",
		"--limit", "25",
		"--raw",
		"user", "list",
	)

	if cli.Format != "text" {
		t.Errorf("expected format 'text', got %q", cli.Format)
	}
	if cli.Workspace != "myteam" {
		t.Errorf("expected workspace 'myteam', got %q", cli.Workspace)
	}
	if !cli.Quiet {
		t.Error("expected quiet to be true")
	}
	if !cli.Verbose {
		t.Error("expected verbose to be true")
	}
	if !cli.NoPager {
		t.Error("expected no-pager to be true")
	}
	if cli.Limit != 25 {
		t.Errorf("expected limit 25, got %d", cli.Limit)
	}
	if !cli.Raw {
		t.Error("expected raw to be true")
	}
}

func TestGlobalFlags_ShortFlags(t *testing.T) {
	cli, _ := mustParse(t, "-f", "text", "-w", "myteam", "-q", "-v", "user", "list")

	if cli.Format != "text" {
		t.Errorf("expected format 'text', got %q", cli.Format)
	}
	if cli.Workspace != "myteam" {
		t.Errorf("expected workspace 'myteam', got %q", cli.Workspace)
	}
	if !cli.Quiet {
		t.Error("expected quiet to be true")
	}
	if !cli.Verbose {
		t.Error("expected verbose to be true")
	}
}

func TestGlobalFlags_InvalidFormat(t *testing.T) {
	var cli cmd.CLI
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parser.Parse([]string{"--format", "xml", "user", "list"})
	if err == nil {
		t.Error("expected error for invalid format, got nil")
	}
}

func TestGlobalFlags_EnvVars(t *testing.T) {
	t.Setenv("SLACK_WORKSPACE", "envteam")
	t.Setenv("SLACK_FORMAT", "text")

	cli, _ := mustParse(t, "user", "list")

	if cli.Workspace != "envteam" {
		t.Errorf("expected workspace 'envteam' from env, got %q", cli.Workspace)
	}
	if cli.Format != "text" {
		t.Errorf("expected format 'text' from env, got %q", cli.Format)
	}
}

func TestGlobalFlags_FlagsOverrideEnv(t *testing.T) {
	t.Setenv("SLACK_WORKSPACE", "envteam")

	cli, _ := mustParse(t, "--workspace", "flagteam", "user", "list")

	if cli.Workspace != "flagteam" {
		t.Errorf("expected workspace 'flagteam' from flag, got %q", cli.Workspace)
	}
}

func TestSubcommands_Exist(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"auth login", []string{"auth", "login"}},
		{"auth logout", []string{"auth", "logout"}},
		{"auth status", []string{"auth", "status"}},
		{"channel list", []string{"channel", "list"}},
		{"channel info", []string{"channel", "info", "C123"}},
		{"channel members", []string{"channel", "members", "C123"}},
		{"message list", []string{"message", "list", "C123"}},
		{"message read alias", []string{"message", "read", "C123"}},
		{"message get", []string{"message", "get", "C123", "1234567890.123456"}},
		{"thread list", []string{"thread", "list", "C123", "1234567890.123456"}},
		{"thread read alias", []string{"thread", "read", "C123", "1234567890.123456"}},
		{"user list", []string{"user", "list"}},
		{"user info", []string{"user", "info", "U123"}},
		{"reaction list", []string{"reaction", "list", "C123", "1234567890.123456"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ctx := mustParse(t, tt.args...)
			if ctx.Command() == "" {
				t.Error("expected a command to be selected")
			}
		})
	}
}

func TestSubcommands_NotImplemented(t *testing.T) {
	cli, ctx := mustParse(t, "user", "list")
	err := ctx.Run(cli)
	if err == nil {
		t.Error("expected 'not implemented' error, got nil")
	}
	if err.Error() != "not implemented" {
		t.Errorf("expected 'not implemented', got %q", err.Error())
	}
}

func TestUnknownCommand(t *testing.T) {
	var cli cmd.CLI
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parser.Parse([]string{"bogus"})
	if err == nil {
		t.Error("expected error for unknown command, got nil")
	}
}

func TestNoCommand(t *testing.T) {
	var cli cmd.CLI
	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parser.Parse([]string{})
	if err == nil {
		t.Error("expected error when no command given, got nil")
	}
}
