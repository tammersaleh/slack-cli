# Divider blocks in drafts

Slack's compose editor now emits top-level `{"type":"divider"}` blocks (the horizontal rule in the formatting bar). The CLI rejects them locally as `invalid_blocks`, and the Markdown converter rejects `---` as "a horizontal rule has no rich_text equivalent". This follows the header-blocks work (`docs/header-blocks-plan.md`) and reuses its machinery: a divider is a top-level block that splits the rich_text runs.

## Measured 2026-11-06

Composer-authored draft (self-DM): `rich_text, divider, rich_text, divider, rich_text`, each divider a bare `{"type":"divider"}` plus a server-assigned `block_id`. Not tombstoned.

Probes through `drafts.create` on the CLI's session client (self-DM, read back byte-identical modulo `block_id`, alive after 100s):

| payload | result |
|---|---|
| `[divider]` only | accepted, alive (a divider is renderable content) |
| `[rich_text(section without "\n"), divider, rich_text(list)]` | accepted, round-trips |
| `[divider, rich_text, divider, divider, header, divider]` | accepted, round-trips, double divider stored as two |

Desktop rendering: PENDING (the absorption-boundary probe `[section, divider, list]`, divider-only, leading/trailing, and consecutive dividers).

## Design

### Validator

- Allowed top-level types: `rich_text`, `header`, `divider`. A divider needs only `type`; every other field passes through (server validates).
- A divider counts as renderable content (`hasBody`).
- Absorption boundary: follow the Desktop check. Confirmed boundary: reset state like header. Not confirmed: carry state across (conservative). Shipped: carry across, pending Desktop check (`TestValidateBlockShapes_DividerCarriesAbsorptionState`). When Desktop shows `[section without "\n", divider, list]` with no glue, reset `prevElem`/`prevSection` at a divider in `validateBlockShapes`, invert that test, and update SPEC.md, SKILL.md, docs/draft-messages.md, and the CLAUDE.md divider bullet.

### Converter

- `ast.ThematicBreak` (any CommonMark spelling: 3+ of `-`, `*`, `_`, spaces allowed, up to 3 leading spaces) emits `{"type":"divider"}` as a top-level item, flushing the current rich_text run exactly like `header` does in `blocks()`. Leading, trailing, and consecutive dividers pass through.
- `Title\n---` is a Setext H2, not a divider (goldmark runs Setext before thematic break); `Title\n\n---` is paragraph plus divider. One regression test.
- A thematic break inside a quote or list item keeps the existing container rejection (use `***` in the list-item test to avoid Setext ambiguity).
- `applyAbsorptionRule` unchanged: a paragraph before a divider keeps its trailing `\n`.

### Docs

Remove horizontal rule from every rejection list (cmd/draft.go help, SPEC.md, SKILL.md). Fix the claim that Desktop strips `divider` (CLAUDE.md stripping bullet, SKILL.md "mixed top-level blocks [rich_text, section, divider]" bullet, docs/draft-messages.md). Mark the May divider measurements superseded rather than deleting them. Update docs/markdown-drafts-plan.md's horizontal-rule decision as superseded. Tests: cmd/skill_test.go pins "rich_text or header" wording.

## Tests (from Codex review 2026-11-06)

Validator: divider-only hasBody; leading/trailing/consecutive; measured mixed sequences; divider with extra fields passes through; move divider out of the still-rejected table (keep section, context); generic error names all three types; schedule-only update of a divider-only draft; renderable-content error names divider; absorption pin per the Desktop result with the direct section-list rejection as control.
Converter: goldens divider-only, divider-edges (leading/trailing/consecutive), divider-mixed (rich_text / divider / header / divider / rich_text); paragraph before divider keeps `\n`; TestGolden_TopLevelShape allows divider and "no header or divider means exactly one rich_text"; drop the thematic-break error rows; keep in-quote and in-list rejections; Setext-vs-divider regression.
Command: divider_alongside_rich_text rejection row becomes a passthrough asserting the wire JSON; divider-only create at the command level.

## Steps

1. [ ] Desktop check (Tammer). Pending at ship time; the absorption variant shipped is carry-across.
2. [x] Red tests (bf88458). 3. [x] Green validator (ce61a55). 4. [x] Green converter (0990b2f). 5. [x] Docs (2c924ba). 6. [ ] Review, re-review. 7. [ ] Merge, push (`feat:`). 8. [ ] Release, install, verify live.

## Discoveries during implementation

- The command-level tests need `package cmd_test` (the mock helpers live there) while the validator unit tests need `package cmd`, so dividers get two files: `cmd/draft_divider_test.go` and `cmd/draft_divider_cmd_test.go`.
- Two Markdown tests used `---` as their stand-in conversion error (`TestDraftCreate_MarkdownConversionErrorMakesNoCalls`, `TestDraftUpdate_MarkdownConversionErrorMakesNoCalls`); both now use an image.
- `blocks()` breaks runs on `topLevelType` (header or divider) instead of a header-only comparison.
