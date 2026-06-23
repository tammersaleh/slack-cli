package resolve

import (
	"context"
	"regexp"
)

var (
	enrichUserPattern   = regexp.MustCompile(`^[UW][A-Z0-9]+$`)
	enrichChannelPattern = regexp.MustCompile(`^[CDG][A-Z0-9]+$`)
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
// Enrichment is best-effort: missing cache entries are silently skipped. On a
// cache miss, LookupUser falls back to a single users.info and LookupChannel to
// a single conversations.info, rather than paginating users.list /
// conversations.list. Cost is O(unique unresolved IDs in the output), not
// O(workspace size).
func (r *Resolver) Enrich(ctx context.Context, m map[string]any) {
	r.ensureUserCacheLazy()

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
			if name, found := r.LookupChannel(ctx, id); found {
				m[nameField] = name
			}
		}
	}
}
