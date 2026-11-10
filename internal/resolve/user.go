package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
)

var userIDPattern = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

// IsUserID reports whether s is shaped like a Slack user ID (U.../W...).
// Used by callers that need to decide whether a custom-field value is a
// user reference worth resolving to a name.
func IsUserID(s string) bool { return userIDPattern.MatchString(s) }

// userFileCache is the on-disk format for the user cache.
type userFileCache struct {
	UpdatedAt time.Time              `json:"updated_at"`
	Users     map[string]slack.User  `json:"users"`
}

// ResolveUser resolves a user identifier to a Slack user ID: a profile URL or
// ID (answered locally), an email, or a name with or without "@".
//
// Email and name both read the user cache first, and a cache that is missing
// or stale is rebuilt by walking the whole users.list directory before the
// lookup answers. An email the cache does not hold then falls back to
// users.lookupByEmail; a name has no fallback.
//
// A failed directory load is not reported: the email path moves on to
// users.lookupByEmail and the name path returns no-match. See
// todo/resolver-errors-are-reported-as-channel-not-found.md.
func (r *Resolver) ResolveUser(ctx context.Context, input string) (string, error) {
	if id, handled, err := userIDFromURL(input); handled {
		return id, err
	}

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

// LookupUser returns the full profile for a user ID, best-effort: the caller
// wants a name for a row and can do without one. It prefers the in-memory or
// file cache and, on a miss, fetches the single user via users.info through
// the shared rate-limit retry, so the request shows in --trace. It never
// bulk-loads users.list just to resolve one ID.
//
// found=false always comes with the error that explains it. Failures are
// memoized for the process, so a wide result set with an unresolvable ID costs
// one lookup, not one per row.
//
// The lookup runs inside the printer's emit path, so its retry is bounded: once
// users.info exhausts its rate-limit retries, every later uncached LookupUser
// in this process returns that error without a request. Otherwise each distinct
// ID in a long output would wait out its own run of Retry-After sleeps.
//
// A command whose answer is the user must call FetchUser instead, which is
// never refused by an earlier failure.
func (r *Resolver) LookupUser(ctx context.Context, id string) (slack.User, bool, error) {
	if user, found := r.cachedUser(id); found {
		return user, true, nil
	}

	r.mu.RLock()
	failed := r.failedUsers[id]
	down := r.usersInfoDown
	r.mu.RUnlock()
	if failed != nil {
		return slack.User{}, false, failed
	}
	if down != nil {
		return slack.User{}, false, down
	}

	user, err := r.FetchUser(ctx, id)
	return user, err == nil, err
}

// FetchUser returns the full profile for a user ID the caller cannot do
// without. It reads the same cache as LookupUser and makes the same traced,
// retrying users.info request on a miss, but it always makes that request:
// neither a memoized failure for this ID nor the stop an exhausted enrichment
// lookup set can answer for it. A transient error on a best-effort lookup must
// not become this command's verdict on the user.
func (r *Resolver) FetchUser(ctx context.Context, id string) (slack.User, error) {
	if user, found := r.cachedUser(id); found {
		return user, nil
	}

	var apiUser *slack.User
	err := api.Call(ctx, "users.info", func() error {
		var err error
		apiUser, err = r.client.Bot().GetUserInfoContext(ctx, id)
		return err
	})
	if err == nil && apiUser == nil {
		err = slack.SlackErrorResponse{Err: "user_not_found"}
	}
	if err != nil {
		r.markUserFailed(ctx, id, err)
		return slack.User{}, err
	}

	r.mu.Lock()
	r.addUserToCache(*apiUser)
	r.mu.Unlock()

	return *apiUser, nil
}

// cachedUser reads a user from the in-memory cache, loading a fresh file cache
// into it first.
func (r *Resolver) cachedUser(id string) (slack.User, bool) {
	r.ensureUserCacheLazy()
	r.mu.RLock()
	defer r.mu.RUnlock()
	user, found := r.users[id]
	return user, found
}

// markUserFailed memoizes a failed users.info lookup. An exhausted rate limit
// stops every later lookup, not just this ID's. A dead context is recorded
// nowhere: it says nothing about the user, and api.Call already refuses to
// spend a request on one.
func (r *Resolver) markUserFailed(ctx context.Context, id string, err error) {
	if ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var exhausted *api.RateLimitExhaustedError
	if errors.As(err, &exhausted) {
		r.usersInfoDown = err
		return
	}
	if r.failedUsers == nil {
		r.failedUsers = make(map[string]error)
	}
	r.failedUsers[id] = err
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

// bulkUserPageSize is the users.list page size for the directory load, the
// largest Slack recommends and what slack-go's own walk used.
const bulkUserPageSize = 200

// bulkLoadUsers fetches all users via users.list and populates both
// in-memory and file caches. Nothing is written unless the whole walk
// succeeds, so a failed or cancelled load leaves the file cache as it was.
func (r *Resolver) bulkLoadUsers(ctx context.Context) error {
	r.mu.Lock()
	if r.users != nil && time.Since(r.usersAt) < memoryCacheTTL {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	// slack-go's GetUsersContext pages on its own and sleeps through every
	// rate limit with no trace event and no attempt cap. The shared pager
	// emits a page event per request, a retry event per wait, and gives up
	// after maxAttempts on one page. slack-go's paginator carries its cursor
	// in the value, so each attempt builds a fresh one with every option
	// reapplied; a shared one would restart at page 1 on a retry.
	bot := r.client.Bot()
	users, err := api.Paginate(ctx, "users.list", 0, func(cursor string) ([]slack.User, string, error) {
		pag := bot.GetUsersPaginated(
			slack.GetUsersOptionLimit(bulkUserPageSize),
			slack.GetUsersOptionCursor(cursor),
		)
		pag, err := pag.Next(ctx)
		if err != nil {
			if pag.Done(err) {
				return nil, "", nil
			}
			return nil, "", err
		}
		return pag.Users, pag.Cursor, nil
	})
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

	// Stat-first: fast-fail on a stale cache before paying the 16MB+
	// ReadFile + Unmarshal. Enrich calls this once per output row, so a
	// stale file on a long thread list would re-parse the whole file
	// dozens of times.
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if time.Since(info.ModTime()) > fileCacheTTL() {
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
