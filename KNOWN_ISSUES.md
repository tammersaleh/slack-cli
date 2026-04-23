# Known Issues

## `channel list --type im` returns empty pages

`slack channel list --type im` returns only `_meta` with `has_more: true` and a `next_cursor`, but no IM records. Paging with `--cursor` returns the same shape forever - empty rows, new cursor, still `has_more: true`. Confirmed on 2026-04-22 across 50+ pages.

Workaround: discover DM channels via message search instead. `slack search messages "from:@<handle>"` returns messages whose `channel.id` starts with `D` for 1:1 DMs; `channel.name` holds the other user's ID.

Suspected cause: `conversations.list` called with `types=im` needs the right scope (`im:read`) and may also need the `users:read` scope to populate the response. If the scope is missing, the API returns empty pages rather than erroring. Worth verifying the CLI asks for both scopes and surfaces a useful error when they're missing.

## Skill docs contradict CLI validation on draft block types

The skill docs (`SKILL.md`, drafts section) say:

> You can include other block types (`section`, `divider`, `header`, `context`) alongside a rich_text block.

But the CLI validator rejects any non-rich_text top-level block in a draft with:

```
invalid_blocks: blocks[N] is "section"; drafts must contain only rich_text top-level blocks. Slack Desktop's Drafts compose editor strips non-rich_text blocks when the user opens the draft.
```

The CLI's behavior is correct - Slack Desktop really does strip non-rich_text blocks from drafts on reconciliation. The docs are stale. Confirmed 2026-04-23 attempting to mix `section` blocks with `rich_text`.

Workaround: put everything in a single `rich_text` top-level block, using child containers (`rich_text_section`, `rich_text_list`, `rich_text_quote`, `rich_text_preformatted`) for structure.

Fix: remove the stale sentence from `SKILL.md` and state plainly that drafts accept only `rich_text` top-level blocks.

## CLI drafts never auto-load into the Slack composer

`slack draft create` hardcodes `is_from_composer: "false"` (cmd/draft.go:209 and :410). Slack's channel composer polls `drafts.listActive`, which filters on `is_from_composer=true`, so CLI-created drafts are invisible to it. They show up in Drafts & sent (which reads `drafts.list`) but clicking "Edit draft" navigates to the channel and leaves the compose box empty - making review-before-send awkward. Confirmed 2026-04-23 in Slack web against a Slackbot DM draft: `drafts.list` returned the draft, `drafts.listActive` returned 0, composer stayed blank.

Workaround: review drafts via `slack draft list` and the Drafts & sent tile preview. The full block structure round-trips correctly even though the composer won't load it.

Fix: flip both `drafts.create` param maps in cmd/draft.go to `is_from_composer: "true"` so CLI drafts behave like composer-originated ones. Worth checking whether the tombstoning reconciliation (see CLAUDE.md draft ghosting note) depends on the flag being false before flipping - it plausibly doesn't, since Desktop's reconciler is what enforces the rich_text requirement and that runs on composer-originated drafts too.
