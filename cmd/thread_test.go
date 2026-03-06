package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestThreadList_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "user": "U01", "text": "parent", "ts": "1709251200.000100", "thread_ts": "1709251200.000100", "reply_count": 2},
				{"type": "message", "user": "U02", "text": "reply 1", "ts": "1709251300.000150", "thread_ts": "1709251200.000100"},
				{"type": "message", "user": "U01", "text": "reply 2", "ts": "1709251400.000180", "thread_ts": "1709251200.000100"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "thread", "list", "C01ABC", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 3 messages (parent + 2 replies) + _meta
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), out)
	}

	parent := parseJSON(t, lines[0])
	if parent["text"] != "parent" {
		t.Errorf("expected first message to be parent, got %q", parent["text"])
	}

	// Verify timestamp enrichment on thread_ts.
	if _, ok := parent["thread_ts_iso"]; !ok {
		t.Error("expected thread_ts_iso field")
	}
}

func TestThreadList_EmptyThread(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	_, err := runWithMock(t, mux, "thread", "list", "C01ABC", "9999999999.999999")
	if err == nil {
		t.Fatal("expected error for empty thread")
	}
}

func TestThreadList_ReadAlias(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "text": "parent", "ts": "1709251200.000100"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "thread", "read", "C01ABC", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
}
