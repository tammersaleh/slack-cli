# Plan: resolve `@name` in search `from:`/`to:`/`in:` modifiers

Work order: `todo/search-from-to-modifiers-with-display-names-return-empty-silently.md`.
Codex MCP was down for this session, so the planning review it normally gets
did not happen. Flagged to Tammer.

## Verified live 2026-09-21 (Enterprise Grid, desktop session token)

`search messages` passes the query to Slack verbatim. For a user whose handle
is `h`, real name `First Last`, id `Uxxx`:

| Query form | Rows |
|---|---|
| `from:@h` | yes |
| `from:<@Uxxx>` | yes, identical rows |
| `from:@First Last` | none, exit 0, no error |
| `from:@Uxxx` | none |
| `from:"First Last"` | none |
| `in:@h` | yes |
| `in:<@Uxxx>` | yes |
| `in:@First Last` | none |
| `to:` | no rows in any form against the peers tried; unverified, treated like `from:` |

So the canonical rewrite target is `<@Uxxx>`, and `ResolveUser` already maps
handle, display name, real name, email, and ID to `Uxxx`.

## Design

New `cmd/searchquery.go`, `rewriteUserModifiers(ctx, resolver, query) (string, error)`,
called by both `search messages` and `search files` before the API call.

Tokenize the query on whitespace, keeping double-quoted spans as one token.
A token is a user modifier when it matches `^-?(from|to|in):(.+)$`
(case-insensitive key) and the value, after stripping surrounding quotes,
starts with `@`. `<@U...>` values are left alone. Non-`@` values (`in:#chan`,
`in:general`) are left alone.

The name span:

- Quoted (`from:"@First Last"` or `from:@"First Last"`): the quoted text is
  the whole name.
- Unquoted: the `@word` plus every following token up to the next
  modifier-shaped token (`^-?[a-z]+:`) or end of query. Resolve the longest
  prefix of that span that resolves (try all words, then drop the last word,
  down to just `@word`). This makes `from:@First Last skypilot` work without
  quotes. Lookups after the first are in-memory cache reads.

On resolution, replace the consumed tokens with `key:<@Uxxx>` (keeping a
leading `-`). If no prefix resolves, fail with `user_not_found` for the full
span plus a hint to quote the name or pass `from:<@Uxxx>` directly. Ambiguous
names keep `ResolveUser`'s first-match behavior; documented, not fixed.

## Steps

- [x] Reproduce live and confirm the rewrite target
- [x] Write plan
- [ ] Tokenizer + rewrite unit tests (package cmd, table-driven)
- [ ] `rewriteUserModifiers` implementation
- [ ] Wire into `search messages` and `search files`; end-to-end tests
      asserting the `query` form param sent to `search.messages`/`search.files`
      and the `user_not_found` failure
- [ ] Update `Help()`, SPEC.md, SKILL.md, CLAUDE.md
- [ ] `mise run test`, `mise run lint`, code review, push, wait for release,
      verify live with the installed binary against a real-name query
- [ ] Delete the todo file and this plan

## Out of scope

The `ts: null` rows noted at the bottom of the todo. Left in a new todo file.
