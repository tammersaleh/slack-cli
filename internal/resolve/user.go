package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

var userIDPattern = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

// userFileCache is the on-disk format for the user cache.
type userFileCache struct {
	UpdatedAt time.Time              `json:"updated_at"`
	Users     map[string]slack.User  `json:"users"`
}

// ResolveUser resolves a user identifier to a Slack user ID.
// Accepts user IDs (passthrough) and email addresses. Email lookups
// check the user cache first, falling back to the API.
func (r *Resolver) ResolveUser(ctx context.Context, input string) (string, error) {
	if userIDPattern.MatchString(input) {
		return input, nil
	}

	if isEmail(input) {
		// Try cache-backed email lookup first.
		if err := r.ensureUserCache(ctx); err == nil {
			r.mu.RLock()
			id, ok := r.usersByEmail[strings.ToLower(input)]
			r.mu.RUnlock()
			if ok {
				return id, nil
			}
		}

		// Fall back to direct API lookup.
		user, err := r.client.Bot().GetUserByEmailContext(ctx, input)
		if err != nil {
			return "", fmt.Errorf("resolving user %q: %w", input, err)
		}
		return user.ID, nil
	}

	return "", fmt.Errorf("cannot resolve user %q: expected a user ID (U...) or email address", input)
}

// LookupUser returns the full cached profile for a user ID. It populates
// the cache on first call by bulk-loading all users. Returns (user, found, error).
func (r *Resolver) LookupUser(ctx context.Context, id string) (slack.User, bool, error) {
	if err := r.ensureUserCache(ctx); err != nil {
		return slack.User{}, false, err
	}

	r.mu.RLock()
	user, found := r.users[id]
	r.mu.RUnlock()
	return user, found, nil
}

// ensureUserCache populates the user cache if it's empty or stale.
func (r *Resolver) ensureUserCache(ctx context.Context) error {
	r.mu.RLock()
	if r.users != nil && time.Since(r.usersAt) < memoryCacheTTL {
		r.mu.RUnlock()
		return nil
	}
	r.mu.RUnlock()

	// Try file cache.
	if fc, err := r.loadUserFileCache(); err == nil && fc != nil {
		r.mu.Lock()
		if r.users == nil || time.Since(r.usersAt) >= memoryCacheTTL {
			r.setUserMaps(fc.Users)
		}
		r.mu.Unlock()
		return nil
	}

	// Bulk-load from API.
	return r.bulkLoadUsers(ctx)
}

// bulkLoadUsers fetches all users via users.list and populates both
// in-memory and file caches.
func (r *Resolver) bulkLoadUsers(ctx context.Context) error {
	r.mu.Lock()
	if r.users != nil && time.Since(r.usersAt) < memoryCacheTTL {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	users, err := r.client.Bot().GetUsersContext(ctx)
	if err != nil {
		return err
	}

	userMap := make(map[string]slack.User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}

	r.mu.Lock()
	r.setUserMaps(userMap)
	r.saveUserFileCache(userMap)
	r.mu.Unlock()
	return nil
}

// setUserMaps populates the in-memory user and email index maps.
// Must be called with r.mu held.
func (r *Resolver) setUserMaps(users map[string]slack.User) {
	r.users = users
	r.usersAt = time.Now()
	r.usersByEmail = make(map[string]string, len(users))
	for id, u := range users {
		if u.Profile.Email != "" {
			r.usersByEmail[strings.ToLower(u.Profile.Email)] = id
		}
	}
}

// loadUserFileCache reads the user file cache if it exists and is fresh.
func (r *Resolver) loadUserFileCache() (*userFileCache, error) {
	path := r.userCachePath()
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var fc userFileCache
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, err
	}

	if time.Since(fc.UpdatedAt) > fileCacheTTL() {
		return nil, nil
	}

	return &fc, nil
}

// saveUserFileCache writes the user map to disk.
func (r *Resolver) saveUserFileCache(users map[string]slack.User) {
	path := r.userCachePath()
	if path == "" {
		return
	}

	fc := userFileCache{
		UpdatedAt: time.Now(),
		Users:     users,
	}
	data, err := json.Marshal(fc)
	if err != nil {
		return
	}

	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

// userCachePath returns the file cache path, or "" if caching is disabled.
func (r *Resolver) userCachePath() string {
	if r.teamID == "" || r.cacheDir == "" {
		return ""
	}
	return filepath.Join(r.cacheDir, "users-"+r.teamID+".json")
}

func isEmail(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && strings.Contains(s[at:], ".")
}
