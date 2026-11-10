package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// profileMock serves users.info for any id and users.profile.get with a
// scripted answer per (user, attempt): an error code, or "" for a profile.
type profileMock struct {
	mu       sync.Mutex
	attempts map[string]int
	// profileErr returns the Slack error for this user's nth profile fetch
	// (1-based), or "" to answer with a profile.
	profileErr func(user string, attempt int) string
	infoErr    func(user string) string
}

func (m *profileMock) mux() *http.ServeMux {
	m.attempts = map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		uid := r.FormValue("user")
		if m.infoErr != nil {
			if code := m.infoErr(uid); code != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"id": uid, "name": "u" + uid}})
	})
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		uid := r.FormValue("user")
		m.mu.Lock()
		m.attempts[uid]++
		n := m.attempts[uid]
		m.mu.Unlock()
		if m.profileErr != nil {
			if code := m.profileErr(uid, n); code != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"profile": map[string]any{"real_name": "Name " + uid, "fields": map[string]any{}},
		})
	})
	return mux
}

func (m *profileMock) profileCalls(user string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts[user]
}

// The reported bug: a systemic failure partway through a bulk --full run
// returned before the trailer, so stdout was a few good rows and nothing else
// - indistinguishable from "only these users exist" once a caller filtered
// out _meta. The abort must end stdout with a trailer that says rows are
// missing, in the same shape channel invite uses.
func TestUserInfo_Full_SystemicAbortWritesTrailer(t *testing.T) {
	m := &profileMock{profileErr: func(user string, _ int) string {
		if user == "U03" {
			return "missing_scope"
		}
		return ""
	}}

	r := runWithMockFull(t, m.mux(), "user", "info", "--full", "U01", "U02", "U03", "U04")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected *output.Error (stderr), got %T: %v", r.err, r.err)
	}
	if oErr.Err != "missing_scope" || oErr.Code != output.ExitAuth {
		t.Errorf("got %q exit %d, want missing_scope exit %d", oErr.Err, oErr.Code, output.ExitAuth)
	}

	rows, meta := rowsAndMeta(t, r.stdout)
	if len(rows) != 2 || rows[0]["input"] != "U01" || rows[1]["input"] != "U02" {
		t.Fatalf("expected the two rows earned before the abort, got:\n%s", r.stdout)
	}
	if meta["has_more"] != true {
		t.Errorf("_meta.has_more = %v, want true (inputs are missing)", meta["has_more"])
	}
	if meta["error"] != "missing_scope" {
		t.Errorf("_meta.error = %v, want missing_scope", meta["error"])
	}
	if _, has := meta["next_cursor"]; has {
		t.Errorf("_meta must carry no cursor: %v", meta)
	}
	if m.profileCalls("U04") != 0 {
		t.Errorf("U04 was fetched after the abort")
	}
}

// users.profile.get rate-limits in the body (200, ok:false, "ratelimited"),
// after about three calls on a real workspace. The fetch goes through the
// shared retry, so a transient limit is waited out instead of ending the run.
func TestUserInfo_Full_RateLimitedProfileIsRetried(t *testing.T) {
	m := &profileMock{profileErr: func(user string, attempt int) string {
		if user == "U02" && attempt == 1 {
			return "ratelimited"
		}
		return ""
	}}

	r := runWithMockFull(t, m.mux(), "user", "info", "--full", "U01", "U02", "U03")
	if r.err != nil {
		t.Fatalf("a transient rate limit must be recovered, got %T: %v", r.err, r.err)
	}
	if got := m.profileCalls("U02"); got != 2 {
		t.Errorf("U02 profile fetches = %d, want 2", got)
	}
	rows, meta := rowsAndMeta(t, r.stdout)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d:\n%s", len(rows), r.stdout)
	}
	for _, row := range rows {
		if row["custom_fields"] == nil || row["error"] != nil {
			t.Errorf("row is not a full profile row: %v", row)
		}
	}
	if meta["error"] != nil || meta["has_more"] != false {
		t.Errorf("_meta = %v, want a clean trailer", meta)
	}
}

// A lookup failure that is not "no such user" must say what it was. Both
// lookups used to label every non-systemic failure user_not_found, so a user
// whose fetch hit a transient Slack error was reported as nonexistent.
func TestUserInfo_LookupFailureKeepsItsCode(t *testing.T) {
	tests := []struct {
		name string
		mock *profileMock
		args []string
	}{
		{
			name: "users.info",
			mock: &profileMock{infoErr: func(user string) string {
				if user == "U02" {
					return "fatal_error"
				}
				return ""
			}},
			args: []string{"user", "info", "U01", "U02"},
		},
		{
			name: "users.profile.get",
			mock: &profileMock{profileErr: func(user string, _ int) string {
				if user == "U02" {
					return "fatal_error"
				}
				return ""
			}},
			args: []string{"user", "info", "--full", "U01", "U02"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runWithMockFull(t, tt.mock.mux(), tt.args...)
			assertExitError(t, r.err)
			rows, meta := rowsAndMeta(t, r.stdout)
			if len(rows) != 2 {
				t.Fatalf("expected 2 rows, got %d:\n%s", len(rows), r.stdout)
			}
			if rows[1]["input"] != "U02" || rows[1]["error"] != "fatal_error" {
				t.Errorf("expected {input:U02, error:fatal_error}, got %v", rows[1])
			}
			if meta["error_count"] != float64(1) || meta["has_more"] != false {
				t.Errorf("_meta = %v, want error_count 1 and has_more false", meta)
			}
		})
	}
}

// manager-chain shares the profile fetch, so it shares both fixes: a trailer
// on the abort path, and retry on a rate limit.
func TestManagerChain_SystemicAbortWritesTrailer(t *testing.T) {
	m := &profileMock{profileErr: func(user string, _ int) string {
		if user == "U02" {
			return "missing_scope"
		}
		return ""
	}}

	r := runWithMockFull(t, m.mux(), "user", "manager-chain", "U01", "U02", "U03")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) || oErr.Err != "missing_scope" {
		t.Fatalf("expected a fatal missing_scope, got %T: %v", r.err, r.err)
	}
	rows, meta := rowsAndMeta(t, r.stdout)
	if len(rows) != 1 || rows[0]["input"] != "U01" {
		t.Fatalf("expected U01's chain row before the abort, got:\n%s", r.stdout)
	}
	if meta["has_more"] != true || meta["error"] != "missing_scope" {
		t.Errorf("_meta = %v, want has_more true and error missing_scope", meta)
	}
}

func TestManagerChain_RateLimitedProfileIsRetried(t *testing.T) {
	m := &profileMock{profileErr: func(user string, attempt int) string {
		if attempt == 1 {
			return "ratelimited"
		}
		return ""
	}}

	r := runWithMockFull(t, m.mux(), "user", "manager-chain", "U01")
	if r.err != nil {
		t.Fatalf("a transient rate limit must be recovered, got %T: %v", r.err, r.err)
	}
	rows, _ := rowsAndMeta(t, r.stdout)
	if len(rows) != 1 || rows[0]["stop_reason"] != "no_manager" {
		t.Fatalf("expected one no_manager row, got:\n%s", r.stdout)
	}
	if got := m.profileCalls("U01"); got != 2 {
		t.Errorf("profile fetches = %d, want 2", got)
	}
}

// The abort trailer carries the per-input failures counted before the abort.
func TestUserInfo_Full_AbortTrailerCountsEarlierErrors(t *testing.T) {
	m := &profileMock{profileErr: func(user string, _ int) string {
		switch user {
		case "U01":
			return "user_not_found"
		case "U02":
			return "missing_scope"
		}
		return ""
	}}

	r := runWithMockFull(t, m.mux(), "user", "info", "--full", "U01", "U02", "U03")
	meta := assertFatalAbort(t, r.err, r.stdout, "missing_scope", output.ExitAuth, 1)
	if meta["error_count"] != float64(1) {
		t.Errorf("_meta.error_count = %v, want 1", meta["error_count"])
	}
}

// Slack's not-found spellings all become user_not_found with its discovery
// hint; that is the one case the label is right.
func TestUserInfo_NotFoundCodesBecomeUserNotFound(t *testing.T) {
	for _, code := range []string{"user_not_found", "users_not_found", "user_not_visible"} {
		t.Run(code, func(t *testing.T) {
			m := &profileMock{infoErr: func(string) string { return code }}
			r := runWithMockFull(t, m.mux(), "user", "info", "U01")
			assertExitError(t, r.err)
			rows, _ := rowsAndMeta(t, r.stdout)
			if len(rows) != 1 || rows[0]["error"] != "user_not_found" || rows[0]["hint"] == nil {
				t.Errorf("expected a user_not_found row with a hint, got %v", rows)
			}
		})
	}
}

// The users.info stage aborts on a systemic error too, and retries a rate
// limit like the profile fetch does.
func TestUserInfo_UsersInfoSystemicAbortWritesTrailer(t *testing.T) {
	m := &profileMock{infoErr: func(user string) string {
		if user == "U02" {
			return "invalid_auth"
		}
		return ""
	}}
	r := runWithMockFull(t, m.mux(), "user", "info", "U01", "U02", "U03")
	assertFatalAbort(t, r.err, r.stdout, "invalid_auth", output.ExitAuth, 1)
}

func TestUserInfo_RateLimitedUsersInfoIsRetried(t *testing.T) {
	// The resolver's cache lookup spends users.info call 1 and swallows its
	// error; the command's own fetch is call 2, limited, and call 3 succeeds.
	var calls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"id": "U01", "name": "alice"}})
	})

	r := runWithMockFull(t, mux, "user", "info", "U01")
	if r.err != nil {
		t.Fatalf("a transient rate limit must be recovered, got %T: %v", r.err, r.err)
	}
	rows, _ := rowsAndMeta(t, r.stdout)
	if len(rows) != 1 || rows[0]["name"] != "alice" {
		t.Errorf("expected alice's row, got:\n%s", r.stdout)
	}
}

// An email that cannot be resolved because the lookup itself failed
// systemically stops the run; it is not a missing user.
func TestUserInfo_ResolveSystemicAbortWritesTrailer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "members": []any{}})
	})
	mux.HandleFunc("/api/users.lookupByEmail", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing_scope"})
	})

	r := runWithMockFull(t, mux, "user", "info", "alice@example.com", "U02")
	assertFatalAbort(t, r.err, r.stdout, "missing_scope", output.ExitAuth, 0)
}
