package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMessageList_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "user": "U01", "text": "hello", "ts": "1709251200.000100"},
				{"type": "message", "user": "U02", "text": "world", "ts": "1709251100.000050"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 messages + meta), got %d:\n%s", len(lines), out)
	}

	msg := parseJSON(t, lines[0])
	if msg["text"] != "hello" {
		t.Errorf("expected text='hello', got %q", msg["text"])
	}
	// Timestamp enrichment.
	if _, ok := msg["ts_iso"]; !ok {
		t.Error("expected ts_iso field from timestamp enrichment")
	}
}

func TestMessageList_HasRepliesFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "text": "has replies", "ts": "1709251200.000100", "reply_count": 3},
				{"type": "message", "text": "no replies", "ts": "1709251100.000050"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC", "--has-replies")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 message + meta), got %d:\n%s", len(lines), out)
	}

	msg := parseJSON(t, lines[0])
	if msg["text"] != "has replies" {
		t.Errorf("expected text='has replies', got %q", msg["text"])
	}
}

func TestMessageList_HasReactionsFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "text": "reacted", "ts": "1709251200.000100", "reactions": []map[string]any{{"name": "thumbsup", "count": 1}}},
				{"type": "message", "text": "no reactions", "ts": "1709251100.000050"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC", "--has-reactions")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 message + meta), got %d:\n%s", len(lines), out)
	}
}

func TestMessageGet_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		oldest := r.FormValue("oldest")
		if oldest == "1709251200.000100" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"messages": []map[string]any{{"type": "message", "user": "U01", "text": "found", "ts": "1709251200.000100"}},
			})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"messages": []map[string]any{},
			})
		}
	})

	out, err := runWithMock(t, mux, "message", "get", "C01ABC", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	msg := parseJSON(t, lines[0])
	if msg["input"] != "1709251200.000100" {
		t.Errorf("expected input='1709251200.000100', got %q", msg["input"])
	}
}

func TestMessageGet_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"messages": []map[string]any{},
		})
	})

	out, err := runWithMock(t, mux, "message", "get", "C01ABC", "9999999999.999999")
	if err == nil {
		t.Fatal("expected error for not found message")
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (error + meta), got %d:\n%s", len(lines), out)
	}

	errLine := parseJSON(t, lines[0])
	if errLine["error"] != "message_not_found" {
		t.Errorf("expected error='message_not_found', got %q", errLine["error"])
	}
}

func TestMessageList_Pagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": true,
			"messages": []map[string]any{{"type": "message", "text": "msg1", "ts": "1709251200.000100"}},
			"response_metadata": map[string]string{"next_cursor": "nextpage"},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC", "--limit", "1")
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

func TestMessageList_EnrichesUserName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "user": "U01", "text": "hello", "ts": "1709251200.000100"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			OK      bool              `json:"ok"`
			Members []map[string]any  `json:"members"`
		}{
			OK: true,
			Members: []map[string]any{
				{"id": "U01", "name": "tammer", "real_name": "Tammer Saleh", "profile": map[string]any{"email": "t@example.com"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	msg := parseJSON(t, lines[0])
	if msg["user"] != "U01" {
		t.Errorf("expected user='U01', got %q", msg["user"])
	}
	if msg["user_name"] == nil || msg["user_name"] == "" {
		t.Error("expected user_name to be enriched from cache")
	}
}
