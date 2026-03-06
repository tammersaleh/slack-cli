package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

func TestUserList_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{"id": "U01", "name": "tammer", "real_name": "Tammer Saleh", "profile": map[string]any{"email": "tammer@example.com"}},
				{"id": "U02", "name": "alice", "real_name": "Alice Smith", "profile": map[string]any{"email": "alice@example.com"}},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "user", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 users + meta), got %d:\n%s", len(lines), out)
	}

	user := parseJSON(t, lines[0])
	if user["name"] != "tammer" {
		t.Errorf("expected name='tammer', got %q", user["name"])
	}
}

func TestUserList_QueryFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{"id": "U01", "name": "tammer", "real_name": "Tammer Saleh", "profile": map[string]any{"email": "tammer@example.com"}},
				{"id": "U02", "name": "alice", "real_name": "Alice Smith", "profile": map[string]any{"email": "alice@example.com"}},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "user", "list", "--query", "tammer")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 user + meta), got %d:\n%s", len(lines), out)
	}
}

func TestUserInfo_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"user": map[string]any{
				"id": "U01", "name": "tammer", "real_name": "Tammer Saleh",
				"profile": map[string]any{"email": "tammer@example.com"},
			},
		})
	})

	out, err := runWithMock(t, mux, "user", "info", "U01")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 user + meta), got %d:\n%s", len(lines), out)
	}

	user := parseJSON(t, lines[0])
	if user["input"] != "U01" {
		t.Errorf("expected input='U01', got %q", user["input"])
	}
	if user["name"] != "tammer" {
		t.Errorf("expected name='tammer', got %q", user["name"])
	}
}

func TestUserInfo_ByEmail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.lookupByEmail", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"user": map[string]any{"id": "U01", "name": "tammer"},
		})
	})
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"user": map[string]any{"id": "U01", "name": "tammer", "real_name": "Tammer Saleh"},
		})
	})

	out, err := runWithMock(t, mux, "user", "info", "tammer@example.com")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	user := parseJSON(t, lines[0])
	if user["input"] != "tammer@example.com" {
		t.Errorf("expected input='tammer@example.com', got %q", user["input"])
	}
}

func TestUserInfo_PartialFailure_NoStderr(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		uid := r.FormValue("user")
		if uid == "U01" {
			json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"user": map[string]any{"id": "U01", "name": "tammer"},
			})
		} else {
			json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "user_not_found",
			})
		}
	})

	r := runWithMockFull(t, mux, "user", "info", "U01", "U99INVALID")
	if r.err == nil {
		t.Fatal("expected error for partial failure")
	}
	var oErr *output.Error
	if errors.As(r.err, &oErr) {
		t.Errorf("partial failure should not return *output.Error (would be printed to stderr), got: %v", r.err)
	}
}

func TestUserInfo_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "user_not_found",
		})
	})

	_, err := runWithMock(t, mux, "user", "info", "U99INVALID")
	if err == nil {
		t.Fatal("expected error for not found user")
	}
}
