package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPinList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pins.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if ch := r.FormValue("channel"); ch != "C01ABC" {
			t.Errorf("expected channel='C01ABC', got %q", ch)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"items": []map[string]any{
				{
					"type":    "message",
					"channel": "C01ABC",
					"message": map[string]any{"text": "pinned msg", "ts": "1709251200.000100", "user": "U01"},
				},
			},
		})
	})

	out, err := runWithMock(t, mux, "pin", "list", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 pin + meta), got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["type"] != "message" {
		t.Errorf("expected type='message', got %q", item["type"])
	}
	if item["channel"] != "C01ABC" {
		t.Errorf("expected channel='C01ABC', got %q", item["channel"])
	}
}

func TestPinList_ChannelResolution(t *testing.T) {
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
	mux.HandleFunc("/api/pins.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if ch := r.FormValue("channel"); ch != "C01ABC" {
			t.Errorf("expected resolved channel C01ABC, got %q", ch)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"items": []map[string]any{},
		})
	})

	_, err := runWithMock(t, mux, "pin", "list", "#general")
	if err != nil {
		t.Fatal(err)
	}
}
