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
