# Known Issues

## Behavioral

- `saved list` automatic enrichment adds `channel_name` from cache even
  without `--enrich`, but only if the channel happens to be cached from
  a prior operation. Field presence is nondeterministic on cache state.

- `file list --user` accepts only raw user IDs. Passing an email or
  `@name` silently sends the raw string to the Slack API and returns no
  results. Flag help says "user ID" but this is inconsistent with
  `--channel` which resolves names.

- `user list --query @tammer` searches for the literal string `@tammer`
  instead of stripping the `@` prefix first.

- `--fields` strips enrichment-added fields (`user_name`, `channel_name`,
  `ts_iso`) because filtering runs after enrichment. Use
  `--fields user,user_name` to get both. Documented in skill file.

- `channel members` output uses `user_id` as the field name, but
  SPEC.md shows `id`. The enrichment system relies on `user_id`.

- `message list` and `thread list` don't include `channel_id` in each
  item's output. Agents can't construct follow-up commands (`message
  permalink`, `reaction list`) from piped output alone without knowing
  the channel from the original command.

## Performance

- HTTP/2 connections are not multiplexed. The Chrome TLS transport
  creates a new H2 connection per API request rather than reusing one.
  Correct but wasteful.

- The `go 1.25.0` requirement in go.mod is higher than necessary.
  The codebase only uses Go 1.22 features (range-over-integer).

## Test quality

- Resolver channel tests (`internal/resolve/channel_test.go`) use
  catch-all `http.HandlerFunc` instead of path-checked `http.ServeMux`.
  Tests would pass even if the resolver called the wrong API endpoint.
