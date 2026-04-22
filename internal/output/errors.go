package output

import "fmt"

// Helpers for common recoverable errors. Each returns an *Error whose Hint
// field names the concrete command an agent can run to recover: a naming
// lookup for "not found" cases, the valid format list for parse failures,
// a minimal example for missing required input. Call sites should use
// these instead of constructing *Error inline so the recovery guidance
// stays consistent across commands.

// AsItem returns the Error as a flat map suitable for per-item JSONL
// emission from bulk commands (e.g. `channel info X Y Z`). Pulls Input
// straight from the Error so callers don't re-pass context they already
// handed the constructor. Fields missing on the Error are omitted. Use
// this so per-item failures carry the same recovery hint as single-shot
// errors do on stderr.
func (e *Error) AsItem() map[string]any {
	m := map[string]any{"error": e.Err}
	if e.Input != "" {
		m["input"] = e.Input
	}
	if e.Detail != "" {
		m["detail"] = e.Detail
	}
	if e.Hint != "" {
		m["hint"] = e.Hint
	}
	return m
}

// ChannelNotFound is returned when a channel name or ID can't be resolved.
// Covers name lookups, #prefixed names, and DM channel IDs.
func ChannelNotFound(input string) *Error {
	return &Error{
		Err:    "channel_not_found",
		Detail: fmt.Sprintf("No channel matching '%s'", input),
		Hint:   fmt.Sprintf("Run 'slack channel list --query %s' to find public/private channels by name, or add '--include-non-member' to see channels you haven't joined. For DMs, list with '--type im' and match on 'user'/'user_name'.", input),
		Input:  input,
		Code:   ExitGeneral,
	}
}

// UserNotFound is returned when a user identifier (ID, email, or @name)
// can't be resolved.
func UserNotFound(input string) *Error {
	return &Error{
		Err:    "user_not_found",
		Detail: fmt.Sprintf("No user matching '%s'", input),
		Hint:   fmt.Sprintf("Run 'slack user list --query %s' to find users by display or real name, or 'slack user info <id-or-email>' if you have one. Note: users.lookupByEmail can fail on Enterprise Grid with a session token; use @name instead.", input),
		Input:  input,
		Code:   ExitGeneral,
	}
}

// DraftNotFound is returned when a draft ID is unknown.
func DraftNotFound(id string) *Error {
	return &Error{
		Err:    "draft_not_found",
		Detail: fmt.Sprintf("No draft with id %s", id),
		Hint:   "Run 'slack draft list' to see active drafts with their IDs. Add '--include-sent' or '--include-deleted' to see drafts Slack has otherwise hidden.",
		Input:  id,
		Code:   ExitGeneral,
	}
}

// SectionNotFound is returned when a sidebar section ID is unknown.
func SectionNotFound(id string) *Error {
	return &Error{
		Err:    "section_not_found",
		Detail: fmt.Sprintf("No section with ID '%s'", id),
		Hint:   "Run 'slack section list' to see section IDs. Section management uses undocumented APIs and requires a session token (xoxc-).",
		Input:  id,
		Code:   ExitGeneral,
	}
}

// InvalidTimestamp is returned when a --at / --after / --before flag
// can't be parsed. The hint enumerates every accepted format so an agent
// can fix and retry without a second round-trip.
func InvalidTimestamp(flag, value string) *Error {
	return &Error{
		Err:    "invalid_timestamp",
		Detail: fmt.Sprintf("Cannot parse %s: %s", flag, value),
		Hint:   "Accepted formats: RFC 3339 ('2026-04-20T09:00:00-07:00'), ISO date-time without zone ('2026-04-20T09:00:00'), or date only (YYYY-MM-DD, e.g. '2026-04-20'). For Slack message timestamps, use the raw '1713300000.123456' form from 'ts' fields, not these date formats.",
		Input:  value,
		Code:   ExitGeneral,
	}
}

// MissingBlocks is returned when a draft command needs Block Kit JSON on
// stdin but stdin is empty or a TTY. The hint carries a copy-pasteable
// minimal payload so the first invocation can succeed without reading
// the skill doc.
func MissingBlocks() *Error {
	return &Error{
		Err:    "missing_blocks",
		Detail: "pipe Block Kit JSON on stdin (this CLI requires structured input, no plain-text shortcut)",
		Hint:   `Minimal example: echo '[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello"}]}]}]' | slack draft create <channel>. Run 'slack skill' for the full Block Kit shape.`,
		Code:   ExitGeneral,
	}
}

// ThreadNotFoundNoMessage is returned when the thread root timestamp
// doesn't match any message in the channel.
func ThreadNotFoundNoMessage(channel, ts string) *Error {
	return &Error{
		Err:    "thread_not_found",
		Detail: fmt.Sprintf("No message at timestamp %s", ts),
		Hint:   fmt.Sprintf("The timestamp doesn't match any message in %s. Run 'slack message list %s' to find valid 'ts' values, or use 'slack search messages' with modifiers to locate the message.", channel, channel),
		Input:  ts,
		Code:   ExitGeneral,
	}
}

// ThreadNotFoundNoReplies is returned when the target message exists but
// has no thread replies. The timestamp is captured as Input so programmatic
// consumers can key off it without parsing the caller's Detail.
func ThreadNotFoundNoReplies(ts string) *Error {
	return &Error{
		Err:    "thread_not_found",
		Detail: "Message has no replies",
		Hint:   "This is a top-level message with no thread. Run 'slack message list <channel> --has-replies' to find messages that do have replies.",
		Input:  ts,
		Code:   ExitGeneral,
	}
}
