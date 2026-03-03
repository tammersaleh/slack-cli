package api

import (
	"errors"
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

// ClassifyError maps a Slack API error to an output.Error with the
// appropriate exit code.
func ClassifyError(err error) *output.Error {
	var rateLimitErr *slack.RateLimitedError
	if errors.As(err, &rateLimitErr) {
		return &output.Error{
			Err:  "rate_limited",
			Detail: "Rate limited after maximum retries",
			Code: output.ExitRateLimit,
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
