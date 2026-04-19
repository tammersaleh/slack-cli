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

	// Try display name / username lookup from cache.
	name := input
	if len(name) > 0 && name[0] == '@' {
		name = name[1:]
	}
	if err := r.ensureUserCache(ctx); err == nil {
		r.mu.RLock()
		id, ok := r.usersByName[strings.ToLower(name)]
		r.mu.RUnlock()
		if ok {
			return id, nil
		}
	}

	return "", fmt.Errorf("cannot resolve user %q: no match by ID, email, or name", input)
}

// LookupUser returns the full profile for a user ID, preferring in-memory
// or file cache. On cache miss, fetches the single user via users.info and
// caches the result. Avoids bulk-loading the whole workspace just to resolve
// one ID (expensive on large orgs). Returns (user, found, error) where
// found=false means both the cache and users.info turned up empty; the API
// error itself is logged nowhere because enrichment is best-effort.
func (r *Resolver) LookupUser(ctx context.Context, id string) (slack.User, bool, error) {
	r.ensureUserCacheLazy()

	r.mu.RLock()
	user, found := r.users[id]
	r.mu.RUnlock()
	if found {
		return user, true, nil
	}

	// Cache miss - fetch a single user instead of paginating users.list.
	apiUser, err := r.client.Bot().GetUserInfoContext(ctx, id)
	if err != nil || apiUser == nil {
		return slack.User{}, false, nil
	}

	r.mu.Lock()
	r.addUserToCache(*apiUser)
	r.mu.Unlock()

	return *apiUser, true, nil
}

// addUserToCache inserts a single user into in-memory cache maps, creating
// them if necessary. Populates both the email and name indexes so a later
// ResolveUser(@name) for this user hits cache instead of triggering a bulk
// users.list load. Must be called with r.mu held.
func (r *Resolver) addUserToCache(u slack.User) {
	if r.users == nil {
		r.users = make(map[string]slack.User)
		r.usersByEmail = make(map[string]string)
		r.usersByName = make(map[string]string)
		r.usersAt = time.Now()
	}
	r.users[u.ID] = u
	if u.Profile.Email != "" {
		r.usersByEmail[strings.ToLower(u.Profile.Email)] = u.ID
	}
	// Mirror setUserMaps' name indexing so single-user inserts stay
	// symmetric with bulk-loaded entries.
	if u.Name != "" {
		key := strings.ToLower(u.Name)
		if _, exists := r.usersByName[key]; !exists {
			r.usersByName[key] = u.ID
		}
	}
	if u.Profile.DisplayName != "" {
		key := strings.ToLower(u.Profile.DisplayName)
		if _, exists := r.usersByName[key]; !exists {
			r.usersByName[key] = u.ID
		}
	}
	if u.RealName != "" {
		key := strings.ToLower(u.RealName)
		if _, exists := r.usersByName[key]; !exists {
			r.usersByName[key] = u.ID
		}
	}
}

// ensureUserCacheLazy populates the in-memory user cache from the file cache
// if one is fresh. Unlike ensureUserCache, it never calls the bulk users.list
// endpoint - callers that only need a single-ID lookup should use this plus
// an on-demand users.info fallback.
func (r *Resolver) ensureUserCacheLazy() {
	r.mu.RLock()
	if r.users != nil && time.Since(r.usersAt) < memoryCacheTTL {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	if fc, err := r.loadUserFileCache(); err == nil && fc != nil {
		r.mu.Lock()
		if r.users == nil || time.Since(r.usersAt) >= memoryCacheTTL {
			r.setUserMaps(fc.Users)
		}
		r.mu.Unlock()
	}
}

// ensureUserCache populates the user cache if it's empty or stale. Falls
// back to bulk-loading the full users.list when nothing else turns up.
// Used by ResolveUser for display-name / email lookups, which need the full
// workspace to scan by name.
func (r *Resolver) ensureUserCache(ctx context.Context) error {
	r.ensureUserCacheLazy()

	r.mu.RLock()
	fresh := r.users != nil && time.Since(r.usersAt) < memoryCacheTTL
	r.mu.RUnlock()
	if fresh {
		return nil
	}

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

// setUserMaps populates the in-memory user, email, and name index maps.
// Must be called with r.mu held.
func (r *Resolver) setUserMaps(users map[string]slack.User) {
	r.users = users
	r.usersAt = time.Now()
	r.usersByEmail = make(map[string]string, len(users))
	r.usersByName = make(map[string]string, len(users))
	for id, u := range users {
		if u.Profile.Email != "" {
			r.usersByEmail[strings.ToLower(u.Profile.Email)] = id
		}
		// Index by username (first match wins on collision).
		if u.Name != "" {
			key := strings.ToLower(u.Name)
			if _, exists := r.usersByName[key]; !exists {
				r.usersByName[key] = id
			}
		}
		// Also index by display name and real name.
		if u.Profile.DisplayName != "" {
			key := strings.ToLower(u.Profile.DisplayName)
			if _, exists := r.usersByName[key]; !exists {
				r.usersByName[key] = id
			}
		}
		if u.RealName != "" {
			key := strings.ToLower(u.RealName)
			if _, exists := r.usersByName[key]; !exists {
				r.usersByName[key] = id
			}
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
		_ = os.Remove(path) // clean up corrupted cache
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
