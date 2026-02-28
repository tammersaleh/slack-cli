# slack-cli

Read-only Slack CLI for agent/automation use. Go, cobra, slack-go/slack.

## On startup

Every time you start in this repo, figure out where we left off and propose continuing. Check:

1. Any open PRs with your commits (`gh pr list`)
2. Which issues are closed vs open (`gh issue list`)
3. Any in-progress branches (`git branch`)

Propose the next action and ask for confirmation before proceeding.

## Working on issues

All work is tracked in GitHub Issues. When asked to work on an issue:

1. Read the issue (`gh issue view <number>`)
2. Check that any dependency issues are closed. If not, raise it.
3. Read `SPEC.md` for full context on the relevant feature.
4. Write the implementation plan into the issue description (`gh issue edit <number> --body ...`), updating it as the work progresses.
5. Red, green, refactor: write failing tests first, then implement, then clean up.
6. Each issue gets its own branch (`<issue-number>-<short-description>`) and a single PR.
7. Keep commits small and conventional (`feat:`, `fix:`, `chore:`, `test:`, `docs:`).

## Project structure

```
cmd/           # cobra command definitions
internal/      # internal packages (api, output, config, resolve, auth)
```

## Testing

Tests live next to the code they test (`foo_test.go`). Use table-driven tests. Mock the Slack API client at the interface boundary - don't make real API calls in tests.

## Git

This is a personal project. You can push and create PRs.

## Output

JSON to stdout (default). Errors as JSON to stderr. Human-readable text via `--format=text`.
