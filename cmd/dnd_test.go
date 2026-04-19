package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDndInfo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dnd.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if u := r.FormValue("user"); u != "U01ABC" {
			t.Errorf("expected user='U01ABC', got %q", u)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                 true,
			"dnd_enabled":        true,
			"next_dnd_start_ts":  1709272800,
			"next_dnd_end_ts":    1709308800,
			"snooze_enabled":     false,
		})
	})

	out, err := runWithMock(t, mux, "dnd", "info", "U01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["dnd_enabled"] != true {
		t.Errorf("expected dnd_enabled=true, got %v", item["dnd_enabled"])
	}
	if item["user_id"] != "U01ABC" {
		t.Errorf("expected user_id='U01ABC', got %q", item["user_id"])
	}
}
