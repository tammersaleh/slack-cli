# slack-cli

Read-only Slack CLI for agent/automation use. Go, kong, slack-go/slack.

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
3. Keep commits small and conventional (`feat:`, `fix:`, `chore:`, `test:`, `docs:`).
4. Push a PR. Spawn a sub-agent (`code-review:code-review`) to review it. Address feedback.
5. Merge the PR.
6. Move on to the next feature.

## Autonomy

Work through features independently. Only escalate when:

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
