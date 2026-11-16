# Drafts

Slack's `drafts.*` endpoints are an undocumented internal API that the
Slack Desktop client uses to persist unsent messages across devices.
`slack-cli` wraps them so an agent can stage a message for a human to
review and send.

This document is more detailed than usual because getting the behavior
right took a long investigation. The summary up front; the receipts
below.

## TL;DR

Use rich_text blocks for prose, header blocks for headings, divider blocks
for horizontal rules, and nothing else. For multi-paragraph prose with
bullets use sibling containers inside one `rich_text`. Everything else is a
trap.

```json
[{"type":"header","level":1,"text":{"type":"plain_text","text":"Title","emoji":true}},
 {"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello"}]}]}]
```

The header block is the shape the compose editor stores for its big /
medium / small headings (read back from a composer-authored draft
2026-11-06); the matrix below predates it. See "Header blocks" under
Markdown input.

Two independent failure modes:

1. **Tombstone.** Slack Desktop sets `is_deleted=true` on any
   server-side draft whose `blocks` array doesn't contain at least one
   `rich_text` block with non-empty `elements`, a `header` block, or a
   `divider` block. Lands within seconds of the user opening the Drafts
   panel.
2. **Strip.** Slack Desktop's Drafts compose editor (the panel the
   user opens to review and send) silently discards every top-level
   block that is not `rich_text`, `header`, or `divider` when it renders
   the draft. A `[rich_text, section]` array ships fine to the API but
   the user only sees the rich_text content. Not reversible - once
   rendered for edit, the stripped content never comes back.

Block type is the only variable that matters. Host, user-agent, TLS
fingerprint, and the `last_updated_client` stamp are all irrelevant.

## Markdown input

`draft create --markdown` and `draft update --markdown` convert Markdown
into the shape above via `internal/mdblocks` (goldmark) - a `header` block
per heading and one `rich_text` block per run of content between headings -
then run `validateBlockShapes` on the result. The subset, the mapping table, and
the rejection list live in SPEC.md under `slack draft create`.

Verified 2026-10-16 against the live `drafts.*` API, self-DM, session
token on an Enterprise Grid org. One draft held every supported construct:
a heading, a paragraph with bold / italic / strike / code / bold+italic
and a trailing emoji, a bullet list with a link and an emoji in items plus
nesting to indent 2 and a sibling after the nested lists, an ordered list
starting at 3 (`offset: 2`) split by a nested bullet list and resuming at
`offset: 4`, a two-paragraph quote, a fenced code block, a `user` element,
a `broadcast`, and in follow-up probes a `channel` element and a
skin-toned emoji (`name: "wave::skin-tone-3"`).

| Check | Result |
|---|---|
| `drafts.create` accepts the payload | ok, every probe |
| `drafts.list` blocks after 25s vs payload sent | byte-identical (plus `block_id`) |
| `is_deleted` after 25s | false, every probe |
| Desktop compose box: italic, strike, `offset` numbering, whether the trailing `\n` after a paragraph is a blank line or a line break | not inspected - the probe draft was left in the self-DM for a human to open |

The reconciliation window was observed with Desktop running; whether the
Drafts panel was opened during it is not known, so "not tombstoned" is
necessary rather than sufficient evidence. The CLI author's claim is
limited to the API round-trip.

### Header blocks (2026-11-06)

The compose editor's formatting bar offers Big / Medium / Small headings.
A composer-authored draft holding three of them and nothing else reads
back as three top-level `header` blocks, no `rich_text` at all, not
tombstoned:

```json
[{"type":"header","level":1,"text":{"type":"plain_text","text":"Big heading","emoji":true}},
 {"type":"header","level":2,"text":{"type":"plain_text","text":"Medium heading","emoji":true}},
 {"type":"header","level":3,"text":{"type":"plain_text","text":"Small heading","emoji":true}}]
```

`block_id` is server-assigned. Probes through the CLI's own session client
(`PostInternalForm`, `is_from_composer: true`), self-DM, each read back
byte-identical modulo `block_id` and alive after 60s unless noted:

| payload | result |
|---|---|
| `[header L1, rich_text(section "para one\n"), header L2 with `:tada:`, rich_text(list, section), header L3]` | accepted, round-trips |
| header without `level` | accepted, stored without `level` |
| header `level: 4` | accepted, stored as 4; Desktop renders it as the small heading, the same as 3 |
| header `level: 0` | `invalid_message` |
| header without `emoji` key | accepted, server adds `emoji: true` |
| header only, no rich_text | accepted, alive after 60s (a header is renderable content) |
| header `text.type: mrkdwn` | `invalid_message` |
| header with empty `text.text` | `invalid_message` |
| `[rich_text(section "no newline"), header, rich_text(list)]` | accepted, round-trips; Desktop renders paragraph, heading, bullet with no glue |

So `validateBlockShapes` admits `header` beside `rich_text`, requires
`plain_text` with non-empty text, requires `level` (when present) to be an
integer of at least 1 with no upper bound, and counts a header as
renderable content. It treats a header as an absorption boundary: a
section without a trailing `\n` followed by a header and then a list is
accepted. 5.10.0 rejected it, because the probe proved storage, not
rendering; the Desktop inspection below relaxed it the same day.

The Markdown converter maps `#`/`##`/`###` to level 1/2/3 and clamps
deeper headings to 3 (the composer offers three sizes). Header text is
plain, so inline formatting flattens, `:name:` is written back as text,
and a link, mention, channel, or broadcast inside a heading is
`invalid_markdown`.

Desktop compose box, inspected 2026-11-06 on the probe drafts above and on
a `--markdown` draft with headings at levels 1, 2, and 3 followed by
paragraphs and a list:

- The Markdown draft renders its three headings at three sizes, then the
  paragraphs and the list.
- `level: 4` renders as the small heading, visually the same as level 3.
  The converter's clamp to 3 is what the client does anyway.
- `:tada:` inside a header's plain_text renders as the emoji.
- A paragraph with a trailing `\n` before a header and one without look
  identical. The header supplies its own spacing.
- `[rich_text(section "no newline"), header, rich_text(list)]` renders as
  a paragraph, a heading, and a bullet. No glue. A header is a boundary
  for section-before-container absorption, so the validator resets its
  state at one.

### Divider blocks (2026-11-06)

The compose editor's formatting bar also offers a horizontal rule. A
composer-authored draft with text, rule, text, rule, text reads back as
`rich_text, divider, rich_text, divider, rich_text`, each divider a bare
`{"type":"divider"}` plus a server-assigned `block_id`. Not tombstoned.
This supersedes the May 2026 matrix rows and the DOM observation that a
divider is dropped: a composer-authored divider is kept.

Probes through the CLI's session client (self-DM, read back byte-identical
modulo `block_id`, alive after 100s):

| payload | result |
|---|---|
| `[divider]` only | accepted, alive (a divider is renderable content) |
| `[rich_text(section without "\n"), divider, rich_text(list)]` | accepted, round-trips |
| `[divider, rich_text, divider, divider, header, divider]` | accepted, round-trips, double divider stored as two |

So `validateBlockShapes` admits `divider` beside `rich_text` and `header`,
checks only its `type` (every other field passes through), and counts it
as renderable content. The Markdown converter emits one for any CommonMark
thematic break (`---`, `***`, `___`), flushing the rich_text run around it,
so leading, trailing, and consecutive rules each become a divider. A rule
inside a quote or list item keeps the container rejection. `Title`
directly followed by `---` is a Setext heading, not a divider.

Desktop rendering is pending a check: divider-only, leading and trailing,
consecutive dividers, and whether a divider is an absorption boundary (the
`[section without "\n", divider, list]` probe). Until then the validator
carries the section-before-container state across a divider, so that
payload is rejected like the same section and list with nothing between
them.

### Quoted lists and code (2026-11-03)

Slack has no list-in-quote nesting: a `rich_text_list` inside
`rich_text_quote.elements` is `invalid_message` at `drafts.create`. The
composer serializes a quoted list as a sibling `rich_text_list` with
`border: 1` and no `rich_text_quote` at all. `--markdown` now emits that
shape for `> - item`, and `rich_text_preformatted` with `border: 1` for a
code block inside a quote.

Measured 2026-11-02 (self-DM, session token, Enterprise Grid org) with a
probe draft holding quote paragraph, bordered bullet list at indent 0 and 1,
bordered ordered list with `offset: 2`, a second quote paragraph, a
bordered preformatted block, and a trailing section: `drafts.create`
accepted it and `drafts.list` returned every `border`, `indent`, and
`offset` unchanged, immediately and after 30s, `is_deleted` unset.

Desktop compose box, inspected 2026-11-03:

| Check | Result |
|---|---|
| quote paragraph, bordered lists, second quote paragraph, bordered code block | one continuous left bar; the quote's last line does not glue into the first bullet |
| `rich_text_preformatted` with `border: 1` | renders as a code block inside the bar (whether the composer itself emits this shape for a quoted code block was not checked) |
| bordered `indent: 1` item | renders nested (hollow bullet) under the bordered parent |
| ordered item with `offset: 2` | renders as `1.`, not `3.`; the compose box ignores `offset`, at least on a bordered list |

The `offset` finding also answers the open row in the table above for
bordered lists. An unbordered ordered list's `offset` rendering is still
not inspected. The converter keeps emitting `offset` since the API stores
it and the sent message may honor it.

## Endpoints

All four endpoints are POST against `https://slack.com/api/<method>`,
form-encoded, authenticated with a session (xoxc-) token plus a `d`
cookie. They return `application/json` in the usual
`{"ok": bool, ...}` / `{"ok": false, "error": "..."}` shape.

### drafts.list

```
POST https://slack.com/api/drafts.list
Content-Type: application/x-www-form-urlencoded
Authorization: Bearer xoxc-...
Cookie: d=xoxd-...
```

Body fields:

- `token` - duplicate of the Authorization token. Required.
- `limit` - page size. Omitted, the page is 10. Above 100 it is clamped to 100.
- `is_active` - `"true"` returns only drafts that are neither sent nor deleted, filtered before `limit` applies. `"false"` is the same as omitting it.
- `last_updated_ts` - a lower bound: only drafts updated after it are returned. Still newest-first and still capped, so it does not page.

Response:

```json
{"ok": true, "drafts": [...], "files": [...], "has_more": false}
```

`files` holds one full file object per distinct file that a draft on the
page references and that still exists. It is `[]` when no draft on the page
has a file. See "Files on drafts" below.

#### Measured 2026-11-10

Read-only probes through the CLI's session client (the out-of-tree probe
module described in `CLAUDE.md`) against an account holding more than 100
drafts. Only counts, key names, and id-set overlaps were recorded.

The page is ordered by `last_updated_ts`, descending. `date_created` is not
monotonic across it. The order and the id set were identical across some 40
requests over 15 minutes. `limit` 100, 200, and 1000 each returned the same
100 drafts in the same order with `has_more: true`, and no `limit` returned
the first 10 of those.

Nothing continues past the page. Each of these, sent with `limit=100` and a
value taken from the last draft of the page (its id or its `last_updated_ts`),
returned the same 100 ids: `cursor`, `page`, `offset`, `skip`, `marker`,
`after`, `last_id`, `latest`, `oldest` with `latest`, `before`, `before_ts`,
`max_ts`, `max_last_updated_ts`, `last_updated_ts_before`, `until`, `ts`,
`start_ts`, `count`. `sort`, `sort_dir`, `order`, and `direction` set to `asc`
changed nothing, and neither did `is_sent=false`, `is_deleted=false`,
`include_sent=false`, `include_deleted=false`, or `is_scheduled=true`. The
response has no cursor and no `response_metadata`: its top-level keys are
`ok`, `drafts`, `files`, and `has_more` (`files` was empty in these probes
and went unexamined until 2026-11-16, see "Files on drafts"). The Slack web
client's own request was not captured.

`last_updated_ts` is the one parameter that changed the answer. Set to the
last draft's value it returned 99 drafts, the page minus that draft. Set to 0
or to a time before the page it returned the full page. It is a "changed
since" bound for syncing, and the answer is still the newest 100 above it.

`is_active=true` is what reaches further. The plain page of 100 held 73 sent
drafts, 26 deleted, and 1 live. `is_active=true` at `limit=100` returned that
1 draft with `has_more: false`. At `limit=10` it returned the same draft,
which is not among the newest 10 of the plain listing, so the filter runs
before the limit. `drafts.listActive` (a separate endpoint that returns
`active_draft_ids` and no draft bodies) named the same single id.

That one live draft was scheduled, and had `is_from_composer: true`. So two
things are not measured: whether `is_active=true` also returns an unscheduled
live draft, and whether it returns a live draft with `is_from_composer:
false`. To settle the first, leave an unsent, unscheduled draft in any
conversation and compare `slack draft list --limit 100` with a probe-module
`is_active=true` request.

Every one of the 100 drafts carried `is_sent`, `is_deleted`, and
`is_from_composer`, along with `blocks`, `client_msg_id`, `date_created`,
`date_scheduled`, `destinations`, `file_ids`, `id`, `last_updated_client`,
`last_updated_ts`, `team_id`, and `user_id`. 10 of the 100 also carried
`unfurl`, which the CLI does not model. No draft was both sent and deleted.

What the CLI does with this: `draft list` prints the live rows of the plain
page and, when that page is truncated, adds the `is_active=true` answer.
`draft update` and `draft delete` look an id up in the plain page of 100 and
then in the active answer. The plain request stays first because it is what
shows sent and deleted state, and because the active filter's coverage of
every live draft is not measured.

Not measured, because both need the owner to send a draft from a client:
what `drafts.update` answers for a sent draft (the CLI does not depend on the
code), and whether a sent draft later becomes `is_deleted` as well (the CLI
checks sent first).

### drafts.create

Same URL pattern, method `drafts.create`. Body fields:

- `token`
- `blocks` - Block Kit JSON array; must include rich_text (see below).
- `destinations` - JSON array of one destination object. Example:
  `[{"channel_id":"D09..."}]` or `[{"channel_id":"C01...","thread_ts":"1709.1"}]`.
- `client_msg_id` - lowercase UUID v4. Other formats fail with `invalid_client_msg_id`.
- `file_ids` - JSON array of `F…` file ids, `"[]"` for none. Required:
  omitting the param is `invalid_arguments`. An id that does not exist, or
  whose upload was never completed, is `file_not_found`.
- `is_from_composer` - `"false"` from the API. Desktop sends `"true"`.
- `date_scheduled` - optional Unix seconds for scheduled send.

Response:

```json
{"ok": true, "draft": {...}, "files": [...]}
```

`files` holds the file objects for the draft's `file_ids`, in the same
order.

Each destination channel can have at most one active draft. Creating a
second draft for a destination that already has one returns
`attached_draft_exists`.

### drafts.update

Body fields, in addition to the create set:

- `draft_id`
- `client_last_updated_ts` - the existing draft's `last_updated_ts`, but
  padded to exactly 7 decimal places. The server returns 6; update and
  delete both reject 6-decimal values with a conflict. `padDraftTS`
  handles this.

Errors: `draft_update_invalid` when the draft is already tombstoned
(`is_deleted=true`). A report on 2026-10-13 points at the same code for a
draft that was already sent (inferred from the code path taken, not
captured), so the CLI does not read the code. On any failed update it lists
the drafts again and acts on the draft's state: see Auto-replace below.

### drafts.delete

Body fields:

- `token`
- `draft_id`
- `client_last_updated_ts` (padded, same as update)

Errors: `draft_delete_invalid` when already tombstoned. `draft delete` does
not send the request for a draft `drafts.list` shows deleted: it reports
`status: already_deleted` and exits 0. If the delete fails on a draft the
lookup showed live, it lists the drafts again and reports `already_deleted`
only when the draft is now marked deleted.

### Draft shape

```json
{
  "id": "Dr0...",
  "client_msg_id": "uuid",
  "user_id": "U...",
  "team_id": "E... or T...",
  "date_created": 1776538579,
  "date_scheduled": 0,
  "last_updated_ts": "1776538579.203325",
  "last_updated_client": "Slack SSB Mac (Atom)",
  "blocks": [...],
  "file_ids": ["F0..."],
  "is_from_composer": false,
  "is_deleted": false,
  "is_sent": false,
  "destinations": [{"channel_id": "D09...", "user_ids": ["U..."]}]
}
```

The response carries no `client_last_updated_ts`. That is a *request* parameter,
which update and delete send as `padDraftTS(last_updated_ts)`. An earlier version
of this example listed it here, next to the response's own `last_updated_ts` and
`last_updated_client`, which is how it got mistaken for a response field and
picked up a matching struct tag. Verified against a real `drafts.list` payload
2026-07-28.

Slack reflects DM destinations back with both `channel_id` and
`user_ids`. When you update or delete, strip `user_ids` from
destinations that also have `channel_id`, or the API rejects with
`both_user_ids_and_channel_id_provided_in_destination`. See
`normalizeDestinationsForWrite` in `cmd/draft.go`.

`file_ids` is `[]` on a draft with no files. A draft with a file has the same
key set as one without. The file objects are not inside the draft: they are
in the response's top-level `files` array.

### Files on drafts (2026-11-16)

Measured on an Enterprise Grid org with a desktop session token (E-org
context), through the out-of-tree probe module, in a self-DM only. Every
probe draft and file was deleted afterwards. The plan and the design that
came out of it are in `docs/draft-files-plan.md`.

Read-only, from `drafts.list limit=100`: 4 of 100 drafts carried a non-empty
`file_ids`, all four of them sent.

- `file_ids` is a JSON array of `F…` file id strings.
- The top-level `files` array holds one full file object (the `files.info`
  shape: `id`, `name`, `title`, `size`, `filetype`, `mimetype`, `mode`,
  `shares`, thumbnails, and more) per distinct file that still exists and is
  referenced by a draft on the page. It is deduplicated. A deleted file is
  absent from it while its id stays in the draft's `file_ids`.
- Two of the four were composer drafts (`is_from_composer: true`) with
  `blocks: []` and one file. The composer's own file-only shape is an empty
  blocks array.

Upload: slack-go v0.18.0's helpers work on the session client (the xoxc
token plus the `d` cookie): `GetUploadURLExternalContext`, `UploadToURL` (a
multipart POST to `files.slack.com`), and `CompleteUploadExternalContext`
with no channel. The result is a file with no shares, `is_public: false`,
visible to the uploader.

- A 29-byte `.txt` became `mode: snippet`, a 600-byte PDF `mode: hosted`.
  The PDF's `filetype` and `mimetype` were empty on a `files.info` seconds
  after the upload and `pdf` / `application/pdf` a minute later.
- `files.getUploadURLExternal` with `length=0` is `invalid_arguments`. With
  `length=99999999999999` it answered ok, so no size limit shows at that
  step.
- A file id whose upload was never completed is `file_not_found` on
  `drafts.create`.

`drafts.create` with files:

| request | result |
|---|---|
| rich_text plus one file id | ok. Response is `{ok, draft, files}`, `draft.file_ids` holds the id, `files` the file object |
| `blocks=[]` plus one file id | ok, `is_deleted: false` |
| a file id already on another live draft | ok |
| two file ids (PDF, txt), thread destination | ok, order kept in `draft.file_ids` and `files` |
| an id that does not exist | `file_not_found` |
| `file_ids` param omitted | `invalid_arguments` |

All three live drafts were alive (`is_deleted: false`) 78 seconds after
create with Slack Desktop running. The Drafts panel was not known to be
open, so survival of panel reconciliation is unverified, for the file-only
draft in particular.

Update and delete:

- `slack draft update <id> --markdown` on the text-plus-file draft kept the
  file: the `file_ids` round trip in `buildIntent` works.
- `drafts.delete` leaves the files in place (`files.info` still answers).
- `files.delete` on a file a live draft references answers ok. The draft
  keeps the dangling id in `file_ids` and the file drops out of the
  top-level `files`. What Desktop shows for such a draft is unknown.

What the CLI does with this: `draft create --file` and `draft update --file`
upload through those three steps and send the ids as `file_ids`,
`draft update --no-files` sends `[]`, a file counts as renderable content,
the tombstone replacement carries `file_ids` across, and every draft row has
`file_ids` plus a `files` summary joined from the response's top-level
array. Uploaded files are deleted when the upload fails or Slack refuses the
draft write, and left in place (ids in the error's `detail`) when the
write's outcome is unknown. The CLI never deletes a file it did not upload
in the same invocation.

Mock-tested only (`cmd/draft_files_test.go`): the whole `--file` path
through the CLI, every cleanup branch, and the row join. The mock models the
measurements above.

Not measured: Desktop rendering of a CLI-attached file, sending such a
draft, an open Drafts panel, a scheduled draft with a file, `--broadcast`, a
workspace (T) token, the request sequence the composer itself makes, the
errors for an oversized or blocked file, the `drafts.update` response shape
(assumed `{ok, draft, files}` like create), and what `files.delete` answers
for an id whose upload was never completed (the CLI treats `file_not_found`
there as nothing left to delete).

### Drafting to a conversation that doesn't exist yet

A `channel_id` only exists once a DM/MPDM has been opened. To stage a
draft to people the user has never messaged, send a `user_ids`
destination instead - no `channel_id`. Slack opens the conversation when
the draft is sent. `drafts.create` accepts `{"user_ids":["U1","U2",...]}`;
this is exactly what the web client does for a new DM/MPDM. `draft create`
builds it from user recipient args (`@name` / email / `Uxxx`), one for a
1:1 DM, several for an MPDM. Set exactly one of `channel_id` / `user_ids`
per destination, never both. Order is preserved and duplicates removed;
no local recipient cap (MPDMs cap near 8 people - Slack rejects an
oversized list).

## Auth

Cookie auth only. Any xoxc- session token plus the matching `d` cookie
works. A plain Chrome UA or Go's default UA is fine. Enterprise Grid
needs the E-prefix org-level token (saved via `slack auth login
--desktop` with `SLACK_WORKSPACE_ORG` set); the T-prefix workspace
token returns `team_is_restricted`.

No special routing is needed. `slack.com/api/drafts.*` works from any
network that can reach Slack. No workspace subdomain, no
`slack_route` query param, no Slack_SSB user-agent suffix.

Channel-*name* resolution is the exception to "org token for everything":
on Grid, `users.conversations` and `conversations.list` are
`enterprise_is_restricted` on the E-prefix token, so `draft create
'#name'` resolves the name on a separate workspace (T-prefix) client
before posting `drafts.create` on the session client. Select the pair
with `SLACK_WORKSPACE` plus `SLACK_WORKSPACE_ORG` and leave `SLACK_TOKEN`
unset (it collapses both clients onto one token). Channel IDs and
channel/message URLs resolve locally; every user-recipient form (`@name`,
email, U/W id, profile URL) stays on the session credential.

## The ghosting bug

Slack Desktop's "Drafts & sent" panel runs a reconciliation pass every
time the user opens it: it diffs server-side drafts against its local
IndexedDB and marks any server draft that doesn't match its local
state as `is_deleted=true`. The server accepts that write.

The matching rule hinges on block content. If the server draft's
`blocks` array has at least one `rich_text` block whose `elements`
array is non-empty, Desktop recognizes it as kin and leaves it alone.
Without that, Desktop writes the tombstone. From the outside, it looks
like the draft vanishes from the user's side within seconds of them
opening the panel.

The test matrix that pinned this down - same auth, same destination,
same `user_ids`, only blocks and the carrier vary:

| Blocks                | Host              | UA     | Carrier    | Survives nav? |
|-----------------------|-------------------|--------|------------|---------------|
| rich_text             | slack.com         | Chrome | standalone | yes           |
| rich_text             | workspace.ent     | SSB    | slack-cli  | yes           |
| section+mrkdwn        | slack.com         | Chrome | standalone | no            |
| section+mrkdwn        | workspace.ent     | SSB    | slack-cli  | no            |
| section+plain_text    | workspace.ent     | SSB    | slack-cli  | no            |
| divider only (2026-05) | workspace.ent    | SSB    | slack-cli  | no (superseded by composer-authored divider support observed 2026-11-06: yes, alive after 100s) |
| header only (2026-05) | workspace.ent     | SSB    | slack-cli  | no (superseded: yes on 2026-11-06, see Header blocks) |
| context only          | workspace.ent     | SSB    | slack-cli  | no            |
| [section, divider] (2026-05) | workspace.ent | SSB   | slack-cli  | no (superseded 2026-11-06: a divider alone is renderable, see Divider blocks) |
| [rich_text, section]  | workspace.ent     | SSB    | slack-cli  | yes           |
| [section, rich_text]  | workspace.ent     | SSB    | slack-cli  | yes           |
| [rich_text, divider]  | workspace.ent     | SSB    | slack-cli  | yes           |
| rich_text, empty elements | workspace.ent | SSB    | slack-cli  | no            |
| rich_text, single space text | workspace.ent | SSB | slack-cli | no            |
| rich_text, `"x"` text | workspace.ent     | SSB    | slack-cli  | yes           |

Presence of one non-empty `rich_text` block, anywhere in the array, was
the hinge when this matrix was measured; `header` (2026-11-06) and
`divider` (2026-11-06) now count too.

## How to repro

Terminal, Slack Desktop running, no existing draft to self-DM:

```bash
echo '[{"type":"section","text":{"type":"mrkdwn","text":"ghost-me"}}]' \
  | curl -sX POST 'https://slack.com/api/drafts.create' \
    -H "Authorization: Bearer $SLACK_XOXC" \
    -H "Cookie: d=$SLACK_XOXD" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode 'destinations=[{"channel_id":"D09SELF"}]' \
    --data-urlencode "client_msg_id=$(uuidgen | tr '[:upper:]' '[:lower:]')" \
    --data-urlencode 'file_ids=[]' \
    --data-urlencode 'is_from_composer=false' \
    --data-urlencode "token=$SLACK_XOXC" \
    --data-urlencode 'blocks=@/dev/stdin'
```

Capture the `id` from the response, open the Drafts panel in Desktop
(Cmd+K, type "drafts", Enter), then list:

```bash
curl -sX POST 'https://slack.com/api/drafts.list' \
  -H "Authorization: Bearer $SLACK_XOXC" \
  -H "Cookie: d=$SLACK_XOXD" \
  --data-urlencode "token=$SLACK_XOXC" \
  --data-urlencode 'limit=5' \
  | jq '.drafts[] | {id, is_deleted, last_updated_ts, last_updated_client}'
```

`is_deleted` flips to `true` within a few seconds and `last_updated_ts`
moves forward to the time Desktop wrote the tombstone.
`last_updated_client` is not evidence of who wrote: with a
desktop-extracted session token the CLI's own writes are stamped
`"Slack SSB Mac (Atom)"` too (verified 2026-10-21 on a fresh CLI create
whose `last_updated_ts` equalled its creation time). The server derives
the stamp from the token's client, not from the request. Only
`last_updated_ts` moving after the CLI's last write shows a second
writer.

Swap the `section` block for a `rich_text` one and the draft survives
the same navigation indefinitely.

## The stripping bug

Surviving reconciliation is necessary but not sufficient. The test
matrix above has `[rich_text, section]` and `[section, rich_text]`
marked "Survives nav?: yes" - they don't tombstone. But they don't
render correctly either.

When the user opens a draft in Slack's Drafts compose editor (Desktop
or `app.slack.com`), the client reads the `blocks` array and rebuilds
a local representation for editing. That rebuild path only understands
`rich_text` and, since the composer grew its own headings and horizontal
rule (2026-11-06), `header` and `divider`. Every other top-level block
type - `section`, `context` - is dropped. The draft is still alive on the
server, but the user only sees the rich_text, header, and divider
content. Any `section` they'd
hoped to surface (mrkdwn prose) is gone. It doesn't come back when the
draft is subsequently sent.

Same investigation surfaced a second quirk: multiple top-level
`rich_text` blocks are **flattened** into one by the rebuild before
absorption runs. So splitting structure across separate top-level
rich_text blocks does not give you paragraph breaks - the flattened
stream is what the absorption rule applies to.

## The absorption rule

A `rich_text_section` followed directly by a `rich_text_list`,
`rich_text_preformatted`, or `rich_text_quote` gets its content
absorbed into the following container - **unless** the section's last
text inline ends with `\n`. Heading text glues onto the first bullet,
merges into the code block, or glues into the quote. The rule applies
across the flattened stream, so it spans top-level rich_text
boundaries.

Slack's own compose editor avoids the bug by emitting a trailing
newline on every section preceding one of these containers - see the
reference draft in `docs/draft-messages.md` history or capture a new
one yourself with the Chrome / web compose editor.

### Reproduction matrix

Captured 2026-05-15 against `drafts.create` + Slack web compose
editor. Each shape uses a single top-level `rich_text` unless noted.

| Shape                                                   | Section text  | Editor renders                                                |
|---------------------------------------------------------|---------------|---------------------------------------------------------------|
| section + rich_text_list                                | `"INTRO"`     | `• INTROlist-A` (section absorbed into first bullet)          |
| section + rich_text_list                                | `"INTRO\n"`   | `INTRO` paragraph, then `• list-A` `• list-B` (clean)         |
| section + rich_text_preformatted                        | `"PREFIX"`    | One code block reading `PREFIX code body` (section absorbed)  |
| section + rich_text_preformatted                        | `"PREFIX\n"`  | `PREFIX` paragraph, then code block (clean)                   |
| section + rich_text_quote                               | `"PREFIX"`    | `PREFIXquoted body` glued in the quote                        |
| section + rich_text_quote                               | `"PREFIX\n"`  | `PREFIX` paragraph, then quote (clean)                        |
| [rich_text(section), rich_text(list)] (multi top-level) | `"INTRO"`     | Flattens then absorbs - same `INTROlist-A` glue               |

The CLI's `validateBlocksShape` enforces the trailing-newline rule:
when a section is directly followed by an absorbing container, the
section's elements must terminate with a text inline whose `text` ends
with `\n`. Trailing empty text inlines (zero-length `text`) are
ignored so callers can append `{"type":"text","text":""}` without
defeating the check. Non-text trailing inlines (link, emoji, user,
channel, broadcast) don't carry the newline themselves; callers must
append a final text inline ending in `\n`.

### Reproduction

Same rule reproduces in the web client, not just Desktop. Create a
mixed draft via curl against `drafts.create`, open it in
`app.slack.com`, and inspect the compose editor DOM. Payload:

```json
[
  {"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"INTRO"}]}]},
  {"type":"section","text":{"type":"mrkdwn","text":"SECTION"}},
  {"type":"rich_text","elements":[{"type":"rich_text_list","style":"bullet","elements":[
    {"type":"rich_text_section","elements":[{"type":"text","text":"list-A"}]},
    {"type":"rich_text_section","elements":[{"type":"text","text":"list-B"}]}
  ]}]},
  {"type":"divider"},
  {"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"OUTRO"}]}]}
]
```

Observed in the compose editor:

- `SECTION` absent from both `innerText` and `innerHTML` of
  `[data-qa="message_input_container"]` - not hidden, truly absent.
- No `<hr>` in the DOM - divider also dropped. (Superseded by
  composer-authored divider support observed 2026-11-06: the composer
  now stores `divider` blocks itself, so the rebuild keeps them. Desktop
  rendering of a CLI-authored divider is pending a check.)
- One `<ul>` with two `<li>`s; the first reads
  `"INTROlist-A"` (intro rich_text absorbed into the following list's
  first item despite being a separate top-level block).
- `OUTRO` renders as its own paragraph after the list.

Unlike ghosting, there's no server-side signal for stripping. The
draft looks healthy in `drafts.list`. The only way to observe it is
to open the compose editor and read the DOM.

The CLI's response: `validateBlocksShape` rejects any draft whose
top-level `blocks` array contains anything other than `rich_text`,
and rejects sections directly preceding list/preformatted/quote
containers without a trailing `\n` (the absorption rule above).
Slack's API would accept the bad shape but the user would see
content loss.

## Tables

Tables ARE draftable - but the `table` block has to live in an
**attachment** (`attachments[].blocks[]`), never in top-level `blocks`.
In top-level blocks the compose editor strips it on open; in an
attachment it survives and renders as an editable Table card.

Verified 2026-05-29 against the live `drafts.*` API plus the
`app.slack.com` compose editor (DOM inspection), self-DM only. Both
`table` and `data_table` were tested end-to-end (created via raw
`drafts.create`, through reconciliation and reopen). They diverge:
`table` survives in an attachment and renders as an editable Table card,
but `data_table` (the interactive variant) is stripped from the compose
editor even in an attachment, and a data_table-only draft is tombstoned.
So `data_table` is NOT draftable - the CLI rejects it.

History: an earlier pass shipped the conclusion "tables can't be
drafted." It had only tried a `table` in *top-level blocks*, watched the
editor strip it, and stopped. The fix is the attachment location below.

| Payload                                         | create | Survives reconciliation  | Renders in editor    |
|-------------------------------------------------|--------|--------------------------|----------------------|
| `table` in top-level `blocks`                   | ok     | no (tombstoned if alone) | no - stripped        |
| `[rich_text, table]` (both top-level)           | ok     | yes (rich_text body)     | no - table stripped  |
| `table` in `attachments[].blocks[]` + rich_text | ok     | yes                      | yes - Table card     |
| `table` in `attachments[].blocks[]`, no blocks  | ok     | yes                      | yes - Table card     |
| `data_table` in `attachments[]` + rich_text     | ok     | yes (rich_text body)     | no - stripped        |
| `data_table` in `attachments[]`, no blocks      | ok     | no (tombstoned)          | n/a - gone           |
| `rich_text_preformatted` ASCII table            | ok     | yes                      | yes - monospace text |

How Slack itself does it: pasting tabular data into the composer (or
••• -> Format this message -> Create a Table) produces a `table` block
stored in the draft's `attachments[0].blocks[]` - not top-level blocks.
The composer's formatting *toolbar* has no table button, which is why
the earlier pass wrongly concluded "no table support"; the feature lives
behind the paste/format paths and stores into attachments.

Cells are `raw_text` (plain) or `rich_text` (bold/italic/link/...).
Slack's paste bolds the header row (rich_text cells) and leaves body
cells `raw_text`; there is no "header row" flag. `column_settings`
(`[{align, is_wrapped}]`) is accepted and stored.

Reconciliation survives on renderable content - a non-empty `rich_text`
block OR a table attachment. An attachments-only table draft (empty
`blocks`) does NOT tombstone: opening the Drafts panel repeatedly left
`is_deleted` false.

The CLI's own create path was verified live 2026-10-21 (self-DM, session
token, Desktop running): `draft create --markdown --table`, the same with
an eight-row five-column TSV and a body holding a mention, links, bold and
a list, and a table-only `draft create --table` with no stdin. In every
case the `drafts.create` response echoed `attachments`, `drafts.list`
returned the attachment immediately and again 30 to 50 seconds later, and
`is_deleted` stayed unset. A report the same day of a `--markdown --table`
draft to a private channel coming back with no `attachments` could not be
reproduced; the create response in that run was hidden by `--fields
id,channel`, and the draft was later rewritten by another writer (its
`last_updated_ts` moved after the CLI's last write) with the attachment
intact. If it recurs, read the draft back right after create with no
`--fields`, then again after opening the destination channel in Desktop.

CLI support: `drafts.create`/`update` carry an `attachments` form param.
The CLI accepts a stdin object `{"blocks":[...],"attachments":[...]}`, or
builds a table attachment from CSV/TSV via `--table`. `validateBlockShapes`
still rejects a `table` in top-level blocks (pointing the caller at
attachments); `validateAttachmentBlocks` restricts caller-supplied
attachment blocks to `table` (it rejects `data_table` - not draftable)
while round-tripping existing attachments verbatim. The tombstone
auto-replace path carries attachments across. See `cmd/draft.go` and
`skills/slack-cli/SKILL.md` "Tabular data".

The inline alternative (no attachment): a monospace ASCII table in a
`rich_text_preformatted` element - plain text, not an editable table,
but it needs no attachment.

## Red herrings

The previous investigation (visible as commit 80ed7ab, then reverted)
went deep on several dead ends. None of these matter. Listed here so
the next person doesn't repeat them.

`last_updated_client` server stamp. The server sets this based on UA
parsing. A plain Chrome UA stamps `"Chrome"`. A UA containing
`Slack_SSB/4.48.100` stamps `"Slack SSB Mac (Atom)"`. Neither stamp
controls reconciliation. Desktop checks block content, not the stamp.

Workspace subdomain routing. `<workspace>.enterprise.slack.com/api/*`
and `slack.com/api/*` behave identically for drafts.* calls. Earlier
experiments thought the subdomain made drafts survive. They did, but
only because those experiments used rich_text blocks.

`slack_route` query param (`?slack_route=<org>:<org>`). Used by the
web client. Omitting it does not ghost any drafts. Not required.

TLS ClientHello fingerprint. `refraction-networking/utls` with a
Chrome hello and Go's native `crypto/tls` both work. Neither is
special to drafts.

HTTP/2 vs HTTP/1.1. h2 is negotiated via ALPN by default. Forcing 1.1
results in EOF from the server, which is unrelated.

Header order in the HTTP/2 HPACK frame. Comparing `GODEBUG=http2debug=2`
dumps from the working standalone and the ghosting CLI showed
character-identical frames. Not the issue.

Connection keep-alive / connection reuse. Standalone always creates a
fresh TCP, CLI may or may not reuse - drafting behavior is identical
either way.

`_x_*` telemetry fields in the body (`_x_reason`, `_x_mode`,
`_x_sonic`, etc.). The web client attaches these. They have no effect
on survival.

The `client_last_updated_ts` decimal padding. Seven decimals matters
for `drafts.update` and `drafts.delete` (conflicts otherwise), but not
for ghosting.

`is_from_composer=true` vs `false`. No effect on survival.

Cookie source. The cookie stored by `slack auth login --desktop`
(extracted from Slack Desktop's Chromium cookie jar) and the cookie in
the user's shell env both work. They're different values but both valid.

None of the above affect the ghosting outcome. Every theory listed
here was tested with section-block drafts and the drafts still ghosted,
and with rich_text drafts and the drafts still survived.

## How we isolated it

Briefly, because the experimental path is instructive. Start with two
programs that make byte-identical HTTP requests (verified with a
local-capture TCP proxy): a standalone Go binary that survives nav, and
the slack-cli binary that ghosts. Same Go version, same token, same
cookie, same URL, same body, same headers. Outcomes diverge.

The difference turned out to be invisible at the HTTP layer because
the bodies were not actually identical. The standalone had a hardcoded
rich_text payload; the CLI sent whatever the skill docs said to use,
which at the time was "section+mrkdwn preferred for most cases." The
test harness had bias baked into the comparison.

Once I spotted that, the 2x2 was trivial:

- CLI + rich_text: survive
- CLI + section+mrkdwn: ghost
- standalone + rich_text: survive
- standalone + section+mrkdwn: ghost

From there, varying host / UA / TLS / order / headers all pointed the
same way - block content, nothing else.

## Implementation

### Validation at the CLI boundary

`validateBlockShapes` in `cmd/draft.go` rejects three shapes of draft
payload:

1. Arrays with no non-empty `rich_text` block, no `header`, and no
   `divider` (would ghost), unless the draft has a table attachment or a
   file.
2. Arrays containing a top-level block that is not `rich_text`, `header`,
   or `divider` (would ship fine but get stripped by Desktop's compose
   editor).
3. A `header` whose text is not non-empty `plain_text`, or whose `level`
   is not an integer of at least 1 (Slack answers `invalid_message`).

Both errors return `invalid_blocks` with a message pointing the caller
at the skill docs. The CLI never ships a draft that would ghost or
silently lose content.

`DraftUpdateCmd.buildIntent` runs the same check against the existing
draft's blocks when the user does a schedule-only update (no new
blocks piped in). A legacy draft whose blocks don't satisfy the
requirement fails fast with `invalid_blocks`, rather than round-trip
through `drafts.update` and ghost or strip again.

### Auto-replace

`DraftUpdateCmd.Run` detects `is_deleted=true` on the existing draft and
transparently calls `drafts.create` at the same destination, carrying the
draft's blocks, attachments, and `file_ids`. The new
draft lands on stdout with `replaced_draft_id` naming the old ID, and a
`warnTombstoned` warning lands on stderr, so the caller knows the ID changed.

When the lookup showed a live draft and `drafts.update` then fails, the
command lists the drafts a second time and branches on what it finds, not on
Slack's error string. `is_sent` is `draft_already_sent`. `is_deleted` (the
tombstone landed between the list and the update) takes the replacement
path. Neither, or a draft no longer in the list, surfaces Slack's error
unchanged.

A sent draft is never replaced. `is_sent` is checked first, on the lookup
and on the second look, so a draft that is both sent and deleted is
`draft_already_sent`. Before 2026-11-10 a sent draft went down the
replacement path and the command printed a new unsent draft to the same
destination, exit 0.

This path still exists as defense-in-depth. Our validation prevents
the CLI from creating ghost-prone drafts, but a second Slack client on
the same account - another Desktop, a browser tab - could tombstone a
well-formed draft during its own reconciliation. Nothing we can do
about that from the CLI side.

Replacing a tombstoned draft is the default and there is no flag to turn it
off (decided 2026-11-10). The old draft stays in `drafts.list` as a
tombstone. `drafts.delete` rejects it, and `draft delete` reports it as
`already_deleted`.

### Parked: why a well-formed CLI draft is tombstoned at all

Two reports on 2026-08-03 showed drafts tombstoned that passed
`validateBlockShapes`, so the block-shape rule above does not explain every
tombstone. Both were drafts to several `user_ids` whose group DM did not
exist yet. The untested hypothesis is that a `user_ids` draft to a
conversation that does not exist is tombstoned when a client reconciles it.

Testing it means creating a draft addressed to other users and comparing
`is_deleted` a minute later against a channel draft made in the same run.
The repo allows draft writes only in a self-DM, so the question is parked
until the owner runs that comparison himself. Two reports are a lead, not a
finding.

Also unmeasured: whether a tombstoned draft still shows in the Drafts panel
(if Desktop wrote the tombstone it should already hide it), and whether
`drafts.update` on a live draft ever returns a new id by itself. The stderr
`draft_tombstoned` warning says the draft was tombstoned "by Slack Desktop
(likely viewed there)", which is a guess, not something the CLI observed.

## If Slack changes the API

Symptoms to watch for:

- Drafts consistently ghost within seconds of Drafts-panel nav despite
  our rich_text validation passing. The reconciliation rule changed.

- A mixed-block draft (staged via curl to bypass CLI validation)
  suddenly renders `section` / `context` content in the compose editor.
  The strip rule relaxed; our validation is now unnecessarily strict.
  This happened for `header` and `divider` on 2026-11-06.

- `drafts.create` rejects previously-valid payloads
  (`invalid_arg_name`, new required fields, etc.). The wire format
  drifted.

- `drafts.list` starts returning a cursor. Assume pagination is real;
  wire it in.

Diagnostic steps, in order:

1. Reproduce with curl. If curl survives but the CLI ghosts, it's our
   bug - check commits that touched `cmd/draft.go` or
   `internal/api/client.go` since this doc.

2. Reproduce with two flavors of blocks: pure rich_text and
   section+mrkdwn. If both survive, Desktop relaxed the reconciliation
   rule; our validation is now unnecessarily strict. If both ghost,
   the rule tightened further and we need a new experiment.

3. Check `last_updated_ts` on a ghosted draft against the CLI's last
   write. A later value means another client wrote the tombstone.
   `last_updated_client` cannot tell the CLI from Desktop when the token
   came from Desktop (see above).

4. If Desktop stopped reconciling block content and started keying on
   something else, try: `is_from_composer=true` on create; different
   `client_msg_id` formats; draft content length; block order; file
   attachments; thread parent. Run the matrix.

5. Watch the Slack Desktop app's dev tools network tab while opening
   the Drafts panel. The request that drives reconciliation is
   identifiable; if its shape changed, our assumptions about the rule
   changed with it.

## References

Source:

- `cmd/draft.go` - `DraftCreateCmd`, `DraftUpdateCmd`, `DraftDeleteCmd`,
  `validateBlockShapes`, `padDraftTS`, `normalizeDestinationsForWrite`,
  auto-replace path.
- `skills/slack-cli/SKILL.md` - rich_text schema docs shipped to agents.
- `internal/api/client.go` - `PostInternalForm`, the shared internal-API
  HTTP path.

Commits:

- `52c7631` - the real fix: rich_text validation, revert of the
  workspace-subdomain plumbing.
- `87e642c` - review-round fixes: schedule-only validation guard, test
  accuracy, drop alias.
- `95d6fc0` - stale-comment cleanup.
- `80ed7ab` (before the fix) - the prior attempt that theorized
  host/UA; kept in history as the counterpoint.
