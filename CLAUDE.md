# slack-cli

Slack CLI for agent/automation use. Go, kong, slack-go/slack. JSONL output,
OAuth + Desktop auth, bot and session (`xoxc-`) tokens, public and
undocumented internal APIs. Mostly reads; the writes are channel creation and
membership (`channel create`/`invite`/`kick`), drafts (including uploading a
file to attach to one), sidebar sections, and Slack Lists, and it never posts
a message.

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
- `saved.list` - list saved-for-later items (JSON or form body; page size is `limit`, 1-50, default 20; `count` is ignored)
- `client.counts` - get all channels the user is in (Enterprise Grid safe)
- `users.channelSections.list` - list sidebar sections
- `users.channelSections.create` - create a sidebar section (requires an `emoji` param, empty is fine)
- `users.channelSections.set` - rename a section / set its emoji (omitted keys are cleared)
- `users.channelSections.channels.bulkUpdate` - move channels between sections
- `users.channelSections.delete` - delete a section (verified live, not wrapped)
- `conversations.info` - channel metadata (used for name resolution in sections)
- `admin.roles.entity.listAssignments` - channel managers ("Managed by")
- `lists.records.list` - Slack List records with real cursor pagination (`list_id`, `limit` >= 2, `cursor`, optional `view_id`)
- `files.info` - read as raw JSON for lists (`list_metadata.schema`, `list_limits`), since slack-go's `File` drops both
- `lists.records.create` (`list_id`, `fields` JSON `[{key, value}]` by column **key**, scalar values; unknown keys are silently dropped and an out-of-range select value is stored as-is), `lists.cells.update` (`list_id`, `cells` JSON `[{row_id, column_id, <typed>}]` by column **id**; echoes changed cells only), `lists.records.info` (`list_id`, `id`) - returns `list`, `record`, `subtasks`
- `lists.create` (`name`, `description`), `lists.update` (`id`, any of `name`/`description`/`icon`), `lists.columns.create` (`list_id`, `type`, `name`, `options` JSON), `lists.columns.update` (`list_id`, `id` = column id, any of `name`/`options`), `lists.columns.delete` (`list_id`, `id`) - all but delete return `{"ok":true,"list":{...full file object...}}`; every select choice needs `value`, `label`, `color`

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
   is the only exception. This reviewer is the code review for this
   repo. A Codex review does not replace it and is not also required
   here. Codex stays available for planning.
7. Merge to main and push. The pre-push hook runs `mise run check`
   (test + lint); never bypass with `--no-verify`.
8. **Not done until installed and verified locally.** A push is not the
   finish line. After every release-cutting push, launch a background
   sub-agent to wait for the release and upgrade, so the main session isn't
   blocked babysitting CI. The sub-agent polls `gh release list` /
   `gh run list` until the new tag cuts, then runs
   `brew upgrade --cask tammersaleh/tap/tammersaleh-slack-cli` and reports the installed
   `slack version`; the main session then exercises the new behavior.
   Practical note (learned 2026-07-09): a general-purpose sub-agent is a
   poor poller - it repeatedly misreads its own role and bails early with
   "I'll wait for the notification" instead of looping. A background `Bash`
   poll loop (`run_in_background`) that re-invokes the main session on exit
   works reliably: loop `gh release view vX.Y.Z` with `sleep 90` until the
   tag cuts, then a second loop on the tap's raw `Casks/tammersaleh-slack-cli.rb` until
   `version "X.Y.Z"` appears (the cask artifact lags the git tag by minutes -
   `brew upgrade` says "already installed" until then), then upgrade and
   verify in the main session. The
   release flow: `release.yml` runs `verify` on the push, release-please
   then opens a release PR and merges it at once, and the merge push cuts
   the tag and runs GoReleaser, which pushes the Homebrew artifact -
   minutes, not instant. If `verify` fails, nothing releases: fix it and
   push again. The Bash
   tool caps a command at 10 minutes, so a poll loop must fit inside that
   and be re-launched, not written as a one-hour loop. The artifact is a
   **cask**, not a formula: it lives at `Casks/tammersaleh-slack-cli.rb` in the tap (not
   `Formula/`), and the version line is `version "x.y.z"`. Don't waste time
   grepping `Formula/`. raw.githubusercontent.com caches the cask for minutes after
   the tap commit lands (seen 2026-10-19: still `5.2.0` after the release run
   had succeeded); `gh api repos/tammersaleh/homebrew-tap/contents/Casks/tammersaleh-slack-cli.rb -q .content | base64 -d`
   reads the real file. Then install and verify against the real artifact,
   never your local build:
   - Binary change (`feat:`/`fix:`): `brew upgrade --cask tammersaleh/tap/tammersaleh-slack-cli`
     (plain `brew upgrade tammersaleh-slack-cli` can no-op if the cask tap hasn't synced;
     poll the tap's `Casks/tammersaleh-slack-cli.rb` for the new `version` first), confirm
     `slack version` is the version just cut, and exercise the new behavior
     with the installed `slack`. Note Slack rate-limits quickly under repeated
     manual calls - a `{"error":"rate_limited"}` row is the expected systemic
     fail-fast, not a bug; wait and retry. Capture command output to a file
     or pipe it straight into `jq`; never `OUT=$(slack ...)` then
     `echo "$OUT" | jq`. zsh's `echo` expands `\n` inside the JSON into a
     real newline and `jq` reports a control-character parse error that
     looks like a CLI bug (seen 2026-10-16 verifying `--markdown`, whose
     output is full of `"text":"\n"`).
   - Skill change (`SKILL.md`): run `skills update`, then confirm the
     expected text is in the installed skill.
   `chore:`/`docs:`/`test:`/`refactor:` cut no release, so there's no
   binary to install - but a `docs:` edit to `SKILL.md` still ships via
   `skills update`. Never report a change "done" off a push alone; if the
   release hasn't cut yet, wait and re-check.
9. Retrospective: review your approach and these instructions. Record
   what you learned in two places. A rule that changes how future
   sessions work goes in CLAUDE.md, in one or two sentences, under the
   matching "Architecture decisions" subsection. The measurements, dates,
   and the story of how it was found go in the matching file under
   `docs/`. Replace a rule that changed. Do not append a second bullet
   beside the old one.
10. Move on to the next feature.

Never ask permission to run this workflow. Committing, pushing to main, and
waiting out the release are the documented process, not a decision point -
asking "should I push?" wastes a round trip. The Autonomy section below is
the general rule; this is the specific one people trip over.

### One sub-agent per feature

Run each feature in its own sub-agent (`fork` when it needs the current
conversation, otherwise `general-purpose`) to keep test output, diffs, and
review transcripts out of the main thread's context. The sub-agent does steps
1 through 7 end to end: SPEC read, branch, red-green-refactor, `mise run
check`, commits, code review, merge, push. The main thread keeps the plan
file current, reads the agent's report, does step 8 (install and verify) and
step 9 (retrospective), then starts the next feature's agent. Give the agent
the SPEC.md section, the plan file path, and the sibling-repo pointers up
front so it starts cold without re-discovery.

The `feature-dev:code-reviewer` agent has no shell: it hand-traces goldens
and cannot check goldmark segmentation (indented code inside a quote, say),
so run `mise run test` after adding a golden it suggested before trusting
it. A `fork` sub-agent cannot spawn agents, so it cannot run the mandatory
`feature-dev:code-reviewer` pass itself (seen 2026-10-19: it substituted a
Codex review and pushed). Either run the feature in a `general-purpose` agent,
or have the main thread run the reviewer on the pushed range and ship any
fixes as a follow-up commit. The reviewer found two test gaps that Codex had
not.

Two mechanics seen 2026-11-04 running five feature agents in sequence: the
`feature-dev:code-reviewer` a `general-purpose` agent spawns runs as a
background task whose completion notification can land in the **main**
session, not the spawning agent, which then idles "waiting for the reviewer".
Tell the agent up front that the main thread will relay the verdict, and relay
it (findings plus nits) when the notification arrives. And `rebase.autostash`
is on in this repo's git config, so an agent that rebases onto a moved main
will stash and restore the main thread's uncommitted plan-file edit; have it
cherry-pick onto `origin/main` in a throwaway worktree instead, or confirm the
plan file's diff survived.

A `todo/` sweep on 2026-11-10 ran six batch agents in sequence, grouped by the code they touch rather than one per work order, after four parallel read-only triage agents classified every file as stale, obvious, or needing an owner decision. What held: triage agents re-measured claims and overturned three work orders' stated causes (a page-101 wrap to page 1, and two "Slack Connect" reports that were really sibling-workspace channels), so never brief a fix agent from a work order's own diagnosis alone. Every batch agent widened scope where its own change made a neighbouring path newly reachable (the fatal `_meta` trailer on every multi-input command came from this), which was right, and each also filed new work orders instead of fixing orthogonal finds. Two agents skipped the second reviewer pass when the follow-up commit was tests only. If `git push` fails with `sign_and_send_pubkey: signing failed ... agent refused operation`, the SSH agent is locked and only Tammer can unlock it: leave commits local, keep going with the next batch on top, and rebase onto `origin/main` before the eventual push because release-please will have landed a release commit meanwhile. `skills update` can sit silent for five minutes while it downloads, so run it in the background with its output in a file.

## Bug reports and todos

Both directories are untracked scratch space holding work orders, not
artifacts. Delete the file when the work is done - don't archive it or append
a resolution section. Findings belong in `docs/`, SPEC.md, and the commit
body, with the resulting rule in CLAUDE.md (see workflow step 9).

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
`release.yml` runs `verify` (the steps in `ci.yml` plus govulncheck) on
every push to main except the release commit itself. Release-please runs
only after `verify` passes. A commit with a version-bumping type opens a
release PR that merges at once. The merge push cuts a tag + GitHub
Release; GoReleaser builds binaries and pushes an updated cask to
`tammersaleh/homebrew-tap`. Nobody runs `git tag` by hand - and nobody
clicks Merge on the release PR either.

Two repo secrets drive it. `RELEASE_PAT` (fine-grained, the six CLI repos,
contents and pull requests write) opens and merges the release PR. It
cannot write the tap. `HOMEBREW_TAP_TOKEN` (tap repo only) pushes the cask.

The cask token is `tammersaleh-slack-cli`, not `slack-cli`: homebrew/cask
ships its own `slack-cli` (Slack's platform CLI, binary also `slack`), and
Homebrew resolves an installed cask by bare token, so even a qualified
`brew install --cask tammersaleh/tap/slack-cli` was attributed to
homebrew/cask and `brew bundle` twice replaced 5.9.1 with the official
4.13.0 (2026-11-04/05, exit 0, noticed only when `slack message` was an
unknown command). The tap carries `cask_renames.json` for the old token. Installing the new
token on a machine that had the old one leaves an empty
`Caskroom/slack-cli/.metadata` shell whose receipt names homebrew/cask;
`rm -rf` it before `brew bundle cleanup`, or cleanup uninstalls it through
the core cask definition. The binary stays `slack`.

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
- **The version number is never a reason to ask.** Pick the type from
  user-facing impact, commit it, push it. A major bump is not a bigger
  decision than a patch, it is the same decision with a different
  number, and `feat!:`/`fix!:` exist precisely so breaking changes ship
  with an honest version. If the change breaks a caller, mark it
  breaking and let release-please cut whatever that implies.

Do not ask about any of this. Not "should I cut 5.0.0?", not "is
`fix!:` right?", not "want me to downgrade this to avoid a major?".
Pick the type, say in one line why you picked it, and push. Being
wrong about a type is cheap and fixable; a round trip asking about it
is not. Reasoning that a release is "outward-facing", "irreversible",
"consequential", or "a big signal" is the exact rationalization this
rule exists to stop - a published version is the normal, expected
output of this repo, and none of those adjectives make it a question.

## Breaking changes are fine

Do not weigh breakage when choosing a design. Tammer is effectively the only consumer of this CLI, and he uses it through Claude, which reads SKILL.md and the help text and adapts to changed behavior. Pick the behavior that makes the CLI work best, even when it changes output shape, exit codes, defaults, or flag meanings. Never keep a worse behavior for compatibility, never add a compatibility flag or a deprecation period, and never present "it is a breaking change" as a cost when laying out options. Mark the commit `feat!:` or `fix!:` so the version is accurate, and update SKILL.md, SPEC.md, and the help text in the same change, because those are how the consumer learns the new behavior. (Tammer, 2026-11-10)

## Autonomy

Work through features independently. Never stop to ask "should I
continue?" or "want me to keep going?" - the answer is always yes.
After giving a status summary, keep working. Only escalate when:

- A design decision isn't covered by `SPEC.md`.
- Something feels wrong (scope creep, Slack API limitation, etc.).

Neither exception covers releases. Commit type, version bump, pushing
to main, and cutting a release are all part of the workflow, never
questions - see "The version number is never a reason to ask" above.
`AskUserQuestion` is the wrong tool for every one of them, and dressing
the question up as "flagging a decision" or "surfacing a consequence"
rather than asking permission is the same thing wearing a hat: if the
turn ends with work sitting uncommitted or unpushed pending an answer,
the rule was broken. Finish the workflow, then report what shipped.

## Project structure

```
cmd/
  root.go        # CLI struct, global flags, NewPrinter/NewClient/NewResolver
  auth.go        # auth login/logout/status
  bookmark.go    # bookmark list
  cache.go       # cache info/clear
  channel.go     # channel list/info/members
  channel_create.go     # channel create
  channel_membership.go # channel invite/kick
  dnd.go         # dnd info
  draft.go       # draft list/create/update/delete (internal API), block validation
  draft_files.go # --file upload, orphan cleanup, and the file summary on draft rows
  emoji.go       # emoji list
  file.go        # file list/info/download
  list.go        # list info/schema/records/find (internal API); list_render.go holds the pure schema join
  list_write.go  # list create/update, list column add/rename/set-choices/delete; list_columns.go holds the pure spec/choice/resolution helpers
  list_records.go # list record add/update; list_values.go holds the pure COLUMN=VALUE grammar and per-type conversion
  message.go     # message list (alias: read) / get
  permalink.go   # message permalink
  pin.go         # pin list
  presence.go    # presence get
  reaction.go    # reaction list
  saved.go       # saved list/counts (internal API)
  search.go      # search messages/files
  section.go     # section list/channels/find/move/create/rename (internal API)
  status.go      # status get
  thread.go      # thread list (alias: read)
  user.go        # user list/info
  usergroup.go   # usergroup list/members
  version.go     # version
  workspace.go   # workspace info
  paginate.go    # streamPages, pageCursorFetch: the shared streaming loop and trailer
  abort.go       # abortWithTrailer for multi-input commands
  bounded.go     # runBounded worker loop
  msgref.go      # <channel> <ts> and permalink argument grammar
  recipients.go  # classifyRecipient/splitRecipients for mixed channel and user args
  resolve_errors.go # resolver error mapping, resolveInputs
  searchquery.go # from:/to:/in: user modifier rewrite
internal/
  api/           # Slack API client (Client, Paginate[T], ClassifyError,
                 #   PostInternal, PostInternalForm, WithCookie)
  auth/          # credentials CRUD, OAuth flow, Desktop extraction
  output/        # Printer (JSONL + enrichment), Meta, Error with exit codes
  resolve/       # channel/user resolution with 3-tier cache + enrichment
  mdblocks/      # Markdown to header, divider, and rich_text blocks
  slackurl/      # Slack URL parser
docs/            # measurements and reasoning behind the rules in this file, plus feature plans
```

## Testing

Tests live next to the code they test (`foo_test.go`). Use table-driven tests. Mock the Slack API via httptest servers - set `SLACK_TOKEN` and `SLACK_API_URL` env vars in tests. Use `runWithMock(t, handler, args...)` helper in `cmd/channel_test.go` for end-to-end command tests. For session-token (xoxc) flows use `runWithMockSession` in `cmd/saved_test.go`.

Test helpers call `isolateTestEnv(t)` (in `cmd/channel_test.go`) to clear workspace/cookie/fields env vars that could leak from the developer's shell. Tokens (`SLACK_TOKEN`, `SLACK_USER_TOKEN`) are intentionally left alone - helpers and individual tests set those explicitly for the scenario under test. If you add a new test helper that stands up its own `httptest.Server`, call `isolateTestEnv(t)` at the top.

For functions only accessible within `package cmd` (like `padDraftTS`), put unit tests in a file that uses `package cmd` rather than `package cmd_test` - see `cmd/padding_test.go` for the pattern.

Go is 1.27 (`.mise.toml`, `go.mod`). golangci-lint is v2 with its default linters and no config file, so errcheck applies to tests too. mise builds golangci-lint and govulncheck with the pinned Go. After a Go bump, rebuild both with `mise install --force` or they cannot read the new standard library.

Every build uses the `http2legacy` build tag. On Go 1.27, x/net reimplements `http2.Transport` on top of net/http, and the tag keeps x/net's own HTTP/2 client, the one the Chrome TLS transport was verified against Slack with. `.mise.toml` sets `GOFLAGS=-tags=http2legacy` for build, test, lint, and govulncheck, and mise-action exports it in CI. `.goreleaser.yml` sets the tag again for the release. A bare `go build` or `go test` outside mise compiles the other implementation, so use `mise run` or `mise exec`. Check a binary with `go version -m ./slack`. Removing the tag is part of moving the transport to net/http.

Dependabot ignores `golang.org/x/net`, because a newer x/net may drop the legacy implementation and the tag would then do nothing, with no error. Bump x/net by hand. govulncheck in `verify` fails the release when a called x/net vulnerability appears, and that is the signal to bump. Another module's bump can still raise x/net through its own requirement. `TestHTTP2LegacyImplementation` (internal/api/http2legacy_test.go) catches that: it fails when the tests were built without the tag, or when x/net under the tag no longer compiles `transport.go`. It runs in `mise run test`, so it fails `verify` and blocks the release. A bare `go test ./...` without `-tags http2legacy` fails it on purpose. The same check by hand:

```bash
mise exec -- go list -deps -f '{{if eq .ImportPath "golang.org/x/net/http2"}}{{.GoFiles}}{{end}}' ./cmd/slack
```

## Git

This is a personal project. The workflow is entirely local: commit
on a branch (or directly on main for small fixes), merge to main,
push. Don't open pull requests. `gh pr create` is not part of any
flow here. The only PRs that exist are release-please's and Dependabot's,
and both are created and merged without human involvement.

Dependabot opens monthly grouped PRs for `gomod` and `github-actions`, and
`pr.yml` merges them once tests pass. A merged bump cuts no release by
itself. It ships with the next pushed release. Major versions are ignored,
so bump those by hand.

Three workflow files: `ci.yml` holds the test steps and is reusable only,
`pr.yml` calls it for pull requests and merges Dependabot PRs, `release.yml`
calls it as `verify` for pushes to main. Jobs in `ci.yml` must stay at
`contents: read`. A called workflow cannot request more than its caller
grants.

## Output

JSONL to stdout. Every command emits one JSON object per line, ending with a `_meta` trailer. Errors as JSON to stderr. See `SPEC.md` for the full output model.

## Architecture decisions

Each bullet is the current rule. The measurements, dates, and reasoning behind it live in the file under `docs/` named at the end of each subsection. Read that file before changing the code a rule covers.

### Basics

- No config file. All config via flags/env vars. Kong handles precedence.
- Workspaces keyed by `TeamID` (stable) not `TeamName` (mutable) in credentials.json.
- User resolution: ID, email, or `@name` (handle / display name / real name, exact and case-insensitive). Name lookups rank the cached directory and fail `ambiguous_user` on a tie - see the user name ranking bullet below. `users.lookupByEmail` fails with `xoxc-` tokens on Enterprise Grid (scope limitation).
- Channel resolution: first match wins on name collision. No ambiguity errors.
- Channel list defaults to member-only. `--include-non-member` to expand.
- Single-page pagination by default. `--cursor` to continue, `--all` to fetch everything.
- Output enrichment: all output automatically resolves user/channel IDs to names from cache. Best-effort, adds `user_name`/`channel_name` fields.
- Internal APIs: `PostInternal` (JSON body) and `PostInternalForm` (form-encoded) for undocumented Slack endpoints (`saved.list`, `users.channelSections.*`).
- `api.Client` wraps `slack-go/slack` with separate bot/user token clients. `WithCookie` injects `d` cookie + Chrome user-agent via custom `http.RoundTripper` for `xoxc-` tokens.
- Agent skill is checked-in static markdown at `skills/slack-cli/SKILL.md` (no generator). `allowed-tools: Bash(slack *)` assumes the binary is on PATH; the SKILL.md's prereq paragraph documents brew/`go install`. Distributed via the `skills` npm tool - `skills add tammersaleh/slack-cli -g` installs, `skills update` re-fetches from main. Content regression tests live at `cmd/skill_test.go` and read the file from disk. The install command appears in agent-facing hints in two places (`MissingBlocks` in internal/output/errors.go and `skillHint` in cmd/draft.go); changing it means changing both.

### Auth and token contexts

- Desktop auth (`--desktop`) reads `xoxc-` tokens from Slack Desktop's LevelDB and decrypts the `d` cookie from its SQLite cookies DB using the Slack Safe Storage password (`SLACK_SAFE_STORAGE_PASSWORD` env var). Works with Enterprise Grid.
- `auth_method` field in credentials.json tracks how each workspace was authenticated (`"oauth"` or `"desktop"`). Used for context-specific error hints.
- Chrome TLS fingerprinting (`utls`) + user-agent on all cookie-based API requests. Required for Enterprise Grid.
- `E`-prefix workspace IDs are Enterprise Grid org-level contexts. `conversations.list` fails on these. The `T`-prefix workspace within the same org works.
- Internal APIs (`saved.list`, `users.channelSections.*`) require the `E`-prefix org token on Enterprise Grid. The `T`-prefix returns `team_is_restricted`.
- Two-client split on Enterprise Grid: internal APIs want the session (E-org) token, while channel-name resolution and `chat.getPermalink` want the workspace (T) client. `saved list --enrich`, `channel managers`, `channel list --has-unread`, `draft create`, and `list record` channel values build the workspace client only when they need it, so an org-only credential keeps working. `NewClient` must precede `NewResolver`, and it clobbers `cli.authMethod`, so capture the session method and restore it. `SLACK_TOKEN` outranks stored credentials for both clients and collapses them onto one token.
- `cli.authMethod` is one ambient value that `ClassifyError` reads for its re-auth hint. A command holding two clients sets it per stage.
- `auth login`'s stderr hint reads `SLACK_WORKSPACE`/`SLACK_WORKSPACE_ORG` from the environment (not `cli.Workspace`), matches by exact credentials map key, and lists every candidate workspace sorted by team name then id. `--workspace` is team-ID only.

Details are in `docs/auth.md`, `docs/saved-items.md`, and `docs/draft-internals.md`.

### Desktop auth gotchas

- Chromium LevelDB keys are prefixed with origin (`_https://app.slack.com\x00\x01`), not bare key names. Must scan with `strings.Contains`.
- LevelDB values have a `0x01` binary prefix before JSON. Strip by finding first `{`.
- The `d` cookie MUST stay URL-encoded. Decoding `%2B` etc. causes `invalid_auth`.
- utls + Chrome fingerprint negotiates HTTP/2 via ALPN. `net/http.Transport` can't handle h2 with custom `DialTLSContext`. The Chrome transport (`chromeTLSTransport`) keeps a pooled `http2.Transport` whose `DialTLSContext` returns utls-fingerprinted conns and rejects non-h2 via `errHTTP1Negotiated`; the h1 pool is only used when that sentinel comes back. Both pools reuse conns per host, so repeated slack.com requests multiplex over one h2 connection.
- LevelDB is locked by running Slack. Must copy the directory first, remove the LOCK file, then open read-only.
- `SLACK_COOKIE` env var provides the `d` cookie for `xoxc-` token auth without stored credentials.

### Pagination and streaming

- `api.Paginate[T]` handles cursor-based pagination with rate-limit retry (5 attempts, respects Retry-After). `api.PaginateEach[T]` adds per-page callback with early exit. `api.FetchPage[T]` is the single-page primitive all three share - use it for streaming pagination, where pages print as they arrive. `maxAttempts = 5` means 1 initial call + 4 retries, no wait after the last failure; `RateLimitExhaustedError.Attempts` counts calls, not retries. All accept an endpoint name for diagnostics.
- Every streaming list command goes through `streamPages` (cmd/paginate.go), which ends stdout with exactly one `_meta` trailer, including on mid-stream failure. `emit` is output-only: no network calls and no fallible checks, which belong in the fetch closure. An `*output.Error` from the closure, or a Slack code in `api.nonResumableErrors`, makes the trailer terminal (`has_more:false`, no cursor). Anything else keeps `has_more:true` and the failed page's cursor. `pageCursorFetch` adapts the page-number APIs and takes the next page from the response, never a counter. `user list` builds a fresh `slack.UserPagination` per attempt.
- Resume-cursor disposition lives in `api.nonResumableErrors` and `api.IsNonResumablePageError` (internal/api/errors.go), consulted only by `streamError`. An error belongs there only when it voids the continuation itself: `invalid_cursor`, the request-shape family, `unknown_method`/`method_deprecated`, and `thread_not_found`. Auth, scope, permission, and membership errors and every unknown code stay resumable. `ClassifyError` decides the exit code independently. Only a typed `slack.SlackErrorResponse` counts as a Slack error.
- Cursors are endpoint-specific, and Slack does not reject a cursor from the other list endpoint: it answers from a different result set or returns zero rows. SPEC tells callers to discard a cursor when flags change. `--all` and `--cursor` are mutually exclusive.
- Slack's page-number APIs wrap to page 1 after page 100. `pageCursorFetch` refuses a page above `slackMaxPage` before the request with a terminal `search_page_limit` or `file_page_limit`.
- `_meta.total` on the page-number commands is Slack's `paging.total` from the first page fetched (`pageWalk.report()`). The CLI does not compare it to rows emitted.
- `filter_exhaustive` in the trailer is true only when a client-side filter (`--query`, `--has-unread`) saw every page. `withClientSideFilter()` attaches it to every trailer, failures included. Both filters imply a full walk on the member-only default, never under `--include-non-member` or `--cursor`.
- Pagination cursors pass Slack's tokens through unchanged. For page-number APIs (`search.messages`, `search.files`, `files.list`), the cursor is the raw next page number as a string; `parsePageCursor` in cmd/search.go handles it.
- Resumability needed no change for the new codes: `IsNonResumablePageError` only ever answers true for typed Slack errors, so `timeout`/`http_error`/`parse_error`/`unknown_error` all keep `has_more:true` plus the failed page's cursor - correct in each case (a deadline says nothing about the cursor, a 500 clears, a captive portal clears).

Details are in `docs/pagination.md`.

### Errors, exit codes, and rate limits

- Helpers that surface errors: return raw errors, classify at the call site via `cli.ClassifyError`. Pre-classifying in a helper (e.g. `api.ClassifyError`) strips the auth-method hint added by `cli.ClassifyError`.
- Partial-failure commands return `*output.ExitError` (exit code only), not `*output.Error` (which also prints JSON to stderr). Per-item errors belong on stdout inline so the JSONL contract is preserved.
- Per-item errors go to stdout (one JSON per item). Fatal errors go to stderr. `ExitError` carries exit code without stderr output for partial-failure commands.
- Rate limit errors include `endpoint` field for diagnostics.
- `--timeout` threads through `cli.Context()`; every command's `Run` calls it and `defer cancel()`. Don't use `context.Background()` in commands - it bypasses the flag.
- `--trace` attaches a JSON-lines tracer via `api.WithTracer(ctx, api.NewJSONLinesTracer(w))`. `Paginate[Each]`/`FetchPage` (`page`), `api.Call` (`call`), and internal-API POSTs (`internal_api`) emit events. The resolver's own requests are instrumented: the `users.info`/`conversations.info` enrichment lookups are `call` events and the user-directory load is `page` events. Slack-go bot-token calls outside those helpers (`message get`'s `conversations.history`, `users.lookupByEmail`) are not instrumented - that'd need a transport hook.
- `api.ClassifyError` always puts a snake_case code in `output.Error.Err`, never raw Go error text. The original text goes in `Detail`. The fallback is `unknown_error`. The local codes are `timeout` (match with `errors.Is`), `http_error`, and `parse_error`, all exit 1. `TestClassifyError_ErrIsAlwaysAStableCode` pins this.
- `api.RateLimitWait` recognizes both rate-limit shapes: `*slack.RateLimitedError` (HTTP 429) and an `ok:false` body whose code is `ratelimited` or `rate_limited`. Both retry and classify `rate_limited`, exit 3.
- `postInternal` returns `*slack.RateLimitedError` for both shapes and makes one attempt. `Client.RetryInternal`/`RetryInternalForm` add retry for the single-shot reads and the two set-state section writes. Creates, draft writes, and every `lists.*` write stay single-shot on purpose. Paginated internal calls stay on plain `PostInternal*` because `FetchPage` already retries them.
- `abortWithTrailer` (cmd/abort.go) is how every multi-input command that is not a `streamPages` stream stops on a fatal error: a `{"_meta":{"has_more":true,"error":"<code>","error_count":N}}` trailer, then the classified error on stderr. Decide fatal versus per-row with `isSystemicErr`, not an exit-code compare. Inputs resolve up front through `resolveInputs`.
- `missing_scope` is in `api.authErrors` (exit 2) and stays resumable. `no_permission` and `not_allowed_token_type` stay exit 1. `cli.ClassifyError` gives `missing_scope` its own hint (`missingScopeHint`).

Details are in `docs/errors-and-rate-limits.md`.

### Output

- No text output format. No `--format`, `--raw`, `--verbose`, or `--no-pager` flags.
- `--fields` for output field filtering. `--quiet` suppresses stdout entirely.
- `output.alwaysKeptFields` (internal/output/output.go) is the set `--fields` cannot strip: `input`, `error`, `enrich_error`, `candidates` (an `ambiguous_user` row's matches), and `replaced_draft_id`. A row-level error marker is that row's only signal its data is incomplete, so filtering one away manufactures a row that looks whole - and the nonzero exit code names no row. `input` was already special-cased; the markers joined it rather than each command re-solving it.
- `Printer.KeepWhenTrue` keeps a key through `--fields` only while its value is `true`. `user info` and `user manager-chain` set it to `deleted`, so a deactivated account is never printed as an empty row.
- Output pipeline order is `projection.filter` → `enrichTimestamps` → `EnrichFunc` → `projection.dropBorrowed` (internal/output/output.go). `--fields` can name a derived key (`user_name`, `ts_iso`): the filter borrows the source keys, enrichment derives from them, and the borrowed keys are dropped. `resolve.EnrichSources()` supplies the reverse mapping. Only top-level names reverse. An unknown name selects nothing with no error.
- `file download` emits both `size` (what `files.info` claims) and `bytes_written` (measured by a `countingWriter` around the destination file), because Slack reports `size: 0` for every Slack List while the export body is hundreds of KB; with `-o -` raw bytes go to stdout and no row is emitted, so there is nothing to count there.

Details are in `docs/output.md`.

### Resolver and caches

- Channel resolver uses `PaginateEach` for early exit (stops paginating once target is found). File cache at `<user-config-dir>/slack-cli/cache/channels-{teamID}.json` (24h TTL, configurable via `SLACK_CACHE_TTL`). In-memory cache (5min TTL) for session reuse. Reverse index (ID->name) for output enrichment.
- Channel-name resolution is member-first: `users.conversations`, then `conversations.list`, both through `scanForName`, first duplicate-name match wins. The file cache holds only a complete `conversations.list` snapshot, and only an exhausted org walk writes it (`setChannelMaps`, `saveFileCache`). Every other path extends memory through `addChannelsToCache` (first-write-wins). A member-scan error falls through to the org walk.
- A proven channel-name miss is answered from memory for the 5-minute memory TTL (`channelsComplete`). The proof needs an exhausted org walk merged with a successful member scan. It is process-local.
- `exclude_archived` shrinks `conversations.list` pages. Do not work around it. `--limit 200` roughly halves the request count of a bulk `--include-non-member` walk and stays opt-in.
- `LookupChannel` is a cache hit or one `conversations.info` call, never a `conversations.list` bulk load. `addChannelToCache` touches memory only. `LookupChannelName` is the pure cache read.
- User cache: file at `<user-config-dir>/slack-cli/cache/users-{teamID}.json` (24h TTL). Bulk-loads all users on first miss. Indexes by ID, email, handle, and label (display name and real name).
- `bulkLoadUsers` (internal/resolve/user.go) walks `users.list` through `api.Paginate` at limit 200 with a fresh `slack.UserPagination` per attempt, so the walk shows in `--trace` and gives up after 5 attempts on one page. Nothing is cached unless the whole walk succeeds. Email order is cache first, then `users.lookupByEmail`.
- `LookupUser` and `LookupChannel` are best-effort `api.Call`s. An exhausted rate limit sets a per-method stop (`usersInfoDown`, `conversationsInfoDown`) and other failures are memoized per ID. `FetchUser` is the entry point for a command whose answer is the user (`user info`, `status get`): same cache and request, but it never consults the memo or the stop.
- Resolver user cache: `LookupUser(id)` falls back to `users.info` on miss rather than bulk-loading via `users.list`. `ResolveUser(@name|email)` still bulk-loads since there's no single-user API that scans by display name. Single-user inserts via `addUserToCache` go through the same `indexUser` as the bulk `setUserMaps`, but a cache they created is partial (`usersComplete` false) and never answers a name or proves an email absent: the next `ResolveUser` name or email lookup loads the directory.
- User name ranking (`userIDByName`): the handle tier beats the label tier, active accounts beat deactivated ones, one candidate left is the answer, and a tie is `*resolve.AmbiguousUserError`, surfaced as `ambiguous_user` with `candidates`. Matching is exact and case-insensitive. `usersComplete` marks a full directory, and a partial cache never answers a name.
- Slack URL input goes through `internal/slackurl.Parse(s) (Ref, matched, err)`. The route picks the kind and the ID prefix only validates. `matched=false` means not a URL. `matched=true` with an error means fail fast with no pagination. `/lists/<team>/<F…>` is kind file.
- URL resolution wiring: `ResolveChannel` accepts Kind channel/message (message permalink → channel, ts dropped - lossy, documented); `ResolveUser` accepts Kind user only. Both fast-fail on matched-but-bad URLs with sentinel `resolve.ErrBadURL`. Never flatten resolver errors to not_found at call sites - map via `cli.channelResolveError`/`userResolveError` (cmd/resolve_errors.go) so a bad URL is `invalid_input`, not `channel_not_found`.
- Resolver errors are three kinds: `resolve.ErrBadURL` (`invalid_input`), `resolve.ErrNotFound` (`channel_not_found` or `user_not_found`), and the raw error of a failed lookup, which goes through `cli.ClassifyError`. Map them with `cli.channelResolveError`/`userResolveError` (cmd/resolve_errors.go). `isInputResolveErr` is the fail-closed gate to a per-input row. Two-client commands classify a failed channel lookup under `cli.withAuthMethod(workspaceAuthMethod, ...)`. An email is a clean miss only when the directory loaded without it and `users.lookupByEmail` answered `users_not_found`.

Details are in `docs/resolver.md`.

### Messages and threads

- `message list` sends `oldest`/`latest` on every `conversations.history` request of a walk: the bounds are paging state, not a first-page filter. When `oldest` is set and `latest` is not, the command sends `latest=9999999999.999999` (`historyOpenLatest`, cmd/message.go) so every mode is one newest-first stream. `historyWindowMux` in `cmd/message_test.go` encodes the measured semantics.
- ts-shaped commands (`message get`, `reaction list`, `thread list`) take `Args []string` parsed by `cmd/msgref.go`. Channel mode is `<channel> <ts>...`. URL mode is all message permalinks and may span channels. Mixing or empty is `invalid_input` before any API call. They always emit `channel_id`. `message permalink` is not URL-enabled.
- `conversations.history` never returns thread replies. `message get` falls back through `findThreadReply` (cmd/message.go): `chat.getPermalink` for the parent `thread_ts`, then `conversations.replies`, scanning for the exact ts because the parent is always prepended. Only `message_not_found`/`thread_not_found` collapse to the not-found row.

Details are in `docs/messages.md`.

### Saved items

- `saved list --enrich` resolves each message through `lookupSavedMessage` (cmd/saved.go): history windowed on the exact ts, then `findThreadReply`. Match the exact ts, record resolution in `enrichOutcome.resolved` (never infer it from empty text), and index outcomes by input position.
- `saved list --limit` is `saved.list`'s `limit`, range 1-50, sent on every page. Slack ignores `count`. Above 50 is `invalid_input`. `saved counts` asks for `limit: 1`.
- Enrichment errors are fail-closed: `itemLocalEnrichErrors` lists the four codes that mark one row with `enrich_error`. Everything else, including a code Slack has not invented, voids the page.
- Enrichment retry is page-granular. slack-go does not retry these lookups, so `api.FetchPage` retries the whole page and an exhausted error names `saved.list`.
- `enrichConcurrency = 10` bounds in-flight items. It is not a rate limiter.

Details are in `docs/saved-items.md`.

### Sections

- `section channels` and `section find` read names through `lookupSectionChannels` (cmd/section.go), fail-closed. Only `channel_not_found` marks a row (`{id, enrich_error}`, exit 1). Anything else voids the read with zero rows and a trailer. Bounded worker loops use `runBounded` (cmd/bounded.go). Do not write another copy.
- `section rename` and `section emoji` write via `users.channelSections.set`, which clears any key omitted from the form. `setSection` reads the current values, sends both `name` and `emoji`, re-fetches, and fails `rename_failed` or `emoji_failed` on a mismatch. `users.channelSections.create` needs an `emoji` key (empty is fine).
- Emoji are sent as bare names. `normalizeEmojiArg` strips one surrounding colon pair from user input and touches nothing else. The server validates.
- `section move` writes via `users.channelSections.channels.bulkUpdate`, form-encoded with JSON-string `insert` and `remove` params. An insert on an already-sectioned channel is ignored, so a move sends an insert for the target and a remove per source section. Omit `remove` when empty. `moved_count` comes from a re-fetch (`countMoved`).

Details are in `docs/sections.md`.

### Channels

- IM channels have `is_member=false` in `conversations.list` responses - Slack doesn't populate a "member" concept for DMs. This is why no membership filter may key off that field; see the `channel list` sourcing entry below.
- `channel list` reads `users.conversations` by default and `conversations.list` under `--include-non-member`. The endpoint is the member filter (`channelSource`, cmd/channel.go). `users.conversations` omits `is_member` and `num_members`, so `normalizeUsersConversations` reports `is_member:true` for every row except `im` and deletes `num_members`.
- `channel list --has-unread` reads the internal `client.counts` once before streaming, on the session client (E-org context on Grid), with the two-client split. A conversation absent from the counts has no unread. A bot token fails `session_token_required`.
- `channel managers` reads the internal `admin.roles.entity.listAssignments` on the session client and resolves channel names on the workspace client. It emits every assignment with its `role_id` and does not filter to `Rl0A`.
- `channel invite` and `channel kick` (cmd/channel_membership.go) resolve everything before the first write, then make one `api.Call` per (channel, user) pair. Error dispositions are explicit allowlists (`membershipOp.pairErrors`/`channelErrors`): the idempotent code is a `status`, a pair code marks one row, a channel code marks that channel's remaining users, and everything else aborts with a trailer.
- `channel create` (cmd/channel_create.go) is one `conversations.create` through `api.CallOnce` on the workspace client, never retried. It strips one leading `#` and nothing else. Errors are fatal on stderr with empty stdout. An `unknownCreateOutcome` code gets a "may or may not exist" hint.

Details are in `docs/channels.md`.

### Drafts

- Drafts (`drafts.list`, `drafts.create`, `drafts.update`, `drafts.delete`) follow the internal-API pattern - plain `PostInternalForm` against `slack.com/api/*`. No workspace subdomain, slack_route, or special UA is required.
- `validateBlockShapes` (cmd/draft.go) is the gate for draft payloads. Top-level blocks may be `rich_text`, `header`, and `divider` only. Tables ride in `attachments` (`table` only, `data_table` is rejected). A draft needs renderable content (a `rich_text` with non-empty elements, a header, a divider, a table attachment, or a file) or Slack Desktop tombstones it. A `rich_text_section` directly before a list, preformatted, or quote container must end with a text inline ending in `\n`. A header resets that rule. A divider does not yet.
- Other draft gotchas: `blocks` is Block Kit JSON (top-level `rich_text`, `header`, and `divider` only; tables ride in `attachments` - validated via `validateBlockShapes` / `validateAttachmentBlocks` in cmd/draft.go); `client_last_updated_ts` must be exactly 7 decimal places on update/delete (helper `padDraftTS`, both pads and truncates); `drafts.list` has no cursor pagination, only `limit` + `has_more`. Update/delete auto-fetch the draft via `drafts.list` so callers don't juggle the ts themselves.
- `--markdown` converts through `internal/mdblocks` (goldmark) and then `parseDraftPayload`. `invalid_markdown` is the caller's error. `invalid_blocks` after a conversion is a converter defect. The converter emits one `header` per heading (level clamped to 3), one `divider` per thematic break, and one `rich_text` per run between them. Nested lists split into sibling `rich_text_list` containers. A list or code block inside a quote becomes a bordered sibling. Symbolic `@name` is rejected. `--print-payload` makes zero API calls. Goldens are in `internal/mdblocks/testdata`.
- To probe `drafts.create` with a payload the validator rejects, build the out-of-tree probe module described in `docs/draft-internals.md`. Curl does not work on Grid.
- Draft state: `IsSent`/`IsDeleted`/`IsFromComposer` are `*bool`, emitted only when Slack sent them. `draft list` trailers carry `suppressed_sent`/`suppressed_deleted`. `--limit` outside 1-100 is `invalid_input`. `draft update` refuses a sent draft (`draft_already_sent`). A failed `drafts.update` triggers one re-fetch and the draft's state decides: sent is an error, deleted is replaced (the row carries `replaced_draft_id`), live or missing surfaces Slack's error.
- `drafts.list` does not page and `limit` clamps at 100. `is_active=true` filters before the limit, so `draftLookup` and the default `draft list` union the plain page with the active answer. `draft_lookup_incomplete` fires only when the plain page was truncated and the active answer lacks the id. `draft delete` rows are `{id, status}` with `deleted` or `already_deleted`.
- Draft files (cmd/draft_files.go): `--file` on update replaces the list, `--no-files` clears it, and no flag preserves it. Upload runs last, each step an `api.CallOnce`. `uploadDraftFiles` deletes its uploads when one fails partway. `settleUploads` deletes them only when Slack refused the draft write (`slackRefused`) and otherwise names the ids in `detail`. The CLI never deletes a file it did not upload in the same invocation. Rows always emit `file_ids`.
- `is_from_composer` must be `"true"` on create, update, and replacement. Slack's channel composer reads `drafts.listActive`, which filters on this flag, so a draft with `false` is invisible to the "Edit draft" flow. The update path rewrites the flag unconditionally. `drafts.listActive` is a separate endpoint from `drafts.list?is_active=true`, which the default `draft list` and the draft lookup use.
- `draft create` takes variadic `Recipients`. `classifyRecipient` (cmd/recipients.go) sorts each into user (`@name`, email, `U`/`W` id, profile URL) or channel (everything else). One channel gives `channel_id`. One or more users give `user_ids`. Mixing the two, more than one channel, or `--thread`/`--broadcast` with users is `invalid_input`. Users resolve fully before any write.

Details are in `docs/draft-internals.md` and `docs/draft-messages.md`.

### Slack Lists

- List reads (cmd/list.go, cmd/list_render.go) ride the session client and `PostInternalForm`. `files.info` decodes into the local `listFile` struct. `list records` reads the schema once and joins each page inside the fetch closure (`renderRecord`), so an unknown column fails the page as `list_schema_mismatch`. Records decode with `UseNumber`. `list find` is `search.files` with `type:list` and returns one row per match.
- List writes (cmd/list_write.go, cmd/list_columns.go) validate every input before the first call and render success rows from the server's returned `list` object. A `list create` failure after the first call is a partial row returned as `*output.ExitError`, and the list is never deleted. `set-choices` is a whole-set read-then-replace. A `[]string` kong flag needs `sep:"none"` to hold commas.
- Record writes (cmd/list_records.go, cmd/list_values.go): `lists.records.create` addresses columns by key with scalar values, and `lists.cells.update` addresses them by id with typed objects. Both build from one `cellWrite`. Validate fully before the first call because Slack silently drops unknown keys. A name-shaped channel value takes the conditional two-client split. `update` re-reads through `lists.records.info`.

Details are in `docs/lists.md` and `docs/lists-plan.md`.

### Search

- `search messages` and `search files` rewrite `from:@x`, `to:@x`, and `in:@x` to `<@Uxxx>` through `rewriteUserModifiers` (cmd/searchquery.go), because Slack returns an empty page for a name it cannot match. Unquoted names are greedy and the longest resolving prefix wins. An unresolved name is fatal `user_not_found`, a shared name is `ambiguous_user`, and a failed directory load is returned raw. The prefix loop returns on those errors and never tries a shorter prefix.
- `search messages`/`search files` emit a `search_query` trace event (`endpoint`, `input`, `query`) from `searchQuery` (cmd/search.go) after `rewriteUserModifiers`, and that is the only place the sent query is visible - trace only, never `_meta`. It exists because a report that `from:@handle` returned another user's messages could not be checked (2026-11-10): every form of the handle resolved correctly on every run, and the likely cause was a query with two `from:` modifiers, which Slack ORs (measured: 24 rows from one author and 68 from the other on one page).

Details are in `docs/search.md`.

### Users

- Custom profile fields come only from `users.profile.get` with `include_labels=true` (scope `users.profile:read`). `user info --full` adds `custom_fields` keyed by normalized label with deterministic keys. `user manager-chain` walks the "Manager" field upward with a depth cap of 20 and a cycle guard, puts `stop_reason` on the last row, and emits `id` rather than `user_id`.

Details are in `docs/users.md` and `docs/profile-feature-plan.md`.

### Testing traps

- `runWithMockSession` sets `SLACK_TOKEN`, so both clients get one token and no assertion can tell which client made a call. Use `runWithTwoCredentials` (cmd/saved_enrich_test.go) for any command that holds both clients.
- Do not assert that `enrichItems` joins its workers on the fatal path. The check is flaky on correct code.
- `runWithMockSession` cannot be used for any test that needs a handler still running when the command returns: its `defer srv.Close()` blocks until handlers finish, so a held handler is always complete by the time the helper hands back. `cancelRunSavedList` (cmd/saved_cancel_test.go) is the pattern - the test body owns the server. Related: `r.ParseForm()` in a handler that means to block on `r.Context().Done()` is load-bearing, because the server only starts the background read that notices a hung-up client once the request body hits EOF.
- Assert row count and trailer contents (`assertFatalPage`), assert the `*output.ExitError` positively (`assertExitError`), and give duplicate inputs different outcomes. Mutation-test a guard: break the implementation and confirm the test fails.
- Timeout E2E tests: assert on the classified error (`assertTimeoutError` in cmd/root_test.go - code `timeout`, detail keeping `"deadline exceeded"`), not elapsed time. `httptest.Server.Close` waits for in-flight handlers, so a passing test run looks slow even when the client returned promptly - and a handler that blocks on `<-r.Context().Done()` alone can hang `Close` forever, because the server does not reliably notice a client abandoning an unanswered request. Bound it: `select { case <-r.Context().Done(): case <-time.After(2*time.Second): }`.

Details are in `docs/testing.md`.

## Sandbox

GPG-signed git commits and `mise run` commands require `dangerouslyDisableSandbox: true` (Go build cache and GPG keyring access).
