package resolve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// conversationsListMux wraps a ServeMux for tests about the org-wide walk. It
// serves an empty users.conversations so the member-scoped scan the resolver
// tries first misses cleanly and hands off to the given conversations.list
// handler. Any other path will 404 the resolver rather than masking a
// wrong-endpoint bug behind a catch-all handler.
func conversationsListMux(handler http.HandlerFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", emptyChannelsHandler)
	mux.HandleFunc("/api/conversations.list", handler)
	return mux
}

// emptyChannelsHandler is a one-page response with no channels.
func emptyChannelsHandler(w http.ResponseWriter, _ *http.Request) {
	writeChannelPage(w, "", nil)
}

// writeChannelPage encodes one page of either list endpoint's response.
func writeChannelPage(w http.ResponseWriter, nextCursor string, channels []map[string]any) {
	if channels == nil {
		channels = []map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":                true,
		"channels":          channels,
		"response_metadata": map[string]string{"next_cursor": nextCursor},
	})
}

// channelRow is a minimal channel as either list endpoint returns it.
func channelRow(id, name string) map[string]any {
	return map[string]any{"id": id, "name": name, "is_channel": true}
}

// resolverMux serves both endpoints ResolveChannel walks and counts the calls
// to each, so a test can assert which endpoint answered.
type resolverMux struct {
	handler     http.Handler
	memberCalls int
	orgCalls    int
}

func newResolverMux(member, org http.HandlerFunc) *resolverMux {
	rm := &resolverMux{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		rm.memberCalls++
		member(w, r)
	})
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		rm.orgCalls++
		org(w, r)
	})
	rm.handler = mux
	return rm
}

// TestResolveChannel_MemberHitSkipsOrgWalk covers the reason member-first
// exists: a name the user is a member of must cost one Tier-3 request, not a
// partial walk of every channel in the workspace. Measured on a large
// Enterprise Grid org, the org walk cost 75 and 95 requests for two member
// channels; the member scan finds them in one.
func TestResolveChannel_MemberHitSkipsOrgWalk(t *testing.T) {
	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{channelRow("C01ABC", "target")})
		},
		func(http.ResponseWriter, *http.Request) {
			t.Error("conversations.list must not be called when the member scan hits")
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), "", "")
	id, err := r.ResolveChannel(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C01ABC" {
		t.Errorf("got %q, want %q", id, "C01ABC")
	}
	if rm.memberCalls != 1 {
		t.Errorf("got %d users.conversations calls, want 1", rm.memberCalls)
	}
	if rm.orgCalls != 0 {
		t.Errorf("got %d conversations.list calls, want 0", rm.orgCalls)
	}
}

// The member scan only covers the user's own conversations. A name outside it
// must still resolve through the org-wide walk.
func TestResolveChannel_MemberMissFallsBackToOrgWalk(t *testing.T) {
	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{channelRow("C01ABC", "mine")})
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{channelRow("C02DEF", "theirs")})
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), "", "")
	id, err := r.ResolveChannel(context.Background(), "theirs")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C02DEF" {
		t.Errorf("got %q, want %q", id, "C02DEF")
	}
	if rm.memberCalls != 1 || rm.orgCalls != 1 {
		t.Errorf("got %d member / %d org calls, want 1 / 1", rm.memberCalls, rm.orgCalls)
	}
}

// The member scan sees only part of the workspace, so its results must never
// reach the file cache - that file is the complete conversations.list snapshot
// and other code is entitled to read it as one.
func TestResolveChannel_MemberHitDoesNotWriteFileCache(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T01ABC"

	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{channelRow("C01ABC", "target")})
		},
		func(http.ResponseWriter, *http.Request) {
			t.Error("conversations.list must not be called when the member scan hits")
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), teamID, cacheDir)
	if _, err := r.ResolveChannel(context.Background(), "target"); err != nil {
		t.Fatal(err)
	}

	cacheFile := filepath.Join(cacheDir, "channels-"+teamID+".json")
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		data, _ := os.ReadFile(cacheFile)
		t.Errorf("member scan wrote the org file cache: %s", data)
	}
}

// A member-scan failure is not a verdict on the name. Enterprise Grid returns
// enterprise_is_restricted for the org-level token, and an older OAuth token
// can be missing a scope; either way the org walk still knows the answer.
func TestResolveChannel_MemberScanErrorFallsBackToOrgWalk(t *testing.T) {
	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "enterprise_is_restricted"})
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{channelRow("C02DEF", "target")})
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), "", "")
	id, err := r.ResolveChannel(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C02DEF" {
		t.Errorf("got %q, want %q", id, "C02DEF")
	}
	if rm.orgCalls != 1 {
		t.Errorf("got %d conversations.list calls, want 1", rm.orgCalls)
	}
}

// Everything the member scan saw goes into the in-memory maps, so a second
// name from the same pages costs nothing. Without this a command resolving
// several channels re-walks the member list per name.
func TestResolveChannel_MemberHitCachesWholePageInMemory(t *testing.T) {
	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{
				channelRow("C01ABC", "first"),
				channelRow("C02DEF", "second"),
			})
		},
		func(http.ResponseWriter, *http.Request) {
			t.Error("conversations.list must not be called when the member scan hits")
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), "", "")
	ctx := context.Background()
	if _, err := r.ResolveChannel(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	id, err := r.ResolveChannel(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C02DEF" {
		t.Errorf("got %q, want %q", id, "C02DEF")
	}
	if rm.memberCalls != 1 {
		t.Errorf("got %d users.conversations calls, want 1 (page should be cached)", rm.memberCalls)
	}
	// The reverse index feeds output enrichment; both rows belong in it.
	if name, ok := r.LookupChannelName("C01ABC"); !ok || name != "first" {
		t.Errorf("reverse index missing C01ABC: got %q (ok=%v)", name, ok)
	}
}

// An org walk that early-exits saw only the pages before the match, so it must
// extend the cache rather than replace it. Replacing threw away names the
// member scan had already resolved in this process, which turned a resolved
// channel back into a cache miss and dropped its enrichment name.
func TestResolveChannel_OrgWalkHitKeepsEarlierMemberResults(t *testing.T) {
	rm := newResolverMux(
		func(w http.ResponseWriter, req *http.Request) {
			_ = req.ParseForm()
			// Page 1 carries the member channel; page 2 is where a name the
			// user is not in would have to be, and never arrives.
			if req.FormValue("cursor") == "" {
				writeChannelPage(w, "page2", []map[string]any{channelRow("C01ABC", "mine")})
				return
			}
			writeChannelPage(w, "", nil)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "page2", []map[string]any{channelRow("C02DEF", "theirs")})
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), "", "")
	ctx := context.Background()
	if _, err := r.ResolveChannel(ctx, "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveChannel(ctx, "theirs"); err != nil {
		t.Fatal(err)
	}

	if name, ok := r.LookupChannelName("C01ABC"); !ok || name != "mine" {
		t.Errorf("org walk dropped the member result from the reverse index: got %q (ok=%v)", name, ok)
	}
	memberCallsBefore := rm.memberCalls
	id, err := r.ResolveChannel(ctx, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C01ABC" {
		t.Errorf("got %q, want %q", id, "C01ABC")
	}
	if rm.memberCalls != memberCallsBefore {
		t.Errorf("re-resolving a cached name cost %d more member requests, want 0", rm.memberCalls-memberCallsBefore)
	}
}

// The file cache snapshot is complete but up to a day old, so a hit against it
// must not evict a channel the member scan resolved earlier in the same
// process - a channel joined since the snapshot is exactly what is missing
// from it, and re-resolving it costs another scan.
func TestResolveChannel_FileCacheHitKeepsEarlierMemberResults(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T01ABC"
	data, err := json.Marshal(channelFileCache{
		UpdatedAt: time.Now(),
		Channels:  map[string]string{"old-news": "C02DEF"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "channels-"+teamID+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{channelRow("C01ABC", "just-joined")})
		},
		func(http.ResponseWriter, *http.Request) {
			t.Error("conversations.list must not be called: one name is in the member scan, the other in the file cache")
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), teamID, cacheDir)
	ctx := context.Background()
	if _, err := r.ResolveChannel(ctx, "just-joined"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveChannel(ctx, "old-news"); err != nil {
		t.Fatal(err)
	}

	if name, ok := r.LookupChannelName("C01ABC"); !ok || name != "just-joined" {
		t.Errorf("file cache hit dropped the member result: got %q (ok=%v)", name, ok)
	}
	memberCallsBefore := rm.memberCalls
	if _, err := r.ResolveChannel(ctx, "just-joined"); err != nil {
		t.Fatal(err)
	}
	if rm.memberCalls != memberCallsBefore {
		t.Errorf("re-resolving a cached name cost %d more member requests, want 0", rm.memberCalls-memberCallsBefore)
	}
}

// A member-scan failure is worth seeing under --trace: the run silently costs
// the org walk, and the reason is the only clue why.
func TestResolveChannel_MemberScanFallbackIsTraced(t *testing.T) {
	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "enterprise_is_restricted"})
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{channelRow("C02DEF", "target")})
		},
	)

	var trace bytes.Buffer
	ctx := api.WithTracer(context.Background(), api.NewJSONLinesTracer(&trace))
	r := NewResolver(newTestClient(t, rm.handler), "", "")
	if _, err := r.ResolveChannel(ctx, "target"); err != nil {
		t.Fatal(err)
	}

	var fallback map[string]any
	for _, line := range strings.Split(strings.TrimSpace(trace.String()), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("trace line is not JSON: %q", line)
		}
		if ev["kind"] == "fallback" {
			fallback = ev
		}
	}
	if fallback == nil {
		t.Fatalf("no fallback event in trace: %q", trace.String())
	}
	if fallback["from"] != "users.conversations" || fallback["to"] != "conversations.list" {
		t.Errorf("got from=%v to=%v, want users.conversations -> conversations.list", fallback["from"], fallback["to"])
	}
	if reason, _ := fallback["reason"].(string); !strings.Contains(reason, "enterprise_is_restricted") {
		t.Errorf("got reason=%q, want it to carry the Slack error", reason)
	}
}

// The member scan asks for the same channel kinds the org walk does. Adding
// im/mpim would cost extra pages for names nobody resolves.
func TestResolveChannel_MemberScanRequestsPublicAndPrivateOnly(t *testing.T) {
	var gotTypes, gotExcludeArchived string
	rm := newResolverMux(
		func(w http.ResponseWriter, req *http.Request) {
			_ = req.ParseForm()
			gotTypes = req.FormValue("types")
			gotExcludeArchived = req.FormValue("exclude_archived")
			writeChannelPage(w, "", []map[string]any{channelRow("C01ABC", "target")})
		},
		func(http.ResponseWriter, *http.Request) {
			t.Error("conversations.list must not be called when the member scan hits")
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), "", "")
	if _, err := r.ResolveChannel(context.Background(), "target"); err != nil {
		t.Fatal(err)
	}
	if gotTypes != "public_channel,private_channel" {
		t.Errorf("got types=%q, want %q", gotTypes, "public_channel,private_channel")
	}
	if gotExcludeArchived != "true" {
		t.Errorf("got exclude_archived=%q, want %q", gotExcludeArchived, "true")
	}
}

// First match wins is the documented collision rule, and it has to hold for
// the value returned as well as the value cached - otherwise the first call
// and every later call disagree about the same name.
func TestResolveChannel_DuplicateNameFirstMatchWinsOnEveryCall(t *testing.T) {
	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{
				channelRow("C01ABC", "dupe"),
				channelRow("C02DEF", "dupe"),
			})
		},
		func(http.ResponseWriter, *http.Request) {
			t.Error("conversations.list must not be called when the member scan hits")
		},
	)

	r := NewResolver(newTestClient(t, rm.handler), "", "")
	ctx := context.Background()
	first, err := r.ResolveChannel(ctx, "dupe")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.ResolveChannel(ctx, "dupe")
	if err != nil {
		t.Fatal(err)
	}
	if first != "C01ABC" {
		t.Errorf("got %q from the scan, want the first match %q", first, "C01ABC")
	}
	if second != first {
		t.Errorf("cached call returned %q, scan returned %q - they must agree", second, first)
	}
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

	tests := []string{"U01XYZ789", "T01ABC123", "B012345678", "W98765432"}
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

// When conversations.info fails (e.g. Enterprise Grid returns
// enterprise_is_restricted), Enrich should not re-hit the API on every
// subsequent row. The per-session negative memo bounds it to one call per
// unresolvable ID; without it, a long thread with --fields channel_id turns
// into a per-message retry storm that compounds into rate-limit sleeps.
func TestEnsureChannelCache_FailureCachedAcrossEnrichCalls(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("conversations.list must not be called during enrichment")
	})
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "enterprise_is_restricted",
		})
	})
	client := newTestClient(t, mux)

	r := NewResolver(client, "", "")
	ctx := context.Background()

	for range 5 {
		r.Enrich(ctx, map[string]any{"channel_id": "C01ABC123"})
	}

	if calls != 1 {
		t.Errorf("conversations.info hit %d times, want 1 (failure should be memoized)", calls)
	}
}
