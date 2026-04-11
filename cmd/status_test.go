package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestStatusGet_WithUser(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{
					"id":        "U01ABC",
					"name":      "tammer",
					"real_name": "Tammer Saleh",
					"profile": map[string]any{
						"email":             "tammer@example.com",
						"status_text":       "In a meeting",
						"status_emoji":      ":calendar:",
						"status_expiration": 1709254800,
					},
				},
			},
		})
	})
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"user": map[string]any{
				"id":        "U01ABC",
				"name":      "tammer",
				"real_name": "Tammer Saleh",
				"profile": map[string]any{
					"status_text":       "In a meeting",
					"status_emoji":      ":calendar:",
					"status_expiration": 1709254800,
				},
			},
		})
	})

	out, err := runWithMock(t, mux, "status", "get", "U01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["status_text"] != "In a meeting" {
		t.Errorf("expected status_text='In a meeting', got %q", item["status_text"])
	}
	if item["user_id"] != "U01ABC" {
		t.Errorf("expected user_id='U01ABC', got %q", item["user_id"])
	}
}

func TestStatusGet_DefaultUser(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth.test", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     "https://acme.slack.com/",
			"team":    "Acme",
			"team_id": "T01",
			"user":    "tammer",
			"user_id": "U01ABC",
		})
	})
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{
					"id":   "U01ABC",
					"name": "tammer",
					"profile": map[string]any{
						"status_text":  "Working",
						"status_emoji": ":computer:",
					},
				},
			},
		})
	})
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"user": map[string]any{
				"id":   "U01ABC",
				"name": "tammer",
				"profile": map[string]any{
					"status_text":  "Working",
					"status_emoji": ":computer:",
				},
			},
		})
	})

	out, err := runWithMock(t, mux, "status", "get")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["status_text"] != "Working" {
		t.Errorf("expected status_text='Working', got %q", item["status_text"])
	}
}
