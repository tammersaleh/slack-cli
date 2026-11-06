# Header blocks in drafts

Slack's compose editor now emits top-level `header` blocks (Big / Medium / Small heading in the formatting bar). The CLI rejects them locally as `invalid_blocks` and the Markdown converter turns `#` headings into bold `rich_text_section`s. This plan adds header support to both paths and corrects every doc that says drafts cannot hold headings.

## Measured 2026-11-06

Composer-authored draft (self-DM, three headings, nothing else), read back with `slack draft list`:

```json
[{"type":"header","level":1,"text":{"type":"plain_text","text":"Big heading","emoji":true}},
 {"type":"header","level":2,"text":{"type":"plain_text","text":"med heding","emoji":true}},
 {"type":"header","level":3,"text":{"type":"plain_text","text":"small heading","emoji":true}}]
```

No `rich_text` block at all, and the draft was not tombstoned. `block_id` is server-assigned.

Probes sent through `drafts.create` with the CLI's own session client (`PostInternalForm`, `is_from_composer: true`), self-DM, all read back byte-identical (modulo `block_id`) and alive after 60s:

| payload | result |
|---|---|
| `[header L1, rich_text(section "para one\n"), header L2 with `:tada:`, rich_text(list, section), header L3]` | accepted, round-trips |
| header without `level` | accepted, stored without `level` |
| header `level: 4` | accepted, stored as 4 |
| header `level: 0` | `invalid_message` |
| header without `emoji` key | accepted, server adds `emoji: true` |
| header only, no rich_text | accepted, alive after 60s (so a header is renderable content) |
| header `text.type: mrkdwn` | `invalid_message` |
| header with empty `text.text` | `invalid_message` |
| `[rich_text(section "no newline"), header, rich_text(list)]` | accepted, round-trips |

Not measured (needs a human in Desktop): rendering of level 4, whether a trailing `\n` on the paragraph before a header adds a blank line, whether Desktop flattens rich_text blocks across a header (the absorption rule), and whether `:tada:` renders as an emoji in a header. Probe drafts left in the self-DM for that: the mixed one, the level-4 one, and the no-newline-before-header one.

## Design

### Validator (`validateBlockShapes`, cmd/draft.go)

- Top-level block types allowed: `rich_text` and `header`. Everything else keeps the current rejections (`table`, `data_table`, generic).
- A `header` must have `text` as an object with `type: "plain_text"` and a non-empty string `text`. `level`, when present, must be a JSON integer >= 1 (Slack rejects 0; the composer emits 1..3; 4 is API-accepted with unverified rendering). `emoji` is optional and passed through. Failures are `invalid_blocks` naming the block index and the exact fix.
- A header is NOT treated as a boundary for the absorption check. The previous-section state carries across a header, so `[rich_text(section without "\n"), header, rich_text(list)]` is still `invalid_blocks`. Whether Desktop flattens across a header is unverified (the probe only proves storage), and a wrong reject with an actionable message is cheaper than a wrong accept that glues content. Relax once the probe draft has been inspected in Desktop. The converter is unaffected: it terminates every section that has any successor.
- The return value becomes "has renderable body": true for a non-empty `rich_text` or any `header`. Rename `hasRichTextBody` accordingly at the call sites.

### Converter (`internal/mdblocks`)

- `Convert` emits a JSON array of top-level blocks in source order: `header` blocks for ATX/Setext headings, and one `rich_text` block per run of containers between headers. A document with no headings still produces exactly one `rich_text` (goldens unchanged except `heading.json` and `absorption.json`).
- `#`/`##`/`###` map to `level` 1/2/3. `####` and deeper clamp to 3 (the composer offers three sizes; deeper levels are a documented normalization like the dropped fence language).
- Header text is `plain_text`, so inline formatting (bold, italic, strike, code) is dropped to plain text. `:emoji:` tokens are written back as `:name:` text (the header carries `emoji: true`, the composer's own shape). A link, user mention, channel mention, or broadcast inside a heading is rejected with `invalid_markdown` - plain_text cannot carry them and dropping the URL or mention is content loss. Soft/hard breaks inside a heading become a space.
- `applyAbsorptionRule` keeps its current rule: every section with any successor, header included, ends with `\n`. A header is still the successor of a Markdown paragraph, and dropping the newline would change existing output on an unmeasured hunch. Only the final section of the whole document goes without.
- Header-only Markdown is valid content: the empty check must count headers, not just `c.containers`. Never emit an empty `rich_text` block (leading, trailing, or consecutive headings).
- Bare URLs and emails in a heading are linkify link nodes and are rejected like explicit links; document and test that case.
- `TestGolden_SingleTopLevelBlock` becomes "exactly one rich_text when the source has no heading; otherwise top-level types are header/rich_text and no two rich_text blocks are adjacent".

### Docs

Every statement that drafts strip or lack headings goes. Files: `SPEC.md` (draft create help, Markdown table, Block Kit rules), `skills/slack-cli/SKILL.md` (Markdown subset, "express header as bold rich_text", block-types-that-break list, default pattern, validation), `docs/draft-messages.md` (stripping section, matrix row "header only", verification table), `cmd/draft.go` help text and error strings, `CLAUDE.md` (mdblocks and validator bullets), `docs/markdown-drafts-plan.md` (heading bullet, mark superseded by this file).

## Steps

1. [x] Red: validator tests for accepted header shapes, rejected shapes (missing text, mrkdwn, empty text, level 0, non-integer level), header-only renderable body, header as absorption boundary.
2. [x] Red: converter goldens `heading.md`/`.json` rewritten; new goldens for mixed heading/paragraph/list, deep heading clamp, emoji in heading; error tests for link/mention/channel/broadcast in heading.
3. [x] Green: validator (`validateHeaderBlock`, commit a39762f).
4. [x] Green: converter (`heading`/`plainText`/`blocks`, commit 79aadbd).
5. [x] Refactor (`codeSpanText` shared by inline and heading paths), `mise run check` green.
6. [x] Docs: SPEC, SKILL, draft-messages, help text, CLAUDE.md, markdown-drafts-plan.
7. [ ] Code review (feature-dev:code-reviewer), fix, re-review.
8. [ ] Commit `feat(draft): accept header blocks and convert Markdown headings to them`, push, wait for release, brew upgrade, verify live in self-DM with `--markdown`.

## Discoveries during implementation

- The pre-existing `TestDraftCreate_RejectsNonRichTextBlocks` row `header_before_rich_text` was replaced with a `context` row so the generic-rejection branch keeps a leading-block case; the header shape moved to `TestDraftCreate_HeaderBeforeRichTextPassesThrough`.
- Headings inside a quote or list item already fail through the containers' own "may hold ... only" rejections; pinned by `TestConvert_HeadingInsideContainerIsRejected`, no new code.
- `@name` (bad mention), image, and inline HTML inside a heading were already errors on the old bold-section path and stay so; only link, mention, channel, and broadcast needed new heading-specific rejections.
- JSON `1.0` decodes to the same float64 as `1`, so the validator cannot tell them apart; both are accepted as level 1. `1.5` and `"2"` are rejected.

## Decisions

- Clamp `####`+ to level 3 rather than pass through: composer range is 1..3, level 4 rendering unverified. The validator has no upper bound (hand-written `level: 4` is server-accepted).
- Codex review 2026-11-06: agreed on clamp and rejection; pushed back on dropping the newline before a header (kept) and on the header-as-absorption-boundary (made conservative). Additional tests it named: `cmd/draft_test.go` has a `header_before_rich_text` rejection row that becomes passthrough coverage; `cmd/skill_test.go` `TestSkill_DraftGuidance` pins "One top-level rich_text" wording; header-only create and a schedule-only update of a header-only draft; validator level cases zero/negative/fractional/string; converter cases header-only, consecutive, Setext, empty heading, H4/H6 clamp, emoji with skin tone.
- Reject links/mentions in headings rather than flatten: dropping a URL is content loss; dropping bold inside a heading is not.
