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

// conversationsListMux wraps a ServeMux that only handles
// /api/conversations.list. Any other path will 404 the resolver rather than
// masking a wrong-endpoint bug behind a catch-all handler.
func conversationsListMux(handler http.HandlerFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", handler)
	return mux
}

func TestResolveChannel_IDPassthrough(t *testing.T) {
	// No API calls needed for IDs.
	r := NewResolver(api.NewWithAPIURL("xoxb-unused", "http://unused/api/"), "", "")

	tests := []struct {
		name  string
		input string
	}{
		{"C prefix (channel)", "C01ABC123"},
		{"G prefix (group DM)", "G01ABC123"},
		{"D prefix (DM)", "D01ABC123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := r.ResolveChannel(context.Background(), tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if id != tt.input {
				t.Errorf("got %q, want %q", id, tt.input)
			}
		})
	}
}

func TestResolveChannel_NonChannelIDFastFails(t *testing.T) {
	// If input looks like a Slack ID with a non-channel prefix, we should
	// fail immediately without paginating. This handler would fail the test
	// if the resolver called it.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected API call for non-channel ID: %s", r.URL.Path)
	}))
	r := NewResolver(client, "", "")

	tests := []string{"U09T3DUS6P9", "T01ABC123", "B012345678", "W98765432"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := r.ResolveChannel(context.Background(), input)
			if err == nil {
				t.Fatalf("expected error for non-channel ID %q, got nil", input)
			}
		})
	}
}

func TestResolveChannel_ByName(t *testing.T) {
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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

	// Pre-populate expired file cache (older than 24h default TTL). Mtime
	// must also be stale - loadFileCache fast-fails on mtime before
	// reading, so a fresh mtime would mask the expiry path.
	cache := channelFileCache{
		UpdatedAt: time.Now().Add(-25 * time.Hour),
		Channels:  map[string]string{"stale-channel": "C888"},
	}
	data, _ := json.Marshal(cache)
	cacheFile := filepath.Join(cacheDir, "channels-"+teamID+".json")
	if err := os.WriteFile(cacheFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(cacheFile, stale, stale); err != nil {
		t.Fatal(err)
	}

	calls := 0
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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

	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
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

// Mirrors TestLoadUserFileCache_FastFailsOnStaleMtime for the channel
// file cache: stat-first must short-circuit before any ReadFile/Unmarshal.
// Corrupt bytes with a stale mtime confirm we never touched them - a
// read+parse would fail Unmarshal and trigger the cleanup os.Remove.
func TestLoadFileCache_FastFailsOnStaleMtime(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"
	cacheFile := filepath.Join(cacheDir, "channels-"+teamID+".json")

	if err := os.WriteFile(cacheFile, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(cacheFile, stale, stale); err != nil {
		t.Fatal(err)
	}

	r := NewResolver(nil, teamID, cacheDir)
	fc, err := r.loadFileCache()
	if err != nil {
		t.Fatalf("stale cache should return nil error, got %v", err)
	}
	if fc != nil {
		t.Error("stale cache should return nil *channelFileCache")
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("stale cache file should not have been read: %v", err)
	}
}

// When conversations.list fails (e.g. Enterprise Grid returns
// enterprise_is_restricted), Enrich should not re-trigger the bulk load on
// every subsequent item. Without this guard, a long thread with --fields
// channel_id turns into a per-message retry storm that compounds into
// rate-limit sleeps.
func TestEnsureChannelCache_FailureCachedAcrossEnrichCalls(t *testing.T) {
	calls := 0
	client := newTestClient(t, conversationsListMux(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "enterprise_is_restricted",
		})
	}))

	r := NewResolver(client, "", "")
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		r.Enrich(ctx, map[string]any{"channel_id": "C01ABC123"})
	}

	if calls != 1 {
		t.Errorf("conversations.list hit %d times, want 1 (failure should be cached)", calls)
	}
}
