package api

import (
	"errors"
	"fmt"
	"net"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// authErrors are Slack API error strings that indicate authentication problems.
var authErrors = map[string]bool{
	"invalid_auth":    true,
	"token_revoked":   true,
	"not_authed":      true,
	"account_inactive": true,
	"token_expired":   true,
}

// nonResumableErrors are Slack API error strings that invalidate the
// continuation itself: the request as sent was malformed, its cursor was
// rejected, the method does not exist, or the target timestamp matches no
// message. Slack answers an identical retry identically, so a paginated
// command must not hand the caller a resume cursor
// for one of these - the documented contract reads that as "the named page
// never arrived, resume from here" and an automated consumer retries forever.
//
// Membership is the narrow question "is the continuation itself void", not the
// broader "is this error deterministic". Everything else defaults to resumable,
// including errors that are perfectly deterministic right now: a revoked token,
// a missing scope, a channel the caller has not joined, and an admin
// restriction all make the same page fetchable once the operator fixes them, so
// the checkpoint is worth keeping. A retained cursor is a checkpoint, not
// permission to retry blindly; if it has also expired by the time the caller
// resumes, Slack answers invalid_cursor and that failure is terminal here.
//
// Deliberately absent, and why: internal_error, service_unavailable,
// fatal_error, request_timeout and ratelimited can clear on their own; the
// authErrors above, missing_scope, no_permission, not_allowed_token_type,
// team_is_restricted, restricted_action and ekm_access_denied clear when
// credentials or policy change; not_in_channel and is_archived clear when
// membership or archival changes; channel_not_found and user_not_found can mean
// "not visible to this token" rather than "does not exist". Unknown future
// codes stay resumable too - wrongly discarding a usable checkpoint is the
// worse mistake.
var nonResumableErrors = map[string]bool{
	// The cursor itself was rejected. Reporting it back as the resume point
	// is self-contradictory; this is the case the set exists for.
	"invalid_cursor": true,

	// Request shape. Nothing about waiting or re-authenticating changes the
	// bytes that were sent.
	"invalid_arguments": true,
	"invalid_arg_name":  true,
	"invalid_array_arg": true,
	"invalid_charset":   true,
	"invalid_form_data": true,
	"invalid_post_type": true,
	"missing_post_type": true,
	"missing_argument":  true,
	"invalid_limit":     true,
	"invalid_types":     true,
	"invalid_query":     true, // search.messages, search.files
	"invalid_ts_latest": true, // conversations.history --before
	"invalid_ts_oldest": true, // conversations.history --after

	// The one target error in the set, and it is here for consistency rather
	// than determinism: the CLI already declares this outcome terminal when it
	// synthesizes it itself (conversations.replies answering ok:true with no
	// messages, see output.ThreadNotFoundNoMessage), and SPEC names
	// thread_not_found as the canonical has_more:false example. Slack raises
	// its own thread_not_found for a ts that matches no message (verified live
	// 2026-07-27), and the same outcome must not be terminal on one path and
	// resumable on the other. Unlike channel_not_found this carries no
	// visibility ambiguity - a caller who cannot see the conversation gets
	// channel_not_found or not_in_channel instead.
	"thread_not_found": true,

	// The method is gone. This binary cannot continue the stream at all.
	"unknown_method":    true,
	"method_deprecated": true,
}

// slackErrorCode extracts Slack's error string from err. It is the single place
// that recognizes a Slack API error, so exit-code classification and cursor
// disposition - two independent policies - can never disagree about whether a
// given error came from Slack.
func slackErrorCode(err error) (string, bool) {
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		return slackErr.Err, true
	}
	return "", false
}

// IsNonResumablePageError reports whether err voids the continuation, meaning a
// paginated command must not report a resume cursor for the page that failed.
// Only a typed Slack API error can qualify: a local error that happens to spell
// one of the strings must not become terminal.
func IsNonResumablePageError(err error) bool {
	code, ok := slackErrorCode(err)
	return ok && nonResumableErrors[code]
}

// RateLimitExhaustedError is returned when a page exhausts its retry budget.
// Attempts counts every request made for that page, including the first, so
// the number of waits is Attempts-1.
type RateLimitExhaustedError struct {
	Err      error
	Endpoint string
	Attempts int
}

func (e *RateLimitExhaustedError) Error() string {
	return fmt.Sprintf("rate limited after %d attempts on %s", e.Attempts, e.Endpoint)
}

func (e *RateLimitExhaustedError) Unwrap() error { return e.Err }

// ClassifyError maps a Slack API error to an output.Error with the
// appropriate exit code.
func ClassifyError(err error) *output.Error {
	var rlExhausted *RateLimitExhaustedError
	if errors.As(err, &rlExhausted) {
		return &output.Error{
			Err:      "rate_limited",
			Detail:   fmt.Sprintf("Rate limited after %d attempts on %s", rlExhausted.Attempts, rlExhausted.Endpoint),
			Endpoint: rlExhausted.Endpoint,
			Code:     output.ExitRateLimit,
		}
	}

	// A bare rate-limit error comes from a single un-retried call, not from
	// an exhausted retry budget - don't claim retries that never happened.
	var rateLimitErr *slack.RateLimitedError
	if errors.As(err, &rateLimitErr) {
		return &output.Error{
			Err:    "rate_limited",
			Detail: fmt.Sprintf("Rate limited; Slack asked to retry after %s", rateLimitErr.RetryAfter),
			Code:   output.ExitRateLimit,
		}
	}

	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		if authErrors[slackErr.Err] {
			return &output.Error{
				Err:    slackErr.Err,
				Detail: slackErr.Error(),
				Code:   output.ExitAuth,
			}
		}
		// The bare string "invalid_cursor" does not tell a caller that the
		// fix is to drop --cursor and start over, so this one gets a real
		// detail and a recovery hint.
		if slackErr.Err == "invalid_cursor" {
			return output.InvalidCursor()
		}
		return &output.Error{
			Err:    slackErr.Err,
			Detail: slackErr.Error(),
			Code:   output.ExitGeneral,
		}
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return &output.Error{
			Err:    "network_error",
			Detail: err.Error(),
			Code:   output.ExitNetwork,
		}
	}

	return &output.Error{
		Err:  err.Error(),
		Code: output.ExitGeneral,
	}
}
