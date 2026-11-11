package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// mapOrderRuns is how many fresh resolvers each ranking case builds. Go
// randomizes map iteration per range, so an index that let insertion order
// pick the winner fails some of these runs.
const mapOrderRuns = 50

func named(id, handle, display, realName string, deleted bool) slack.User {
	return slack.User{ID: id, Name: handle, RealName: realName, Deleted: deleted, Profile: slack.UserProfile{DisplayName: display}}
}

func userMap(users ...slack.User) map[string]slack.User {
	m := make(map[string]slack.User, len(users))
	for _, u := range users {
		m[u.ID] = u
	}
	return m
}

// nameRankingCases is the whole ranking rule. Every case gives its candidates
// a different outcome, so no case can pass by picking any one of them.
var nameRankingCases = []struct {
	name          string
	users         []slack.User
	lookup        string
	wantID        string
	wantAmbiguous []string // candidate IDs, in order
}{
	{
		name: "a handle beats another account's display name",
		users: []slack.User{
			named("U01", "other", "sam", "Other Person", false),
			named("U02", "sam", "Samantha", "Samantha Lee", false),
		},
		lookup: "sam", wantID: "U02",
	},
	{
		name: "a handle beats another account's real name",
		users: []slack.User{
			named("U01", "other", "", "sam", false),
			named("U02", "sam", "", "Samantha Lee", false),
		},
		lookup: "sam", wantID: "U02",
	},
	{
		name: "a deactivated account's handle still names it",
		users: []slack.User{
			named("U01", "aadams", "Alice Adams", "Alice Adams", false),
			named("U02", "aadams-old", "Alice Adams", "", true),
		},
		lookup: "@aadams-old", wantID: "U02",
	},
	{
		name: "a deactivated handle beats an active display name",
		users: []slack.User{
			named("U01", "other", "sam", "Other Person", false),
			named("U02", "sam", "", "", true),
		},
		lookup: "sam", wantID: "U02",
	},
	{
		name: "an active account beats a deactivated one on a display name",
		users: []slack.User{
			named("U01", "aadams-old", "Alice Adams", "", true),
			named("U02", "aadams", "Alice Adams", "Alice Adams", false),
		},
		lookup: "@Alice Adams", wantID: "U02",
	},
	{
		name: "an active account beats a deactivated one whatever the ID order",
		users: []slack.User{
			named("U01", "aadams", "Alice Adams", "Alice Adams", false),
			named("U02", "aadams-old", "Alice Adams", "", true),
		},
		lookup: "@Alice Adams", wantID: "U01",
	},
	{
		name: "a deactivated account answers when nothing active holds the name",
		users: []slack.User{
			named("U01", "bbrown-old", "Bob Brown", "", true),
			named("U02", "cchen", "Carol Chen", "Carol Chen", false),
		},
		lookup: "Bob Brown", wantID: "U01",
	},
	{
		name: "two active accounts sharing a display name tie",
		users: []slack.User{
			named("U02", "aadams2", "Alice Adams", "Alice B Adams", false),
			named("U01", "aadams", "Alice Adams", "Alice A Adams", false),
		},
		lookup: "@Alice Adams", wantAmbiguous: []string{"U01", "U02"},
	},
	{
		name: "a display name and another account's real name tie",
		users: []slack.User{
			named("U01", "aadams", "Ali", "Alice Adams", false),
			named("U02", "aadams2", "Alice Adams", "Alicia Adams", false),
		},
		lookup: "alice adams", wantAmbiguous: []string{"U01", "U02"},
	},
	{
		name: "a tie lists only the active accounts",
		users: []slack.User{
			named("U01", "aadams-old", "Alice Adams", "", true),
			named("U03", "aadams2", "Alice Adams", "Alice Adams", false),
			named("U02", "aadams", "Alice Adams", "Alice Adams", false),
		},
		lookup: "Alice Adams", wantAmbiguous: []string{"U02", "U03"},
	},
	{
		name: "two deactivated accounts tie when nothing active holds the name",
		users: []slack.User{
			named("U02", "bbrown-older", "Bob Brown", "", true),
			named("U01", "bbrown-old", "Bob Brown", "", true),
		},
		lookup: "Bob Brown", wantAmbiguous: []string{"U01", "U02"},
	},
	{
		name: "one account holding the name twice does not tie with itself",
		users: []slack.User{
			named("U01", "cchen", "Carol Chen", "Carol Chen", false),
		},
		lookup: "Carol Chen", wantID: "U01",
	},
	{
		name: "matching ignores case",
		users: []slack.User{
			named("U01", "CChen", "Carol Chen", "", false),
		},
		lookup: "@cchen", wantID: "U01",
	},
}

func checkNameResult(t *testing.T, id string, err error, wantID string, wantAmbiguous []string) {
	t.Helper()
	if wantAmbiguous == nil {
		if err != nil || id != wantID {
			t.Fatalf("got %q, %v, want %q", id, err, wantID)
		}
		return
	}
	var amb *AmbiguousUserError
	if !errors.As(err, &amb) {
		t.Fatalf("got %q, %v, want an AmbiguousUserError", id, err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("an ambiguous name must not read as ErrNotFound")
	}
	got := make([]string, len(amb.Candidates))
	for i, u := range amb.Candidates {
		got[i] = u.ID
	}
	if len(got) != len(wantAmbiguous) {
		t.Fatalf("candidates = %v, want %v", got, wantAmbiguous)
	}
	for i := range got {
		if got[i] != wantAmbiguous[i] {
			t.Fatalf("candidates = %v, want %v", got, wantAmbiguous)
		}
	}
}

func TestUserIDByName_Ranking(t *testing.T) {
	for _, tt := range nameRankingCases {
		t.Run(tt.name, func(t *testing.T) {
			for range mapOrderRuns {
				r := NewResolver(nil, "", "")
				r.setUserMaps(userMap(tt.users...))
				id, err := r.userIDByName(tt.lookup)
				checkNameResult(t, id, err, tt.wantID, tt.wantAmbiguous)
			}
		})
	}
}

// The single-insert path ranks the same as a bulk load, in every insertion
// order.
func TestUserIDByName_RankingOnSingleInserts(t *testing.T) {
	for _, tt := range nameRankingCases {
		t.Run(tt.name, func(t *testing.T) {
			permute(tt.users, func(order []slack.User) {
				r := NewResolver(nil, "", "")
				for _, u := range order {
					r.addUserToCache(u)
				}
				id, err := r.userIDByName(tt.lookup)
				checkNameResult(t, id, err, tt.wantID, tt.wantAmbiguous)
			})
		})
	}
}

func permute(users []slack.User, fn func([]slack.User)) {
	var rec func(int)
	order := append([]slack.User(nil), users...)
	rec = func(k int) {
		if k == len(order) {
			fn(order)
			return
		}
		for i := k; i < len(order); i++ {
			order[k], order[i] = order[i], order[k]
			rec(k + 1)
			order[k], order[i] = order[i], order[k]
		}
	}
	rec(0)
}

// Re-inserting a user replaces its index entries: the old name stops
// resolving and the user is not listed twice under a name it kept.
func TestAddUserToCache_ReinsertReplacesNameEntries(t *testing.T) {
	r := NewResolver(nil, "", "")
	r.setUserMaps(userMap(
		named("U01", "aadams", "Alice Adams", "Alice Adams", false),
		named("U02", "bbrown", "Bob Brown", "Bob Brown", false),
	))

	r.addUserToCache(named("U01", "aadams", "Alice Adams", "Alice Q Adams", false))
	if id, err := r.userIDByName("Alice Adams"); err != nil || id != "U01" {
		t.Errorf("kept display name: got %q, %v, want U01 once", id, err)
	}

	r.addUserToCache(named("U01", "aadams", "Ali", "Alice Q Adams", false))
	if _, err := r.userIDByName("Alice Adams"); !errors.Is(err, ErrNotFound) {
		t.Errorf("dropped display name: err = %v, want ErrNotFound", err)
	}
	if id, err := r.userIDByName("ali"); err != nil || id != "U01" {
		t.Errorf("new display name: got %q, %v, want U01", id, err)
	}
	if id, err := r.userIDByName("Bob Brown"); err != nil || id != "U02" {
		t.Errorf("untouched user: got %q, %v, want U02", id, err)
	}
}

// Two accounts sharing an email has not been observed, but the answer must
// not depend on map order if it happens.
func TestSetUserMaps_SharedEmailIsDeterministic(t *testing.T) {
	withEmail := func(id string, deleted bool) slack.User {
		return slack.User{ID: id, Deleted: deleted, Profile: slack.UserProfile{Email: "alice@example.com"}}
	}
	tests := []struct {
		name  string
		users []slack.User
		want  string
	}{
		{"active beats deactivated", []slack.User{withEmail("U01", true), withEmail("U02", false)}, "U02"},
		{"lower ID breaks a tie", []slack.User{withEmail("U02", false), withEmail("U01", false), withEmail("U03", false)}, "U01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range mapOrderRuns {
				r := NewResolver(nil, "", "")
				r.setUserMaps(userMap(tt.users...))
				if got := r.usersByEmail["alice@example.com"]; got != tt.want {
					t.Fatalf("got %q, want %q", got, tt.want)
				}
			}
		})
	}
}

// The file cache stores users, not an index, so its format did not change
// with the ranking. A cache file written before the change (the shape below
// is what every earlier release wrote) answers under the new rules with no
// request.
func TestResolveUser_FileCacheFromAnEarlierRelease(t *testing.T) {
	cacheDir := t.TempDir()
	old := map[string]any{
		"updated_at": time.Now().Format(time.RFC3339Nano),
		"users": map[string]any{
			"U01": map[string]any{"id": "U01", "name": "aadams-old", "deleted": true, "real_name": "",
				"profile": map[string]any{"display_name": "Alice Adams", "real_name": "Alice Adams"}},
			"U02": map[string]any{"id": "U02", "name": "aadams", "deleted": false, "real_name": "Alice Adams",
				"profile": map[string]any{"display_name": "Alice Adams", "real_name": "Alice Adams"}},
			"U03": map[string]any{"id": "U03", "name": "bbrown", "real_name": "Bob Brown",
				"profile": map[string]any{"display_name": "Bob"}},
			"U04": map[string]any{"id": "U04", "name": "bbrown2", "real_name": "Bob Brown",
				"profile": map[string]any{"display_name": "Bobby"}},
		},
	}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "users-T123.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s: the file cache holds the directory", r.URL.Path)
	}))
	ctx := context.Background()

	for range mapOrderRuns {
		r := NewResolver(client, "T123", cacheDir)
		id, err := r.ResolveUser(ctx, "@Alice Adams")
		checkNameResult(t, id, err, "U02", nil)
		id, err = r.ResolveUser(ctx, "@Bob Brown")
		checkNameResult(t, id, err, "", []string{"U03", "U04"})
	}
}

// An ambiguous name carries what a caller needs to pick: each candidate's
// record, and the input as typed.
func TestAmbiguousUserError_CarriesCandidates(t *testing.T) {
	r := NewResolver(nil, "", "")
	a := named("U01", "aadams", "Alice Adams", "Alice Adams", false)
	a.Profile.Title = "Staff Engineer"
	b := named("U02", "aadams2", "Alice Adams", "Alice Adams", false)
	r.setUserMaps(userMap(a, b))

	_, err := r.userIDByName("@Alice Adams")
	var amb *AmbiguousUserError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want an AmbiguousUserError", err)
	}
	if amb.Input != "@Alice Adams" {
		t.Errorf("Input = %q, want the input as typed", amb.Input)
	}
	if got := amb.Candidates[0]; got.Name != "aadams" || got.Profile.Title != "Staff Engineer" {
		t.Errorf("first candidate = %+v, want aadams with its title", got)
	}
}
