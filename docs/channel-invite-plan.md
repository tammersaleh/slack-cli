# Plan: `channel invite` and `channel kick`

Source: `todo/channel-has-no-invite-command.md`. Delete that file when this lands.

## Verified live (2026-10-19, desktop session tokens on an Enterprise Grid org)

Side-effect-free probes with curl against `conversations.invite` and `conversations.kick`. No user was added to or removed from any channel.

| call | token | result |
|---|---|---|
| invite self to a private channel the user is in | workspace (T) | `{"ok":false,"error":"cant_invite_self","errors":[{"user":"U…","ok":false,"error":"cant_invite_self"}]}` |
| same | org (E) | identical |
| invite self to a Slack Connect private channel | T | identical (`cant_invite_self`) |
| invite self plus a bogus `U0000000000` | T | `error:"cant_invite_self"`, `errors` has one entry per user: `cant_invite_self`, `user_not_found` |
| invite self to `C0000000000` | T | `{"ok":false,"error":"channel_not_found"}`, no `errors` array |
| kick self from a private channel | T | `{"ok":false,"error":"cant_kick_self"}` |

Conclusions:

- The desktop session token has write access to private channels: Slack reaches per-user validation instead of answering `missing_scope`. Both the T and E contexts work, so no two-client split is needed.
- `conversations.invite` answers per user. The top-level `error` is the first user's error and `errors[]` carries one entry per failed user. A channel-level failure (`channel_not_found`) has no `errors[]`.
- slack-go's `InviteUsersToConversationContext` returns a `slack.SlackErrorResponse` whose `Errors` field carries `errors[]` (its `Error()` string is only the top-level code). A batched call could attribute errors to users; the design below does not batch for other reasons.
- Rate limits are HTTP 429 on the public API and slack-go turns those into `*slack.RateLimitedError`, which `api.FetchPage`'s retry already handles.

Not verified, and not verifiable without a side effect: whether a batched invite with one failing user still invites the others. Slack documents a `force=true` parameter for exactly that case. The design below does not batch, so it does not depend on the answer.

## Design

### Grammar

```
slack channel invite <recipient>...
slack channel kick <recipient>...
```

Variadic positionals classified syntactically by `classifyRecipient`: `@name`, email, `U…`/`W…` id, or user-profile URL is a user; everything else is a channel. The classifier and the malformed-URL split move out of cmd/draft.go into a neutral `cmd/recipients.go` shared by `draft create` and the two new commands. At least one channel and at least one user, or `invalid_input` with no API call. Order does not matter, so both of these work:

```
slack channel invite @alice '#eng-a' '#eng-b' '#eng-c'    # one person, many channels (the workflow that filed this)
slack channel invite '#eng-a' @alice @bob                 # many people, one channel
```

Bare names are channels, matching `draft create`. A person must be `@name`, email, or id.

### Resolution before any write

Every channel and every user resolves first. Any failure aborts the command before the first invite: a bad URL is `invalid_input`, otherwise `channel_not_found` / `user_not_found` naming the input. A write command must not do half the work and then fail on a typo. Users dedupe by resolved id, channels too, preserving first-seen order.

This prevents a resolvable typo from causing partial mutation. It does not prove every target exists: IDs and URLs pass through with no lookup, name caches can be stale, and membership or archive state can change after resolution. No `conversations.info`/`users.info` validation calls are added for id inputs; they cost latency and cannot close the race.

### One API call per (channel, user) pair

`conversations.invite` with a single user, and `conversations.kick` (which is single-user natively). Rationale:

- The workflow that filed this is one user across hundreds of channels, where batching buys nothing.
- Per-user outcomes are unambiguous. A batched call's partial-success semantics are unverified, and the only way to learn them is a real invite.
- Each call goes through a new `api.Call(ctx, endpoint string, fn func() error) error`, the single-shot sibling of `FetchPage` built on the same retry loop (5 attempts, `Retry-After`, `RateLimitExhaustedError` naming the endpoint, `call` trace events). Writes are not disguised as one-item pages and do not go through `streamPages`, whose cursor and trailer semantics are for pagination. Tier 3 at 50/min means a 600-channel run takes about 12 minutes, which is the caller's decision, not the CLI's.

Calls run sequentially in input order so the output is deterministic and a failure stops the run at a known point.

### Output

One row per (channel, user) pair, in input order, channels outer, users inner:

```
{"channel_id":"C01ABC","user_id":"U01XYZ","status":"invited"}
{"channel_id":"C01ABC","user_id":"U02MGR","status":"already_in_channel"}
{"channel_id":"C02DEF","user_id":"U01XYZ","error":"not_in_channel","detail":"..."}
{"_meta":{"has_more":false,"error_count":1}}
```

`channel_id` and `user_id` are the field names so the printer's enrichment attaches `channel_name` and `user_name`. `input` is omitted since the ids are already resolved and the row names both sides.

`status` values:

- invite: `invited`, `already_in_channel`
- kick: `kicked`, `not_in_channel`

Each Slack error code has one of three dispositions, from explicit allowlists:

| disposition | invite | kick |
|---|---|---|
| idempotent, `status`, exit 0 | `already_in_channel` | `not_in_channel` |
| pair error, one `error` row, continue | `cant_invite_self`, `cant_invite`, `user_is_ultra_restricted`, `user_is_restricted`, `ura_max_channels`, `user_not_found`, `user_team_not_in_channel` | `cant_kick_self`, `user_not_found` |
| channel error, `error` row for every remaining pair in that channel with no further calls, continue with the next channel | `not_in_channel` (the caller is not a member), `is_archived`, `channel_not_found`, `method_not_supported_for_channel_type` | `channel_not_found`, `cant_kick_from_general`, `is_archived`, `method_not_supported_for_channel_type` |
| fatal, abort the run | everything else | everything else |

Note `not_in_channel` means the caller on invite and the target on kick. `restricted_action` and `no_permission` are fatal: they describe workspace policy or the caller, not a pair, and `no_permission` is documented ambiguously, so fail closed. `too_many_users` cannot happen with one user per call, so it is a protocol bug and fatal. Unknown codes are fatal: treating an unknown code as pair-local is how a systemic failure gets multiplied across the matrix (the same fail-closed rule `saved list --enrich` uses). Auth, rate-limit exhaustion, network, timeout, `missing_scope`, and `parse_error` are fatal by the same rule. The implementer confirms every listed code against the Slack method docs before committing the lists; a code not in the docs and not observed live is left out.

Pair and channel errors count in `error_count` and the command exits 1 via `*output.ExitError` with no stderr. A fatal error writes the classified error to stderr with its own exit code (2 auth, 3 rate limit, 4 network, 1 otherwise) and the trailer reads `{"_meta":{"has_more":true,"error":"<code>","error_count":N}}`: rows already emitted are authoritative, `has_more:true` means some requested pairs have no row, there is no cursor, and the recovery is to rerun the same command, which is idempotent. A fatal error after Slack committed a write but before the response arrived is the one unknowable pair; no row is guessed for it.

Rows emit as they complete, so a long run shows progress and a killed run leaves a usable partial record.

### Scopes

`conversations.kick` needs `channels:manage` and `groups:write` on a bot token for public and private channels, and those two also authorize `conversations.invite`, so the granular `channels:write.invites`/`groups:write.invites` add consent text without reducing authority. Add exactly `channels:manage,groups:write` to `botScopes` in `internal/auth/oauth.go`. Not `im:write`/`mpim:write`: both methods are documented for channels only and answer `method_not_supported_for_channel_type` on a DM. Both scopes authorize more than membership changes (create, archive, rename); Slack offers nothing narrower that covers kick, and the CLI uses only the two methods above. OAuth tokens issued before this must re-auth; the desktop session token already has them (verified above). `missing_scope` from an old OAuth token is fatal (exit 1 today, see the code-review note below).

### Design principle

SPEC.md's "No message sending" principle lists the write operations. Add channel membership (`channel invite`, `channel kick`) to the list, and fix the "read-only" claim in SPEC.md's first line and in CLAUDE.md's opening description, which the sections and drafts already contradicted. The CLI still never posts a message.

### Not doing

- No batched `users` parameter. See above.
- No `channel create`. Separate todo if someone hits it.
- No concurrency. Tier 3 is 50/min; parallel calls only reach the limit faster.
- No `--dry-run`. Resolution failures already abort before any write, and the rows are the record.
- No batching with `force=true`. Viable (slack-go keeps `errors[]`), but it adds response reconciliation, makes invite structurally different from kick, and saves nothing for the one-user-many-channels workflow.

### Docs check (2026-10-19)

Method docs confirmed for `conversations.invite`: `already_in_channel`, `cant_invite`, `cant_invite_self`, `channel_not_found`, `is_archived`, `method_not_supported_for_channel_type`, `not_in_channel` (the authenticated user is not in the channel), `ura_max_channels`, `user_is_restricted`, `user_not_found`, `invitee_cant_see_channel`, `org_user_not_in_team`, `no_external_invite_permission`, `app_cannot_join_channel`, `no_permission` (user or token, ambiguous). Not documented, dropped from the plan's lists: `user_is_ultra_restricted`, `user_team_not_in_channel`, `too_many_users`. Codex's three codes were documented after all and are in. Bot scopes listed: `channels:manage`, `channels:write.invites`, `groups:write`, `groups:write.invites`, `im:write`, `mpim:write`. `force` exists (invite valid users, skip invalid ids).

Method docs confirmed for `conversations.kick`: `cant_kick_from_general`, `cant_kick_self`, `channel_not_found`, `method_not_supported_for_channel_type`, `not_in_channel` (the user is not in the channel), `not_supported` (this channel and user combination), `restricted_action` (team preference), `user_not_found`, `no_permission`. Not documented, dropped: `is_archived`. `not_supported` added as a pair error. Bot scopes: `channels:manage`, `groups:write`.

`missing_scope` classifies exit 1 in `api.ClassifyError` (not in `authErrors`); the fatal test asserts that rather than the exit 2 the plan text assumed.

Code review (Codex, 2026-10-19) on the branch: accepted - trace docs for the new `call` kind and the `retry.attempt` meaning (SPEC's example said 2, the code has always emitted the rate-limited attempt, 1), a trace test that asserts payloads, kick mirrors for dedupe, a command-level rate-limit-exhaustion test (exit 3, trailer `rate_limited`), a `--quiet` test, and `Call` tests for the one-second floor and cancellation during the wait. Rejected - reclassifying `missing_scope` to exit 2 (pre-existing CLI-wide behavior, SPEC contradiction filed as `todo/missing-scope-exits-1-not-2.md`); dropping kong's `required:""` so a bare `slack channel invite` returns JSON `invalid_input` (every other positional command uses the kong usage error, and SPEC never promised otherwise); fixing resolver error flattening (`channel_not_found` for a rate limit during `#name` resolution - pre-existing, already `todo/resolver-errors-are-reported-as-channel-not-found.md`).

Codex review of this plan (2026-10-19) produced the `api.Call` primitive, the three-tier error dispositions, the two-scope decision, the shared recipients file, and the fatal-trailer contract. Rejected: unverified error codes it proposed for the allowlists (`invitee_cant_see_channel`, `org_user_not_in_team`, `no_external_invite_permission`, `not_supported`), pending a docs check by the implementer.

## Steps

1. [x] SPEC.md: principle line, `channel invite` and `channel kick` sections with the table above summarized, scopes note.
2. [x] Tests (red), `cmd/channel_invite_test.go` and `internal/api/call_test.go`: grammar (`invalid_input` for no channel or no user, zero API calls; no args at all is kong's usage error), resolution-before-write (a bad user among good ones makes zero invite calls; `channel_not_found`, `user_not_found`, `invalid_input` for a bad URL), one call per pair with `channel` and `users`/`user` form params asserted, row order, `status` values, pair error with `error_count` and `ExitError` code 1, channel error fan-out (one `not_in_channel` answer yields a row per remaining user with no further calls, next channel proceeds), endpoint-specific `not_in_channel` (status on kick, channel error on invite), `no_permission` and an unknown code abort, fatal after successful rows (rows present, trailer `has_more:true` with `error`, stderr error, exit 2 for `invalid_auth`), rate-limit retry (429 with `Retry-After` then 200 yields `invited`), dedupe of repeated inputs, kick mirror of each. Table-driven where the invite/kick pairs differ only in endpoint and status words. `api.Call` unit tests mirror `FetchPage`'s retry tests.
3. [x] Implement `cmd/recipients.go` (moved classifier), `api.Call`, `ChannelInviteCmd`, `ChannelKickCmd`, shared `membershipChange` runner with the disposition tables.
4. [x] `botScopes`.
5. [x] SKILL.md: both commands under the channel section, with the one-person-many-channels example. `cmd/skill_test.go` regression lines.
6. [x] CLAUDE.md architecture bullet with the verified table's conclusions.
7. [x] Review (two Codex passes, clean), merge, push. [ ] Wait for release, install, verify. Error paths verify live without side effects (`cant_invite_self`, `cant_kick_self`, `user_not_found`, `channel_not_found`). A real invite end to end needs a scratch channel and a consenting user, which the session does not have; ask Tammer.
