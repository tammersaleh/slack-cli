package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWorkspaceInfo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/team.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"team": map[string]any{
				"id":     "T01ABC",
				"name":   "Acme Corp",
				"domain": "acme",
			},
		})
	})

	out, err := runWithMock(t, mux, "workspace-info", "info")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["id"] != "T01ABC" {
		t.Errorf("expected id='T01ABC', got %q", item["id"])
	}
	if item["name"] != "Acme Corp" {
		t.Errorf("expected name='Acme Corp', got %q", item["name"])
	}
}
