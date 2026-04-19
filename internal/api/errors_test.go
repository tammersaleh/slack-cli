package api

import (
	"errors"
	"net"
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
	err := &RateLimitExhaustedError{Err: inner, Endpoint: "conversations.list", Retries: 5}
	cliErr := ClassifyError(err)
	if cliErr.Code != output.ExitRateLimit {
		t.Errorf("got code=%d, want %d", cliErr.Code, output.ExitRateLimit)
	}
	if cliErr.Endpoint != "conversations.list" {
		t.Errorf("got endpoint=%q, want %q", cliErr.Endpoint, "conversations.list")
	}
	if cliErr.Detail != "Rate limited after 5 retries on conversations.list" {
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
