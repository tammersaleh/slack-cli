package resolve

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var userIDPattern = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

// ResolveUser resolves a user identifier to a Slack user ID.
// Accepts user IDs (passthrough) and email addresses.
func (r *Resolver) ResolveUser(ctx context.Context, input string) (string, error) {
	if userIDPattern.MatchString(input) {
		return input, nil
	}

	if isEmail(input) {
		user, err := r.client.Bot().GetUserByEmailContext(ctx, input)
		if err != nil {
			return "", fmt.Errorf("resolving user %q: %w", input, err)
		}
		return user.ID, nil
	}

	return "", fmt.Errorf("cannot resolve user %q: expected a user ID (U...) or email address", input)
}

func isEmail(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && strings.Contains(s[at:], ".")
}
