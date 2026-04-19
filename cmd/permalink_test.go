package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMessagePermalink_SingleTimestamp(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"channel":   r.FormValue("channel"),
			"permalink": "https://acme.slack.com/archives/C01ABC/p1709251200000100",
		})
	})

	out, err := runWithMock(t, mux, "message", "permalink", "C01ABC", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 result + meta), got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["input"] != "1709251200.000100" {
		t.Errorf("expected input='1709251200.000100', got %q", item["input"])
	}
	if item["channel"] != "C01ABC" {
		t.Errorf("expected channel='C01ABC', got %q", item["channel"])
	}
	if item["permalink"] != "https://acme.slack.com/archives/C01ABC/p1709251200000100" {
		t.Errorf("unexpected permalink: %q", item["permalink"])
	}

	meta := parseJSON(t, lines[1])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != false {
		t.Error("expected has_more=false")
	}
}

func TestMessagePermalink_MultipleTimestamps(t *testing.T) {
	mux := http.NewServeMux()
	callCount := 0
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		callCount++
		ts := r.FormValue("message_ts")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"channel":   r.FormValue("channel"),
			"permalink": "https://acme.slack.com/archives/C01ABC/p" + ts,
		})
	})

	out, err := runWithMock(t, mux, "message", "permalink", "C01ABC", "1.1", "2.2", "3.3")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (3 results + meta), got %d:\n%s", len(lines), out)
	}
	if callCount != 3 {
		t.Errorf("expected 3 API calls, got %d", callCount)
	}

	for i, expectedInput := range []string{"1.1", "2.2", "3.3"} {
		item := parseJSON(t, lines[i])
		if item["input"] != expectedInput {
			t.Errorf("line %d: expected input=%q, got %q", i, expectedInput, item["input"])
		}
		if item["ts"] != expectedInput {
			t.Errorf("line %d: expected ts=%q, got %q", i, expectedInput, item["ts"])
		}
		if item["permalink"] == nil || item["permalink"] == "" {
			t.Errorf("line %d: expected non-empty permalink", i)
		}
	}
}

func TestMessagePermalink_ChannelResolution(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01ABC", "name": "general", "is_member": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ch := r.FormValue("channel")
		if ch != "C01ABC" {
			t.Errorf("expected resolved channel C01ABC, got %q", ch)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"channel":   ch,
			"permalink": "https://acme.slack.com/archives/C01ABC/p100",
		})
	})

	out, err := runWithMock(t, mux, "message", "permalink", "#general", "1.0")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["channel"] != "C01ABC" {
		t.Errorf("expected resolved channel C01ABC, got %q", item["channel"])
	}
}

func TestMessagePermalink_AuthError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "not_authed",
		})
	})

	_, err := runWithMock(t, mux, "message", "permalink", "C01ABC", "1.0")
	if err == nil {
		t.Fatal("expected fatal error for not_authed")
	}
}

func TestMessagePermalink_PartialFailure(t *testing.T) {
	mux := http.NewServeMux()
	call := 0
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "message_not_found",
			})
			return
		}
		_ = r.ParseForm()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"channel":   r.FormValue("channel"),
			"permalink": "https://acme.slack.com/archives/C01ABC/pOK",
		})
	})

	out, err := runWithMock(t, mux, "message", "permalink", "C01ABC", "1.0", "2.0", "3.0")
	if err == nil {
		t.Fatal("expected error for partial failure")
	}

	lines := nonEmptyLines(out)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (2 ok + 1 error + meta), got %d:\n%s", len(lines), out)
	}

	// Second item should be the error.
	errItem := parseJSON(t, lines[1])
	if errItem["error"] != "message_not_found" {
		t.Errorf("expected error='message_not_found', got %q", errItem["error"])
	}
	if errItem["input"] != "2.0" {
		t.Errorf("expected input='2.0' on error item, got %q", errItem["input"])
	}

	meta := parseJSON(t, lines[3])
	m := meta["_meta"].(map[string]any)
	if m["error_count"] != float64(1) {
		t.Errorf("expected error_count=1, got %v", m["error_count"])
	}
}

func TestMessagePermalink_Fields(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"channel":   "C01ABC",
			"permalink": "https://acme.slack.com/archives/C01ABC/p100",
		})
	})

	out, err := runWithMock(t, mux, "--fields", "permalink", "message", "permalink", "C01ABC", "1.0")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	item := parseJSON(t, lines[0])
	if _, ok := item["permalink"]; !ok {
		t.Error("expected permalink field")
	}
	if _, ok := item["channel"]; ok {
		t.Error("expected channel to be filtered out by --fields")
	}
}
