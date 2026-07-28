package api

import (
	"context"
	"encoding/json"
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
//
// Err is a machine code throughout, never raw Go error text. It is what a
// paginated command copies into _meta.error, which SPEC documents as the field
// a consumer branches on to tell a truncated stream from a complete one, and
// _meta has no room for anything but the code. Go and slack-go wording is a
// dependency detail that changes under a toolchain bump, and it leaks
// implementation - request URLs and Go type names both showed up in that field
// before this was enforced. The specifics still get reported: every code below
// carries the original text in Detail, which the printer writes to stderr.
//
// The final fallback is a code too, so an error shape nothing here recognizes
// cannot reintroduce free text. That is the point of the arrangement: the
// recognizers exist to name failures worth branching on, not to keep the field
// clean, so a shape added by a future dependency is already contained.
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

	// The caller's own --timeout expiring. Two wrappings reach here and both
	// have to match, which is why this is errors.Is and not a string compare:
	// the HTTP client returns a *url.Error around the deadline when it fires
	// mid-request, and FetchPage's pre-flight ctx.Err() returns it bare when it
	// fires between pages.
	//
	// Exit stays ExitGeneral. ExitNetwork reads like the network failed, and a
	// self-imposed deadline says nothing about the network - the request may
	// have been perfectly healthy and merely slower than the caller allowed.
	// Moving it would also change a documented exit code for no gain.
	if errors.Is(err, context.DeadlineExceeded) {
		return &output.Error{
			Err:    "timeout",
			Detail: err.Error(),
			Hint:   "The --timeout budget expired. Raise it, drop it (no timeout is the default), or resume from the trailer's next_cursor.",
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

	// A non-200 that is not a 429; slack-go turns 429 into RateLimitedError
	// above. Not split by status class and not carrying the number in its own
	// field: Slack reports API-level failures as 200 with ok:false, so what
	// reaches here in practice is edge-level 5xx, and Detail already names the
	// status.
	var statusErr slack.StatusCodeError
	if errors.As(err, &statusErr) {
		return &output.Error{
			Err:    "http_error",
			Detail: err.Error(),
			Code:   output.ExitGeneral,
		}
	}

	// A 200 whose body is not the JSON slack-go expected. Reachable without
	// Slack changing anything: a captive portal or intercepting proxy answering
	// 200 with HTML lands here. parse_error is the code the CLI already uses
	// for an unparseable response body (cmd/saved.go), so this adds no new
	// vocabulary.
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return &output.Error{
			Err:    "parse_error",
			Detail: err.Error(),
			Hint:   "The response was not the JSON Slack's API returns. Check for a proxy or captive portal intercepting requests to slack.com.",
			Code:   output.ExitGeneral,
		}
	}

	// Unrecognized. The code says only that, and Detail carries the text, so a
	// consumer sees a value it can switch on and an operator still sees what
	// happened. Deliberately not "internal_error": that is a real Slack API
	// error string which passes through above, and two different failures must
	// not share one code.
	return &output.Error{
		Err:    "unknown_error",
		Detail: err.Error(),
		Code:   output.ExitGeneral,
	}
}
