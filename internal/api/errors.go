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
				Err:  slackErr.Err,
				Detail: slackErr.Error(),
				Code: output.ExitAuth,
			}
		}
		return &output.Error{
			Err:  slackErr.Err,
			Detail: slackErr.Error(),
			Code: output.ExitGeneral,
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
