package resolve

import (
	"sync"
	"time"

	"github.com/tammersaleh/slack-cli/internal/api"
)

const (
	memoryCacheTTL = 5 * time.Minute
	fileCacheTTL   = 1 * time.Hour
)

// Resolver resolves human-friendly channel names and user identifiers to Slack IDs.
type Resolver struct {
	client   *api.Client
	teamID   string
	cacheDir string

	mu         sync.RWMutex
	channels   map[string]string // name -> ID
	channelsAt time.Time
}

// NewResolver creates a Resolver using the given API client. If teamID and
// cacheDir are non-empty, a file-based channel cache is used to persist
// lookups across invocations.
func NewResolver(client *api.Client, teamID, cacheDir string) *Resolver {
	return &Resolver{client: client, teamID: teamID, cacheDir: cacheDir}
}
