package cmd

import (
	"errors"

	"github.com/tammersaleh/slack-cli/internal/output"
	"github.com/tammersaleh/slack-cli/internal/resolve"
)

// channelResolveError maps a ResolveChannel failure to an output error: a bad
// Slack URL becomes invalid_input; anything else is channel_not_found. Return
// it directly for single-shot commands, or call .AsItem() for per-item rows.
func channelResolveError(input string, err error) *output.Error {
	if errors.Is(err, resolve.ErrBadURL) {
		return output.InvalidURL(input, err.Error())
	}
	return output.ChannelNotFound(input)
}

// userResolveError is the user-arg counterpart of channelResolveError.
func userResolveError(input string, err error) *output.Error {
	if errors.Is(err, resolve.ErrBadURL) {
		return output.InvalidURL(input, err.Error())
	}
	return output.UserNotFound(input)
}

// isBadURLErr reports whether err is a bad-Slack-URL failure. Lets lenient
// call sites (e.g. file list filters) keep passing unresolved names through
// while still rejecting malformed/wrong-kind URLs.
func isBadURLErr(err error) bool {
	return errors.Is(err, resolve.ErrBadURL)
}
