package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPresenceGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.getPresence", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if u := r.FormValue("user"); u != "U01ABC" {
			t.Errorf("expected user='U01ABC', got %q", u)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          true,
			"presence":    "active",
			"online":      true,
			"auto_away":   false,
			"manual_away": false,
		})
	})

	out, err := runWithMock(t, mux, "presence", "get", "U01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["presence"] != "active" {
		t.Errorf("expected presence='active', got %q", item["presence"])
	}
	if item["user_id"] != "U01ABC" {
		t.Errorf("expected user_id='U01ABC', got %q", item["user_id"])
	}
}
