# slack-cli

Read-only Slack CLI for agent/automation use. Go, kong, slack-go/slack.

## On startup

Every time you start in this repo, figure out where we left off and propose continuing. Check:

1. Any open PRs with your commits (`gh pr list`)
2. Which issues are closed vs open (`gh issue list`)
3. Any in-progress branches (`git branch`)

Propose the next action and ask for confirmation before proceeding.

## Working on issues

All work is tracked in GitHub Issues. When working on an issue:

1. Read the issue (`gh issue view <number>`).
2. Check that any dependency issues are closed. If not, raise it.
3. Read `SPEC.md` for full context on the relevant feature.
4. Write the plan, complexity analysis, and scope cut recommendations into the issue description (`gh issue edit <number> --body ...`).
5. Proceed to implementation. Red-green-refactor: write failing tests first, then implement, then clean up.
6. Each issue gets its own branch (`<issue-number>-<short-description>`) and a single PR.
7. Keep commits small and conventional (`feat:`, `fix:`, `chore:`, `test:`, `docs:`).

Update the issue description as the plan evolves. The issue is the source of truth for the plan. When scope changes, update `SPEC.md` to match.

## Autonomy

Work through issues independently. Only escalate when:

- A design decision isn't covered by `SPEC.md`.
- A dependency issue is blocking.
- Something feels wrong (scope creep, Slack API limitation, etc.).

After pushing a PR and addressing code review feedback, merge the PR yourself.

## Plan review

Before implementing, critically review the plan: What part is most likely to add complexity? What part of the scope would you recommend cutting? Include this analysis in the issue description.

## PR review

After pushing a PR, spawn a sub-agent (`code-review:code-review`) to independently review the PR. The review agent should submit all feedback as PR review comments. Address its feedback before considering the PR done.

## Project structure

```
cmd/           # kong command definitions
internal/
  api/         # Slack API client wrapper (Client, Paginate[T], ClassifyError)
  auth/        # credentials CRUD, OAuth flow, token resolution
  output/      # Printer, Error with exit codes (0=success, 1=general, 2=auth, 3=rate-limit, 4=network)
  resolve/     # channel/user name-to-ID resolution with in-memory cache
```

## Testing

Tests live next to the code they test (`foo_test.go`). Use table-driven tests. Mock the Slack API client at the interface boundary - don't make real API calls in tests.

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
- `api.Client` wraps `slack-go/slack` with separate bot/user token clients.
- No text output format. No `--format`, `--raw`, or `--no-pager` flags.
- `--fields` for output field filtering. `--quiet` suppresses stdout entirely.

## Sandbox

GPG-signed git commits and `mise run` commands require `dangerouslyDisableSandbox: true` (Go build cache and GPG keyring access).
