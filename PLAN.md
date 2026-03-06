# Chrome Auth Implementation Plan

## Context

User's workspace won't allow app installation, so we need Chrome cookie extraction (`xoxc-` + `d` cookie) to authenticate. This pulls Phase 3's `auth login --chrome` forward.

`xoxc-` tokens require a `d` cookie on every API request (unlike `xoxb-`/`xoxp-` which use `Authorization: Bearer` alone). slack-go supports `slack.OptionHTTPClient()` for injecting a custom transport.

Slack's `localConfig_v2` in localStorage contains a `teams` map keyed by team ID, each with a `token` field. The `d` cookie is shared across all workspaces.

## Steps

### 1. Credentials layer: add `auth_method` and `cookie`

- Add `AuthMethod` (`"oauth"` or `"chrome"`) and `Cookie` fields to `WorkspaceCredentials`
- `ResolveToken` -> `ResolveCredentials` returning a struct with bot, user, cookie
- Support `SLACK_COOKIE` env var
- Write tests first: credential round-trip with cookie, env var override

### 2. Cookie-aware API client

- Add `WithCookie(cookie string)` option to `api.Client`
- Implement `cookieTransport` wrapping `http.DefaultTransport`, injecting `Cookie: d=<value>` header
- Pass cookie through from `cmd/root.go` `NewClient()`
- Write test: verify cookie header appears on mock server requests

### 3. Auth hint based on `auth_method`

- When `NewClient()` gets an auth error, check stored `auth_method` to pick the right hint
- `"chrome"` -> `"Run 'slack auth login --chrome' to re-authenticate"`
- `"oauth"` -> `"Run 'slack auth login' to re-authenticate"`
- env var -> current hint (no stored method)

### 4. Chrome extraction (`internal/auth/chrome.go`)

- Add `chromedp` dependency
- `ChromeLogin(ctx, opts)` function:
  1. Launch Chrome with `--remote-debugging-port` on random port, profile dir `~/.config/slack-cli/chrome-profile/`
  2. Navigate to `https://app.slack.com`
  3. Poll every 2s: read `localStorage.localConfig_v2` for `xoxc-` tokens, read `d` cookie
  4. Once found, extract all teams from `localConfig_v2.teams`
  5. Validate each with `auth.test` (using cookie transport)
  6. Return slice of `WorkspaceCredentials`
  7. Close Chrome
- `ChromeLoginExisting(ctx, port)` for `--chrome-port`: connect to existing debug instance, find Slack tab, extract same way
- Hard to unit test (needs browser). Skip unit tests for CDP interaction; test everything around it.

### 5. `auth login --chrome` command

- Add `--chrome` flag and `--chrome-port` flag to `AuthLoginCmd`
- When `--chrome` is set, call `ChromeLogin` instead of OAuth
- Save all returned workspaces with `auth_method: "chrome"`
- Emit one JSONL line per workspace + `_meta`

### 6. Update SPEC.md

- Move Chrome auth from Phase 3 to Phase 1
- Document `--chrome`, `--chrome-port`, `SLACK_COOKIE`
- Document `auth_method` in credentials file

### 7. Refactor and clean up

- Review all changes for simplicity
- Update CLAUDE.md if needed
