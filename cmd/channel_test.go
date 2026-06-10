package cmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// mockResult holds the captured output from a CLI command run against a mock server.
type mockResult struct {
	stdout string
	stderr string
	err    error
}

// runWithMockFull runs a CLI command against a mock Slack API server.
// Returns stdout, stderr, and any error from the command.
// isolateTestEnv clears Slack-related env vars that could leak from the
// developer's shell into tests (e.g. SLACK_WORKSPACE set by direnv).
// Tokens are left alone - helpers and tests set SLACK_TOKEN /
// SLACK_USER_TOKEN explicitly for the scenario under test.
func isolateTestEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"SLACK_COOKIE",
		"SLACK_WORKSPACE",
		"SLACK_WORKSPACE_ORG",
		"SLACK_FIELDS",
		"SLACK_CACHE_TTL",
		"SLACK_CLIENT_ID",
		"SLACK_CLIENT_SECRET",
		"SLACK_SAFE_STORAGE_PASSWORD",
	} {
		t.Setenv(v, "")
	}
}

func runWithMockFull(t *testing.T, handler http.Handler, args ...string) mockResult {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	isolateTestEnv(t)
	t.Setenv("SLACK_TOKEN", "xoxb-test")
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer

	parser, err := kong.New(&cli,
		kong.Name("slack"),
		kong.Exit(func(int) {}),
	)
	if err != nil {
		t.Fatal(err)
	}

	kctx, err := parser.Parse(args)
	if err != nil {
		return mockResult{err: err}
	}

	cli.SetOutput(&outBuf, &errBuf)

	runErr := kctx.Run(&cli)
	return mockResult{stdout: outBuf.String(), stderr: errBuf.String(), err: runErr}
}

// runWithMock runs a CLI command against a mock Slack API server.
// Returns stdout content and any error from the command.
func runWithMock(t *testing.T, handler http.Handler, args ...string) (string, error) {
	t.Helper()
	r := runWithMockFull(t, handler, args...)
	return r.stdout, r.err
}

// emptyMux returns a ServeMux with no handlers for commands that don't call APIs.
func emptyMux() *http.ServeMux {
	return http.NewServeMux()
}

func nonEmptyLines(s string) []string {
	var result []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func parseJSON(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", line, err)
	}
	return m
}

func TestChannelList_PaginationFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		ok   bool
	}{
		{"defaults", []string{"channel", "list"}, true},
		{"with limit", []string{"channel", "list", "--limit", "50"}, true},
		{"with cursor", []string{"channel", "list", "--cursor", "abc"}, true},
		{"with all", []string{"channel", "list", "--all"}, true},
		{"with type", []string{"channel", "list", "--type", "private"}, true},
		{"with query", []string{"channel", "list", "--query", "eng"}, true},
		{"invalid type", []string{"channel", "list", "--type", "bogus"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cli cmd.CLI
			parser, _ := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
			_, err := parser.Parse(tt.args)
			if tt.ok && err != nil {
				t.Errorf("expected success, got: %v", err)
			}
			if !tt.ok && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestChannelList_DefaultTypeIsAll(t *testing.T) {
	// Default --type should request all four channel kinds from
	// conversations.list, not just public_channel.
	var gotTypes string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		gotTypes = r.FormValue("types")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          []map[string]any{},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	if _, err := runWithMock(t, mux, "channel", "list"); err != nil {
		t.Fatal(err)
	}

	want := "public_channel,private_channel,mpim,im"
	if gotTypes != want {
		t.Errorf("default types = %q, want %q", gotTypes, want)
	}
}

func TestChannelList_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01", "name": "general", "is_channel": true, "is_member": true, "num_members": 10},
				{"id": "C02", "name": "random", "is_channel": true, "is_member": true, "num_members": 5},
				{"id": "C03", "name": "external", "is_channel": true, "is_member": false, "num_members": 2},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 2 member channels + _meta
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 channels + meta), got %d:\n%s", len(lines), out)
	}

	ch := parseJSON(t, lines[0])
	if ch["name"] != "general" {
		t.Errorf("expected first channel 'general', got %q", ch["name"])
	}

	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != false {
		t.Error("expected has_more=false")
	}
}

func TestChannelList_IncludeNonMember(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01", "name": "general", "is_channel": true, "is_member": true},
				{"id": "C02", "name": "external", "is_channel": true, "is_member": false},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "channel", "list", "--include-non-member")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 channels + meta), got %d:\n%s", len(lines), out)
	}
}

func TestChannelList_QueryFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01", "name": "general", "is_channel": true, "is_member": true},
				{"id": "C02", "name": "engineering", "is_channel": true, "is_member": true},
				{"id": "C03", "name": "random", "is_channel": true, "is_member": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "channel", "list", "--query", "eng")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 1 matching channel + _meta
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 channel + meta), got %d:\n%s", len(lines), out)
	}
}

func TestChannelList_HasUnread(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01", "name": "general", "is_channel": true, "is_member": true, "unread_count": 5},
				{"id": "C02", "name": "random", "is_channel": true, "is_member": true, "unread_count": 0},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "channel", "list", "--has-unread")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 channel + meta), got %d:\n%s", len(lines), out)
	}
}

func TestChannelList_Pagination(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		_ = r.ParseForm()
		cursor := r.FormValue("cursor")

		if cursor == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C01", "name": "page1", "is_member": true}},
				"response_metadata": map[string]string{"next_cursor": "page2cursor"},
			})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C02", "name": "page2", "is_member": true}},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		}
	})

	// Without --all, should return first page only with has_more=true.
	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
	meta := parseJSON(t, lines[1])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != true {
		t.Error("expected has_more=true for first page")
	}
	if m["next_cursor"] != "page2cursor" {
		t.Errorf("expected next_cursor='page2cursor', got %q", m["next_cursor"])
	}
}

func TestChannelList_AllPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		cursor := r.FormValue("cursor")
		if cursor == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C01", "name": "page1", "is_member": true}},
				"response_metadata": map[string]string{"next_cursor": "page2cursor"},
			})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C02", "name": "page2", "is_member": true}},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		}
	})

	out, err := runWithMock(t, mux, "channel", "list", "--all")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 2 channels from 2 pages + _meta
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}
	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != false {
		t.Error("expected has_more=false after all pages")
	}
}

func TestChannelList_IMsIgnoreMemberFilter(t *testing.T) {
	// Slack returns IMs with is_member=false - IMs don't have a "member"
	// concept. The default member-only filter would hide every DM; we must
	// include IMs unconditionally so `--type im` actually returns DMs.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "D01", "is_im": true, "is_member": false, "user": "U01"},
				{"id": "D02", "is_im": true, "is_member": false, "user": "U02"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "channel", "list", "--type", "im")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 IM rows + meta, got %d lines:\n%s", len(lines), out)
	}
	first := parseJSON(t, lines[0])
	if first["id"] != "D01" {
		t.Errorf("expected first id=D01, got %q", first["id"])
	}
}

func TestChannelList_NonIMChannelsStillFilteredByMember(t *testing.T) {
	// The IM carve-out must not leak into regular channels. A
	// public_channel with is_member=false should still be hidden by
	// default.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01", "name": "general", "is_channel": true, "is_member": true},
				{"id": "C02", "name": "external", "is_channel": true, "is_member": false},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 1 channel + meta, got %d lines:\n%s", len(lines), out)
	}
	first := parseJSON(t, lines[0])
	if first["name"] != "general" {
		t.Errorf("expected non-member channel filtered, got first=%q", first["name"])
	}
}

func TestChannelInfo_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"channels": []map[string]any{{"id": "C01", "name": "general"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01", "name": "general", "is_channel": true, "num_members": 42},
		})
	})

	out, err := runWithMock(t, mux, "channel", "info", "#general")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	ch := parseJSON(t, lines[0])
	if ch["input"] != "#general" {
		t.Errorf("expected input='#general', got %q", ch["input"])
	}
}

func TestChannelInfo_InlineError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"channels": []map[string]any{{"id": "C01", "name": "general"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01", "name": "general"},
		})
	})

	out, err := runWithMock(t, mux, "channel", "info", "#general", "#nonexistent")
	// Should return error since one channel failed.
	if err == nil {
		t.Fatal("expected error for partial failure")
	}

	lines := nonEmptyLines(out)
	// 1 success + 1 error + _meta = 3 lines
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}

	errLine := parseJSON(t, lines[1])
	if errLine["error"] != "channel_not_found" {
		t.Errorf("expected error='channel_not_found', got %q", errLine["error"])
	}

	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["error_count"] != float64(1) {
		t.Errorf("expected error_count=1, got %v", m["error_count"])
	}
}

func TestChannelMembers_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.members", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"members":          []string{"U01", "U02", "U03"},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "channel", "members", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (3 members + meta), got %d:\n%s", len(lines), out)
	}

	member := parseJSON(t, lines[0])
	if member["user_id"] != "U01" {
		t.Errorf("expected first member 'U01', got %q", member["user_id"])
	}
}

func TestChannelManagers_Basic(t *testing.T) {
	var gotEntityID string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin.roles.entity.listAssignments", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotEntityID = r.FormValue("entity_id")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"role_assignments": []map[string]any{
				{"role_id": "Rl0A", "users": []string{"U018Z62JVG8", "U02ABC"}},
			},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "managers", "C0A065FTV4H")
	if err != nil {
		t.Fatal(err)
	}

	if gotEntityID != "C0A065FTV4H" {
		t.Errorf("expected entity_id='C0A065FTV4H' in request, got %q", gotEntityID)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 managers + meta), got %d:\n%s", len(lines), out)
	}

	first := parseJSON(t, lines[0])
	if first["user_id"] != "U018Z62JVG8" {
		t.Errorf("expected first user_id='U018Z62JVG8', got %q", first["user_id"])
	}
	if first["role_id"] != "Rl0A" {
		t.Errorf("expected role_id='Rl0A', got %q", first["role_id"])
	}
	second := parseJSON(t, lines[1])
	if second["user_id"] != "U02ABC" {
		t.Errorf("expected second user_id='U02ABC', got %q", second["user_id"])
	}
}

// TestChannelManagers_EmitsAllRoles verifies the command does not hard-filter
// to one role ID: every returned assignment is emitted, one row per user,
// carrying its own role_id. Guards against silently dropping managers if
// Slack's channel-manager role ID ever differs from the observed Rl0A.
func TestChannelManagers_EmitsAllRoles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin.roles.entity.listAssignments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"role_assignments": []map[string]any{
				{"role_id": "Rl0A", "users": []string{"U01"}},
				{"role_id": "Rl99", "users": []string{"U02"}},
				{"role_id": "RlEMPTY", "users": []string{}},
			},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "managers", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 users + meta; empty assignment skipped), got %d:\n%s", len(lines), out)
	}
	if r0 := parseJSON(t, lines[0]); r0["user_id"] != "U01" || r0["role_id"] != "Rl0A" {
		t.Errorf("row 0 = %v, want U01/Rl0A", r0)
	}
	if r1 := parseJSON(t, lines[1]); r1["user_id"] != "U02" || r1["role_id"] != "Rl99" {
		t.Errorf("row 1 = %v, want U02/Rl99", r1)
	}
}

func TestChannelManagers_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin.roles.entity.listAssignments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"role_assignments": []map[string]any{},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "managers", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (meta only), got %d:\n%s", len(lines), out)
	}
	meta := parseJSON(t, lines[0])["_meta"].(map[string]any)
	if meta["error_count"] != nil {
		t.Errorf("empty managers must not be an error, got error_count=%v", meta["error_count"])
	}
}

// TestChannelManagers_ByName exercises the two-client split: a channel *name*
// is resolved via conversations.list (public client), and the resolved ID is
// forwarded as entity_id to the internal endpoint (session client).
func TestChannelManagers_ByName(t *testing.T) {
	var gotEntityID string
	listCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		listCalled = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C0A065FTV4H", "name": "sa-approvals", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/admin.roles.entity.listAssignments", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotEntityID = r.FormValue("entity_id")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"role_assignments": []map[string]any{
				{"role_id": "Rl0A", "users": []string{"U018Z62JVG8"}},
			},
		})
	})

	out, err := runWithMockSession(t, mux, "channel", "managers", "#sa-approvals")
	if err != nil {
		t.Fatal(err)
	}

	if !listCalled {
		t.Error("expected conversations.list to be called to resolve the channel name")
	}
	if gotEntityID != "C0A065FTV4H" {
		t.Errorf("expected resolved entity_id='C0A065FTV4H', got %q", gotEntityID)
	}
	if first := parseJSON(t, nonEmptyLines(out)[0]); first["user_id"] != "U018Z62JVG8" {
		t.Errorf("expected manager 'U018Z62JVG8', got %q", first["user_id"])
	}
}

func TestChannelManagers_SessionTokenRequired(t *testing.T) {
	// A regular xoxb- token must be rejected as session_token_required (exit 2)
	// before any API call - and as *output.Error so main.go prints it to stderr.
	mux := http.NewServeMux()
	r := runWithMockFull(t, mux, "channel", "managers", "C01ABC")
	if r.err == nil {
		t.Fatal("expected error for non-session token")
	}
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", r.err, r.err)
	}
	if oErr.Err != "session_token_required" {
		t.Errorf("expected error 'session_token_required', got %q", oErr.Err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("expected exit code %d (auth), got %d", output.ExitAuth, oErr.Code)
	}
}

func TestChannelInfo_PartialFailure_NoStderr(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"channels": []map[string]any{{"id": "C01", "name": "general"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01", "name": "general"},
		})
	})

	r := runWithMockFull(t, mux, "channel", "info", "#general", "#nonexistent")
	if r.err == nil {
		t.Fatal("expected error for partial failure")
	}

	// The returned error must NOT be *output.Error, because main.go prints
	// those to stderr. Per-item errors belong on stdout only.
	var oErr *output.Error
	if errors.As(r.err, &oErr) {
		t.Errorf("partial failure should not return *output.Error (would be printed to stderr), got: %v", r.err)
	}

	lines := nonEmptyLines(r.stdout)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), r.stdout)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
