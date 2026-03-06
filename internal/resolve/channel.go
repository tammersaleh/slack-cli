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

var channelIDPattern = regexp.MustCompile(`^[C][A-Z0-9]+$`)

// channelFileCache is the on-disk format for the channel name cache.
type channelFileCache struct {
	UpdatedAt time.Time         `json:"updated_at"`
	Channels  map[string]string `json:"channels"`
}

// ResolveChannel resolves a channel name or ID to a Slack channel ID.
// Accepts channel IDs (passthrough), names with or without `#` prefix.
func (r *Resolver) ResolveChannel(ctx context.Context, input string) (string, error) {
	if channelIDPattern.MatchString(input) {
		return input, nil
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
			r.channels = fc.Channels
			r.channelsAt = time.Now()
			r.mu.Unlock()
			return id, nil
		}
	}

	// 3. Paginate page-by-page with early exit.
	return r.resolveByPagination(ctx, name)
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
	r.channels = channels
	r.channelsAt = time.Now()

	// Write file cache only on full pagination (found=="" means we exhausted all pages).
	if found == "" {
		r.saveFileCache(channels)
		return "", fmt.Errorf("channel %q not found", name)
	}

	return found, nil
}

// loadFileCache reads the channel file cache if it exists and is fresh.
func (r *Resolver) loadFileCache() (*channelFileCache, error) {
	path := r.channelCachePath()
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var fc channelFileCache
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, err
	}

	if time.Since(fc.UpdatedAt) > fileCacheTTL {
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
