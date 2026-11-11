package resolve

import (
	"errors"
	"fmt"

	"github.com/tammersaleh/slack-cli/internal/slackurl"
)

// ErrBadURL indicates the input was an http(s) URL but not a usable Slack
// reference of the kind the resolver needs (malformed, wrong host, or
// wrong-kind). Call sites map this to invalid_input rather than not_found, so
// "you pasted a file link where a user goes" doesn't masquerade as "user not
// found".
var ErrBadURL = errors.New("invalid Slack URL")

// ErrNotFound is a clean miss: the lookup ran to completion and nothing
// matches the input. It is the only resolver error that means "no such
// channel" or "no such user". Any other error says the lookup could not find
// out (auth, a rate limit, the network, --timeout) and comes back as the
// underlying call returned it, for the call site to classify.
var ErrNotFound = errors.New("not found")

// channelIDFromURL extracts a channel ID from a Slack URL. handled=true means
// input was URL-shaped and fully accounted for here (the caller must not fall
// through to name resolution); a non-nil error then wraps ErrBadURL.
func channelIDFromURL(input string) (id string, handled bool, err error) {
	ref, matched, perr := slackurl.Parse(input)
	if !matched {
		return "", false, nil
	}
	if perr != nil {
		return "", true, fmt.Errorf("%w: %v", ErrBadURL, perr)
	}
	switch ref.Kind {
	case slackurl.KindChannel, slackurl.KindMessage:
		return ref.ChannelID, true, nil
	default:
		return "", true, fmt.Errorf("%w: %s URL does not identify a channel", ErrBadURL, ref.Kind)
	}
}

// userIDFromURL extracts a user ID from a Slack URL. A file URL embeds the
// uploader's user ID but identifies a file, so only KindUser is accepted.
func userIDFromURL(input string) (id string, handled bool, err error) {
	ref, matched, perr := slackurl.Parse(input)
	if !matched {
		return "", false, nil
	}
	if perr != nil {
		return "", true, fmt.Errorf("%w: %v", ErrBadURL, perr)
	}
	if ref.Kind == slackurl.KindUser {
		return ref.UserID, true, nil
	}
	return "", true, fmt.Errorf("%w: %s URL does not identify a user", ErrBadURL, ref.Kind)
}
