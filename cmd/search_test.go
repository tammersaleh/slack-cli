package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// searchResponse builds a mock Slack search.messages API response.
func searchResponse(matches []map[string]any, page, pageCount, total int) map[string]any {
	return map[string]any{
		"ok": true,
		"messages": map[string]any{
			"matches": matches,
			"paging": map[string]any{
				"count": len(matches),
				"total": total,
				"page":  page,
				"pages": pageCount,
			},
			"pagination": map[string]any{
				"total_count": total,
				"page":        page,
				"per_page":    20,
				"page_count":  pageCount,
				"first":       1,
				"last":        pageCount,
			},
			"total": total,
		},
	}
}

func searchMatch(ts, text, user, username, channelID, channelName, permalink string) map[string]any {
	return map[string]any{
		"type":      "message",
		"ts":        ts,
		"text":      text,
		"user":      user,
		"username":  username,
		"permalink": permalink,
		"channel": map[string]any{
			"id":   channelID,
			"name": channelName,
		},
	}
}

func TestSearchMessages_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if q := r.Form.Get("query"); q != "deploy failed" {
			t.Errorf("expected query 'deploy failed', got %q", q)
		}
		json.NewEncoder(w).Encode(searchResponse(
			[]map[string]any{
				searchMatch("1709251200.000100", "The deploy failed at 3am", "U01XYZ", "tammer", "C01ABC", "general", "https://acme.slack.com/archives/C01ABC/p1709251200000100"),
				searchMatch("1709164800.000050", "deploy failed again", "U02ABC", "alice", "C03GHI", "engineering", "https://acme.slack.com/archives/C03GHI/p1709164800000050"),
			},
			1, 1, 2,
		))
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "messages", "deploy failed")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 2 messages + _meta
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}

	msg := parseJSON(t, lines[0])
	if msg["type"] != "message" {
		t.Errorf("expected type 'message', got %q", msg["type"])
	}
	if msg["text"] != "The deploy failed at 3am" {
		t.Errorf("unexpected text: %v", msg["text"])
	}
	if msg["permalink"] != "https://acme.slack.com/archives/C01ABC/p1709251200000100" {
		t.Errorf("unexpected permalink: %v", msg["permalink"])
	}
	ch := msg["channel"].(map[string]any)
	if ch["id"] != "C01ABC" {
		t.Errorf("unexpected channel id: %v", ch["id"])
	}

	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != false {
		t.Error("expected has_more=false")
	}
}

func TestSearchMessages_Pagination(t *testing.T) {
	mux := http.NewServeMux()
	callCount := 0
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		r.ParseForm()
		page := r.Form.Get("page")

		if page == "" || page == "1" {
			json.NewEncoder(w).Encode(searchResponse(
				[]map[string]any{
					searchMatch("1709251200.000100", "msg1", "U01", "tammer", "C01", "general", "https://x/p1"),
				},
				1, 2, 2,
			))
		} else if page == "2" {
			json.NewEncoder(w).Encode(searchResponse(
				[]map[string]any{
					searchMatch("1709164800.000050", "msg2", "U02", "alice", "C01", "general", "https://x/p2"),
				},
				2, 2, 2,
			))
		}
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")

	// First page should show has_more=true with a cursor
	out, err := runWithMock(t, mux, "search", "messages", "test", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 msg + meta), got %d:\n%s", len(lines), out)
	}

	meta := parseJSON(t, lines[1])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != true {
		t.Error("expected has_more=true on first page")
	}
	cursor, ok := m["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("expected non-empty next_cursor")
	}

	// Second page using cursor
	callCount = 0
	out2, err := runWithMock(t, mux, "search", "messages", "test", "--limit", "1", "--cursor", cursor)
	if err != nil {
		t.Fatal(err)
	}

	lines2 := nonEmptyLines(out2)
	if len(lines2) != 2 {
		t.Fatalf("expected 2 lines on page 2, got %d:\n%s", len(lines2), out2)
	}

	msg2 := parseJSON(t, lines2[0])
	if msg2["text"] != "msg2" {
		t.Errorf("expected msg2, got %v", msg2["text"])
	}

	meta2 := parseJSON(t, lines2[1])
	m2 := meta2["_meta"].(map[string]any)
	if m2["has_more"] != false {
		t.Error("expected has_more=false on last page")
	}
}

func TestSearchMessages_All(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		page := r.Form.Get("page")

		if page == "" || page == "1" {
			json.NewEncoder(w).Encode(searchResponse(
				[]map[string]any{
					searchMatch("1.1", "msg1", "U01", "a", "C01", "g", "https://x/1"),
				},
				1, 2, 2,
			))
		} else {
			json.NewEncoder(w).Encode(searchResponse(
				[]map[string]any{
					searchMatch("2.2", "msg2", "U02", "b", "C01", "g", "https://x/2"),
				},
				2, 2, 2,
			))
		}
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "messages", "test", "--all")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 2 messages + _meta
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}

	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != false {
		t.Error("expected has_more=false after fetching all")
	}
}

func TestSearchMessages_SortFlags(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.messages", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		// CLI default is "timestamp" which differs from slack-go's default "score",
		// so it should always be sent.
		if s := r.Form.Get("sort"); s != "timestamp" {
			t.Errorf("expected sort=timestamp (CLI default), got %q", s)
		}
		if d := r.Form.Get("sort_dir"); d != "asc" {
			t.Errorf("expected sort_dir=asc, got %q", d)
		}
		json.NewEncoder(w).Encode(searchResponse(nil, 1, 1, 0))
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	_, err := runWithMock(t, mux, "search", "messages", "test", "--sort-dir", "asc")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSearchMessages_MissingUserToken(t *testing.T) {
	mux := http.NewServeMux()
	// No SLACK_USER_TOKEN set

	_, err := runWithMock(t, mux, "search", "messages", "test query")
	if err == nil {
		t.Fatal("expected error for missing user token")
	}

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "missing_user_token" {
		t.Errorf("expected error 'missing_user_token', got %q", oErr.Err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("expected exit code %d, got %d", output.ExitAuth, oErr.Code)
	}
}

func TestSearchMessages_AllAndCursorMutuallyExclusive(t *testing.T) {
	mux := http.NewServeMux()
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	_, err := runWithMock(t, mux, "search", "messages", "test", "--all", "--cursor", "abc")
	if err == nil {
		t.Fatal("expected error for --all with --cursor")
	}
}
