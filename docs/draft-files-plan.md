# File attachments on drafts

`draft create` and `draft update` cannot attach an uploaded file. `drafts.create` hard-codes `file_ids: "[]"`, the tombstone replacement drops files, and no draft row shows them. Work order: `todo/draft-create-and-update-cannot-attach-a-file.md` (delete it when this lands).

## Status

- [x] Measure (2026-11-16, below)
- [x] Codex review of this plan (2026-11-16, folded in below)
- [x] Record findings in `docs/draft-messages.md`
- [x] Tests, then implementation
- [x] SPEC.md, SKILL.md, help text, CLAUDE.md
- [ ] `feature-dev:code-reviewer` pass, before and after fixes
- [ ] Push, release, install, verify with the installed binary
- [ ] Tammer checks Desktop: the file renders in the composer, the draft survives an open Drafts panel, send posts the file

## Measured 2026-11-16

Enterprise Grid org, desktop session token (E-org context), out-of-tree probe module, self-DM only. Every probe draft and file was deleted afterwards.

### Read-only

`drafts.list limit=100`: 4 of 100 drafts carried a non-empty `file_ids`, all four sent.

- `file_ids` is a JSON array of `F…` file id strings.
- The top-level `files` array holds one full file object (the `files.info` shape: `id`, `name`, `title`, `size`, `filetype`, `mimetype`, `mode`, `shares`, thumbnails, ...) per distinct file that still exists and is referenced by a draft on the page. It is deduplicated, and a deleted file is absent from it while its id stays in the draft's `file_ids`.
- Two of the four were composer drafts (`is_from_composer: true`) with `blocks: []` and one file, so the composer's own file-only shape is an empty blocks array.
- A draft with a file has the same key set as one without. Some drafts of either kind carry an extra `unfurl` key.

### Upload

slack-go v0.18.0's helpers work on the session client (`client.Bot()`, xoxc token plus `d` cookie): `GetUploadURLExternalContext`, `UploadToURL` (multipart POST to `files.slack.com`), `CompleteUploadExternalContext` with no channel. The result is a file with no shares, `is_public: false`, visible to the uploader.

- A 29-byte `.txt` became `mode: snippet`, a 600-byte PDF `mode: hosted`. The PDF's `filetype` and `mimetype` were empty on a `files.info` seconds after upload and `pdf` / `application/pdf` a minute later.
- `files.getUploadURLExternal` with `length=0` is `invalid_arguments`. With `length=99999999999999` it answered ok, so no size limit shows at that step. The error for an oversized or blocked file is unmeasured.
- A file id whose upload was never completed (`files.completeUploadExternal` skipped) is `file_not_found` on `drafts.create`.

### drafts.create with files

| request | result |
|---|---|
| rich_text plus one file id | ok. Response is `{ok, draft, files}`, `draft.file_ids` holds the id, `files` the file object |
| `blocks=[]` plus one file id | ok, `is_deleted: false` |
| a file id already on another live draft | ok |
| two file ids (PDF, txt), thread destination | ok, order kept in `draft.file_ids` and `files` |
| an id that does not exist | `file_not_found` |
| `file_ids` param omitted | `invalid_arguments` |

All three live drafts were alive (`is_deleted: false`) 78s after create with Slack Desktop running. The Drafts panel was not known to be open, so survival of panel reconciliation is unverified, for the file-only draft in particular.

### Update and delete

- `slack draft update <id> --markdown` (5.13.1) on the text-plus-file draft kept the file: the round trip in `buildIntent` works.
- `drafts.delete` leaves the files in place (`files.info` still answers).
- `files.delete` on a file a live draft references answers ok. The draft keeps the dangling id in `file_ids` and the file drops out of the top-level `files`. What Desktop shows for such a draft is unknown.

### Not measured

Desktop rendering of a CLI-attached file, sending such a draft, an open Drafts panel, a scheduled draft with a file, `--broadcast`, a workspace (T) token, the request sequence the composer itself makes, size and blocked-type errors, the `drafts.update` response shape (assumed `{ok, draft, files}` like create).

## Design

### Flags

- `draft create --file PATH` (repeatable): upload each file, send their ids as `file_ids` in flag order.
- `draft update --file PATH` (repeatable): replace the draft's files with these. No `--file` preserves the existing list (today's behavior). `--no-files` clears it. `--file` with `--no-files` is `invalid_input`.
- `--print-payload` with `--file` or `--no-files` is `invalid_input`: it promises zero API calls, same as the other write-only flags.

### Validation before any request

Each path must be a regular file of non-zero size, else `invalid_input` naming the path (the `--table` convention). No size cap and no type check locally: the server validates.

### Renderable content

A file counts. `draft create --file x.pdf` with empty stdin is a file-only draft sent as `blocks=[]`, the shape the composer itself produced. On update the rule runs on the effective draft: body, table attachment, or a non-empty effective file list. `errNoRenderableContent` and `MissingBlocks` wording gain the file case.

### Upload

`uploadDraftFiles(ctx, client, paths) ([]string, error)` in a new `cmd/draft_files.go`, on the session client, three `api.CallOnce` steps per file so `--trace` shows them (`files.getUploadURLExternal`, `files.upload` for the POST, `files.completeUploadExternal`). Title and filename are the path's base name. Errors are returned raw and classified at the call site.

Upload runs as late as possible, so an input error never uploads. On create: after recipients resolve. On update the order is:

1. Validate flags and every path locally.
2. Look the draft up, refuse a sent one.
3. `buildIntent` takes the file policy (preserve, clear, replace) and runs the renderable-content rule on the effective draft, counting "will have files" for replace. A schedule-only or body-only update of an existing file-only draft must stay valid.
4. Upload the replacement files once and put their ids in the intent.
5. Tombstoned already: `createReplacement` with those ids. Otherwise `drafts.update`.
6. `drafts.update` refused: the existing re-fetch runs before any cleanup. Tombstoned means the replacement reuses the uploaded ids. Sent, live, missing, or a failed re-fetch means cleanup, then the error.

### Orphans

An uploaded file that no draft references is clutter in `file list`.

- An upload fails partway: delete every file id `files.getUploadURLExternal` handed out in this invocation, completed or not (`files.delete`, best effort). No draft write has happened, so nothing can reference them.
- The draft write gets an application-level refusal from Slack (`api.SlackErrorCode(err) != ""`; a rate limit is `*slack.RateLimitedError` and does not qualify): delete the uploaded files, best effort. On update this happens only after the re-fetch has ruled out the replacement branch, and a refused replacement create is treated the same way.
- Cleanup uses a fresh short context (10s), since the command's own may be the thing that expired.
- A draft write that succeeded but whose response could not be parsed deletes nothing.
- Any other failure of the draft write (timeout, network, 5xx, rate limit): the draft may exist, and deleting would leave it a dangling id. Leave the files and name their ids in the error's `detail`.
- A cleanup that itself fails also names the ids in `detail`.
- `draft update --file` and `--no-files` never delete the files they displace, and `draft delete` never deletes files: the CLI cannot tell a file it uploaded from one the user attached, and one file can sit on two drafts.

### Tombstone replacement

`createReplacement` carries `intent.fileIDs` across (one id on two drafts is accepted). The comment claiming files do not carry is removed.

### Output

- `formatDraft` always emits `file_ids` (an array, `[]` when none, never `null`).
- When Slack's response carried file objects for those ids, the row also gets `files`: `[{id, name, title, size, filetype, mimetype}]`, empty strings omitted, in `file_ids` order, decoded into a small local type (`size` as int64), joined per response. `drafts.list`, `drafts.create`, and `drafts.update` responses are decoded for the top-level `files` and joined by id. An id with no object (deleted file) appears in `file_ids` only.

### Docs

`draft create --help`, `draft update --help`, SPEC.md, `skills/slack-cli/SKILL.md`, `docs/draft-messages.md` (findings above, and correct lines 224/248/297/354 from "only seen empty"), CLAUDE.md draft bullets and the "no upload path" facts.

## Tests

Mock-level, `runWithMockSessionStdin`. The mock must serve `files.getUploadURLExternal` (returning an `upload_url` on the same httptest server), the upload POST, `files.completeUploadExternal`, `files.delete`, and model `drafts.create` answering `file_not_found` for an id that was never completed.

- create: one file, two files (order), file-only with empty stdin, file plus markdown, file plus `--table`.
- create: missing path, directory, empty file are `invalid_input` with zero requests.
- create: unresolvable recipient uploads nothing.
- create: second upload fails, first is deleted. `drafts.create` refused, files deleted. `drafts.create` 500, files kept and ids in detail.
- create: `--print-payload --file` is `invalid_input`.
- update: no flag preserves, `--file` replaces, `--no-files` clears, both is `invalid_input`, `--file` alone is a valid update, `--no-files` on a draft with no body is `invalid_blocks`.
- update: tombstoned draft with files, replacement carries the ids.
- rows: `file_ids` always present, `files` joined from the top-level array on list, create, update, and a dangling id shows in `file_ids` only.
- the uploaded bytes and filename reach the upload endpoint intact.

From the Codex review, also:

- update of an existing file-only draft: schedule-only succeeds and keeps the files, and replacing blocks with `[]` succeeds.
- second path invalid: zero requests, first path not uploaded.
- update refused, re-fetch says tombstoned: replacement reuses the uploaded ids, no `files.delete`. Re-fetch says sent: uploads deleted.
- update answers rate limit or 500: no delete, ids in `detail`.
- replacement create refused: delete. 500: keep.
- `files.completeUploadExternal` fails after an id was allocated: that id's delete is attempted.
- draft write ok with an unparseable response: no delete.
- `file_ids` serializes as `[]`, never `null`.
- multipart assertion covers bytes, base name, the `file` field name.

Rejected from the review: a `math.MaxInt` guard on file size (64-bit only targets) and any handling of slack-go's goroutine leak on a cancelled upload (the process exits).

Commit type: `feat:`.

## What changed during implementation

- `files.delete` answering `file_not_found` counts as deleted. The plan deletes every id `files.getUploadURLExternal` handed out, completed or not, and also names any id whose delete failed. An id whose upload never completed is expected to answer `file_not_found` (unmeasured), which would have put a leftover-file warning on every partial-upload failure for a file that does not exist.
- Paths must also be readable. `validateDraftFiles` opens each file. Without it an unreadable file fails only after Slack has handed out an id for it.
- On update, what happens to the uploads is always decided by the `drafts.update` error itself, after the re-fetch. The plan's step 6 says sent, live, missing, or a failed re-fetch means cleanup. That holds when the update was refused. When the update answered 500 (not systemic, so the re-fetch runs) and the draft is still live, the write may have applied, so the files stay and the `detail` names them, which is the plan's own rule for an unknown outcome and its "update answers rate limit or 500: no delete" test.
- `slackRefused` is just "the error is a typed Slack error". The transport already reports both rate-limit shapes as `*slack.RateLimitedError`, so the explicit rate-limit exclusion was unreachable.
- The row's `files` key is absent, not `[]`, when no file object joined.
- An `{ok:true}` draft write whose body does not parse deletes nothing and does not name the ids either: the error is `parse_error` as before.
- slack-go hangs on a file that fails mid-read (its error channel is unbuffered and the pipe is closed only after the send). Not handled. The local path check keeps directories and unreadable files away from it, and `--timeout` bounds the rest.
- Extra tests beyond the list: a comma in a path, an unreadable path, cleanup after the command's own `--timeout` expired, a cleanup that itself fails, `--trace` events, the active-answer join on `draft list`, and a failed write with no uploads leaving the error untouched.
