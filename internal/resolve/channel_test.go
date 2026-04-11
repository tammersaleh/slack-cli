package resolve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	r := NewResolver(api.NewWithAPIURL("xoxb-unused", "http://unused/api/"), "", "")
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

	r := NewResolver(client, "", "")
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

	r := NewResolver(client, "", "")
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

	r := NewResolver(client, "", "")
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

	r := NewResolver(client, "", "")

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

	r := NewResolver(client, "", "")
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

func TestResolveChannel_EarlyExit(t *testing.T) {
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
					{"id": "C222", "name": "target", "is_channel": true},
				},
				"response_metadata": map[string]string{"next_cursor": "page2"},
			})
		default:
			t.Fatalf("should not fetch page with cursor %q (early exit expected)", cursor)
		}
	}))

	r := NewResolver(client, "", "")
	id, err := r.ResolveChannel(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C222" {
		t.Errorf("got %q, want %q", id, "C222")
	}
	if calls != 1 {
		t.Errorf("got %d API calls, want 1 (should stop after first page)", calls)
	}
}

func TestResolveChannel_FileCacheHit(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

	// Pre-populate file cache.
	cache := channelFileCache{
		UpdatedAt: time.Now(),
		Channels:  map[string]string{"cached-channel": "C999"},
	}
	data, _ := json.Marshal(cache)
	cacheFile := filepath.Join(cacheDir, "channels-"+teamID+".json")
	if err := os.WriteFile(cacheFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Client that should never be called.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when file cache is fresh")
	}))

	r := NewResolver(client, teamID, cacheDir)
	id, err := r.ResolveChannel(context.Background(), "cached-channel")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C999" {
		t.Errorf("got %q, want %q", id, "C999")
	}
}

func TestResolveChannel_FileCacheExpired(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

	// Pre-populate expired file cache (older than 24h default TTL).
	cache := channelFileCache{
		UpdatedAt: time.Now().Add(-25 * time.Hour),
		Channels:  map[string]string{"stale-channel": "C888"},
	}
	data, _ := json.Marshal(cache)
	cacheFile := filepath.Join(cacheDir, "channels-"+teamID+".json")
	if err := os.WriteFile(cacheFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C111", "name": "general", "is_channel": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	}))

	r := NewResolver(client, teamID, cacheDir)
	_, err := r.ResolveChannel(context.Background(), "stale-channel")
	if err == nil {
		t.Error("expected error - stale-channel not in fresh API results")
	}
	if calls != 1 {
		t.Errorf("got %d API calls, want 1 (should refetch after expired cache)", calls)
	}
}

func TestResolveChannel_FileCacheWrittenAfterFullPagination(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

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

	r := NewResolver(client, teamID, cacheDir)
	// Resolve a channel that doesn't exist to force full pagination.
	_, _ = r.ResolveChannel(context.Background(), "nonexistent")

	// Verify file cache was written.
	cacheFile := filepath.Join(cacheDir, "channels-"+teamID+".json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("file cache not written: %v", err)
	}
	var cache channelFileCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("invalid file cache: %v", err)
	}
	if cache.Channels["general"] != "C111" {
		t.Errorf("file cache missing general, got %v", cache.Channels)
	}
	if cache.Channels["random"] != "C222" {
		t.Errorf("file cache missing random, got %v", cache.Channels)
	}
}

func TestLookupChannelName(t *testing.T) {
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

	r := NewResolver(client, "", "")

	// Populate cache by resolving a channel.
	_, err := r.ResolveChannel(context.Background(), "general")
	if err != nil {
		t.Fatal(err)
	}

	// Reverse lookup should work.
	name, ok := r.LookupChannelName("C111")
	if !ok || name != "general" {
		t.Errorf("expected general, got %q (ok=%v)", name, ok)
	}
	name, ok = r.LookupChannelName("C222")
	if !ok || name != "random" {
		t.Errorf("expected random, got %q (ok=%v)", name, ok)
	}

	// Unknown ID returns false.
	_, ok = r.LookupChannelName("C999")
	if ok {
		t.Error("expected false for unknown channel")
	}
}
