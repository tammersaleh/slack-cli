package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// membershipMock serves conversations.invite and conversations.kick. Each
// (channel,user) pair answers the code in answers (ok when absent), and every
// call is logged in order so tests can assert exactly which pairs were sent.
type membershipMock struct {
	mu      sync.Mutex
	answers map[string]string // "C01ABC/U01XYZ" -> slack error code, "" for ok
	calls   []string          // "endpoint C01ABC/U01XYZ"
	// rateLimitOnce, when set, answers the first request for that pair with a
	// 429 carrying Retry-After and then lets it through.
	rateLimitOnce map[string]bool
	// rateLimitAlways answers every request for that pair with a 429, which
	// is how a run exhausts the retry budget. Those requests are not logged.
	rateLimitAlways map[string]bool
	// fatalAfter, when > 0, answers every call past that many with fatalCode.
	fatalAfter int
	fatalCode  string
}

func newMembershipMock() *membershipMock {
	return &membershipMock{answers: map[string]string{}, rateLimitOnce: map[string]bool{}, rateLimitAlways: map[string]bool{}}
}

func (m *membershipMock) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.invite", m.handle("conversations.invite", "users"))
	mux.HandleFunc("/api/conversations.kick", m.handle("conversations.kick", "user"))
	return mux
}

func (m *membershipMock) handle(endpoint, userParam string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t := http.StatusBadRequest
			http.Error(w, err.Error(), t)
			return
		}
		pair := r.PostForm.Get("channel") + "/" + r.PostForm.Get(userParam)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.rateLimitOnce[pair] || m.rateLimitAlways[pair] {
			delete(m.rateLimitOnce, pair)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
			return
		}
		m.calls = append(m.calls, endpoint+" "+pair)
		if m.fatalAfter > 0 && len(m.calls) > m.fatalAfter {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": m.fatalCode})
			return
		}
		if code := m.answers[pair]; code != "" {
			resp := map[string]any{"ok": false, "error": code}
			if endpoint == "conversations.invite" {
				resp["errors"] = []map[string]any{{"user": r.PostForm.Get(userParam), "ok": false, "error": code}}
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"id": r.PostForm.Get("channel")}})
	}
}

func (m *membershipMock) callLog() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

// verb binds the two commands' differing words so each scenario runs for both.
type membershipVerb struct {
	cmd        string
	endpoint   string
	done       string
	idempotent string
	pairErr    string
	channelErr string
}

var membershipVerbs = []membershipVerb{
	{"invite", "conversations.invite", "invited", "already_in_channel", "cant_invite", "is_archived"},
	{"kick", "conversations.kick", "kicked", "not_in_channel", "cant_kick_self", "cant_kick_from_general"},
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func assertRow(t *testing.T, row map[string]any, channel, user, key, value string) {
	t.Helper()
	if row["channel_id"] != channel || row["user_id"] != user {
		t.Errorf("row = %v, want channel %s user %s", row, channel, user)
	}
	if row[key] != value {
		t.Errorf("row %v: %s = %v, want %q", row, key, row[key], value)
	}
	other := "error"
	if key == "error" {
		other = "status"
	}
	if _, has := row[other]; has {
		t.Errorf("row %v carries both status and error", row)
	}
}

func TestChannelMembership_Grammar(t *testing.T) {
	for _, v := range membershipVerbs {
		for _, tc := range []struct {
			name string
			args []string
		}{
			{"no user", []string{"C01ABC", "C02DEF"}},
			{"no channel", []string{"@alice", "U02MGR"}},
			{"bad url", []string{"C01ABC", "https://acme.slack.com/team/not-a-user-id"}},
		} {
			t.Run(v.cmd+"/"+tc.name, func(t *testing.T) {
				m := newMembershipMock()
				out, err := runWithMock(t, m.mux(), append([]string{"channel", v.cmd}, tc.args...)...)
				var oErr *output.Error
				if !errors.As(err, &oErr) || oErr.Err != "invalid_input" {
					t.Fatalf("expected invalid_input, got %v", err)
				}
				if out != "" {
					t.Errorf("expected no stdout, got %q", out)
				}
				if len(m.callLog()) != 0 {
					t.Errorf("expected no API calls, got %v", m.callLog())
				}
			})
		}
	}
}

// Resolution happens for every input before the first write, so a bad user
// among good channels leaves every channel untouched.
func TestChannelMembership_ResolveBeforeWrite(t *testing.T) {
	for _, v := range membershipVerbs {
		t.Run(v.cmd, func(t *testing.T) {
			m := newMembershipMock()
			mux := m.mux()
			mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":                true,
					"members":           []map[string]any{{"id": "U01XYZ", "name": "alice"}},
					"response_metadata": map[string]string{"next_cursor": ""},
				})
			})
			out, err := runWithMock(t, mux, "channel", v.cmd, "C01ABC", "C02DEF", "@alice", "@nobody")
			var oErr *output.Error
			if !errors.As(err, &oErr) || oErr.Err != "user_not_found" || oErr.Input != "@nobody" {
				t.Fatalf("expected user_not_found for @nobody, got %v", err)
			}
			if out != "" {
				t.Errorf("expected no stdout, got %q", out)
			}
			if len(m.callLog()) != 0 {
				t.Errorf("expected no writes, got %v", m.callLog())
			}
		})
	}
}

func TestChannelMembership_ChannelNotFound(t *testing.T) {
	m := newMembershipMock()
	mux := m.mux()
	for _, ep := range []string{"/api/users.conversations", "/api/conversations.list"} {
		mux.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                true,
				"channels":          []map[string]any{},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		})
	}
	_, err := runWithMock(t, mux, "channel", "invite", "#missing", "U01XYZ")
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "channel_not_found" || oErr.Input != "#missing" {
		t.Fatalf("expected channel_not_found for #missing, got %v", err)
	}
	if len(m.callLog()) != 0 {
		t.Errorf("expected no writes, got %v", m.callLog())
	}
}

// One call per pair, channels outer in input order, users inner in input
// order, with the user in the endpoint's own parameter name.
func TestChannelMembership_OneCallPerPairInOrder(t *testing.T) {
	for _, v := range membershipVerbs {
		t.Run(v.cmd, func(t *testing.T) {
			m := newMembershipMock()
			out, err := runWithMock(t, m.mux(), "channel", v.cmd, "U01XYZ", "C02DEF", "C01ABC", "U02MGR")
			if err != nil {
				t.Fatal(err)
			}
			assertCalls(t, m.callLog(), []string{
				v.endpoint + " C02DEF/U01XYZ",
				v.endpoint + " C02DEF/U02MGR",
				v.endpoint + " C01ABC/U01XYZ",
				v.endpoint + " C01ABC/U02MGR",
			})
			rows, meta := rowsAndMeta(t, out)
			if len(rows) != 4 {
				t.Fatalf("got %d rows, want 4:\n%s", len(rows), out)
			}
			assertRow(t, rows[0], "C02DEF", "U01XYZ", "status", v.done)
			assertRow(t, rows[1], "C02DEF", "U02MGR", "status", v.done)
			assertRow(t, rows[2], "C01ABC", "U01XYZ", "status", v.done)
			assertRow(t, rows[3], "C01ABC", "U02MGR", "status", v.done)
			if meta["has_more"] != false || meta["error_count"] != nil || meta["error"] != nil {
				t.Errorf("meta = %v, want has_more:false and no error fields", meta)
			}
		})
	}
}

// Repeated inputs collapse to one call; first-seen order wins.
func TestChannelMembership_Dedupe(t *testing.T) {
	for _, v := range membershipVerbs {
		t.Run(v.cmd, func(t *testing.T) {
			m := newMembershipMock()
			_, err := runWithMock(t, m.mux(), "channel", v.cmd, "C01ABC", "U01XYZ", "C01ABC", "U01XYZ", "C02DEF")
			if err != nil {
				t.Fatal(err)
			}
			assertCalls(t, m.callLog(), []string{
				v.endpoint + " C01ABC/U01XYZ",
				v.endpoint + " C02DEF/U01XYZ",
			})
		})
	}
}

// Rate-limit exhaustion is fatal with exit 3: rows so far stay, the trailer
// names rate_limited, and no further pair is attempted.
func TestChannelMembership_RateLimitExhaustedIsFatal(t *testing.T) {
	m := newMembershipMock()
	m.rateLimitAlways["C02DEF/U01XYZ"] = true
	r := runWithMockFull(t, m.mux(), "channel", "invite", "C01ABC", "C02DEF", "C03GHI", "U01XYZ")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) || oErr.Err != "rate_limited" || oErr.Code != output.ExitRateLimit {
		t.Fatalf("expected rate_limited exit %d, got %v", output.ExitRateLimit, r.err)
	}
	if oErr.Endpoint != "conversations.invite" {
		t.Errorf("endpoint = %q, want conversations.invite", oErr.Endpoint)
	}
	assertCalls(t, m.callLog(), []string{"conversations.invite C01ABC/U01XYZ"})
	rows, meta := rowsAndMeta(t, r.stdout)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1:\n%s", len(rows), r.stdout)
	}
	assertRow(t, rows[0], "C01ABC", "U01XYZ", "status", "invited")
	if meta["has_more"] != true || meta["error"] != "rate_limited" {
		t.Errorf("meta = %v, want has_more:true error:rate_limited", meta)
	}
}

// --quiet keeps the exit code of a pair error with nothing on stdout, and
// still makes every call.
func TestChannelMembership_QuietKeepsExitCode(t *testing.T) {
	m := newMembershipMock()
	m.answers["C01ABC/U01XYZ"] = "cant_invite"
	r := runWithMockFull(t, m.mux(), "channel", "invite", "--quiet", "C01ABC", "U01XYZ", "U02MGR")
	assertExitError(t, r.err)
	if r.stdout != "" || r.stderr != "" {
		t.Errorf("expected no output under --quiet, got stdout %q stderr %q", r.stdout, r.stderr)
	}
	if len(m.callLog()) != 2 {
		t.Errorf("expected both pairs attempted, got %v", m.callLog())
	}
}

// The idempotent answer is a status, not an error: exit 0, no error_count.
func TestChannelMembership_IdempotentIsStatus(t *testing.T) {
	for _, v := range membershipVerbs {
		t.Run(v.cmd, func(t *testing.T) {
			m := newMembershipMock()
			m.answers["C01ABC/U01XYZ"] = v.idempotent
			out, err := runWithMock(t, m.mux(), "channel", v.cmd, "C01ABC", "U01XYZ", "U02MGR")
			if err != nil {
				t.Fatalf("idempotent outcome must exit 0, got %v", err)
			}
			rows, meta := rowsAndMeta(t, out)
			if len(rows) != 2 {
				t.Fatalf("got %d rows, want 2:\n%s", len(rows), out)
			}
			assertRow(t, rows[0], "C01ABC", "U01XYZ", "status", v.idempotent)
			assertRow(t, rows[1], "C01ABC", "U02MGR", "status", v.done)
			if meta["error_count"] != nil {
				t.Errorf("error_count = %v, want absent", meta["error_count"])
			}
		})
	}
}

// A pair error marks its row and the run continues through every other pair.
func TestChannelMembership_PairError(t *testing.T) {
	for _, v := range membershipVerbs {
		t.Run(v.cmd, func(t *testing.T) {
			m := newMembershipMock()
			m.answers["C01ABC/U01XYZ"] = v.pairErr
			out, err := runWithMock(t, m.mux(), "channel", v.cmd, "C01ABC", "C02DEF", "U01XYZ", "U02MGR")
			assertExitError(t, err)
			if len(m.callLog()) != 4 {
				t.Errorf("expected all 4 pairs attempted, got %v", m.callLog())
			}
			rows, meta := rowsAndMeta(t, out)
			if len(rows) != 4 {
				t.Fatalf("got %d rows, want 4:\n%s", len(rows), out)
			}
			assertRow(t, rows[0], "C01ABC", "U01XYZ", "error", v.pairErr)
			assertRow(t, rows[1], "C01ABC", "U02MGR", "status", v.done)
			assertRow(t, rows[2], "C02DEF", "U01XYZ", "status", v.done)
			assertRow(t, rows[3], "C02DEF", "U02MGR", "status", v.done)
			if meta["error_count"] != float64(1) || meta["has_more"] != false {
				t.Errorf("meta = %v, want error_count:1 has_more:false", meta)
			}
		})
	}
}

// A channel error fans out to every remaining user in that channel without
// another call, and the next channel proceeds normally.
func TestChannelMembership_ChannelErrorFanOut(t *testing.T) {
	for _, v := range membershipVerbs {
		t.Run(v.cmd, func(t *testing.T) {
			m := newMembershipMock()
			m.answers["C01ABC/U01XYZ"] = v.channelErr
			out, err := runWithMock(t, m.mux(), "channel", v.cmd, "C01ABC", "C02DEF", "U01XYZ", "U02MGR", "U03BOB")
			assertExitError(t, err)
			assertCalls(t, m.callLog(), []string{
				v.endpoint + " C01ABC/U01XYZ",
				v.endpoint + " C02DEF/U01XYZ",
				v.endpoint + " C02DEF/U02MGR",
				v.endpoint + " C02DEF/U03BOB",
			})
			rows, meta := rowsAndMeta(t, out)
			if len(rows) != 6 {
				t.Fatalf("got %d rows, want 6:\n%s", len(rows), out)
			}
			assertRow(t, rows[0], "C01ABC", "U01XYZ", "error", v.channelErr)
			assertRow(t, rows[1], "C01ABC", "U02MGR", "error", v.channelErr)
			assertRow(t, rows[2], "C01ABC", "U03BOB", "error", v.channelErr)
			assertRow(t, rows[3], "C02DEF", "U01XYZ", "status", v.done)
			assertRow(t, rows[4], "C02DEF", "U02MGR", "status", v.done)
			assertRow(t, rows[5], "C02DEF", "U03BOB", "status", v.done)
			if meta["error_count"] != float64(3) {
				t.Errorf("error_count = %v, want 3", meta["error_count"])
			}
		})
	}
}

// not_in_channel is the target user on kick (idempotent) and the caller on
// invite (channel error). Same code, different disposition.
func TestChannelMembership_NotInChannelIsEndpointSpecific(t *testing.T) {
	m := newMembershipMock()
	m.answers["C01ABC/U01XYZ"] = "not_in_channel"

	out, err := runWithMock(t, m.mux(), "channel", "kick", "C01ABC", "U01XYZ", "U02MGR")
	if err != nil {
		t.Fatalf("kick not_in_channel must be idempotent, got %v", err)
	}
	rows, _ := rowsAndMeta(t, out)
	assertRow(t, rows[0], "C01ABC", "U01XYZ", "status", "not_in_channel")
	assertRow(t, rows[1], "C01ABC", "U02MGR", "status", "kicked")

	m = newMembershipMock()
	m.answers["C01ABC/U01XYZ"] = "not_in_channel"
	out, err = runWithMock(t, m.mux(), "channel", "invite", "C01ABC", "U01XYZ", "U02MGR")
	assertExitError(t, err)
	assertCalls(t, m.callLog(), []string{"conversations.invite C01ABC/U01XYZ"})
	rows, _ = rowsAndMeta(t, out)
	assertRow(t, rows[0], "C01ABC", "U01XYZ", "error", "not_in_channel")
	assertRow(t, rows[1], "C01ABC", "U02MGR", "error", "not_in_channel")
}

// Fatal codes abort after the rows already earned: the returned error is the
// classified *output.Error (which main prints to stderr) with its exit code,
// and the trailer reads has_more:true with the code and no cursor.
func TestChannelMembership_FatalAbortsRun(t *testing.T) {
	cases := []struct {
		code     string
		wantExit int
	}{
		{"invalid_auth", output.ExitAuth},
		{"missing_scope", output.ExitGeneral},
		{"no_permission", output.ExitGeneral},
		{"restricted_action", output.ExitGeneral},
		{"some_code_slack_invents_next_year", output.ExitGeneral},
	}
	for _, v := range membershipVerbs {
		for _, tc := range cases {
			t.Run(v.cmd+"/"+tc.code, func(t *testing.T) {
				m := newMembershipMock()
				m.fatalAfter = 1
				m.fatalCode = tc.code
				r := runWithMockFull(t, m.mux(), "channel", v.cmd, "C01ABC", "C02DEF", "U01XYZ", "U02MGR")
				var oErr *output.Error
				if !errors.As(r.err, &oErr) {
					t.Fatalf("expected *output.Error, got %T: %v", r.err, r.err)
				}
				if oErr.Err != tc.code || oErr.Code != tc.wantExit {
					t.Errorf("got error %q exit %d, want %q exit %d", oErr.Err, oErr.Code, tc.code, tc.wantExit)
				}
				if len(m.callLog()) != 2 {
					t.Errorf("expected the run to stop at the fatal call, got %v", m.callLog())
				}
				rows, meta := rowsAndMeta(t, r.stdout)
				if len(rows) != 1 {
					t.Fatalf("got %d rows, want the 1 completed before the fatal error:\n%s", len(rows), r.stdout)
				}
				assertRow(t, rows[0], "C01ABC", "U01XYZ", "status", v.done)
				if meta["has_more"] != true || meta["error"] != tc.code || meta["next_cursor"] != nil {
					t.Errorf("meta = %v, want has_more:true error:%s and no cursor", meta, tc.code)
				}
			})
		}
	}
}

// A 429 with Retry-After is retried, so a rate-limited pair still lands.
func TestChannelMembership_RateLimitRetry(t *testing.T) {
	for _, v := range membershipVerbs {
		t.Run(v.cmd, func(t *testing.T) {
			m := newMembershipMock()
			m.rateLimitOnce["C01ABC/U01XYZ"] = true
			out, err := runWithMock(t, m.mux(), "channel", v.cmd, "C01ABC", "U01XYZ")
			if err != nil {
				t.Fatal(err)
			}
			rows, _ := rowsAndMeta(t, out)
			if len(rows) != 1 {
				t.Fatalf("got %d rows, want 1:\n%s", len(rows), out)
			}
			assertRow(t, rows[0], "C01ABC", "U01XYZ", "status", v.done)
		})
	}
}

// Rows use channel_id/user_id so the printer's enrichment attaches names.
func TestChannelMembership_RowsEnrichNames(t *testing.T) {
	m := newMembershipMock()
	mux := m.mux()
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          []map[string]any{{"id": "C01ABC", "name": "eng-a"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"members":           []map[string]any{{"id": "U01XYZ", "name": "alice", "profile": map[string]any{"display_name": "alice"}}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	out, err := runWithMock(t, mux, "channel", "invite", "#eng-a", "@alice")
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, m.callLog(), []string{"conversations.invite C01ABC/U01XYZ"})
	rows, _ := rowsAndMeta(t, out)
	if rows[0]["channel_name"] != "eng-a" || rows[0]["user_name"] != "alice" {
		t.Errorf("row = %v, want channel_name eng-a and user_name alice", rows[0])
	}
}

