package cmd

import (
	"strings"

	"github.com/tammersaleh/slack-cli/internal/output"
	"github.com/tammersaleh/slack-cli/internal/resolve"
	"github.com/tammersaleh/slack-cli/internal/slackurl"
)

// Recipient classification shared by draft create and channel invite/kick:
// which positional args name a channel and which name a person.

type recipientKind int

const (
	recipientChannel recipientKind = iota
	recipientUser
)

// classifyRecipient decides, syntactically, whether a recipient arg
// names a channel or a user. No network calls. User-shaped: @name, an email,
// a Uxxx/Wxxx id, or a user-profile URL. Everything else (#name, bare name,
// C/D/G/M id, channel/message URL) is a channel; bare names stay channels for
// backward compatibility, so a person must be given as @name, email, or id.
func classifyRecipient(s string) recipientKind {
	if ref, ok, _ := slackurl.Parse(s); ok {
		if ref.Kind == slackurl.KindUser {
			return recipientUser
		}
		return recipientChannel
	}
	// An "@" means a @name handle (leading) or an email (interior); channel
	// names can't contain one, and a URL with an "@" was handled above.
	if strings.Contains(s, "@") {
		return recipientUser
	}
	if resolve.IsUserID(s) {
		return recipientUser
	}
	return recipientChannel
}

// splitRecipients classifies recipient args into channel and user inputs.
// Pure - no network calls - so a caller can pick the resolver's client before
// any resolution happens. A malformed URL surfaces its own precise reason
// here, ahead of the caller's mix/arity checks - otherwise it gets bucketed as
// a channel and reported as "cannot mix" or "no channel", hiding the actual
// problem. (A well-formed but wrong-kind URL, e.g. a file link, still
// classifies as a channel and is rejected by ResolveChannel as invalid_input.)
func splitRecipients(args []string) (channels, users []string, err error) {
	for _, rec := range args {
		if _, matched, perr := slackurl.Parse(rec); matched && perr != nil {
			return nil, nil, output.InvalidURL(rec, perr.Error())
		}
		switch classifyRecipient(rec) {
		case recipientUser:
			users = append(users, rec)
		default:
			channels = append(channels, rec)
		}
	}
	return channels, users, nil
}
