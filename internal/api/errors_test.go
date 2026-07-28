package api

import (
	"errors"
	"fmt"
	"net"
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
