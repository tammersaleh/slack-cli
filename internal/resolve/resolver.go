package resolve

import (
	"sync"
	"time"

	"github.com/slack-go/slack"
)

const cacheTTL = 5 * time.Minute

// Resolver resolves human-friendly channel names and user identifiers to Slack IDs.
type Resolver struct {
	bot *slack.Client

	mu          sync.Mutex
	channels    map[string]string // name → ID
	channelsAt  time.Time
}

// NewResolver creates a Resolver using the given bot client.
func NewResolver(bot *slack.Client) *Resolver {
	return &Resolver{bot: bot}
}
