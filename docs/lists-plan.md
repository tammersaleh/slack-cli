# Slack Lists support

Work order: `todo/lists-are-not-supported.md`. Plan written 2026-11-04 after live verification. Keep this file current - mark steps done, record decisions.

## Verified live (desktop session token, Enterprise Grid, six lists, 6 to 1735 records)

- `/lists/<team>/<F-id>` URLs are rejected by `slackurl.Parse` (`unsupported Slack URL route "lists"`). The team segment differs between the URL a user copies (org id) and the permalink Slack returns (workspace id), so only the file id is reliable.
- `files.info` on a list returns `list_metadata` (schema, views, subtask_schema) and `list_limits` (`row_count`, `row_count_limit` 5000, `archived_row_count`, `over_row_maximum`, column counts) but no records. slack-go's `File` struct drops all of it.
- Records come only from `url_private_download` (`.../files-pri/<T>-<F>/download/list`). The body is a `files.info`-shaped JSON document with `list_metadata`, `list_records`, `list_csv_download_url` added. No cursor anywhere in it. `?limit=` is ignored. A 1735-record list came back whole (2.5 MB) and matched `list_limits.row_count`. So no pagination exists; the only truncation signal is `row_count` versus records returned, plus `over_row_maximum`.
- The download URL and the CSV URL both need the `d` cookie. Bearer token alone answers HTTP 200 with the Slack login HTML, not 401. Cookie alone works. A bot (`xoxb`) token was not tested - no OAuth credential available.
- `files.info` reports `size: 0` for every list; `file download` prints that while writing hundreds of KB.
- The public Lists API (`slackLists.items.list`, `slackLists.items.info`) answers `not_allowed_token_type` for `xoxc` on both org and workspace contexts. It needs an OAuth token with `lists:read`. Not usable for the desktop auth path.
- Every field has `key`, `value`, `column_id`, plus one type-specific key. Column types seen and their field shape:

| column type | field key | shape |
|---|---|---|
| `text`, `rich_text` | `text` and `rich_text` | `text` is Slack's rendered mrkdwn (`<@U…>` mentions, bullets as `• `, newlines between sections); `rich_text` is the block array |
| `select` | `select` | array of choice values (`Opt…` ids or slugs); labels live in `schema[].options.choices[] {value,label,color}`; `format` is `single_select`, `multi_select`, or absent |
| `user`, `created_by`, `todo_assignee` | `user` | array of user ids |
| `date` | `date`, `timestamp` | `date: ["2026-05-21"]`, `timestamp: [-1]` |
| `created_time` | `timestamp` | `[1787681564]` |
| `checkbox`, `todo_completed` | `checkbox` | bool |
| `link` | `link` | `[{originalUrl, attachment, displayAsUrl}]` |
| `message` | `message` | `[{value: permalink, channel_id, ts, thread_ts}]` (`thread_ts` is `0000000000.000000` for a non-reply) |
| `todo_due_date` | not observed set | |

- A record omits fields with no value: 4 to 7 fields on a 7-column list. A select field never had more than one value on the lists seen. Record metadata: `id` (`Rec…`), `list_id`, `date_created`, `created_by`, `updated_by`, `updated_timestamp`, `position`, `thread_ts` (present on some lists), `platform_refs`, `view_positions`.
- No list with archived rows was found, so what an archived record looks like is unknown. `list_limits.archived_row_count` is the signal to watch.


## Verified live, round two: the internal `lists.*` API (2026-11-04)

Scope changed to full parity with the Slack MCP (`slack_read_list`, `slack_create_list`, `slack_update_list`, `slack_add_list_record`, `slack_update_list_record`). Found by probing method names with no arguments: `unknown_method` means absent, `not_allowed_token_type` means OAuth-only, `invalid_arguments` with `[ERROR] missing required field: X` means it exists and names its arguments. Then exercised every write on two scratch lists owned by the probing user, both deleted with `files.delete` afterwards (`files.info` answers `file_deleted`).

Every method below is form-encoded against `slack.com/api/`, same pattern as `drafts.*`. Session cookie required. On Enterprise Grid the org (`E`) token works for all of them; the workspace (`T`) token answers `team_is_restricted` on `lists.update`, `lists.columns.create`, `lists.columns.update`, `lists.records.reorder`, `lists.views.update`, `lists.records.restore`. So all list commands ride `NewSessionClient` with no two-client split.

| method | arguments | response | notes |
|---|---|---|---|
| `lists.records.list` | `list_id`, `limit` (>= 2; 1000 accepted), `cursor`, optional `view_id` | `records[]`, `limits`, `response_metadata.next_cursor` | real cursor pagination; garbage cursor is `invalid_cursor`; archived records never appear (`archived=true` and `include_archived=true` are ignored) |
| `lists.records.info` | `list_id`, `id` | `list` (full file object including `list_metadata.schema` and `list_limits`), `record`, `subtasks[]` | deleted record is `record_deleted` |
| `lists.create` | `name`, `description` | `list` (full file object) | `schema` at create wants a `key` per column and was not pursued; add columns after. An empty `name` is accepted and makes a list titled `list` |
| `lists.update` | `id`, any of `name`, `description`, `icon` | `list` | `icon` takes `rocket` or `:wrench:`, both stored with colons |
| `lists.columns.create` | `list_id`, `type`, `name`, `options` (JSON) | `list` | `type` enum includes text, rich_text, select, multi_select, date, user, checkbox, number, link, email, channel; bogus type is `invalid_arguments`; select choices need `value`, `label`, `color` each or it is `invalid_arguments` naming the pointer |
| `lists.columns.update` | `list_id`, `id` (column id, not key), any of `name`, `options` | `list` | |
| `lists.columns.delete` | `list_id`, `id` | `{ok}` | works on the T token |
| `lists.records.create` | `list_id`, `fields` (JSON `[{key, value}]`) | `record` | **`value` is a scalar**: string for text (plain or a JSON-encoded rich_text block array), select (choice `value`), date (`YYYY-MM-DD`), user id, channel id, email, URL; bool for checkbox; number or numeric string for number. Arrays and typed keys (`rich_text`, `select`) are `invalid_arguments`. **An unknown `key` is silently dropped and an empty record is still created. A select `value` not in the choices is stored as-is.** The CLI must validate both client-side |
| `lists.cells.update` | `list_id`, `cells` (JSON `[{row_id, column_id, <typed>}]`) | `cells[]` echoing stored values | typed keys: `rich_text` (block array; `text` and `value` are rejected), `select` (array of choice values, `[]` clears, unknown is `invalid_option_id`), `checkbox`, `number` (array), `date` (array), `user` (array, `[]` clears), `email`, `channel`, `link` (`[{original_url}]`, snake_case here while reads emit `originalUrl`) |
| `lists.records.delete` | `list_id`, `id` | `{ok}` | |
| `lists.records.archive` | `list_id`, `ids` (JSON array) | `{ok}` | `limits.archived_row_count` increments; archived rows vanish from `records.list` |
| `files.delete` | `file` | `{ok}` | deletes a list |

Not found under any name tried: a list search or list-of-lists method. Title lookup (the MCP's `list_title`) is `search.files` with `type:list` plus the title, which `search files` already does; duplicates by title are common (three lists shared one title), so title resolution must be a convenience that reports ambiguity, never a silent first match.

`lists.records.create` and `lists.records.list` accept the T token too, so the earlier download-URL design is superseded: `list records` reads through `lists.records.list` with real pagination and `streamPages`, schema through `lists.records.info` or `files.info`. The 200-HTML download case no longer matters for lists.

## Decisions

Scope is parity with the five Slack MCP list tools. Every list command uses `NewSessionClient` and `PostInternalForm`; no two-client split (verified above). Records never go through the download URL.

### Command surface

```
slack list find <title>                      search.files type:list, one row per match, never picks one
slack list info <list>                       title, description, icon, limits, created/updated, permalink
slack list schema <list>                     one row per column
slack list records <list> [--limit N] [--cursor C] [--all] [--view ID] [--raw]
slack list create <name> [--description D] [--icon E] [--column SPEC]...
slack list update <list> [--name N] [--description D] [--icon E]
slack list column add <list> SPEC...
slack list column rename <list> <column> <name>
slack list column set-choices <list> <column> CHOICE...
slack list column delete <list> <column>
slack list record add <list> COLUMN=VALUE...
slack list record update <list> <record-id> COLUMN=VALUE...
```

`<list>` is an `F…` id or a `/lists/<team>/<F…>` URL. `<column>` is a column key, column id, or display name (case-insensitive, must be unique or `ambiguous_column`). `SPEC` is `NAME:TYPE[:CHOICE,CHOICE...]`, choices only for select and multi_select, `value` is the slugified label, `color` assigned round-robin from Slack's palette (the MCP does the same). `COLUMN=VALUE` pairs split on the first `=`. Alternatively a JSON object on stdin (`{"Status":"Done"}`) for values holding `=`, newlines, booleans, numbers, or `null`; the two forms are mutually exclusive. Two assignments resolving to the same column (by name, key, or id) are `invalid_input`. Zero assignments is `invalid_input` for both `add` and `update`, because Slack happily creates an empty record. stdin `null` clears on `update` and is `invalid_input` on `add`.

Not in scope, deferred: record delete and archive, list delete, column type change, views, subtasks. Each is one more form call and can land later.

### Reads

1. `list records` streams `lists.records.list` through `streamPages` with real cursors. Default `--limit` 100, Slack rejects `limit` below 2 so 1 is a local `invalid_input`. `_meta` also carries `row_count` and `archived_row_count` from `limits` so a caller can tell the hidden archived rows exist.
2. Schema for the join comes from `files.info` raw JSON (`list_metadata.schema`, `list_limits`, `title`, `description`, `icon`), fetched once before the first page (`lists.records.info` needs a record id). Records and schema can drift by one edit between the two calls; a field whose `column_id` is not in the schema fails the page with `list_schema_mismatch`. That check runs inside the fetch closure, before the page's first `PrintItem`, so `streamPages` still writes the trailer (`emit` is output-only).
3. Row shape: record metadata top level (`id`, `created_by`, `updated_by`, `date_created`, `updated_timestamp`, `position`, `thread_ts` when present), column values under `fields` keyed by display name. Duplicate names get `_2`, `_3` by probing for an unused key. Every schema column present, `null` when unset. `--fields` stays top level.
4. Rendering per column type: text and rich_text emit Slack's rendered `text` string; select and multi_select emit an array of labels (unknown value falls back to the raw value); user, created_by, todo_assignee emit an array of user ids; channel emits channel ids; date emits `date[0]`; created_time emits `timestamp[0]`; checkbox and todo_completed emit the bool; number emits `number[0]`; email emits the array; link emits an array of URLs; message emits `[{permalink, channel_id, ts, thread_ts}]`; unknown types emit the raw field object minus `key` and `column_id`. `--raw` adds `raw_fields`.
5. `list schema` rows: `{column_id, key, name, type, is_primary_column, format?, choices?:[{value,label,color}]}`.
6. `list find` runs `search.files` with `type:list "<title>"` and emits `{id, title, user, created, permalink}` per hit. It never resolves to one list; the caller picks.

### Writes

7. `list create` validates everything local first (non-empty name, every `--column` spec parsed, types known, choices only on select types, no duplicate labels or slugged values, icon shape), then calls `lists.create` with `name` and `description`, one `lists.columns.create` per `--column` in order, then `lists.update` for `--icon`. The success row is rendered from the last server response. A failure after `lists.create` is a partial item: `{"id":"F…","error":"<code>","failed_step":"column","failed_column":"Status"}` plus a hint to finish with `list column add <id>` or `list update <id> --icon`, returned as `*output.ExitError` (not `_meta.error`, which means a truncated stream). The list is never auto-deleted.
8. `list record add` resolves each `COLUMN` against the schema, converts each value per column type, and sends `lists.records.create` with `[{key, value}]`. Because Slack silently drops unknown keys and stores out-of-range select values, every column must resolve (`column_not_found`, zero calls) and every select value must match a choice label or value (`invalid_choice`, zero calls). Local validation also covers: strict `true`/`false` for checkbox, finite numbers, real calendar dates for `YYYY-MM-DD`, one value for single-select. `multi_select` on `add` is unverified (the measured API rejects arrays in `value`), so it is `invalid_input` on `add` until measured, and goes through `cells.update` on `update`. User columns accept `@name`, email, or id through `ResolveUser`; channel columns accept `#name` or id through `ResolveChannel`. The response record is emitted through the same renderer as `list records`.
9. `list record update` uses `lists.cells.update` with the typed shapes. One cell per pair, all in one call. Empty value clears (`select: []`, `user: []`, `rich_text` of one empty section is unverified, so clearing a text column is `invalid_input` until measured). `link` is sent as `[{original_url}]`. Slack validates select here (`invalid_option_id`), the CLI validates first anyway so both paths agree. `cells.update` echoes only the changed cells, so the command follows with `lists.records.info` and emits the complete server record through the shared renderer (a row padded with `null` would claim the other columns were cleared).
10. `list column add`, `rename`, `set-choices`, `delete` map one to one onto `lists.columns.create/update/update/delete`. `set-choices` is whole-set replacement: it re-reads current choices and preserves `value` and `color` for labels that survive (exact match, case-sensitive), assigns new ones to new labels, rejects duplicate labels and slug collisions between new and preserved values. A renamed label is remove-plus-add and gets a new value; the command cannot infer renames. Read-then-replace races with another editor; documented, not designed around (same as `section rename`). What Slack does to records holding a removed value is unverified: measure on the scratch list before shipping, since the pattern elsewhere is silent acceptance.
11. `list update` maps onto `lists.update`. At least one flag required.
12. All write rows come from the server response, never from the request (same rule as `section rename`).
13. `file download` keeps `size` and adds `bytes_written` via a counting writer.

### Codex review (2026-11-04, parity scope)

Accepted: `set-choices` naming, mutually exclusive positional/stdin input with first-`=` split and duplicate detection, the local validation list, partial `list create` failure as an `ExitError` row with recovery hint, `record update` re-reading the record, schema mismatch inside the fetch closure, success rows from the final response, exact-match label preservation and slug-collision checks. Rejected: moving the `bytes_written` fix out of this plan. It is in the same work order, ships as its own `fix:` commit, and costs nothing to keep here.

### Codex review (2026-11-04, first scope)

Accepted: suffix probing, join-by-`column_id` with a loud mismatch error, labels always as arrays, document shape validation, explicit `list schema` contract, helpers in `cmd/` instead of a package. Rejected: HTML detection in `file download` (unverified bot-token case, would reject real HTML files). The truncation design from that round is moot now that `lists.records.list` paginates.

## Steps

Each step is one `feat:`/`fix:` commit on its own branch, run as its own sub-agent per CLAUDE.md, with SPEC.md, SKILL.md, and CLAUDE.md updated in the same commit.

- [ ] (a) `feat(slackurl): accept /lists/ URLs as file links`. `lists` case in `Parse`: `len(segs) >= 3`, `segs[2]` matches `fileIDRE`, `segs[1]` ignored. Tests: both team-id forms give the same `FileID`; missing id; bad id. Hint text in `internal/output/errors.go` mentions list links.
- [ ] (b) `feat(list): add list info, schema, records, find`. `cmd/list.go` + `cmd/list_render.go` (pure: schema join, rendering, name dedupe) + tests via `runWithMockSession`. Table-driven rendering tests cover every type in the table above, unknown type, missing field, duplicate names including `Foo`/`Foo_2`/`Foo`, unknown select value, unknown `column_id`.
- [ ] (c) `feat(list): add list create, update, and column commands`.
- [ ] (d) `feat(list): add list record add and update`. Includes user and channel resolution and client-side validation. Mock must model Slack dropping unknown keys so a test proves the CLI refuses before the call.
- [ ] (e) `fix(file): report bytes written by file download`.
- [ ] Each step: code review pass, push, wait for release, install, verify on a scratch list named `slack-cli scratch` owned by the user, delete it afterwards with `files.delete` (raw call, there is no CLI command).
- [ ] Delete `todo/lists-are-not-supported.md`.

## Reference for the implementing session

### How the probes were run

Token and cookie come from the credentials file (`~/Library/Application Support/slack-cli/credentials.json`, `workspaces[<id>].bot_token` and `.cookie`), selected by `SLACK_WORKSPACE` (T) and `SLACK_WORKSPACE_ORG` (E). Each call is a form POST to `https://slack.com/api/<method>` with `Authorization: Bearer <xoxc>`, `Cookie: d=<xoxd>`, and a Chrome User-Agent. `PostInternalForm` already does exactly this. Error detail lives in `response_metadata.messages` as `[ERROR] missing required field: X` or `[ERROR] ... [json-pointer:/fields/0/value]`; surface those strings in the error `detail`, they are the only documentation this API has. A method probed with no arguments answers `unknown_method`, `not_allowed_token_type`, `team_is_restricted` (wrong Grid context), or `invalid_arguments` naming the required fields. That is a side-effect-free way to confirm a method still exists before relying on it.

### Response shapes (synthetic ids)

`lists.records.list`:

```json
{"ok":true,"records":[{"id":"Rec01AAA","list_id":"F01ABC","date_created":1709251200,"created_by":"U01XYZ","updated_by":"U01XYZ","updated_timestamp":"1709251300","position":"1709251200.12345","thread_ts":"1709251201.000100","platform_refs":{},"view_positions":{"View01AAA":"1709251200.12345"},"fields":[{"key":"name","column_id":"Col01AAA","value":"[{\"type\":\"rich_text\",...}]","text":"Ship it","rich_text":[{"type":"rich_text","block_id":"x","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"Ship it"}]}]}]},{"key":"Col01BBB","column_id":"Col01CCC","value":"done","select":["done"]}]}],"limits":{"over_row_maximum":false,"row_count_limit":5000,"row_count":53,"archived_row_count":0,"over_column_maximum":false,"column_count":7,"column_count_limit":30,"over_view_maximum":false,"view_count":2,"view_count_limit":50,"max_attachments_per_cell":10},"response_metadata":{"next_cursor":"bGlzdF9pZDo..."}}
```

`next_cursor` is an empty string on the last page. Record order within a walk was not characterized (not creation order, not obviously position order); do not promise one in SPEC.

Schema column (from `files.info` `file.list_metadata.schema[]`, also inside every `list` object the write methods return):

```json
{"id":"Col01CCC","name":"Status","key":"Col01BBB","type":"select","is_primary_column":false,"options":{"format":"single_select","default_value":"todo","choices":[{"value":"todo","label":"To do","color":"gray"},{"value":"done","label":"Done","color":"green"}]}}
```

The primary text column has `key: "name"`. Todo-style columns created by Slack templates have short ids (`Col00`, `Col01`) and keys like `todo_completed`. `id` and `key` are different strings on every column except those template ones; reads join on `id` (`column_id`), `lists.records.create` addresses columns by `key`, `lists.cells.update` and `lists.columns.*` by `id`. `options` is absent (`null`) on plain columns; text columns sometimes carry `{"format":"text"}`. `list_metadata` also has `description` (string), `icon` (`":wrench:"`), `views[] {id,name,type}`, `subtask_schema[]`.

`lists.create`, `lists.update`, `lists.columns.create`, `lists.columns.update` all return `{"ok":true,"list":{...}}` where `list` is the full file object: `id`, `title`, `name` (always `"list"`), `user`, `user_team`, `created`, `updated`, `permalink` (`https://acme.slack.com/lists/T01ABC/F01ABC`), `list_metadata`, `list_limits`, `list_csv_download_url`, `url_private_download`. `lists.records.create` returns `{"ok":true,"record":{...}}` with the same record shape as the list method, fields echoed with their typed keys filled in server-side. `lists.records.info` returns `{"ok":true,"list":{...},"record":{...},"subtasks":[]}`. `lists.cells.update` returns `{"ok":true,"cells":[{"row_id":"Rec01AAA","column_id":"Col01CCC","select":["done"]}]}`, changed cells only. `lists.columns.delete`, `lists.records.delete`, `lists.records.archive`, `files.delete` return a bare `{"ok":true}`.

### Write payloads that worked, verbatim shapes

`lists.records.create`, form field `fields` holding JSON:

```json
[{"key":"name","value":"Ship it"},{"key":"Col01BBB","value":"done"},{"key":"Col01DDD","value":"2026-12-01"},{"key":"Col01EEE","value":"U01XYZ"},{"key":"Col01FFF","value":true},{"key":"Col01GGG","value":3},{"key":"Col01HHH","value":"https://example.com"},{"key":"Col01III","value":"a@example.com"},{"key":"Col01JJJ","value":"C01ABC"}]
```

A text or rich_text column also takes a JSON-encoded block array as the string value (`"[{\"type\":\"rich_text\",...}]"`) and Slack renders `text` from it, including `<@U…>` for a user element. That is how a `--markdown` path would plug in later via `internal/mdblocks`. A numeric string for a number column is accepted and stored as a number.

`lists.cells.update`, form field `cells`:

```json
[{"row_id":"Rec01AAA","column_id":"Col01AAA","rich_text":[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"edited"}]}]}]},
 {"row_id":"Rec01AAA","column_id":"Col01CCC","select":["done"]},
 {"row_id":"Rec01AAA","column_id":"Col01DDD","date":["2027-01-02"]},
 {"row_id":"Rec01AAA","column_id":"Col01EEE","user":["U01XYZ"]},
 {"row_id":"Rec01AAA","column_id":"Col01FFF","checkbox":false},
 {"row_id":"Rec01AAA","column_id":"Col01GGG","number":[7]},
 {"row_id":"Rec01AAA","column_id":"Col01HHH","link":[{"original_url":"https://example.org"}]},
 {"row_id":"Rec01AAA","column_id":"Col01III","email":["b@example.com"]},
 {"row_id":"Rec01AAA","column_id":"Col01JJJ","channel":["C01ABC"]}]
```

Rejected there: `text`, `value` (`invalid additional property`), `link` without `original_url`, a `row_id` not matching `^Rec[A-Z0-9]{8,}$` or `column_id` not matching `^Col[A-Z0-9]{2,}$` (schema-validated before any lookup, so a bogus id is `invalid_arguments`, not `record_not_found`), a select value outside the choices (`invalid_option_id`). Clearing verified: `select: []`, `user: []`. Clearing a text column (empty `rich_text`) and clearing date, number, checkbox were not tried.

`lists.columns.create`, form fields `list_id`, `type`, `name`, `options` (JSON):

```json
{"choices":[{"value":"todo","label":"To do","color":"gray"},{"value":"done","label":"Done","color":"green"}],"format":"single_select"}
```

Every choice needs all three of `value`, `label`, `color` or the call is `invalid_arguments` with a json-pointer to the choice. Colors seen in the wild: blue, green, yellow, red, purple, orange, cyan, pink, indigo, brown, gray (the MCP's list; only the first eight plus gray were observed on real lists). `multi_select` was accepted as a `type` once choices carried colors. `lists.columns.update` takes `list_id`, `id`, and any of `name`, `options`; `type` change was not tried.

`lists.update`: `id` plus any of `name`, `description`, `icon`. `lists.create`: `name`, `description`; `schema` exists as a parameter but demanded a `key` per column and was not pursued.

### Unverified, measure on the scratch list before promising

- What `lists.records.list` order is, and whether `view_id` changes it.
- Whether `lists.records.create` enforces single-select cardinality (it takes a scalar, so probably moot) and what happens to records when `set-choices` removes a value they hold.
- Clearing text, date, number, checkbox via `cells.update`.
- `multi_select` writes on create (scalar `value` only) and on `cells.update` (`select` with two values).
- `lists.columns.update` with `type`.
- Whether an OAuth bot token can call any `lists.*` method (no OAuth credential on this machine; `session_token_required` is the likely answer and the command should say so like `saved list` does).
- `lists.records.list` on a list the user cannot see (expect `list_not_found`, the same code a bogus id gets).

### Cleanup that is not a CLI command

The scratch list has to be deleted with a raw `files.delete` call (`file=<F id>`); there is no `file delete` command. The record-level `lists.records.delete` and `lists.records.archive` also work if a test leaves rows behind on a real list.

### Housekeeping

- `todo/lists-are-not-supported.md` is the work order; delete it when (a) through (e) ship. It holds real workspace identifiers and is untracked, which is fine. This plan file is committed, so keep it synthetic.
- `cmd/skill_test.go` reads `skills/slack-cli/SKILL.md` from disk and asserts on content; new commands need a SKILL.md entry and probably a test line.
- The CLAUDE.md "Internal Slack APIs" key-endpoint list and the `cmd/` tree in "Project structure" both need `list.go` entries.
- SPEC.md gets a `### list` section next to `### file`; the Grid token note belongs there (org token for everything, no split).
