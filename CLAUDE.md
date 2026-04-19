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

Channels and users are cached under `~/.config/slack-cli/cache/` with a
24h TTL (configurable via `SLACK_CACHE_TTL`). Both cache files are keyed
by workspace `TeamID`.

## Workflow

Work is driven by `SPEC.md`. Each feature gets its own branch. The workflow for each feature:

1. Read `SPEC.md` for the relevant command/feature.
2. Create a feature branch off main.
3. Red-green-refactor: write failing tests first, then implement, then clean up.
4. Run both `mise run test` and `mise run lint` after every change. Both must pass before committing.
5. Keep commits small and conventional. Commit types drive releases - see "Release versioning" below.
6. Code review: spawn a `code-review:code-review` sub-agent to review the branch changes (`git diff main...HEAD`). Tell the reviewer to scrutinize tests: look for tests that don't actually test what they claim, useless tests, and missing test coverage. Address all findings before merging.
7. Merge to main and push. The pre-push hook runs `mise run check` (test + lint + goreleaser snapshot); never bypass with `--no-verify`.
8. Retrospective: review your approach and these instructions. Update CLAUDE.md with anything you learned that would help future sessions.
9. Move on to the next feature.

## Release versioning

Releases are automated via release-please + GoReleaser. Release-please
watches main, maintains an open release PR with an auto-generated
`CHANGELOG.md` and version bump, and cuts the tag + GitHub Release when
that PR is merged. GoReleaser then builds binaries and pushes the Homebrew
Formula to `tammersaleh/homebrew-tap`. Nobody runs `git tag` by hand.

This means commit type is not a style choice - it ships as a version
number and changelog copy users read. Pick the type based on user-facing
impact, not diff size:

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
  `refactor:` even if the PR is large.
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
  skill.go       # generate Claude skill file
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

This is a personal project. You can push, create PRs, and merge.

## Output

JSONL to stdout. Every command emits one JSON object per line, ending with a `_meta` trailer. Errors as JSON to stderr. See `SPEC.md` for the full output model.

## Architecture decisions

- No config file. All config via flags/env vars. Kong handles precedence.
- Workspaces keyed by `TeamID` (stable) not `TeamName` (mutable) in credentials.json.
- User resolution: ID, email, or `@name` (display name / username / real name). Name lookups use the user cache index. `users.lookupByEmail` fails with `xoxc-` tokens on Enterprise Grid (scope limitation).
- Channel resolution: first match wins on name collision. No ambiguity errors.
- Channel list defaults to member-only. `--include-non-member` to expand.
- Single-page pagination by default. `--cursor` to continue, `--all` to fetch everything.
- `api.Paginate[T]` handles cursor-based pagination with rate-limit retry (5 attempts, respects Retry-After). `api.PaginateEach[T]` adds per-page callback with early exit. Both accept an endpoint name for diagnostics.
- Channel resolver uses `PaginateEach` for early exit (stops paginating once target is found). File cache at `~/.config/slack-cli/cache/channels-{teamID}.json` (24h TTL, configurable via `SLACK_CACHE_TTL`). In-memory cache (5min TTL) for session reuse. Reverse index (ID->name) for output enrichment.
- User cache: file at `~/.config/slack-cli/cache/users-{teamID}.json` (24h TTL). Bulk-loads all users on first miss. Indexes by ID, email, and display name.
- Output enrichment: all output automatically resolves user/channel IDs to names from cache. Best-effort, adds `user_name`/`channel_name` fields.
- Internal APIs: `PostInternal` (JSON body) and `PostInternalForm` (form-encoded) for undocumented Slack endpoints (`saved.list`, `users.channelSections.*`).
- `api.Client` wraps `slack-go/slack` with separate bot/user token clients. `WithCookie` injects `d` cookie + Chrome user-agent via custom `http.RoundTripper` for `xoxc-` tokens.
- Desktop auth (`--desktop`) reads `xoxc-` tokens from Slack Desktop's LevelDB and decrypts the `d` cookie from its SQLite cookies DB using the Slack Safe Storage password (`SLACK_SAFE_STORAGE_PASSWORD` env var). Works with Enterprise Grid.
- `auth_method` field in credentials.json tracks how each workspace was authenticated (`"oauth"` or `"desktop"`). Used for context-specific error hints.
- Chrome TLS fingerprinting (`utls`) + user-agent on all cookie-based API requests. Required for Enterprise Grid.
- `E`-prefix workspace IDs are Enterprise Grid org-level contexts. `conversations.list` fails on these. The `T`-prefix workspace within the same org works.
- Internal APIs (`saved.list`, `users.channelSections.*`) require the `E`-prefix org token on Enterprise Grid. The `T`-prefix returns `team_is_restricted`.
- Drafts (`drafts.list`, `drafts.create`, `drafts.update`, `drafts.delete`) follow the internal-API pattern - plain `PostInternalForm` against `slack.com/api/*`. No workspace subdomain, slack_route, or special UA is required.
- Draft ghosting (`is_deleted=true` appearing on the server within seconds of create) is caused by Slack Desktop's Drafts-panel composer reconciliation: any server-side draft whose `blocks` array does not contain at least one `rich_text` block with non-empty `elements` gets tombstoned. Host, UA, TLS fingerprint, and `last_updated_client` stamp do not matter - block content does. `validateBlocksShape` (cmd/draft.go) rejects such payloads before they ship. The `auto-replace` path (`createReplacement` for tombstoned drafts) still exists as defense-in-depth for races where Desktop already marked a draft deleted between our `drafts.list` and `drafts.update`. Full write-up in `docs/draft-messages.md`.
- Other draft gotchas: `blocks` is Block Kit JSON (rich_text required; other block types allowed alongside - see skill.go); `client_last_updated_ts` must be exactly 7 decimal places on update/delete (helper `padDraftTS`, both pads and truncates); `drafts.list` has no cursor pagination, only `limit` + `has_more`. Update/delete auto-fetch the draft via `drafts.list` so callers don't juggle the ts themselves.
- Helpers that surface errors: return raw errors, classify at the call site via `cli.ClassifyError`. Pre-classifying in a helper (e.g. `api.ClassifyError`) strips the auth-method hint added by `cli.ClassifyError`.
- Partial-failure commands return `*output.ExitError` (exit code only), not `*output.Error` (which also prints JSON to stderr). Per-item errors belong on stdout inline so the JSONL contract is preserved.
- `--timeout` threads through `cli.Context()`; every command's `Run` calls it and `defer cancel()`. Don't use `context.Background()` in commands - it bypasses the flag.
- `--trace` attaches a JSON-lines tracer via `api.WithTracer(ctx, api.NewJSONLinesTracer(w))`. `Paginate[Each]` and internal-API POSTs emit events. Slack-go bot-token calls are not yet instrumented - that'd need a transport hook.
- Output pipeline order: `filterFields` → `enrichTimestamps` → `EnrichFunc`. Enrichment runs after `--fields` so `--fields user` keeps the resolved `user_name`; enrichment is "extra," not user-filterable.
- Pagination cursors pass Slack's tokens through unchanged. For page-number APIs (`search.messages`, `search.files`, `files.list`), the cursor is the raw next page number as a string; `parsePageCursor` in cmd/search.go handles it.
- Resolver user cache: `LookupUser(id)` falls back to `users.info` on miss rather than bulk-loading via `users.list`. `ResolveUser(@name|email)` still bulk-loads since there's no single-user API that scans by display name. Single-user inserts via `addUserToCache` mirror the bulk `setUserMaps` indexing (users/usersByEmail/usersByName) so a follow-up name lookup in the same session hits memory cache.
- Channel cache: `Enrich` warms via `ensureChannelCache` when the item being printed contains a channel ID, so output enrichment is deterministic across commands. Commands that never emit channel IDs don't pay the `conversations.list` cost.
- Timeout E2E tests: assert on the error text (`"deadline exceeded"`), not elapsed time. `httptest.Server.Close` waits for in-flight handlers, so a passing test run looks slow even when the client returned promptly.

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

## Sandbox

GPG-signed git commits and `mise run` commands require `dangerouslyDisableSandbox: true` (Go build cache and GPG keyring access).
