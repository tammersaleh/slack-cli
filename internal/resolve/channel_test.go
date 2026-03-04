package resolve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/api"
)

func newTestClient(t *testing.T, handler http.Handler) *api.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return api.NewWithAPIURL("xoxb-test", srv.URL+"/api/")
}

func TestResolveChannel_IDPassthrough(t *testing.T) {
	// No API calls needed for IDs.
	r := NewResolver(api.NewWithAPIURL("xoxb-unused", "http://unused/api/"))
	id, err := r.ResolveChannel(context.Background(), "C01ABC123")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C01ABC123" {
		t.Errorf("got %q, want %q", id, "C01ABC123")
	}
}

func TestResolveChannel_ByName(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C111", "name": "general", "is_channel": true},
				{"id": "C222", "name": "random", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	}))

	r := NewResolver(client)
	id, err := r.ResolveChannel(context.Background(), "general")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C111" {
		t.Errorf("got %q, want %q", id, "C111")
	}
}

func TestResolveChannel_HashPrefix(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C111", "name": "general", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	}))

	r := NewResolver(client)
	id, err := r.ResolveChannel(context.Background(), "#general")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C111" {
		t.Errorf("got %q, want %q", id, "C111")
	}
}

func TestResolveChannel_NotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          []map[string]any{},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	}))

	r := NewResolver(client)
	_, err := r.ResolveChannel(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent channel")
	}
}

func TestResolveChannel_CacheReuse(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C111", "name": "general", "is_channel": true},
				{"id": "C222", "name": "random", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	}))

	r := NewResolver(client)

	// First call populates cache.
	_, err := r.ResolveChannel(context.Background(), "general")
	if err != nil {
		t.Fatal(err)
	}

	// Second call should use cache.
	_, err = r.ResolveChannel(context.Background(), "random")
	if err != nil {
		t.Fatal(err)
	}

	if calls != 1 {
		t.Errorf("got %d API calls, want 1 (cache should be reused)", calls)
	}
}

func TestResolveChannel_Pagination(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		cursor := r.FormValue("cursor")
		switch cursor {
		case "":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"channels": []map[string]any{
					{"id": "C111", "name": "general", "is_channel": true},
				},
				"response_metadata": map[string]string{"next_cursor": "page2"},
			})
		case "page2":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"channels": []map[string]any{
					{"id": "C222", "name": "target", "is_channel": true},
				},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		default:
			t.Fatalf("unexpected cursor %q", cursor)
		}
	}))

	r := NewResolver(client)
	id, err := r.ResolveChannel(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C222" {
		t.Errorf("got %q, want %q", id, "C222")
	}
	if calls != 2 {
		t.Errorf("got %d API calls, want 2", calls)
	}
}
