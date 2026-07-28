package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
)

var channelIDPattern = regexp.MustCompile(`^[CDGM][A-Z0-9]+$`)

// slackIDPattern matches any Slack ID-shaped input (uppercase start,
// followed by uppercase+digits, 6+ chars total). Used to fast-fail on
// inputs that look like IDs but aren't channel IDs (users, teams, bots).
var slackIDPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{5,}$`)

// channelFileCache is the on-disk format for the channel name cache.
type channelFileCache struct {
	UpdatedAt time.Time         `json:"updated_at"`
	Channels  map[string]string `json:"channels"`
}

// ResolveChannel resolves a channel name or ID to a Slack channel ID.
// Accepts channel IDs (passthrough), names with or without `#` prefix.
func (r *Resolver) ResolveChannel(ctx context.Context, input string) (string, error) {
	if id, handled, err := channelIDFromURL(input); handled {
		return id, err
	}

	if channelIDPattern.MatchString(input) {
		return input, nil
	}

	// Fast-fail: input looks like a Slack ID but with a non-channel prefix
	// (user Uxxx, team Txxx, bot Bxxx, etc.). Paginating conversations.list
	// would exhaust every page looking for a match that can't exist.
	if slackIDPattern.MatchString(input) {
		return "", fmt.Errorf("not a channel ID (non-channel prefix %q): %s", string(input[0]), input)
	}

	name := input
	if len(name) > 0 && name[0] == '#' {
		name = name[1:]
	}

	// 1. In-memory cache.
	r.mu.RLock()
	if r.channels != nil && time.Since(r.channelsAt) < memoryCacheTTL {
		if id, ok := r.channels[name]; ok {
			r.mu.RUnlock()
			return id, nil
		}
	}
	r.mu.RUnlock()

	// 2. File cache. Its snapshot is complete but up to a day old, so it
	// extends the in-memory maps rather than replacing them - a replace would
	// evict anything a member scan resolved earlier in this process, including
	// channels created or joined since the snapshot was taken.
	if fc, err := r.loadFileCache(); err == nil && fc != nil {
		if id, ok := fc.Channels[name]; ok {
			r.mu.Lock()
			r.addChannelsToCache(fc.Channels)
			r.mu.Unlock()
			return id, nil
		}
	}

	// 3. Paginate page-by-page with early exit.
	return r.resolveByPagination(ctx, name)
}

// LookupChannel resolves a single channel ID to its name. It checks the
// in-memory cache and, on a miss, falls back to a single conversations.info
// call rather than bulk-loading the whole workspace via conversations.list
// (which is Tier-2 rate-limited and can take minutes on a large Enterprise
// Grid org). This mirrors LookupUser's single-fetch fallback. Failures are
// memoized for the process so a wide result set with an unresolvable channel
// ID doesn't re-hit conversations.info on every row. Best-effort: returns
// ("", false) on any error or an empty name (e.g. a DM).
func (r *Resolver) LookupChannel(ctx context.Context, id string) (string, bool) {
	if name, found := r.LookupChannelName(id); found {
		return name, true
	}

	r.mu.RLock()
	_, failed := r.failedChannels[id]
	r.mu.RUnlock()
	if failed {
		return "", false
	}

	info, err := r.client.Bot().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: id})
	if err != nil || info == nil || info.Name == "" {
		r.markChannelFailed(id)
		return "", false
	}

	r.mu.Lock()
	r.addChannelToCache(info.ID, info.Name)
	r.mu.Unlock()
	return info.Name, true
}

// addChannelToCache inserts a single channel into the in-memory cache maps,
// creating them if necessary. Mirrors addUserToCache: it never persists to the
// file cache, so a sparse enrich-time lookup can't be mistaken for the complete
// conversations.list snapshot that saveFileCache writes. Must be called with
// r.mu held.
func (r *Resolver) addChannelToCache(id, name string) {
	if r.channels == nil {
		r.channels = make(map[string]string)
		r.channelsByID = make(map[string]string)
		r.channelsAt = time.Now()
	}
	// First-write-wins on the forward map, matching ResolveChannel's
	// collision semantics. Channel names are unique per workspace, so this
	// only matters for the empty-name DMs we already reject.
	if _, exists := r.channels[name]; !exists {
		r.channels[name] = id
	}
	r.channelsByID[id] = name
}

// markChannelFailed records a channel ID whose conversations.info lookup failed
// so subsequent enrichment rows skip the call.
func (r *Resolver) markChannelFailed(id string) {
	r.mu.Lock()
	if r.failedChannels == nil {
		r.failedChannels = make(map[string]struct{})
	}
	r.failedChannels[id] = struct{}{}
	r.mu.Unlock()
}

// resolveChannelTypes are the channel kinds a name can refer to. DMs have no
// name to resolve and group DMs only a generated one, so both list walks ask
// for the same two kinds.
var resolveChannelTypes = []string{"public_channel", "private_channel"}

// resolveByPagination resolves a name from the API, trying the user's own
// conversations before the whole workspace.
//
// The member-scoped scan reads users.conversations: Tier 3, and only the
// conversations the authed user is in. The fallback reads conversations.list:
// Tier 2, and every channel in the workspace. Measured cold on a large
// Enterprise Grid org, resolving a name the user is a member of took 75 and 95
// conversations.list requests for two probes (76s and 259s including
// rate-limit waits) against 3 users.conversations requests each, and 6-7 to
// exhaust the whole member list. Most names callers pass are channels they are
// in, and a hit never touches the org walk.
//
// A member scan that misses or fails costs those 6-7 requests before the
// unchanged org walk. Failure is not a verdict on the name - Enterprise Grid
// returns enterprise_is_restricted for an org-level token, an older OAuth token
// can lack a scope - so any error falls through rather than aborting. A dead
// context needs no special case: the org walk checks ctx before its first
// request and returns the same error.
func (r *Resolver) resolveByPagination(ctx context.Context, name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Re-check memory cache after acquiring write lock.
	if r.channels != nil && time.Since(r.channelsAt) < memoryCacheTTL {
		if id, ok := r.channels[name]; ok {
			return id, nil
		}
	}

	bot := r.client.Bot()

	found, seen, err := scanForName(ctx, "users.conversations", name, func(cursor string) ([]slack.Channel, string, error) {
		// An empty UserID means the authenticated user.
		return bot.GetConversationsForUserContext(ctx, &slack.GetConversationsForUserParameters{
			Types:           resolveChannelTypes,
			Limit:           200,
			ExcludeArchived: true,
			Cursor:          cursor,
		})
	})
	if found != "" {
		// Memory only. The file cache is the complete conversations.list
		// snapshot; a member-scoped view must never be persisted as one.
		r.addChannelsToCache(seen)
		return found, nil
	}
	if err != nil {
		api.TracerFrom(ctx).Event("fallback", map[string]any{
			"from":   "users.conversations",
			"to":     "conversations.list",
			"reason": err.Error(),
		})
	}

	found, channels, err := scanForName(ctx, "conversations.list", name, func(cursor string) ([]slack.Channel, string, error) {
		return bot.GetConversationsContext(ctx, &slack.GetConversationsParameters{
			Types:           resolveChannelTypes,
			Limit:           200,
			ExcludeArchived: true,
			Cursor:          cursor,
		})
	})
	if err != nil {
		return "", err
	}

	// found=="" means the walk exhausted every page, so this map is the
	// complete snapshot: it replaces the cache and gets written to disk.
	if found == "" {
		r.setChannelMaps(channels)
		r.saveFileCache(channels)
		return "", fmt.Errorf("channel %q not found", name)
	}

	// An early exit saw only the pages up to the match. Extend the cache with
	// them rather than replacing it, or a hit here would evict names an
	// earlier member scan in this process already resolved.
	r.addChannelsToCache(channels)
	return found, nil
}

// scanForName walks one list endpoint page by page, stopping at the first page
// that contains name. It returns the matched ID - empty when every page was
// fetched without a match - and a name->ID map of every row it saw.
//
// First match wins on a duplicate name, for the returned ID and the map alike:
// returning the last match while caching the first made a repeat lookup of the
// same name answer differently than the lookup that populated the cache.
func scanForName(ctx context.Context, endpoint, name string, fetch api.PageFunc[slack.Channel]) (string, map[string]string, error) {
	seen := make(map[string]string)
	var found string

	err := api.PaginateEach(ctx, endpoint, fetch, func(items []slack.Channel) bool {
		for _, ch := range items {
			if _, exists := seen[ch.Name]; !exists {
				seen[ch.Name] = ch.ID
			}
			if found == "" && ch.Name == name {
				found = ch.ID
			}
		}
		return found != ""
	})
	return found, seen, err
}

// addChannelsToCache inserts every entry into the in-memory maps without
// replacing what is already there, so a partial view can extend the cache but
// never shrink it. Must be called with r.mu held.
func (r *Resolver) addChannelsToCache(channels map[string]string) {
	for name, id := range channels {
		r.addChannelToCache(id, name)
	}
}

// setChannelMaps populates forward and reverse channel maps.
// Must be called with r.mu held.
func (r *Resolver) setChannelMaps(channels map[string]string) {
	r.channels = channels
	r.channelsAt = time.Now()
	r.channelsByID = make(map[string]string, len(channels))
	for name, id := range channels {
		r.channelsByID[id] = name
	}
}

// LookupChannelName returns the cached name for a channel ID.
// Returns ("", false) if the channel is not in the cache.
func (r *Resolver) LookupChannelName(id string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name, ok := r.channelsByID[id]
	return name, ok
}

// loadFileCache reads the channel file cache if it exists and is fresh.
func (r *Resolver) loadFileCache() (*channelFileCache, error) {
	path := r.channelCachePath()
	if path == "" {
		return nil, nil
	}

	// Stat-first: skip the ReadFile + Unmarshal when the file is already
	// past the TTL. Matches loadUserFileCache for consistency.
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

	var fc channelFileCache
	if err := json.Unmarshal(data, &fc); err != nil {
		_ = os.Remove(path) // clean up corrupted cache
		return nil, err
	}

	if time.Since(fc.UpdatedAt) > fileCacheTTL() {
		return nil, nil
	}

	return &fc, nil
}

// saveFileCache writes the channel map to disk.
func (r *Resolver) saveFileCache(channels map[string]string) {
	path := r.channelCachePath()
	if path == "" {
		return
	}

	fc := channelFileCache{
		UpdatedAt: time.Now(),
		Channels:  channels,
	}
	data, err := json.Marshal(fc)
	if err != nil {
		return
	}

	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

// channelCachePath returns the file cache path, or "" if caching is disabled.
func (r *Resolver) channelCachePath() string {
	if r.teamID == "" || r.cacheDir == "" {
		return ""
	}
	return filepath.Join(r.cacheDir, "channels-"+r.teamID+".json")
}
