package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
// A name matches a handle, display name, or real name, case-insensitively and
// exactly. userIDByName has the ranking; a name it cannot rank to one user is
// an *AmbiguousUserError.
//
// The error is ErrBadURL for an unusable Slack URL, ErrNotFound for a clean
// miss, or the raw error of the call that failed:
//
//   - A name is a clean miss only when the directory loaded and does not
//     hold it. A failed load is returned as it failed.
//   - An email is a clean miss when the directory loaded without it and
//     users.lookupByEmail answered users_not_found. Any other answer from
//     that call is returned raw.
//   - An email whose directory load failed still tries users.lookupByEmail,
//     and a hit there answers. Anything else returns the load's error: the
//     endpoint does not see every account the directory holds, so its
//     users_not_found proves nothing on its own.
//
// Callers tell the kinds apart with errors.Is and errors.As.
func (r *Resolver) ResolveUser(ctx context.Context, input string) (string, error) {
	if id, handled, err := userIDFromURL(input); handled {
		return id, err
	}

	if userIDPattern.MatchString(input) {
		return input, nil
	}

	loadErr := r.ensureUserCache(ctx)

	if isEmail(input) {
		if loadErr == nil {
			r.mu.RLock()
			id, ok := r.usersByEmail[strings.ToLower(input)]
			r.mu.RUnlock()
			if ok {
				return id, nil
			}
		}

		user, err := r.client.Bot().GetUserByEmailContext(ctx, input)
		switch {
		case err == nil:
			return user.ID, nil
		case loadErr != nil:
			return "", fmt.Errorf("loading the user directory to resolve %q: %w", input, loadErr)
		}
		if code, ok := api.SlackErrorCode(err); ok && code == "users_not_found" {
			return "", userNotFound(input)
		}
		return "", fmt.Errorf("resolving user %q: %w", input, err)
	}

	if loadErr != nil {
		return "", fmt.Errorf("loading the user directory to resolve %q: %w", input, loadErr)
	}
	return r.userIDByName(input)
}

// AmbiguousUserError is a name that two or more users hold with nothing to
// rank one above the other. Candidates are sorted by ID. It is a verdict on
// the input, like ErrNotFound: the directory loaded and the name does not
// pick one user.
type AmbiguousUserError struct {
	Input      string
	Candidates []slack.User
}

func (e *AmbiguousUserError) Error() string {
	ids := make([]string, len(e.Candidates))
	for i, u := range e.Candidates {
		ids[i] = u.ID
	}
	return fmt.Sprintf("user %q: name matches %d users (%s)", e.Input, len(ids), strings.Join(ids, ", "))
}

// userIDByName answers a name lookup from a loaded directory. Its error is a
// verdict on the name, never on the lookup.
//
// The match is case-insensitive and exact. A handle match outranks a display
// name or real name match, whatever the accounts' state: a handle is the one
// name a caller can use to ask for a specific account, a deactivated one
// included. Within the winning tier an active account outranks a deactivated
// one, so a deactivated account answers only when no active account holds the
// name there. Whatever is still tied is an AmbiguousUserError; nothing picks
// between two accounts of equal rank.
//
// The ranking reads the user records at lookup, so the answer does not depend
// on the order users entered the cache.
func (r *Resolver) userIDByName(input string) (string, error) {
	key := strings.ToLower(strings.TrimPrefix(input, "@"))
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, tier := range [][]string{r.usersByHandle[key], r.usersByLabel[key]} {
		candidates := r.activeFirst(tier)
		switch len(candidates) {
		case 0:
			continue
		case 1:
			return candidates[0].ID, nil
		}
		return "", &AmbiguousUserError{Input: input, Candidates: candidates}
	}
	return "", userNotFound(input)
}

// activeFirst returns the users behind ids, sorted by ID, without the
// deactivated ones when at least one is active. Must be called with r.mu held.
func (r *Resolver) activeFirst(ids []string) []slack.User {
	var active, deleted []slack.User
	for _, id := range ids {
		u, ok := r.users[id]
		switch {
		case !ok:
		case u.Deleted:
			deleted = append(deleted, u)
		default:
			active = append(active, u)
		}
	}
	out := active
	if len(out) == 0 {
		out = deleted
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func userNotFound(input string) error {
	return fmt.Errorf("user %q: no match by ID, email, or name: %w", input, ErrNotFound)
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

// addUserToCache inserts a single user into the in-memory cache maps,
// creating them if necessary, through the same indexing a bulk load uses. A
// cache created here is partial (usersComplete stays false), so a later name
// lookup still loads the directory before it answers. Must be called with
// r.mu held.
func (r *Resolver) addUserToCache(u slack.User) {
	if r.users == nil {
		r.users = make(map[string]slack.User)
		r.usersByEmail = make(map[string]string)
		r.usersByHandle = make(map[string][]string)
		r.usersByLabel = make(map[string][]string)
		r.usersAt = time.Now()
		r.usersComplete = false
	}
	if old, ok := r.users[u.ID]; ok {
		r.unindexUser(old)
	}
	r.users[u.ID] = u
	r.indexUser(u)
}

// nameKeys returns the lowercased keys a user is indexed under: the handle,
// and the distinct display name and real name.
func nameKeys(u slack.User) (handle string, labels []string) {
	handle = strings.ToLower(u.Name)
	display, realName := strings.ToLower(u.Profile.DisplayName), strings.ToLower(u.RealName)
	if display != "" {
		labels = append(labels, display)
	}
	if realName != "" && realName != display {
		labels = append(labels, realName)
	}
	return handle, labels
}

// indexUser adds a user already in r.users to the email and name indexes.
// Must be called with r.mu held.
func (r *Resolver) indexUser(u slack.User) {
	if email := strings.ToLower(u.Profile.Email); email != "" {
		// Two accounts sharing an email has not been seen, but the winner
		// must not depend on map order if it happens: active first, then
		// the lower ID.
		if cur, ok := r.users[r.usersByEmail[email]]; !ok || outranks(u, cur) {
			r.usersByEmail[email] = u.ID
		}
	}
	handle, labels := nameKeys(u)
	if handle != "" {
		r.usersByHandle[handle] = append(r.usersByHandle[handle], u.ID)
	}
	for _, key := range labels {
		r.usersByLabel[key] = append(r.usersByLabel[key], u.ID)
	}
}

// outranks reports whether a beats b for a key only one user can hold.
func outranks(a, b slack.User) bool {
	if a.Deleted != b.Deleted {
		return !a.Deleted
	}
	return a.ID < b.ID
}

// unindexUser removes a user's name index entries ahead of a re-insert whose
// names may have changed. A stale email entry is harmless and stays. Must be
// called with r.mu held.
func (r *Resolver) unindexUser(u slack.User) {
	drop := func(index map[string][]string, key string) {
		ids := index[key]
		for i, id := range ids {
			if id == u.ID {
				index[key] = append(ids[:i:i], ids[i+1:]...)
				break
			}
		}
		if len(index[key]) == 0 {
			delete(index, key)
		}
	}
	handle, labels := nameKeys(u)
	if handle != "" {
		drop(r.usersByHandle, handle)
	}
	for _, key := range labels {
		drop(r.usersByLabel, key)
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

// ensureUserCache makes the user cache a whole, fresh directory: the file
// cache when one is fresh, otherwise a full users.list walk. A cache holding
// only users fetched one at a time does not count. Used by ResolveUser for
// name and email lookups, which need every account to rank a name.
func (r *Resolver) ensureUserCache(ctx context.Context) error {
	r.ensureUserCacheLazy()

	r.mu.RLock()
	fresh := r.usersComplete && time.Since(r.usersAt) < memoryCacheTTL
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
	if r.usersComplete && time.Since(r.usersAt) < memoryCacheTTL {
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

// setUserMaps replaces the in-memory user cache with a whole directory and
// rebuilds the email and name indexes from it. Must be called with r.mu held.
func (r *Resolver) setUserMaps(users map[string]slack.User) {
	r.users = users
	r.usersAt = time.Now()
	r.usersComplete = true
	r.usersByEmail = make(map[string]string, len(users))
	r.usersByHandle = make(map[string][]string, len(users))
	r.usersByLabel = make(map[string][]string, len(users))
	for _, u := range users {
		r.indexUser(u)
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

// isEmail reports whether s is shaped like an email. An address holds no
// whitespace: the search rewrite offers multi-word spans ("bob@example.com
// deploy now"), and treating one as an email would spend a
// users.lookupByEmail call on it and return whatever Slack makes of it.
func isEmail(s string) bool {
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	at := strings.Index(s, "@")
	return at > 0 && strings.Contains(s[at:], ".")
}
