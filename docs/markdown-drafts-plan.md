# Plan: Markdown input for `draft create` / `draft update`

Status: implemented on branch `feat/markdown-drafts` 2026-10-16; steps 1-8 done, 9-12 in progress (see Steps). Work order: `todo/draft-create-has-no-markdown-input-so-every-caller-hand-rolls-a-converter.md`. Codex planning thread reviewed the design on 2026-10-16; decisions below record where it was followed and where not.

## Context

The only body input drafts accept is rich_text Block Kit JSON. Four scripts outside this repo each carry a Markdown-to-rich_text converter, and the most complete one has three bugs (no ordered lists, `*x*` emitted literally, blocks reordered). The CLI should own the conversion once, held to `validateBlockShapes` like hand-written JSON.

Constraints already established in this repo (see `docs/draft-messages.md`, `cmd/draft.go`, SKILL.md rich_text section):

- Only `rich_text` survives as a top-level block. Desktop flattens adjacent top-level `rich_text` blocks, so emit exactly one.
- Containers: `rich_text_section`, `rich_text_list` (style bullet/ordered, indent 0-2, cannot nest a list), `rich_text_quote`, `rich_text_preformatted`.
- Absorption rule: a section directly before list/preformatted/quote must end with a text inline ending in `\n`.
- Slack's `markdown` block returns `internal_error` from `drafts.create`. Conversion must be client-side.
- `@name` resolution is a coin flip on name collisions (`todo/at-name-resolution-picks-a-random-user-when-names-collide.md`). Not fixed.

## Decisions

- Parser: goldmark (`github.com/yuin/goldmark`) with the strikethrough, linkify, and table extensions (table enabled only so pipe tables can be rejected with a line number). Not a hand-rolled line parser: the caller bugs are exactly the inline-emphasis and list-detection cases CommonMark parsers get right.
- New package `internal/mdblocks`: pure, no network. `Convert(src []byte, opts) ([]byte, error)` returns the blocks JSON array. Slack-specific inline tokens (`:emoji:`, `<@U>`, `<#C>`, `@here`) are goldmark inline parsers, not regex post-processing, so escapes (`\:wave:`) and code spans stay literal and errors carry line numbers.
- Flags on both `draft create` and `draft update`:
  - `--markdown`: stdin is Markdown. No file flag; shell redirection covers it. Combinable with `--table`.
  - `--print-payload`: convert, validate, print `{"blocks":[...]}` (plus `attachments` when `--table` is given) as one JSONL row and a `_meta` trailer. No draft or destination calls. Positionals stay syntactically required but are not resolved. Rejects schedule flags. Named `payload` not `blocks` because `--table` adds an attachment the preview must show.
- `@channel`, `@here`, `@everyone` always become `broadcast` elements. No gating flag: the user reviews and sends the draft in Slack, so a broadcast in the compose box is visible before it goes out. (fe-recap refused them because the script posted directly.)
- Symbolic `@name` / `@"Full Name"` resolution is **out of the first release** (Tammer confirmed: force IDs). It depends on the collision fix. Only `<@Uxxx>` / `<@Wxxx>` pass through as `user` elements. Phase 2 (after the sibling todo lands) adds `@handle` and `@"Full Name"` with explicit quoting for multi-word names. Longest-resolving-prefix in prose is rejected: it would make a document's meaning depend on the current directory.
- Error taxonomy: `invalid_markdown` (exit 1) for anything the subset cannot represent, with `line N:` in the detail. `invalid_blocks` from `validateBlockShapes` on converter output means a converter defect. `invalid_input` stays for flag misuse (`--print-payload` without `--markdown`, `--print-payload` with `--at`).
- Headings: a `rich_text_section` with bold text (inline styles inherit bold), always terminated by a text inline ending in `\n` so a blank line separates the heading from what follows. Extended after review (2026-10-16): every non-final section gets the trailing `\n`, including paragraph before paragraph, because Slack encodes paragraph structure as `\n` in text and two bare adjacent sections may render one line apart or glued. If Desktop shows a double gap, drop the paragraph case; if it shows a single line break, switch to `\n\n`. Level is dropped; `#` through `###` render the same. Verify live that the trailing `\n` renders as a blank line rather than a bare line break; switch to `\n\n` if not.
- Normalizations that are documented, not errors (Codex proposed rejecting these; overbuilt): heading level is dropped, fenced-code language tag is dropped, link title is dropped. Agents write ```` ```bash ```` constantly. Rejecting that is friction with no safety benefit.
- Strikethrough is GFM `~~x~~`. Single-tilde is not supported.
- Soft line break and hard line break both become `\n`. Slack's composer is line-oriented and agents write one sentence per line.
- Ordered lists: `offset: N-1` for a list starting at N, continued across a nested-list split. Live probe 2026-10-16: `offset: 2` and `offset: 4` were accepted by `drafts.create`, stored verbatim, and the draft was not tombstoned after 25s. Whether Desktop renders the numbering from `offset` is pending Tammer's inspection of the probe draft left in the self-DM.
- List nesting: separate `rich_text_list` blocks in source order. `parent A / child / parent B` emits list(0), list(1), list(0). Depth capped at 2 (indent 0-2), deeper is `invalid_markdown`.
- Blockquote: paragraph children only. Breaks within a paragraph join with `\n`, paragraphs join with `\n\n`. Lists, code, nested quotes inside a quote are `invalid_markdown`.
- Links: `http`, `https`, `mailto` only; relative or other schemes are `invalid_markdown`. Label is plain text with inherited style (Slack link elements cannot hold nested inlines).
- Emoji: boundary-aware scanner. `:30:` inside `10:30:45` is not an emoji. `:wave::skin-tone-2:` is one emoji element named `wave::skin-tone-2`.
- Code spans are opaque: no emoji, mention, or link recognition inside.
- Unsupported, always `invalid_markdown` with a line: thematic break, image, HTML block, inline HTML, pipe table. (Task-list syntax `- [ ]` is plain text without the GFM extension, so it passes through literally rather than erroring.)
- Absorption is a final pass over the emitted container sequence: when a section precedes list/preformatted/quote, extend the trailing text with `\n` or append `{"type":"text","text":"\n"}` if the last inline is not text. Then the CLI runs `validateBlockShapes` as a backstop.
- `draft update --markdown` produces a bare blocks array, so `buildIntent` replaces blocks and preserves attachments with no change.

## Steps

1. [done] Update `SPEC.md` draft create/update sections with the subset, normalizations, error taxonomy, preview contract.
2. [done] `go get github.com/yuin/goldmark` (v1.8.6).
3. [done] `internal/mdblocks`: golden tests first (`testdata/<case>.md` and `.json`, exact JSON) for every supported and rejected construct listed under Tests. Watch them fail.
4. [done] Implement AST walk with source-ordered list segmentation, inline style inheritance, Slack inline parsers, absorption pass, typed `Error{Line, Detail}`.
5. [done] Wire `--markdown` and `--print-payload` into `DraftCreateCmd` and `DraftUpdateCmd`. Conversion happens in `readDraftPayload`'s place, output fed through `parseDraftPayload` so `validateBlockShapes` runs.
6. [done] Command tests via `runWithMockSession` (`cmd/draft_markdown_test.go`, `cmd/draft_markdown_shapes_test.go`): create sends the converted `blocks` form field; update preserves attachments; `--table` composes; preview prints payload and makes zero draft/resolution calls; conversion failure makes zero write calls.
7. [done] `mise run test`, `mise run lint`.
8. [done] Live probe in a self-DM (see below). Nothing needed adjusting; API round-trip byte-identical for every construct. Probe draft left in the self-DM for Desktop inspection; two smaller probes (skin-tone emoji, channel element) verified and deleted.
9. Code review (feature-dev:code-reviewer), before and after fixes.
10. [done] Help text, SKILL.md (Markdown section under drafts, point agents at `--markdown` first), CLAUDE.md architecture-decision bullet with verified findings only.
11. Commit as `feat: add --markdown input to draft create and update`. Push, wait for release, `brew upgrade --cask tammersaleh/tap/slack-cli`, exercise with installed binary.
12. Delete the todo file. Tell Tammer which `~/brain/cw` converters can now be deleted (fe-recap `markdown_to_blocks`, sweep.py `rich_text`).

## Tests (mdblocks goldens)

Order preservation: paragraph, list, paragraph, list. Nested `parent A / child / parent B` is three list containers. Bullet inside ordered and ordered inside bullet keep style and depth. Heading always ends with a newline text inline, including before a plain paragraph. Heading ending in link or emoji gets a separate newline text element. Paragraph (not heading) before a list gets the newline on its text. Paragraph before paragraph gets the trailing newline too (changed after review, see Decisions). Bold+italic+strike nest into one style object. Styled link inherits style. Markdown, `:wave:`, `<@U01XYZ>`, `[x](url)` inside a code span stay literal. `\*`, `\:` stay literal. `alice@example.com` and `10:30:45` are not tokens. `:wave::skin-tone-2:` is one emoji. Soft and hard breaks give `\n`. Two quote paragraphs join with `\n\n`. Every unsupported node reports the right line. Empty input errors. Output always has exactly one top-level block. Every golden output passes `validateBlockShapes` (backstop only; the exact-JSON assertion is the real check).

## Live verification (self-DM only, before step 10)

Done 2026-10-16 through the API; see `docs/draft-messages.md` "Markdown input" for the table. Pending Tammer in Desktop: open the probe draft in the self-DM and check (a) italic and strike render in the compose box, (b) the ordered list shows 3, 4 then 5 across the nested bullet, (c) a blank line follows the heading, (d) nested bullets at indent 1 and 2. Then send it to yourself and read it back with `slack message get`.


1. Ordered list starting at 1. Ordered list starting at 3 with candidate `offset: 2`. Ordered list split around a nested bullet list.
2. Bullet indent 1 and 2.
3. Italic, strike, bold+italic in the compose box, not just the posted message. Heading's trailing `\n` renders as a blank line before the next paragraph.
4. Quote with one and two paragraphs. Fenced and indented code.
5. Emoji inside a list item. List item ending in a link.
6. `<@U>` and `<#C>` elements. `@here` as a broadcast element.
7. Open in Desktop, re-read with `slack draft list`, diff against what was sent. Send to self, read with `slack message get`.

Record findings (shapes and behavior, no IDs) in `docs/draft-messages.md` and CLAUDE.md.

## Phase 2 (separate feature, after the collision fix)

`@handle` and `@"Full Name"` resolution through `ResolveUser` with typed not-found / ambiguous / upstream errors preserved through the converter's error wrapping. One resolver call per distinct name per conversion. Unquoted token ends at whitespace or punctuation.

## Code review (feature-dev:code-reviewer, 2026-10-16)

First pass found: `<>` in prose panicked `parseAngle` (fixed, guard `end < 2`); inline errors on nodes with no text segment reported the next line (fixed, `lineOf` now scans to the first non-blank byte after the previous sibling for inlines and blocks alike); empty `-` items and images inside link labels were silently dropped (fixed: placeholder item, error); `textOf` resolved entities after unescaping so `\&amp;` became `&` (fixed, single-pass `decodeText`); paragraph-to-paragraph spacing was unverified and the heading help overclaimed a blank line (spacing rule extended, help and SKILL reworded as pending). Test gaps filled: absorption with styled/user trailing inlines and heading before quote, loose and empty list items, ordered inside bullet, token right boundaries, error lines on later lines and inside list items, update conversion error, update with `--table`, more `--print-payload` flag combinations.

Second pass found: a two-space hard break after a literal backslash lost the backslash (trim now only for the backslash form, checked against the source byte at `Segment.Stop`); `&#38;amp;` double-decoded (own single-scan `resolveEntities`); a block after a fenced code block reported the closing-fence line (skip the fence line); `>` alone emitted `elements: null`; `0.` gave `offset: -1` (clamped). The reviewer's "empty bullet placeholder" question was answered live: Slack rejects an empty text inline with `invalid_message`, so empty items and quotes are now `invalid_markdown`.

Third pass: an empty fenced code block still emitted an empty text inline (now `invalid_markdown`); `lineOf` for an empty first list item descended into the next item (now walks ancestors' previous siblings instead of the parent's first offset); numeric references accepted more digits than CommonMark (capped at 7 decimal / 6 hex); goldmark was `// indirect` in go.mod (`go mod tidy`).

## Discoveries during implementation

- goldmark keeps backslash escapes and HTML entities in the raw text segment and resolves them in its HTML renderer, not the parser. An AST walker has to call `util.UnescapePunctuations` / `ResolveEntityNames` / `ResolveNumericReferences` itself, or `\*` ships literally.
- goldmark's `Text.SoftLineBreak()` / `HardLineBreak()` are flags on the text node that ends the line, not separate nodes.
- A `\<@U01XYZ>` escape skips the `<` trigger, so the `@` parser then fires with `<` as the preceding character. The `@` parser treats a preceding `<` as "already declined by the angle parser" and stays literal.
- linkify turns bare emails into `AutoLinkEmail` nodes whose `URL()` lacks the `mailto:` prefix; the converter adds it. Consequence: an email in prose is a `link` element, not text.
- `slack channel list` takes `--type`, not `--types`; a bad flag prints usage to stdout, which `jq` then chokes on. Cost two probe iterations.

## Codex suggestions rejected

- Reject fenced-code language tags and link titles: friction without safety value. Documented drop instead.
- `slack draft render` command: not needed; `--print-payload` covers it.
