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
  auth/          # credentials CRUD, OAuth flow, Chrome CDP extraction, token resolution
  output/        # Printer (JSONL), Meta, Error with exit codes
  resolve/       # channel/user name-to-ID resolution with in-memory cache
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
- User resolution: ID + email only. Display name (`@name`) deferred to Phase 2 (expensive at scale).
- Channel resolution: first match wins on name collision. No ambiguity errors.
- Channel list defaults to member-only. `--include-non-member` to expand.
- Single-page pagination by default. `--cursor` to continue, `--all` to fetch everything.
- `api.Paginate[T]` handles cursor-based pagination with rate-limit retry (5 attempts, respects Retry-After). Used by `--all` and by the resolver internally.
- `api.Client` wraps `slack-go/slack` with separate bot/user token clients. `WithCookie` injects `d` cookie via custom `http.RoundTripper` for `xoxc-` tokens.
- Chrome auth (`--chrome`) uses `chromedp` to extract `xoxc-` tokens and `d` cookie from browser session. Supports all workspaces in a single extraction.
- `auth_method` field in credentials.json tracks how each workspace was authenticated. Used for context-specific error hints.
- `SLACK_COOKIE` env var provides the `d` cookie for `xoxc-` token auth without stored credentials.
- No text output format. No `--format`, `--raw`, or `--no-pager` flags.
- `--fields` for output field filtering. `--quiet` suppresses stdout entirely.

## Sandbox

GPG-signed git commits and `mise run` commands require `dangerouslyDisableSandbox: true` (Go build cache and GPG keyring access).
