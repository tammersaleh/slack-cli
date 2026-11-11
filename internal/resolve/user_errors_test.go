package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/api"
)

func slackFailure(code string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
	}
}

// userLookupMux serves the two endpoints a name or email lookup can reach.
func userLookupMux(list, byEmail http.HandlerFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", list)
	mux.HandleFunc("/api/users.lookupByEmail", byEmail)
	return mux
}

func lookupByEmailHit(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"id": id}})
	}
}

// A clean miss is ErrNotFound and nothing else is. The directory walk's
// error used to be discarded, so a bad token or an exhausted rate limit
// during the walk came back as "no such user".
func TestResolveUser_ErrorKinds(t *testing.T) {
	loaded := func(w http.ResponseWriter, r *http.Request) { usersListHandler(t, testUsers())(w, r) }
	unreachable := func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
	}

	tests := []struct {
		name         string
		list         http.HandlerFunc
		byEmail      http.HandlerFunc
		input        string
		wantID       string
		wantNotFound bool
		wantSlack    string // Slack code the raw error must carry
	}{
		{name: "name absent from a loaded directory", list: loaded, byEmail: unreachable,
			input: "@nonexistent", wantNotFound: true},
		{name: "name, directory load fails", list: slackFailure("invalid_auth"), byEmail: unreachable,
			input: "@alice", wantSlack: "invalid_auth"},
		{name: "email absent, lookupByEmail says not found", list: loaded, byEmail: slackFailure("users_not_found"),
			input: "nobody@example.com", wantNotFound: true},
		{name: "email absent, lookupByEmail fails", list: loaded, byEmail: slackFailure("missing_scope"),
			input: "nobody@example.com", wantSlack: "missing_scope"},
		{name: "email, directory load fails, lookupByEmail hits", list: slackFailure("invalid_auth"), byEmail: lookupByEmailHit("U02MGR"),
			input: "bob@example.com", wantID: "U02MGR"},
		// users.lookupByEmail does not see every account the directory
		// holds, so its not-found proves nothing when the directory is down.
		{name: "email, directory load fails, lookupByEmail says not found", list: slackFailure("invalid_auth"), byEmail: slackFailure("users_not_found"),
			input: "bob@example.com", wantSlack: "invalid_auth"},
		{name: "email, both fail: the directory's error is the cause", list: slackFailure("invalid_auth"), byEmail: slackFailure("missing_scope"),
			input: "bob@example.com", wantSlack: "invalid_auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewResolver(newTestClient(t, userLookupMux(tt.list, tt.byEmail)), "T01ABC", t.TempDir())
			id, err := r.ResolveUser(context.Background(), tt.input)
			if tt.wantID != "" {
				if err != nil || id != tt.wantID {
					t.Fatalf("got (%q, %v), want %q", id, err, tt.wantID)
				}
				return
			}
			if got := errors.Is(err, ErrNotFound); got != tt.wantNotFound {
				t.Fatalf("errors.Is(err, ErrNotFound) = %v, want %v (err: %v)", got, tt.wantNotFound, err)
			}
			if tt.wantSlack != "" {
				if code, ok := api.SlackErrorCode(err); !ok || code != tt.wantSlack {
					t.Errorf("err = %v, want Slack's %s", err, tt.wantSlack)
				}
			}
		})
	}
}

// The directory walk gives up after 5 attempts on one page. That is a failed
// lookup naming users.list, not a missing user.
func TestResolveUser_ExhaustedDirectoryLoadIsNotErrNotFound(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	list := func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeRateLimited(w)
	}
	r := NewResolver(newTestClient(t, userLookupMux(list, slackFailure("users_not_found"))), "T01ABC", t.TempDir())

	_, err := r.ResolveUser(context.Background(), "@alice")
	var exhausted *api.RateLimitExhaustedError
	if errors.Is(err, ErrNotFound) || !errors.As(err, &exhausted) {
		t.Fatalf("err = %v, want a RateLimitExhaustedError that is not ErrNotFound", err)
	}
	if exhausted.Endpoint != "users.list" {
		t.Errorf("endpoint = %q, want users.list", exhausted.Endpoint)
	}
	if got := calls.Load(); got != 5 {
		t.Errorf("users.list requests = %d, want 5 attempts", got)
	}
}
