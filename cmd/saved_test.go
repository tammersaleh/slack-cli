package cmd_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
)

// runWithMockSession is like runWithMock but uses an xoxc- session token.
func runWithMockSession(t *testing.T, handler http.Handler, args ...string) (string, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	t.Setenv("SLACK_TOKEN", "xoxc-test")
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
		return "", err
	}

	cli.SetOutput(&outBuf, &errBuf)
	runErr := kctx.Run(&cli)
	return outBuf.String(), runErr
}

func savedListHandler(t *testing.T, items []map[string]any, counts map[string]any, nextCursor string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/saved.list" {
			http.Error(w, "not found", 404)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		resp := map[string]any{
			"ok":          true,
			"saved_items": items,
			"counts":      counts,
			"response_metadata": map[string]string{
				"next_cursor": nextCursor,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func TestSavedList_Basic(t *testing.T) {
	items := []map[string]any{
		{
			"item_id":      "C01ABC",
			"ts":           "1709251200.000100",
			"date_created": 1709251200,
			"todo_state":   "uncompleted",
		},
		{
			"item_id":      "C02DEF",
			"ts":           "1709164800.000050",
			"date_created": 1709164800,
			"todo_state":   "uncompleted",
		},
	}
	counts := map[string]any{"uncompleted": 2, "completed": 0, "total": 2}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", savedListHandler(t, items, counts, ""))

	out, err := runWithMockSession(t, mux, "saved", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 items + meta), got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["channel_id"] != "C01ABC" {
		t.Errorf("expected channel_id='C01ABC', got %q", item["channel_id"])
	}
	if item["message_ts"] != "1709251200.000100" {
		t.Errorf("expected message_ts, got %q", item["message_ts"])
	}
	if item["permalink"] == nil || item["permalink"] == "" {
		t.Error("expected non-empty permalink")
	}
}

func TestSavedList_Pagination(t *testing.T) {
	items := []map[string]any{
		{"item_id": "C01ABC", "ts": "1.1", "date_created": 1709251200, "todo_state": "uncompleted"},
	}
	counts := map[string]any{"uncompleted": 5, "total": 5}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", savedListHandler(t, items, counts, "nextpage"))

	out, err := runWithMockSession(t, mux, "saved", "list", "--limit", "1")
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
		t.Error("expected has_more=true")
	}
	if m["next_cursor"] != "nextpage" {
		t.Errorf("expected next_cursor='nextpage', got %q", m["next_cursor"])
	}
}

func TestSavedList_SessionTokenRequired(t *testing.T) {
	// With a regular xoxb- token, saved.list should fail with session_token_required.
	mux := http.NewServeMux()
	// No handler needed - should fail before making the call.

	_, err := runWithMock(t, mux, "saved", "list")
	if err == nil {
		t.Fatal("expected error for non-session token")
	}
}

func TestSavedCounts(t *testing.T) {
	items := []map[string]any{
		{"item_id": "C01ABC", "ts": "1.1", "date_created": 1709251200, "todo_state": "uncompleted"},
	}
	counts := map[string]any{
		"uncompleted": 12,
		"overdue":     2,
		"completed":   45,
		"total":       59,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", savedListHandler(t, items, counts, ""))

	out, err := runWithMockSession(t, mux, "saved", "counts")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (counts + meta), got %d:\n%s", len(lines), out)
	}

	c := parseJSON(t, lines[0])
	if c["uncompleted"] != float64(12) {
		t.Errorf("expected uncompleted=12, got %v", c["uncompleted"])
	}
	if c["total"] != float64(59) {
		t.Errorf("expected total=59, got %v", c["total"])
	}
}

func TestSavedList_Enrich(t *testing.T) {
	items := []map[string]any{
		{"item_id": "C01ABC", "ts": "1709251200.000100", "date_created": 1709251200, "todo_state": "uncompleted"},
	}
	counts := map[string]any{"uncompleted": 1, "total": 1}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", savedListHandler(t, items, counts, ""))
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"text": "Hello world", "user": "U01XYZ", "ts": "1709251200.000100"},
			},
		})
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channel": map[string]any{
				"id":   "C01ABC",
				"name": "general",
			},
		})
	})

	out, err := runWithMockSession(t, mux, "saved", "list", "--enrich")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["text"] != "Hello world" {
		t.Errorf("expected text='Hello world', got %q", item["text"])
	}
	if item["channel_name"] != "general" {
		t.Errorf("expected channel_name='general', got %q", item["channel_name"])
	}
	if item["from_user"] != "U01XYZ" {
		t.Errorf("expected from_user='U01XYZ', got %q", item["from_user"])
	}
}

func TestSavedList_IncludeCompleted(t *testing.T) {
	gotIncludeCompleted := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if ic, ok := req["include_completed"]; ok && ic == true {
			gotIncludeCompleted = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"saved_items":       []map[string]any{},
			"counts":            map[string]any{},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	_, err := runWithMockSession(t, mux, "saved", "list", "--include-completed")
	if err != nil {
		t.Fatal(err)
	}

	if !gotIncludeCompleted {
		t.Error("expected include_completed=true in request body")
	}
}
