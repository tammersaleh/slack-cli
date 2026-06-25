# Draft create to new DMs / MPDMs (user_ids destinations)

## Problem

`slack draft create <channel>` runs its positional through `ResolveChannel`,
which paginates `conversations.list`. A DM or multi-person DM to people the
user has never messaged has no existing conversation channel, so resolution
fails with `channel_not_found` before any write. You can't stage a draft to a
new DM/MPDM.

`drafts.create` accepts `{"user_ids": ["U1",...]}` instead of
`{"channel_id":"C..."}` - that's how Slack's web client stages drafts to new
DMs/MPDMs; the conversation is created on send. The `destination` struct
already has `UserIDs []string`; it's just never populated on the create path.

## Interface (decided)

Variadic positional recipients:

```
slack draft create '#general'        < b.json   # channel (unchanged)
slack draft create @alice            < b.json   # new 1:1 DM
slack draft create @alice @bob U0333 < b.json   # new MPDM
```

`DraftCreateCmd.Channel string` becomes `Recipients []string` (variadic
`arg:"" required:""`, at least one).

## Classification (per recipient arg)

`classifyRecipient(s)` -> channel | user. Syntactic only; no network.

- slack URL: `slackurl.Parse`; `KindUser` -> user, channel/message -> channel,
  file URL -> caller rejects (`invalid_input`, not a draft destination).
- `@name` or contains `@` (email) -> user.
- `resolve.IsUserID` (`U…/W…`) -> user.
- everything else (`#name`, bare name, `C/D/G/M…` id) -> channel.

Bare names stay channels (backward compatible). To DM someone you must use
`@name`, email, or a `U…` id.

## Routing rules

- mix of channel + user args -> `invalid_input` (hint: bare names are channels,
  use `@name` for users).
- channel path: exactly one channel arg, else `invalid_input`. Resolve via
  `ResolveChannel`. `--thread`/`--broadcast` apply as today.
- user path (>= 1 user arg, no channel args): resolve every arg via
  `ResolveUser`; dedupe by resolved ID preserving first-seen order; do NOT sort;
  do NOT auto-add the caller. `--thread`/`--broadcast` -> `invalid_input` (a
  `user_ids` set identifies a participant set, not an existing thread context).
  `--at`/`--date-scheduled` allowed.

## Partial failure

Resolve all user args, collect every unresolved input, fail the whole command
(no best-effort - a single destination must be atomic). Error lists all bad
inputs.

## Deliberate non-decisions

- No local 1..8 recipient cap. MPDMs cap near 8, but `drafts.create` is
  undocumented; the codebase avoids baking inferred limits into undocumented
  endpoints (cf. channel managers role-id note in CLAUDE.md). Let Slack reject
  oversized lists; the error is already classified. Document the practical
  limit in help/SPEC.
- No `@self` magic alias and no self-recipient special-case; single user is the
  normal 1:1 DM case.

## Files

- `cmd/draft.go`: `Recipients []string`, `classifyRecipient`, rebuilt
  destination logic in `DraftCreateCmd.Run`, `Help()` text.
- `cmd/draft_test.go`: user single/multi, channel passthrough, mix error,
  multi-channel error, thread+user error, partial-resolve failure.
- `cmd/draft_classify_test.go` (or in draft_test): unit tests for
  `classifyRecipient`.
- `SPEC.md`, `docs/draft-messages.md`, `skills/slack-cli/SKILL.md`.

## Status

- [x] tests (red)
- [x] implementation (green)
- [x] docs/SPEC/SKILL/CLAUDE.md
- [x] test + lint + code review (code-reviewer + Codex)
- [ ] commit

## Review outcomes

Code-reviewer + Codex, two rounds. Addressed: collapsed dead `@` check;
removed the unreachable user-path bad-URL branch (a malformed URL can't reach
the user path - `KindUser` needs a well-formed id); added resolved-ID dedup
test; strengthened error-type assertions. From Codex: added a URL-parse
pre-scan in `resolveDestination` so a malformed URL surfaces its own
`invalid_input` ahead of the mix/arity checks (it was being masked as "cannot
mix"). Held the line on no local 8-recipient cap (undocumented endpoint; Slack
rejects oversized lists).
