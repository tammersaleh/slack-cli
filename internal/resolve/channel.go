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

	// 2. File cache.
	if fc, err := r.loadFileCache(); err == nil && fc != nil {
		if id, ok := fc.Channels[name]; ok {
			r.mu.Lock()
			r.setChannelMaps(fc.Channels)
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

// resolveByPagination fetches channels page-by-page, stopping as soon as the
// target name is found. If all pages are exhausted, the complete map is written
// to the file cache.
func (r *Resolver) resolveByPagination(ctx context.Context, name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Re-check memory cache after acquiring write lock.
	if r.channels != nil && time.Since(r.channelsAt) < memoryCacheTTL {
		if id, ok := r.channels[name]; ok {
			return id, nil
		}
	}

	channels := make(map[string]string)
	var found string
	bot := r.client.Bot()

	err := api.PaginateEach(ctx, "conversations.list", func(cursor string) ([]slack.Channel, string, error) {
		return bot.GetConversationsContext(ctx, &slack.GetConversationsParameters{
			Types:           []string{"public_channel", "private_channel"},
			Limit:           200,
			ExcludeArchived: true,
			Cursor:          cursor,
		})
	}, func(items []slack.Channel) bool {
		for _, ch := range items {
			if _, exists := channels[ch.Name]; !exists {
				channels[ch.Name] = ch.ID
			}
			if ch.Name == name {
				found = ch.ID
			}
		}
		return found != ""
	})
	if err != nil {
		return "", err
	}

	// Update in-memory cache with whatever we fetched.
	r.setChannelMaps(channels)

	// Write file cache only on full pagination (found=="" means we exhausted all pages).
	if found == "" {
		r.saveFileCache(channels)
		return "", fmt.Errorf("channel %q not found", name)
	}

	return found, nil
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
