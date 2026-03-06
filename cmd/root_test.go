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

	if cli.Fields != "" {
		t.Errorf("expected default fields empty, got %q", cli.Fields)
	}
	if cli.Quiet {
		t.Error("expected quiet to default to false")
	}
	if cli.Verbose {
		t.Error("expected verbose to default to false")
	}
}

func TestGlobalFlags_Override(t *testing.T) {
	cli, _ := mustParse(t,
		"--workspace", "myteam",
		"--fields", "id,name",
		"--quiet",
		"--verbose",
		"user", "list",
	)

	if cli.Workspace != "myteam" {
		t.Errorf("expected workspace 'myteam', got %q", cli.Workspace)
	}
	if cli.Fields != "id,name" {
		t.Errorf("expected fields 'id,name', got %q", cli.Fields)
	}
	if !cli.Quiet {
		t.Error("expected quiet to be true")
	}
	if !cli.Verbose {
		t.Error("expected verbose to be true")
	}
}

func TestGlobalFlags_ShortFlags(t *testing.T) {
	cli, _ := mustParse(t, "-w", "myteam", "-q", "-v", "user", "list")

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

func TestGlobalFlags_EnvVars(t *testing.T) {
	t.Setenv("SLACK_WORKSPACE", "envteam")

	cli, _ := mustParse(t, "user", "list")

	if cli.Workspace != "envteam" {
		t.Errorf("expected workspace 'envteam' from env, got %q", cli.Workspace)
	}
}

func TestGlobalFlags_FlagsOverrideEnv(t *testing.T) {
	t.Setenv("SLACK_WORKSPACE", "envteam")

	cli, _ := mustParse(t, "--workspace", "flagteam", "user", "list")

	if cli.Workspace != "flagteam" {
		t.Errorf("expected workspace 'flagteam' from flag, got %q", cli.Workspace)
	}
}

func TestParsedFields(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{"empty", "", nil},
		{"single", "id", []string{"id"}},
		{"multiple", "id,name,email", []string{"id", "name", "email"}},
		{"with spaces", "id, name , email", []string{"id", "name", "email"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := &cmd.CLI{Fields: tt.input}
			got := cli.ParsedFields()
			if tt.expect == nil && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
			if len(got) != len(tt.expect) {
				t.Errorf("expected %d fields, got %d", len(tt.expect), len(got))
				return
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("field %d: got %q, want %q", i, got[i], tt.expect[i])
				}
			}
		})
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
