# Known Issues

No outstanding issues at this time. The prior list was cleared in the
holistic-fix pass; new items land here as they're discovered.

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
