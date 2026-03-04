package resolve

import (
	"sync"
	"time"

	"github.com/tammersaleh/slack-cli/internal/api"
)

const cacheTTL = 5 * time.Minute

// Resolver resolves human-friendly channel names and user identifiers to Slack IDs.
type Resolver struct {
	client *api.Client

	mu         sync.RWMutex
	channels   map[string]string // name → ID
	channelsAt time.Time
}

// NewResolver creates a Resolver using the given API client.
func NewResolver(client *api.Client) *Resolver {
	return &Resolver{client: client}
}
