package cmd

import (
	"context"
	"errors"

	"github.com/tammersaleh/slack-cli/internal/output"
	"github.com/tammersaleh/slack-cli/internal/resolve"
)

// channelResolveError maps a ResolveChannel failure to an output error. A bad
// Slack URL is invalid_input and a clean miss is channel_not_found; both are
// verdicts on the input. Anything else means the lookup could not find out,
// and is classified like any other API failure, so a bad token exits 2 and an
// exhausted rate limit exits 3 instead of reading as an absent channel.
// Return it directly for single-shot commands, or call .AsItem() for per-item
// rows after isInputResolveErr.
//
// A two-client command must have cli.authMethod name the credential the
// resolver ran on when it calls this, or the re-auth hint names the wrong one.
func (c *CLI) channelResolveError(input string, err error) *output.Error {
	switch {
	case errors.Is(err, resolve.ErrBadURL):
		return output.InvalidURL(input, err.Error())
	case errors.Is(err, resolve.ErrNotFound):
		return output.ChannelNotFound(input)
	}
	return c.ClassifyError(err)
}

// isInputResolveErr reports whether a resolver error is a verdict on the one
// input (a bad URL, a clean miss) rather than a failure to look it up. Only
// these may become a per-input row or a passed-through filter value; every
// other error, including one nobody has enumerated, stops the command.
func isInputResolveErr(err error) bool {
	return errors.Is(err, resolve.ErrBadURL) || errors.Is(err, resolve.ErrNotFound)
}

// withAuthMethod runs fn with cli.authMethod set to method and restores the
// previous value. A two-client command uses it to classify a failure against
// the credential that made the call.
func (c *CLI) withAuthMethod(method string, fn func() error) error {
	prev := c.authMethod
	c.authMethod = method
	defer func() { c.authMethod = prev }()
	return fn()
}

// resolvedInput is one input of a multi-input command after the up-front
// resolution pass: its ID, or the error row for an input that names nothing.
type resolvedInput struct {
	input string
	id    string
	miss  *output.Error
}

// resolveInputs resolves every input of a multi-input command before the
// command prints anything, in input order. An input that names nothing keeps
// its place with a miss, which the command prints as that input's row. A
// lookup that failed for any other reason returns the classified error and no
// inputs: the command stops with no rows, rather than printing some inputs
// and one row per remaining input for the same rate limit.
func resolveInputs(ctx context.Context, inputs []string, resolveFn func(context.Context, string) (string, error), toErr func(string, error) *output.Error) ([]resolvedInput, *output.Error) {
	out := make([]resolvedInput, 0, len(inputs))
	for _, in := range inputs {
		id, err := resolveFn(ctx, in)
		switch {
		case err == nil:
			out = append(out, resolvedInput{input: in, id: id})
		case isInputResolveErr(err):
			out = append(out, resolvedInput{input: in, miss: toErr(in, err)})
		default:
			return nil, toErr(in, err)
		}
	}
	return out, nil
}

// userResolveError is the user-arg counterpart of channelResolveError: a bad
// URL is invalid_input, a clean miss is user_not_found, and a lookup that
// failed (the directory walk, users.lookupByEmail) keeps its own code and
// exit. A new verdict on the input gets a case here and in isInputResolveErr.
func (c *CLI) userResolveError(input string, err error) *output.Error {
	switch {
	case errors.Is(err, resolve.ErrBadURL):
		return output.InvalidURL(input, err.Error())
	case errors.Is(err, resolve.ErrNotFound):
		return output.UserNotFound(input)
	}
	return c.ClassifyError(err)
}

// resolveRefChannels resolves the distinct channels referenced by refs, once
// each, returning a raw-channel -> ID map. Any failure is fatal, mapped by
// channelResolveError. URL-mode refs already hold a channel ID, so
// ResolveChannel passes them through without an API call.
func resolveRefChannels(ctx context.Context, cli *CLI, r *resolve.Resolver, refs []msgRef) (map[string]string, *output.Error) {
	ids := make(map[string]string, len(refs))
	for _, ref := range refs {
		if _, ok := ids[ref.channel]; ok {
			continue
		}
		id, err := r.ResolveChannel(ctx, ref.channel)
		if err != nil {
			return nil, cli.channelResolveError(ref.channel, err)
		}
		ids[ref.channel] = id
	}
	return ids, nil
}
