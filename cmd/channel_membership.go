package cmd

import (
	"context"
	"fmt"

	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// Channel membership writes: `channel invite` and `channel kick`. Both take
// mixed channel and user recipients, resolve everything before the first
// write, then make one API call per (channel, user) pair and emit one row per
// pair. The two differ only in endpoint, status words, and how Slack's error
// codes are classified, so they share membershipOp and runMembership.

const membershipHelp = `Recipients are classified by shape: @name, an email, a U…/W… id, or a
user-profile URL is a person; everything else (#name, bare name, C… id,
channel URL) is a channel. At least one of each is required; order does not
matter.

Every recipient resolves before the first write. A typo aborts the whole
command with no rows and no changes. One API call per (channel, user) pair,
in input order; one row per pair, emitted as it completes:

  {"channel_id":"C01ABC","user_id":"U01XYZ","status":"%s"}

A pair that already has the requested state reports "%s" as its status
and exits 0, so re-running the same command is safe. A Slack error about
one pair, or about one channel (reported for every remaining user in it),
is an "error" row; the run continues and exits 1. Anything systemic -
auth, missing_scope, no_permission, rate-limit exhaustion, an unknown
code - aborts the run, and the trailer reads has_more:true with the code.
Rerun the same command to finish.

Examples:

  slack channel %s @alice '#eng-a' '#eng-b'    # one person, several channels
  slack channel %s '#eng-a' @alice @bob         # several people, one channel`

type ChannelInviteCmd struct {
	Recipients []string `arg:"" required:"" name:"recipient" help:"Channels (#name, id, URL) and users (@name, email, id, profile URL), at least one of each."`
}

func (ChannelInviteCmd) Help() string {
	return sprintfMembershipHelp("invited", "already_in_channel", "invite")
}

func (c *ChannelInviteCmd) Run(cli *CLI) error {
	return runMembership(cli, c.Recipients, inviteOp)
}

type ChannelKickCmd struct {
	Recipients []string `arg:"" required:"" name:"recipient" help:"Channels (#name, id, URL) and users (@name, email, id, profile URL), at least one of each."`
}

func (ChannelKickCmd) Help() string {
	return sprintfMembershipHelp("kicked", "not_in_channel", "kick")
}

func (c *ChannelKickCmd) Run(cli *CLI) error {
	return runMembership(cli, c.Recipients, kickOp)
}

// membershipOp is one membership write and its error dispositions.
//
// Dispositions are explicit allowlists with a fail-closed default. A code in
// pairErrors describes one user in one channel; a code in channelErrors
// describes the channel, so every remaining user in it gets the same row with
// no further calls. Everything else, including codes Slack has not invented
// yet, aborts the run: treating an unknown code as pair-local is how a
// systemic failure gets multiplied across every pair.
type membershipOp struct {
	endpoint string
	// done is the status for a performed write; idempotent is the Slack code
	// reported as a status (not an error) because the pair already has the
	// requested state.
	done, idempotent string
	pairErrors       map[string]bool
	channelErrors    map[string]bool
	call             func(ctx context.Context, client *api.Client, channelID, userID string) error
}

// Codes confirmed against the conversations.invite method docs 2026-10-19,
// plus cant_invite_self and user_not_found observed live. not_in_channel
// means the caller here.
var inviteOp = membershipOp{
	endpoint:   "conversations.invite",
	done:       "invited",
	idempotent: "already_in_channel",
	pairErrors: set(
		"cant_invite_self", "cant_invite", "user_is_restricted", "ura_max_channels",
		"user_not_found", "invitee_cant_see_channel", "org_user_not_in_team",
		"no_external_invite_permission", "app_cannot_join_channel",
	),
	channelErrors: set("not_in_channel", "is_archived", "channel_not_found", "method_not_supported_for_channel_type"),
	call: func(ctx context.Context, client *api.Client, channelID, userID string) error {
		_, err := client.Bot().InviteUsersToConversationContext(ctx, channelID, userID)
		return err
	},
}

// Codes confirmed against the conversations.kick method docs 2026-10-19, plus
// cant_kick_self observed live. not_in_channel means the target user here, so
// it is the idempotent outcome. restricted_action (workspace policy) and
// no_permission (the caller) are deliberately absent: both abort.
var kickOp = membershipOp{
	endpoint:      "conversations.kick",
	done:          "kicked",
	idempotent:    "not_in_channel",
	pairErrors:    set("cant_kick_self", "user_not_found", "not_supported"),
	channelErrors: set("channel_not_found", "cant_kick_from_general", "method_not_supported_for_channel_type"),
	call: func(ctx context.Context, client *api.Client, channelID, userID string) error {
		return client.Bot().KickUserFromConversationContext(ctx, channelID, userID)
	},
}

func set(codes ...string) map[string]bool {
	m := make(map[string]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m
}

func runMembership(cli *CLI, recipients []string, op membershipOp) error {
	channelInputs, userInputs, err := splitRecipients(recipients)
	if err != nil {
		return err
	}
	if len(channelInputs) == 0 || len(userInputs) == 0 {
		return &output.Error{
			Err:    "invalid_input",
			Detail: "need at least one channel and one user; bare names are channels, a person is @name, an email, or a U… id",
			Code:   output.ExitGeneral,
		}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}
	ctx, cancel := cli.Context()
	defer cancel()
	r := cli.NewResolver(client)

	// Resolve everything first. A write command must not do half the work
	// and then fail on a typo, so any miss aborts before the first call.
	channels, err := resolveAll(ctx, channelInputs, r.ResolveChannel, channelResolveError)
	if err != nil {
		return err
	}
	users, err := resolveAll(ctx, userInputs, r.ResolveUser, userResolveError)
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	errorCount := 0
	row := func(channelID, userID string) map[string]any {
		return map[string]any{"channel_id": channelID, "user_id": userID}
	}
	errorRow := func(channelID, userID string, oErr *output.Error) error {
		errorCount++
		m := row(channelID, userID)
		m["error"] = oErr.Err
		if oErr.Detail != "" {
			m["detail"] = oErr.Detail
		}
		if oErr.Hint != "" {
			m["hint"] = oErr.Hint
		}
		return p.PrintItem(m)
	}

	for _, channelID := range channels {
	users:
		for i, userID := range users {
			callErr := api.Call(ctx, op.endpoint, func() error {
				return op.call(ctx, client, channelID, userID)
			})
			if callErr == nil {
				m := row(channelID, userID)
				m["status"] = op.done
				if err := p.PrintItem(m); err != nil {
					return err
				}
				continue
			}

			code, isSlack := api.SlackErrorCode(callErr)
			switch {
			case isSlack && code == op.idempotent:
				m := row(channelID, userID)
				m["status"] = code
				if err := p.PrintItem(m); err != nil {
					return err
				}
			case isSlack && op.pairErrors[code]:
				if err := errorRow(channelID, userID, cli.ClassifyError(callErr)); err != nil {
					return err
				}
			case isSlack && op.channelErrors[code]:
				// The channel itself is the problem: report it for every
				// user still to come and move on without calling again.
				oErr := cli.ClassifyError(callErr)
				for _, remaining := range users[i:] {
					if err := errorRow(channelID, remaining, oErr); err != nil {
						return err
					}
				}
				break users
			default:
				// Fatal. The rows above are authoritative; has_more:true says
				// some requested pairs have no row, and there is no cursor
				// because the recovery is to rerun the (idempotent) command.
				oErr := cli.ClassifyError(callErr)
				if err := p.PrintMeta(output.Meta{HasMore: true, Error: oErr.Err, ErrorCount: errorCount}); err != nil {
					return err
				}
				return oErr
			}
		}
	}

	if err := p.PrintMeta(output.Meta{ErrorCount: errorCount}); err != nil {
		return err
	}
	if errorCount > 0 {
		return &output.ExitError{Code: output.ExitGeneral}
	}
	return nil
}

// resolveAll resolves every input in order, deduping by resolved id with
// first-seen order, and returns the first failure mapped through toErr.
func resolveAll(ctx context.Context, inputs []string, resolveFn func(context.Context, string) (string, error), toErr func(string, error) *output.Error) ([]string, error) {
	ids := make([]string, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		id, err := resolveFn(ctx, in)
		if err != nil {
			return nil, toErr(in, err)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func sprintfMembershipHelp(done, idempotent, verb string) string {
	return fmt.Sprintf(membershipHelp, done, idempotent, verb, verb)
}
