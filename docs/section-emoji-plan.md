# Plan: set a sidebar section's emoji

Source: `todo/section-has-no-way-to-set-an-emoji.md`. Delete that file when this lands.

## Verified live (2026-10-16, Enterprise Grid org session token, scratch section created and deleted)

`users.channelSections.create` and `users.channelSections.set` both validate the `emoji` value server-side:

| value sent | result | `list` returns |
|---|---|---|
| `heart` | ok | `heart` |
| `tada`, `thumbsup`, `+1`, a custom emoji name | ok | same string, no alias normalization |
| `wave::skin-tone-2` | ok | `wave::skin-tone-2` |
| `` (empty) | ok | `` (clears) |
| `:heart:` | `emoji_invalid` (create and set) | unchanged |
| `:wave::skin-tone-2:` | `emoji_invalid` | unchanged |
| `Heart`, `heart ` (trailing space), `heart::skin-tone-9`, `🎉`, unknown name | `emoji_invalid` | unchanged |

So the wire format is the bare name, exactly as `list` emits it. The server rejects everything else with a clear code and the re-fetch equality check used by `rename` holds (no normalization observed).

## Design

### `section create <name> [--emoji NAME]`

`createSection` takes an emoji argument; the flag's value replaces the hard-coded `""`. The row gains `emoji` (always present, `""` when unset).

### `section move --new-section NAME [--emoji NAME]`

Same creation path, so the flag rides along. `--emoji` without `--new-section` is `invalid_input` (emoji belongs to the section being created, not to an existing target).

### `section emoji <section-id> <emoji>`

Fetch sections, `section_not_found` if missing, post `users.channelSections.set` with the current name and the new emoji, re-fetch, fail `emoji_failed` (no row, exit 1) unless name and emoji both match, else emit `sectionRow`. An empty `<emoji>` argument clears. Shares a `setSection(ctx, client, id, name, emoji) (*sectionData, error)` helper with `rename`, which keeps its current behavior (emoji preserved, `rename_failed`). The helper returns a sentinel mismatch error carrying the observed state; each caller maps it to its own code.

### Colon handling

`:heart:` and `:wave::skin-tone-2:` are how people type emoji, and Slack rejects both. The CLI strips exactly one leading and one trailing colon when both are present and something sits between them (length 3 or more): `:heart:` -> `heart`, `:wave::skin-tone-2:` -> `wave::skin-tone-2`. `:` and `::` go verbatim, so a malformed value gets `emoji_invalid` instead of silently clearing the emoji. Nothing else is touched: case, whitespace, unknown names, and unicode go verbatim and Slack's `emoji_invalid` passes through as the error. Normalization happens once, inside the shared code path, so `create`, `move --new-section`, and `emoji` cannot drift. The stripped value is what the re-fetch compares against and what the output row shows.

### `--emoji` presence on `move`

A plain string cannot tell `--emoji ""` from no flag, and `--emoji ""` without `--new-section` must still be `invalid_input` with no API call. Track presence (kong pointer field or equivalent).

### Race

`section emoji` has the same read-modify-write window as `rename`: a concurrent rename between the list and the set is overwritten. The endpoint offers nothing atomic. Document in CLAUDE.md, do not design around it.

### Not doing

- No local emoji-name validation or `emoji.list` lookup. The server already answers `emoji_invalid` and a local list would rot.
- No `--emoji` on `rename`. `rename` and `emoji` are each one field; a caller wanting both runs both.

## Steps

1. [x] SPEC.md: `section create` flag and row, `section move --emoji`, new `section emoji` section with the verified table summarized.
2. [x] Tests (red): `TestSectionCreate_Emoji`, `TestSectionCreate_EmojiColonsStripped`, `TestSectionMove_NewSectionEmoji`, `TestSectionMove_EmojiWithoutNewSection`, `TestSectionEmoji`, `TestSectionEmoji_Clear`, `TestSectionEmoji_ColonsStripped`, `TestSectionEmoji_SectionNotFound`, `TestSectionEmoji_IgnoredWrite` (setHook reverts), `TestSectionEmoji_NamePreserved` (form carries current name), `TestSectionEmoji_SessionTokenRequired`, `TestSectionEmoji_EmojiInvalid` (mock answers `emoji_invalid`, code passes through, exit 1), `TestSectionEmoji_NameChangedAfterWrite` (setHook changes the name, emoji intact, must be `emoji_failed`), `TestSectionEmoji_SectionVanished` (no row), `TestSectionMove_EmojiWithoutNewSection_Empty` (`--emoji ""` still `invalid_input`, no API call), `TestSectionMove_NewSectionEmojiColonsStripped`, `TestSectionCreate_EmojiInvalid`, colon edge cases `:` and `::` sent verbatim. Update the existing create output assertion to require `"emoji":""`. Existing `sectionStore.applySet` already models clear-on-omit.

Codex review of this plan (2026-10-16) produced the presence-tracking, length-3 colon rule, and the extra postcondition tests above. Nothing it suggested was rejected.
3. [x] Implement: `createSection` emoji param, `setSection` helper, `SectionEmojiCmd`, flags, `normalizeEmojiArg`.
4. [x] SKILL.md: add `slack section emoji <section-id> <emoji>` and `--emoji` on create.
5. [x] CLAUDE.md architecture bullet with the verified table's conclusions.
6. [x] `mise run check`, code review before and after plus a Codex diff review, merge, push, released as 5.3.0, installed and verified live 2026-10-19 on a scratch section: create with `:tada:` stored `tada`, `wave::skin-tone-2` set, `:heart` answered `emoji_invalid` exit 1, empty cleared. (`feat:`), wait for release, `brew upgrade --cask tammersaleh/tap/slack-cli`, verify live on the five sections in the todo.
7. [x] Delete `todo/section-has-no-way-to-set-an-emoji.md`.

## Live measurements added after Codex review (2026-10-16)

`:`, `::`, `::heart::`, `:heart`, `heart:` all `emoji_invalid`. `wave::skin-tone-6` accepted, `skin-tone-1` and `skin-tone-9` rejected. Mock's `[2-6]` range interpolates 3-5.
