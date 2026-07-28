package resolve

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// justInsideTTL returns a timestamp the memory cache still counts as fresh.
// Kept a whole second inside the boundary so a slow test machine cannot drift
// across it.
func justInsideTTL() time.Time {
	return time.Now().Add(-(memoryCacheTTL - time.Second))
}

// wellOutsideTTL returns a timestamp the memory cache counts as stale.
func wellOutsideTTL() time.Time {
	return time.Now().Add(-2 * memoryCacheTTL)
}

// failingHandler fails the test if any request reaches it. Use it to assert a
// code path is served entirely from cache - a request count of zero is the only
// observable that distinguishes "answered from memory" from "refetched and got
// the same answer".
func failingHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API request to %s - the fresh memory cache should have answered", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func TestResolveChannel_FreshMemoryCacheCostsNoRequest(t *testing.T) {
	r := NewResolver(newTestClient(t, failingHandler(t)), "", "")
	// Seed through setChannelMaps, not by assigning r.channels directly - the
	// forward and reverse maps are always populated together, and a resolver
	// holding one without the other is a state production cannot reach.
	r.setChannelMaps(map[string]string{"eng": "C01ABC"})
	r.channelsAt = justInsideTTL()

	id, err := r.ResolveChannel(context.Background(), "eng")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C01ABC" {
		t.Errorf("got %q, want C01ABC", id)
	}
}

// The same lookup with a stale in-memory map must go back to Slack. Without
// this, a test asserting only the resolved ID passes whether or not the TTL is
// honored, because the mock returns the same channel either way.
func TestResolveChannel_StaleMemoryCacheRefetches(t *testing.T) {
	rm := newResolverMux(
		func(w http.ResponseWriter, _ *http.Request) {
			writeChannelPage(w, "", []map[string]any{channelRow("C01ABC", "eng")})
		},
		emptyChannelsHandler,
	)
	r := NewResolver(newTestClient(t, rm.handler), "", "")
	r.setChannelMaps(map[string]string{"eng": "C01ABC"})
	r.channelsAt = wellOutsideTTL()

	id, err := r.ResolveChannel(context.Background(), "eng")
	if err != nil {
		t.Fatal(err)
	}
	if id != "C01ABC" {
		t.Errorf("got %q, want C01ABC", id)
	}
	if rm.memberCalls == 0 {
		t.Error("stale memory cache was served without refetching")
	}
}

// ResolveUser's name path calls ensureUserCache, which must not bulk-load
// users.list when the in-memory directory is still fresh. users.list is the
// most expensive call this package makes - a full directory measured 72
// requests - so a broken freshness check is costly, not just redundant.
func TestResolveUser_FreshMemoryCacheSkipsBulkLoad(t *testing.T) {
	r := NewResolver(newTestClient(t, failingHandler(t)), "", "")
	r.setUserMaps(map[string]slack.User{
		"U01XYZ": {ID: "U01XYZ", Name: "alice", RealName: "Alice Adams"},
	})
	r.usersAt = justInsideTTL()

	id, err := r.ResolveUser(context.Background(), "@alice")
	if err != nil {
		t.Fatal(err)
	}
	if id != "U01XYZ" {
		t.Errorf("got %q, want U01XYZ", id)
	}
}

func TestResolveUser_StaleMemoryCacheBulkLoads(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{"id": "U01XYZ", "name": "alice", "real_name": "Alice Adams"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	r := NewResolver(newTestClient(t, mux), "", "")
	r.setUserMaps(map[string]slack.User{
		"U01XYZ": {ID: "U01XYZ", Name: "alice"},
	})
	r.usersAt = wellOutsideTTL()

	if _, err := r.ResolveUser(context.Background(), "@alice"); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("stale user cache was served without bulk-loading users.list")
	}
}

// An empty input must produce an error, not an index-out-of-range panic on the
// '#'/'@' prefix strip. Nothing else in the suite passes the empty string, so
// the length guards were unverified.
func TestResolveEmptyInput(t *testing.T) {
	r := NewResolver(newTestClient(t, http.HandlerFunc(emptyChannelsHandler)), "", "")

	t.Run("channel", func(t *testing.T) {
		if _, err := r.ResolveChannel(context.Background(), ""); err == nil {
			t.Error("expected an error for an empty channel name")
		}
	})

	t.Run("user", func(t *testing.T) {
		if _, err := r.ResolveUser(context.Background(), ""); err == nil {
			t.Error("expected an error for an empty user name")
		}
	})
}

// isEmail requires the @ to be preceded by something. A leading-@ handle like
// "@example.com" is a name, not an address - misclassifying it would send
// ResolveUser down the users.lookupByEmail path instead of the name index.
func TestIsEmail(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"alice@example.com", true},
		{"@example.com", false},
		{"alice@example", false},
		{"alice", false},
		{"", false},
		{"@alice", false},
	}
	for _, tt := range tests {
		if got := isEmail(tt.in); got != tt.want {
			t.Errorf("isEmail(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// setUserMaps builds three independent indexes. A user carrying only one of
// username / display name / real name must still be findable by it, and the
// email index must not depend on any of them.
func TestSetUserMaps_IndexesEachFieldIndependently(t *testing.T) {
	tests := []struct {
		name   string
		user   slack.User
		lookup string
	}{
		{
			name:   "username only",
			user:   slack.User{ID: "U01XYZ", Name: "alice"},
			lookup: "alice",
		},
		{
			name:   "display name only",
			user:   slack.User{ID: "U02MGR", Profile: slack.UserProfile{DisplayName: "Bob"}},
			lookup: "bob",
		},
		{
			name:   "real name only",
			user:   slack.User{ID: "U03CAR", RealName: "Carol Chen"},
			lookup: "carol chen",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewResolver(nil, "", "")
			r.setUserMaps(map[string]slack.User{tt.user.ID: tt.user})
			if got := r.usersByName[tt.lookup]; got != tt.user.ID {
				t.Errorf("usersByName[%q] = %q, want %q", tt.lookup, got, tt.user.ID)
			}
		})
	}

	t.Run("email indexed without any name field", func(t *testing.T) {
		r := NewResolver(nil, "", "")
		u := slack.User{ID: "U01XYZ", Profile: slack.UserProfile{Email: "Alice@Example.com"}}
		r.setUserMaps(map[string]slack.User{u.ID: u})
		if got := r.usersByEmail["alice@example.com"]; got != u.ID {
			t.Errorf("usersByEmail lowercased lookup = %q, want %q", got, u.ID)
		}
	})
}

// addUserToCache is the single-user insert path, used when LookupUser falls back
// to users.info. Its indexing must match setUserMaps' bulk indexing, or a name
// resolved after a single-ID fetch behaves differently from the same name after
// a bulk load. The duplicated field guards are why this needs its own test -
// exercising setUserMaps says nothing about this function.
func TestAddUserToCache_MirrorsBulkIndexing(t *testing.T) {
	tests := []struct {
		name      string
		user      slack.User
		byName    string
		wantEmail string
	}{
		{
			name:   "username only",
			user:   slack.User{ID: "U01XYZ", Name: "alice"},
			byName: "alice",
		},
		{
			name:   "display name only",
			user:   slack.User{ID: "U02MGR", Profile: slack.UserProfile{DisplayName: "Bob"}},
			byName: "bob",
		},
		{
			name:   "real name only",
			user:   slack.User{ID: "U03CAR", RealName: "Carol Chen"},
			byName: "carol chen",
		},
		{
			name:      "email only",
			user:      slack.User{ID: "U02MGR", Profile: slack.UserProfile{Email: "Bob@Example.com"}},
			wantEmail: "bob@example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewResolver(nil, "", "")
			r.addUserToCache(tt.user)

			if tt.byName != "" {
				if got := r.usersByName[tt.byName]; got != tt.user.ID {
					t.Errorf("usersByName[%q] = %q, want %q", tt.byName, got, tt.user.ID)
				}
			}
			if tt.wantEmail != "" {
				if got := r.usersByEmail[tt.wantEmail]; got != tt.user.ID {
					t.Errorf("usersByEmail[%q] = %q, want %q", tt.wantEmail, got, tt.user.ID)
				}
			}
			if _, ok := r.users[tt.user.ID]; !ok {
				t.Errorf("user %q missing from the ID map", tt.user.ID)
			}
		})
	}
}

// Enrich names a user by display name, then real name, then username. Nothing
// covered the fallbacks, so a user with no display name - common for accounts
// that never set one - could have silently produced an empty user_name.
func TestEnrich_UserNameFallbackChain(t *testing.T) {
	tests := []struct {
		name string
		user slack.User
		want string
	}{
		{
			name: "prefers display name",
			user: slack.User{ID: "U01XYZ", Name: "alice", RealName: "Alice Adams", Profile: slack.UserProfile{DisplayName: "Ali"}},
			want: "Ali",
		},
		{
			name: "falls back to real name",
			user: slack.User{ID: "U01XYZ", Name: "alice", RealName: "Alice Adams"},
			want: "Alice Adams",
		},
		{
			name: "falls back to username",
			user: slack.User{ID: "U01XYZ", Name: "alice"},
			want: "alice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewResolver(nil, "", "")
			r.addUserToCache(tt.user)

			m := map[string]any{"user": "U01XYZ"}
			r.Enrich(context.Background(), m)

			if got := m["user_name"]; got != tt.want {
				t.Errorf("user_name = %v, want %q", got, tt.want)
			}
		})
	}
}

// fileCacheTTL honors SLACK_CACHE_TTL and ignores values it cannot use, rather
// than falling to a zero TTL that would treat every cache as expired.
func TestFileCacheTTL_EnvOverride(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset falls back to the default", env: "", want: defaultFileCacheTTL},
		{name: "valid duration is used", env: "30m", want: 30 * time.Minute},
		{name: "unparseable value falls back", env: "not-a-duration", want: defaultFileCacheTTL},
		{name: "zero falls back", env: "0s", want: defaultFileCacheTTL},
		{name: "negative falls back", env: "-5m", want: defaultFileCacheTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SLACK_CACHE_TTL", tt.env)
			if got := fileCacheTTL(); got != tt.want {
				t.Errorf("fileCacheTTL() = %v, want %v", got, tt.want)
			}
		})
	}
}
