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

## Commands

### Messages

` + "```" + `
slack message list <channel> [--limit N] [--after TS] [--before TS] [--has-replies] [--has-reactions]
` + "```" + `

` + "```" + `
slack message get <channel> <ts>...
slack message permalink <channel> <ts>...
` + "```" + `

### Threads

` + "```" + `
slack thread list <channel> <ts> [--limit N]
` + "```" + `

### Search (requires user token)

` + "```" + `
slack search messages <query> [--limit N] [--sort timestamp|score] [--sort-dir asc|desc]
slack search files <query> [--limit N]
` + "```" + `

### Channels

` + "```" + `
slack channel list [--limit N] [--type public|private|mpim|im] [--query STR] [--include-non-member]
slack channel info <channel>...
slack channel members <channel> [--limit N]
` + "```" + `

### Users

` + "```" + `
slack user list [--limit N] [--query STR] [--presence]
slack user info <user>...
` + "```" + `

User arguments accept IDs (U...), emails, or @names.

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

Drafts must include at least one ` + "`rich_text`" + ` block with non-empty
` + "`elements`" + `. Slack Desktop's Drafts-panel reconciliation tombstones
(sets ` + "`is_deleted=true`" + `) any server-side draft that lacks rich_text
content, so section-only or divider-only arrays round-trip as deleted
within seconds. You can include other block types (` + "`section`" + `,
` + "`divider`" + `, ` + "`header`" + `, ` + "`context`" + `) alongside a rich_text block.

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

Options to get a heading above a list:

- Put the heading INSIDE the list as its own rich_text_section item
  (accept that it shows as a bullet).
- Use a ` + "`rich_text_quote`" + ` or ` + "`rich_text_preformatted`" + ` between
  heading and list; either forces a block break.
- Drop the heading entirely and use a bolded first bullet.

##### Validation

The CLI checks: non-empty JSON array, each top-level block has a string
` + "`type`" + ` field, and at least one block is a ` + "`rich_text`" + ` with
non-empty ` + "`elements`" + ` (drafts without rich_text get tombstoned by
Desktop). Semantic errors (missing required subfields, unknown inline
types, malformed style objects) surface as ` + "`invalid_blocks`" + ` from
Slack's API, not locally.

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

Channels accept IDs (C.../G.../D...) or #names. Users accept IDs (U...), emails,
or @display-names.
`
