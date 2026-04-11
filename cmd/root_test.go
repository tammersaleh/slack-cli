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
}

func TestGlobalFlags_Override(t *testing.T) {
	cli, _ := mustParse(t,
		"--workspace", "myteam",
		"--fields", "id,name",
		"--quiet",
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
}

func TestGlobalFlags_ShortFlags(t *testing.T) {
	cli, _ := mustParse(t, "-w", "myteam", "-q", "user", "list")

	if cli.Workspace != "myteam" {
		t.Errorf("expected workspace 'myteam', got %q", cli.Workspace)
	}
	if !cli.Quiet {
		t.Error("expected quiet to be true")
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
		{"auth login desktop", []string{"auth", "login", "--desktop"}},
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
		{"search messages", []string{"search", "messages", "test query"}},
		{"search files", []string{"search", "files", "test query"}},
		{"message permalink", []string{"message", "permalink", "C123", "1234.5678"}},
		{"message post", []string{"message", "post", "C123", "--text", "hi"}},
		{"saved list", []string{"saved", "list"}},
		{"saved counts", []string{"saved", "counts"}},
		{"section list", []string{"section", "list"}},
		{"section channels", []string{"section", "channels", "S123"}},
		{"section find", []string{"section", "find", "pattern"}},
		{"section create", []string{"section", "create", "name"}},
		{"file list", []string{"file", "list"}},
		{"file info", []string{"file", "info", "F123"}},
		{"file download", []string{"file", "download", "F123"}},
		{"pin list", []string{"pin", "list", "C123"}},
		{"bookmark list", []string{"bookmark", "list", "C123"}},
		{"status get", []string{"status", "get"}},
		{"presence get", []string{"presence", "get", "U123"}},
		{"dnd info", []string{"dnd", "info", "U123"}},
		{"usergroup list", []string{"usergroup", "list"}},
		{"usergroup members", []string{"usergroup", "members", "S123"}},
		{"emoji list", []string{"emoji", "list"}},
		{"workspace info", []string{"workspace-info", "info"}},
		{"cache info", []string{"cache", "info"}},
		{"cache clear", []string{"cache", "clear"}},
		{"version", []string{"version"}},
		{"skill", []string{"skill"}},
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

func TestChannelList_Defaults(t *testing.T) {
	cli, _ := mustParse(t, "channel", "list")
	if cli.Channel.List.Limit != 100 {
		t.Errorf("expected default limit 100, got %d", cli.Channel.List.Limit)
	}
	if cli.Channel.List.Type != "public" {
		t.Errorf("expected default type 'public', got %q", cli.Channel.List.Type)
	}
	if !cli.Channel.List.ExcludeArchived {
		t.Error("expected exclude-archived to default to true")
	}
}

func TestMessageList_Defaults(t *testing.T) {
	cli, _ := mustParse(t, "message", "list", "#general")
	if cli.Message.List.Limit != 20 {
		t.Errorf("expected default limit 20, got %d", cli.Message.List.Limit)
	}
}

func TestThreadList_Defaults(t *testing.T) {
	cli, _ := mustParse(t, "thread", "list", "#general", "1234.5678")
	if cli.Thread.List.Limit != 50 {
		t.Errorf("expected default limit 50, got %d", cli.Thread.List.Limit)
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
