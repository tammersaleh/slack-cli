package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestUsergroupList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/usergroups.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"usergroups": []map[string]any{
				{"id": "S01ABC", "name": "Engineering", "handle": "engineering", "user_count": 34},
				{"id": "S02DEF", "name": "Design", "handle": "design", "user_count": 12},
			},
		})
	})

	out, err := runWithMock(t, mux, "usergroup", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 groups + meta), got %d:\n%s", len(lines), out)
	}

	g := parseJSON(t, lines[0])
	if g["id"] != "S01ABC" {
		t.Errorf("expected id='S01ABC', got %q", g["id"])
	}
	if g["name"] != "Engineering" {
		t.Errorf("expected name='Engineering', got %q", g["name"])
	}
}

func TestUsergroupList_Query(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/usergroups.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"usergroups": []map[string]any{
				{"id": "S01ABC", "name": "Engineering"},
				{"id": "S02DEF", "name": "Design"},
			},
		})
	})

	out, err := runWithMock(t, mux, "usergroup", "list", "--query", "eng")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 match + meta), got %d:\n%s", len(lines), out)
	}
}

func TestUsergroupMembers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/usergroups.users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"users": []string{"U01", "U02", "U03"},
		})
	})

	out, err := runWithMock(t, mux, "usergroup", "members", "S01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (3 members + meta), got %d:\n%s", len(lines), out)
	}

	m := parseJSON(t, lines[0])
	if m["user_id"] != "U01" {
		t.Errorf("expected user_id='U01', got %q", m["user_id"])
	}
}
