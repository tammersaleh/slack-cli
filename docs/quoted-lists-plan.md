# Quoted lists in `--markdown`

Work order: `todo/quoted-lists-are-rejected-but-slack-supports-them-as-a-bordered-sibling-list.md`. Delete it when this lands.

## Context

`draft create --markdown` and `draft update --markdown` reject a list inside a blockquote (`> - item`) with `invalid_markdown`. Slack's composer serializes a quoted list as a `rich_text_list` with `border: 1`, a sibling of `rich_text_quote`, never nested in it. Nesting a list inside `rich_text_quote.elements` is `invalid_message` at the API.

### Measured 2026-11-02 (self-DM, session token, installed `slack` 5.4.0)

The probe draft, created via `slack draft create <own user id>` from a raw blocks payload:

```text
rich_text_quote (paragraph)
rich_text_list   bullet  indent 0 border 1
rich_text_list   bullet  indent 1 border 1
rich_text_list   ordered indent 0 offset 2 border 1
rich_text_quote (paragraph)
rich_text_preformatted border 1
rich_text_section
```

`drafts.create` accepted it. `drafts.list` returned every `border`, `indent`, and `offset` unchanged, immediately and again after 30s with `is_deleted` unset. So the todo's verify items 4 and 5 hold at the API level: bordered lists keep `offset`, bordered `indent` 1 is accepted, bordered preformatted is accepted, nothing tombstones.

Rendering, inspected by Tammer in Slack Desktop 2026-11-03 (screenshot of the compose box):

1. The quote paragraph, bordered lists, second quote paragraph, and bordered code block render under one continuous left bar. The quote's last line does not glue into the first bullet.
2. `rich_text_preformatted` with `border: 1` renders as a code block inside the bar. Whether the composer itself emits this shape for a quoted code block was not checked.
3. The bordered `indent: 1` item renders nested (hollow bullet) under the bordered parent.
4. The ordered item with `offset: 2` renders as `1.`, not `3.`. The compose box ignores `offset`, at least on a bordered list. Not in scope here; recorded in `docs/draft-messages.md`.

The probe draft was deleted after inspection.

## Decisions

- Pure quoted lists (`> - a`, `> 1. a`, with nesting) ship regardless of the rendering check. The shape matches composer output exactly.
- Mixed quotes (paragraph, list, paragraph) ship: rendering check 1 passed.
- Quoted code (`> ` fenced or indented block) ships as `rich_text_preformatted` with `border: 1`: accepted, round-trips, survives reconciliation, renders as a quoted code block. Composer emission for it was not observed; the rendering is the contract that matters to the caller.
- Nested blockquotes stay rejected. No shape for them.
- `border` is threaded as a parameter (`list(l, indent, border bool)`), not stored on the converter. The key is omitted when false so every existing golden is unchanged.
- Paragraph-only `rich_text_quote` is unchanged, no `border` key.
- `applyAbsorptionRule` and `validateBlockShapes` are unchanged. No evidence that quote-then-bordered-list absorbs, and a raw bordered list already passes validation.
- One `feat:` commit for code, tests, SPEC, SKILL, docs, and the todo deletion. Splitting leaves a commit whose docs contradict its behavior.

## Steps

- [x] Tammer opened the probe draft in Slack Desktop and answered checks 1 to 3 (above).
- [x] `quote`: walk children in source order. Consecutive paragraphs flush as one `rich_text_quote` (joined `\n\n`, as today). An `*ast.List` calls `list(v, 0, true)`. A fenced or indented code block emits `rich_text_preformatted` with `border: 1` (extract a `preformatted(node, border)` helper shared with `block`). A nested blockquote keeps a rejection naming it.
- [x] `list`: add `border bool`, set `"border": 1` on every container it emits, including nested splits.
- [x] Goldens: `quote-list` (pure `> - a` with two items, no `rich_text_quote` in output), `quote-ordered-nested` (`> 3. a`, `>    - nested`, `> 5. b`: three bordered containers, resumed `offset: 3`), `quote-mixed` (paragraph, list, paragraph, fenced code inside one quote: quote, bordered list, quote, bordered preformatted).
- [x] `TestConvert_Errors`: drop the `list in quote` case, keep `nested quote`, add an empty code block inside a quote.
- [x] `cmd/draft_markdown_shapes_test.go` runs the new goldens through `validateBlockShapes` automatically. Confirm.
- [x] `markdownHint` in `cmd/draft.go`: `> quotes (may hold lists and code)`.
- [x] `SPEC.md`: `> ` row (a list or code block inside a quote becomes a sibling `rich_text_list` / `rich_text_preformatted` with `border: 1`), rejection list (nested quote only).
- [x] `skills/slack-cli/SKILL.md`: supported and rejected lists, Block Kit bullets for `border` on list and preformatted (the quoted-list shape, a quote cannot hold a list).
- [x] `docs/markdown-drafts-plan.md` line 33 and `docs/draft-messages.md`: record the 2026-11-03 rendering findings, including `offset` rendering as `1.`.
- [ ] `mise run test`, `mise run lint`, code-reviewer pass before and after fixes.
- [ ] Commit `feat(draft): accept lists and code inside --markdown quotes`, push, wait for release, `brew upgrade --cask tammersaleh/tap/slack-cli`, verify `> - item` live in the self-DM.
- [x] Delete the todo file. Retrospective into CLAUDE.md.

## Discoveries

- The `quote-ordered-nested` resumed segment is `offset: 3`, not 4. Markdown numbers items by count, not by the written number, so `> 3. a` / nested / `> 5. b` is item 1 then item 2 of a list starting at 3: `offset: 2` then `offset: 2 + 1`. The written `5.` is ignored, same as the existing `ordered-split` golden.
- `lineOf`'s fallback (for nodes with no source segment, such as an empty fenced code block) skipped whitespace after the previous sibling but not `>` markers, so an empty code block inside a quote reported the quote's blank `>` line. It now skips `>` when the node has a blockquote ancestor.
- Review pass 1 (code-reviewer): the "composer's own shape" claim was scoped to lists (code-block emission by the composer is unverified), the `offset` rendering limitation was added to SPEC and SKILL, the nested-quote test asserts `quote inside a quote` plus a case after a quoted list, and goldens `quote-indented-code` and `quote-list-then-text` were added.
- Review pass 2: one stale composer claim in `docs/markdown-drafts-plan.md` scoped, the "not inspected" `offset` sentences in SPEC and SKILL now name the unbordered case only, help paragraph rewrapped. No code findings.
