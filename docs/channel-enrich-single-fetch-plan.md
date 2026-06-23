# Channel enrichment single-fetch fallback

## Problem

`slack thread list <url>` hangs ~13 min on a cold cache against a large
Enterprise Grid workspace. Root cause (evidence-based, not the user cache):

- warm channel cache + cleared user cache -> 0.86s
- cleared channel cache + warm everything -> hits a 30s timeout, never finishes

Output enrichment turns IDs into names per JSONL row. For channel IDs,
`Enrich` calls `ensureChannelCache`, which bulk-loads the entire workspace via
`conversations.list` (Tier-2, rate-limited) just to name one `channel_id`.

The user side already avoids this: `LookupUser` falls back to a single
`users.info` on a cache miss. The channel side has no equivalent -
`LookupChannelName` only reads the in-memory map.

## Fix

Mirror the user side. Cost model goes from O(workspace size) to
O(unique unresolved channel IDs in output).

1. `internal/resolve/resolver.go`: add `failedChannels map[string]struct{}` to
   `Resolver` (per-session negative memo).
2. `internal/resolve/channel.go`:
   - `addChannelToCache(id, name)` (hold `r.mu`): mirror `addUserToCache` -
     init maps + `channelsAt` only when nil; `channels[name]=id` first-write-wins;
     `channelsByID[id]=name`.
   - `LookupChannel(ctx, id) (string, bool)`: pure-cache hit via
     `LookupChannelName`; negative-memo short-circuit; else single
     `conversations.info` (`GetConversationInfoContext`); cache on success;
     mark failed on error/empty name.
   - remove `ensureChannelCache` (only caller is `Enrich`).
3. `internal/resolve/enrich.go`: drop `hasChannelField` precheck +
   `ensureChannelCache`; call `LookupChannel(ctx, id)` per field.

### Decisions (reviewed with Codex)

- Negative memo is pragmatic: any failure/empty-name memoized for the process.
  A short-lived CLI won't recover mid-run from auth/scope/network issues, and
  bounding work matters more than a late success.
- DMs/IMs: `conversations.info` may return empty `Name` -> leave `channel_name`
  absent (best-effort). Don't assume DM name is always empty; gate on
  `info.Name != ""`.
- Don't widen negative memo to users (bug is channel-side; user path isn't
  implicated).
- Cache-completeness audit: clean. Every `r.channels` read returns only on a
  name *hit*; `saveFileCache` only persists the complete paginated map, never
  the sparse in-memory one. Sparse additions can't cause false negatives or get
  persisted as a complete snapshot.
- Keep `LookupChannelName` as the pure cache read; `LookupChannel` is the
  networked path.

## Tests (red first)

- `LookupChannel` single-ID fetch: hits `conversations.info`, never
  `conversations.list`.
- cache reuse: second lookup of same ID = 0 extra calls.
- negative memo: failing/empty-name ID looked up twice = 1 `conversations.info`.
- `Enrich` sets `channel_name` via `conversations.info`, never
  `conversations.list`.
- `Enrich` skips lookup when `channel_name` already present.
- run with `-race`.

## Commit

`fix:` - user-facing hang (13 min -> sub-second on cold channel cache).
