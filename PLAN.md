# Phase 1 Implementation Plan

## Context

Scaffolding exists: auth flow, api.Client, api.Paginate, api.ClassifyError, resolve.Resolver. But the output layer uses the old model (indented JSON arrays). All data commands are stubs.

The spec requires JSONL output with `_meta` trailers, `--fields` filtering, per-command pagination flags (`--limit`, `--cursor`, `--all`), and timestamp enrichment on JSONL lines.

## Order of Work

Each step is a branch + PR.

### Step 1: Output layer rewrite

Rewrite `internal/output` to emit JSONL:

- `Printer.PrintItem(v any)` - emit one JSON line (compact, not indented), with timestamp enrichment and `--fields` filtering
- `Printer.PrintMeta(meta Meta)` - emit `{"_meta":{...}}` trailer
- `Printer.PrintError(e *Error)` - JSON to stderr (unchanged)
- Remove `PrintRaw`, `Print` (old indented JSON methods)
- `Meta` struct: `HasMore bool`, `NextCursor string`, `ErrorCount int`
- `--fields` filtering: top-level field whitelist applied before marshaling
- Keep timestamp enrichment logic, adapt for compact single-line output
- Update all tests

### Step 2: Fix global flags + main entrypoint

- Remove `Format`, `Raw`, `NoPager`, global `Limit` from CLI struct
- Add `Fields []string` global flag
- Update `main.go` error handling to use `output.Error` + proper exit codes
- Update `cmd/root_test.go`

### Step 3: Update auth commands

- `auth login` → JSONL output with `_meta`
- `auth logout` → JSONL output with `_meta`
- `auth status` → JSONL output with `_meta`
- Match spec field names exactly
- Add helper to create api.Client from CLI flags (token resolution)

### Step 4: channel list

- Pagination flags: `--limit` (default 100), `--cursor`, `--all`
- Filters: `--type`, `--exclude-archived` (default true), `--include-non-member`, `--has-unread`, `--query`
- Default: member-only channels
- Client-side filters: `--query`, `--has-unread`
- Single page by default, stream with `--all`
- Tests with mock Slack API

### Step 5: channel info

- Multiple channel args
- `input` field on each result
- Inline errors for not-found channels
- `error_count` in `_meta`
- Channel name resolution via Resolver

### Step 6: channel members

- Single channel arg + pagination flags
- Returns `{"id":"U..."}` per member
- Channel name resolution

### Step 7: message list (aliased as message read)

- Channel arg + pagination flags
- `--after`, `--before` (Unix ts or ISO 8601)
- `--unread` (sets oldest to last_read, mutually exclusive with --after)
- `--has-replies`, `--has-reactions` (client-side filters)
- Timestamp enrichment on ts, thread_ts, latest_reply fields

### Step 8: message get

- Channel arg + multiple timestamp args
- `input` field, inline errors for not-found
- Uses conversations.history with oldest=ts&latest=ts&inclusive=true&limit=1

### Step 9: thread list (aliased as thread read)

- Channel + timestamp args + pagination flags
- Parent message included as first result
- Uses conversations.replies

### Step 10: user list

- Pagination flags
- `--query` (client-side name/email filter)
- `--presence` flag

### Step 11: user info

- Multiple user args (ID or email)
- `input` field, inline errors
- Uses users.info (by ID) or users.lookupByEmail (by email)

### Step 12: reaction list

- Channel + multiple timestamp args
- Each reaction is a separate JSONL line with `input` field
- Uses reactions.get
