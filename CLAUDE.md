# slack-cli

Read-only Slack CLI for agent/automation use. Go, kong, slack-go/slack.
JSONL output, OAuth + Desktop auth, bot and session (`xoxc-`) tokens,
public and undocumented internal APIs.

## Design constraint: no message sending

The CLI never posts messages. `chat.postMessage` is intentionally not
wrapped - too risky for agent use. Draft staging via the internal
`drafts.*` endpoints is allowed (the user reviews and sends from Slack);
direct send is not. `message post` was removed for this reason.

When manually testing draft creation, stage drafts in a self-DM. Never
target other users or shared channels during verification.

## This repo is public - never commit real workspace data

No real channel names, channel IDs, user IDs, team/org IDs, custom-profile
field IDs (`Xf…`), people's names, job titles, reporting lines, employee IDs, or
workspace subdomains. This applies to tests, docs, SPEC examples, command help
text (it ships inside the binary), and CLAUDE.md itself.

Use the synthetic conventions already in the repo: `C01ABC`/`C02DEF` channels,
`U01XYZ`/`U02MGR` users, `T01ABC` teams, `Xf01AAA` profile fields, `@alice` /
Alice Adams / Bob Brown / Carol Chen people, `acme.slack.com`, `@example.com`
emails. Tammer's own name and `@example.com` address are fine - it's his repo.

Live verification against a real workspace is still the rule; only the *findings*
get written down. Record shapes, counts, and behavior, never identifiers: write
"a channel the user manages" rather than the channel's real name, and "an
Enterprise Grid org" rather than the employer name paired with its exact channel
count. When a measurement's value depends on a literal ID, say the ID is
workspace-specific and omit it.

Do not quote a real identifier even as an example of what not to write - that
still commits it. Writing an earlier draft of this very rule is how the real
channel name got reintroduced into CLAUDE.md hours after being scrubbed from
everywhere else, and it took a history rewrite to remove again.

History was rewritten 2026-07-27 (`git filter-repo`, all branches and tags
force-pushed). Note `--replace-text` only rewrites file contents: commit
*messages* need `--replace-message`, and the first pass missed them because a
scrub commit body had helpfully listed every real ID. GitHub still serves the
pre-rewrite commits through `refs/pull/*` and by SHA; only GitHub Support can
purge those.

A scrub landed 2026-07-27 after real IDs, an org chart naming employees up to the
CEO, and an employee ID were found in the public tree. Grep before committing:
`git grep -nE '\b[CDGU]0[A-Z0-9]{8,}\b'` should only match synthetic IDs.

## Internal Slack APIs

The saved items and sidebar sections features use undocumented Slack
internal APIs (not in the public Web API docs). These require xoxc/xoxd
session tokens and use the same auth pattern as Desktop extraction.

Key endpoints:
- `saved.list` - list saved-for-later items
- `client.counts` - get all channels the user is in (Enterprise Grid safe)
- `users.channelSections.list` - list sidebar sections
- `users.channelSections.create` - create a sidebar section
- `users.channelSections.channels.bulkUpdate` - move channels between sections
- `conversations.info` - channel metadata (used for name resolution in sections)
- `admin.roles.entity.listAssignments` - channel managers ("Managed by")

All use POST with `Content-Type: application/x-www-form-urlencoded` (or
`application/json` for `client.counts`). Auth: Bearer token + Cookie header
with `d=<xoxd_token>`. A valid User-Agent header is required - Slack
invalidates tokens without one.

## Auth for internal APIs

The existing Desktop auth already handles xoxc/xoxd tokens and Chrome TLS
fingerprinting. The internal API calls should reuse the same `api.Client`
transport layer. The `d` cookie injection and Chrome user-agent are already
wired into the custom `RoundTripper`.

## Caching

Channels and users are cached under `slack-cli/cache/` inside the user
config directory with a 24h TTL (configurable via `SLACK_CACHE_TTL`).
Both cache files are keyed by workspace `TeamID`.

The path resolves via `os.UserConfigDir()`, so it is **`~/Library/Application
Support/slack-cli/cache/` on macOS**, not `~/.config/slack-cli/cache/` -
that form is Linux-only. This matters when measuring cold-cache behavior:
`rm -f ~/.config/slack-cli/cache/channels-*.json` silently matches nothing
on macOS, and zsh reports `no matches found` rather than failing the
command, so the "cold" run that follows is really a cache hit. Get the real
paths from `slack cache info`. Credentials sit beside the cache dir at
`slack-cli/credentials.json`.

## Workflow

Work is driven by `SPEC.md`. Every change - feature, bug fix, perf fix,
refactor - follows the same workflow. No shortcuts for "small" fixes:

1. Read `SPEC.md` for the relevant command/feature.
2. Create a feature branch off main (or work directly on main for hot
   fixes - still follow every other step).
3. Red-green-refactor: write failing tests first, then implement, then
   clean up.
4. Run both `mise run test` and `mise run lint` after every change.
   Both must pass before committing.
5. Keep commits small and conventional. Commit types drive releases -
   see "Release versioning" below.
6. **MANDATORY code review before push** (code changes only - skip
   for docs-only changes where every modified file is Markdown or
   plain-text documentation): spawn a `feature-dev:code-reviewer`
   sub-agent on the pending changes (`git diff main...HEAD` for a
   branch, or on the commits about to push for direct-to-main work).
   Tell the reviewer to scrutinize tests: look for tests that don't
   actually test what they claim, useless tests, and missing test
   coverage. Address every important or critical finding. **Re-run
   the reviewer after addressing feedback** to confirm the fixes are
   clean - "before and after" reviews are both required. Never push
   code without a clean review pass. Skipping is not acceptable
   regardless of how small a code change looks; the docs carve-out
   is the only exception.
7. Merge to main and push. The pre-push hook runs `mise run check`
   (test + lint); never bypass with `--no-verify`.
8. **Not done until installed and verified locally.** A push is not the
   finish line. After every release-cutting push, launch a background
   sub-agent to wait for the release and upgrade, so the main session isn't
   blocked babysitting CI. The sub-agent polls `gh release list` /
   `gh run list` until the new tag cuts, then runs
   `brew upgrade --cask tammersaleh/tap/slack-cli` and reports the installed
   `slack version`; the main session then exercises the new behavior.
   Practical note (learned 2026-07-09): a general-purpose sub-agent is a
   poor poller - it repeatedly misreads its own role and bails early with
   "I'll wait for the notification" instead of looping. A background `Bash`
   poll loop (`run_in_background`) that re-invokes the main session on exit
   works reliably: loop `gh release view vX.Y.Z` with `sleep 90` until the
   tag cuts, then a second loop on the tap's raw `Casks/slack-cli.rb` until
   `version "X.Y.Z"` appears (the cask artifact lags the git tag by minutes -
   `brew upgrade` says "already installed" until then), then upgrade and
   verify in the main session. The
   release flow: release-please opens a release PR that auto-merges once CI
   is green, then GoReleaser tags it and pushes the Homebrew artifact -
   minutes, not instant. The artifact is a
   **cask**, not a formula: it lives at `Casks/slack-cli.rb` in the tap (not
   `Formula/`), and the version line is `version "x.y.z"`. Don't waste time
   grepping `Formula/`. Then install and verify against the real artifact,
   never your local build:
   - Binary change (`feat:`/`fix:`): `brew upgrade --cask tammersaleh/tap/slack-cli`
     (plain `brew upgrade slack-cli` can no-op if the cask tap hasn't synced;
     poll the tap's `Casks/slack-cli.rb` for the new `version` first), confirm
     `slack version` is the version just cut, and exercise the new behavior
     with the installed `slack`. Note Slack rate-limits quickly under repeated
     manual calls - a `{"error":"ratelimited"}` row is the expected systemic
     fail-fast, not a bug; wait and retry.
   - Skill change (`SKILL.md`): run `skills update`, then confirm the
     expected text is in the installed skill.
   `chore:`/`docs:`/`test:`/`refactor:` cut no release, so there's no
   binary to install - but a `docs:` edit to `SKILL.md` still ships via
   `skills update`. Never report a change "done" off a push alone; if the
   release hasn't cut yet, wait and re-check.
9. Retrospective: review your approach and these instructions. Update
   CLAUDE.md with anything you learned that would help future sessions.
10. Move on to the next feature.

Never ask permission to run this workflow. Committing, pushing to main, and
waiting out the release are the documented process, not a decision point -
asking "should I push?" wastes a round trip. The Autonomy section below is
the general rule; this is the specific one people trip over.

## Bug reports and todos

Both directories are untracked scratch space holding work orders, not
artifacts. Delete the file when the work is done - don't archive it or append
a resolution section. Findings belong in CLAUDE.md, SPEC.md, and the commit
body, where they'll actually be read.

- `bugs/` - something is broken now. Verify, fix, delete.
- `todo/` - work deferred out of the current change, usually because it's a
  separate design decision. Write it for a session starting cold: what was
  measured and how, the proposed approach, and what to verify rather than
  assume. See the 2026-07-27 `channel list` entry for the shape.

Deferring is the right call when a finding is real but orthogonal to the fix
in hand - resist widening scope mid-change, and resist dropping the finding.

Verify before fixing, and verify the whole report: a report can be right
about the symptom and wrong about the cause, or bundle a real bug with a
non-bug. Say which claims held. The 2026-07-27 truncation report is the
model - two real defects, one misdiagnosis (`--all` was said to omit the
trailer on success; it never did - those runs were rate-limited with stderr
discarded), and a third defect the report never named (zero rate-limit retry
in the streaming loops).

## Release versioning

Releases are fully automated via release-please + GoReleaser.
Release-please watches main; when a commit with a version-bumping type
lands, it opens a release PR which auto-merges once CI passes. The
merged PR cuts a tag + GitHub Release; GoReleaser builds binaries and
pushes an updated Formula to `tammersaleh/homebrew-tap`. Nobody runs
`git tag` by hand - and nobody clicks Merge on the release PR either.

This means commit type is not a style choice. It is the entire release
trigger. A `feat:` or `fix:` commit pushed to main ships as a new
Homebrew release within minutes. A `chore:` or `docs:` commit pushed to
main ships nothing. Pick the type based on user-facing impact, not diff
size:

- `feat:` - minor bump, listed under "Features". New commands, flags,
  outputs, or API surface.
- `fix:` - patch bump, listed under "Bug Fixes". Behavior that was
  already promised but broken.
- `feat!:` (or `BREAKING CHANGE:` footer) - minor bump pre-1.0, major
  post-1.0, listed under "BREAKING CHANGES". Anything that can break
  an existing caller: removed/renamed flags, changed output shape,
  changed exit codes, changed command behavior.
- `chore:`, `docs:`, `test:`, `refactor:`, `perf:`, `style:` - no
  release, not in changelog. Internal only.

Rules:

- If a commit contains both a feat and a fix, split it into two commits.
  A single commit gets one type; mixed intent produces a misleading
  changelog entry.
- Dependency bumps: `fix:` if the bump reaches users (a fix in a
  runtime dep), otherwise `chore:` (test deps, build tooling, etc.).
- Don't downgrade a type to avoid a release. If it's user-facing, it's
  `feat:` or `fix:`, and release-please should cut a version for it.
- Don't upgrade a type to force a release. Internal refactors are
  `refactor:` even if the change is large.
- Write the subject in imperative mood ("add channel mark", not "added
  channel mark") and keep it under ~70 chars; release-please quotes it
  verbatim into the changelog.
- Body text on feat/fix commits shows up in the expanded release notes.
  Worth keeping them tidy for the same reason.

## Autonomy

Work through features independently. Never stop to ask "should I
continue?" or "want me to keep going?" - the answer is always yes.
After giving a status summary, keep working. Only escalate when:

- A design decision isn't covered by `SPEC.md`.
- Something feels wrong (scope creep, Slack API limitation, etc.).

## Project structure

```
cmd/
  root.go        # CLI struct, global flags, NewPrinter/NewClient/NewResolver
  auth.go        # auth login/logout/status
  bookmark.go    # bookmark list
  cache.go       # cache info/clear
  channel.go     # channel list/info/members
  dnd.go         # dnd info
  emoji.go       # emoji list
  file.go        # file list/info/download
  message.go     # message list (alias: read) / get
  permalink.go   # message permalink
  pin.go         # pin list
  presence.go    # presence get
  reaction.go    # reaction list
  saved.go       # saved list/counts (internal API)
  search.go      # search messages/files
  section.go     # section list/channels/find/move/create (internal API)
  status.go      # status get
  thread.go      # thread list (alias: read)
  user.go        # user list/info
  usergroup.go   # usergroup list/members
  version.go     # version
  workspace.go   # workspace info
internal/
  api/           # Slack API client (Client, Paginate[T], ClassifyError,
                 #   PostInternal, PostInternalForm, WithCookie)
  auth/          # credentials CRUD, OAuth flow, Desktop extraction
  output/        # Printer (JSONL + enrichment), Meta, Error with exit codes
  resolve/       # channel/user resolution with 3-tier cache + enrichment
```

## Testing

Tests live next to the code they test (`foo_test.go`). Use table-driven tests. Mock the Slack API via httptest servers - set `SLACK_TOKEN` and `SLACK_API_URL` env vars in tests. Use `runWithMock(t, handler, args...)` helper in `cmd/channel_test.go` for end-to-end command tests. For session-token (xoxc) flows use `runWithMockSession` in `cmd/saved_test.go`.

Test helpers call `isolateTestEnv(t)` (in `cmd/channel_test.go`) to clear workspace/cookie/fields env vars that could leak from the developer's shell. Tokens (`SLACK_TOKEN`, `SLACK_USER_TOKEN`) are intentionally left alone - helpers and individual tests set those explicitly for the scenario under test. If you add a new test helper that stands up its own `httptest.Server`, call `isolateTestEnv(t)` at the top.

For functions only accessible within `package cmd` (like `padDraftTS`), put unit tests in a file that uses `package cmd` rather than `package cmd_test` - see `cmd/padding_test.go` for the pattern.

## Git

This is a personal project. The workflow is entirely local: commit
on a branch (or directly on main for small fixes), merge to main,
push. Don't open pull requests. `gh pr create` is not part of any
flow here. The only PR that exists is the release-please automation
PR, and that's created and merged without human involvement.

## Output

JSONL to stdout. Every command emits one JSON object per line, ending with a `_meta` trailer. Errors as JSON to stderr. See `SPEC.md` for the full output model.

## Architecture decisions

- No config file. All config via flags/env vars. Kong handles precedence.
- Workspaces keyed by `TeamID` (stable) not `TeamName` (mutable) in credentials.json.
- User resolution: ID, email, or `@name` (display name / username / real name). Name lookups use the user cache index. `users.lookupByEmail` fails with `xoxc-` tokens on Enterprise Grid (scope limitation).
- Channel resolution: first match wins on name collision. No ambiguity errors.
- Channel list defaults to member-only. `--include-non-member` to expand.
- Single-page pagination by default. `--cursor` to continue, `--all` to fetch everything.
- `api.Paginate[T]` handles cursor-based pagination with rate-limit retry (5 attempts, respects Retry-After). `api.PaginateEach[T]` adds per-page callback with early exit. `api.FetchPage[T]` is the single-page primitive all three share - use it for streaming pagination, where pages print as they arrive. `maxAttempts = 5` means 1 initial call + 4 retries, no wait after the last failure; `RateLimitExhaustedError.Attempts` counts calls, not retries. All accept an endpoint name for diagnostics.
- Every streaming list command goes through `streamPages` (cmd/paginate.go) - `channel list`/`members`, `message list`, `thread list`, `file list`, `search messages`/`files`, `user list`, `saved list`. It owns the fetch/emit/trailer loop so stdout always ends with exactly one `_meta`, including on mid-stream failure (`{"has_more":true,"next_cursor":"<failed page>","error":"rate_limited"}`). Before this, a rate-limited `--all` wrote partial JSONL with no trailer and a truncated run was indistinguishable from a complete one - it corrupted a real analysis (285 of ~700 channels read as the full set). The commands' hand-rolled loops also had zero rate-limit retry; routing them through `FetchPage` fixed that too. Contract details that matter: `emit` is **output-only** - no network calls, no fallible domain checks - because an error from it is treated as broken stdout and returns with no trailer; domain checks belong in the fetch closure, which runs before the page's first `PrintItem` (see `thread list`'s not-found checks and `saved list`'s parse + `--enrich`). `streamError` splits failures three ways: an error the closure already built as `*output.Error` is a verdict on the request, so the trailer gets `has_more:false` and no cursor; a Slack error string in `api.nonResumableErrors` gets the same terminal treatment; everything else gets `has_more:true` + the failed page's cursor. The resume cursor is `cursor`, not `next` - the failed page is the first one missing. `pageCursorFetch` adapts the page-number APIs (files.list, search.*), deriving the next page from the response's paging block, never an incremented counter, so a retry can't skip. `user list` builds a fresh `slack.UserPagination` per attempt with every option reapplied - the paginator carries cursor state and a shared one would rewind to page 1 on retry. There's a repeated-cursor guard because `--timeout` defaults to off. `has_more` is now uniformly `next_cursor != ""`; `message list` previously reported Slack's `has_more` separately, which could contradict the cursor.
- Resume-cursor disposition lives in one place: `api.nonResumableErrors` + `api.IsNonResumablePageError` (internal/api/errors.go), consulted only by `streamError`. The membership question is narrow - **does this error void the continuation itself** - not the broader "is this error deterministic". In are `invalid_cursor`, the request-shape family (`invalid_arguments`, `invalid_limit`, `invalid_query`, `invalid_ts_oldest`/`_latest`, `missing_argument`, …), `unknown_method`/`method_deprecated`, and `thread_not_found`. `thread_not_found` is the one target error, and it's in for consistency rather than determinism: `thread list` already synthesizes a terminal `output.ThreadNotFoundNoMessage` when `conversations.replies` answers `ok:true` with no messages, and SPEC names `thread_not_found` as *the* `has_more:false` example - but Slack raises its own `ok:false`/`thread_not_found` for a ts matching no message (verified live 2026-07-27), which took the resumable branch. The same outcome cannot be terminal on one path and resumable on the other. It carries none of `channel_not_found`'s visibility ambiguity: a caller who can't see the conversation gets `channel_not_found`/`not_in_channel` instead. Deliberately out, staying resumable: auth errors, `missing_scope`, `no_permission`, `team_is_restricted`, `ekm_access_denied`, `not_in_channel`, `is_archived`, `channel_not_found`, `user_not_found`, and every unknown future code. Those are all deterministic *right now* but clear when credentials, scopes, membership, or admin policy change, and the cursor still names a real page - re-auth-then-resume is a genuine recovery path, so discarding the checkpoint is the worse error. `TestNonResumableAndAuthSetsDoNotOverlap` pins that auth never leaks into the set. Two policies, deliberately independent: `ClassifyError` decides exit code, `IsNonResumablePageError` decides cursor disposition, and `streamError` asks the second about the *original* error so resumability is never inferred from the exit code. Both share `slackErrorCode` so they can't disagree about what counts as a Slack error - only a typed `slack.SlackErrorResponse` qualifies, so a local error that happens to spell `invalid_cursor` stays resumable. The bug this fixed: a rejected cursor came back as `{"has_more":true,"next_cursor":"<the cursor Slack just rejected>","error":"invalid_cursor"}`, which per SPEC means "resume from here" - an automated consumer retries forever. `invalid_cursor` also gets `output.InvalidCursor()`'s real detail and hint instead of echoing the bare Slack string, since "invalid_cursor" alone doesn't tell a caller to drop `--cursor`.
- Cross-endpoint cursor reuse is not symmetrical, verified live 2026-07-27 on Enterprise Grid. A `conversations.list` cursor (`team:C…`) fed to the default `users.conversations` path is **silently accepted** and answers from a different result set (exit 0, plausible-looking rows). The reverse - a `users.conversations` cursor (`trunc:C…`) fed to `--include-non-member` - is also accepted but returns **zero rows with the same cursor echoed back**, so `has_more:true` with no progress. Neither produces `invalid_cursor`; only a malformed/expired token does. There is nothing to detect this from the client side (cursors are opaque and endpoint-specific), which is why SPEC tells callers to discard a cursor whenever flags change. `--all` and `--cursor` are mutually exclusive, so the echo case can't drive `streamPages`' loop.
- `message list` time bounds are **paging state, not a first-page filter** - `oldest`/`latest` go on every `conversations.history` request of a walk. The cursor carries only a position; the bounds define the window *and* the direction. Measured live 2026-07-27 on a channel whose `--after A --before B` window holds exactly 6 messages: sending the bounds only on page 1 (the old `boundsSent` flag) made `--all --limit 2` emit 1324 messages and still be running at the 2-minute mark, walking backwards to 2021. Oldest-only is worse - `--after X --all` emitted 954 rows *with duplicates* and no `_meta` trailer, because page 1 pages forward and page 2 (bounds dropped) resumes the same cursor backwards over ground already covered. Repeating the bounds fixes both: 6 rows and 3 rows respectively, `has_more:false`. The observed `conversations.history` semantics, none of them documented: with `latest` set, or with no bounds at all, the walk is newest-first from `latest`; with only `oldest` set the walk runs oldest-first from `oldest`; a cursor **replaces** whichever bound is the anchor but the opposite bound still filters (so cursor + a conflicting `oldest` returns messages below that `oldest`); the cursor ts is inclusive. Consequence for callers: `--after` alone is newest-first *within* a page but pages ascend, so an `--all` run is not globally sorted - adding `--before` restores one descending stream (verified: 15 rows either way, descending only with both bounds). Consequence for this repo: the `todo/` entry that proposed dropping the bounds when `--cursor` is set had it exactly backwards, and `cmd/message_test.go`'s `historyWindowMux` encodes the semantics above so the next person doesn't re-derive them live.
- Internal-API pagination (`saved list`) gets the trailer fix but **not** retry: `PostInternal` ignores HTTP status, so a 429 surfaces as `SlackErrorResponse{"ratelimited"}` (exit 1), never `*slack.RateLimitedError`. Making internal APIs retry means teaching `PostInternal`/`PostInternalForm` to read status + Retry-After. Not done.
- `saved list --enrich` needs the **two-client split**, and this is the finding that cost the most to reach. `saved.list` is internal and wants the session (E-org) token; the per-item message lookups run on the **workspace (T-prefix) public client**, because `chat.getPermalink` answers `enterprise_is_restricted` on the org context. `conversations.history` and `conversations.info` both work fine on the org token, which is exactly why the split wasn't needed until the reply fallback existed - the command had gotten away with one client for its whole life. Measured live 2026-07-28 on a Grid org: with the org token every one of the 13 reply items failed the fallback, and the same channel+ts resolved cleanly under `message get` (which uses `NewClient`). Same conversation, same call, different token. The public client is built **only for `--enrich`**: requiring a workspace credential for a bare `saved list` would break a topology that used to work (an org session credential alone) to buy nothing, since the org token resolves channel names fine. Order matters twice: `NewClient` must precede `NewResolver` so the resolver keys its cache on the workspace team id, and it clobbers `cli.authMethod`. Two Grid caveats that fall out of the split: `SLACK_TOKEN` outranks stored credentials for *both* clients, so setting it collapses them onto one token and reinstates whichever restriction that context carries (leave it unset, select with `SLACK_WORKSPACE` + `SLACK_WORKSPACE_ORG`); and the workspace credential needs the `:history` scopes, which a desktop session token has and an OAuth token did not until they were added to `botScopes`. Diagnosing this took a detour worth recording - `auth status` appeared to list only one credential, which made the two tokens look identical; it lists all of them, but a `SLACK_WORKSPACE` in the environment filters it, and `SLACK_WORKSPACE_ORG` was silently steering `NewSessionClient` somewhere else the whole time. Check the env before concluding two clients resolve the same token.
- `saved list --enrich` resolves each item's message via `lookupSavedMessage` (cmd/saved.go): `conversations.history` windowed on the exact ts, then `findThreadReply` (cmd/message.go, shared with `message get`) when that window doesn't contain it. `conversations.history` **never returns thread replies**, so the old history-only lookup dropped `text`/`from_user` for every saved reply and still exited 0. Measured live 2026-07-28: 13 of 35 saved items were replies, and all 13 came back unenriched - 37% of the output silently missing the only fields the flag adds. After the fix all 35 rows carry text, exit 0, and each reply's text matches byte-for-byte what an independent `message get` probe returned (so no thread parent leaked in - the failure mode to actually test for). Three rules hold the rewrite together. (1) **Match the exact ts** rather than `Messages[0]`; the window should only ever hold the target, and attaching a neighbour's text to a saved item is a wrong answer that reads as a right one. `conversations.replies` also always prepends the thread parent, which `findThreadReply` already scans past. (2) **Resolution success is its own field** (`enrichOutcome.resolved`), never inferred from non-empty text - a bot or block-only message legitimately has neither `text` nor `user` and must not acquire an error marker. (3) **Outcomes are indexed by input position**, not keyed by `channelID:ts`; the old map let duplicate saved rows overwrite each other last-writer-wins and made row association depend on which goroutine finished first. The per-item `conversations.info` call is gone: `channel_name` always came from the printer's `EnrichFunc`, which calls the same single `conversations.info` one layer down via `LookupChannel` and memoizes it (verified - a no-`--enrich` run already names all non-DM rows). Volume goes from `2N` to `N + 2F` for `N` items with `F` replies. DM rows still get no name from either path; `conversations.info` on a `D` id returns empty `name` plus a `user` field naming the peer, so naming them is possible but is a separate feature (display vs real name, deactivated users, MPIMs).
- Enrichment error policy is **fail-closed**, and that is the whole point of it: `itemLocalEnrichErrors` (cmd/saved.go) enumerates the four codes that describe one saved item - `message_not_found`, `thread_not_found`, `not_in_channel`, `channel_not_found` - which mark their own row with `enrich_error` and let the page through. Everything else, *including a code Slack has not invented yet*, voids the page. The inverse arrangement (enumerate the fatal codes, inline the rest) is how this bug class returns under a new code; the original `if err == nil` swallowed all of them, so a rate limit and a deleted message both read as "no text", and `api.FetchPage`'s `Retry-After` retry sat one layer up permanently unreachable because no error ever propagated to it. SPEC's claim that "rate limit retries apply" to enrichment was false for the same reason. Fail-closed earned its keep immediately: `enterprise_is_restricted` is deliberately **not** in the item-local set, and because of that the wrong-token bug above surfaced as a loud page failure on the first live run. Had it been inlined, the run would have emitted 13 tidily-marked rows and the real cause - a misconfigured token context, fixable - would have looked like 13 permanently unreadable channels.
- Retry for the enrichment lookups is **page-granular, and slack-go does not provide it**. Verified against slack-go v0.18.0: it retries only inside its own `GetAll*` helpers (`GetAllConversationsContext` and friends); `GetConversationHistoryContext`, `GetPermalinkContext` and `GetConversationRepliesContext` all go through one-shot `postMethod`/`getMethod`. So a 429 on a lookup becomes a systemic error, and because `enrichItems` runs *inside* the fetch closure that `api.FetchPage` wraps, FetchPage retries **the whole page** with `Retry-After` backoff - `saved.list` plus every already-successful lookup, up to `maxAttempts`, with the resulting `RateLimitExhaustedError` naming `saved.list` rather than the method actually limited. Output-correct (the abandoned attempt emits nothing) but coarse, and worth knowing before trusting a `--trace` endpoint attribution. Don't write "slack-go owns retry here" - that was in a comment and in SPEC, and both were wrong.
- `cli.authMethod` is a single ambient value that `ClassifyError` reads for its re-auth hint, so a command holding **two** clients has to set it per stage, not once. `saved list` sets the session method before `PostInternal` and the public method before enrichment (and before emit, since the resolver's lookups run there). Restoring the session method once and leaving it - which is what `channel managers` does, correctly, because the internal call is its only auth-classified failure - would tell a user with a revoked *workspace* token to re-auth the *session* credential. Mixed auth methods across the two credentials (org desktop + workspace OAuth) is a supported setup, so this is not hypothetical.
- `output.alwaysKeptFields` (internal/output/output.go) is the set `--fields` cannot strip: `input`, `error`, `enrich_error`. A row-level error marker is that row's only signal its data is incomplete, so filtering one away manufactures a row that looks whole - and the nonzero exit code names no row. `input` was already special-cased; the markers joined it rather than each command re-solving it.
- The single-token test harness is a **structural blind spot**, and it is why the wrong-client bug reached a live run past a green suite. `runWithMockSession` sets `SLACK_TOKEN`, which short-circuits `ResolveCredentials` (internal/auth/credentials.go) before it ever reads the credentials file - so `NewSessionClient` and `NewClient` hand back the same token and no assertion can tell which client made which call. `runWithTwoCredentials` (cmd/saved_enrich_test.go) is the way out: redirect `HOME`/`XDG_CONFIG_HOME` at a temp dir, write a real credentials.json with two workspaces and distinct tokens, set `SLACK_WORKSPACE` + `SLACK_WORKSPACE_ORG`, and leave `SLACK_TOKEN` unset. One httptest server is enough - route on the bearer/form token per endpoint. `TestSavedListEnrich_TokenRouting` was confirmed to fail on the one-client implementation with the same `enterprise_is_restricted` the live run produced; a routing test that passes both ways is worthless. Reach for this helper for any command that holds both clients.
- Test-design traps hit while writing the enrich suite, all three of which produced tests that passed for the wrong reason. (1) `for _, l := range lines { if !strings.Contains(l, "_meta") { fail } }` **passes on empty output** - it cannot tell "no rows plus a trailer" from "nothing at all". Assert row count and trailer contents (`assertFatalPage`). (2) Asserting `!errors.As(err, &*output.Error)` for a partial failure passes for *any* unrelated error; assert the `*output.ExitError` positively, code included (`assertExitError`). (3) A duplicate-input test where both duplicates get identical mock results cannot distinguish indexed outcomes from the old last-writer-wins map - give the duplicates *different* outcomes and assert one of each, leaving which-row-got-which unasserted since scheduling decides. Likewise an ordering test must gate on the later handler's **response completion**, not its entry: releasing at entry fires before the response is written or parsed, so both handlers then race and prove nothing. Mutation-test any such guard by breaking the implementation on purpose and confirming the test fails. Mechanics that matter: the fatal error is stored **unclassified** so `streamError` decides resumability the usual way (a rate limit or an auth failure stays resumable - the cursor still names a real page); classification happens inline only to sort item-local from systemic. `failPage` keeps the **first** substantive cause under a `sync.Once` so a sibling's `context.Canceled` can't overwrite it, and cancels a page-scoped child context; workers are **always joined** before outcomes are read, since returning early would leak goroutines still mutating the slice and still calling Slack. Workers re-check `ctx` after acquiring a semaphore slot, because a `select` with both cases ready can pick the slot over a done context. The partial-failure `*output.ExitError` is returned **after** `streamPages` finishes, never from the fetch closure - returning it there would suppress the very page holding the marked rows - so a later fatal page outranks an earlier page's partial status. `enrichConcurrency = 10` bounds in-flight items and is **not** a rate limiter: a cap of 1 would still spend 100 requests inside a minute, and quotas are per method, so dropping the `conversations.info` calls buys nothing in the history/permalink/replies buckets. A real fix is a command-lifetime per-method coordinator sharing `Retry-After` deadlines; deferred, and complicated by `findThreadReply` wrapping two methods behind one helper.
- Channel resolver uses `PaginateEach` for early exit (stops paginating once target is found). File cache at `<user-config-dir>/slack-cli/cache/channels-{teamID}.json` (24h TTL, configurable via `SLACK_CACHE_TTL`). In-memory cache (5min TTL) for session reuse. Reverse index (ID->name) for output enrichment.
- Name resolution is **member-first**: `resolveByPagination` scans `users.conversations` (Tier 3, the user's own conversations) before walking `conversations.list` (Tier 2, the whole workspace). Both walks go through the shared `scanForName` helper, same types (`public_channel,private_channel`), `exclude_archived=true`, limit 200. Measured cold (file cache deleted before each run) on a large Enterprise Grid org 2026-07-27, `channel info <name>`, request counts from `--trace` and inclusive of rate-limit retry attempts: two private member channels took 75 and 95 conversations.list requests (76s / 259s) and now take 3 users.conversations requests (2s / 1s); a public member channel now takes 5 (2s); a non-member channel was 64 requests / 192s and is now 6 member + 58 org / 103s; a miss was 190 / 460s (176 pages + 14 retries) and is now 6 + 190 / 462s. The whole member list is 6-7 pages at limit 200, so hits get an order of magnitude cheaper and the miss and non-member paths pay ~6 extra Tier-3 requests, about 2s. Why this matters more than it looks: the file cache is written **only** on a full walk, i.e. only when a name is NOT found, so an early-exit hit persists nothing and *every* cold-process member-name lookup used to pay a partial org crawl. Correctness rules that hold the design together: (1) the member scan's map goes into memory only, via `addChannelsToCache` (first-write-wins, no `saveFileCache`) - the file cache is contractually the complete `conversations.list` snapshot; (2) a member-scan error never aborts resolution, it falls through to the org walk and emits a `fallback` trace event (Grid org tokens get `enterprise_is_restricted`, older OAuth tokens can lack a scope); no context-cancellation special case is needed because the org walk's own `ctx.Err()` check returns the same error before its first request; (3) `scanForName` takes the **first** duplicate-name match, matching the map it builds - the old loop returned the *last* match while caching the first, so a repeat lookup of a duplicated name disagreed with the lookup that populated the cache; (4) only an **exhausted** org walk may call `setChannelMaps` (wholesale replace) and `saveFileCache`. Every other path extends the maps via `addChannelsToCache` (first-write-wins, no eviction): an early-exit hit saw only the pages before the match, and a file-cache hit is a complete but up-to-24h-old snapshot. Replacing in either case evicted names an earlier member scan had already resolved in the same process - typically a channel joined since the snapshot, the exact thing the file cache lacks - turning a resolved channel back into a cache miss and dropping its enrichment name.
- `exclude_archived` shrinkage on `conversations.list` is real but **not worth working around** (measured 2026-07-27, same Grid org, limit 200, full `--include-non-member` walk): `exclude_archived=true` returned 5330 channels in 176 requests (30/page); `exclude_archived=false` returned 16805 in 175 requests (96/page) of which 5698 were unarchived. Slack picks a fixed candidate window per request and filters after, so fetching archived rows to get fuller pages buys **1 request out of 176** while tripling the bytes. Page size does track `--limit` though (first page, exclude_archived: 13 items at limit 50, 26 at 100, 48 at 200; latency flat at ~320-360ms), so `--limit 200` roughly halves request count for a bulk `--include-non-member` walk. That stays opt-in - the `--limit` flag help now explains the shrink - rather than a sentinel default, which would change what one page means for anything scripting the current default. The resolver already used limit 200.
- User cache: file at `<user-config-dir>/slack-cli/cache/users-{teamID}.json` (24h TTL). Bulk-loads all users on first miss. Indexes by ID, email, and display name.
- Output enrichment: all output automatically resolves user/channel IDs to names from cache. Best-effort, adds `user_name`/`channel_name` fields.
- Internal APIs: `PostInternal` (JSON body) and `PostInternalForm` (form-encoded) for undocumented Slack endpoints (`saved.list`, `users.channelSections.*`).
- `api.Client` wraps `slack-go/slack` with separate bot/user token clients. `WithCookie` injects `d` cookie + Chrome user-agent via custom `http.RoundTripper` for `xoxc-` tokens.
- Desktop auth (`--desktop`) reads `xoxc-` tokens from Slack Desktop's LevelDB and decrypts the `d` cookie from its SQLite cookies DB using the Slack Safe Storage password (`SLACK_SAFE_STORAGE_PASSWORD` env var). Works with Enterprise Grid.
- `auth_method` field in credentials.json tracks how each workspace was authenticated (`"oauth"` or `"desktop"`). Used for context-specific error hints.
- Chrome TLS fingerprinting (`utls`) + user-agent on all cookie-based API requests. Required for Enterprise Grid.
- `E`-prefix workspace IDs are Enterprise Grid org-level contexts. `conversations.list` fails on these. The `T`-prefix workspace within the same org works.
- Internal APIs (`saved.list`, `users.channelSections.*`) require the `E`-prefix org token on Enterprise Grid. The `T`-prefix returns `team_is_restricted`.
- Drafts (`drafts.list`, `drafts.create`, `drafts.update`, `drafts.delete`) follow the internal-API pattern - plain `PostInternalForm` against `slack.com/api/*`. No workspace subdomain, slack_route, or special UA is required.
- Draft ghosting (`is_deleted=true` appearing on the server within seconds of create) is caused by Slack Desktop's Drafts-panel composer reconciliation: any server-side draft whose `blocks` array does not contain at least one `rich_text` block with non-empty `elements` gets tombstoned. Host, UA, TLS fingerprint, and `last_updated_client` stamp do not matter - block content does. `validateBlockShapes` (cmd/draft.go) rejects such payloads before they ship. The `auto-replace` path (`createReplacement` for tombstoned drafts) still exists as defense-in-depth for races where Desktop already marked a draft deleted between our `drafts.list` and `drafts.update`. Full write-up in `docs/draft-messages.md`.
- Draft stripping (distinct from ghosting): Slack Desktop's Drafts compose editor silently discards every non-`rich_text` top-level block when the user opens the draft to edit. A `[rich_text, section]` payload ships fine and the draft isn't tombstoned, but the user only ever sees the rich_text content. `validateBlockShapes` rejects any non-`rich_text` top-level block with `invalid_blocks` for this reason - section/divider/header/context alongside rich_text is silent content loss. Tables are the exception worth knowing: a `table` block is stripped in *top-level* blocks but SURVIVES in `attachments[].blocks[]`, rendering as an editable Table card - so the top-level rejection steers callers to attachments / `--table`. `data_table` (the interactive variant) is NOT draftable: it's stripped from the compose editor and a data_table-only draft is tombstoned, even in an attachment - so it's rejected outright (both verified end-to-end 2026-05-29; see docs/draft-messages.md). Draft payload model: stdin is a bare blocks array OR `{blocks, attachments}`; create/update carry an `attachments` form param; update is tri-state per field (provided replaces, absent preserves, `attachments:[]` clears); `--table FILE` builds a table attachment from CSV/TSV (header row bold, `--no-header` to disable); `createReplacement` carries attachments across the tombstone path; a draft survives reconciliation with a rich_text body OR a table attachment (attachments-only is fine). Caller-supplied attachment blocks are restricted to `table` (`validateAttachmentBlocks` rejects `data_table`); existing attachments round-trip verbatim. The canonical Slack-editor shape is a single top-level `rich_text` containing sibling `rich_text_section`/`rich_text_list`/`rich_text_preformatted`/`rich_text_quote` containers; multiple top-level `rich_text` blocks get flattened by Desktop before absorption runs.
- Section-before-container absorption (list/preformatted/quote): a `rich_text_section` directly followed by `rich_text_list`, `rich_text_preformatted`, or `rich_text_quote` gets its content absorbed into the following container (heading glues onto first bullet / merges into the code block / glues into the quote) unless the section's last text inline ends with `\n`. Slack's own compose editor sidesteps this by always emitting the trailing newline; agent-built payloads must too. `validateBlockShapes` enforces it: the section's element stream must terminate with a text inline whose `text` ends with `\n` (trailing empty text inlines are ignored). Non-text trailing inlines (link, emoji, user, channel, broadcast) don't satisfy the rule; append a final `{"type":"text","text":"\n"}`. The check spans top-level rich_text boundaries because Desktop flattens them. The reproduction matrix (2026-05-15, both negative and positive) lives in `docs/draft-messages.md`.
- Other draft gotchas: `blocks` is Block Kit JSON (top-level `rich_text` only; tables ride in `attachments` - validated via `validateBlockShapes` / `validateAttachmentBlocks` in cmd/draft.go); `client_last_updated_ts` must be exactly 7 decimal places on update/delete (helper `padDraftTS`, both pads and truncates); `drafts.list` has no cursor pagination, only `limit` + `has_more`. Update/delete auto-fetch the draft via `drafts.list` so callers don't juggle the ts themselves.
- Agent skill is checked-in static markdown at `skills/slack-cli/SKILL.md` (no generator). `allowed-tools: Bash(slack *)` assumes the binary is on PATH; the SKILL.md's prereq paragraph documents brew/`go install`. Distributed via the `skills` npm tool - `skills add tammersaleh/slack-cli -g` installs, `skills update` re-fetches from main. Content regression tests live at `cmd/skill_test.go` and read the file from disk. The install command appears in agent-facing hints in two places (`MissingBlocks` in internal/output/errors.go and `skillHint` in cmd/draft.go); changing it means changing both.
- `is_from_composer` must be `"true"` on create/update/replacement. Slack's channel composer reads `drafts.listActive`, which filters on this flag, so a draft with `false` lands in `drafts.list` but is invisible to the "Edit draft" flow - clicking it opens an empty compose box. The update path rewrites the flag unconditionally so pre-fix drafts heal on the next update rather than round-tripping stale `false`. Note: `drafts.listActive` is a separate endpoint from `drafts.list?is_active=true`; the CLI's `--active` flag hits the latter, not the former.
- `draft create` takes a variadic positional `Recipients []string`, not a single `<channel>`. `classifyRecipient` (cmd/draft.go) syntactically sorts each arg into channel vs user: user-shaped is `@name` / email / `Uxxx`-`Wxxx` id / user-profile URL; everything else (`#name`, bare name, `C/D/G/M` id, channel/message URL) is a channel. Bare names stay channels (backward compatible) - a person must be `@name`/email/id. `resolveDestination` then builds exactly one of `channel_id` (one channel recipient, current behavior) or `user_ids` (>=1 user recipient → DM/MPDM that need not exist yet; Slack opens the conversation on send). This is the fix for "can't draft to someone I've never DMed". Mixing channel+user, more than one channel, or `--thread`/`--broadcast` on a user destination are all fatal `invalid_input`. User recipients resolve fully before any write; every unresolved input is reported (`user_not_found`) and aborts - no best-effort, the destination must be atomic. user_ids preserve input order, deduped by resolved id; the caller is NOT auto-added. No local recipient cap (MPDMs cap near 8 - Slack rejects oversized lists; the codebase avoids baking inferred limits into undocumented endpoints). Set exactly one of channel_id/user_ids per destination - `normalizeDestinationsForWrite` strips user_ids only when channel_id is also present (the round-tripped drafts.list shape), which create never produces.
- IM channels have `is_member=false` in `conversations.list` responses - Slack doesn't populate a "member" concept for DMs. This is why no membership filter may key off that field; see the `channel list` sourcing entry below.
- `channel list --has-unread` reads unread state from the **undocumented internal** `client.counts`, not any list endpoint. Neither `conversations.list` nor `users.conversations` returns `unread_count` at all (raw-JSON checked), so the old filter compared a never-populated Go zero value and silently matched nothing - an agent asking "what's unread" was told "nothing". `fetchUnreadState` (cmd/channel.go) makes one `PostInternal` call and builds an id→state map before streaming starts, because `streamPages`' emit callback may not do network calls. Rows that match carry `has_unreads`/`mention_count`/`last_read` so the caller can see what it filtered on. Verified live 2026-07-27 against a Grid org: buckets are `channels`/`mpims`/`ims` (also `threads`, and `file_channels` which is an object not an array - unmarshal it as `[]row` and you get a type error); every public and private channel the user is in is present (1168 of 1168); `has_unreads` agreed with `latest > last_read` on all 1194 rows; DMs appear only while open (22 of 271 mpims, 64 of 350 ims), so **absent means no unread badge, not unknown** - which matches Slack's own sidebar. End-to-end the emitted set is exactly (conversations you're in) ∩ (unread per counts): 24 = 24 with zero unread conversations missing from the listing. Needs a session token, and on Grid the **E-org** context - the T-workspace token returns `team_is_restricted` on this endpoint, the mirror image of `users.conversations` failing `enterprise_is_restricted` on the E token. So `--has-unread` uses the same two-client split as `channel managers`, including capturing the session auth method before `NewClient` clobbers it. A bot token fails `session_token_required` (exit 2) rather than filtering on nothing.
- `filter_exhaustive` in the `_meta` trailer reports whether a client-side filter (`--query`, `--has-unread`) saw every page: true only when the stream ended without error and Slack reported no further cursor. Both filters imply a full walk on the member-only default (cheap since the endpoint swap) but never under `--include-non-member` (whole-workspace walk) or `--cursor` (a resume point `--all` can't combine with), and `user list` never widens on its own (a full directory measured 72 Tier-2 requests). Attached via `withClientSideFilter()`, a `streamOption` applied inside `streamPages` to **every** trailer including the failure paths - a marker set only on the happy path would claim a rate-limited search was exhaustive. The value is derivable from `error`+`has_more`; it exists because `has_more:true` reads as "there are more items" rather than "your filter didn't run on all of them", and that misreading is what made a partial `--query` look like `channel_not_found`.
- `channel list` sources its two modes from **different endpoints**, and that choice IS the member filter - there is no client-side membership check left in the command. Default → `users.conversations` (only the user's own conversations, Tier 3); `--include-non-member` → `conversations.list` (the whole workspace, Tier 2). `channelSource` (cmd/channel.go) binds endpoint name + fetch + row normalization together so the three can't drift. Measured on a large Enterprise Grid org 2026-07-27: default is 19 requests / ~5s at the real `--limit 100` default (10 requests / 3.5s at limit 200), against 178 requests / 8m15s / 5699 channels for the full `conversations.list` walk - the old default paginated all 5699 to emit 1787. Verified live before shipping, all on a desktop `xoxc` token against the T-workspace: (1) `users.conversations` **omits `is_member` entirely on the wire** for every type - raw-JSON checked, not just the decoded struct - so slack-go zeroes it and the printer would emit `is_member:false` for channels the user is plainly in; `normalizeUsersConversations` reports `true` for every row except `im`. Group DMs get `true` because being in one is a real membership and `conversations.list` is documented to report it as such (unverifiable on this org - it returns no mpims at all, see below); `im` keeps `false` because Slack sends no `is_member` for a 1:1 DM on either endpoint (raw-JSON checked on both), so there is no value to report and `false` is what this command has always emitted for DMs. (2) It also omits **`num_members`**, which `conversations.list` does send; the key is deleted rather than emitted as a false `0`. Those two are the only key differences between the endpoints' public-channel payloads. (3) The full ID-set diff explains both directions: +271 rows are group DMs (`conversations.list` returns **zero mpims** on this org even with `types=mpim`, so the old default omitted every group DM), and -18 rows are DMs whose user is **deactivated** (all 18 confirmed `deleted=true`; `is_open` is false for all 349 IMs `users.conversations` returns, so openness is not the discriminator). Zero non-member leakage: every uc row that `conversations.list` also returned had `is_member=true`. (4) Each `--type` works alone and the four single-type walks union **exactly** to the combined walk (1787, no dupes). (5) `exclude_archived=false` is a proper superset (608 vs 587 private). (6) Pages do not shrink under `exclude_archived` the way `conversations.list` pages do (193/191/193 of 200). (7) On the E-prefix org token `users.conversations` fails `enterprise_is_restricted` - but so does `conversations.list`, so no regression; `channel list` uses the T-workspace client either way. Scopes are unchanged (`channels:read,groups:read,im:read,mpim:read` already in `botScopes`), though only the session-token path was exercised live - an OAuth `xoxb` run is unverified. `UserID` stays empty (= the token's actor), which preserves whose membership is listed.
- Channel managers (`channel managers`, the "Managed by" list in the About tab) come from the **undocumented internal** `admin.roles.entity.listAssignments` (`entity_id=<channel>`), NOT any public API. `conversations.info` has no managers field, and the public `admin.roles.listAssignments` is org-admin gated (`invalid_actor` for a member token); the `.entity.` variant works with a plain member session token, so it follows the `PostInternalForm` pattern. Response: `{role_assignments:[{role_id, users:[…]}]}`. `Rl0A` is the Channel Manager role (observed via the Slack web client 2026-06-10). The command emits every returned assignment's users verbatim with `role_id` - it does NOT hard-filter to `Rl0A`, since a wrong/stale ID would silently report no managers (the worst failure for an undocumented endpoint); no human `role` label (the mapping is undocumented and would rot). Two-client split is mandatory on Enterprise Grid: the session client (E-org token, `NewSessionClient`) backs the internal call, but channel-*name* resolution uses the public client (T-workspace token, `NewClient`) because `conversations.list` is `team_is_restricted` on the org context. Order matters: `NewClient` clobbers `cli.authMethod`, so the command captures the session auth method and restores it before any `ClassifyError` on the internal call. No `slack_route` param needed (verified live). One row per assignment per user, no dedupe; empty list is success not an error.
- `section move` writes via `users.channelSections.channels.bulkUpdate`, which is **form-encoded** (`PostInternalForm`) with two JSON-string params, NOT a JSON body. `insert=[{channel_section_id, channel_ids:[…]}]` adds channels to a section; `remove=[{channel_section_id, channel_ids:[…]}]` removes them. Verified live 2026-07-09: `remove` pulls a channel out of a section (→ unsectioned); `insert` adds a channel ONLY if it is currently unsectioned - **insert on an already-sectioned channel is silently ignored**. So moving an already-sectioned channel requires insert (target) AND remove (source) in the same call. The old code sent a JSON body `{channel_sections:[{channel_ids_page}]}` (full membership replacement) via `PostInternal`; Slack returned `{"ok":true}` and did nothing - the classic undocumented-endpoint no-op. The command builds one insert for the target and one remove per source section (grouped, sorted by section id for determinism); a channel currently in no section contributes no remove entry, and `remove` is omitted entirely when empty (never send `[]` - unverified). Because the endpoint returns a bare `{"ok":true}` regardless, `moved_count` is NOT the requested count: after the write the command re-fetches sections and counts only channels now in the target and in no other section (`countMoved`). `--section` is validated against the fetched list up front (`section_not_found`) so a typo'd id fails loudly instead of no-opping. Stateful httptest mock in `cmd/section_test.go` (`sectionStore`) reproduces the insert/remove semantics faithfully (remove-then-insert, insert-ignored-while-sectioned) so the round-trip and verified count are actually exercised. Membership completeness: `users.channelSections.list` returns per-section `channel_ids_page` with `channel_ids`, `count`, and a `cursor` (the last channel id). `count` counts all-ever-assigned channels including archived/left ones; `channel_ids` returns only the user's currently-active membership. Verified 2026-07-09: sections like "Maintenance" report `count=70` but 21 active ids (the 49 missing are archived - all 21 returned are non-archived), while active-heavy sections ("INT Customers" 216, "EXT Customers" 191) return their full membership in one page. So `channel_ids` is complete for active channels at these sizes (no low page limit - 216 came back uncut) and `section move`/`list`/`channels`/`find` all operate on it correctly. Latent, unreproduced risk: a section with more *active* channels than the (unknown, >216) page limit would truncate `channel_ids`; there is no dedicated channels.list endpoint (`unknown_method`), and no section command paginates the per-section page. Not worth fixing until a workspace actually hits it.
- Helpers that surface errors: return raw errors, classify at the call site via `cli.ClassifyError`. Pre-classifying in a helper (e.g. `api.ClassifyError`) strips the auth-method hint added by `cli.ClassifyError`.
- Partial-failure commands return `*output.ExitError` (exit code only), not `*output.Error` (which also prints JSON to stderr). Per-item errors belong on stdout inline so the JSONL contract is preserved.
- `--timeout` threads through `cli.Context()`; every command's `Run` calls it and `defer cancel()`. Don't use `context.Background()` in commands - it bypasses the flag.
- `--trace` attaches a JSON-lines tracer via `api.WithTracer(ctx, api.NewJSONLinesTracer(w))`. `Paginate[Each]` and internal-API POSTs emit events. Slack-go bot-token calls are not yet instrumented - that'd need a transport hook.
- Output pipeline order: `filterFields` → `enrichTimestamps` → `EnrichFunc`. Enrichment runs after `--fields` so `--fields user` keeps the resolved `user_name`; enrichment is "extra," not user-filterable.
- Pagination cursors pass Slack's tokens through unchanged. For page-number APIs (`search.messages`, `search.files`, `files.list`), the cursor is the raw next page number as a string; `parsePageCursor` in cmd/search.go handles it.
- Resolver user cache: `LookupUser(id)` falls back to `users.info` on miss rather than bulk-loading via `users.list`. `ResolveUser(@name|email)` still bulk-loads since there's no single-user API that scans by display name. Single-user inserts via `addUserToCache` mirror the bulk `setUserMaps` indexing (users/usersByEmail/usersByName) so a follow-up name lookup in the same session hits memory cache.
- User custom-profile data (`user info --full`, `user manager-chain`): plain `users.info` returns `profile.fields: []` - it does NOT expand custom profile fields (Manager, Division, Department, Employee ID, GitHub Handle, Start Date, etc.). Those come only from `users.profile.get` with `include_labels=true`, which returns a map keyed by opaque field ID (`Xf…`) with `{value, alt, label}`. slack-go wraps it as `Bot().GetUserProfileContext(ctx, &slack.GetUserProfileParameters{UserID, IncludeLabels:true})`; it's a public method (no internal-API plumbing) but needs the `users.profile:read` scope - added to `botScopes` in `internal/auth/oauth.go`, so OAuth tokens issued before that must re-auth (desktop session tokens already have it). `--full` adds a top-level `custom_fields` object keyed by normalized snake_case label (additive; `profile.fields` stays `[]`), keys assigned deterministically (sorted by field ID, `_2`/`_3` collision suffixes, `field_<id>` for empty labels) so they don't flap with Go map iteration. User-ID-valued fields (Manager) resolve to `value_name` via `resolve.IsUserID` + `LookupUser`. The Manager field value is a user ID; the **Direct Reports** field exists but is empty in SCIM (no reliable downward data), so `manager-chain` is upward-only. `manager-chain` walks the field labeled "Manager" (case-insensitive; `--manager-field` override), one row per level, with a command-local profile cache (shared manager fetched once), depth cap 20 (`len(chain) >= managerChainMaxDepth`), seen-set cycle guard, and `stop_reason` on the terminal row (`no_manager` not an error; `invalid_manager_value`/`ambiguous_manager_field`/`cycle_detected`/`max_depth`/`profile_lookup_failed` are, counted in `_meta.error_count`). Rows emit `id` not `user_id` to dodge the printer's `user_id`→`user_name` auto-enrichment re-fetch (see `internal/resolve/enrich.go`); `manager_name` comes from the next hop's profile, no extra lookup. Error split via `isSystemicErr`: auth/`missing_scope`/`ratelimited`/`account_inactive`/`token_revoked` fail the whole command fast; other errors are per-item rows. Full write-up in `docs/profile-feature-plan.md`.
- Channel cache enrichment: `Enrich` resolves a `channel_id` via `LookupChannel`, which mirrors `LookupUser` - in-memory cache hit, else a single `conversations.info` call. It does NOT bulk-load `conversations.list`. This was the cause of a multi-minute hang: on a cold channel cache, naming the one channel in a `thread list`/`saved list` output used to paginate the entire workspace via `conversations.list` (Tier-2, rate-limited - ~13 min on a large Enterprise Grid org). `LookupChannel` failures are memoized per-process in `failedChannels` so a wide result set with an unresolvable ID doesn't re-hit `conversations.info` per row (this replaced `ensureChannelCache`'s empty-attempt storm guard). `addChannelToCache` only touches in-memory maps - it never calls `saveFileCache`, so a sparse enrich-time entry can't be persisted as if it were the complete `conversations.list` snapshot. Cache-completeness is safe because every `r.channels` read returns only on a name *hit* and never treats a miss as authoritative. `LookupChannelName` stays the pure cache read; `LookupChannel` is the networked path. Cost model: O(distinct unresolved IDs in output), not O(workspace size).
- Timeout E2E tests: assert on the classified error (`assertTimeoutError` in cmd/root_test.go - code `timeout`, detail keeping `"deadline exceeded"`), not elapsed time. `httptest.Server.Close` waits for in-flight handlers, so a passing test run looks slow even when the client returned promptly - and a handler that blocks on `<-r.Context().Done()` alone can hang `Close` forever, because the server does not reliably notice a client abandoning an unanswered request. Bound it: `select { case <-r.Context().Done(): case <-time.After(2*time.Second): }`.
- Slack URL input: `internal/slackurl` is a pure, route-aware parser (`Parse(s) (Ref, matched, err)`). Route picks the `Kind` (channel/message/user/file); the prefix letter only validates the extracted id. Tri-state result: `matched=false` = not a URL (caller falls through to ID/name handling); `matched=true, err!=nil` = URL-shaped but unusable (caller fast-fails, no pagination). A file URL embeds a uploader `U…` *and* an `F…`, so kind must come from the route, not a prefix scan. ts comes from the `p<digits>` segment (last 6 = micros); `thread_ts`/`cid` are validated and `cid` must match the path channel.
- URL resolution wiring: `ResolveChannel` accepts Kind channel/message (message permalink → channel, ts dropped - lossy, documented); `ResolveUser` accepts Kind user only. Both fast-fail on matched-but-bad URLs with sentinel `resolve.ErrBadURL`. Never flatten resolver errors to not_found at call sites - map via `channelResolveError`/`userResolveError` (cmd/resolve_errors.go) so a bad URL is `invalid_input`, not `channel_not_found`. `isBadURLErr` lets lenient sites (file list filters) keep name passthrough while still rejecting bad URLs.
- ts-shaped commands (`message get`, `reaction list`, `thread list`) take a single `Args []string` positional + custom `Help()`, parsed by the pure grammar helper `cmd/msgref.go` (`parseMessageRefs`/`parseThreadRef`). Two grammars: channel mode (`<channel> <ts>...`) vs URL mode (first arg is a self-contained message permalink → every arg must be one; may span channels). Mixing or empty is fatal `invalid_input`, fully validated before any API call. `resolveRefChannels` resolves distinct channels once. A reply permalink's `thread_ts` resolves to the parent thread. `message permalink` is intentionally NOT URL-enabled (it produces permalinks). These commands now always emit `channel_id` (both modes) so cross-channel output is unambiguous - assert it in channel-mode tests too. `msgRef` also carries `threadTS`, populated in URL mode from the permalink's `thread_ts` (empty otherwise); `message get` uses it to skip a `chat.getPermalink` round-trip in its thread-reply fallback.
- `message get` thread-reply fallback (`findThreadReply` in cmd/message.go): `conversations.history` NEVER returns thread replies, so a bare reply ts used to 404 (`message_not_found`) even though the message exists. On empty history the command discovers the parent `thread_ts` via `chat.getPermalink` (its permalink URL embeds `thread_ts=` for a reply; a reply permalink input already carries it, so `msgRef.threadTS` short-circuits the call) then fetches the reply via `conversations.replies` windowed with `oldest=latest=ts&inclusive=true`. Gotcha: `conversations.replies` ALWAYS prepends the thread parent regardless of the oldest/latest window, so the fallback scans the returned page for `ts == target` rather than assuming a single-message response (limit is 2 to cover parent+target). The helper returns raw errors; the call site classifies and collapses ONLY `message_not_found`/`thread_not_found` into the existing not-found row (via `isMessageMiss`) - auth/rate/`missing_scope`/network and any permalink-parse drift abort loudly instead of masking as not-found. Root-ts and genuinely-missing cases keep the original single `conversations.history` call.

### Desktop auth gotchas

- Chromium LevelDB keys are prefixed with origin (`_https://app.slack.com\x00\x01`), not bare key names. Must scan with `strings.Contains`.
- LevelDB values have a `0x01` binary prefix before JSON. Strip by finding first `{`.
- The `d` cookie MUST stay URL-encoded. Decoding `%2B` etc. causes `invalid_auth`.
- utls + Chrome fingerprint negotiates HTTP/2 via ALPN. `net/http.Transport` can't handle h2 with custom `DialTLSContext`. The Chrome transport (`chromeTLSTransport`) keeps a pooled `http2.Transport` whose `DialTLSContext` returns utls-fingerprinted conns and rejects non-h2 via `errHTTP1Negotiated`; the h1 pool is only used when that sentinel comes back. Both pools reuse conns per host, so repeated slack.com requests multiplex over one h2 connection.
- LevelDB is locked by running Slack. Must copy the directory first, remove the LOCK file, then open read-only.
- `SLACK_COOKIE` env var provides the `d` cookie for `xoxc-` token auth without stored credentials.
- No text output format. No `--format`, `--raw`, `--verbose`, or `--no-pager` flags.
- `--fields` for output field filtering. `--quiet` suppresses stdout entirely.
- Per-item errors go to stdout (one JSON per item). Fatal errors go to stderr. `ExitError` carries exit code without stderr output for partial-failure commands.
- Rate limit errors include `endpoint` field for diagnostics.
- `api.ClassifyError` never puts raw Go error text in `output.Error.Err`. That field is the value a paginated command copies into `_meta.error`, which SPEC documents as the field a consumer branches on, and `_meta` has no room for anything else - so it is a snake_case code in every case and the original text goes in `Detail` (stderr only). The **fallback itself** is a code (`unknown_error`), which is what makes the guarantee hold for error shapes nobody enumerated; the recognizers above it exist to name failures worth branching on, not to keep the field clean. Verified live 2026-07-27 that the field previously leaked, in order of how much they gave away: `json: cannot unmarshal string into Go struct field .channels of type []slack.Channel` (Go type names), `Post "https://slack.com/api/conversations.list": context deadline exceeded` (request URL), `slack server error: 500 Internal Server Error`, `invalid character '<' looking for beginning of value`. Codes added: `timeout` (`errors.Is(err, context.DeadlineExceeded)` - **must** be `errors.Is`, since the HTTP client wraps the deadline in a `*url.Error` mid-request while `FetchPage`'s pre-flight `ctx.Err()` returns it bare, and both reach here), `http_error` (`slack.StatusCodeError`, a non-200 that is not a 429 - slack-go turns 429 into `RateLimitedError` first), `parse_error` (`*json.SyntaxError`/`*json.UnmarshalTypeError`; reachable with Slack behaving normally - a captive portal or proxy answering 200 with HTML - and the code was already in use in cmd/saved.go, so no new vocabulary). All keep exit 1: `ExitNetwork` reads as "the network failed", which a self-imposed `--timeout` is not, and moving a documented exit code buys a consumer nothing. Not added: `context.Canceled` - there is no signal handling and `cancel()` is only deferred past the end of `Run`, so it is unreachable; it would land in `unknown_error` with its text in the detail. `unknown_error` deliberately is not `internal_error`, which is a real Slack API error string that passes through unchanged - two failures must not share a code. The invariant is pinned by `TestClassifyError_ErrIsAlwaysAStableCode` (regex over a corpus of every known shape), which catches a new leak that per-case tests cannot.
- Resumability needed no change for the new codes: `IsNonResumablePageError` only ever answers true for typed Slack errors, so `timeout`/`http_error`/`parse_error`/`unknown_error` all keep `has_more:true` plus the failed page's cursor - correct in each case (a deadline says nothing about the cursor, a 500 clears, a captive portal clears).

## Sandbox

GPG-signed git commits and `mise run` commands require `dangerouslyDisableSandbox: true` (Go build cache and GPG keyring access).
