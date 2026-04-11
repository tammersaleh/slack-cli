package resolve

import (
	"os"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
)

const (
	memoryCacheTTL     = 5 * time.Minute
	defaultFileCacheTTL = 24 * time.Hour
)

// fileCacheTTL returns the configured file cache TTL, checking
// SLACK_CACHE_TTL first and falling back to 24h.
func fileCacheTTL() time.Duration {
	if v := os.Getenv("SLACK_CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultFileCacheTTL
}

// Resolver resolves human-friendly channel names and user identifiers to Slack IDs.
type Resolver struct {
	client   *api.Client
	teamID   string
	cacheDir string

	mu         sync.RWMutex
	channels   map[string]string // name -> ID
	channelsAt time.Time

	users        map[string]slack.User // ID -> User
	usersByEmail map[string]string     // email -> ID
	usersByName  map[string]string     // lowercase display name -> ID
	usersAt      time.Time
}

// NewResolver creates a Resolver using the given API client. If teamID and
// cacheDir are non-empty, file-based caches persist lookups across invocations.
func NewResolver(client *api.Client, teamID, cacheDir string) *Resolver {
	return &Resolver{client: client, teamID: teamID, cacheDir: cacheDir}
}
