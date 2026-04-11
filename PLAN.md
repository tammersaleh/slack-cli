# Implementation Plan: MCP Replacement

Goal: close all gaps needed to decommission the korotovsky Slack MCP
server and the two Python skills (saved-items, organize-channels).

## Phase 2a: Critical path (MCP replacement)

### 1. search messages

The most-used MCP operation. `update-world` agents call
`conversations_search_messages` dozens of times per run with patterns
like `in:#channel-name` and `from:@user` plus `filter_date_after`.

Already spec'd in SPEC.md. Requires user token (`xoxp-`) or session
token (`xoxc-`). The `search.messages` Slack API supports cursor-based
pagination and Slack search modifiers in the query string.

Branch: `feat/search-messages`

### 2. message permalink

Simple feature, already spec'd. Generates permalinks via
`chat.getPermalink`. Replaces the `slack-permalink` bash script in
`~/brain/cw/scripts/`.

Branch: `feat/message-permalink`

### 3. User caching

The MCP caches a 17MB `users_cache.json` with 24h TTL. Without user
caching, every user lookup hits the API. The `update-world` agents
resolve many user IDs per run.

Extend the existing channel resolver caching pattern:
- File cache at `~/.config/slack-cli/cache/users-{teamID}.json`
- 24h TTL (configurable via `SLACK_CACHE_TTL` env var)
- In-memory cache for session reuse
- Bulk-load on first miss (paginate `users.list --all`), then point
  lookups hit cache

Branch: `feat/user-cache`

### 4. Channel cache TTL bump

Current channel file cache TTL is 1h. MCP uses 24h. Bump to 24h
(same env var `SLACK_CACHE_TTL` controls both).

Can bundle with user caching work.

## Phase 2b: Python skill replacement

### 5. saved list

Replaces `~/dotfiles/public/.claude/skills/slack-saved-items/`.

New command: `slack saved list`

Internal API: `saved.list` (POST, xoxc token required)
- Request: `{token, count, cursor, include_completed}`
- Response: `{saved_items[], counts{}, response_metadata{next_cursor}}`

Subcommands:
- `slack saved list [--limit N] [--enrich] [--include-completed]`
- `slack saved counts`

The `--enrich` flag fetches message text and channel names concurrently
(same pattern as the Python script: `conversations.history` +
`conversations.info` per item, semaphore-limited concurrency).

Permalink generation: `https://TEAM.slack.com/archives/{channel}/p{ts_nodot}`

Branch: `feat/saved-items`

### 6. section list/find/move/create

Replaces `~/dotfiles/private/.claude/skills/slack-organize-channels/`.

New commands:
- `slack section list` - list sidebar sections
- `slack section channels --section ID` - channels in a section
- `slack section find --pattern STR` - find channels by name substring
- `slack section move --channels IDs (--section ID | --new-section NAME)` - move channels
- `slack section create --name STR` - create a section

Internal APIs (all POST, xoxc token required):
- `client.counts` - get all channel IDs (Enterprise Grid safe, JSON body)
- `users.channelSections.list` - sidebar sections with membership
- `users.channelSections.create` - create section
- `users.channelSections.channels.bulkUpdate` - move channels (batch 50, 1s delay)
- `conversations.info` - resolve channel names (concurrent, 15 semaphore)

The Python script caches channel prefixes at
`~/.cache/tars/channel-prefixes.json`. The CLI can use the same channel
cache it already maintains.

Branch: `feat/sidebar-sections`

## Phase 2c: Nice to have

### 7. message post

`slack message post --channel ID --text "..."` via `chat.postMessage`.
Currently unused by any automation but closes full feature parity.

Branch: `feat/message-post`

### 8. search files

Already spec'd in SPEC.md. Lower priority than message search.

Branch: `feat/search-files`

### Remaining Phase 2 items from SPEC.md

These are spec'd but lower priority for the MCP replacement goal:
- `file list/info/download`
- `pin list`
- `bookmark list`
- `status get`, `presence get`, `dnd info`
- `user lookup`
- `usergroup list/members`
- `emoji list`
- `workspace info`

## Done

- [x] Phase 1: Core read operations (all complete and merged)
- [x] QA pass: channel resolver early exit, rate limit endpoint field
- [x] search messages (#32)
- [x] message permalink (#33)
- [x] User caching + channel cache TTL bump to 24h (#34)
- [x] Fix all errcheck lint violations (CI was failing since chrome-auth)
- [x] saved list + saved counts (#35)
- [x] section list/channels/find/move/create (#36)
- [x] message post (#37)
- [x] search files (#38)
- [x] file list/info/download (#39)
- [x] pin list + bookmark list (#40)
