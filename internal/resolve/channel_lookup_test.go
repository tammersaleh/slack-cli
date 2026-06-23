package resolve

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// conversationsInfoHandler serves conversations.info lookups from the given
// id->name map. Unknown IDs return channel_not_found. Any other path 404s so a
// stray conversations.list call (the bug this fix prevents) fails the test.
func conversationsInfoHandler(t *testing.T, names map[string]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/conversations.info" {
			t.Errorf("unexpected API call: %s (want conversations.info)", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		_ = r.ParseForm()
		id := r.FormValue("channel")
		name, ok := names[id]
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "channel_not_found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": id, "name": name},
		})
	}
}

// TestLookupChannel_SingleIDFetch verifies a cache miss resolves via a single
// conversations.info call, never the bulk conversations.list pagination.
func TestLookupChannel_SingleIDFetch(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("conversations.list must not be called for a single-ID lookup")
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		calls++
		conversationsInfoHandler(t, map[string]string{"C01ABC": "general"})(w, r)
	})
	r := NewResolver(newTestClient(t, mux), "T123", t.TempDir())

	name, found := r.LookupChannel(context.Background(), "C01ABC")
	if !found {
		t.Fatal("expected channel to be found")
	}
	if name != "general" {
		t.Errorf("got name=%q, want %q", name, "general")
	}
	if calls != 1 {
		t.Errorf("got %d conversations.info calls, want 1", calls)
	}
}

// TestLookupChannel_CacheReuse verifies a second lookup of the same ID hits the
// in-memory cache populated by the first fetch.
func TestLookupChannel_CacheReuse(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		calls++
		conversationsInfoHandler(t, map[string]string{"C01ABC": "general"})(w, r)
	})
	r := NewResolver(newTestClient(t, mux), "T123", t.TempDir())
	ctx := context.Background()

	if _, found := r.LookupChannel(ctx, "C01ABC"); !found {
		t.Fatal("first lookup should find the channel")
	}
	if _, found := r.LookupChannel(ctx, "C01ABC"); !found {
		t.Fatal("second lookup should find the channel from cache")
	}
	if calls != 1 {
		t.Errorf("got %d conversations.info calls, want 1 (cache reused)", calls)
	}
}

// TestLookupChannel_NegativeMemo verifies a failed lookup is memoized for the
// session so repeated rows with the same unresolvable ID don't re-hit the API,
// while a *different* ID is unaffected (the memo is keyed by ID, not global).
func TestLookupChannel_NegativeMemo(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		calls++
		conversationsInfoHandler(t, map[string]string{"C0GOOD": "general"})(w, r)
	})
	r := NewResolver(newTestClient(t, mux), "T123", t.TempDir())
	ctx := context.Background()

	if _, found := r.LookupChannel(ctx, "C0BAD"); found {
		t.Fatal("expected lookup to fail")
	}
	if _, found := r.LookupChannel(ctx, "C0BAD"); found {
		t.Fatal("expected second lookup to fail")
	}
	if calls != 1 {
		t.Errorf("got %d conversations.info calls, want 1 (failure memoized)", calls)
	}

	// A different, resolvable ID must not be blocked by C0BAD's memo.
	if name, found := r.LookupChannel(ctx, "C0GOOD"); !found || name != "general" {
		t.Errorf("got (%q, %v) for C0GOOD, want (\"general\", true)", name, found)
	}
	if calls != 2 {
		t.Errorf("got %d conversations.info calls, want 2 (memo must be per-ID)", calls)
	}
}

// TestLookupChannel_EmptyNameMemoized verifies a channel that resolves with an
// empty name (e.g. a DM) is treated as not-found and memoized.
func TestLookupChannel_EmptyNameMemoized(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "D01IM", "name": ""},
		})
	})
	r := NewResolver(newTestClient(t, mux), "T123", t.TempDir())
	ctx := context.Background()

	if _, found := r.LookupChannel(ctx, "D01IM"); found {
		t.Fatal("expected empty-name channel to be not-found")
	}
	if _, found := r.LookupChannel(ctx, "D01IM"); found {
		t.Fatal("expected second empty-name lookup to be not-found")
	}
	if calls != 1 {
		t.Errorf("got %d conversations.info calls, want 1 (empty name memoized)", calls)
	}
}

// TestEnrich_ChannelUsesInfoNotList is the regression guard for the 13-minute
// hang: enriching a channel_id must use conversations.info, never bulk
// conversations.list.
func TestEnrich_ChannelUsesInfoNotList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("conversations.list must not be called during enrichment")
	})
	mux.HandleFunc("/api/conversations.info", conversationsInfoHandler(t, map[string]string{"C01ABC": "general"}))
	r := NewResolver(newTestClient(t, mux), "T123", t.TempDir())

	m := map[string]any{"channel_id": "C01ABC"}
	r.Enrich(context.Background(), m)

	if m["channel_name"] != "general" {
		t.Errorf("got channel_name=%v, want %q", m["channel_name"], "general")
	}
}

// TestEnrich_ChannelNamePresentSkipsLookup verifies enrichment doesn't overwrite
// or re-fetch when channel_name is already set.
func TestEnrich_ChannelNamePresentSkipsLookup(t *testing.T) {
	r := NewResolver(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Fatalf("no API call expected when channel_name is present: %s", req.URL.Path)
	})), "T123", t.TempDir())

	m := map[string]any{"channel_id": "C01ABC", "channel_name": "preset"}
	r.Enrich(context.Background(), m)

	if m["channel_name"] != "preset" {
		t.Errorf("got channel_name=%v, want %q", m["channel_name"], "preset")
	}
}
