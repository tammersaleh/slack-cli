# slack-cli

Slack CLI for agent/automation use. Go, kong, slack-go/slack.

## Goal: Replace the Slack MCP + custom Python scripts

This CLI replaces three separate Slack integrations used in Tammer's
`~/brain/cw` knowledge base system:

1. **korotovsky/slack-mcp-server** (Docker, xoxc/xoxd auth) - search,
   history, replies, channels list. Config in `~/.claude.json` under
   `mcpServers.slack`. Caches channels and users to
   `~/.cache/slack-mcp-server/` with 24h TTL via Docker volume mount.

2. **slack-saved-items skill** (Python, httpx) - lists Slack saved-for-later
   items via the internal `saved.list` API. Source:
   `~/dotfiles/public/.claude/skills/slack-saved-items/`

3. **slack-organize-channels skill** (Python, httpx) - manages Slack sidebar
   sections via internal `channelSections.*` and `client.counts` APIs.
   Source: `~/dotfiles/private/.claude/skills/slack-organize-channels/`

Plus two helper scripts in `~/brain/cw/scripts/`:
- `slack-permalink` (bash) - build permalink URLs from channel+ts
- `slack-channel-ids` (python) - resolve channel names to IDs from KB files

### What's done (Phase 1)

All core read operations: auth (OAuth + Desktop), channel list/info/members,
message list/get, thread list, user list/info, reaction list. JSONL output,
field filtering, pagination, 3-tier channel caching, Chrome TLS fingerprinting.

### What remains

See `PLAN.md` for the full implementation roadmap. In priority order:

1. **search messages** - the #1 used MCP operation. Critical path.
2. **message permalink** - already spec'd in SPEC.md Phase 2.
3. **User caching** - MCP caches 17MB of user data with 24h TTL. Without
   this, user lookups are painfully slow. Extend the channel caching
   pattern to users.
4. **Channel cache TTL** - bump from 1h to 24h to match MCP behavior.
5. **saved list** - internal API (`saved.list`). Replaces the Python skill.
6. **section list/find/move/create** - internal APIs
   (`channelSections.*`, `client.counts`). Replaces the Python skill.
7. **message post** - `chat.postMessage`. Currently unused but closes
   feature parity with the MCP.

### Internal Slack APIs

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

### Auth for internal APIs

The existing Desktop auth already handles xoxc/xoxd tokens and Chrome TLS
fingerprinting. The internal API calls should reuse the same `api.Client`
transport layer. The `d` cookie injection and Chrome user-agent are already
wired into the custom `RoundTripper`.

### Caching reference

The MCP server (`~/.cache/slack-mcp-server/`) maintains:
- `channels_cache_v2.json` (~961K, 24h TTL)
- `users_cache.json` (~17MB, 24h TTL)

The Python sidebar script has its own cache at
`~/.cache/tars/channel-prefixes.json` for channel prefix lookups.

The CLI should consolidate all caching under `~/.config/slack-cli/cache/`
with configurable TTL (default 24h for both channels and users).

## On startup

Every time you start in this repo, figure out where we left off and propose continuing. Check:

1. Any open PRs (`gh pr list`)
2. Any in-progress branches (`git branch`)
3. Recent commits on main (`git log --oneline -10`)

Propose the next action and ask for confirmation before proceeding.

## Workflow

Work is driven by `SPEC.md`. Each feature gets its own branch and PR. The workflow for each feature:

1. Read `SPEC.md` for the relevant command/feature.
2. Red-green-refactor: write failing tests first, then implement, then clean up.
3. Run both `mise run test` and `mise run lint` after every change. Both must pass before committing.
4. Keep commits small and conventional (`feat:`, `fix:`, `chore:`, `test:`, `docs:`).
5. Push a PR. Spawn a sub-agent (`code-review:code-review`) to review it. Address feedback.
6. Merge the PR.
7. Retrospective: review your approach and these instructions. Update CLAUDE.md with anything you learned that would help future sessions.
8. Move on to the next feature.

## Autonomy

Work through features independently. Never stop to ask "should I
continue?" or "want me to keep going?" - the answer is always yes.
After merging a feature, immediately start the next one per `PLAN.md`
priority order. After giving a status summary, keep working. Only
escalate when:

- A design decision isn't covered by `SPEC.md`.
- Something feels wrong (scope creep, Slack API limitation, etc.).

## Project structure

```
cmd/
  root.go        # CLI struct, global flags, NewPrinter/NewClient/NewResolver helpers
  auth.go        # auth login/logout/status
  channel.go     # channel list/info/members
  message.go     # message list (alias: read) / get
  thread.go      # thread list (alias: read)
  user.go        # user list/info
  reaction.go    # reaction list
internal/
  api/           # Slack API client wrapper (Client, Paginate[T], ClassifyError, WithCookie)
  auth/          # credentials CRUD, OAuth flow, Desktop extraction, token resolution
  output/        # Printer (JSONL), Meta, Error with exit codes
  resolve/       # channel/user name-to-ID resolution with in-memory + file cache
```

## Testing

Tests live next to the code they test (`foo_test.go`). Use table-driven tests. Mock the Slack API via httptest servers - set `SLACK_TOKEN` and `SLACK_API_URL` env vars in tests. Use `runWithMock(t, handler, args...)` helper in `cmd/channel_test.go` for end-to-end command tests.

## Git

This is a personal project. You can push, create PRs, and merge.

## Output

JSONL to stdout. Every command emits one JSON object per line, ending with a `_meta` trailer. Errors as JSON to stderr. See `SPEC.md` for the full output model.

## Architecture decisions

- No config file. All config via flags/env vars. Kong handles precedence.
- Workspaces keyed by `TeamID` (stable) not `TeamName` (mutable) in credentials.json.
- User resolution: ID + email only. Display name (`@name`) deferred to Phase 2 (expensive at scale). `users.lookupByEmail` fails with `xoxc-` tokens on Enterprise Grid (scope limitation).
- Channel resolution: first match wins on name collision. No ambiguity errors.
- Channel list defaults to member-only. `--include-non-member` to expand.
- Single-page pagination by default. `--cursor` to continue, `--all` to fetch everything.
- `api.Paginate[T]` handles cursor-based pagination with rate-limit retry (5 attempts, respects Retry-After). `api.PaginateEach[T]` adds per-page callback with early exit. Both accept an endpoint name for diagnostics.
- Channel resolver uses `PaginateEach` for early exit (stops paginating once target is found). File cache at `~/.config/slack-cli/cache/channels-{teamID}.json` (1h TTL) persists across invocations. In-memory cache (5min TTL) for session reuse.
- `api.Client` wraps `slack-go/slack` with separate bot/user token clients. `WithCookie` injects `d` cookie + Chrome user-agent via custom `http.RoundTripper` for `xoxc-` tokens.
- Desktop auth (`--desktop`) reads `xoxc-` tokens from Slack Desktop's LevelDB and decrypts the `d` cookie from its SQLite cookies DB using the Slack Safe Storage password (`SLACK_SAFE_STORAGE_PASSWORD` env var). Works with Enterprise Grid.
- `auth_method` field in credentials.json tracks how each workspace was authenticated (`"oauth"` or `"desktop"`). Used for context-specific error hints.
- Chrome TLS fingerprinting (`utls`) + user-agent on all cookie-based API requests. Required for Enterprise Grid.
- `E`-prefix workspace IDs are Enterprise Grid org-level contexts. `conversations.list` fails on these. The `T`-prefix workspace within the same org works.

### Desktop auth gotchas

- Chromium LevelDB keys are prefixed with origin (`_https://app.slack.com\x00\x01`), not bare key names. Must scan with `strings.Contains`.
- LevelDB values have a `0x01` binary prefix before JSON. Strip by finding first `{`.
- The `d` cookie MUST stay URL-encoded. Decoding `%2B` etc. causes `invalid_auth`.
- utls + Chrome fingerprint negotiates HTTP/2 via ALPN. `net/http.Transport` can't handle h2 with custom `DialTLSContext`. Custom `RoundTripper` detects ALPN and delegates to `http2.Transport`.
- LevelDB is locked by running Slack. Must copy the directory first, remove the LOCK file, then open read-only.
- `SLACK_COOKIE` env var provides the `d` cookie for `xoxc-` token auth without stored credentials.
- No text output format. No `--format`, `--raw`, `--verbose`, or `--no-pager` flags.
- `--fields` for output field filtering. `--quiet` suppresses stdout entirely.
- Per-item errors go to stdout (one JSON per item). Fatal errors go to stderr. `ExitError` carries exit code without stderr output for partial-failure commands.
- Rate limit errors include `endpoint` field for diagnostics.

## Sandbox

GPG-signed git commits and `mise run` commands require `dangerouslyDisableSandbox: true` (Go build cache and GPG keyring access).
