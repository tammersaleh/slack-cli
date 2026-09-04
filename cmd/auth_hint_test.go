package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/auth"
)

func TestSortWorkspaces(t *testing.T) {
	ws := []auth.WorkspaceCredentials{
		{TeamID: "T03GHI", TeamName: "Zeta"}, // case-sensitive sort would put "Z" before "a"
		{TeamID: "E01ORG", TeamName: "Acme"},
		{TeamID: "T02DEF", TeamName: "acme"},
		{TeamID: "T01ABC", TeamName: "Acme"},
		{TeamID: "T04JKL", TeamName: "alpha"},
	}
	sortWorkspaces(ws)

	var got []string
	for _, w := range ws {
		got = append(got, w.TeamID)
	}
	// Name first (case-insensitive), then ID breaks ties.
	want := []string{"E01ORG", "T01ABC", "T02DEF", "T04JKL", "T03GHI"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got order %v, want %v", got, want)
	}
}

func TestPrintWorkspaceHints(t *testing.T) {
	saved := map[string]auth.WorkspaceCredentials{
		"T01ABC": {TeamID: "T01ABC", TeamName: "Acme Corp"},
		"T02DEF": {TeamID: "T02DEF", TeamName: "Other Corp"},
		"E01ORG": {TeamID: "E01ORG", TeamName: "Acme"},
	}
	noOrg := map[string]auth.WorkspaceCredentials{
		"T01ABC": {TeamID: "T01ABC", TeamName: "Acme Corp"},
	}

	tests := []struct {
		name       string
		current    string
		currentOrg string
		saved      map[string]auth.WorkspaceCredentials
		want       []string // substrings that must appear, in order
		wantAbsent []string // substrings that must not appear
	}{
		{
			name:  "nothing set lists every regular workspace and the org",
			saved: saved,
			want: []string{
				"Set your default workspace",
				"export SLACK_WORKSPACE=T01ABC  # Acme Corp",
				"export SLACK_WORKSPACE=T02DEF  # Other Corp",
				"Enterprise Grid detected",
				"export SLACK_WORKSPACE_ORG=E01ORG  # Acme (org)",
			},
			wantAbsent: []string{"export SLACK_WORKSPACE=E01ORG", "is set to"},
		},
		{
			name:       "both set and valid reports current values, no export lines",
			current:    "T02DEF",
			currentOrg: "E01ORG",
			saved:      saved,
			want: []string{
				"SLACK_WORKSPACE is set to T02DEF  # Other Corp",
				"SLACK_WORKSPACE_ORG is set to E01ORG  # Acme (org)",
			},
			wantAbsent: []string{"export ", "Set your default", "Enterprise Grid detected"},
		},
		{
			name:    "workspace set but org unset keeps the org hint",
			current: "T01ABC",
			saved:   saved,
			want: []string{
				"SLACK_WORKSPACE is set to T01ABC  # Acme Corp",
				"Enterprise Grid detected",
				"export SLACK_WORKSPACE_ORG=E01ORG  # Acme (org)",
			},
			wantAbsent: []string{"export SLACK_WORKSPACE=", "Set your default"},
		},
		{
			name:    "stale workspace value is called out and choices listed",
			current: "T09OLD",
			saved:   saved,
			want: []string{
				"SLACK_WORKSPACE=T09OLD does not match any saved workspace",
				"export SLACK_WORKSPACE=T01ABC  # Acme Corp",
				"export SLACK_WORKSPACE=T02DEF  # Other Corp",
			},
			wantAbsent: []string{"is set to"},
		},
		{
			name:       "stale org value is called out",
			current:    "T01ABC",
			currentOrg: "E09OLD",
			saved:      saved,
			want: []string{
				"SLACK_WORKSPACE is set to T01ABC  # Acme Corp",
				"SLACK_WORKSPACE_ORG=E09OLD does not match any saved org",
				"export SLACK_WORKSPACE_ORG=E01ORG  # Acme (org)",
			},
			wantAbsent: []string{"SLACK_WORKSPACE_ORG is set"},
		},
		{
			name:       "org set on a non-grid credential set is stale",
			currentOrg: "E01ORG",
			saved:      noOrg,
			want: []string{
				"export SLACK_WORKSPACE=T01ABC  # Acme Corp",
				"SLACK_WORKSPACE_ORG=E01ORG does not match any saved org",
			},
			wantAbsent: []string{"Enterprise Grid detected", "SLACK_WORKSPACE_ORG is set"},
		},
		{
			name:       "no grid workspaces and no org set prints no org section",
			saved:      noOrg,
			want:       []string{"export SLACK_WORKSPACE=T01ABC  # Acme Corp"},
			wantAbsent: []string{"SLACK_WORKSPACE_ORG", "Enterprise Grid"},
		},
		{
			name: "org-only credentials offer the org id for SLACK_WORKSPACE",
			saved: map[string]auth.WorkspaceCredentials{
				"E01ORG": {TeamID: "E01ORG", TeamName: "Acme"},
			},
			want: []string{
				"Set your default workspace:",
				"export SLACK_WORKSPACE=E01ORG  # Acme",
				"Enterprise Grid detected",
				"export SLACK_WORKSPACE_ORG=E01ORG  # Acme (org)",
			},
		},
		{
			name:    "an org id in SLACK_WORKSPACE is a valid saved selection",
			current: "E01ORG",
			saved:   saved,
			want: []string{
				"SLACK_WORKSPACE is set to E01ORG  # Acme",
				"Enterprise Grid detected",
			},
			wantAbsent: []string{"export SLACK_WORKSPACE="},
		},
		{
			name: "the map key is the id, even when TeamID disagrees",
			saved: map[string]auth.WorkspaceCredentials{
				"T01ABC": {TeamID: "T99BAD", TeamName: "Acme Corp"},
			},
			want:       []string{"export SLACK_WORKSPACE=T01ABC  # Acme Corp"},
			wantAbsent: []string{"T99BAD"},
		},
		{
			name:    "matching is by map key, not TeamID",
			current: "T01ABC",
			saved: map[string]auth.WorkspaceCredentials{
				"T01ABC": {TeamID: "T99BAD", TeamName: "Acme Corp"},
			},
			want:       []string{"SLACK_WORKSPACE is set to T01ABC  # Acme Corp"},
			wantAbsent: []string{"export ", "T99BAD"},
		},
		{
			name:       "a T id in SLACK_WORKSPACE_ORG is the wrong kind of credential",
			current:    "T01ABC",
			currentOrg: "T02DEF",
			saved:      saved,
			want: []string{
				"SLACK_WORKSPACE is set to T01ABC  # Acme Corp",
				"SLACK_WORKSPACE_ORG=T02DEF does not match any saved org",
				"export SLACK_WORKSPACE_ORG=E01ORG  # Acme (org)",
			},
			wantAbsent: []string{"SLACK_WORKSPACE_ORG is set"},
		},
		{
			name:    "stale workspace with org-only credentials is still called out",
			current: "T09OLD",
			saved: map[string]auth.WorkspaceCredentials{
				"E01ORG": {TeamID: "E01ORG", TeamName: "Acme"},
			},
			want: []string{
				"SLACK_WORKSPACE=T09OLD does not match any saved workspace",
				"export SLACK_WORKSPACE=E01ORG  # Acme",
			},
		},
		{
			name:  "empty saved set prints nothing",
			saved: map[string]auth.WorkspaceCredentials{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printWorkspaceHints(&buf, tt.current, tt.currentOrg, tt.saved)
			out := buf.String()

			if len(tt.want) == 0 && out != "" {
				t.Fatalf("expected no output, got:\n%s", out)
			}
			pos := 0
			for _, s := range tt.want {
				i := strings.Index(out[pos:], s)
				if i < 0 {
					t.Fatalf("missing or out of order %q in:\n%s", s, out)
				}
				pos += i + len(s)
			}
			for _, s := range tt.wantAbsent {
				if strings.Contains(out, s) {
					t.Errorf("unexpected %q in:\n%s", s, out)
				}
			}
		})
	}
}

// Regular workspaces must list in sorted order regardless of map iteration.
func TestPrintWorkspaceHints_DeterministicOrder(t *testing.T) {
	saved := map[string]auth.WorkspaceCredentials{}
	for _, id := range []string{"T05", "T03", "T01", "T04", "T02", "E02", "E01"} {
		saved[id] = auth.WorkspaceCredentials{TeamID: id, TeamName: "team-" + id}
	}
	want := strings.Join([]string{
		"",
		"Set your default workspace:",
		"",
		"  export SLACK_WORKSPACE=T01  # team-T01",
		"  export SLACK_WORKSPACE=T02  # team-T02",
		"  export SLACK_WORKSPACE=T03  # team-T03",
		"  export SLACK_WORKSPACE=T04  # team-T04",
		"  export SLACK_WORKSPACE=T05  # team-T05",
		"",
		"Enterprise Grid detected. Internal APIs (saved items, sidebar",
		"sections) require the org-level token:",
		"",
		"  export SLACK_WORKSPACE_ORG=E01  # team-E01 (org)",
		"  export SLACK_WORKSPACE_ORG=E02  # team-E02 (org)",
		"",
	}, "\n") + "\n"
	for i := 0; i < 20; i++ {
		var buf bytes.Buffer
		printWorkspaceHints(&buf, "", "", saved)
		if buf.String() != want {
			t.Fatalf("run %d: got\n%s\nwant\n%s", i, buf.String(), want)
		}
	}
}

// saveAndPrintWorkspaces must emit sorted rows and build hints from the
// merged credentials file, not only from this login's workspaces.
func TestSaveAndPrintWorkspaces_SortedRowsAndMergedHints(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SLACK_WORKSPACE", "")
	t.Setenv("SLACK_WORKSPACE_ORG", "")

	path, err := auth.DefaultCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	// "Alder" sorts between the two new entries, so the merged hint order
	// proves the file's workspace was sorted in with the new ones.
	existing := &auth.Credentials{Workspaces: map[string]auth.WorkspaceCredentials{
		"T03OLD": {TeamID: "T03OLD", TeamName: "Alder", BotToken: "xoxc-old"},
	}}
	if err := auth.SaveCredentials(path, existing); err != nil {
		t.Fatal(err)
	}

	var cli CLI
	var out, errBuf bytes.Buffer
	cli.SetOutput(&out, &errBuf)

	// Unsorted input. Rows print only these two, sorted acme then Beta; the
	// hint merges in Alder from the file between them.
	input := []auth.WorkspaceCredentials{
		{TeamID: "T02DEF", TeamName: "Beta", BotToken: "xoxc-b", AuthMethod: "desktop"},
		{TeamID: "T01ABC", TeamName: "acme", BotToken: "xoxc-a", AuthMethod: "desktop"},
	}
	if err := saveAndPrintWorkspaces(&cli, input); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + _meta, got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"team_id":"T01ABC"`) || !strings.Contains(lines[1], `"team_id":"T02DEF"`) {
		t.Errorf("rows not sorted by name:\n%s", out.String())
	}
	if !strings.Contains(lines[2], `"_meta"`) {
		t.Errorf("missing trailer:\n%s", out.String())
	}

	hint := errBuf.String()
	wantHint := "\nSet your default workspace:\n\n" +
		"  export SLACK_WORKSPACE=T01ABC  # acme\n" +
		"  export SLACK_WORKSPACE=T03OLD  # Alder\n" +
		"  export SLACK_WORKSPACE=T02DEF  # Beta\n\n"
	if hint != wantHint {
		t.Errorf("hint got\n%s\nwant\n%s", hint, wantHint)
	}
	if strings.Contains(hint, "SLACK_WORKSPACE_ORG") {
		t.Errorf("no org stored, hint should not mention the org var:\n%s", hint)
	}
}

// The hint reads the env, not the kong-merged --workspace flag.
func TestSaveAndPrintWorkspaces_HintReadsEnvNotFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SLACK_WORKSPACE", "T01ABC")
	t.Setenv("SLACK_WORKSPACE_ORG", "E01ORG")

	var cli CLI
	cli.Workspace = "T02DEF"    // as if --workspace T02DEF were passed on login
	cli.WorkspaceOrg = "E02ORG" // and a flag-shaped org override
	var out, errBuf bytes.Buffer
	cli.SetOutput(&out, &errBuf)

	input := []auth.WorkspaceCredentials{
		{TeamID: "T01ABC", TeamName: "Acme Corp", BotToken: "xoxc-a", AuthMethod: "desktop"},
		{TeamID: "T02DEF", TeamName: "Other Corp", BotToken: "xoxc-b", AuthMethod: "desktop"},
		{TeamID: "E01ORG", TeamName: "Acme", BotToken: "xoxc-e1", AuthMethod: "desktop"},
		{TeamID: "E02ORG", TeamName: "Other", BotToken: "xoxc-e2", AuthMethod: "desktop"},
	}
	if err := saveAndPrintWorkspaces(&cli, input); err != nil {
		t.Fatal(err)
	}
	hint := errBuf.String()
	for _, want := range []string{
		"SLACK_WORKSPACE is set to T01ABC  # Acme Corp",
		"SLACK_WORKSPACE_ORG is set to E01ORG  # Acme (org)",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint should reflect the env value %q:\n%s", want, hint)
		}
	}
	for _, flag := range []string{"T02DEF", "E02ORG"} {
		if strings.Contains(hint, flag) {
			t.Errorf("hint should not reflect the flag value %s:\n%s", flag, hint)
		}
	}
}
