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

## Test quality

- Resolver channel tests (`internal/resolve/channel_test.go`) use
  catch-all `http.HandlerFunc` instead of path-checked `http.ServeMux`.
  Tests would pass even if the resolver called the wrong API endpoint.
