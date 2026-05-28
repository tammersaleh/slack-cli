package cmd

import (
	"fmt"

	"github.com/tammersaleh/slack-cli/internal/slackurl"
)

// msgRef is one message reference for a ts-shaped command. channel is raw (an
// ID, a name, or a channel id extracted from a URL) and must be passed through
// ResolveChannel by the caller. input is the original positional argument,
// echoed back in the output `input` field.
type msgRef struct {
	channel string
	ts      string
	input   string
}

// messageURLRef reports whether arg is a self-contained message permalink and,
// if so, returns its parsed ref. A channel/user/file URL, a non-URL, or a
// malformed URL all return ok=false.
func messageURLRef(arg string) (slackurl.Ref, bool) {
	ref, matched, err := slackurl.Parse(arg)
	if !matched || err != nil {
		return slackurl.Ref{}, false
	}
	if ref.Kind != slackurl.KindMessage || ref.TS == "" {
		return slackurl.Ref{}, false
	}
	return ref, true
}

// parseMessageRefs interprets the positional args of a multi-message command
// (message get, reaction list) as one of two grammars:
//
//	channel mode: <channel-like> <bare-ts>...   (>= 2 args)
//	URL mode:     <message-url>...              (each a message permalink)
//
// The first positional being a message permalink selects URL mode, in which
// every argument must also be a message permalink - so a list may span
// channels. Mixing the forms, or passing no usable refs, is an error. The
// whole argv is validated here before the caller touches the API.
func parseMessageRefs(args []string) ([]msgRef, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("need a channel and at least one timestamp, or one or more message URLs")
	}

	if ref, ok := messageURLRef(args[0]); ok {
		refs := []msgRef{{channel: ref.ChannelID, ts: ref.TS, input: args[0]}}
		for _, a := range args[1:] {
			r, ok := messageURLRef(a)
			if !ok {
				return nil, fmt.Errorf("cannot mix message URLs with other arguments: %q", a)
			}
			refs = append(refs, msgRef{channel: r.ChannelID, ts: r.TS, input: a})
		}
		return refs, nil
	}

	// Channel mode: args[0] is channel-like, args[1:] are bare timestamps.
	if len(args) < 2 {
		return nil, fmt.Errorf("need a channel and at least one timestamp, or one or more message URLs")
	}
	channel := args[0]
	refs := make([]msgRef, 0, len(args)-1)
	for _, ts := range args[1:] {
		if err := rejectURL(ts); err != nil {
			return nil, err
		}
		refs = append(refs, msgRef{channel: channel, ts: ts, input: ts})
	}
	return refs, nil
}

// parseThreadRef interprets the positional args of thread list, which targets a
// single parent message:
//
//	channel mode: <channel-like> <bare-ts>
//	URL mode:     <message-url>           (a reply link resolves to its parent)
//
// In URL mode the parent ts is the link's thread_ts when present, else its own
// ts, so pasting any message in a thread yields the whole thread.
func parseThreadRef(args []string) (msgRef, error) {
	if len(args) == 0 {
		return msgRef{}, fmt.Errorf("need a channel and a timestamp, or a message URL")
	}

	if ref, ok := messageURLRef(args[0]); ok {
		if len(args) != 1 {
			return msgRef{}, fmt.Errorf("a message URL takes no further arguments")
		}
		ts := ref.TS
		if ref.ThreadTS != "" {
			ts = ref.ThreadTS
		}
		return msgRef{channel: ref.ChannelID, ts: ts, input: args[0]}, nil
	}

	if len(args) != 2 {
		return msgRef{}, fmt.Errorf("need exactly a channel and a timestamp, or a single message URL")
	}
	if err := rejectURL(args[1]); err != nil {
		return msgRef{}, err
	}
	return msgRef{channel: args[0], ts: args[1], input: args[1]}, nil
}

// rejectURL errors if s is URL-shaped, used to forbid a URL in a bare-ts slot.
func rejectURL(s string) error {
	if _, matched, _ := slackurl.Parse(s); matched {
		return fmt.Errorf("cannot mix a URL with channel + timestamp arguments: %q", s)
	}
	return nil
}
