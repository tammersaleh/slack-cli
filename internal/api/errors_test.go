package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

func TestClassifyError_RateLimit(t *testing.T) {
	err := &slack.RateLimitedError{}
	cliErr := ClassifyError(err)
	if cliErr.Code != output.ExitRateLimit {
		t.Errorf("got code=%d, want %d", cliErr.Code, output.ExitRateLimit)
	}
}

func TestClassifyError_RateLimitWithEndpoint(t *testing.T) {
	inner := &slack.RateLimitedError{}
	err := &RateLimitExhaustedError{Err: inner, Endpoint: "conversations.list", Attempts: 5}
	cliErr := ClassifyError(err)
	if cliErr.Code != output.ExitRateLimit {
		t.Errorf("got code=%d, want %d", cliErr.Code, output.ExitRateLimit)
	}
	if cliErr.Endpoint != "conversations.list" {
		t.Errorf("got endpoint=%q, want %q", cliErr.Endpoint, "conversations.list")
	}
	if cliErr.Detail != "Rate limited after 5 attempts on conversations.list" {
		t.Errorf("got detail=%q", cliErr.Detail)
	}
}

func TestClassifyError_Auth(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"invalid_auth", slack.SlackErrorResponse{Err: "invalid_auth"}},
		{"token_revoked", slack.SlackErrorResponse{Err: "token_revoked"}},
		{"not_authed", slack.SlackErrorResponse{Err: "not_authed"}},
		{"account_inactive", slack.SlackErrorResponse{Err: "account_inactive"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cliErr := ClassifyError(tt.err)
			if cliErr.Code != output.ExitAuth {
				t.Errorf("got code=%d, want %d", cliErr.Code, output.ExitAuth)
			}
		})
	}
}

// The predicate decides whether a failed page's cursor is worth handing back
// to the caller. Membership is a contract, so both directions are pinned: a
// request Slack will reject identically forever is non-resumable, and anything
// that could clear on its own or after the operator fixes something stays
// resumable.
func TestIsNonResumablePageError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		// The case the set exists for: handing back a cursor Slack just
		// rejected tells a consumer to retry a token that can never work.
		{"invalid_cursor", slack.SlackErrorResponse{Err: "invalid_cursor"}, true},
		{"wrapped invalid_cursor", fmt.Errorf("fetch page: %w", slack.SlackErrorResponse{Err: "invalid_cursor"}), true},
		{"doubly wrapped invalid_cursor", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", slack.SlackErrorResponse{Err: "invalid_cursor"})), true},

		// Request shape. The bytes that were sent are wrong; nothing about
		// waiting or re-authenticating changes them.
		{"invalid_arguments", slack.SlackErrorResponse{Err: "invalid_arguments"}, true},
		{"invalid_limit", slack.SlackErrorResponse{Err: "invalid_limit"}, true},
		{"invalid_ts_latest", slack.SlackErrorResponse{Err: "invalid_ts_latest"}, true},
		{"unknown_method", slack.SlackErrorResponse{Err: "unknown_method"}, true},

		// The CLI already treats its own synthesized thread_not_found as
		// terminal, so Slack's must match.
		{"thread_not_found", slack.SlackErrorResponse{Err: "thread_not_found"}, true},

		// Transient: the same request can succeed on the next attempt.
		{"internal_error", slack.SlackErrorResponse{Err: "internal_error"}, false},
		{"service_unavailable", slack.SlackErrorResponse{Err: "service_unavailable"}, false},
		{"fatal_error", slack.SlackErrorResponse{Err: "fatal_error"}, false},
		{"ratelimited", slack.SlackErrorResponse{Err: "ratelimited"}, false},
		{"RateLimitedError", &slack.RateLimitedError{}, false},

		// Deliberately resumable: the request is well formed and the cursor
		// is still a real checkpoint. Re-authenticating, being granted a
		// scope, joining the channel, or an admin lifting a restriction all
		// make the same page fetchable, so the checkpoint is worth keeping.
		{"token_revoked", slack.SlackErrorResponse{Err: "token_revoked"}, false},
		{"token_expired", slack.SlackErrorResponse{Err: "token_expired"}, false},
		{"missing_scope", slack.SlackErrorResponse{Err: "missing_scope"}, false},
		{"not_allowed_token_type", slack.SlackErrorResponse{Err: "not_allowed_token_type"}, false},
		{"team_is_restricted", slack.SlackErrorResponse{Err: "team_is_restricted"}, false},
		{"ekm_access_denied", slack.SlackErrorResponse{Err: "ekm_access_denied"}, false},
		{"not_in_channel", slack.SlackErrorResponse{Err: "not_in_channel"}, false},
		{"is_archived", slack.SlackErrorResponse{Err: "is_archived"}, false},
		{"channel_not_found", slack.SlackErrorResponse{Err: "channel_not_found"}, false},
		{"user_not_found", slack.SlackErrorResponse{Err: "user_not_found"}, false},

		// Unknown future Slack codes default to resumable: a wrong terminal
		// verdict discards a usable checkpoint, which is the worse mistake.
		{"unknown future code", slack.SlackErrorResponse{Err: "some_new_slack_error"}, false},

		// Only a typed Slack error qualifies. A local error that merely
		// spells the string must not become terminal.
		{"bare error with the same text", errors.New("invalid_cursor"), false},
		{"network error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNonResumablePageError(tt.err); got != tt.want {
				t.Errorf("IsNonResumablePageError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// The two classifications are independent by design: exit code is one policy,
// cursor disposition another. An auth error must never end up in the
// non-resumable set, because that would silently drop a checkpoint the caller
// can use after re-authenticating.
func TestNonResumableAndAuthSetsDoNotOverlap(t *testing.T) {
	for code := range authErrors {
		if nonResumableErrors[code] {
			t.Errorf("%q is in both authErrors and nonResumableErrors; an auth failure keeps its resume cursor", code)
		}
	}
}

// invalid_cursor gets a real detail and a recovery hint instead of echoing the
// bare Slack string, because "invalid_cursor" alone does not tell a caller that
// the fix is to drop --cursor.
func TestClassifyError_InvalidCursorExplainsRecovery(t *testing.T) {
	cliErr := ClassifyError(slack.SlackErrorResponse{Err: "invalid_cursor"})
	if cliErr.Err != "invalid_cursor" {
		t.Errorf("got err=%q, want invalid_cursor", cliErr.Err)
	}
	if cliErr.Code != output.ExitGeneral {
		t.Errorf("got code=%d, want %d", cliErr.Code, output.ExitGeneral)
	}
	if !strings.Contains(cliErr.Hint, "--cursor") {
		t.Errorf("hint must tell the caller to drop --cursor, got %q", cliErr.Hint)
	}
	if cliErr.Detail == "invalid_cursor" || cliErr.Detail == "" {
		t.Errorf("detail must explain the rejection, got %q", cliErr.Detail)
	}
}

func TestClassifyError_Network(t *testing.T) {
	err := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	cliErr := ClassifyError(err)
	if cliErr.Code != output.ExitNetwork {
		t.Errorf("got code=%d, want %d", cliErr.Code, output.ExitNetwork)
	}
}

func TestClassifyError_General(t *testing.T) {
	err := errors.New("something went wrong")
	cliErr := ClassifyError(err)
	if cliErr.Code != output.ExitGeneral {
		t.Errorf("got code=%d, want %d", cliErr.Code, output.ExitGeneral)
	}
}

// syntaxError returns the *json.SyntaxError the stdlib raises for an HTML body,
// which is what a captive portal or intercepting proxy answering 200 produces.
func syntaxError(t *testing.T) error {
	t.Helper()
	var v map[string]any
	err := json.Unmarshal([]byte("<html>captive portal</html>"), &v)
	var target *json.SyntaxError
	if !errors.As(err, &target) {
		t.Fatalf("wanted a *json.SyntaxError, got %T: %v", err, err)
	}
	return err
}

// unmarshalTypeError returns the *json.UnmarshalTypeError the stdlib raises
// when a field arrives with the wrong type. Its text names Go types, so it must
// never reach the error code.
func unmarshalTypeError(t *testing.T) error {
	t.Helper()
	var v struct {
		Channels []string `json:"channels"`
	}
	err := json.Unmarshal([]byte(`{"channels":"nope"}`), &v)
	var target *json.UnmarshalTypeError
	if !errors.As(err, &target) {
		t.Fatalf("wanted a *json.UnmarshalTypeError, got %T: %v", err, err)
	}
	return err
}

// classifyCorpus is every failure shape ClassifyError is known to see, with the
// two verified deadline wrappings and the three response failures that used to
// fall through to the raw-text fallback. Each case pins the whole classified
// error - code, exit code, detail, and hint - and names the verbatim
// _meta.error the CLI emitted before stable codes landed, so a regression is
// recognizable.
type classifyCase struct {
	name     string
	err      error
	wantErr  string
	wantCode int
	// wantDetail is a substring the detail must carry, so information the
	// code no longer spells is still reported on stderr rather than dropped.
	wantDetail string
	// wantHint is a substring the hint must carry, for the codes whose
	// recovery is not obvious from the code alone. Empty means the code must
	// carry no hint at all, rather than "unasserted" - otherwise forgetting
	// to fill this in for a new recognizer leaves its hint unverified, which
	// is how parse_error's hint went uncovered in the first place. Giving a
	// code a hint is a deliberate act; say so here.
	wantHint string
}

func classifyCorpus(t *testing.T) []classifyCase {
	t.Helper()
	return []classifyCase{
		// Was: `_meta.error` = "context deadline exceeded".
		{
			name: "bare deadline", err: context.DeadlineExceeded,
			wantErr: "timeout", wantCode: output.ExitGeneral,
			wantDetail: "deadline exceeded", wantHint: "--timeout",
		},
		// Was: `_meta.error` = `Post "https://slack.com/api/conversations.list": context deadline exceeded`.
		// The HTTP client wraps the deadline, so a string compare would miss it.
		{
			name: "deadline wrapped by the http client",
			err:  &url.Error{Op: "Post", URL: "https://slack.com/api/conversations.list", Err: context.DeadlineExceeded},
			// The URL in that text is why the code cannot just be the text.
			wantErr: "timeout", wantCode: output.ExitGeneral,
			wantDetail: "deadline exceeded", wantHint: "--timeout",
		},
		// Was: `_meta.error` = "slack server error: 500 Internal Server Error".
		{
			name: "non-200 that is not 429",
			err:  slack.StatusCodeError{Code: 500, Status: "500 Internal Server Error"},
			// The status is the whole payload of this failure, so it has to
			// survive in the detail.
			wantErr: "http_error", wantCode: output.ExitGeneral, wantDetail: "500",
		},
		// Was: `_meta.error` = "invalid character '<' looking for beginning of value".
		{
			name: "html body", err: syntaxError(t),
			wantErr: "parse_error", wantCode: output.ExitGeneral,
			wantDetail: "invalid character", wantHint: "captive portal",
		},
		// Was: `_meta.error` = "json: cannot unmarshal string into Go struct
		// field .channels of type []slack.Channel" - Go type names in a field
		// a consumer is told to switch on.
		{
			name: "wrong json types", err: unmarshalTypeError(t),
			wantErr: "parse_error", wantCode: output.ExitGeneral,
			wantDetail: "cannot unmarshal", wantHint: "captive portal",
		},
		{
			name: "unrecognized", err: errors.New("something no one anticipated"),
			wantErr: "unknown_error", wantCode: output.ExitGeneral,
			wantDetail: "something no one anticipated",
		},

		// Cases classified before the fallback. Pinned here so adding a
		// recognizer above can't shadow one of them.
		{
			name: "rate limited", err: &slack.RateLimitedError{},
			wantErr: "rate_limited", wantCode: output.ExitRateLimit, wantDetail: "retry after",
		},
		{
			name: "rate limit budget exhausted",
			err:  &RateLimitExhaustedError{Err: &slack.RateLimitedError{}, Endpoint: "conversations.list", Attempts: 5},
			// A *slack.RateLimitedError is reachable through Unwrap, so the
			// bare-rate-limit branch must not claim this one first and lose
			// the endpoint and attempt count.
			wantErr: "rate_limited", wantCode: output.ExitRateLimit, wantDetail: "conversations.list",
		},
		{
			name: "auth", err: slack.SlackErrorResponse{Err: "invalid_auth"},
			wantErr: "invalid_auth", wantCode: output.ExitAuth, wantDetail: "invalid_auth",
		},
		{
			name: "rejected cursor", err: slack.SlackErrorResponse{Err: "invalid_cursor"},
			wantErr: "invalid_cursor", wantCode: output.ExitGeneral,
			wantDetail: "cursor", wantHint: "--cursor",
		},
		{
			name: "other slack error", err: slack.SlackErrorResponse{Err: "missing_scope"},
			wantErr: "missing_scope", wantCode: output.ExitGeneral, wantDetail: "missing_scope",
		},
		{
			name: "connection refused",
			err:  &net.OpError{Op: "dial", Err: errors.New("connect: connection refused")},
			wantErr: "network_error", wantCode: output.ExitNetwork, wantDetail: "connection refused",
		},
	}
}

// stableCodePattern is the shape every _meta.error value must have: a
// snake_case identifier a consumer can switch on. Raw Go error text fails it on
// the space alone.
var stableCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// _meta.error is a machine code, so ClassifyError must never put free text in
// it - not for the failures it recognizes and not for the ones it does not.
// This is the invariant the fallback used to break; it holds for any future
// error shape, which enumerating cases one at a time cannot promise.
func TestClassifyError_ErrIsAlwaysAStableCode(t *testing.T) {
	for _, tt := range classifyCorpus(t) {
		t.Run(tt.name, func(t *testing.T) {
			cliErr := ClassifyError(tt.err)
			if !stableCodePattern.MatchString(cliErr.Err) {
				t.Errorf("error code %q is not a stable snake_case identifier; raw Go text must go in detail, not the code (source error: %v)", cliErr.Err, tt.err)
			}
		})
	}
}

// The code names the failure, the detail carries the specifics, and the hint
// names the recovery - so replacing raw text with a code does not drop what the
// text said. Meta has no detail field; stderr is where the specifics land.
func TestClassifyError_CodesAndDetails(t *testing.T) {
	for _, tt := range classifyCorpus(t) {
		t.Run(tt.name, func(t *testing.T) {
			cliErr := ClassifyError(tt.err)
			if cliErr.Err != tt.wantErr {
				t.Errorf("got err=%q, want %q", cliErr.Err, tt.wantErr)
			}
			if cliErr.Code != tt.wantCode {
				t.Errorf("got code=%d, want %d", cliErr.Code, tt.wantCode)
			}
			if !strings.Contains(strings.ToLower(cliErr.Detail), strings.ToLower(tt.wantDetail)) {
				t.Errorf("detail %q must carry %q so the specifics the code omits are still reported", cliErr.Detail, tt.wantDetail)
			}
			switch {
			case tt.wantHint == "" && cliErr.Hint != "":
				t.Errorf("hint %q is unasserted; add it to the corpus case so a regression in it is caught", cliErr.Hint)
			case !strings.Contains(strings.ToLower(cliErr.Hint), strings.ToLower(tt.wantHint)):
				t.Errorf("hint %q must carry %q so the caller is told what to change", cliErr.Hint, tt.wantHint)
			}
		})
	}
}

// A deadline that fires during the dial is reported by net as an i/o timeout,
// not as context.DeadlineExceeded, so it stays a network error. Pinned because
// the deadline check sits ahead of the net.OpError check and must not swallow
// transport failures on its way past.
func TestClassifyError_NetworkTimeoutIsNotAContextTimeout(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://slack.com/api/conversations.list", Err: &net.OpError{
		Op:  "dial",
		Err: errors.New("i/o timeout"),
	}}
	cliErr := ClassifyError(err)
	if cliErr.Err != "network_error" || cliErr.Code != output.ExitNetwork {
		t.Errorf("got err=%q code=%d, want network_error/%d", cliErr.Err, cliErr.Code, output.ExitNetwork)
	}
}
