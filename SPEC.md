# slack-cli Specification

A read-only, non-interactive CLI for Slack, built in Go. Designed for agent and automation use, with JSON as the default output format.

## Design Principles

- **Read-only**: This CLI reads from Slack. It does not post messages, modify channels, or change state. The one planned exception is marking messages as read (`channel mark`), which may be added in a future phase.
- **Agent-first**: Structured JSON output by default. No interactive prompts. Deterministic, scriptable behavior.
- **Resource-verb pattern**: `slack <resource> <verb> [flags]`, consistent with `gh`, `kubectl`, and `aws`.
- **Composable**: Works with pipes, `jq`, and shell scripts. Stdout for data, stderr for diagnostics.
- **Handles the boring parts**: Automatic cursor-based pagination, rate limit retry with backoff, token refresh.

## Authentication

### Token types

Two token types are relevant:

- **Bot tokens** (`xoxb-`): Cover most read operations. Tied to an app, not a user.
- **User tokens** (`xoxp-`): Required for `search` and reading your own DND/presence state. Tied to a specific user.

The CLI stores tokens in `~/.config/slack-cli/credentials.json`, keyed by workspace. Multiple workspaces are supported. A `--workspace` flag (or `SLACK_WORKSPACE` env var) selects which workspace to use when multiple are configured. If only one workspace exists, it's used automatically.

### Auth methods

#### OAuth (primary, implemented first)

`slack auth login` starts a local OAuth flow:

1. Starts a temporary local HTTP server on a random port.
2. Opens the Slack OAuth authorize URL in the user's browser.
3. Receives the callback with an authorization code.
4. Exchanges the code for bot + user tokens.
5. Stores both tokens in the credentials file.

The CLI ships with a bundled Slack App manifest. On first `slack auth login`, if no app exists, it guides the user to create one from the manifest (or, if possible, creates it via the App Manifest API).

#### Chrome cookie extraction (future)

For organizations that restrict OAuth app installation, `slack auth login --chrome` will extract session tokens from a Chrome browser running with `--remote-debugging-port`. This is a fallback for locked-down workspaces.

### Auth commands

```
slack auth login          # OAuth flow, stores tokens
slack auth login --chrome # (future) Extract from Chrome
slack auth logout         # Remove stored tokens for a workspace
slack auth status         # Show current auth state (workspace, user, token types, scopes)
slack auth switch         # Switch active workspace
```

### Environment variable overrides

`SLACK_TOKEN` and `SLACK_USER_TOKEN` override stored credentials when set. Useful in CI/CD and agent contexts where file-based config isn't practical.

## Global Flags

```
--workspace, -w   Select workspace (name or ID)
--format, -f      Output format: json (default), text
--quiet, -q       Suppress non-essential output (only errors on stderr)
--verbose, -v     Include extra diagnostic info on stderr
--no-pager        Disable automatic paging of long output
--limit           Max items to return for list commands (0 = all)
--raw             Output raw API responses without transformation
```

## Output Format

### JSON (default)

All commands output a JSON object to stdout. The structure depends on the command:

- **Single item commands** (info, get): The item object directly.
- **List commands**: An array of item objects.

Fields use `snake_case`, matching the Slack API. Timestamps are Unix epoch floats (Slack's native format) with an additional `_iso` suffixed field for human readability.

```json
{"ts": "1234567890.123456", "ts_iso": "2024-01-15T10:30:00Z"}
```

### Text format (`--format=text`)

Human-readable columnar output. Designed for quick scanning, not parsing.

### Errors

Errors go to stderr as JSON:

```json
{"error": "channel_not_found", "detail": "No channel matching '#nonexistent'", "hint": "Run 'slack channel list' to see available channels"}
```

Exit codes:

- `0`: Success
- `1`: General error (API error, invalid input)
- `2`: Authentication error (no token, expired, insufficient scopes)
- `3`: Rate limited (after exhausting retries)
- `4`: Network error

## Channel resolution

Anywhere a channel is required, the CLI accepts:

- Channel ID (`C01234ABCDE`)
- Channel name with `#` prefix (`#general`)
- Channel name without `#` (`general`)

Names are resolved to IDs via `conversations.list` with client-side caching (cache TTL: 5 minutes). If ambiguous (e.g., multiple channels named `general` across public/private), the CLI errors with the matches listed.

## User resolution

Anywhere a user is required, the CLI accepts:

- User ID (`U01234ABCDE`)
- Display name with `@` prefix (`@tammer`)
- Email address (`tammer@example.com`)

Same caching and ambiguity behavior as channels.

## Commands

### channel

```
slack channel list [flags]
  --type          public, private, mpim, im, all (default: public)
  --exclude-archived  Exclude archived channels (default: true)
  --member        Only channels the authenticated user is a member of

slack channel info <channel>

slack channel members <channel>
```

### message

```
slack message list <channel> [flags]
  --limit         Number of messages (default: 20)
  --before        Messages before this timestamp
  --after         Messages after this timestamp
  --oldest        Oldest timestamp to include
  --latest        Latest timestamp to include
  --inclusive     Include messages at oldest/latest boundary

  Aliased as `slack message read`.

slack message get <channel> <timestamp>
  Retrieve a single message by its timestamp.

slack message permalink <channel> <timestamp>
  Get a permalink URL for a message.
```

### thread

```
slack thread list <channel> <timestamp> [flags]
  --limit         Number of replies (default: 50)

  Aliased as `slack thread read`.
```

### user

```
slack user list [flags]
  --presence      Include presence information (slower)

slack user info <user>

slack user lookup <email>
  Find a user by email address.
```

### reaction

```
slack reaction list <channel> <timestamp>
  List all reactions on a message.
```

### search

Requires a user token (`xoxp-`). Will error with a clear message if only a bot token is available.

```
slack search messages <query> [flags]
  --sort          timestamp, score (default: timestamp)
  --sort-dir      asc, desc (default: desc)
  --limit         Max results (default: 20)

slack search files <query> [flags]
  Same flags as search messages.
```

### file

```
slack file list [flags]
  --channel       Filter by channel
  --user          Filter by user
  --type          Filter by file type
  --limit         Max results (default: 20)

slack file info <file-id>

slack file download <file-id> [output-path]
  Downloads file content. Defaults to original filename in current directory.
```

### pin

```
slack pin list <channel>
```

### bookmark

```
slack bookmark list <channel>
```

### status

```
slack status get [user]
  Returns status emoji and text. Defaults to authenticated user.

slack presence get [user]

slack dnd info [user]
```

### usergroup

```
slack usergroup list [flags]
  --include-disabled   Include disabled usergroups

slack usergroup members <usergroup>
```

### emoji

```
slack emoji list
```

### workspace

```
slack workspace info
  Current workspace metadata (name, domain, plan, etc.)
```

## Pagination

All list commands handle pagination automatically. By default, they return all results. Use `--limit` to cap the number of items returned.

Internally, the CLI uses cursor-based pagination with a page size of 200. It follows `response_metadata.next_cursor` until exhausted or `--limit` is reached.

For very large result sets, `--limit` should be used to avoid excessive API calls.

## Rate Limiting

The CLI automatically retries on HTTP 429 responses. It reads the `Retry-After` header and waits accordingly, with jitter. After 5 consecutive rate limit hits on the same request, it exits with code 3.

Rate limit retries are logged to stderr when `--verbose` is set.

## Configuration

`~/.config/slack-cli/config.toml`:

```toml
[defaults]
workspace = "mycompany"
format = "json"
limit = 50

[workspaces.mycompany]
# Populated by `slack auth login`
```

Config values are overridden by environment variables, which are overridden by flags.

Precedence: flags > env vars > config file > defaults.

## Phasing

### Phase 1: Core

- `auth login` (OAuth), `auth status`, `auth logout`
- `channel list`, `channel info`, `channel members`
- `message list` / `message read`, `message get`
- `thread list` / `thread read`
- `user list`, `user info`
- `reaction list`
- JSON and text output formats
- Pagination and rate limiting
- Channel/user name resolution

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

### Phase 3: Advanced

- `auth login --chrome` (cookie extraction)
- `channel mark` (mark as read - the one planned write operation)
- `auth switch` (multi-workspace)

## Dependencies

- **CLI framework**: [kong](https://github.com/alecthomas/kong) for command structure and flag parsing.
- **Slack API**: [slack-go/slack](https://github.com/slack-go/slack) as the API client.
- **Output**: `encoding/json` from stdlib. A thin table formatter for text mode.
