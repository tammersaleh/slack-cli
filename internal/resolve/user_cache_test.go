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

func testUsers() []slack.User {
	return []slack.User{
		{ID: "U001", Name: "alice", RealName: "Alice Smith", Profile: slack.UserProfile{Email: "alice@example.com"}},
		{ID: "U002", Name: "bob", RealName: "Bob Jones", Profile: slack.UserProfile{Email: "bob@example.com"}},
		{ID: "U003", Name: "charlie", RealName: "Charlie Brown", Profile: slack.UserProfile{Email: ""}},
	}
}

func TestLookupUser_BulkLoad(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		usersListHandler(t, testUsers())(w, r)
	}))

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
	if calls != 1 {
		t.Errorf("got %d API calls, want 1", calls)
	}
}

func TestLookupUser_CacheReuse(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		usersListHandler(t, testUsers())(w, r)
	}))

	r := NewResolver(client, "T123", t.TempDir())
	ctx := context.Background()

	// First lookup triggers bulk load.
	_, _, err := r.LookupUser(ctx, "U001")
	if err != nil {
		t.Fatal(err)
	}

	// Second lookup should use cache.
	user, found, err := r.LookupUser(ctx, "U002")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected bob to be found from cache")
	}
	if user.Name != "bob" {
		t.Errorf("got name=%q, want %q", user.Name, "bob")
	}
	if calls != 1 {
		t.Errorf("got %d API calls, want 1 (cache should be reused)", calls)
	}
}

func TestLookupUser_NotFound(t *testing.T) {
	client := newTestClient(t, usersListHandler(t, testUsers()))
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

	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		usersListHandler(t, testUsers())(w, r)
	}))

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
	if calls != 1 {
		t.Errorf("got %d API calls, want 1 (should refetch after expired cache)", calls)
	}
}

func TestLookupUser_FileCacheWritten(t *testing.T) {
	cacheDir := t.TempDir()
	teamID := "T123"

	client := newTestClient(t, usersListHandler(t, testUsers()))
	r := NewResolver(client, teamID, cacheDir)

	// Trigger bulk load.
	_, _, err := r.LookupUser(context.Background(), "U001")
	if err != nil {
		t.Fatal(err)
	}

	// Verify file cache was written.
	cacheFile := filepath.Join(cacheDir, "users-"+teamID+".json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("file cache not written: %v", err)
	}
	var fc userFileCache
	if err := json.Unmarshal(data, &fc); err != nil {
		t.Fatalf("invalid file cache: %v", err)
	}
	if len(fc.Users) != 3 {
		t.Errorf("expected 3 users in file cache, got %d", len(fc.Users))
	}
	if fc.Users["U001"].Name != "alice" {
		t.Errorf("expected alice in file cache, got %q", fc.Users["U001"].Name)
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
