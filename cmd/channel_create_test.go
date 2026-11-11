package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// createMock serves conversations.create. A successful answer is built from
// the request the way Slack's is, except that the stored name is storedName
// when set, so a test can tell a row rendered from the response from one
// rendered from the request.
type createMock struct {
	mu         sync.Mutex
	forms      []url.Values
	errCode    string // answer ok:false with this code
	rateLimit  bool   // answer every request with HTTP 429
	storedName string
}

func (m *createMock) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.create", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.forms = append(m.forms, r.PostForm)
		if m.rateLimit {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
			return
		}
		if m.errCode != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": m.errCode})
			return
		}
		name := r.PostForm.Get("name")
		if m.storedName != "" {
			name = m.storedName
		}
		private := r.PostForm.Get("is_private") == "true"
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{
			"id":              "C01ABC",
			"name":            name,
			"name_normalized": name,
			"is_channel":      true,
			"is_private":      private,
			"is_archived":     false,
			"is_member":       true,
			"created":         1700000000,
			"creator":         "U01XYZ",
			"topic":           map[string]any{"value": "", "creator": "", "last_set": 0},
			"purpose":         map[string]any{"value": "", "creator": "", "last_set": 0},
		}})
	})
	return mux
}

func (m *createMock) requests() []url.Values {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]url.Values(nil), m.forms...)
}

// oneRowAndTrailer asserts stdout is exactly one row then a clean trailer.
func oneRowAndTrailer(t *testing.T, stdout string) map[string]any {
	t.Helper()
	lines := nonEmptyLines(stdout)
	if len(lines) != 2 {
		t.Fatalf("expected one row and a trailer, got %d lines: %s", len(lines), stdout)
	}
	meta, ok := parseJSON(t, lines[1])["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("last line is not a _meta trailer: %s", lines[1])
	}
	if meta["has_more"] != false || meta["error"] != nil {
		t.Errorf("trailer = %v, want has_more:false and no error", meta)
	}
	return parseJSON(t, lines[0])
}

func TestChannelCreate_PublicAndPrivate(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantPrivate bool
		wantForm    string
	}{
		{"public", []string{"channel", "create", "eng-a"}, false, "false"},
		{"private", []string{"channel", "create", "eng-a", "--private"}, true, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &createMock{}
			r := runWithMockFull(t, m.mux(), tc.args...)
			if r.err != nil {
				t.Fatalf("unexpected error: %v", r.err)
			}
			row := oneRowAndTrailer(t, r.stdout)
			if row["id"] != "C01ABC" || row["name"] != "eng-a" {
				t.Errorf("row id/name = %v/%v, want C01ABC/eng-a", row["id"], row["name"])
			}
			if row["is_private"] != tc.wantPrivate {
				t.Errorf("is_private = %v, want %v", row["is_private"], tc.wantPrivate)
			}
			if row["is_member"] != true || row["is_archived"] != false {
				t.Errorf("is_member/is_archived = %v/%v, want true/false", row["is_member"], row["is_archived"])
			}
			// Slack's create response carries no member count; a 0 would be a
			// fabricated value for a channel that has one member.
			if _, has := row["num_members"]; has {
				t.Errorf("row carries num_members, which conversations.create does not send: %v", row)
			}

			reqs := m.requests()
			if len(reqs) != 1 {
				t.Fatalf("got %d conversations.create requests, want 1", len(reqs))
			}
			if got := reqs[0].Get("name"); got != "eng-a" {
				t.Errorf("name sent = %q, want eng-a", got)
			}
			if got := reqs[0].Get("is_private"); got != tc.wantForm {
				t.Errorf("is_private sent = %q, want %q", got, tc.wantForm)
			}
			if _, has := reqs[0]["team_id"]; has {
				t.Errorf("team_id was sent; the workspace token needs none")
			}
		})
	}
}

// The row is the server's channel object, never the request: Slack decides
// the stored name.
func TestChannelCreate_RowComesFromResponse(t *testing.T) {
	m := &createMock{storedName: "stored-name"}
	stdout, err := runWithMock(t, m.mux(), "channel", "create", "asked-name")
	if err != nil {
		t.Fatal(err)
	}
	if row := oneRowAndTrailer(t, stdout); row["name"] != "stored-name" {
		t.Errorf("name = %v, want the stored name from the response", row["name"])
	}
}

// '#name' is how every other command writes a channel, so one leading '#' is
// dropped. Nothing else about the name is touched: Slack validates it.
func TestChannelCreate_NameHandling(t *testing.T) {
	cases := []struct{ arg, sent string }{
		{"#eng-a", "eng-a"},
		{"##eng-a", "#eng-a"},
		{"Eng A!", "Eng A!"},
		{" eng-a ", " eng-a "},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			m := &createMock{}
			if _, err := runWithMock(t, m.mux(), "channel", "create", tc.arg); err != nil {
				t.Fatal(err)
			}
			reqs := m.requests()
			if len(reqs) != 1 || reqs[0].Get("name") != tc.sent {
				t.Fatalf("requests = %v, want one with name %q", reqs, tc.sent)
			}
		})
	}
}

func TestChannelCreate_Fields(t *testing.T) {
	m := &createMock{}
	stdout, err := runWithMock(t, m.mux(), "channel", "create", "eng-a", "--private", "--fields", "id,name")
	if err != nil {
		t.Fatal(err)
	}
	row := oneRowAndTrailer(t, stdout)
	if len(row) != 2 || row["id"] != "C01ABC" || row["name"] != "eng-a" {
		t.Errorf("row = %v, want exactly id and name", row)
	}
}

// Every failure is fatal: nothing on stdout, the classified error returned
// for stderr, and exactly one request.
func TestChannelCreate_Errors(t *testing.T) {
	cases := []struct {
		code     string
		wantCode int
		hint     string // substring the hint must contain; "" for no create-specific hint
	}{
		{"name_taken", output.ExitGeneral, "already has that name"},
		{"invalid_name_required", output.ExitGeneral, "lowercase"},
		{"invalid_name_maxlength", output.ExitGeneral, "lowercase"},
		{"invalid_name_specials", output.ExitGeneral, "lowercase"},
		{"invalid_name_punctuation", output.ExitGeneral, "lowercase"},
		{"invalid_name", output.ExitGeneral, "lowercase"},
		{"restricted_action", output.ExitGeneral, "Workspace policy does not let this account create"},
		{"user_is_restricted", output.ExitGeneral, "guest"},
		{"user_is_ultra_restricted", output.ExitGeneral, "guest"},
		{"no_permission", output.ExitGeneral, "does not have permission to create a channel"},
		{"cannot_create_channel", output.ExitGeneral, "SLACK_WORKSPACE"},
		{"missing_scope", output.ExitAuth, "scope"},
		{"invalid_auth", output.ExitAuth, "auth login"},
		{"some_code_slack_has_not_invented", output.ExitGeneral, ""},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			m := &createMock{errCode: tc.code}
			r := runWithMockFull(t, m.mux(), "channel", "create", "eng-a")
			var oErr *output.Error
			if !errors.As(r.err, &oErr) {
				t.Fatalf("expected *output.Error, got %T: %v", r.err, r.err)
			}
			if oErr.Err != tc.code || oErr.Code != tc.wantCode {
				t.Errorf("error = %q exit %d, want %q exit %d", oErr.Err, oErr.Code, tc.code, tc.wantCode)
			}
			if tc.hint == "" {
				if oErr.Hint != "" {
					t.Errorf("unknown code got a hint: %q", oErr.Hint)
				}
			} else if !strings.Contains(oErr.Hint, tc.hint) {
				t.Errorf("hint = %q, want it to contain %q", oErr.Hint, tc.hint)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want nothing: no channel was created", r.stdout)
			}
			if n := len(m.requests()); n != 1 {
				t.Errorf("got %d requests, want 1", n)
			}
		})
	}
}

// The name_taken hint names the channel so the caller can go and find it.
func TestChannelCreate_NameTakenHintNamesTheChannel(t *testing.T) {
	m := &createMock{errCode: "name_taken"}
	r := runWithMockFull(t, m.mux(), "channel", "create", "#eng-a")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected *output.Error, got %v", r.err)
	}
	if !strings.Contains(oErr.Hint, "Run slack channel info '#eng-a' ") {
		t.Errorf("hint = %q, want the channel info lookup for #eng-a", oErr.Hint)
	}
}

// A create is not safe to repeat: a second request after one that did run
// answers name_taken, which would report a failure for a channel that
// exists. So a rate limit is one request and a rate_limited error.
func TestChannelCreate_RateLimitIsNotRetried(t *testing.T) {
	m := &createMock{rateLimit: true}
	r := runWithMockFull(t, m.mux(), "channel", "create", "eng-a")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) || oErr.Err != "rate_limited" || oErr.Code != output.ExitRateLimit {
		t.Fatalf("expected rate_limited exit %d, got %v", output.ExitRateLimit, r.err)
	}
	if n := len(m.requests()); n != 1 {
		t.Errorf("got %d requests, want exactly 1 (no retry)", n)
	}
	if !strings.Contains(oErr.Hint, "Run slack channel info '#eng-a' ") {
		t.Errorf("hint = %q, want it to say how to check whether the channel exists", oErr.Hint)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want nothing", r.stdout)
	}
}

// A body-level ratelimited (HTTP 200) is the other wire shape of a rate
// limit and gets the same single-request treatment.
func TestChannelCreate_BodyRateLimitIsNotRetried(t *testing.T) {
	m := &createMock{errCode: "ratelimited"}
	r := runWithMockFull(t, m.mux(), "channel", "create", "eng-a")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) || oErr.Err != "rate_limited" || oErr.Code != output.ExitRateLimit {
		t.Fatalf("expected rate_limited exit %d, got %v", output.ExitRateLimit, r.err)
	}
	if n := len(m.requests()); n != 1 {
		t.Errorf("got %d requests, want exactly 1 (no retry)", n)
	}
	if !strings.Contains(oErr.Hint, unknownOutcomeHint) {
		t.Errorf("hint = %q, want the unknown-outcome text", oErr.Hint)
	}
}

const unknownOutcomeHint = "may or may not exist. Run slack channel info '#eng-a' before running this again"

// A reply that never arrived or could not be read does not say whether the
// create ran. Each of these gets the check-first hint ahead of whatever the
// classifier said, and one request.
func TestChannelCreate_UnknownOutcome(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		handler  http.HandlerFunc
		wantErr  string
		wantKeep string // classifier hint text that must survive
	}{
		{
			name: "http_error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
			wantErr: "http_error",
		},
		{
			name: "parse_error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("<html>captive portal</html>"))
			},
			wantErr:  "parse_error",
			wantKeep: "proxy or captive portal",
		},
		{
			name: "timeout",
			args: []string{"--timeout", "400ms"},
			handler: func(_ http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
				}
			},
			wantErr:  "timeout",
			wantKeep: "--timeout budget expired",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/api/conversations.create", func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests++
				mu.Unlock()
				tc.handler(w, r)
			})
			r := runWithMockFull(t, mux, append(tc.args, "channel", "create", "eng-a")...)
			var oErr *output.Error
			if !errors.As(r.err, &oErr) || oErr.Err != tc.wantErr {
				t.Fatalf("expected %s, got %v", tc.wantErr, r.err)
			}
			if !strings.Contains(oErr.Hint, unknownOutcomeHint) {
				t.Errorf("hint = %q, want the unknown-outcome text", oErr.Hint)
			}
			if tc.wantKeep != "" && !strings.Contains(oErr.Hint, tc.wantKeep) {
				t.Errorf("hint = %q, want the classifier's %q kept", oErr.Hint, tc.wantKeep)
			}
			if strings.Contains(oErr.Hint, "next_cursor") {
				t.Errorf("hint = %q, offers a resume cursor a create does not have", oErr.Hint)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want nothing", r.stdout)
			}
			mu.Lock()
			defer mu.Unlock()
			if requests != 1 {
				t.Errorf("got %d requests, want 1", requests)
			}
		})
	}
}

// A --timeout that has already expired sends nothing, so nothing can have
// been created and the hint must not suggest otherwise.
func TestChannelCreate_TimeoutBeforeRequestSendsNothing(t *testing.T) {
	m := &createMock{}
	r := runWithMockFull(t, m.mux(), "--timeout", "1ns", "channel", "create", "eng-a")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) || oErr.Err != "timeout" {
		t.Fatalf("expected timeout, got %v", r.err)
	}
	if n := len(m.requests()); n != 0 {
		t.Fatalf("got %d requests, want 0", n)
	}
	if strings.Contains(oErr.Hint, "may or may not exist") {
		t.Errorf("hint = %q, claims an unknown outcome for a request never sent", oErr.Hint)
	}
}

func TestChannelCreate_Trace(t *testing.T) {
	m := &createMock{}
	r := runWithMockFull(t, m.mux(), "--trace", "channel", "create", "eng-a")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var found bool
	for _, line := range nonEmptyLines(r.stderr) {
		ev := parseJSON(t, line)
		if ev["kind"] == "call" && ev["endpoint"] == "conversations.create" {
			found = true
		}
	}
	if !found {
		t.Errorf("no call trace event for conversations.create in stderr: %s", r.stderr)
	}
}

// On Enterprise Grid the create rides the workspace credential: the org token
// answers cannot_create_channel unless the request names a team.
func TestChannelCreate_UsesWorkspaceToken(t *testing.T) {
	m := &createMock{}
	wrapped, snapshot := tokenRecorder(t, m.mux())
	stdout, err := runWithTwoCredentials(t, wrapped, "channel", "create", "eng-a", "--private")
	if err != nil {
		t.Fatal(err)
	}
	oneRowAndTrailer(t, stdout)
	assertEndpointTokens(t, snapshot(), map[string]string{"conversations.create": teamToken})
}
