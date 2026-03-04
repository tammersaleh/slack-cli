package resolve

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/slack-go/slack"
)

var channelIDPattern = regexp.MustCompile(`^[C][A-Z0-9]+$`)

// ResolveChannel resolves a channel name or ID to a Slack channel ID.
// Accepts channel IDs (passthrough), names with or without `#` prefix.
func (r *Resolver) ResolveChannel(ctx context.Context, input string) (string, error) {
	if channelIDPattern.MatchString(input) {
		return input, nil
	}

	// Strip # prefix.
	name := input
	if len(name) > 0 && name[0] == '#' {
		name = name[1:]
	}

	channels, err := r.loadChannels(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving channel %q: %w", input, err)
	}

	if id, ok := channels[name]; ok {
		return id, nil
	}

	return "", fmt.Errorf("channel %q not found", input)
}

// loadChannels returns the cached channel name→ID map, refreshing if expired.
func (r *Resolver) loadChannels(ctx context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.channels != nil && time.Since(r.channelsAt) < cacheTTL {
		return r.channels, nil
	}

	channels := map[string]string{}
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		params := &slack.GetConversationsParameters{
			Types:           []string{"public_channel", "private_channel"},
			Limit:           200,
			ExcludeArchived: true,
			Cursor:          cursor,
		}
		chans, next, err := r.bot.GetConversationsContext(ctx, params)
		if err != nil {
			return nil, err
		}

		for _, ch := range chans {
			// First match wins - don't overwrite if name already seen.
			if _, exists := channels[ch.Name]; !exists {
				channels[ch.Name] = ch.ID
			}
		}

		if next == "" {
			break
		}
		cursor = next
	}

	r.channels = channels
	r.channelsAt = time.Now()
	return channels, nil
}
