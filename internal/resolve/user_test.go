package resolve

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/api"
)

func TestResolveUser_IDPassthrough(t *testing.T) {
	r := NewResolver(api.NewWithAPIURL("xoxb-unused", "http://unused/api/"))

	tests := []struct {
		name  string
		input string
	}{
		{"U prefix", "U01ABC123"},
		{"W prefix", "W01ABC123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := r.ResolveUser(context.Background(), tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if id != tt.input {
				t.Errorf("got %q, want %q", id, tt.input)
			}
		})
	}
}

func TestResolveUser_Email(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users.lookupByEmail" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		email := r.FormValue("email")
		if email != "tammer@example.com" {
			t.Errorf("got email=%q, want %q", email, "tammer@example.com")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"user": map[string]any{
				"id":   "U789",
				"name": "tammer",
			},
		})
	}))

	r := NewResolver(client)
	id, err := r.ResolveUser(context.Background(), "tammer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if id != "U789" {
		t.Errorf("got %q, want %q", id, "U789")
	}
}

func TestResolveUser_EmailNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "users_not_found",
		})
	}))

	r := NewResolver(client)
	_, err := r.ResolveUser(context.Background(), "nobody@example.com")
	if err == nil {
		t.Error("expected error for unknown email")
	}
}

func TestResolveUser_InvalidFormat(t *testing.T) {
	r := NewResolver(api.NewWithAPIURL("xoxb-unused", "http://unused/api/"))
	_, err := r.ResolveUser(context.Background(), "tammer")
	if err == nil {
		t.Error("expected error for bare name")
	}
}
