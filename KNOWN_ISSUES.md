# Known Issues

## `thread list` with narrow `--fields` is CPU-bound (2026-04-21)

Surfaced while fixing the channel_id warm bug below. On a 62-message
thread, `--fields ts` takes ~5s of CPU; `--fields user,ts,blocks` takes
~0.2s. Scales linearly with message count (~80ms per message). Whenever
`user` is absent from `--fields`, something in the per-item pipeline
does ~80ms of extra CPU work. Didn't track it down - it's a different
code path than the channel_id retry storm and isn't tied to enrichment
(happens even when no resolver fields are present).

### Reproduction

```bash
# 5s CPU
time slack thread list C07TYQ7LVUH 1773191599.103689 --all \
  --fields ts --workspace E06F3U108F3 >/dev/null

# 0.2s CPU
time slack thread list C07TYQ7LVUH 1773191599.103689 --all \
  --fields user,ts,blocks --workspace E06F3U108F3 >/dev/null

# 0.2s CPU (no --fields at all)
time slack thread list C07TYQ7LVUH 1773191599.103689 --all \
  --workspace E06F3U108F3 >/dev/null
```

### Workaround

Include `user` in `--fields` if you need a narrow field set.

## Closed

### `thread list --fields channel_id` retry storm (2026-04-21)

`ensureChannelCache` didn't cache the failure path. On Enterprise Grid,
`conversations.list` 403s with `enterprise_is_restricted`; every
enriched row then re-triggered the warm, which rate-limited at 30s per
retry. A 62-message thread went from 2s to 40+s (originally reported as
3+ min / 60s timeout). Fix: on warm failure, set `r.channels` to an
empty map and stamp `r.channelsAt`, so the in-memory TTL guard absorbs
subsequent calls. `ResolveChannel` (explicit name lookup) still errors
on each call since the empty-map check falls through.

## Closed in the holistic-fix pass

Kept briefly for blame / rollback context; drop in a later cleanup.

### Behavioral

- `saved list` auto-enrichment is now deterministic. The channel cache
  warms on first call when output contains channel IDs.
- `file list --user` now resolves raw IDs, emails, and `@name` via the
  standard user resolver.
- `user list --query @tammer` strips the `@` before matching, so the
  search finds the user.
- `--fields` now runs before enrichment, so `--fields user` still yields
  `user_name` in output.
- `channel members` still emits `user_id`; SPEC.md was updated to match
  the code (and to preserve the enrichment hookup).
- `message list` and `thread list` now include `channel_id` per item.
- Pagination cursors are raw pass-through everywhere. `search messages`,
  `search files`, and `file list` stopped base64-encoding page numbers.
- `--timeout` is a global flag. Commands take context from `cli.Context()`
  and cancel on deadline.
- Resolver user cache no longer bulk-loads on first enrichment miss. It
  falls back to `users.info` for single-ID lookups; only name/email
  resolution triggers the full `users.list`.

### Diagnostics

- `--trace` emits structured JSONL to stderr for pagination and internal
  API calls (endpoint, attempt, latency_ms, error). Bot-token calls
  via `slack-go` remain untraced - tracing those needs a transport hook,
  which didn't seem worth it yet.

### Spec drift

- `--verbose` is gone from SPEC.md, replaced with `--trace`.

### Performance

- Chrome TLS transport pools HTTP/2 ClientConns per host via
  `http2.Transport`. Requests to slack.com reuse one multiplexed conn.
  HTTP/1.1 fallback is preserved (checked via ALPN in DialTLSContext).
- `go 1.25.0` in go.mod was *not* higher than necessary after all -
  `slack-go/slack`, `golang.org/x/net`, and others require 1.25. Left
  as-is. The original note conflated "our code's needs" with "effective
  minimum".

### Test quality

- `internal/resolve/channel_test.go` now uses `http.ServeMux` with
  path-specific handlers, so tests fail if the resolver hits the wrong
  endpoint.
