# slack-cli Specification

A non-interactive CLI for Slack, built in Go. Designed for agents and automation. Reads nearly everything; writes are limited to the operations named in the design principles below, and it never posts a message.

## Design Principles

- **No message sending**: The CLI never posts messages to Slack. `chat.postMessage` is intentionally not wrapped - too risky for agent use. Write operations are limited to `channel mark`, channel membership (`channel invite`, `channel kick`), draft staging (unsent messages the user sends from the Slack app), and sidebar `section` management (personal sidebar only).
- **Agent-first**: JSONL output. No interactive prompts. Deterministic, scriptable.
- **Thin wrapper**: One API page per call by default. The caller controls data volume. The single exception is `channel list --query`, which searches every page of your own conversations because a partial client-side search reports "not found" for something that exists; it is bounded and cheap, and every other path still requires `--all`.
- **Resource-verb pattern**: `slack <resource> <verb> [flags]`, consistent with `gh`, `kubectl`, `aws`.
- **Composable**: Pipes, `jq`, shell scripts. Stdout for data, stderr for diagnostics.

## Output

All commands produce JSONL (newline-delimited JSON) on stdout. Each line is a self-contained JSON object. Fields use `snake_case`, matching the Slack API.

### List commands

One JSON object per item, followed by a `_meta` trailer:

```
$ slack channel list --limit=2
{"id":"C01ABC","name":"general","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Company-wide announcements","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"General discussion","creator":"U01XYZ","last_set":1609459200},"is_member":true}
{"id":"C02DEF","name":"random","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Water cooler","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"Non-work chatter","creator":"U01XYZ","last_set":1609459200},"is_member":true}
{"_meta":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}
```

The `_meta` line is always present, even when there are no results:

```
$ slack channel list --query=nonexistent
{"_meta":{"has_more":false,"filter_exhaustive":true}}
```

### Info commands

Info commands accept one or more arguments. Each result includes an `input` field matching the argument that produced it:

```
$ slack channel info #general #random
{"input":"#general","id":"C01ABC","name":"general","is_channel":true,...}
{"input":"#random","id":"C02DEF","name":"random","is_channel":true,...}
{"_meta":{"has_more":false}}
```

Per-item errors appear inline, interleaved with successful results:

```
$ slack channel info #general #nonexistent #random
{"input":"#general","id":"C01ABC","name":"general",...}
{"input":"#nonexistent","error":"channel_not_found","detail":"No channel matching '#nonexistent'"}
{"input":"#random","id":"C02DEF","name":"random",...}
{"_meta":{"has_more":false,"error_count":1}}
```

The `input` field is always present on info command output regardless of `--fields`.

### Timestamp enrichment

Fields named `ts` or ending in `_ts` get an `_iso` sibling with RFC 3339 format:

```json
{"ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z"}
```

### Field filtering

`--fields` limits output to specified top-level fields. Applies to data lines only, not `_meta`:

```
$ slack channel list --limit=2 --fields=id,name,is_private
{"id":"C01ABC","name":"general","is_private":false}
{"id":"C02DEF","name":"random","is_private":false}
{"_meta":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}
```

Nested objects are returned whole when their parent field is selected (`--fields=topic` returns the full topic object). No dot-notation for nested field access - use `jq` for that.

Three fields survive filtering regardless of what was asked for: `input`, and
the row-level error markers `error` and `enrich_error`. A marker is a row's only
signal that its data is incomplete, so filtering one away would produce a row
that looks whole - and the nonzero exit code cannot say which row was affected.

### Errors

Errors that prevent command execution entirely (auth failure, network error) go to stderr as JSON:

```json
{"error":"not_authed","detail":"No token found","hint":"Run 'slack auth login' or set SLACK_TOKEN"}
```

Per-item errors in multi-argument info commands appear inline on stdout (see above).

A failure that cuts a paginated command short also sets `error` in the `_meta`
trailer, so the truncation is visible on stdout. See "Truncated results" under
Pagination.

`error` is always a snake_case machine code, never raw Go or library error
text - it is safe to switch on. When the code is a category rather than a
specific cause, `detail` on stderr carries the specifics. Slack's own API error
strings (`missing_scope`, `not_in_channel`, ...) pass through as the code
unchanged. The codes the CLI raises for failures Slack did not name:

- `rate_limited` - retries exhausted, or a rate limit on an un-retried call
  (exit 3). Slack's own body-level `ratelimited` string is reported under this
  code too, so `ratelimited` never appears as an `error` value.
- `network_error` - the request never reached Slack: DNS, connection refused,
  connection reset, TLS (exit 4).
- `timeout` - the `--timeout` budget expired (exit 1). Raise the budget, drop
  it, or resume from `next_cursor`.
- `http_error` - Slack answered with a non-200 that was not a 429; `detail`
  names the status (exit 1).
- `parse_error` - the response body was not the JSON the API returns. Usually a
  proxy or captive portal intercepting the request (exit 1).
- `invalid_cursor` - Slack rejected the token passed to `--cursor`; it is void,
  so rerun without it (exit 1).
- `pagination_error` - Slack returned a cursor that repeats a page already
  fetched, which would loop forever (exit 1).
- `unknown_error` - the CLI did not recognize the failure. Read `detail` (exit 1).

`missing_scope` is the one Slack string that exits 2 without being a login
problem. Its `hint` depends on the credential: an OAuth token gets the scopes
the CLI requests now by re-running `slack auth login`, while a desktop session
token carries whatever Slack gives the account, so logging in again adds
nothing. `no_permission` and `not_allowed_token_type` exit 1.

Exit codes:

- `0`: All items succeeded
- `1`: General error (API error, invalid input, partial failure in multi-item commands)
- `2`: Authentication error (no token, expired, insufficient scopes)
- `3`: Rate limited (after exhausting retries)
- `4`: Network error

### Trace output

With `--trace`, structured diagnostics go to stderr as JSONL. Each event
carries `ts`, `kind`, and kind-specific attributes. Current kinds:

- `page` - one per paginated page fetch (`endpoint`, `attempt`, `items`,
  `latency_ms`, `has_more`, `error`)
- `call` - one per single-shot request through the shared retry (`endpoint`,
  `attempt`, `latency_ms`, `error`); `channel invite`, `channel kick`, and the
  `users.profile.get` fetches of `user info --full` and `user manager-chain`
- `retry` - emitted before each rate-limit sleep (`endpoint`, `attempt`,
  `wait_ms`); `attempt` is the request that was rate limited
- `internal_api` - one per internal API POST (`endpoint`, `latency_ms`,
  `bytes`, `error`)

```json
{"ts":"2024-03-01T00:00:00.000Z","kind":"page","endpoint":"conversations.list","attempt":1,"items":200,"latency_ms":182,"has_more":true,"error":""}
{"ts":"2024-03-01T00:00:00.183Z","kind":"retry","endpoint":"conversations.list","attempt":1,"wait_ms":3000}
```

Tracing does not cover slack-go bot token calls made outside the paginated
helpers and `api.Call` (e.g. `conversations.history` in `message get`).

## Global Flags

```
--workspace, -w   Select workspace by team ID (e.g. T01ABC). Env: SLACK_WORKSPACE
--fields          Comma-separated list of top-level fields to include
--quiet, -q       Suppress stdout output (exit code and stderr only)
--timeout         Overall command timeout (e.g. 30s, 2m). Env: SLACK_TIMEOUT
--trace           Emit structured diagnostics (endpoint, latency, retries) to stderr as JSONL
```

## Authentication

### Token types

- **Bot tokens** (`xoxb-`): Most read operations. Tied to an app.
- **User tokens** (`xoxp-`): Required for `search`. Tied to a user.
- **Session tokens** (`xoxc-`): Extracted from Slack Desktop app. Require a `d` cookie on every API request. Function as user tokens.

Tokens stored in `slack-cli/credentials.json` inside the user config directory (see Caching for how that resolves per platform), keyed by workspace `TeamID`. Each entry includes an `auth_method` field (`"oauth"` or `"desktop"`) used for context-specific error hints. `--workspace` selects the workspace when multiple exist. If only one workspace exists, it's used automatically.

### OAuth

`slack auth login` runs a local OAuth flow:

1. Starts a temporary HTTP server on a random port.
2. Opens Slack OAuth URL in the browser.
3. Receives callback with authorization code.
4. Exchanges code for bot + user tokens.
5. Stores both in credentials file with `auth_method: "oauth"`.

Requires `SLACK_CLIENT_ID` and `SLACK_CLIENT_SECRET` environment variables. The user creates a Slack app manually - setup instructions are in the README.

### Desktop extraction

`slack auth login --desktop` extracts session tokens from the Slack Desktop app's local storage. Works with Enterprise Grid. Fallback for workspaces that restrict OAuth app installation.

Requires `SLACK_SAFE_STORAGE_PASSWORD` env var (the Slack Safe Storage password from macOS Keychain Access - search for "Slack Safe Storage", show password, copy it).

1. Copies Slack's LevelDB from `~/Library/Application Support/Slack/Local Storage/leveldb/` (avoids lock contention).
2. Reads `localConfig_v2` (or `v3`) to extract `xoxc-` tokens per workspace.
3. Decrypts the `d` cookie from `~/Library/Application Support/Slack/Cookies` (SQLite) using the Safe Storage password (PBKDF2 + AES-CBC).
4. Validates each workspace via `auth.test`.
5. Stores all valid workspaces with `auth_method: "desktop"`.

All API requests use Chrome's TLS fingerprint and user-agent to avoid Slack's anomaly detection on Enterprise Grid.

Session tokens (`xoxc-`) expire. When they do, the CLI detects the failure and hints the user to re-run `slack auth login --desktop`.

### Environment variable overrides

`SLACK_TOKEN`, `SLACK_USER_TOKEN`, and `SLACK_COOKIE` override stored credentials when set. `SLACK_COOKIE` provides the `d` cookie value for `xoxc-` token authentication. For CI/CD and agent contexts.

### auth commands

#### slack auth login

```
slack auth login [flags]
  --client-id       Slack app client ID. Env: SLACK_CLIENT_ID
  --client-secret   Slack app client secret. Env: SLACK_CLIENT_SECRET
  --desktop         Extract credentials from Slack Desktop app. Env: SLACK_SAFE_STORAGE_PASSWORD required.
```

Without `--desktop`, opens browser for OAuth flow. On success:

```
$ slack auth login
{"team_id":"T01ABC","team_name":"Acme Corp","user_id":"U01XYZ","auth_method":"oauth","has_bot_token":true,"has_user_token":true}
{"_meta":{"has_more":false}}
```

With `--desktop`, extracts credentials from Slack Desktop. Emits one line per workspace:

```
$ slack auth login --desktop
{"team_id":"T01ABC","team_name":"Acme Corp","user_id":"U01XYZ","auth_method":"desktop","has_bot_token":true,"has_user_token":false}
{"team_id":"T02DEF","team_name":"Other Corp","user_id":"U02ABC","auth_method":"desktop","has_bot_token":true,"has_user_token":false}
{"_meta":{"has_more":false}}
```

Rows are sorted by `team_name` (case-insensitive), then `team_id`. Desktop extraction reads workspaces from a map, so without the sort the order changed run to run.

After the trailer, login writes workspace-selection guidance to stderr. It checks the `SLACK_WORKSPACE` and `SLACK_WORKSPACE_ORG` environment variables (not `--workspace`, which is a one-shot selector) against every stored workspace, not just the ones this login produced. Matching is by exact `team_id`, the same lookup every other command performs:

- Value set and matches a stored workspace: `SLACK_WORKSPACE is set to T01ABC  # Acme Corp` (and `SLACK_WORKSPACE_ORG is set to E01ORG  # Acme (org)`). No export suggestion.
- Value set but matches nothing stored: `SLACK_WORKSPACE=T09OLD does not match any saved workspace. Set one of:` followed by the candidate export lines.
- Value unset: `Set your default workspace:` followed by the candidate export lines. When an `E`-prefixed org workspace is stored, an `Enterprise Grid detected` block follows with the `export SLACK_WORKSPACE_ORG=...` line(s).

Candidates for `SLACK_WORKSPACE` are every non-`E` workspace; when only `E`-prefixed org workspaces are stored, those are offered instead. `SLACK_WORKSPACE_ORG` counts as set only when it names a stored `E`-prefixed workspace: a `T` id there is reported as not matching any saved org, since internal APIs answer `team_is_restricted` on it.

The hint never picks a workspace for the user. Earlier versions suggested the first non-`E` workspace in map order, which named a different workspace each run and read as an error when `SLACK_WORKSPACE` was already set correctly.

Errors:

- `missing_client_credentials` (exit 1): `SLACK_CLIENT_ID` or `SLACK_CLIENT_SECRET` not set (OAuth mode only).
- `oauth_failed` (exit 1): OAuth flow failed (user denied, code exchange error).
- `desktop_auth_failed` (exit 1): Desktop extraction failed (Slack not installed, password not set, no tokens found).

#### slack auth logout

```
slack auth logout
```

Removes stored tokens for the active workspace:

```
$ slack auth logout
{"team_id":"T01ABC","team_name":"Acme Corp","status":"logged_out"}
{"_meta":{"has_more":false}}
```

Errors:

- `not_authed` (exit 2): No credentials for the specified workspace.

#### slack auth status

```
slack auth status
```

```
$ slack auth status
{"team_id":"T01ABC","team_name":"Acme Corp","user_id":"U01XYZ","has_bot_token":true,"has_user_token":true}
{"_meta":{"has_more":false}}
```

Errors:

- `not_authed` (exit 2): No credentials found.

#### slack auth switch (Phase 3)

```
slack auth switch <workspace>
```

## Name Resolution

### Channels

Anywhere a channel is required, the CLI accepts:

- Channel ID: `C01234ABCDE`
- Name with `#`: `#general`
- Name without `#`: `general`
- Channel URL: `https://acme.slack.com/archives/C01234ABCDE` or the `app.slack.com/client/<team>/C01234ABCDE` form
- Message permalink: `https://acme.slack.com/archives/C01234ABCDE/p1709251200000100` - resolves to the channel; the timestamp is ignored

Names are resolved from cache first (in-memory, 5-minute TTL; then the channel file cache), then from the API. The API path scans `users.conversations` - just the conversations you are in - before falling back to a walk of `conversations.list` for the whole workspace, so a name you are a member of costs a handful of requests instead of a partial org-wide crawl. The member scan is never persisted to the file cache, which stays the complete `conversations.list` snapshot. On name collision, first match wins, and a repeat lookup returns the same ID as the lookup that populated the cache. A URL that isn't a usable channel reference (wrong kind or malformed) is rejected as `invalid_input` rather than treated as a name.

Output enrichment (resolving a `channel_id` to a `channel_name` on emitted rows) does *not* trigger the `conversations.list` bulk load. On a cache miss it resolves the single ID via `conversations.info`, mirroring how user enrichment falls back to `users.info`. Enrichment cost is proportional to the number of distinct unresolved IDs in the output, not to workspace size. Failed lookups are memoized for the process so a wide result set with an unresolvable channel doesn't re-hit the API per row.

### Users

Anywhere a user is required, the CLI accepts:

- User ID: `U01234ABCDE`
- Email: `tammer@example.com`
- Display name / username / real name: `@tammer` (resolved from the cached user list)
- User URL: `https://acme.slack.com/team/U01234ABCDE`, the enterprise `/user/@W…` profile form, or `app.slack.com/client/<team>/U01234ABCDE`

A file URL embeds the uploader's user ID but identifies a file, so it is not accepted as a user. Wrong-kind or malformed URLs are rejected as `invalid_input`.

## Pagination

By default, list commands return a single page. Page size is controlled by `--limit` with per-command defaults.

To continue, pass `next_cursor` from the `_meta` trailer:

```
$ slack channel list --limit=100
...
{"_meta":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}

$ slack channel list --limit=100 --cursor=dXNlcjpVMDYx
...
{"_meta":{"has_more":false}}
```

Use `--all` to fetch all pages in a single call. Results stream to stdout as each page arrives:

```
$ slack channel list --all
...every channel...
{"_meta":{"has_more":false}}
```

`--all` and `--cursor` are mutually exclusive. Providing both is an error.

### Truncated results

A paginated command can fail partway through, after some pages have already
streamed to stdout. The rows already written stay there - they are real
results - and the trailer marks the stream incomplete with the same error code
the command writes to stderr:

```
$ slack channel list --all
{"id":"C01ABC",...}
{"_meta":{"has_more":true,"next_cursor":"dGVhbTpD","error":"rate_limited"}}
```

So a consumer reading stdout alone can still tell a truncated listing from a
complete one. The rule: **a listing is complete only when the last line is a
`_meta` object with no `error` field.** For an exhaustive `--all` scan, also
require `has_more:false`.

When `_meta.error` is present:

- `has_more:true` with `next_cursor` means the named page never arrived -
  resume from that cursor.
- `has_more:true` with no `next_cursor` means the first page failed - rerun the
  command.
- `has_more:false` means the outcome is terminal and there is nothing to resume
  (`thread_not_found`, for example).

A retained `next_cursor` is a checkpoint, not permission to retry immediately or
indefinitely. Read `_meta.error` first: `rate_limited` needs a wait, an auth
error (exit code 2) needs credentials repaired, `missing_scope` needs a scope
granted, `timeout` needs a larger `--timeout`. Bound your retries either way -
no error code makes an unchanged retry loop safe.

`_meta.error` is a machine code in every case, so it is safe to switch on; see
the code list under Errors. `_meta` carries no detail field, so when the code
alone is not enough, read `detail` from the stderr error object.

A failure that voids the request itself is reported as terminal, so it can never
be mistaken for a resume point. A cursor Slack rejected is the case that matters
most:

```
$ slack channel list --cursor=dGVhbTpD
{"_meta":{"has_more":false,"error":"invalid_cursor"}}
```

Malformed arguments (`invalid_arguments`, `invalid_limit`, `invalid_query`,
`invalid_ts_oldest`), a timestamp matching no message (`thread_not_found`), and
a method that no longer exists (`unknown_method`) are terminal for the same
reason. Everything else keeps its resume cursor.

The cursor is a page-boundary retry point, not a transactional checkpoint.
Slack cursors can expire, and page-number result sets shift between runs, so a
resumed run can duplicate or miss items if the underlying data changed. Work
that needs consistency should rerun from the start and deduplicate by ID.

A cursor is only valid for the same command, the same flags, and the same
workspace that produced it. Slack mints it against one endpoint and one set of
server-side filters, so reusing it after changing `--type`, the archive setting,
`--include-non-member`, the workspace, or the CLI version is undefined - Slack
may reject it or answer from a different result set. Discard it and rerun from
the start instead.

The trailer is not guaranteed in every circumstance: if stdout itself fails
(broken pipe) nothing further can be written, and a fatal error raised before
any data line - bad input, an unresolvable channel, auth failure - is reported
on stderr with no trailer, as it always has been.

Rate-limited pages are retried automatically: up to 5 attempts per page,
honoring Slack's `Retry-After`, with no wait after the final failure. A
command aborts on the first page that exhausts its budget. Because `--timeout`
defaults to off, a heavily throttled `--all` can wait a long time; pass
`--timeout` when a predictable bound matters.

Pagination flags on list commands:

```
--limit    Page size (per-command default, max varies by Slack endpoint)
--cursor   Continue from a previous page's next_cursor
--all      Fetch all pages, streaming results
```

Some endpoints are not paginated by Slack (usergroup list, emoji list). These return all results in a single call regardless of flags.

## Rate Limiting

Slack reports a rate limit in two shapes, and the CLI treats them as one condition: an HTTP 429, usually with a `Retry-After` header, or a 200 whose body is `{"ok":false,"error":"ratelimited"}` (`users.profile.get` answers this way). Both surface as `rate_limited`, exit 3.

Requests that go through the shared retry are attempted up to 5 times: every paginated page, `channel invite`/`kick` calls, and the `user info --full` and `user manager-chain` profile lookups. The wait between attempts is `Retry-After`, or one second when Slack sent none (the body shape never carries one). There is no wait after the last failure, and the command then exits 3:

```json
{"error":"rate_limited","detail":"Rate limited after 5 attempts on conversations.history","endpoint":"conversations.history"}
```

The internal API methods (`saved.*`, `users.channelSections.*`, `drafts.*`, `lists.*`, `client.counts`, `admin.roles.entity.listAssignments`) follow the same rules. `saved list` pages are retried. A 429 that carries no `Retry-After` still counts as a rate limit.

Other single-shot calls are not retried. A rate limit on one of them is reported at once, with the wait Slack asked for in `detail` when it sent one:

```json
{"error":"rate_limited","detail":"Rate limited; Slack asked to retry after 30s"}
```

Retries are logged to stderr when `--trace` is set.

## Commands

### channel

#### slack channel list

```
slack channel list [flags]
  --limit              Page size (default: 100, max: 200)
  --cursor             Continue from previous page
  --all                Fetch all pages
  --type               public, private, mpim, im, all (default: all)
  --exclude-archived   Exclude archived channels (default: true)
  --include-non-member Include channels the user hasn't joined
  --has-unread         Only channels with unread messages
  --query              Filter by name substring (client-side)
```

Default behavior returns only conversations the authenticated user is in.

```
$ slack channel list --limit=2
{"id":"C01ABC","name":"general","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Company-wide announcements","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"General discussion","creator":"U01XYZ","last_set":1609459200},"is_member":true,"unread_count":5,"last_read":"1709251200.000100"}
{"id":"C02DEF","name":"random","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Water cooler","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"Non-work chatter","creator":"U01XYZ","last_set":1609459200},"is_member":true,"unread_count":0,"last_read":"1709251300.000200"}
{"_meta":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}
```

##### Which endpoint each mode reads

The two modes read different Slack endpoints, and that choice is the whole of
the member filter:

| Mode | Endpoint | Returns |
|---|---|---|
| default | `users.conversations` | only the conversations you are in |
| `--include-non-member` | `conversations.list` | every channel in the workspace |

`--include-non-member` is far more expensive, because the workspace is far
larger than your own membership. Measured on an Enterprise Grid org with 1787
member conversations out of 5699 workspace channels: 19 requests and about 5
seconds for the default, against 178 rate-limited requests and about 8 minutes
with `--include-non-member`.

Four consequences of the default endpoint, all specific to it:

- `is_member` is reported as `true` for channels and group DMs. Slack omits the
  field from this endpoint's rows entirely, and membership is what put a
  conversation in the result set. Only `im` rows keep `is_member:false`: Slack
  sends no membership for a 1:1 DM on either endpoint.
- `num_members` is absent. Slack does not send it on this endpoint and there is
  nothing to infer it from. Use `channel info` for a member count.
- Group DMs (`mpim`) are included. `conversations.list` does not return them at
  all on a session token, so `--include-non-member` omits every group DM even
  under `--type mpim`.
- DMs with deactivated users are omitted. `users.conversations` leaves them out;
  they still appear under `--include-non-member`.

```
$ slack channel list --has-unread --fields=id,name,has_unreads,mention_count
{"id":"C01ABC","name":"general","has_unreads":true,"mention_count":2}
{"id":"C03GHI","name":"engineering","has_unreads":true,"mention_count":0}
{"_meta":{"has_more":false,"filter_exhaustive":true}}
```

`--query` and `--has-unread` are client-side filters applied after the API page is fetched. The returned page may contain fewer items than `--limit`.

Because `--query` and `--has-unread` filter client-side, a run that only looks
at one page can report zero matches for a channel that exists, or five unread
channels when there are thirty. On the member-only default the command therefore
searches every page - affordable now that it reads `users.conversations`. Under
`--include-non-member` (a whole-workspace walk) or `--cursor` (a resume point,
which cannot combine with `--all`) it filters only the page fetched.

Either way the trailer says which happened:

```
$ slack channel list --query approvals
{"id":"C01ABC","name":"team-approvals"}
{"_meta":{"has_more":false,"filter_exhaustive":true}}

$ slack channel list --query approvals --include-non-member
{"_meta":{"has_more":true,"next_cursor":"dGVhbTpD","filter_exhaustive":false}}
```

`filter_exhaustive` is present whenever a client-side filter is active
(`--query`, `--has-unread`). It is `true` only when the filter saw every page:
the stream finished without error and Slack reported no further pages. **An empty
result with `filter_exhaustive:false` does not mean nothing matches** - it means
the filtering was partial. `user list` never widens on its own (a full directory
is dozens of rate-limited requests), so pass `--all` there.

`--has-unread` reads unread state from the internal `client.counts` endpoint -
one request for the whole workspace - because no list endpoint returns unread
information. It therefore needs a session token, and on Enterprise Grid the org
context (`SLACK_WORKSPACE_ORG`); with a bot token the command fails
`session_token_required` rather than filtering on nothing. Matching rows carry
`has_unreads`, `mention_count`, and `last_read` from that response.

A conversation Slack omits from `client.counts` is treated as having no unreads.
Every channel you are in is present; DMs appear only while open, and a closed DM
carries no unread badge in Slack's own sidebar either.

Errors:

- `not_authed` (exit 2): No token.

Slack API: `users.conversations`, or `conversations.list` with `--include-non-member`; plus internal `client.counts` with `--has-unread`

#### slack channel info

```
slack channel info <channel>...
```

```
$ slack channel info #general
{"input":"#general","id":"C01ABC","name":"general","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Company-wide announcements","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"General discussion","creator":"U01XYZ","last_set":1609459200},"num_members":142,"is_member":true,"unread_count":5,"last_read":"1709251200.000100"}
{"_meta":{"has_more":false}}
```

```
$ slack channel info #general #nonexistent
{"input":"#general","id":"C01ABC","name":"general",...}
{"input":"#nonexistent","error":"channel_not_found","detail":"No channel matching '#nonexistent'"}
{"_meta":{"has_more":false,"error_count":1}}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `not_authed` (exit 2): No token.

Slack API: `conversations.info`

#### slack channel members

Returns user IDs only. Use `slack user info` to enrich with profile data.

```
slack channel members <channel> [flags]
  --limit    Page size (default: 100, max: 200)
  --cursor   Continue from previous page
  --all      Fetch all pages
```

```
$ slack channel members #general --limit=3
{"user_id":"U01XYZ"}
{"user_id":"U02ABC"}
{"user_id":"U03DEF"}
{"_meta":{"has_more":true,"next_cursor":"dGVhbTpD"}}
```

The `user_id` field name is used (rather than `id`) so the CLI's enrichment layer can attach a resolved `user_name` alongside.

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `not_authed` (exit 2): No token.

Slack API: `conversations.members`

#### slack channel managers

Lists a channel's Channel Managers - the users shown under "Managed by"
in the channel's About tab. Returns the role assignments reported by
Slack's internal Managed-by data, one JSONL row per assigned user.

Requires a session token (`xoxc-`); uses an undocumented internal API.
On Enterprise Grid the org (`E`-prefix) token is used for the internal
call (`SLACK_WORKSPACE_ORG`), while channel-name resolution uses the
workspace (`T`-prefix) token.

```
slack channel managers <channel>
```

```
$ slack channel managers #approvals
{"user_id":"U01XYZ","role_id":"Rl0A"}
{"_meta":{"has_more":false}}
```

`role_id` is passed through verbatim (`Rl0A` is the Channel Manager
role). It is not mapped to a human label - the mapping is undocumented
and could drift. A channel with no managers emits zero rows; that is
success, not an error. A user appearing under multiple role assignments
yields one row per assignment.

The `user_id` field name (rather than `id`) lets the enrichment layer
attach a resolved `user_name`.

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `session_token_required` (exit 2): Token is not a session (`xoxc-`) token.
- `not_authed` (exit 2): No token.

Slack API: `admin.roles.entity.listAssignments` (internal)

#### slack channel invite

Adds users to channels via `conversations.invite`. Recipients are variadic and
classified syntactically: `@name`, an email, a `U…`/`W…` id, or a user-profile
URL is a user; everything else (`#name`, bare name, `C…` id, channel URL) is a
channel. At least one channel and at least one user are required; order does not
matter.

```
slack channel invite <recipient>...
```

```
$ slack channel invite @alice '#eng-a' '#eng-b'      # one person, many channels
$ slack channel invite '#eng-a' @alice @bob           # many people, one channel
```

Every channel and user resolves before the first write. A resolution failure
aborts the command with no rows and no API writes: a malformed URL is
`invalid_input`, otherwise `channel_not_found` / `user_not_found` naming the
input. Ids and URLs pass through without an existence check. Repeated inputs
dedupe by resolved id, first-seen order.

One `conversations.invite` call per (channel, user) pair, sequential, channels
in input order and users in input order within each channel, with the standard
rate-limit retry. One row per pair, emitted as it completes:

```
$ slack channel invite @alice '#eng-a' '#eng-b'
{"channel_id":"C01ABC","user_id":"U01XYZ","status":"invited"}
{"channel_id":"C02DEF","user_id":"U01XYZ","status":"already_in_channel"}
{"_meta":{"has_more":false}}
```

`channel_id` and `user_id` carry the enrichment names (`channel_name`,
`user_name`). `status` is `invited` or `already_in_channel`. The idempotent
outcome is a status, not an error, so re-running the same command is safe and
exits 0.

Every Slack error code has one of three dispositions:

| disposition | codes |
|---|---|
| idempotent: `status`, exit 0 | `already_in_channel` |
| pair error: one `error` row, continue | `cant_invite_self`, `cant_invite`, `user_is_restricted`, `ura_max_channels`, `user_not_found`, `invitee_cant_see_channel`, `org_user_not_in_team`, `no_external_invite_permission`, `app_cannot_join_channel` |
| channel error: an `error` row for every remaining user in that channel with no further calls, then the next channel | `not_in_channel` (the caller is not a member), `is_archived`, `channel_not_found`, `method_not_supported_for_channel_type` |
| fatal: abort the run | everything else, including codes Slack has not invented yet |

```
$ slack channel invite @alice @bob '#eng-a' '#archived'
{"channel_id":"C01ABC","user_id":"U01XYZ","status":"invited"}
{"channel_id":"C01ABC","user_id":"U02MGR","error":"cant_invite","detail":"cant_invite"}
{"channel_id":"C03GHI","user_id":"U01XYZ","error":"is_archived","detail":"is_archived"}
{"channel_id":"C03GHI","user_id":"U02MGR","error":"is_archived","detail":"is_archived"}
{"_meta":{"has_more":false,"error_count":3}}
```

Pair and channel errors count in `error_count` and the command exits 1 with
nothing on stderr. A fatal error (auth, `missing_scope`, `no_permission`,
rate-limit exhaustion, network, timeout, an unknown code) writes the classified
error to stderr with its own exit code and ends stdout with
`{"_meta":{"has_more":true,"error":"<code>","error_count":N}}`. Rows already
emitted are authoritative; `has_more:true` means some requested pairs have no
row. There is no cursor: rerun the same command, which is idempotent. Unknown
codes are fatal rather than pair-local because treating a systemic failure as
per-pair would repeat it across every pair.

Scopes: `channels:manage` and `groups:write` on a bot token (both also cover
`conversations.kick`). OAuth tokens issued before these were requested answer
`missing_scope`; re-run `slack auth login`. A desktop session token already has
them, on both the workspace and the org context (verified live on Enterprise
Grid, private channel included).

Errors:

- `invalid_input` (exit 1): No channel recipient, no user recipient, or a malformed Slack URL. No API call is made.
- `channel_not_found` / `user_not_found` (exit 1): A recipient did not resolve. No write is made.
- `not_authed` (exit 2): No token.

Slack API: `conversations.invite`

#### slack channel kick

Removes users from channels via `conversations.kick`. Same grammar, resolution,
ordering, row shape, and trailer contract as `channel invite`.

```
slack channel kick <recipient>...
```

```
$ slack channel kick @alice '#eng-a' '#eng-b'
{"channel_id":"C01ABC","user_id":"U01XYZ","status":"kicked"}
{"channel_id":"C02DEF","user_id":"U01XYZ","status":"not_in_channel"}
{"_meta":{"has_more":false}}
```

`status` is `kicked` or `not_in_channel`. Note `not_in_channel` describes the
target user here and the caller on `invite`, which is why the two commands
classify it differently.

| disposition | codes |
|---|---|
| idempotent: `status`, exit 0 | `not_in_channel` |
| pair error: one `error` row, continue | `cant_kick_self`, `user_not_found`, `not_supported` |
| channel error: an `error` row for every remaining user in that channel, then the next channel | `channel_not_found`, `cant_kick_from_general`, `method_not_supported_for_channel_type` |
| fatal: abort the run | everything else (`restricted_action` is workspace policy and `no_permission` describes the caller, so both abort) |

Errors: as `channel invite`.

Slack API: `conversations.kick`

### message

#### slack message list

Aliased as `slack message read`.

```
slack message list <channel> [flags]
  --limit       Page size (default: 20, max: 200)
  --cursor      Continue from previous page
  --all         Fetch all pages
  --after       Messages after this time (Unix timestamp or ISO 8601)
  --before      Messages before this time (Unix timestamp or ISO 8601)
  --unread      Messages since the last-read position
  --has-replies Only messages with thread replies (client-side filter)
  --has-reactions Only messages with reactions (client-side filter)
```

`--unread` sets `oldest` to the channel's `last_read` timestamp. Mutually exclusive with `--after`.

`--has-replies` and `--has-reactions` are client-side filters applied after the API page is fetched. The returned page may contain fewer items than `--limit`.

`--after` and `--before` are sent on every request of a walk, not just the
first. In `conversations.history` the cursor carries only a position; the
bounds are what define the window and the direction of paging. A `--cursor`
resume therefore has to repeat the bounds the cursor was produced under.

Result ordering depends on which bounds are set. With `--before` (or with no
bounds at all) messages come back newest first across the whole walk. With
`--after` alone, each page is newest first but successive pages move forward
in time, so an `--all` run is not globally sorted. Pass `--before` as well if
you need one descending stream.

```
$ slack message list #general --limit=2
{"client_msg_id":"abc-123","type":"message","user":"U01XYZ","text":"Hey team, the deploy is done.","ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z","thread_ts":"1709251200.000100","reply_count":3,"reply_users":["U02ABC","U03DEF"],"latest_reply":"1709251500.000200","latest_reply_iso":"2024-03-01T00:05:00Z","reactions":[{"name":"thumbsup","count":2,"users":["U02ABC","U03DEF"]}]}
{"client_msg_id":"def-456","type":"message","user":"U02ABC","text":"Nice work! Any issues to watch for?","ts":"1709251100.000050","ts_iso":"2024-02-29T23:58:20Z"}
{"_meta":{"has_more":true,"next_cursor":"bmV4dF90czox"}}
```

```
$ slack message list #general --unread --fields=user,text,ts,ts_iso
{"user":"U01XYZ","text":"Hey team, the deploy is done.","ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z"}
{"user":"U02ABC","text":"Nice work! Any issues to watch for?","ts":"1709251100.000050","ts_iso":"2024-02-29T23:58:20Z"}
{"_meta":{"has_more":false}}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `not_authed` (exit 2): No token.
- `invalid_timestamp` (exit 1): `--after` or `--before` is not a valid timestamp.

Slack API: `conversations.history`

#### slack message get

Retrieves specific messages by timestamp. Uses `conversations.history` with `oldest=ts&latest=ts&inclusive=true&limit=1`. Accepts either a channel plus timestamps, or one-or-more message permalinks (which may span channels in one call); the two forms can't be mixed. Output rows carry `channel_id`.

`conversations.history` never returns thread replies, so when it comes up empty the command falls back to a thread lookup: `chat.getPermalink` on the ts yields the parent `thread_ts` (a reply permalink already carries it, skipping this call), then `conversations.replies` fetches the reply directly. A ts that is neither a top-level message nor a reply is `message_not_found` as before.

```
slack message get <channel> <timestamp>...
slack message get <message-url>...
```

```
$ slack message get #general 1709251200.000100
{"input":"1709251200.000100","client_msg_id":"abc-123","type":"message","user":"U01XYZ","text":"Hey team, the deploy is done.","ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z","reply_count":3,"reactions":[{"name":"thumbsup","count":2,"users":["U02ABC","U03DEF"]}]}
{"_meta":{"has_more":false}}
```

```
$ slack message get #general 1709251200.000100 9999999999.999999
{"input":"1709251200.000100","type":"message","user":"U01XYZ","text":"Hey team, the deploy is done.","ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z",...}
{"input":"9999999999.999999","error":"message_not_found","detail":"No message at timestamp 9999999999.999999 in #general"}
{"_meta":{"has_more":false,"error_count":1}}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `message_not_found` (exit 1): No message at the given timestamp.
- `not_authed` (exit 2): No token.

Slack API: `conversations.history` (filtered), with `chat.getPermalink` + `conversations.replies` fallback for thread replies

#### slack message permalink (Phase 2)

```
slack message permalink <channel> <timestamp>...
```

This command produces a permalink, so it takes only a channel + timestamps - it does not accept a URL (feeding a permalink in to get one back is a no-op).

```
$ slack message permalink #general 1709251200.000100
{"input":"1709251200.000100","channel":"C01ABC","ts":"1709251200.000100","permalink":"https://acme.slack.com/archives/C01ABC/p1709251200000100"}
{"_meta":{"has_more":false}}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `not_authed` (exit 2): No token.

Slack API: `chat.getPermalink`

### thread

#### slack thread list

Aliased as `slack thread read`. Includes the parent message as the first result (the parent can be identified by `ts == thread_ts`).

```
slack thread list <channel> <timestamp> [flags]
slack thread list <message-url> [flags]
  --limit    Page size (default: 50, max: 200)
  --cursor   Continue from previous page
  --all      Fetch all pages
```

A message permalink replaces channel + timestamp. A link to any reply resolves to its parent thread (via `thread_ts`), so pasting any message in a thread returns the whole thread.

```
$ slack thread list #general 1709251200.000100 --limit=3
{"type":"message","user":"U01XYZ","text":"Hey team, the deploy is done.","ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z","thread_ts":"1709251200.000100","reply_count":3,"reply_users":["U02ABC","U03DEF"],"latest_reply":"1709251500.000200","latest_reply_iso":"2024-03-01T00:05:00Z"}
{"type":"message","user":"U02ABC","text":"Nice! Any issues?","ts":"1709251300.000150","ts_iso":"2024-03-01T00:01:40Z","thread_ts":"1709251200.000100"}
{"type":"message","user":"U01XYZ","text":"All clean so far.","ts":"1709251400.000180","ts_iso":"2024-03-01T00:03:20Z","thread_ts":"1709251200.000100"}
{"_meta":{"has_more":true,"next_cursor":"bmV4dF90czox"}}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `thread_not_found` (exit 1): No message at the given timestamp, or message has no replies.
- `not_authed` (exit 2): No token.

Slack API: `conversations.replies`

### user

#### slack user list

```
slack user list [flags]
  --limit       Page size (default: 100, max: 200)
  --cursor      Continue from previous page
  --all         Fetch all pages
  --query       Filter by name or email substring (client-side)
  --presence    Include presence information (requires additional API call per user)
```

```
$ slack user list --limit=2
{"id":"U01XYZ","team_id":"T01ABC","name":"tammer","real_name":"Tammer Saleh","deleted":false,"is_bot":false,"is_admin":true,"updated":1709251200,"profile":{"email":"tammer@example.com","display_name":"tammer","real_name":"Tammer Saleh","status_text":"","status_emoji":"","image_48":"https://avatars.slack-edge.com/U01XYZ_48.jpg"}}
{"id":"U02ABC","team_id":"T01ABC","name":"alice","real_name":"Alice Smith","deleted":false,"is_bot":false,"is_admin":false,"updated":1709164800,"profile":{"email":"alice@example.com","display_name":"alice","real_name":"Alice Smith","status_text":"On vacation","status_emoji":":palm_tree:","image_48":"https://avatars.slack-edge.com/U02ABC_48.jpg"}}
{"_meta":{"has_more":true,"next_cursor":"dXNlcjpV"}}
```

```
$ slack user list --query=tammer --fields=id,name,profile
{"id":"U01XYZ","name":"tammer","profile":{"email":"tammer@example.com","display_name":"tammer","real_name":"Tammer Saleh","status_text":"","status_emoji":"","image_48":"https://avatars.slack-edge.com/U01XYZ_48.jpg"}}
{"_meta":{"has_more":false,"filter_exhaustive":true}}
```

Errors:

- `not_authed` (exit 2): No token.

Slack API: `users.list`

#### slack user info

```
slack user info <user>...
  --full        Fetch custom profile fields (Manager, Division, Department,
                Employee ID, etc.) via users.profile.get
```

```
$ slack user info tammer@example.com
{"input":"tammer@example.com","id":"U01XYZ","team_id":"T01ABC","name":"tammer","real_name":"Tammer Saleh","deleted":false,"is_bot":false,"is_admin":true,"updated":1709251200,"profile":{"email":"tammer@example.com","display_name":"tammer","real_name":"Tammer Saleh","status_text":"","status_emoji":"","image_48":"https://avatars.slack-edge.com/U01XYZ_48.jpg"}}
{"_meta":{"has_more":false}}
```

Plain `user info` returns `profile.fields: []` - Slack does not expand custom
profile fields there. `--full` adds a top-level `custom_fields` object keyed by
normalized snake_case label. The base `slack.User` shape is unchanged
(additive); `profile.fields` stays `[]`. User-ID-valued fields (Manager) get a
resolved `value_name`.

```
$ slack user info --full @alice
{"input":"@alice","id":"U01XYZ","name":"alice","real_name":"Alice Adams","profile":{...},"custom_fields":{"manager":{"id":"Xf01AAA","label":"Manager","value":"U02MGR","value_name":"Bob Brown"},"title":{"id":"Xf01BBB","label":"Title","value":"VP, Engineering"},"division":{"id":"Xf01CCC","label":"Division","value":"Technology"},"employee_id":{"id":"Xf01DDD","label":"Employee ID","value":"1234"}}}
{"_meta":{"has_more":false}}
```

`--full` needs the `users.profile:read` scope. Desktop session tokens have it;
OAuth tokens issued before this scope was added must re-auth (`slack auth login`).

A failed lookup for one input is an error row, `{"input","error","detail","hint"}`,
counted in `_meta.error_count`, exit 1. `error` is `user_not_found` when Slack
says the user does not exist or is not visible (`user_not_found`,
`users_not_found`, `user_not_visible`). Any other per-user failure keeps
Slack's own code, so a transient failure is not reported as a missing user.

A failure that is not about one user stops the run: an auth error,
`missing_scope`, a rate limit that outlasted its retries, a network error, or
the `--timeout` budget. The classified error goes to stderr with its exit
code, and stdout ends with a trailer in the `channel invite` shape:

```
$ slack user info --full U01XYZ U02MGR U03ABC U04DEF
{"input":"U01XYZ","id":"U01XYZ",...}
{"input":"U02MGR","id":"U02MGR",...}
{"_meta":{"has_more":true,"error":"rate_limited"}}
```

The rows printed are authoritative. `has_more:true` means some inputs have no
row and were not looked up. There is no cursor: rerun the inputs that have no
row (every row carries `input`).

`users.profile.get` is rate-limited after a few calls, and reports it in the
response body with no `Retry-After`. Each profile fetch goes through the shared
retry (5 attempts, one second apart), which rides out a brief limit. It does
not pace a long list: a bulk `--full` run can still exhaust the retries and
stop with the trailer above. Spacing calls about 15 seconds apart has worked.

Errors:

- `user_not_found` (exit 1): No user matching the input.
- `missing_scope` (exit 2): Token lacks `users.profile:read` (with `--full`).
- `rate_limited` (exit 3): Rate limit outlasted the retries.
- `not_authed` (exit 2): No token.

Slack API: `users.info` (by ID), `users.lookupByEmail` (by email),
`users.profile.get` (with `--full`)

#### slack user manager-chain

```
slack user manager-chain <user>...
  --manager-field   Custom profile field label holding the manager reference
                    (default: "Manager")
```

Walks the management chain upward, one JSONL row per level. Slack stores no
reliable downward (direct-reports) data, so traversal is upward-only.

```
$ slack user manager-chain @alice
{"input":"@alice","root_user_id":"U01XYZ","level":0,"id":"U01XYZ","display_name":"alice","real_name":"Alice Adams","title":"VP, Engineering","manager_id":"U02MGR","manager_name":"Bob Brown"}
{"input":"@alice","root_user_id":"U01XYZ","level":1,"id":"U02MGR","display_name":"bbrown","real_name":"Bob Brown","title":"Chief Revenue Officer","manager_id":"U03CEO","manager_name":"Carol Chen"}
{"input":"@alice","root_user_id":"U01XYZ","level":2,"id":"U03CEO","display_name":"cchen","real_name":"Carol Chen","title":"CEO","stop_reason":"no_manager"}
{"_meta":{"has_more":false,"error_count":0}}
```

The terminal row carries a `stop_reason`:

- `no_manager`: no manager field set (not an error).
- `invalid_manager_value`: manager field is not a user ID (error).
- `ambiguous_manager_field`: more than one field matches `--manager-field` (error).
- `cycle_detected`: a manager loop was hit (error).
- `max_depth`: chain exceeded the depth cap of 20 (error).
- `profile_lookup_failed`: a hop's profile fetch failed (error).

Error stop_reasons increment `_meta.error_count` and set exit code 1.

A root lookup that fails for a reason other than "no such user" keeps Slack's
own code on its error row. Systemic failures stop the run the way `user info`
does: classified error on stderr, and stdout ends with
`{"_meta":{"has_more":true,"error":"<code>","error_count":N}}`. The chain being
walked when the run stopped emits no rows. Profile fetches go through the same
retry as `user info --full`.

Errors:

- `user_not_found` (exit 1): Root user not found.
- `missing_scope` (exit 2): Token lacks `users.profile:read`.
- `rate_limited` (exit 3): Rate limit outlasted the retries.
- `not_authed` (exit 2): No token.

Slack API: `users.profile.get`

#### slack user lookup (Phase 2)

```
slack user lookup <email>...
```

Shorthand for `slack user info <email>`. Identical behavior; exists for discoverability.

Slack API: `users.lookupByEmail`

### reaction

#### slack reaction list

```
slack reaction list <channel> <timestamp>...
```

Returns individual reactions, not the full message. Each reaction is a separate JSONL line. Like `message get`, it accepts a channel plus timestamps or one-or-more message permalinks (which may span channels); rows carry `channel_id`.

```
$ slack reaction list #general 1709251200.000100
{"input":"1709251200.000100","name":"thumbsup","count":3,"users":["U01XYZ","U02ABC","U03DEF"]}
{"input":"1709251200.000100","name":"tada","count":1,"users":["U02ABC"]}
{"_meta":{"has_more":false}}
```

If the message has no reactions, only the `_meta` line is emitted.

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `message_not_found` (exit 1): No message at the given timestamp.
- `not_authed` (exit 2): No token.

Slack API: `reactions.get`

### search (Phase 2)

Requires a user token (`xoxp-`).

#### slack search messages

```
slack search messages <query> [flags]
  --limit       Page size (default: 20, max: 100)
  --cursor      Continue from previous page
  --all         Fetch all pages
  --sort        timestamp, score (default: timestamp)
  --sort-dir    asc, desc (default: desc)
```

```
$ slack search messages "deploy failed" --limit=2
{"type":"message","channel":{"id":"C01ABC","name":"general"},"user":"U01XYZ","username":"tammer","text":"The deploy failed at 3am, investigating now.","ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z","permalink":"https://acme.slack.com/archives/C01ABC/p1709251200000100"}
{"type":"message","channel":{"id":"C03GHI","name":"engineering"},"user":"U02ABC","username":"alice","text":"deploy failed again, same error as last week","ts":"1709164800.000050","ts_iso":"2024-02-29T00:00:00Z","permalink":"https://acme.slack.com/archives/C03GHI/p1709164800000050"}
{"_meta":{"has_more":true,"next_cursor":"c2VhcmNo"}}
```

Supports Slack search modifiers in the query string: `in:#channel`, `from:@user`, `has:link`, `has:reaction`, `has::eyes:` (a specific emoji reaction; `has:my::eyes:` for your own), `before:2024-03-01`, `after:2024-02-01`, etc.

`@user` in `from:`, `to:`, and `in:` is resolved before the query ships. Slack's
search API matches `from:@handle` and `from:<@Uxxx>` but silently matches nothing
on `from:@Real Name`, `from:@Uxxx`, or `from:"Real Name"` (verified 2026-09-21).
The CLI resolves the name through the user cache (handle, display name, real
name, email, or ID) and rewrites the modifier to `from:<@Uxxx>`. An unquoted
multi-word name extends to the next modifier-shaped token; the longest prefix
that resolves wins, so `from:@Alice Adams skypilot` resolves Alice Adams and
keeps `skypilot` as free text. Quoted forms `from:"Alice Adams"`, `from:"@Alice Adams"`, and
`from:@"Alice Adams"` take the name whole; the `@` is optional inside quotes for
`from:`/`to:` but required for `in:`, whose quoted value may be a channel. Values not starting with `@`
(`in:#general`, `from:<@U01XYZ>`) pass through untouched. A name that does not
resolve fails with `user_not_found` (exit 1) before any search call. A name two
users share resolves to whichever the cache indexed first. Applies to `search
files` too.

Errors:

- `missing_user_token` (exit 2): Search requires a user token. Set `SLACK_USER_TOKEN` or re-run `slack auth login`.
- `not_authed` (exit 2): No token.

Slack API: `search.messages`

#### slack search files

Same flags as `search messages`.

```
$ slack search files "quarterly report" --limit=2
{"id":"F01ABC","name":"Q1-report.pdf","title":"Q1 Quarterly Report","mimetype":"application/pdf","filetype":"pdf","size":1048576,"user":"U01XYZ","created":1709251200,"permalink":"https://acme.slack.com/files/U01XYZ/F01ABC/q1-report.pdf"}
{"id":"F02DEF","name":"Q4-report.xlsx","title":"Q4 Quarterly Report","mimetype":"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet","filetype":"xlsx","size":524288,"user":"U02ABC","created":1701388800,"permalink":"https://acme.slack.com/files/U02ABC/F02DEF/q4-report.xlsx"}
{"_meta":{"has_more":false}}
```

Errors:

- `missing_user_token` (exit 2): Search requires a user token.
- `not_authed` (exit 2): No token.

Slack API: `search.files`

### file (Phase 2)

#### slack file list

```
slack file list [flags]
  --limit      Page size (default: 20, max: 100)
  --cursor     Continue from previous page
  --all        Fetch all pages
  --channel    Filter by channel
  --user       Filter by user
  --type       Filter by file type (e.g., pdf, png, zip)
```

```
$ slack file list --channel=#general --limit=2
{"id":"F01ABC","name":"Q1-report.pdf","title":"Q1 Quarterly Report","mimetype":"application/pdf","filetype":"pdf","size":1048576,"user":"U01XYZ","created":1709251200,"url_private":"https://files.slack.com/files-pri/T01ABC-F01ABC/q1-report.pdf","permalink":"https://acme.slack.com/files/U01XYZ/F01ABC/q1-report.pdf"}
{"id":"F02DEF","name":"screenshot.png","title":"Bug Screenshot","mimetype":"image/png","filetype":"png","size":245760,"user":"U02ABC","created":1709164800,"url_private":"https://files.slack.com/files-pri/T01ABC-F02DEF/screenshot.png","permalink":"https://acme.slack.com/files/U02ABC/F02DEF/screenshot.png"}
{"_meta":{"has_more":true,"next_cursor":"ZmlsZXM"}}
```

Errors:

- `not_authed` (exit 2): No token.

Slack API: `files.list`

#### slack file info

Accepts a bare file ID or a Slack file URL (`/files/<uploader>/<F-id>/…`). A Slack List URL (`/lists/<team>/<F-id>`) is also accepted, since lists are files.

```
slack file info <file-id-or-url>...
```

```
$ slack file info F01ABC
{"input":"F01ABC","id":"F01ABC","name":"Q1-report.pdf","title":"Q1 Quarterly Report","mimetype":"application/pdf","filetype":"pdf","size":1048576,"user":"U01XYZ","created":1709251200,"url_private":"https://files.slack.com/files-pri/T01ABC-F01ABC/q1-report.pdf","url_private_download":"https://files.slack.com/files-pri/T01ABC-F01ABC/download/q1-report.pdf","permalink":"https://acme.slack.com/files/U01XYZ/F01ABC/q1-report.pdf","channels":["C01ABC"],"comments_count":2}
{"_meta":{"has_more":false}}
```

Errors:

- `file_not_found` (exit 1): No file matching the ID.
- `not_authed` (exit 2): No token.

Slack API: `files.info`

#### slack file download

File content is written to disk. JSONL metadata about the download goes to stdout. The file argument may be a bare ID or a Slack file URL.

```
slack file download <file-id-or-url> [flags]
  --output     Output path (default: original filename in current directory, "-" for stdout)
```

```
$ slack file download F01ABC
{"input":"F01ABC","id":"F01ABC","name":"Q1-report.pdf","size":1048576,"bytes_written":1048576,"path":"./Q1-report.pdf"}
{"_meta":{"has_more":false}}
```

`size` is what `files.info` reports; `bytes_written` is what the command actually wrote to `path`. They differ when Slack's metadata is wrong - every Slack List reports `size: 0` while its export body is hundreds of KB.

With `--output=-`, raw file bytes go to stdout and JSONL metadata is suppressed, so no `bytes_written` is reported.

Errors:

- `file_not_found` (exit 1): No file matching the ID.
- `not_authed` (exit 2): No token.

Slack API: `files.info` (to get download URL), then HTTP GET

### list

Slack Lists. A list is a file (`filetype: list`), so `<list>` is an `F…` id, a
`/lists/<team>/<F…>` URL, or a `/files/…/<F…>` URL. Every list command uses the
session (`xoxc-`) token and form-encoded internal calls: `files.info` is read as
raw JSON because slack-go's `File` drops `list_metadata` and `list_limits`, and
`lists.records.list` is undocumented. On Enterprise Grid the org (`E`) token
serves all of them, so there is no two-client split; a bot token fails
`session_token_required` (exit 2) before any call.

Column values are keyed by column display name. Keys are assigned in schema
order: a name already taken gets `_2`, `_3`, ... by probing for an unused key,
so with columns `Foo`, `Foo_2`, `Foo` the third is `Foo_3`. A nameless column is
keyed by its column id.

#### slack list info

```
slack list info <list>
```

```
$ slack list info F01ABC
{"id":"F01ABC","title":"Launch tasks","description":"Everything before launch","icon":":rocket:","user":"U01XYZ","created":1709251200,"updated":1709337600,"permalink":"https://acme.slack.com/lists/T01ABC/F01ABC","limits":{"row_count":53,"row_count_limit":5000,"archived_row_count":2,"over_row_maximum":false,"column_count":7,"column_count_limit":30,"view_count":1,"view_count_limit":50}}
{"_meta":{"has_more":false}}
```

`limits` is Slack's `list_limits` block passed through. `archived_row_count`
is the only signal that the list holds rows `list records` will never show.

Errors:

- `file_not_found` (exit 1): No file matching the id.
- `not_a_list` (exit 1): The id names a file that is not a Slack List.
- `session_token_required` (exit 2): Bot token.

Slack API: `files.info`

#### slack list schema

One row per column, in schema order. `format` and `choices` appear only when
the column carries them (`select` and `multi_select` columns have both).

```
slack list schema <list>
```

```
$ slack list schema F01ABC
{"column_id":"Col01AAA","key":"name","name":"Name","type":"text","is_primary_column":true,"format":"text"}
{"column_id":"Col01CCC","key":"Col01BBB","name":"Status","type":"select","is_primary_column":false,"format":"single_select","choices":[{"value":"todo","label":"To do","color":"gray"},{"value":"done","label":"Done","color":"green"}]}
{"column_id":"Col01DDD","key":"Col01DDK","name":"Owner","type":"user","is_primary_column":false}
{"_meta":{"has_more":false}}
```

`column_id` is what record fields join on. `key` is what the write API
addresses columns by; the primary text column's key is `name`.

Slack API: `files.info`

#### slack list records

```
slack list records <list> [flags]
  --limit      Page size (default: 100; Slack rejects 1, so --limit 1 is invalid_input)
  --cursor     Continue from previous page
  --all        Fetch all pages
  --view       Read through a view (view_id)
  --raw        Add raw_fields holding the original field objects
```

```
$ slack list records F01ABC --limit 2
{"id":"Rec01AAA","created_by":"U01XYZ","updated_by":"U01XYZ","date_created":1709251200,"updated_timestamp":"1709251300","position":"1709251200.12345","fields":{"Name":"Ship it","Status":["Done"],"Owner":["U01XYZ"],"Due":"2026-05-21","Shipped":null,"Link":["https://example.com"]}}
{"id":"Rec01BBB","created_by":"U02MGR","updated_by":"U01XYZ","date_created":1709251260,"updated_timestamp":"1709251400","position":"1709251260.00001","thread_ts":"1709251261.000100","fields":{"Name":"Write the announcement","Status":["To do"],"Owner":[],"Due":null,"Shipped":false,"Link":[]}}
{"_meta":{"has_more":true,"next_cursor":"bGlzdF9pZDo","row_count":53,"archived_row_count":2}}
```

Record metadata sits at the top level (`id`, `created_by`, `updated_by`,
`date_created`, `updated_timestamp`, `position`, and `thread_ts` when the record
has one). Column values sit under `fields`; every schema column is present and
`null` when the record has no value. `--fields` filters the top level only.

The schema is read once from `files.info` before the first page and every page
is joined against it. Rendering by column type:

| column type | value |
|---|---|
| `text`, `rich_text` | Slack's rendered mrkdwn string (`<@U…>` mentions, `• ` bullets); the block array is only in `--raw` |
| `select`, `multi_select` | array of choice labels; a value with no matching choice is emitted as-is |
| `user`, `created_by`, `todo_assignee` | array of user ids |
| `channel` | array of channel ids |
| `date` | `YYYY-MM-DD` string |
| `created_time` | epoch seconds |
| `checkbox`, `todo_completed` | bool |
| `number` | number |
| `email` | array of addresses |
| `link` | array of URLs |
| `message` | `[{permalink, channel_id, ts, thread_ts}]` (`thread_ts` is `0000000000.000000` for a non-reply); the standard timestamp enrichment adds `ts_iso` and `thread_ts_iso` beside them |
| anything else | the raw field object minus `key` and `column_id` |

The trailer carries `row_count` and `archived_row_count` from Slack's limits
block. Archived records never appear in the stream, so a caller comparing rows
emitted to `row_count` should expect the difference to be `archived_row_count`.

Record order within a walk is not promised. It was not creation order or
position order on the lists measured, and whether `--view` changes it is
unverified.

Errors:

- `invalid_input` (exit 1): `--limit 1`, `--all` with `--cursor`, or a URL of the wrong kind.
- `file_not_found` / `not_a_list` (exit 1): As for `list info`.
- `list_schema_mismatch` (exit 1): A record carried a `column_id` the schema does not have, meaning the list was edited between the schema read and that page. The page is dropped, the trailer carries `has_more:false` and the error with no resume cursor; rerun the command.
- `invalid_cursor` (exit 1): Slack rejected `--cursor`.
- `session_token_required` (exit 2): Bot token.

Slack API: `files.info`, then `lists.records.list` (internal; `list_id`, `limit`, `cursor`, optional `view_id`)

#### slack list find

Finds lists by title with `search.files` and the query `type:list "<title>"`.
A `"` inside the title becomes a space, since Slack search cannot escape one.
Titles are not unique - three lists sharing one title is normal - so this emits
one row per match and never picks one. The caller chooses an `id` to pass to the
other list commands.

```
slack list find <title> [flags]
  --limit      Page size (default: 20, max: 100)
  --cursor     Continue from previous page (page number)
  --all        Fetch all pages
```

```
$ slack list find "Launch tasks"
{"id":"F01ABC","title":"Launch tasks","user":"U01XYZ","created":1709251200,"permalink":"https://acme.slack.com/lists/T01ABC/F01ABC"}
{"id":"F02DEF","title":"Launch tasks","user":"U02MGR","created":1709164800,"permalink":"https://acme.slack.com/lists/T01ABC/F02DEF"}
{"_meta":{"has_more":false}}
```

Errors:

- `invalid_input` (exit 1): Blank title, `--limit` over 100, or `--all` with `--cursor`.
- `session_token_required` (exit 2): Bot token.

Slack API: `search.files`

#### slack list create

Creates a list, then adds each `--column` in order, then sets the icon. All
input is validated before the first call: non-blank name, every spec parsed,
known types, choices only on select types, no duplicate labels or colliding
values, icon shape.

```
slack list create <name> [flags]
  --description  List description
  --icon         Icon emoji name, rocket or :rocket: (Slack stores it with colons)
  --column SPEC  Column to add, NAME:TYPE[:CHOICE,CHOICE...]; repeatable, applied in order
```

`SPEC` is `NAME:TYPE[:CHOICE,CHOICE...]`. Types: `text`, `rich_text`,
`select`, `multi_select`, `date`, `user`, `checkbox`, `number`, `link`,
`email`, `channel`. Choices are only valid for `select` and `multi_select`.
Each choice's `value` is the slugified label (lowercase, non-alphanumeric runs
collapsed to `_`; a label with no ASCII alphanumerics gets `choice_N`), its
`color` comes round-robin from Slack's palette (blue, green, yellow, red,
purple, orange, cyan, pink, indigo, brown, gray). Labels must be unique and so
must their slugs (`To do` and `to-do` collide). A column name cannot contain a
colon.

```
$ slack list create "Launch tasks" --description "Everything before launch" --icon rocket --column "Owner:user" --column "Status:select:To do,Done"
{"id":"F01ABC","title":"Launch tasks","description":"Everything before launch","icon":":rocket:","user":"U01XYZ","created":1709251200,"updated":1709251200,"permalink":"https://acme.slack.com/lists/T01ABC/F01ABC","limits":{"row_count":0,"archived_row_count":0}}
{"_meta":{"has_more":false}}
```

The row is the server's final list object (same shape as `list info`), never
an echo of the flags. A failure after `lists.create` means the list exists
with some of the work done. The command emits a partial row on stdout and
exits with the classified error's code but no stderr error (an
`*output.ExitError`, not `_meta.error`, which would claim a truncated stream):

```
$ slack list create "Launch tasks" --icon rocket --column "Owner:user" --column "Status:select:a"
{"id":"F01ABC","error":"invalid_arguments","detail":"invalid_arguments: [ERROR] ...","failed_step":"column","failed_column":"Status","hint":"The list was created. Finish it with: slack list column add F01ABC 'Status:select:a' ; slack list update F01ABC --icon rocket"}
{"_meta":{"has_more":false}}
```

`failed_step` is `column` (with `failed_column`) or `icon`. The hint lists
the specs not yet added, failed one first, and the icon if one was given.
A response the command cannot read after `lists.create` is a partial row
too (`parse_error`), never a stderr error that would lose the id. The list
is never deleted on failure.

Errors:

- `invalid_input` (exit 1): Blank name, unparseable spec, unknown type, choices on a non-select type, duplicate or colliding choice labels, blank or malformed icon. No call is made.
- `invalid_arguments` (exit 1): Slack rejected a payload; `detail` carries Slack's `response_metadata.messages`.
- `session_token_required` (exit 2): Bot token.

Slack API: `lists.create` (`name`, `description`), then `lists.columns.create` (`list_id`, `type`, `name`, `options` JSON for select types) per column, then `lists.update` (`id`, `icon`). All internal.

#### slack list update

```
slack list update <list> [flags]
  --name         New title (must not be blank)
  --description  New description; "" clears it
  --icon         Icon emoji name, rocket or :rocket:
```

At least one flag is required. Only the flags given are sent; the others are
untouched. The row is the returned list object, same shape as `list info`.

```
$ slack list update F01ABC --name "Launch tasks (Q2)" --icon :wrench:
{"id":"F01ABC","title":"Launch tasks (Q2)","description":"Everything before launch","icon":":wrench:","user":"U01XYZ","created":1709251200,"updated":1709337700,"permalink":"https://acme.slack.com/lists/T01ABC/F01ABC","limits":{"row_count":53,"archived_row_count":2}}
{"_meta":{"has_more":false}}
```

Errors:

- `invalid_input` (exit 1): No flags, blank `--name`, blank `--icon`, or a URL of the wrong kind. No call is made.
- `list_not_found` (exit 1): Slack's own code for an unknown id.
- `session_token_required` (exit 2): Bot token.

Slack API: `lists.update` (`id`, any of `name`, `description`, `icon`)

#### slack list column

Column commands read the schema from `files.info` first, so `<column>` can be
a column key, a column id, or a display name. An exact id or key match wins;
otherwise the name is compared case-insensitively and must match exactly one
column (`ambiguous_column` names the candidates). Resolution failures make no
write.

```
slack list column add <list> SPEC...
slack list column rename <list> <column> <name>
slack list column set-choices <list> <column> CHOICE...
slack list column delete <list> <column>
```

`add` makes one `lists.columns.create` call per spec in order, emitting one
row per column as the server returned it (`list schema`'s shape plus
`list_id`). Every spec is validated before the first call. A failure stops
the command there: the columns already added have their rows, the failed
spec gets `{"list_id","name","type","error","detail"}`, and the exit is the
classified error's code without a stderr error. A response the command
cannot read (no list object, or no single new column id in it) is reported
the same way as `parse_error`, since the column may or may not exist.

```
$ slack list column add F01ABC "Tags:multi_select:a,b" "Notes:rich_text"
{"list_id":"F01ABC","column_id":"Col01KKK","key":"Col01KKL","name":"Tags","type":"multi_select","is_primary_column":false,"format":"multi_select","choices":[{"value":"a","label":"a","color":"blue"},{"value":"b","label":"b","color":"green"}]}
{"list_id":"F01ABC","column_id":"Col01LLL","key":"Col01LLM","name":"Notes","type":"rich_text","is_primary_column":false}
{"_meta":{"has_more":false}}
```

`rename` sends only `name`; id, key, type, and choices are untouched. Slack
allows two columns with the same name.

```
$ slack list column rename F01ABC Status State
{"list_id":"F01ABC","column_id":"Col01CCC","key":"Col01BBB","name":"State","type":"select","is_primary_column":false,"format":"single_select","choices":[{"value":"todo","label":"To do","color":"gray"},{"value":"done","label":"Done","color":"green"}]}
{"_meta":{"has_more":false}}
```

`set-choices` replaces the whole choice set of a `select` or `multi_select`
column. The current choices are read from the schema; a label matching an
existing choice exactly (case-sensitive) keeps that choice's `value` and
`color`, so records holding it stay valid. Every other label is a new choice
with a slug value and a palette color by position. A renamed label is
remove-plus-add and gets a new value - the command cannot infer renames.
Duplicate labels, and a new slug colliding with another value (new or
preserved), are `invalid_input` with no write. Other option keys Slack holds
(`default_value`, and anything not modeled here) are sent back unchanged,
except a `default_value` naming a removed choice, which is dropped. This is a
read-then-replace: another editor's change between the schema read and the
write is overwritten, and the endpoint offers nothing atomic (same as
`section rename`). What Slack does to records holding a removed value is
unverified.

```
$ slack list column set-choices F01ABC Status Done Blocked "To do"
{"list_id":"F01ABC","column_id":"Col01CCC","key":"Col01BBB","name":"Status","type":"select","is_primary_column":false,"format":"single_select","choices":[{"value":"done","label":"Done","color":"green"},{"value":"blocked","label":"Blocked","color":"green"},{"value":"todo","label":"To do","color":"gray"}]}
{"_meta":{"has_more":false}}
```

`delete` removes the column. `lists.columns.delete` answers a bare `{ok}`, so
the row is the column as resolved before the call plus `status`.

```
$ slack list column delete F01ABC Owner
{"list_id":"F01ABC","column_id":"Col01DDD","key":"Col01DDK","name":"Owner","type":"user","status":"deleted"}
{"_meta":{"has_more":false}}
```

Errors:

- `invalid_input` (exit 1): Bad spec (`add`), blank name (`rename`), a non-select column or bad labels (`set-choices`), or a URL of the wrong kind. No write is made.
- `column_not_found` / `ambiguous_column` (exit 1): `<column>` matched zero or several columns. No write is made.
- `file_not_found` / `not_a_list` (exit 1): As for `list info`.
- `invalid_arguments` (exit 1): Slack rejected a payload; `detail` carries `response_metadata.messages`.
- `session_token_required` (exit 2): Bot token.

Slack API: `files.info`, then `lists.columns.create` (`list_id`, `type`, `name`, `options`), `lists.columns.update` (`list_id`, `id`, `name` or `options`), or `lists.columns.delete` (`list_id`, `id`). All internal; `id` is the column id, not its key.

#### slack list record

```
slack list record add <list> [COLUMN=VALUE...]
slack list record update <list> <record-id> [COLUMN=VALUE...]
```

Both commands take assignments as positional `COLUMN=VALUE` pairs, split on
the first `=`, or as one JSON object on stdin (`{"Status":"Done"}`) for
values that hold `=`, newlines, booleans, numbers, arrays, or `null`. The two
forms are mutually exclusive (and the object must be the only thing on stdin),
and at least one assignment is required (Slack creates an empty record without
complaint). `COLUMN` resolves like
`<column>` elsewhere: exact key or id, else a unique case-insensitive display
name (`ambiguous_column`). Two assignments naming one column are
`invalid_input`.

Every assignment is validated before the first call, because
`lists.records.create` silently drops a key that is not in the schema and
stores a select value that is not one of the choices:

- `text`, `rich_text`: a string.
- `select`: one choice, matched by value, then exact label, then case-insensitive label; otherwise `invalid_choice`.
- `multi_select`: on `update`, comma-separated choices positionally or a JSON array on stdin (use the array when a label holds a comma); duplicates naming one choice collapse. On `add` it is `invalid_input` (the create API takes one scalar; sending one of several is unverified).
- `checkbox`: `true` or `false` (a JSON bool on stdin).
- `number`: a decimal number (JSON number syntax), finite. Sent as typed, so `3.50` stays `3.50`.
- `date`: a real calendar date `YYYY-MM-DD`.
- `user`: `@name`, email, `U…` id, or profile URL, resolved before the write (`user_not_found` makes no call).
- `channel`: `#name`, `C…` id, or channel URL, resolved before the write (`channel_not_found`). A name resolves on the workspace (`T`) credential, because on Enterprise Grid the org token the other list calls use cannot list channels; ids and URLs need no workspace credential, and a name without one is `not_authed`.
- `email`: one address.
- `link`: an `http(s)` URL.
- `created_by`, `created_time`, `message`, `todo_*`, and unknown types cannot be written (`invalid_input`).

An empty positional value (`Status=`) or a JSON `null` clears the cell. On
`update` that is allowed for `select`, `multi_select`, and `user` columns (the
shapes verified live) and `invalid_input` for every other type until clearing
them is measured. On `add` an empty value is `invalid_input`: omit the column.

`add` sends one `lists.records.create` with `fields` as `[{key, value}]`,
addressed by column **key**, with scalar values (string, bool, or number).
The row is the server's `record` rendered exactly like a `list records` row,
so unassigned columns appear as `null`. If the create succeeds but its
response holds no readable record, the row is partial (`{"list_id","error",
"detail"}`) with the classified exit code and no stderr error, since the
record exists.

```
$ slack list record add F01ABC "Name=Ship it" Status=Done Owner=@alice Due=2026-12-01 Shipped=true
{"id":"Rec01AAAAAAA","created_by":"U01XYZ","updated_by":"U01XYZ","date_created":1709251200,"updated_timestamp":"1709251300","position":"1709251200.00001","fields":{"Name":"Ship it","Status":["Done"],"Owner":["U01XYZ"],"Due":"2026-12-01","Shipped":true,"Link":null}}
{"_meta":{"has_more":false}}
```

`update` sends one `lists.cells.update` with `cells` as
`[{row_id, column_id, <typed>}]`, every assignment in one call, addressed by
column **id**. The typed shapes: `rich_text` is a one-section block array,
`select` an array of choice values (`[]` clears), `checkbox` a bool, and
`number`, `date`, `user`, `email`, `channel` one-element arrays (`user: []`
clears); `link` is `[{"original_url": …}]` (snake_case on write, `originalUrl`
on read). `cells.update` echoes only the changed cells, so the command then
reads `lists.records.info` and emits the complete record. If that re-read
fails after the write succeeded, the result is a partial row
`{"id","list_id","error","detail"}` with the classified exit code and no
stderr error - the write landed.

```
$ slack list record update F01ABC Rec01AAAAAAA Status="To do" Owner=
{"id":"Rec01AAAAAAA","created_by":"U01XYZ","updated_by":"U01XYZ","date_created":1709251200,"updated_timestamp":"1709251400","position":"1709251200.00001","fields":{"Name":"Ship it","Status":["To do"],"Owner":[],"Due":"2026-12-01","Shipped":true,"Link":null}}
{"_meta":{"has_more":false}}
```

`<record-id>` must match `^Rec[A-Z0-9]{8,}$`; Slack schema-validates it
before any lookup, so the CLI rejects a malformed id locally with no call.

Errors:

- `invalid_input` (exit 1): Bad pair, both input forms, no assignments, duplicate column, a value of the wrong shape for its type, a clear where clearing is unsupported, a malformed record id, or a URL of the wrong kind. No write is made.
- `column_not_found` / `ambiguous_column` (exit 1): As for `list column`. No write is made.
- `invalid_choice` (exit 1): A select value matching no choice. No write is made.
- `user_not_found` / `channel_not_found` (exit 1): A user or channel column value did not resolve. No write is made.
- `file_not_found` / `not_a_list` (exit 1): As for `list info`.
- `record_not_found` (exit 1): `update` named a record Slack does not have.
- `invalid_arguments`, `invalid_option_id` (exit 1): Slack rejected a payload; `detail` carries `response_metadata.messages`.
- `session_token_required` (exit 2): Bot token.

Slack API: `files.info`, then `lists.records.create` (`list_id`, `fields`) or `lists.cells.update` (`list_id`, `cells`) followed by `lists.records.info` (`list_id`, `id`). All internal.

### pin (Phase 2)

#### slack pin list

```
slack pin list <channel>
```

```
$ slack pin list #general
{"type":"message","channel":"C01ABC","created":1709251200,"created_by":"U01XYZ","message":{"user":"U01XYZ","text":"Important: new deploy process starts Monday.","ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z"}}
{"type":"file","channel":"C01ABC","created":1709164800,"created_by":"U02ABC","file":{"id":"F01ABC","name":"guidelines.pdf","title":"Team Guidelines","filetype":"pdf","size":524288}}
{"_meta":{"has_more":false}}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `not_authed` (exit 2): No token.

Slack API: `pins.list`

### bookmark (Phase 2)

#### slack bookmark list

```
slack bookmark list <channel>
```

```
$ slack bookmark list #general
{"id":"Bk01ABC","channel_id":"C01ABC","title":"Team Wiki","link":"https://wiki.example.com","emoji":":book:","type":"link","date_created":1709251200,"date_updated":1709251200}
{"id":"Bk02DEF","channel_id":"C01ABC","title":"Sprint Board","link":"https://jira.example.com/board/123","emoji":":clipboard:","type":"link","date_created":1709164800,"date_updated":1709164800}
{"_meta":{"has_more":false}}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `not_authed` (exit 2): No token.

Slack API: `bookmarks.list`

### status (Phase 2)

#### slack status get

Defaults to the authenticated user when no argument is given.

```
slack status get [user...]
```

```
$ slack status get
{"user_id":"U01XYZ","status_text":"In a meeting","status_emoji":":calendar:","status_expiration":1709254800}
{"_meta":{"has_more":false}}
```

```
$ slack status get tammer@example.com alice@example.com
{"input":"tammer@example.com","user_id":"U01XYZ","status_text":"In a meeting","status_emoji":":calendar:","status_expiration":1709254800}
{"input":"alice@example.com","user_id":"U02ABC","status_text":"On vacation","status_emoji":":palm_tree:","status_expiration":0}
{"_meta":{"has_more":false}}
```

Errors:

- `user_not_found` (exit 1): No user matching the input.
- `not_authed` (exit 2): No token.

Slack API: `users.info` (status fields extracted from profile)

#### slack presence get

```
slack presence get [user...]
```

```
$ slack presence get tammer@example.com
{"input":"tammer@example.com","user_id":"U01XYZ","presence":"active","online":true,"auto_away":false,"manual_away":false}
{"_meta":{"has_more":false}}
```

Errors:

- `user_not_found` (exit 1): No user matching the input.
- `not_authed` (exit 2): No token.

Slack API: `users.getPresence`

#### slack dnd info

```
slack dnd info [user...]
```

```
$ slack dnd info tammer@example.com
{"input":"tammer@example.com","user_id":"U01XYZ","dnd_enabled":true,"next_dnd_start_ts":1709272800,"next_dnd_end_ts":1709308800,"snooze_enabled":false}
{"_meta":{"has_more":false}}
```

Errors:

- `user_not_found` (exit 1): No user matching the input.
- `not_authed` (exit 2): No token.

Slack API: `dnd.info`

### usergroup (Phase 2)

#### slack usergroup list

Not paginated by Slack's API - returns all usergroups in a single call.

```
slack usergroup list [flags]
  --include-disabled   Include disabled usergroups
  --query              Filter by name substring (client-side)
```

```
$ slack usergroup list
{"id":"S01ABC","team_id":"T01ABC","name":"Engineering","handle":"engineering","description":"Engineering team","user_count":34,"is_external":false,"date_create":1609459200,"date_update":1709251200}
{"id":"S02DEF","team_id":"T01ABC","name":"Design","handle":"design","description":"Design team","user_count":12,"is_external":false,"date_create":1609459200,"date_update":1709164800}
{"_meta":{"has_more":false}}
```

Errors:

- `not_authed` (exit 2): No token.

Slack API: `usergroups.list`

#### slack usergroup members

Returns user IDs only. Use `slack user info` to enrich.

```
slack usergroup members <usergroup>
```

```
$ slack usergroup members @engineering
{"user_id":"U01XYZ"}
{"user_id":"U02ABC"}
{"user_id":"U03DEF"}
{"_meta":{"has_more":false}}
```

Errors:

- `usergroup_not_found` (exit 1): No usergroup matching the input.
- `not_authed` (exit 2): No token.

Slack API: `usergroups.users.list`

### emoji (Phase 2)

#### slack emoji list

Not paginated - returns all custom emoji in a single call.

```
slack emoji list [flags]
  --query    Filter by name substring (client-side)
```

```
$ slack emoji list --query=party
{"name":"partyparrot","url":"https://emoji.slack-edge.com/T01ABC/partyparrot/abc123.gif"}
{"name":"party_blob","url":"https://emoji.slack-edge.com/T01ABC/party_blob/def456.png"}
{"_meta":{"has_more":false}}
```

Errors:

- `not_authed` (exit 2): No token.

Slack API: `emoji.list`

### workspace (Phase 2)

#### slack workspace info

```
slack workspace info
```

```
$ slack workspace info
{"id":"T01ABC","name":"Acme Corp","domain":"acme","email_domain":"acme.com","icon":{"image_34":"https://a.slack-edge.com/icon_34.png","image_44":"https://a.slack-edge.com/icon_44.png"}}
{"_meta":{"has_more":false}}
```

Errors:

- `not_authed` (exit 2): No token.

Slack API: `team.info`

### saved (Phase 2)

Uses the internal `saved.list` API. Requires `xoxc-` session token.

#### slack saved list

```
slack saved list [flags]
  --limit              Max items to return (default: 20, max: 100)
  --cursor             Continue from previous page
  --all                Fetch all pages
  --enrich             Fetch message text and sender for each item
  --include-completed  Include completed items
```

```
$ slack saved list --limit=2
{"channel_id":"C01ABC","message_ts":"1709251200.000100","saved_at":"2024-03-01T00:00:00Z","todo_state":"uncompleted","permalink":"https://acme.slack.com/archives/C01ABC/p1709251200000100"}
{"channel_id":"C02DEF","message_ts":"1709164800.000050","saved_at":"2024-02-29T00:00:00Z","todo_state":"uncompleted","permalink":"https://acme.slack.com/archives/C02DEF/p1709164800000050"}
{"_meta":{"has_more":true,"next_cursor":"c2F2ZWQ"}}
```

With `--enrich`, each item adds `text` and `from_user`:

```
$ slack saved list --limit=1 --enrich
{"channel_id":"C01ABC","channel_name":"general","message_ts":"1709251200.000100","saved_at":"2024-03-01T00:00:00Z","todo_state":"uncompleted","text":"Hey team, the deploy is done.","from_user":"U01XYZ","permalink":"https://acme.slack.com/archives/C01ABC/p1709251200000100"}
{"_meta":{"has_more":true,"next_cursor":"c2F2ZWQ"}}
```

`channel_name` is not part of `--enrich`. It comes from the standard output
enrichment described under "Output enrichment", with or without the flag, and is
best-effort: eligible named-channel rows get it, DMs never do, and an unresolved
channel omits it.

Enrichment looks up each item's message with `conversations.history`, bounded to
10 items in flight. `conversations.history` never returns thread replies, so an
item the window does not contain falls back to `chat.getPermalink` plus
`conversations.replies` - the same path `message get` uses. A saved reply
therefore costs two extra requests.

Nothing retries an individual lookup. A rate-limited lookup fails the page,
which means `api.FetchPage` retries the whole fetch with `Retry-After` backoff -
so rate-limited enrichment is retried, but at page granularity: `saved.list` and
every already-successful lookup on that page are redone, and the resulting
`rate_limited` error names `saved.list` rather than the method that was actually
limited. The concurrency bound limits in-flight work, not request rate.

The message lookups use the workspace (`T`-prefix) token while `saved.list` uses
the session (`E`-prefix org) token, because `chat.getPermalink` returns
`enterprise_is_restricted` on the org context. So on Enterprise Grid `--enrich`
needs both credentials stored; a bare `saved list` needs only the session one and
resolves channel names through it, as before.

Two consequences on Grid. `SLACK_TOKEN` takes precedence over stored credentials
for every client, so setting it collapses both onto one token and reinstates
whichever restriction that context carries - leave it unset and select with
`SLACK_WORKSPACE` plus `SLACK_WORKSPACE_ORG`. And the workspace credential needs
the `:history` scopes (see README); a desktop session token has them already.

An item whose message cannot be looked up keeps its row and gains an
`enrich_error` code, so an unenriched row is never silently indistinguishable
from a message with no text:

```
$ slack saved list --enrich
{"channel_id":"C01ABC","channel_name":"general","message_ts":"1709251200.000100","saved_at":"2024-03-01T00:00:00Z","todo_state":"uncompleted","enrich_error":"not_in_channel","permalink":"https://acme.slack.com/archives/C01ABC/p1709251200000100"}
{"_meta":{"has_more":false}}
```

`enrich_error` values: `message_not_found`, `thread_not_found`,
`not_in_channel`, `channel_not_found`. A run containing any of them exits 1
after writing every row and the trailer, and writes nothing to stderr.

Any other lookup failure - auth, `missing_scope`, a rate limit, a network
error, or a code not listed above - is treated as a whole-run problem instead.
The page that hit it emits no rows, the error goes to stderr, and `_meta`
carries the code. Note a successfully fetched message with empty `text` is a
success, not an error: bot and block-only messages legitimately have none.

Errors:

- `not_authed` (exit 2): No session token (requires xoxc-).
- `session_token_required` (exit 2): Bot/user token provided but saved.list requires a session token.

Slack API: `saved.list` (internal, undocumented)

#### slack saved counts

```
slack saved counts
```

```
$ slack saved counts
{"uncompleted":12,"overdue":2,"completed":45,"total":59}
{"_meta":{"has_more":false}}
```

Slack API: `saved.list` (counts field from response)

### section (Phase 2)

Manages the user's sidebar channel sections. Uses internal Slack APIs.
Requires `xoxc-` session token. All operations affect only the
authenticated user's sidebar - no channel mutations.

#### slack section list

```
slack section list
```

```
$ slack section list
{"id":"S01ABC","name":"Channels","emoji":"","type":"channels","channel_count":42}
{"id":"S02DEF","name":"Customers","emoji":"handshake","type":"channels","channel_count":15}
{"id":"S03GHI","name":"Direct Messages","emoji":"","type":"ims","channel_count":28}
{"_meta":{"has_more":false}}
```

Slack API: `users.channelSections.list`

#### slack section channels

```
slack section channels <section-id>
```

```
$ slack section channels S02DEF
{"id":"C01ABC","name":"ext-acme","is_archived":false}
{"id":"C02DEF","name":"ext-globex","is_archived":false}
{"_meta":{"has_more":false}}
```

Channel names resolved concurrently (max 15 concurrent
`conversations.info` calls). Uses the CLI's channel cache.

Slack API: `users.channelSections.list` + `conversations.info`

#### slack section find

```
slack section find <pattern>
```

Searches all user channels by name substring (case-insensitive).

```
$ slack section find ext-
{"id":"C01ABC","name":"ext-acme","is_archived":false,"section_name":"Customers","section_id":"S02DEF"}
{"id":"C02DEF","name":"ext-globex","is_archived":false,"section_name":"Customers","section_id":"S02DEF"}
{"id":"C03GHI","name":"ext-initech","is_archived":false,"section_name":"Channels","section_id":"S01ABC"}
{"_meta":{"has_more":false}}
```

Slack API: `users.channelSections.list` + `conversations.info`

#### slack section move

```
slack section move --channels <id>[,<id>...] (--section <id> | --new-section <name> [--emoji <name>])
```

Moves channels to an existing section or creates a new one. Channel IDs are
trimmed and deduped. `--section` must name an existing section (a bad ID is
`section_not_found`, not a silent no-op). `moved_count` is verified: after the
update the sections are re-fetched and only channels now in the target section
(and no other) are counted. `--emoji` sets the new section's emoji and takes
the same values as `section create --emoji`; it is `invalid_input` without
`--new-section` (even as `--emoji ""`), with no API call.

```
$ slack section move --channels C01ABC,C02DEF --section S02DEF
{"moved_count":2,"target_section":"Customers"}
{"_meta":{"has_more":false}}
```

```
$ slack section move --channels C03GHI --new-section "Archive"
{"moved_count":1,"target_section":"Archive","target_section_id":"S04JKL"}
{"_meta":{"has_more":false}}
```

Slack API: `users.channelSections.channels.bulkUpdate`,
optionally `users.channelSections.create`. The bulkUpdate call is form-encoded
with `insert`/`remove` JSON arrays (`[{channel_section_id, channel_ids}]`), one
insert for the target and one remove per source section.

#### slack section create

```
slack section create <name> [--emoji <name>]
```

`--emoji` is a bare emoji name as `section list` emits it (`heart`,
`wave::skin-tone-2`, a custom emoji name). A value wrapped in one pair of
colons (`:heart:`) is unwrapped before sending; see `section emoji` for the
exact rule. Anything Slack does not accept is its own `emoji_invalid` (exit
1). The row always carries `emoji` (`""` when unset).

```
$ slack section create "Archive"
{"id":"S04JKL","name":"Archive","emoji":""}
{"_meta":{"has_more":false}}
```

```
$ slack section create "Archive" --emoji :file_cabinet:
{"id":"S04JKL","name":"Archive","emoji":"file_cabinet"}
{"_meta":{"has_more":false}}
```

Slack API: `users.channelSections.create`. The call always carries an
`emoji` param (empty when the flag is absent); without the key Slack answers
`invalid_arguments`.

#### slack section rename

```
slack section rename <section-id> <new-name>
```

Renames a section in place. The ID is unchanged, so anything keyed on it
(manifests, `section move --section`) keeps working. The current emoji is
preserved. A blank name is `invalid_input` and makes no API call. An unknown
ID is `section_not_found`. A name another section already uses is Slack's
`already_exists`. The write endpoint returns `ok:true` whether or not it
changed anything, so the command re-fetches and emits the section as Slack
now holds it; if the name or emoji does not match the request the command
fails with `rename_failed` and emits no row.

```
$ slack section rename S02DEF "Customers (EXT)"
{"id":"S02DEF","name":"Customers (EXT)","emoji":"handshake","type":"channels","channel_count":15}
{"_meta":{"has_more":false}}
```

Slack API: `users.channelSections.set` (form-encoded `channel_section_id`,
`name`, `emoji`; any key omitted is cleared on the server), then
`users.channelSections.list`.

#### slack section emoji

```
slack section emoji <section-id> <emoji>
```

Sets a section's emoji in place, keeping its ID and name. An empty `<emoji>`
(`""`) clears it. An unknown ID is `section_not_found`. The write endpoint
returns `ok:true` whether or not it changed anything, so the command
re-fetches and emits the section as Slack now holds it; if the name or emoji
does not match the request the command fails with `emoji_failed` and emits
no row.

The wire format is the bare emoji name, exactly as `section list` emits it.
Verified live 2026-10-16 against `users.channelSections.create` and `.set`:
`heart`, `tada`, `+1`, a custom emoji name, `wave::skin-tone-2`, and
`wave::skin-tone-6` are accepted and stored verbatim (no alias
normalization); `` clears; `:heart:`, `:wave::skin-tone-2:`, `:`, `::`,
`::heart::`, `:heart`, `heart:`, `Heart`, `heart ` (trailing space),
`wave::skin-tone-1`, `heart::skin-tone-9`, a raw unicode emoji, and an
unknown name are all `emoji_invalid` and leave the section unchanged.

Because `:heart:` is how people type emoji, the CLI strips exactly one
leading and one trailing colon when both are present and at least one
character sits between them: `:heart:` becomes `heart`,
`:wave::skin-tone-2:` becomes `wave::skin-tone-2`. The strip runs once, so
`::heart::` is sent as `:heart:`. `:` and `::` are sent verbatim. Every one
of those verbatim forms is measured `emoji_invalid`, so a malformed value
fails loudly rather than silently clearing the emoji. Nothing else is
changed - case, whitespace, unknown names, and unicode go verbatim and
Slack's `emoji_invalid` passes through (exit 1). The same rule applies to `section create --emoji` and
`section move --emoji`. The stripped value is what the re-fetch is compared
against and what the row shows.

```
$ slack section emoji S02DEF :handshake:
{"id":"S02DEF","name":"Customers","emoji":"handshake","type":"channels","channel_count":15}
{"_meta":{"has_more":false}}
```

```
$ slack section emoji S02DEF ""
{"id":"S02DEF","name":"Customers","emoji":"","type":"channels","channel_count":15}
{"_meta":{"has_more":false}}
```

Slack API: `users.channelSections.list`, then `users.channelSections.set`
(form-encoded `channel_section_id`, the current `name`, and the new `emoji`;
any key omitted is cleared on the server), then `users.channelSections.list`
again. `rename` and `emoji` each change one field; a caller wanting both runs
both. Both share the read-modify-write window: a concurrent edit of the other
field between the list and the set is overwritten.

### drafts (Phase 3)

Stage unsent messages server-side. Drafts sync across Slack clients. The
user reviews and sends from the Slack app - the CLI never sends, per the
no-message-sending design constraint.

All draft subcommands require a session token (`xoxc-`). They hit the
undocumented `drafts.*` internal API, same auth pattern as `saved.list`
and `users.channelSections.*`.

#### slack draft list

```
slack draft list [flags]
  --active     Exclude scheduled drafts (active only).
  --limit      Page size. Default: 50. Max 100.
```

The `drafts.list` API does not support cursor pagination. Raise `--limit`
to fetch more per call; `has_more` in `_meta` indicates truncation.

```
$ slack draft list
{"id":"Dr01234","client_msg_id":"3b2f...","destinations":[{"channel_id":"C01ABC"}],"blocks":[{"type":"rich_text",...}],"date_created":1713300000,"date_created_iso":"2024-04-17T00:00:00Z","date_scheduled":0,"last_updated_ts":"1713300000.1234567"}
{"_meta":{"has_more":false}}
```

Sent and deleted drafts are suppressed by default. Use `--include-sent`
and `--include-deleted` to see them (Slack keeps tombstones in the list
after drafts are sent or when the desktop client marks them deleted).

Slack API: `drafts.list`

#### slack draft create

```
slack draft create <recipient>... [flags]
  --thread            Thread timestamp to reply to (channel destination only).
  --broadcast         When set with --thread, also post to channel.
  --at                Schedule send at RFC 3339 timestamp or YYYY-MM-DD.
  --date-scheduled    Schedule send at Unix epoch (alternative to --at).
  --table             Build a table from a CSV/TSV file and attach it.
  --no-header         With --table, treat the first row as data, not a bold header.
  --markdown          stdin is Markdown, converted to header, divider, and rich_text blocks.
  --print-payload     With --markdown: print the converted payload, create nothing.

Body on stdin. Block Kit JSON by default: either a bare array of top-level
blocks, or an object {"blocks":[...],"attachments":[...]}. Top-level blocks
must be rich_text, header, or divider; tables go in attachments[].blocks[] (or use --table).
With --markdown, stdin is Markdown (subset below). No plain-text shortcut.
```

##### Markdown input

`--markdown` converts stdin through `internal/mdblocks` into a `header`
block per heading, a `divider` block per thematic break, and exactly one
top-level `rich_text` block per run of content between them (Desktop
flattens adjacent `rich_text` blocks, so one per run is the only stable
shape; a document with neither is one block) and then runs the result through the same `validateBlockShapes` gate
as caller-supplied JSON. Blank stdin with
`--markdown` is "nothing provided" (so `--table` alone still makes an
attachments-only draft); non-blank stdin that converts to nothing is
`invalid_markdown`. The parser is goldmark (CommonMark) with the GFM
strikethrough, linkify, and table extensions, plus inline parsers for the
Slack tokens. Every conversion is deterministic.

| Markdown | rich_text |
|---|---|
| paragraph | `rich_text_section`; soft and hard line breaks both become `\n`; terminated with `\n` when any container follows |
| `---`, `***`, `___` (any CommonMark thematic break) | top-level `{"type":"divider"}` block, the composer's horizontal rule. Leading, trailing, and consecutive breaks each emit one. `Title` directly followed by `---` is a Setext heading, not a divider; a blank line between them makes it paragraph then divider |
| `# heading`, `## heading`, `### heading`, Setext | top-level `header` block, `level` 1, 2, 3, `text` `{"type":"plain_text","text":…,"emoji":true}`; `####` and deeper clamp to 3. Text is plain: bold, italic, strike, and code flatten to their text, `:name:` stays as text, a line break becomes a space. A link (including a bare URL or email), user or channel mention, or broadcast in a heading is `invalid_markdown` |
| `- ` / `* ` list | `rich_text_list` `style:bullet` |
| `N. ` list | `rich_text_list` `style:ordered`, `offset: N-1`; a later segment of the same list continues the count |
| nested list (indent) | a separate `rich_text_list` with `indent` 1 or 2, in source order; the parent list splits around it |
| fenced or indented code | `rich_text_preformatted`; fence language tag dropped |
| `> ` quote | `rich_text_quote`; consecutive paragraphs join with `\n\n`. A list or code block inside the quote becomes a sibling `rich_text_list` / `rich_text_preformatted` with `border: 1`, splitting the quote text around it. Slack has no list-in-quote nesting; the bordered list is the composer's own shape (read back from a Desktop-authored draft 2026-11-02), the bordered code block is API-accepted and renders under the quote bar but composer emission was not checked. The compose box rendered a bordered ordered list's `offset` as `1.` (2026-11-03), so a quoted ordered list may lose its start number |
| `**x**` `*x*` `~~x~~` `` `x` `` | text `style` bold / italic / strike / code, merged across nesting |
| `[t](url)`, bare URL, `<url>`, email | `link` with `url`, `text` when it differs, inherited bold/italic/strike; title and formatting inside the label dropped |
| `:name:`, `:name::skin-tone-N:` | `emoji` |
| `<@Uxxx>` / `<@Wxxx>` | `user` |
| `<#Cxxx>` / `<#Cxxx\|label>` | `channel`; label dropped |
| `@here` `@channel` `@everyone` | `broadcast` |
| `\*` `\:` `\@` `\<`, code spans | literal text |

Every `rich_text_section` with a successor gets its trailing text extended
with `\n` (or a separate `\n` text inline when the last inline is styled, a
link, emoji, user, channel, or broadcast). Before a list, quote, or code
block that is the absorption rule; before another section it keeps the
Markdown paragraph break from collapsing, since Slack's own composer
encodes paragraph structure as `\n` inside text rather than as section
boundaries; before a `header` it is kept for uniformity, since a header
is an absorption boundary and renders the same with or without it
(inspected 2026-11-06), and before a `divider` likewise (Desktop rendering
of that case is pending a check). Whether it renders as a blank line or one line
break before another section in the compose box is pending a Desktop
check. The converter does this itself; `validateBlockShapes` stays as the
backstop, so `invalid_blocks` after a successful conversion means a
converter defect.

Rejected with `invalid_markdown` (exit 1), detail prefixed `line N:`:

- `@name` and `<@name>` where the name is not a `U`/`W` id - "mention people as `<@Uxxx>`". Symbolic resolution is deferred until name collisions resolve deterministically (see `todo/at-name-resolution-picks-a-random-user-when-names-collide.md`).
- Image, HTML block, inline HTML, pipe table (points at `--table`).
- A list nested deeper than indent 2, a list item holding more than one paragraph, a quote inside a quote, a quote holding anything but paragraphs, lists, and code blocks.
- A mention, channel, emoji, or image inside link text (Slack `link` elements hold text only).
- An empty `-` list item, an item whose only content is a nested list, an empty `>` quote, or an empty code block: `drafts.create` rejects an empty text inline with `invalid_message` (verified live 2026-10-16), so these can be neither kept nor dropped. An empty heading likewise (a header with empty text is `invalid_message`, 2026-11-06).
- A link, bare URL, email, `<@Uxxx>`, `<#Cxxx>`, or `@here`/`@channel`/`@everyone` inside a heading: `header` text is `plain_text` and cannot carry them, and dropping a URL or mention is content loss.
- A heading or a horizontal rule inside a quote or a list item (header and divider blocks are top-level only).
- A link whose URL is not absolute `http`, `https`, or `mailto`.

Boundary rules: `:` is an emoji delimiter only when the character before the
opening colon and after the closing one is not a letter, digit, or `_`
(`10:30:45` is a time); `@` starts a mention only when the preceding
character is not a letter, digit, `_`, or `<` (`alice@example.com` is an
email, which linkify turns into a `mailto:` link).

`--print-payload` requires `--markdown` and rejects `--at`,
`--date-scheduled`, `--thread`, and `--broadcast` (on `update`: any schedule
flag) as `invalid_input`. It converts, applies `--table`, validates, prints
one row - `{"blocks":[...]}` plus `"attachments"` when present - and the
`_meta` trailer, and makes no API call: recipients and the draft id are
accepted syntactically but never resolved. On `update` it shows what this
invocation would submit, not the merged draft, so preserved attachments
are not shown.

Verified live 2026-10-16 (self-DM, session token, Enterprise Grid): a draft
holding every construct in the table round-tripped through `drafts.create`
and `drafts.list` with server blocks byte-identical to the payload sent, and
was not tombstoned after 25s. Header blocks were verified the same way on
2026-11-06 (`docs/header-blocks-plan.md`): a header-only draft and a
header / rich_text / header mix round-tripped modulo the server-assigned
`block_id` and were alive after 60s. Desktop compose box, inspected
2026-11-06: headings at levels 1, 2, 3 render at three sizes, `level` 4
renders as the small heading (the same as 3, so the `####` clamp matches
the client), `:name:` text inside a header renders as the emoji, a
paragraph with and without a trailing `\n` before a header looks
identical, and a header between a paragraph and a list breaks absorption.
Divider blocks were verified through the API the same day
(`docs/divider-blocks-plan.md`): a divider-only draft, a section without a
trailing newline followed by a divider and a list, and
`[divider, rich_text, divider, divider, header, divider]` all round-tripped modulo `block_id` and were alive after 100s;
Desktop rendering of dividers is pending a check.
Not inspected: rendering of italic and strike, of `offset` on an
unbordered ordered list (bordered: rendered `1.`, 2026-11-03), and of the
trailing `\n` between two paragraphs as a blank line versus a line break.

```
$ cat body.md | slack draft create '#general' --markdown
$ cat body.md | slack draft create '#general' --markdown --print-payload
{"blocks":[{"type":"header","level":1,"text":{"type":"plain_text","text":"Status","emoji":true}},{"type":"rich_text","elements":[...]}]}
{"_meta":{"has_more":false}}
```

Recipients are either one channel or one-or-more users:

- Channel: `#name`, a bare name, or `Cxxx`/`Dxxx`. Exactly one. A name
  resolves member-first via `users.conversations`, then `conversations.list`,
  on the workspace (T-prefix) credential - on Enterprise Grid both list
  endpoints are `enterprise_is_restricted` on the org context that
  `drafts.create` itself requires. On Grid, select the two credentials with
  `SLACK_WORKSPACE` (workspace) plus `SLACK_WORKSPACE_ORG` (org session) and
  leave `SLACK_TOKEN` unset - it overrides both and collapses the clients
  onto one token. When the workspace credential selection fails outright the
  name lookup errors `not_authed`; when selection lands on a credential whose
  token can't list channels, the lookup fails downstream instead. IDs and
  channel/message URLs resolve locally and need only the session credential.
- Users: `@name`, an email, or `Uxxx`/`Wxxx` id. One produces a 1:1 DM,
  several produce a multi-person DM (MPDM). The conversation need not exist
  yet - Slack opens it on send, so this is how you draft to people you've
  never messaged. Stamped as `user_ids` (no `channel_id`). Order preserved,
  duplicates removed. Slack caps MPDMs near 8 people; an oversized list is
  rejected by Slack. Name/email resolution runs on the session credential,
  preserving the org-level directory scope that has always applied on
  Enterprise Grid.

Bare names are treated as channels (backward compatible); use `@name` for a
person. Channel and user recipients can't be mixed. `--thread` / `--broadcast`
apply to a channel destination only. A user URL (profile link) is a user
recipient; a channel/message URL is a channel recipient.

Minimal plain-text draft:

```
$ echo '[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello"}]}]}]' \
  | slack draft create #general
```

Rich text (file with bullets, link, bold, code):

```
$ slack draft create #general < rich.json
```

New DM / multi-person DM (recipients need not exist as a conversation yet):

```
$ slack draft create @alice < blocks.json              # 1:1 DM
$ slack draft create @alice @bob U07XYZ < blocks.json  # MPDM
```

Thread reply:

```
$ slack draft create #general --thread 1713299000.123456 < blocks.json
```

Scheduled send:

```
$ slack draft create #general --at 2026-04-20T09:00:00-04:00 < blocks.json
```

Table (CSV/TSV file, or a `table` block in the stdin object's attachments):

```
$ slack draft create #general --table report.tsv
$ slack draft create #general < payload.json   # {"blocks":[...],"attachments":[{"blocks":[{"type":"table","rows":[...]}]}]}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `user_not_found` (exit 1): One or more user recipients couldn't be resolved (the message lists every unresolved input).
- `missing_blocks` (exit 1): Nothing piped on stdin and no `--table`.
- `invalid_markdown` (exit 1): With `--markdown`, the body uses a construct outside the subset above; the detail names the line.
- `invalid_blocks` (exit 1): stdin isn't a blocks array or `{blocks, attachments}` object, a top-level block isn't `rich_text`, `header`, or `divider`, a `header` lacks `plain_text` text or has empty text or a `level` that isn't an integer of at least 1, a caller-supplied attachment block isn't a `table` (a `data_table` is rejected - not draftable), or the draft has no renderable content (no non-empty `rich_text` body, no `header`, no `divider`, and no table attachment).
- `invalid_input` (exit 1): `--broadcast` without `--thread`, conflicting schedule flags, `--table` together with stdin attachments, `--print-payload` without `--markdown` or with a schedule/thread flag, mixing channel and user recipients, more than one channel recipient, or `--thread`/`--broadcast` with a user destination.
- `invalid_timestamp` (exit 1): Cannot parse `--at`.
- `not_authed` (exit 2): No session token, or - for a `#name`/bare-name
  channel recipient only - the selected workspace credential cannot be
  resolved.

Local validation enforces: stdin parses as a blocks array or a `{blocks, attachments}` object; every top-level block is `rich_text`, `header`, or `divider`; a `header` has `text` `{"type":"plain_text","text":<non-empty>}` and, when present, an integer `level` of at least 1 (no upper bound - the composer emits 1 through 3, and the API stores 4, which Desktop renders as the small heading; `emoji` and a server-assigned `block_id` pass through); a `divider` needs only its `type` (every other field, including a server-assigned `block_id`, passes through); caller-supplied attachment blocks are `table` (`data_table` is rejected - not draftable); and the draft has renderable content (a non-empty `rich_text` block, a `header`, a `divider`, or a table attachment - a header-only draft and a divider-only draft both survive reconciliation, measured 2026-11-06). A `header` is an absorption boundary: a `rich_text_section` without a trailing `\n` followed by a header and then a list is accepted, since Desktop renders it as paragraph, heading, bullet with no glue (inspected 2026-11-06). A `divider` is not yet treated as one (pending a Desktop check), so the section-before-container state carries across it and `[section without "\n", divider, list]` is rejected. The same section directly before a list, in one `rich_text` or across adjacent `rich_text` blocks, is still rejected. Slack's API accepts other top-level block types, but the Drafts compose editor strips them on open - so the CLI rejects them locally and steers tables into `attachments`, where they survive. Semantic errors inside blocks (required subfields, unknown inline types) defer to Slack's upstream response.

Slack API: `drafts.create`

#### slack draft update

```
slack draft update <draft_id> [flags]
  --at                Reschedule send at RFC 3339 timestamp.
  --date-scheduled    Reschedule send at Unix epoch (use --clear-schedule to zero).
  --clear-schedule    Remove scheduled send.
  --table             Replace attachments with a table built from a CSV/TSV file.
  --no-header         With --table, treat the first row as data, not a bold header.
  --markdown          stdin is Markdown; replaces blocks, preserves attachments.
  --print-payload     With --markdown: print what would be submitted, update nothing.

Optional body on stdin. Block Kit JSON: a bare blocks array, or an object
{"blocks":...,"attachments":...}. Each field is independent - a provided
field replaces, an absent one is preserved; {"attachments":[]} clears
attachments. With --markdown the body converts to a bare blocks array, so
it replaces blocks and preserves attachments through the same rule. Omit
stdin entirely for a schedule-only update.
```

`client_last_updated_ts` is fetched from `drafts.list` and padded to 7
decimal places before calling `drafts.update`. Callers never see it.

Auto-replace on tombstoned drafts: when Slack Desktop marks a draft
`is_deleted: true` (can happen after viewing a CLI-created draft in the
Desktop client), `drafts.update` and `drafts.delete` both reject the
draft. This CLI detects the tombstone and transparently creates a fresh
draft at the same destination, carrying its blocks and attachments. The
warning goes to stderr; the new draft object lands on stdout as usual.

```
$ slack draft update Dr01234 < blocks.json
{"id":"Dr01234","destinations":[{"channel_id":"C01ABC"}],"date_scheduled":0,"last_updated_ts":"1713300000.1234567"}
{"_meta":{"has_more":false}}
```

Reschedule without changing content:

```
$ slack draft update Dr01234 --at 2026-04-21T09:00:00-04:00
```

Errors:

- `draft_not_found` (exit 1): No draft with the given id.
- `invalid_input` (exit 1): No change specified, conflicting schedule flags, or `--table` together with stdin attachments.
- `invalid_blocks` (exit 1): stdin isn't a valid blocks array / `{blocks, attachments}` object, or the resulting draft has no renderable content.
- `invalid_markdown` (exit 1): With `--markdown`, the body uses a construct outside the subset (see `draft create`).
- `not_authed` (exit 2): No session token.

Slack API: `drafts.update` (or `drafts.create` on auto-replace)

#### slack draft delete

```
slack draft delete <draft_id>...
```

Accepts one or more draft ids. Deletes active or scheduled drafts.
`client_last_updated_ts` is auto-fetched and padded as with update.

```
$ slack draft delete Dr01234
{"id":"Dr01234","deleted":true}
{"_meta":{"has_more":false}}
```

Bulk:

```
$ slack draft delete Dr01234 Dr05678
{"id":"Dr01234","deleted":true}
{"id":"Dr05678","deleted":true}
{"_meta":{"has_more":false}}
```

Per-draft errors appear inline; `error_count` in `_meta` indicates partial failure.

Slack API: `drafts.delete`

## Caching

Channel and user resolution caches are stored under `slack-cli/cache/` inside the
user config directory (`os.UserConfigDir`), which is
`~/Library/Application Support` on macOS and `$XDG_CONFIG_HOME` or `~/.config` on
Linux. `slack cache info` prints the resolved paths:

- `channels-{teamID}.json` - channel name-to-ID mappings (24h TTL)
- `users-{teamID}.json` - user ID-to-profile mappings (24h TTL)

TTL is configurable via `SLACK_CACHE_TTL` env var (e.g., `1h`, `24h`,
`7d`). Default: `24h`.

User cache is populated on first miss by paginating `users.list --all`,
then point lookups hit the cache. Channel cache uses the existing
early-exit pagination pattern. Single-ID lookups during output enrichment
bypass both bulk loads and hit `users.info` / `conversations.info` instead,
so a cold cache never forces a full-workspace crawl just to name the handful
of IDs in a command's output.

## Configuration

All configuration is via flags and environment variables. No config file.

Precedence: flags > env vars > defaults.

Environment variables:

- `SLACK_WORKSPACE` - default workspace (same as `--workspace`)
- `SLACK_TOKEN` - bot token override (bypasses stored credentials)
- `SLACK_USER_TOKEN` - user token override (bypasses stored credentials)
- `SLACK_COOKIE` - `d` cookie for `xoxc-` token authentication

Credentials are stored in `slack-cli/credentials.json` inside the user config directory (see Caching above for how that resolves per platform), keyed by workspace `TeamID`. Managed by `slack auth login` / `slack auth logout`.

## Phasing

### Phase 1: Core

- `auth login` (OAuth), `auth login --desktop`, `auth status`, `auth logout`
- `channel list`, `channel info`, `channel members`
- `message list` / `message read`, `message get`
- `thread list` / `thread read`
- `user list`, `user info`
- `reaction list`
- JSONL output with `_meta` trailer
- `--fields` filtering
- Single-page pagination with `--cursor` and `--all`
- Rate limiting with retry
- Channel/user name resolution
- Cookie-based authentication for `xoxc-` tokens

### Phase 2a: MCP replacement (critical path)

- `search messages` - most-used MCP operation
- `message permalink` - replaces `slack-permalink` bash script
- User caching (24h TTL, bulk-load `users.list`)
- Channel cache TTL bump (1h → 24h)

### Phase 2b: Python skill replacement

- `saved list`, `saved counts` - replaces slack-saved-items skill
- `section list`, `section channels`, `section find`, `section move`, `section create`, `section rename`, `section emoji` - replaces slack-organize-channels skill

### Phase 2c: Extended read operations

- `search files`
- `file list`, `file info`, `file download`
- `pin list`
- `bookmark list`
- `status get`, `presence get`, `dnd info`
- `user lookup`
- `usergroup list`, `usergroup members`
- `emoji list`
- `workspace info`
- User display name resolution (`@name`)

### Phase 3: Advanced

- `draft create`, `draft list`, `draft update`, `draft delete` (internal `drafts.*` API)
- `channel mark` (mark as read)
- `auth switch` (multi-workspace)

## Dependencies

- **CLI framework**: [kong](https://github.com/alecthomas/kong) for command structure and flag parsing.
- **Slack API**: [slack-go/slack](https://github.com/slack-go/slack) as the API client.
- **LevelDB**: [goleveldb](https://github.com/syndtr/goleveldb) for reading Slack Desktop local storage.
- **SQLite**: [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go) for reading Slack Desktop cookies.
- **TLS fingerprinting**: [utls](https://github.com/refraction-networking/utls) for Chrome TLS fingerprint on API requests.
- **Output**: `encoding/json` from stdlib.
