# slack-cli Specification

A read-only, non-interactive CLI for Slack, built in Go. Designed for agents and automation.

## Design Principles

- **Read-only**: No posting, modifying channels, or changing state. The one planned exception is `channel mark` (Phase 3).
- **Agent-first**: JSONL output. No interactive prompts. Deterministic, scriptable.
- **Thin wrapper**: One API page per call by default. No hidden pagination loops. The caller controls data volume.
- **Resource-verb pattern**: `slack <resource> <verb> [flags]`, consistent with `gh`, `kubectl`, `aws`.
- **Composable**: Pipes, `jq`, shell scripts. Stdout for data, stderr for diagnostics.

## Output

All commands produce JSONL (newline-delimited JSON) on stdout. Each line is a self-contained JSON object. Fields use `snake_case`, matching the Slack API.

### List commands

One JSON object per item, followed by a `_meta` trailer:

```
$ slack channel list --limit=2
{"id":"C01ABC","name":"general","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Company-wide announcements","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"General discussion","creator":"U01XYZ","last_set":1609459200},"num_members":142,"is_member":true,"unread_count":5,"last_read":"1709251200.000100"}
{"id":"C02DEF","name":"random","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Water cooler","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"Non-work chatter","creator":"U01XYZ","last_set":1609459200},"num_members":89,"is_member":true,"unread_count":0,"last_read":"1709251300.000200"}
{"_meta":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}
```

The `_meta` line is always present, even when there are no results:

```
$ slack channel list --query=nonexistent
{"_meta":{"has_more":false}}
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
$ slack channel list --limit=2 --fields=id,name,unread_count
{"id":"C01ABC","name":"general","unread_count":5}
{"id":"C02DEF","name":"random","unread_count":0}
{"_meta":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}
```

Nested objects are returned whole when their parent field is selected (`--fields=topic` returns the full topic object). No dot-notation for nested field access - use `jq` for that.

### Errors

Errors that prevent command execution entirely (auth failure, network error) go to stderr as JSON:

```json
{"error":"not_authed","detail":"No token found","hint":"Run 'slack auth login' or set SLACK_TOKEN"}
```

Per-item errors in multi-argument info commands appear inline on stdout (see above).

Exit codes:

- `0`: All items succeeded
- `1`: General error (API error, invalid input, partial failure in multi-item commands)
- `2`: Authentication error (no token, expired, insufficient scopes)
- `3`: Rate limited (after exhausting retries)
- `4`: Network error

### Verbose output

With `--verbose`, diagnostics go to stderr as JSON:

```json
{"level":"debug","msg":"rate limited, retrying in 3s","attempt":2,"endpoint":"conversations.list"}
{"level":"debug","msg":"page fetched","items":200,"has_more":true,"endpoint":"conversations.list"}
```

## Global Flags

```
--workspace, -w   Select workspace (name or ID). Env: SLACK_WORKSPACE
--fields          Comma-separated list of top-level fields to include
--quiet, -q       Suppress stdout output (exit code and stderr only)
--verbose, -v     Log diagnostics to stderr as JSON
```

## Authentication

### Token types

- **Bot tokens** (`xoxb-`): Most read operations. Tied to an app.
- **User tokens** (`xoxp-`): Required for `search`. Tied to a user.
- **Session tokens** (`xoxc-`): Extracted from Chrome browser sessions. Require a `d` cookie on every API request. Function as user tokens.

Tokens stored in `~/.config/slack-cli/credentials.json`, keyed by workspace `TeamID`. Each entry includes an `auth_method` field (`"oauth"` or `"chrome"`) used for context-specific error hints. `--workspace` selects the workspace when multiple exist. If only one workspace exists, it's used automatically.

### OAuth

`slack auth login` runs a local OAuth flow:

1. Starts a temporary HTTP server on a random port.
2. Opens Slack OAuth URL in the browser.
3. Receives callback with authorization code.
4. Exchanges code for bot + user tokens.
5. Stores both in credentials file with `auth_method: "oauth"`.

Requires `SLACK_CLIENT_ID` and `SLACK_CLIENT_SECRET` environment variables. The user creates a Slack app manually - setup instructions are in the README.

### Chrome cookie extraction

`slack auth login --chrome` extracts session tokens from Chrome via the Chrome DevTools Protocol. Fallback for workspaces that restrict OAuth app installation.

1. Launches Chrome with `--remote-debugging-port`, using a dedicated profile at `~/.config/slack-cli/chrome-profile/`.
2. Navigates to `https://app.slack.com`.
3. Polls every 2s for the `d` cookie and `xoxc-` tokens in `localStorage.localConfig_v2`.
4. Extracts all workspaces found in `localConfig_v2.teams`.
5. Validates each via `auth.test` with cookie-based authentication.
6. Stores all valid workspaces with `auth_method: "chrome"`.
7. Closes Chrome.

With `--chrome-port=PORT`, connects to an existing Chrome debug instance instead of launching a new one.

Session tokens (`xoxc-`) expire. When they do, the CLI detects the failure and hints the user to re-run `slack auth login --chrome`.

### Environment variable overrides

`SLACK_TOKEN`, `SLACK_USER_TOKEN`, and `SLACK_COOKIE` override stored credentials when set. `SLACK_COOKIE` provides the `d` cookie value for `xoxc-` token authentication. For CI/CD and agent contexts.

### auth commands

#### slack auth login

```
slack auth login [flags]
  --client-id       Slack app client ID. Env: SLACK_CLIENT_ID
  --client-secret   Slack app client secret. Env: SLACK_CLIENT_SECRET
  --chrome          Authenticate via Chrome cookie extraction
  --chrome-port     Connect to existing Chrome debug instance on this port
```

Without `--chrome`, opens browser for OAuth flow. On success:

```
$ slack auth login
{"team_id":"T01ABC","team_name":"Acme Corp","user_id":"U01XYZ","auth_method":"oauth","has_bot_token":true,"has_user_token":true}
{"_meta":{"has_more":false}}
```

With `--chrome`, extracts credentials from Chrome. Emits one line per workspace:

```
$ slack auth login --chrome
{"team_id":"T01ABC","team_name":"Acme Corp","user_id":"U01XYZ","auth_method":"chrome","has_bot_token":true,"has_user_token":false}
{"team_id":"T02DEF","team_name":"Other Corp","user_id":"U02ABC","auth_method":"chrome","has_bot_token":true,"has_user_token":false}
{"_meta":{"has_more":false}}
```

Errors:

- `missing_client_credentials` (exit 1): `SLACK_CLIENT_ID` or `SLACK_CLIENT_SECRET` not set (OAuth mode only).
- `oauth_failed` (exit 1): OAuth flow failed (user denied, code exchange error).
- `chrome_auth_failed` (exit 1): Chrome extraction failed (not signed in, Chrome not running, timeout).

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

Names are resolved via `conversations.list` with in-memory caching (5-minute TTL). On name collision, first match wins.

### Users

Anywhere a user is required, the CLI accepts:

- User ID: `U01234ABCDE`
- Email: `tammer@example.com`

Display name resolution (`@tammer`) deferred to Phase 2 (#29) - requires fetching and caching all user profiles, which is expensive at scale.

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

Pagination flags on list commands:

```
--limit    Page size (per-command default, max varies by Slack endpoint)
--cursor   Continue from a previous page's next_cursor
--all      Fetch all pages, streaming results
```

Some endpoints are not paginated by Slack (usergroup list, emoji list). These return all results in a single call regardless of flags.

## Rate Limiting

The CLI retries on HTTP 429 responses. It reads the `Retry-After` header and waits accordingly, with jitter. After 5 consecutive rate limit hits on the same request, it exits with code 3 and an error to stderr:

```json
{"error":"rate_limited","detail":"Rate limited after 5 retries","endpoint":"conversations.history"}
```

Retries are logged to stderr when `--verbose` is set.

## Commands

### channel

#### slack channel list

```
slack channel list [flags]
  --limit              Page size (default: 100, max: 200)
  --cursor             Continue from previous page
  --all                Fetch all pages
  --type               public, private, mpim, im, all (default: public)
  --exclude-archived   Exclude archived channels (default: true)
  --include-non-member Include channels the user hasn't joined
  --has-unread         Only channels with unread messages
  --query              Filter by name substring (client-side)
```

Default behavior returns only channels the authenticated user is a member of.

```
$ slack channel list --limit=2
{"id":"C01ABC","name":"general","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Company-wide announcements","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"General discussion","creator":"U01XYZ","last_set":1609459200},"num_members":142,"is_member":true,"unread_count":5,"last_read":"1709251200.000100"}
{"id":"C02DEF","name":"random","is_channel":true,"is_private":false,"is_archived":false,"created":1609459200,"creator":"U01XYZ","topic":{"value":"Water cooler","creator":"U01XYZ","last_set":1609459200},"purpose":{"value":"Non-work chatter","creator":"U01XYZ","last_set":1609459200},"num_members":89,"is_member":true,"unread_count":0,"last_read":"1709251300.000200"}
{"_meta":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}
```

```
$ slack channel list --has-unread --fields=id,name,unread_count
{"id":"C01ABC","name":"general","unread_count":5}
{"id":"C03GHI","name":"engineering","unread_count":12}
{"_meta":{"has_more":false}}
```

`--query` and `--has-unread` are client-side filters applied after the API page is fetched. The returned page may contain fewer items than `--limit`.

Errors:

- `not_authed` (exit 2): No token.

Slack API: `conversations.list`

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
{"id":"U01XYZ"}
{"id":"U02ABC"}
{"id":"U03DEF"}
{"_meta":{"has_more":true,"next_cursor":"dGVhbTpD"}}
```

Errors:

- `channel_not_found` (exit 1): No channel matching the input.
- `not_authed` (exit 2): No token.

Slack API: `conversations.members`

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

Retrieves specific messages by timestamp. Uses `conversations.history` with `oldest=ts&latest=ts&inclusive=true&limit=1`.

```
slack message get <channel> <timestamp>...
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

Slack API: `conversations.history` (filtered)

#### slack message permalink (Phase 2)

```
slack message permalink <channel> <timestamp>...
```

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
  --limit    Page size (default: 50, max: 200)
  --cursor   Continue from previous page
  --all      Fetch all pages
```

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
{"_meta":{"has_more":false}}
```

Errors:

- `not_authed` (exit 2): No token.

Slack API: `users.list`

#### slack user info

```
slack user info <user>...
```

```
$ slack user info tammer@example.com
{"input":"tammer@example.com","id":"U01XYZ","team_id":"T01ABC","name":"tammer","real_name":"Tammer Saleh","deleted":false,"is_bot":false,"is_admin":true,"updated":1709251200,"profile":{"email":"tammer@example.com","display_name":"tammer","real_name":"Tammer Saleh","status_text":"","status_emoji":"","image_48":"https://avatars.slack-edge.com/U01XYZ_48.jpg"}}
{"_meta":{"has_more":false}}
```

Errors:

- `user_not_found` (exit 1): No user matching the input.
- `not_authed` (exit 2): No token.

Slack API: `users.info` (by ID), `users.lookupByEmail` (by email)

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

Returns individual reactions, not the full message. Each reaction is a separate JSONL line.

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

Supports Slack search modifiers in the query string: `in:#channel`, `from:@user`, `has:link`, `has:reaction`, `before:2024-03-01`, `after:2024-02-01`, etc.

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

```
slack file info <file-id>...
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

File content is written to disk. JSONL metadata about the download goes to stdout.

```
slack file download <file-id> [flags]
  --output     Output path (default: original filename in current directory, "-" for stdout)
```

```
$ slack file download F01ABC
{"input":"F01ABC","id":"F01ABC","name":"Q1-report.pdf","size":1048576,"path":"./Q1-report.pdf"}
{"_meta":{"has_more":false}}
```

With `--output=-`, raw file bytes go to stdout and JSONL metadata is suppressed.

Errors:

- `file_not_found` (exit 1): No file matching the ID.
- `not_authed` (exit 2): No token.

Slack API: `files.info` (to get download URL), then HTTP GET

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
{"id":"U01XYZ"}
{"id":"U02ABC"}
{"id":"U03DEF"}
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

## Configuration

All configuration is via flags and environment variables. No config file.

Precedence: flags > env vars > defaults.

Environment variables:

- `SLACK_WORKSPACE` - default workspace (same as `--workspace`)
- `SLACK_TOKEN` - bot token override (bypasses stored credentials)
- `SLACK_USER_TOKEN` - user token override (bypasses stored credentials)
- `SLACK_COOKIE` - `d` cookie for `xoxc-` token authentication

Credentials are stored in `~/.config/slack-cli/credentials.json`, keyed by workspace `TeamID`. Managed by `slack auth login` / `slack auth logout`.

## Phasing

### Phase 1: Core

- `auth login` (OAuth), `auth login --chrome`, `auth status`, `auth logout`
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

### Phase 2: Extended

- `search messages`, `search files`
- `file list`, `file info`, `file download`
- `pin list`
- `bookmark list`
- `status get`, `presence get`, `dnd info`
- `message permalink`
- `user lookup`
- `usergroup list`, `usergroup members`
- `emoji list`
- `workspace info`
- User display name resolution (`@name`)

### Phase 3: Advanced

- `channel mark` (mark as read - the one planned write operation)
- `auth switch` (multi-workspace)

## Dependencies

- **CLI framework**: [kong](https://github.com/alecthomas/kong) for command structure and flag parsing.
- **Slack API**: [slack-go/slack](https://github.com/slack-go/slack) as the API client.
- **Chrome DevTools Protocol**: [chromedp](https://github.com/chromedp/chromedp) for Chrome cookie extraction.
- **Output**: `encoding/json` from stdlib.
