package cmd

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

const channelNameRule = "Channel names are at most 80 characters of lowercase letters, digits, hyphens, and underscores, with at least one letter or digit. Slack does not lowercase the name for you."

type ChannelCreateCmd struct {
	Name    string `arg:"" required:"" help:"Channel name. One leading '#' is dropped; Slack validates the rest."`
	Private bool   `help:"Create a private channel. The default is public."`
}

func (ChannelCreateCmd) Help() string {
	return `Creates one channel and prints it as Slack returned it, in the 'channel info'
row shape, followed by the trailer:

  {"id":"C01ABC","name":"eng-a","is_private":true,"is_member":true,...}

You are the only member. Add people with 'slack channel invite'. There is no
topic, purpose, or invite flag.

` + channelNameRule + `
The name is sent as given and Slack rejects what it does not accept
(invalid_name_specials for uppercase or spaces, invalid_name_maxlength,
invalid_name_punctuation, invalid_name_required). A name any channel already
holds - public, private, or archived - is name_taken.

The request is sent exactly once. A rate limit is not retried, because a
repeat of a create that did run answers name_taken: check with
'slack channel info' before running the command again.

On failure nothing is printed to stdout. No channel was created when Slack
answered with an error code of its own. After rate_limited, timeout,
network_error, http_error, parse_error, or unknown_error it is unknown.

Examples:

  slack channel create eng-a
  slack channel create '#eng-leads' --private`
}

func (c *ChannelCreateCmd) Run(cli *CLI) error {
	// The workspace client, like every other channel command. On Enterprise
	// Grid the org token answers cannot_create_channel unless the request
	// names a team.
	ctx, cancel := cli.Context()
	defer cancel()
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	// '#name' is how a channel is written everywhere else in this CLI. The
	// rest of the name goes to Slack untouched: it is the validator, and
	// rewriting the name locally would create a channel the caller did not
	// ask for.
	name := strings.TrimPrefix(c.Name, "#")

	var ch *slack.Channel
	sent := false
	callErr := api.CallOnce(ctx, "conversations.create", func() error {
		sent = true
		var err error
		ch, err = client.Bot().CreateConversationContext(ctx, slack.CreateConversationParams{
			ChannelName: name,
			IsPrivate:   c.Private,
		})
		return err
	})
	if callErr != nil {
		oErr := cli.ClassifyError(callErr)
		switch {
		case !sent:
			// --timeout expired before the request: nothing to add, and
			// nothing was created.
		case unknownCreateOutcome[oErr.Err]:
			// Keep the classifier's own advice (look for a proxy) behind the
			// one thing that matters first. Its timeout hint offers a resume
			// cursor, which a create does not have.
			if oErr.Err == "timeout" {
				oErr.Hint = "The --timeout budget expired. Raise it or drop it (no timeout is the default)."
			}
			oErr.Hint = strings.TrimSpace(fmt.Sprintf("The request was sent once and not retried, so the channel may or may not exist. Run %s before running this again; a repeat of a create that did run answers name_taken. %s", channelInfoLookup(name), oErr.Hint))
		default:
			if hint := channelCreateHint(oErr.Err, name); hint != "" {
				oErr.Hint = hint
			}
		}
		return oErr
	}

	// The row is the channel Slack stored, never the request.
	m := channelToMap(*ch)
	// conversations.create sends no member count, so the decoded 0 is not a
	// value Slack reported.
	delete(m, "num_members")

	p := cli.NewPrinter()
	if err := p.PrintItem(m); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// unknownCreateOutcome are the classified codes that do not say whether
// Slack ran the create: the answer never arrived or could not be read.
var unknownCreateOutcome = map[string]bool{
	"rate_limited":  true,
	"timeout":       true,
	"network_error": true,
	"http_error":    true,
	"parse_error":   true,
	"unknown_error": true,
}

func channelInfoLookup(name string) string {
	return fmt.Sprintf("slack channel info '#%s'", name)
}

// channelCreateHint returns the recovery hint for a conversations.create
// failure where Slack answered with its own code, or "" to keep whatever the
// classifier attached (the auth and missing_scope hints) or nothing for a
// code not listed here.
func channelCreateHint(code, name string) string {
	switch code {
	case "name_taken":
		return fmt.Sprintf("A channel already has that name. Public, private, and archived channels share one namespace. Run %s to see it; an archived channel, or a private one you are not in, does not resolve by name. Pick another name.", channelInfoLookup(name))
	case "invalid_name", "invalid_name_required", "invalid_name_maxlength", "invalid_name_specials", "invalid_name_punctuation":
		return channelNameRule
	case "restricted_action":
		return "Workspace policy does not let this account create this kind of channel. If the other kind is acceptable, try with or without --private; otherwise ask a workspace admin."
	case "user_is_restricted", "user_is_ultra_restricted":
		return "This account is a guest, and guest accounts cannot create channels."
	case "no_permission", "not_allowed_token_type", "team_access_not_granted", "access_denied":
		return "This credential does not have permission to create a channel in this workspace. Logging in again does not change that; ask a workspace admin."
	case "cannot_create_channel":
		return "Slack answers this when an Enterprise Grid org (E…) token creates a channel without naming a workspace. Select the workspace (T…) credential with SLACK_WORKSPACE or --workspace."
	}
	return ""
}
