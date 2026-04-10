package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMessagePost_Text(t *testing.T) {
	var gotText, gotChannel string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotText = r.FormValue("text")
		gotChannel = r.FormValue("channel")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": gotChannel,
			"ts":      "1709251200.000100",
		})
	})

	out, err := runWithMock(t, mux, "message", "post", "C01ABC", "--text", "Deploy complete")
	if err != nil {
		t.Fatal(err)
	}

	if gotText != "Deploy complete" {
		t.Errorf("expected text='Deploy complete', got %q", gotText)
	}
	if gotChannel != "C01ABC" {
		t.Errorf("expected channel='C01ABC', got %q", gotChannel)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (result + meta), got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["channel"] != "C01ABC" {
		t.Errorf("expected channel='C01ABC', got %q", item["channel"])
	}
	if item["ts"] != "1709251200.000100" {
		t.Errorf("expected ts, got %q", item["ts"])
	}
	if _, ok := item["ts_iso"]; !ok {
		t.Error("expected ts_iso field")
	}
}

func TestMessagePost_Thread(t *testing.T) {
	var gotThreadTS string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotThreadTS = r.FormValue("thread_ts")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": "C01ABC",
			"ts":      "1709251200.000200",
		})
	})

	out, err := runWithMock(t, mux, "message", "post", "C01ABC", "--text", "reply", "--thread", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	if gotThreadTS != "1709251200.000100" {
		t.Errorf("expected thread_ts='1709251200.000100', got %q", gotThreadTS)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
}

func TestMessagePost_ChannelResolution(t *testing.T) {
	var gotChannel string
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
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.FormValue("channel")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": gotChannel,
			"ts":      "1709251200.000100",
		})
	})

	_, err := runWithMock(t, mux, "message", "post", "#general", "--text", "hello")
	if err != nil {
		t.Fatal(err)
	}

	if gotChannel != "C01ABC" {
		t.Errorf("expected resolved channel C01ABC, got %q", gotChannel)
	}
}

func TestMessagePost_MissingText(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMock(t, mux, "message", "post", "C01ABC")
	if err == nil {
		t.Fatal("expected error when no --text or --stdin")
	}
}

func TestMessagePost_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "not_authed",
		})
	})

	_, err := runWithMock(t, mux, "message", "post", "C01ABC", "--text", "hello")
	if err == nil {
		t.Fatal("expected error for not_authed")
	}
}
