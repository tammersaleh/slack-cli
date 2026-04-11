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
func runWithMockFull(t *testing.T, handler http.Handler, args ...string) mockResult {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

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
