package cmd

import (
	"fmt"
	"io"
	"os"
)

type SkillCmd struct {
	Binary string `help:"Path to the slack binary for allowed-tools." default:""`
}

func (c *SkillCmd) Run(cli *CLI) error {
	binary := c.Binary
	if binary == "" {
		if exe, err := os.Executable(); err == nil {
			binary = exe
		} else {
			binary = "slack"
		}
	}

	out := io.Writer(os.Stdout)
	if cli.out != nil {
		out = cli.out
	}

	_, err := fmt.Fprintf(out, skillTemplate, binary)
	return err
}

const skillTemplate = `---
name: slack-cli
description: "Read and manage Slack workspaces: messages, channels, users, files, saved items, sidebar sections"
argument-hint: ""
allowed-tools:
  - Bash(%s *)
---

# Slack CLI

CLI for Slack. JSONL output (one JSON object per line). All commands end with
a ` + "`_meta`" + ` trailer: ` + "`" + `{"_meta":{"has_more":false}}` + "`" + `.

Auth is pre-configured. If you get ` + "`not_authed`" + `, tell the user to run
` + "`slack auth login`" + `.

## Output

Every command writes JSONL to stdout. Filter fields with ` + "`--fields id,name`" + `.
Suppress stdout with ` + "`--quiet`" + `. Errors go to stderr as JSON.

## Errors

Every fatal error on stderr is one JSON object:

- ` + "`error`" + ` - stable code (e.g. ` + "`channel_not_found`" + `)
- ` + "`detail`" + ` - human-readable context
- ` + "`hint`" + ` - runnable recovery command, when available. **Read this and do what it says.**
- ` + "`input`" + ` - the specific input that failed (channel, user, ts, draft id, ...)
- ` + "`endpoint`" + ` - Slack API endpoint, only on upstream API failures

Common errors and their recovery:

| ` + "`error`" + ` | What to run next |
|---|---|
| ` + "`not_authed`" + ` | ` + "`slack auth login --desktop`" + ` (or ` + "`slack auth login`" + ` for OAuth) |
| ` + "`channel_not_found`" + ` | ` + "`slack channel list --query <partial>`" + `; add ` + "`--include-non-member`" + ` for channels you haven't joined |
| ` + "`user_not_found`" + ` | ` + "`slack user list --query <partial>`" + ` or ` + "`slack user info <id-or-email>`" + ` |
| ` + "`draft_not_found`" + ` | ` + "`slack draft list`" + ` (add ` + "`--include-sent`" + ` / ` + "`--include-deleted`" + ` for hidden ones) |
| ` + "`section_not_found`" + ` | ` + "`slack section list`" + ` |
| ` + "`thread_not_found`" + ` | ` + "`slack message list <channel> --has-replies`" + ` to find threads |
| ` + "`invalid_timestamp`" + ` | RFC 3339, ` + "`YYYY-MM-DD`" + `, or raw Slack ts (` + "`1713300000.123456`" + `). Match the ` + "`hint`" + `. |
| ` + "`invalid_blocks`" + ` / ` + "`missing_blocks`" + ` | Block Kit JSON on stdin; drafts require only ` + "`rich_text`" + ` top-level blocks. See draft docs below. |
| ` + "`rate_limited`" + ` | Retry after the delay Slack provided |

Per-item errors in bulk commands (e.g. ` + "`slack channel info X Y Z`" + `)
go to stdout inline as ` + "`{\"input\":..., \"error\":..., \"detail\":..., \"hint\":...}`" + `
rows and set ` + "`_meta.error_count`" + ` in the trailer. Exit code is 1.

### Exit codes

- 0 - success
- 1 - general error (including partial failure in bulk commands)
- 2 - authentication required / not authed
- 3 - rate limited
- 4 - network error

## Commands

### Messages

` + "```" + `
slack message list <channel> [--limit N] [--after TS] [--before TS] [--has-replies] [--has-reactions]
slack message get <channel> <ts>...
slack message permalink <channel> <ts>...
` + "```" + `

Time bounds accept RFC 3339, ` + "`YYYY-MM-DD`" + `, or raw Slack ts.
` + "`--unread`" + ` uses the channel's last_read marker (mutually exclusive
with ` + "`--after`" + `). ` + "`--has-replies`" + ` is a client-side filter - use it
to find threads worth exploring with ` + "`slack thread list`" + `.

Examples:

` + "```" + `
slack message list '#general' --limit 50
slack message list '#general' --after 2026-04-01 --before 2026-04-15
slack message list '#general' --has-replies --fields ts,user,reply_count
` + "```" + `

### Threads

` + "```" + `
slack thread list <channel> <ts> [--limit N]
` + "```" + `

Pair with ` + "`slack message list --has-replies`" + ` to locate a thread root
first. ` + "`ts`" + ` is the parent message's timestamp (not a reply's).

### Search (requires user token)

` + "```" + `
slack search messages <query> [--limit N] [--sort timestamp|score] [--sort-dir asc|desc]
slack search files <query> [--limit N]
` + "```" + `

Query supports Slack's search modifiers. Combine freely:

- ` + "`in:#channel`" + ` - only this channel (also works with names, ` + "`in:@user`" + ` for DMs)
- ` + "`from:@user`" + ` - messages posted by this user
- ` + "`to:@user`" + ` - DMs to this user
- ` + "`after:YYYY-MM-DD`" + ` / ` + "`before:YYYY-MM-DD`" + ` / ` + "`on:YYYY-MM-DD`" + ` / ` + "`during:month`" + `
- ` + "`has:link`" + ` / ` + "`has:pin`" + ` / ` + "`has:reaction`" + ` / ` + "`has:file`" + ` / ` + "`has:image`" + `
- ` + "`is:thread`" + ` - messages in threads; ` + "`is:saved`" + ` / ` + "`is:dm`" + ` / ` + "`is:mpdm`" + `

Example: ` + "`\"deploy blocker in:#general from:@alice after:2026-01-01\"`" + `

Results are ranked by score unless ` + "`--sort timestamp`" + `. Each hit
includes ` + "`channel`" + ` (object with ` + "`id`" + `/` + "`name`" + `), ` + "`user`" + `, ` + "`ts`" + `,
` + "`text`" + `, and ` + "`permalink`" + `.

### Channels

` + "```" + `
slack channel list [--limit N] [--type public|private|mpim|im] [--query STR] [--include-non-member]
slack channel info <channel>...
slack channel members <channel> [--limit N]
` + "```" + `

Defaults to channels you're a member of. Add ` + "`--include-non-member`" + `
to expand.

Examples:

` + "```" + `
slack channel list --query ext-                        # find customer channels
slack channel list --type private --has-unread         # private + unread
slack channel list --include-non-member --all          # workspace-wide
slack channel info '#general' --fields id,name,topic   # narrowed info
` + "```" + `

### Users

` + "```" + `
slack user list [--limit N] [--query STR] [--presence]
slack user info <user>...
` + "```" + `

User arguments accept IDs (` + "`U...`" + `), emails, or ` + "`@name`" + ` (display
name, username, or real name). On Enterprise Grid with a session
token, email lookup may fail - prefer ` + "`@name`" + ` there.

Examples:

` + "```" + `
slack user list --query tamm                      # find by partial name
slack user info U09T3DUS6P9                       # by ID
slack user info alice@example.com bob@example.com  # bulk by email
slack user info @alice                            # by display name
` + "```" + `

### Files

` + "```" + `
slack file list [--limit N] [--channel CH] [--user UID] [--types images,pdfs]
slack file info <file-id>...
slack file download <file-id> [-o path]
` + "```" + `

### Reactions

` + "```" + `
slack reaction list <channel> <ts>...
` + "```" + `

### Pins and Bookmarks

` + "```" + `
slack pin list <channel>
slack bookmark list <channel>
` + "```" + `

### Saved Items (requires session token)

` + "```" + `
slack saved list [--limit N] [--enrich] [--include-completed]
slack saved counts
` + "```" + `

### Sidebar Sections (requires session token)

` + "```" + `
slack section list
slack section channels <section-id>
slack section find <pattern>
slack section move --channels ID,ID --section ID
slack section move --channels ID,ID --new-section "Name"
slack section create <name>
` + "```" + `

### Drafts (requires session token)

Stage unsent messages. The CLI never sends - the human sends from the
Slack app. Block Kit JSON is always piped on stdin; there is no
plain-text shortcut.

` + "```" + `
slack draft list [--active] [--include-sent] [--include-deleted] [--limit N]
slack draft create <channel> [--thread TS [--broadcast]] [--at RFC3339] < blocks.json
slack draft update <draft-id> [--at RFC3339] [--clear-schedule] [< blocks.json]
slack draft delete <draft-id>...
` + "```" + `

#### Block Kit for drafts

Drafts must contain only ` + "`rich_text`" + ` top-level blocks, and at
least one must have non-empty ` + "`elements`" + `. Two Slack Desktop
behaviors force this:

- **Tombstone**: the Drafts-panel reconciliation sets ` + "`is_deleted=true`" + `
  on any server-side draft that lacks a non-empty rich_text, so
  section-only or divider-only arrays round-trip as deleted within
  seconds.
- **Strip**: the Drafts compose editor (the panel the user opens to
  review/send) silently discards any top-level block that is not
  ` + "`rich_text`" + ` when it renders the draft for editing. A
  ` + "`[rich_text, section, divider]`" + ` array ships fine to the API but
  the user only sees the rich_text content - everything else is gone.

The CLI rejects non-rich_text top-level blocks up front with
` + "`invalid_blocks`" + `. If you want something that would normally be a
` + "`section`" + ` or ` + "`header`" + ` (bold headings, mrkdwn prose), express it
inside a rich_text block.

##### rich_text (structured)

Shape:

` + "```" + `
{type:"rich_text", elements:[ <container>, ... ] }

<container> ∈ rich_text_section | rich_text_list | rich_text_quote | rich_text_preformatted
<inline>    ∈ text | link | user | usergroup | channel | emoji | date | broadcast
` + "```" + `

Minimal rich_text (plain text):

` + "```json" + `
[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello"}]}]}]
` + "```" + `

##### Container elements

**rich_text_section** - one paragraph. ` + "`elements`" + ` = array of inline.

` + "```json" + `
{"type":"rich_text_section","elements":[{"type":"text","text":"a sentence"}]}
` + "```" + `

**rich_text_list** - bullet or ordered list. ` + "`elements`" + ` = array of
rich_text_section (each one is a list item).

` + "```json" + `
{"type":"rich_text_list","style":"bullet","indent":0,"elements":[
  {"type":"rich_text_section","elements":[{"type":"text","text":"item one"}]},
  {"type":"rich_text_section","elements":[{"type":"text","text":"item two"}]}
]}
` + "```" + `

- ` + "`style`" + ` is ` + "`bullet`" + ` or ` + "`ordered`" + `.
- ` + "`indent`" + ` is 0, 1, 2 for nesting depth.
- ` + "`border`" + ` is 0 (none) or 1 (thin left border).

Nesting: a rich_text_list cannot contain another rich_text_list in its
` + "`elements`" + `. To nest, emit SEPARATE rich_text_list blocks with
increasing ` + "`indent`" + ` values. Slack renders level 0 as solid ` + "`●`" + `,
level 1 as hollow ` + "`○`" + `, level 2 as filled square ` + "`■`" + `.

` + "```json" + `
[
  {"type":"rich_text_list","style":"bullet","indent":0,"elements":[
    {"type":"rich_text_section","elements":[{"type":"text","text":"parent"}]}
  ]},
  {"type":"rich_text_list","style":"bullet","indent":1,"elements":[
    {"type":"rich_text_section","elements":[{"type":"text","text":"child"}]}
  ]},
  {"type":"rich_text_list","style":"bullet","indent":2,"elements":[
    {"type":"rich_text_section","elements":[{"type":"text","text":"grandchild"}]}
  ]}
]
` + "```" + `

**rich_text_quote** - blockquote, renders with a left border. Same
` + "`elements`" + ` shape as rich_text_section.

` + "```json" + `
{"type":"rich_text_quote","elements":[{"type":"text","text":"quoted passage"}]}
` + "```" + `

**rich_text_preformatted** - code block. ` + "`elements`" + ` is inline (usually a
single ` + "`text`" + ` element). ` + "`border`" + ` 0 or 1.

` + "```json" + `
{"type":"rich_text_preformatted","elements":[{"type":"text","text":"go run main.go"}]}
` + "```" + `

##### Inline elements

**text** - with optional ` + "`style`" + ` for bold/italic/strike/code.

` + "```json" + `
{"type":"text","text":"plain"}
{"type":"text","text":"strong","style":{"bold":true}}
{"type":"text","text":"code","style":{"code":true}}
{"type":"text","text":"both","style":{"bold":true,"italic":true,"strike":true}}
` + "```" + `

**link** - URL with optional anchor text; ` + "`style`" + ` same as text.

` + "```json" + `
{"type":"link","url":"https://example.com","text":"Example"}
{"type":"link","url":"https://example.com"}
` + "```" + `

**user** - mention, no text content.

` + "```json" + `
{"type":"user","user_id":"U01ABC123"}
` + "```" + `

**usergroup** - group mention.

` + "```json" + `
{"type":"usergroup","usergroup_id":"S01ABC123"}
` + "```" + `

**channel** - channel link mention.

` + "```json" + `
{"type":"channel","channel_id":"C01ABC123"}
` + "```" + `

**emoji** - named emoji. Custom workspace emojis work if they exist.

` + "```json" + `
{"type":"emoji","name":"thumbsup"}
{"type":"emoji","name":"partyparrot"}
` + "```" + `

**date** - server-rendered timestamp. ` + "`timestamp`" + ` is Unix seconds.
` + "`format`" + ` uses tokens like ` + "`{date_short}`" + `, ` + "`{date_long}`" + `, ` + "`{time}`" + `.
` + "`fallback`" + ` is what renders if format fails.

` + "```json" + `
{"type":"date","timestamp":1800000000,"format":"{date_short} at {time}","fallback":"soon"}
` + "```" + `

Note: draft previews in Slack Desktop show the fallback, not the formatted
date. The formatted version appears only after the draft is sent. Use a
fallback that reads naturally.

**broadcast** - ` + "`@here`" + `, ` + "`@channel`" + `, ` + "`@everyone`" + `.

` + "```json" + `
{"type":"broadcast","range":"here"}
` + "```" + `

##### Complete example

Thread reply with bold, a link, a bullet list with code, and a mention:

` + "```json" + `
[
  {
    "type": "rich_text",
    "elements": [
      {
        "type": "rich_text_section",
        "elements": [
          {"type": "user", "user_id": "U01ABC123"},
          {"type": "text", "text": " please review "},
          {"type": "link", "url": "https://github.com/org/repo/pull/42", "text": "PR #42"},
          {"type": "text", "text": " - changes:"}
        ]
      },
      {
        "type": "rich_text_list",
        "style": "bullet",
        "indent": 0,
        "elements": [
          {"type": "rich_text_section", "elements": [
            {"type": "text", "text": "Added "},
            {"type": "text", "text": "handleError()", "style": {"code": true}},
            {"type": "text", "text": " helper"}
          ]},
          {"type": "rich_text_section", "elements": [
            {"type": "text", "text": "Dropped legacy JSON path"}
          ]}
        ]
      },
      {
        "type": "rich_text_preformatted",
        "elements": [{"type": "text", "text": "git checkout feat/handler\nmise run test"}]
      }
    ]
  }
]
` + "```" + `

##### Layout quirks

A ` + "`rich_text_list`" + ` that immediately follows one or more
` + "`rich_text_section`" + ` elements **absorbs the last section as the first
list item's content**, gluing the heading text onto the first bullet
with no break.

Bad (intro collapses into first bullet):

` + "```json" + `
[{"type":"rich_text","elements":[
  {"type":"rich_text_section","elements":[{"type":"text","text":"Next steps:"}]},
  {"type":"rich_text_list","style":"bullet","elements":[
    {"type":"rich_text_section","elements":[{"type":"text","text":"do X"}]}
  ]}
]}]
` + "```" + `

Renders as a single bullet reading "Next steps:do X".

This only affects section-BEFORE-list. Section-after-list renders as
its own paragraph. Also: consecutive sections with no list between
them do flow inline with no paragraph break, so if you want separate
paragraphs you need intervening structural elements (list, quote,
preformatted).

**Splitting into multiple top-level ` + "`rich_text`" + ` blocks does not help.**
Slack Desktop's Drafts compose editor flattens every top-level
rich_text block into one before rendering, then applies the
section-before-list absorption across the merged stream. An array of
alternating ` + "`rich_text(section)`" + ` / ` + "`rich_text(list)`" + ` blocks
collapses into the same glued-together mess as a single mixed block.

Options to get a heading above a list:

- Put the heading INSIDE the list as its own rich_text_section item
  (accept that it shows as a bullet).
- Use a ` + "`rich_text_quote`" + ` or ` + "`rich_text_preformatted`" + ` between
  heading and list; either forces a block break.
- Drop the heading entirely and use a bolded first bullet.
- Use the single-section pattern below - it sidesteps absorption
  entirely.

##### Multi-paragraph prose with visual bullets

When a draft needs distinct paragraphs, bold headings, and bulleted
lists mixed together, ` + "`rich_text_list`" + ` is a trap: every
heading-then-list boundary risks absorption, and extra top-level
rich_text blocks don't rescue it.

The reliable pattern is **one top-level ` + "`rich_text`" + ` block
containing one ` + "`rich_text_section`" + `**, with inline ` + "`text`" + `
elements doing the structural work: ` + "`\\n`" + ` for line breaks,
` + "`\\n\\n`" + ` for paragraph gaps, and a literal Unicode ` + "`•`" + ` for
bullet markers.

` + "```json" + `
[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[
  {"type":"user","user_id":"U01ABC123"},
  {"type":"text","text":" Quick brief on X:\n\n"},
  {"type":"text","text":"• First bullet\n"},
  {"type":"text","text":"• Second bullet\n\n"},
  {"type":"text","text":"GPUs:","style":{"bold":true}},
  {"type":"text","text":"\n• Third bullet\n"}
]}]}]
` + "```" + `

Tradeoffs: the ` + "`•`" + ` characters are plain text, not real list
markers, so indent/nesting won't match a native list if the recipient
edits it. Inline styling (` + "`bold`" + `, ` + "`italic`" + `, ` + "`link`" + `, ` + "`user`" + `,
` + "`channel`" + `, ` + "`emoji`" + `) works fine across the whole section.
Paragraph spacing is controlled by ` + "`\\n\\n`" + ` rather than block
breaks, so the result reads like a typed-in Slack message.

Use ` + "`rich_text_list`" + ` only when the draft is a pure list with no
preceding prose.

##### Validation

The CLI checks: non-empty JSON array, every top-level block is a
` + "`rich_text`" + ` with a string ` + "`type`" + `, and at least one has a
non-empty ` + "`elements`" + ` array. Non-rich_text blocks fail locally with
` + "`invalid_blocks`" + ` - Slack's API would accept them but Desktop
would strip (or tombstone) the draft. Semantic errors inside blocks
(missing required subfields, unknown inline types, malformed style
objects) surface as ` + "`invalid_blocks`" + ` from Slack's API, not
locally.

### User State

` + "```" + `
slack status get [user...]
slack presence get <user>...
slack dnd info <user>...
` + "```" + `

### Other

` + "```" + `
slack emoji list [--query STR]
slack usergroup list [--include-disabled] [--query STR]
slack usergroup members <group-id>
slack workspace-info info
` + "```" + `

## Workflows

Compose commands to solve typical agent tasks. Use ` + "`jq`" + ` to thread IDs between steps.

### Find a user, get their recent messages in a channel

` + "```" + `
UID=$(slack user info @alice | jq -r 'select(._meta == null) | .id')
slack message list '#general' --limit 50 | jq -c "select(.user == \"$UID\")"
` + "```" + `

### Find a thread by content and read the whole thread

` + "```" + `
HIT=$(slack search messages "Q3 roadmap in:#general" --limit 1 | jq -c 'select(._meta == null)')
CH=$(echo "$HIT" | jq -r '.channel.id')
TS=$(echo "$HIT" | jq -r '.ts')
slack thread list "$CH" "$TS"
` + "```" + `

### Stage a draft reply to a message you found

` + "```" + `
HIT=$(slack search messages "deploy blocker in:#ext-acme" --limit 1 | jq -c 'select(._meta == null)')
CH=$(echo "$HIT" | jq -r '.channel.id')
TS=$(echo "$HIT" | jq -r '.ts')
cat <<JSON | slack draft create "$CH" --thread "$TS"
[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[
  {"type":"text","text":"Noted - following up by Friday."}
]}]}]
JSON
` + "```" + `

### Find a 1:1 DM channel with a user

` + "`slack channel list --type im`" + ` currently returns empty pages. Use search instead:

` + "```" + `
CH=$(slack search messages "from:@alice" --limit 1 | jq -r 'select(._meta == null) | .channel.id')
` + "```" + `

### Recover from a ` + "`channel_not_found`" + `

When an error has a ` + "`hint`" + ` field, follow it. Example:

` + "```" + `
slack message list '#ext-acm' 2>&1 >/dev/null | jq -r '.hint'
# → "Run 'slack channel list --query ext-acm' to find ... or '--include-non-member' ..."
slack channel list --query ext-acm
` + "```" + `

## Enrichment

Output automatically includes resolved names alongside IDs:

- ` + "`user`" + ` fields gain a ` + "`user_name`" + ` sibling
- ` + "`channel`" + ` / ` + "`channel_id`" + ` fields gain a ` + "`channel_name`" + ` sibling
- ` + "`ts`" + ` and ` + "`*_ts`" + ` fields gain a ` + "`*_iso`" + ` sibling (RFC3339)

Enrichment runs after ` + "`--fields`" + `, so ` + "`--fields user`" + ` keeps the
resolved ` + "`user_name`" + ` too (no need to list both).

## Pagination

Most list commands return one page by default. Use ` + "`--all`" + ` to fetch
everything, or use the ` + "`next_cursor`" + ` from ` + "`_meta`" + ` with ` + "`--cursor`" + `.

## Channel and User Resolution

Channels accept IDs (C.../G.../D...) or #names. Users accept IDs (U...),
emails, or @display-names.

Channel types (` + "`--type`" + ` flag on ` + "`slack channel list`" + `):

- ` + "`public`" + ` - regular #channels everyone in the workspace can see
- ` + "`private`" + ` - invitation-only channels
- ` + "`mpim`" + ` - multi-party DM (group DM with 3+ people)
- ` + "`im`" + ` - 1:1 DM (` + "`D...`" + ` prefix)

User resolution gotchas:

- ` + "`@name`" + ` matches display name, username, or real name via the local user cache.
- Email lookup (passing ` + "`alice@example.com`" + `) goes through Slack's ` + "`users.lookupByEmail`" + ` API, which fails with session tokens on Enterprise Grid. Prefer ` + "`@name`" + ` in that case.
- On name collisions, first match wins silently. Inspect ` + "`slack user list --query <partial>`" + ` to see the candidates.
`
