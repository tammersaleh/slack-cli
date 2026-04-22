# Drafts

Slack's `drafts.*` endpoints are an undocumented internal API that the
Slack Desktop client uses to persist unsent messages across devices.
`slack-cli` wraps them so an agent can stage a message for a human to
review and send.

This document is more detailed than usual because getting the behavior
right took a long investigation. The summary up front; the receipts
below.

## TL;DR

Use rich_text blocks, only rich_text blocks, and for multi-paragraph
prose with bullets use a single `rich_text_section`. Everything else is
a trap.

```json
[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello"}]}]}]
```

Two independent failure modes:

1. **Tombstone.** Slack Desktop sets `is_deleted=true` on any
   server-side draft whose `blocks` array doesn't contain at least one
   `rich_text` block with non-empty `elements`. Lands within seconds
   of the user opening the Drafts panel.
2. **Strip.** Slack Desktop's Drafts compose editor (the panel the
   user opens to review and send) silently discards every top-level
   block that is not `rich_text` when it renders the draft. A
   `[rich_text, section, divider]` array ships fine to the API but the
   user only sees the rich_text content. Not reversible - once
   rendered for edit, the stripped content never comes back.

Block type is the only variable that matters. Host, user-agent, TLS
fingerprint, and the `last_updated_client` stamp are all irrelevant.

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
- `limit` - page size, observed max ~100. Required in practice.
- `is_active` - `"true"` to exclude already-sent and scheduled-past drafts.

Response:

```json
{"ok": true, "drafts": [...], "has_more": false}
```

No cursor pagination. `has_more` indicates there are more drafts, but
there is no documented way to reach them. Raise `limit` to get more in
one call.

### drafts.create

Same URL pattern, method `drafts.create`. Body fields:

- `token`
- `blocks` - Block Kit JSON array; must include rich_text (see below).
- `destinations` - JSON array of one destination object. Example:
  `[{"channel_id":"D09..."}]` or `[{"channel_id":"C01...","thread_ts":"1709.1"}]`.
- `client_msg_id` - lowercase UUID v4. Other formats fail with `invalid_client_msg_id`.
- `file_ids` - JSON array, `"[]"` when no attachments.
- `is_from_composer` - `"false"` from the API. Desktop sends `"true"`.
- `date_scheduled` - optional Unix seconds for scheduled send.

Response:

```json
{"ok": true, "draft": {...}}
```

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
(`is_deleted=true`). The CLI treats this as a signal to auto-replace:
create a fresh draft at the same destination and return the new ID.

### drafts.delete

Body fields:

- `token`
- `draft_id`
- `client_last_updated_ts` (padded, same as update)

Errors: `draft_delete_invalid` when already tombstoned - same
auto-replace path applies.

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
  "client_last_updated_ts": "1776538579.2033250",
  "last_updated_client": "Slack SSB Mac (Atom)",
  "blocks": [...],
  "file_ids": [],
  "is_from_composer": false,
  "is_deleted": false,
  "is_sent": false,
  "destinations": [{"channel_id": "D09...", "user_ids": ["U..."]}]
}
```

Slack reflects DM destinations back with both `channel_id` and
`user_ids`. When you update or delete, strip `user_ids` from
destinations that also have `channel_id`, or the API rejects with
`both_user_ids_and_channel_id_provided_in_destination`. See
`normalizeDestinationsForWrite` in `cmd/draft.go`.

## Auth

Cookie auth only. Any xoxc- session token plus the matching `d` cookie
works. A plain Chrome UA or Go's default UA is fine. Enterprise Grid
needs the E-prefix org-level token (saved via `slack auth login
--desktop` with `SLACK_WORKSPACE_ORG` set); the T-prefix workspace
token returns `team_is_restricted`.

No special routing is needed. `slack.com/api/drafts.*` works from any
network that can reach Slack. No workspace subdomain, no
`slack_route` query param, no Slack_SSB user-agent suffix.

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
| divider only          | workspace.ent     | SSB    | slack-cli  | no            |
| header only           | workspace.ent     | SSB    | slack-cli  | no            |
| context only          | workspace.ent     | SSB    | slack-cli  | no            |
| [section, divider]    | workspace.ent     | SSB    | slack-cli  | no            |
| [rich_text, section]  | workspace.ent     | SSB    | slack-cli  | yes           |
| [section, rich_text]  | workspace.ent     | SSB    | slack-cli  | yes           |
| [rich_text, divider]  | workspace.ent     | SSB    | slack-cli  | yes           |
| rich_text, empty elements | workspace.ent | SSB    | slack-cli  | no            |
| rich_text, single space text | workspace.ent | SSB | slack-cli | no            |
| rich_text, `"x"` text | workspace.ent     | SSB    | slack-cli  | yes           |

Presence of one non-empty `rich_text` block, anywhere in the array, is
the hinge.

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

`is_deleted` flips to `true` within a few seconds; `last_updated_ts`
moves forward to the time Desktop wrote the tombstone; and
`last_updated_client` switches to `"Slack SSB Mac (Atom)"` - the
signature of Desktop writing to the server, not us.

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
`rich_text`. Every other top-level block type - `section`, `divider`,
`header`, `context` - is dropped. The draft is still alive on the
server, but the user only sees the rich_text content. Any `section`
they'd hoped to surface (headings, mrkdwn prose) is gone. It doesn't
come back when the draft is subsequently sent.

Same investigation surfaced a second quirk: multiple top-level
`rich_text` blocks are **flattened** into one by the rebuild before
the section-before-list absorption rule (see layout quirks in the
skill doc) runs. So splitting an alternating heading-then-list into
separate top-level rich_text blocks does not give you paragraph
breaks - it gives you the same "GPUs:" glued onto the first bullet as
a single mixed block. The fix is the single-`rich_text_section`
pattern with inline `\n` and literal `•`, which sidesteps absorption
entirely.

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
- No `<hr>` in the DOM - divider also dropped.
- One `<ul>` with two `<li>`s; the first reads
  `"INTROlist-A"` (intro rich_text absorbed into the following list's
  first item despite being a separate top-level block).
- `OUTRO` renders as its own paragraph after the list.

Swap the whole thing for a single top-level `rich_text` with one
`rich_text_section` using inline `\n` and literal `•` characters, and
every marker renders.

Unlike ghosting, there's no server-side signal for stripping. The
draft looks healthy in `drafts.list`. The only way to observe it is
to open the compose editor and read the DOM.

The CLI's response: `validateBlocksShape` rejects any draft whose
top-level `blocks` array contains anything other than `rich_text`.
Slack's API would accept them but the user would see content loss.

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

`validateBlocksShape` in `cmd/draft.go` rejects two shapes of draft
payload:

1. Arrays with no non-empty `rich_text` block (would ghost).
2. Arrays containing any non-`rich_text` top-level block (would ship
   fine but get stripped by Desktop's compose editor).

Both errors return `invalid_blocks` with a message pointing the caller
at the skill docs. The CLI never ships a draft that would ghost or
silently lose content.

`DraftUpdateCmd.buildIntent` runs the same check against the existing
draft's blocks when the user does a schedule-only update (no new
blocks piped in). A legacy draft whose blocks don't satisfy the
requirement fails fast with `invalid_blocks`, rather than round-trip
through `drafts.update` and ghost or strip again.

### Auto-replace

`DraftUpdateCmd.Run` detects `is_deleted=true` on the existing draft
(or `draft_update_invalid` from the server response, which indicates a
race where the tombstone landed between our list and update) and
transparently calls `drafts.create` at the same destination. The new
draft's ID lands on stdout. A `warnTombstoned` warning lands on
stderr so the caller knows the ID changed.

This path still exists as defense-in-depth. Our validation prevents
the CLI from creating ghost-prone drafts, but a second Slack client on
the same account - another Desktop, a browser tab - could tombstone a
well-formed draft during its own reconciliation. Nothing we can do
about that from the CLI side.

## If Slack changes the API

Symptoms to watch for:

- Drafts consistently ghost within seconds of Drafts-panel nav despite
  our rich_text validation passing. The reconciliation rule changed.

- A mixed-block draft (staged via curl to bypass CLI validation)
  suddenly renders `section` / `divider` content in the compose editor.
  The strip rule relaxed; our validation is now unnecessarily strict.

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

3. Check `last_updated_client` on a ghosted draft. `"Slack SSB Mac
   (Atom)"` means Desktop did the write. Anything else means the
   server tombstoned it itself, which would be a different bug class.

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
  `validateBlocksShape`, `padDraftTS`, `normalizeDestinationsForWrite`,
  auto-replace path.
- `cmd/skill.go` - rich_text schema docs shipped to agents via the
  generated `SKILL.md`.
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
