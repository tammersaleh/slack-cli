package resolve

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// usersListHandler returns a mock handler that serves a users.list response.
func usersListHandler(t *testing.T, users []slack.User) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users.list" {
			http.Error(w, "not found", 404)
			return
		}
		resp := struct {
			OK      bool         `json:"ok"`
			Members []slack.User `json:"members"`
		}{OK: true, Members: users}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// usersInfoHandler returns a mock handler that serves users.info lookups
// from the given slice. Unknown IDs return user_not_found.
func usersInfoHandler(t *testing.T, users []slack.User) http.HandlerFunc {
	t.Helper()
	byID := make(map[string]slack.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users.info" {
			http.Error(w, "not found", 404)
			return
		}
		_ = r.ParseForm()
		uid := r.FormValue("user")
		u, ok := byID[uid]
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "user_not_found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": u})
	}
}

func testUsers() []slack.User {
	return []slack.User{
		{ID: "U001", Name: "alice", RealName: "Alice Smith", Profile: slack.UserProfile{Email: "alice@example.com"}},
		{ID: "U002", Name: "bob", RealName: "Bob Jones", Profile: slack.UserProfile{Email: "bob@example.com"}},
		{ID: "U003", Name: "charlie", RealName: "Charlie Brown", Profile: slack.UserProfile{Email: ""}},
	}
}

// TestLookupUser_SingleIDFetch verifies that LookupUser fetches a missing
// user via users.info instead of bulk-loading the whole users.list.
// Resolver is intentionally lazy here - small command outputs shouldn't
// pay the 17MB users.list cost.
func TestLookupUser_SingleIDFetch(t *testing.T) {
	callsList := 0
	callsInfo := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		callsList++
		usersListHandler(t, testUsers())(w, r)
	})
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		callsInfo++
		usersInfoHandler(t, testUsers())(w, r)
	})
	client := newTestClient(t, mux)

	r := NewResolver(client, "T123", t.TempDir())

	user, found, err := r.LookupUser(context.Background(), "U001")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected user to be found")
	}
	if user.Name != "alice" {
		t.Errorf("got name=%q, want %q", user.Name, "alice")
	}
	if callsList != 0 {
		t.Errorf("got %d users.list calls, want 0 (no bulk load from LookupUser)", callsList)
	}
	if callsInfo != 1 {
		t.Errorf("got %d users.info calls, want 1", callsInfo)
	}
}

func TestLookupUser_CacheReuse(t *testing.T) {
	callsInfo := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		callsInfo++
		usersInfoHandler(t, testUsers())(w, r)
	})
	client := newTestClient(t, mux)

	r := NewResolver(client, "T123", t.TempDir())
	ctx := context.Background()

	// First lookup fetches via users.info and caches the result.
	_, _, err := r.LookupUser(ctx, "U001")
	if err != nil {
		t.Fatal(err)
	}

	// Second lookup for same ID should use the cache from the first fetch.
	user, found, err := r.LookupUser(ctx, "U001")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected alice to be found from cache")
	}
	if user.Name != "alice" {
		t.Errorf("got name=%q, want %q", user.Name, "alice")
	}
	if callsInfo != 1 {
		t.Errorf("got %d users.info calls, want 1 (cache should be reused)", callsInfo)
	}
}

func TestLookupUser_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", usersInfoHandler(t, testUsers()))
	client := newTestClient(t, mux)
	r := NewResolver(client, "T123", t.TempDir())

	_, found, err := r.LookupUser(context.Background(), "U999")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("expected user not to be found")
	}
}

func TestResolveUser_EmailFromCache(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/api/users.list" {
			usersListHandler(t, testUsers())(w, r)
			return
		}
		t.Fatalf("unexpected API call to %s (email should resolve from cache)", r.URL.Path)
	}))

	r := NewResolver(client, "T123", t.TempDir())
	id, err := r.ResolveUser(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if id != "U001" {
		t.Errorf("got %q, want %q", id, "U001")
	}
	if calls != 1 {
		t.Errorf("got %d API calls, want 1 (users.list only)", calls)
	}
}

func TestResolveUser_EmailNotInCache(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/users.list" {
			usersListHandler(t, testUsers())(w, r)
			return
		}
		if r.URL.Path == "/api/users.lookupByEmail" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "users_not_found",
			})
			return
		}
		t.Fatalf("unexpected API call to %s", r.URL.Path)
	}))

	r := NewResolver(client, "T123", t.TempDir())
	_, err := r.ResolveUser(context.Background(), "unknown@example.com")
	if err == nil {
		t.Error("expected error for unknown email")
	}
}

func TestLookupUser_FileCacheHit(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

	// Pre-populate file cache.
	users := testUsers()
	cache := userFileCache{
		UpdatedAt: time.Now(),
		Users:     make(map[string]slack.User),
	}
	for _, u := range users {
		cache.Users[u.ID] = u
	}
	data, _ := json.Marshal(cache)
	cacheFile := filepath.Join(cacheDir, "users-"+teamID+".json")
	if err := os.WriteFile(cacheFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Client that should never be called.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when file cache is fresh")
	}))

	r := NewResolver(client, teamID, cacheDir)
	user, found, err := r.LookupUser(context.Background(), "U001")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected user to be found from file cache")
	}
	if user.Name != "alice" {
		t.Errorf("got name=%q, want %q", user.Name, "alice")
	}
}

func TestLookupUser_FileCacheExpired(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

	// Pre-populate expired file cache.
	cache := userFileCache{
		UpdatedAt: time.Now().Add(-25 * time.Hour),
		Users:     map[string]slack.User{"U001": {ID: "U001", Name: "stale"}},
	}
	data, _ := json.Marshal(cache)
	cacheFile := filepath.Join(cacheDir, "users-"+teamID+".json")
	if err := os.WriteFile(cacheFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	callsInfo := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		callsInfo++
		usersInfoHandler(t, testUsers())(w, r)
	})
	client := newTestClient(t, mux)

	r := NewResolver(client, teamID, cacheDir)
	user, found, err := r.LookupUser(context.Background(), "U001")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected user to be found after refetch")
	}
	if user.Name != "alice" {
		t.Errorf("got name=%q, want %q (should be fresh, not stale)", user.Name, "alice")
	}
	if callsInfo != 1 {
		t.Errorf("got %d users.info calls, want 1", callsInfo)
	}
}

// TestLookupUser_FileCacheNotWritten verifies that LookupUser's on-demand
// fetch does NOT write the full users.json file cache. Only ResolveUser's
// bulk-load path (via users.list) persists to disk - single-user lookups
// shouldn't stomp the 24h file cache with a partial dataset.
func TestLookupUser_FileCacheNotWritten(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", usersInfoHandler(t, testUsers()))
	client := newTestClient(t, mux)
	r := NewResolver(client, teamID, cacheDir)

	_, _, err := r.LookupUser(context.Background(), "U001")
	if err != nil {
		t.Fatal(err)
	}

	cacheFile := filepath.Join(cacheDir, "users-"+teamID+".json")
	if _, err := os.Stat(cacheFile); err == nil {
		t.Error("single-ID LookupUser should not write the bulk file cache")
	}
}

// TestLookupUser_IndexesName verifies the single-user fetch via users.info
// also wires the user into the name index, so a follow-up @name lookup in
// the same session hits memory cache instead of forcing a users.list load.
func TestLookupUser_IndexesName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", usersInfoHandler(t, testUsers()))
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("users.list should not be called; @name should resolve from the cache populated by users.info")
	})
	client := newTestClient(t, mux)

	r := NewResolver(client, "T123", t.TempDir())
	ctx := context.Background()

	// Populate the cache via LookupUser (simulates enrichment of a message).
	if _, _, err := r.LookupUser(ctx, "U001"); err != nil {
		t.Fatal(err)
	}

	// ResolveUser(@alice) should now find U001 in usersByName without
	// bulk-loading.
	id, err := r.ResolveUser(ctx, "@alice")
	if err != nil {
		t.Fatal(err)
	}
	if id != "U001" {
		t.Errorf("got %q, want %q", id, "U001")
	}
}

// TestResolveUser_BulkLoadWritesFileCache verifies that a name/display-name
// lookup (which needs the whole cache to scan) DOES write users.json so
// subsequent enrichment calls hit the file cache.
func TestResolveUser_BulkLoadWritesFileCache(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

	client := newTestClient(t, usersListHandler(t, testUsers()))
	r := NewResolver(client, teamID, cacheDir)

	// @alice triggers bulk load (no way to look up display name otherwise).
	_, err := r.ResolveUser(context.Background(), "@alice")
	if err != nil {
		t.Fatal(err)
	}

	cacheFile := filepath.Join(cacheDir, "users-"+teamID+".json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("file cache not written after bulk load: %v", err)
	}
	var fc userFileCache
	if err := json.Unmarshal(data, &fc); err != nil {
		t.Fatalf("invalid file cache: %v", err)
	}
	if len(fc.Users) != 3 {
		t.Errorf("expected 3 users in file cache, got %d", len(fc.Users))
	}
}

func TestResolveUser_EmailFromFileCacheIndex(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

	// Pre-populate file cache.
	users := testUsers()
	cache := userFileCache{
		UpdatedAt: time.Now(),
		Users:     make(map[string]slack.User),
	}
	for _, u := range users {
		cache.Users[u.ID] = u
	}
	data, _ := json.Marshal(cache)
	cacheFile := filepath.Join(cacheDir, "users-"+teamID+".json")
	if err := os.WriteFile(cacheFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Client that should never be called.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for cached email resolution")
	}))

	r := NewResolver(client, teamID, cacheDir)
	id, err := r.ResolveUser(context.Background(), "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if id != "U002" {
		t.Errorf("got %q, want %q", id, "U002")
	}
}

func TestResolveUser_DisplayName(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		usersListHandler(t, testUsers())(w, r)
	}))

	r := NewResolver(client, "T123", t.TempDir())
	id, err := r.ResolveUser(context.Background(), "@alice")
	if err != nil {
		t.Fatal(err)
	}
	if id != "U001" {
		t.Errorf("got %q, want %q", id, "U001")
	}
	if calls != 1 {
		t.Errorf("got %d API calls, want 1 (users.list only)", calls)
	}
}

func TestResolveUser_DisplayNameCaseInsensitive(t *testing.T) {
	client := newTestClient(t, usersListHandler(t, testUsers()))
	r := NewResolver(client, "T123", t.TempDir())

	id, err := r.ResolveUser(context.Background(), "@Alice Smith")
	if err != nil {
		t.Fatal(err)
	}
	if id != "U001" {
		t.Errorf("got %q, want %q", id, "U001")
	}
}

func TestResolveUser_DisplayNameNotFound(t *testing.T) {
	client := newTestClient(t, usersListHandler(t, testUsers()))
	r := NewResolver(client, "T123", t.TempDir())

	_, err := r.ResolveUser(context.Background(), "@nonexistent")
	if err == nil {
		t.Error("expected error for unknown display name")
	}
}

// When the user file cache is stale, loadUserFileCache must stat the file
// before reading it. A 16MB users-*.json parsed once costs ~80ms; parsed
// on every enriched row in a long thread list compounds into seconds of
// wasted CPU. The stat check short-circuits before paying the read+parse.
func TestLoadUserFileCache_FastFailsOnStaleMtime(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"
	cacheFile := filepath.Join(cacheDir, "users-"+teamID+".json")

	// Deliberately corrupt contents. If we read+parse, Unmarshal fails and
	// loadUserFileCache removes the file. With a stat-first check we never
	// touch the bytes and the file survives.
	if err := os.WriteFile(cacheFile, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(cacheFile, stale, stale); err != nil {
		t.Fatal(err)
	}

	r := NewResolver(nil, teamID, cacheDir)
	fc, err := r.loadUserFileCache()
	if err != nil {
		t.Fatalf("stale cache should return nil error, got %v", err)
	}
	if fc != nil {
		t.Error("stale cache should return nil *userFileCache")
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("stale cache file should not have been read: %v", err)
	}
}
