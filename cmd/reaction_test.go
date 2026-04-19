package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestReactionList_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/reactions.get", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"type": "message",
			"message": map[string]any{
				"reactions": []map[string]any{
					{"name": "thumbsup", "count": 3, "users": []string{"U01", "U02", "U03"}},
					{"name": "tada", "count": 1, "users": []string{"U02"}},
				},
			},
		})
	})

	out, err := runWithMock(t, mux, "reaction", "list", "C01ABC", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 2 reactions + _meta
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 reactions + meta), got %d:\n%s", len(lines), out)
	}

	r1 := parseJSON(t, lines[0])
	if r1["name"] != "thumbsup" {
		t.Errorf("expected name='thumbsup', got %q", r1["name"])
	}
	if r1["input"] != "1709251200.000100" {
		t.Errorf("expected input='1709251200.000100', got %q", r1["input"])
	}
	if r1["count"] != float64(3) {
		t.Errorf("expected count=3, got %v", r1["count"])
	}
}

func TestReactionList_NoReactions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/reactions.get", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"type": "message",
			"message": map[string]any{
				"reactions": []map[string]any{},
			},
		})
	})

	out, err := runWithMock(t, mux, "reaction", "list", "C01ABC", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// Just _meta
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (meta only), got %d:\n%s", len(lines), out)
	}
}

func TestReactionList_MultipleTimestamps(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/reactions.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ts := r.FormValue("timestamp")
		if ts == "1709251200.000100" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"type": "message",
				"message": map[string]any{
					"reactions": []map[string]any{
						{"name": "thumbsup", "count": 1, "users": []string{"U01"}},
					},
				},
			})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "message_not_found",
			})
		}
	})

	out, err := runWithMock(t, mux, "reaction", "list", "C01ABC", "1709251200.000100", "9999999999.999999")
	if err == nil {
		t.Fatal("expected error for partial failure")
	}

	lines := nonEmptyLines(out)
	// 1 reaction + 1 inline error + _meta = 3
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}

	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["error_count"] != float64(1) {
		t.Errorf("expected error_count=1, got %v", m["error_count"])
	}
}
