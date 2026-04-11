package resolve

import (
	"context"
	"regexp"
)

var (
	enrichUserPattern   = regexp.MustCompile(`^[UW][A-Z0-9]+$`)
	enrichChannelPattern = regexp.MustCompile(`^C[A-Z0-9]+$`)
)

// enrichUserFields are top-level map keys that contain user IDs.
var enrichUserFields = map[string]string{
	"user":       "user_name",
	"user_id":    "user_name",
	"from_user":  "from_user_name",
	"created_by": "created_by_name",
}

// enrichChannelFields are top-level map keys that contain channel IDs.
var enrichChannelFields = map[string]string{
	"channel":    "channel_name",
	"channel_id": "channel_name",
}

// Enrich adds resolved names to a map for any recognized user/channel ID fields.
// Enrichment is best-effort: missing cache entries are silently skipped.
func (r *Resolver) Enrich(ctx context.Context, m map[string]any) {
	// Warm caches if not already loaded. Errors are ignored -
	// enrichment is best-effort.
	_ = r.ensureUserCache(ctx)

	for field, nameField := range enrichUserFields {
		if _, already := m[nameField]; already {
			continue
		}
		if id, ok := m[field].(string); ok && enrichUserPattern.MatchString(id) {
			if user, found, _ := r.LookupUser(ctx, id); found {
				name := user.Profile.DisplayName
				if name == "" {
					name = user.RealName
				}
				if name == "" {
					name = user.Name
				}
				m[nameField] = name
			}
		}
	}

	for field, nameField := range enrichChannelFields {
		if _, already := m[nameField]; already {
			continue
		}
		if id, ok := m[field].(string); ok && enrichChannelPattern.MatchString(id) {
			if name, found := r.LookupChannelName(id); found {
				m[nameField] = name
			}
		}
	}
}
