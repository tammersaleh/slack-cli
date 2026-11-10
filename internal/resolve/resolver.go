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

	mu             sync.RWMutex
	channels       map[string]string   // name -> ID
	channelsByID   map[string]string   // ID -> name
	channelsAt     time.Time
	failedChannels map[string]struct{} // IDs that failed conversations.info this session

	users        map[string]slack.User // ID -> User
	usersByEmail map[string]string     // email -> ID
	usersByName  map[string]string     // lowercase display name -> ID
	usersAt      time.Time
	failedUsers  map[string]error // IDs whose users.info lookup failed this session, and why

	// usersInfoDown and conversationsInfoDown are set once that method
	// exhausts its rate-limit retries. Single-ID lookups run in the printer's
	// emit path; without this every distinct uncached ID in an output would
	// wait out its own run of Retry-After sleeps.
	usersInfoDown         error
	conversationsInfoDown bool
}

// NewResolver creates a Resolver using the given API client. If teamID and
// cacheDir are non-empty, file-based caches persist lookups across invocations.
func NewResolver(client *api.Client, teamID, cacheDir string) *Resolver {
	return &Resolver{client: client, teamID: teamID, cacheDir: cacheDir}
}
