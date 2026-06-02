# Profile + manager-chain feature plan

Status: in progress. Resume from "Steps" checklist below.

## Goal

Surface the rich org data Slack's desktop profile panel shows (Title, Manager,
Division, Department, Cost Center, Employee ID, GitHub Handle, Start Date) which
`users.info` does NOT return - it comes back as `profile.fields: []`. That data
lives in custom profile fields, returned only by `users.profile.get`.

Two deliverables:

1. `slack user info --full <user>...` - adds custom profile fields (additive).
2. `slack user manager-chain <user>...` - walks the Manager field upward.

## Verified API findings (live, Enterprise Grid CoreWeave workspace)

- `users.profile.get` with `include_labels=true` returns `profile.fields` as a
  map keyed by opaque field ID (`Xf06EJKXRBAA`), each `{value, alt, label}`.
  One call per user; no separate `team.profile.get` needed.
- slack-go wraps it: `client.GetUserProfileContext(ctx,
  &slack.GetUserProfileParameters{UserID, IncludeLabels:true})` →
  `*slack.UserProfile`. `UserProfile.FieldsMap()` → `map[string]UserProfileCustomField{Value,Alt,Label}`.
  `UserProfile` also carries `RealName`, `DisplayName`, `Title` directly.
- Public documented method. Works on the existing client (desktop xoxc session
  token + cookie; OAuth bot token needs `users.profile:read` - NOT currently
  requested in internal/auth/oauth.go, must add + document re-auth).
- Manager field: label "Manager", value is a user ID (`U09KU7J7TA5`). alt empty.
- "Direct Reports" field exists (type user) but is EMPTY for everyone - SCIM
  doesn't populate it. So downward traversal is impossible. Manager chain walks
  UP only. Verified: John Mancuso → Jon Jones (CRO) → Michael Intrator (CEO) →
  (no manager, terminates).
- NO dedicated org-chart endpoint (users.profile.getOrgChart / atlas.* etc all
  return `unknown_method`).

## Field ID reference (CoreWeave, may differ per workspace - do not hardcode)

Manager=Xf06EJKXRBAA, Division=Xf06FE2VS908, Department=Xf06ER62KJCB,
Cost Center=Xf06ER9D3ABC, Employee ID=Xf06ENBH703X, GitHub Handle=Xf06ETNPGHHA.
Detect by label, never by ID.

## Decisions (owner + Codex review, thread 019e88b4)

- Manager walk = separate subcommand `slack user manager-chain`, NOT a flag.
  Rationale: emits N rows per input (one per level) vs user info's one-row-per-user
  contract. "org-chart" name rejected (overpromises; upward-only).
- Profile flag = `--full` on `user info` (owner pick).
- Detect manager field by label "Manager" (case-insensitive), `--manager-field`
  override on manager-chain. Default "Manager".
- Add `users.profile:read` to OAuth bot scopes; document re-auth.

### `slack user info --full <user>...`

Existing slack.User row + `input` unchanged. ADD top-level `custom_fields` object
keyed by normalized snake_case label:

```json
"custom_fields": {
  "manager": {"id":"Xf06EJKXRBAA","label":"Manager","value":"U09KU7J7TA5","alt":"","value_name":"Jon Jones"},
  "division": {"id":"Xf06FE2VS908","label":"Division","value":"Technology","alt":""}
}
```

- Preserve `id`, `label`, `value`, `alt` always. Add `value_name` only when value
  matches the resolver user-ID pattern (`^[UW][A-Z0-9]+$`, reuse via exported
  `resolve.IsUserID`) AND resolves via LookupUser.
- Do NOT mutate `profile.fields` (stays `[]`). Additive only. `--fields` is
  top-level-only so `custom_fields` is selectable.
- Deterministic collision handling: collect fields, sort by (label, field ID),
  assign snake_case keys, suffix `_2`,`_3` on collision. Empty normalized label →
  `field_<id>`. (Go map iteration is random - must sort or suffixes flap.)
- Per-item failure (item-specific, e.g. user_not_found): error row on stdout
  `{input, error, hint}`, error_count++, exit nonzero. Base row NOT emitted.
- Systemic failure (missing_scope / auth / rate_limited / network): fail fast
  once with hint, do NOT spam a row per input. missing_scope hint: re-auth with
  updated scopes.

### `slack user manager-chain <user>...`

One flat row per level:

```json
{"input":"@alice","root_user_id":"U01","level":0,"id":"U01","display_name":"alice","real_name":"Alice","title":"AE","manager_id":"U02","manager_name":"Jon Jones"}
{"input":"@alice","root_user_id":"U01","level":1,"id":"U02","display_name":"jjones","real_name":"Jon Jones","title":"CRO","manager_id":"U03","manager_name":"Michael Intrator"}
{"input":"@alice","root_user_id":"U01","level":2,"id":"U03","display_name":"mintrator","real_name":"Michael Intrator","title":"CEO","stop_reason":"no_manager"}
```

- Use `id` NOT `user_id` (printer auto-enriches `user_id`→`user_name`, re-adding
  a lookup we're avoiding). `manager_id` is safe (not in enrich map); add
  `manager_name` manually. Same for not emitting `user`/`channel`/`channel_id`.
- Fields per row: input, root_user_id, level, id, display_name, real_name, title,
  manager_id, manager_name. Drop `name` (@handle not in profile.get; would cost a
  lookup). One profile.get per hop gives id-of-manager + title + real_name +
  display_name.
- stop_reason on terminal row only:
  - `no_manager` - NOT an error (empty or absent manager field collapse to this)
  - `invalid_manager_value` (field present but not a user ID) - error
  - `cycle_detected` - error
  - `max_depth` (cap 20 rows; check is `len(chain) >= 20`) - error
  - `profile_lookup_failed` - error
  - errors increment _meta.error_count; command exits nonzero.
- Manager field match: 0 → no_manager; 1 → use; >1 → ambiguous_manager_field
  error (semantic ambiguity, don't silently pick).
- Command-local profile cache keyed by user ID (shared manager fetched once
  across multiple inputs / repeated hops).
- Cycle guard: seen-set of user IDs.
- Strict lookup: profile.get is the hop driver. manager_name via LookupUser
  (best-effort; if it misses, leave manager_name absent, manager_id still emitted).

## Files

- cmd/user.go - add `--full` to UserInfoCmd; add UserManagerChainCmd + wire into UserCmd.
- cmd/user_test.go - tests (red first).
- internal/resolve/user.go - export `IsUserID(s string) bool` reusing userIDPattern.
- internal/auth/oauth.go - add `users.profile:read` scope.
- SPEC.md - document both under ### user.
- skills/slack-cli/SKILL.md - steer agents: common case wants `--full`; add
  manager-chain. Update cmd/skill_test.go regression assertions.
- docs/profile-feature-plan.md - this file.

## Steps

- [x] Export resolve.IsUserID.
- [x] Add users.profile:read to OAuth scopes.
- [x] RED + GREEN: `user info --full` (custom_fields shape, value_name resolution,
      collision determinism, empty-drop, systemic-vs-item error). cmd/user_unit_test.go
      + cmd/user_test.go.
- [x] RED + GREEN: manager-chain (walk, no_manager, cycle, ambiguous, invalid value,
      custom field override). cmd/user_test.go.
- [x] mise run test + mise run lint green.
- [x] SPEC.md + SKILL.md + skill_test.go.
- [ ] Code review (feature-dev:code-reviewer), address, re-review clean.
- [ ] Commit (conventional: feat:), merge main, push.
- [ ] Wait for release, brew upgrade, verify `slack user info --full` + manager-chain
      against installed binary. skills update for SKILL.md.
- [ ] Retrospective: update CLAUDE.md.

## Implementation notes (as built)

- cmd/user.go: `--full` flag on UserInfoCmd; helpers normalizeFieldKey,
  buildCustomFields (deterministic, sorted by field ID), customField type,
  userDisplayName, isSystemicErr. UserManagerChainCmd with walk/formatChain/
  managerReference (mgrOK/Absent/Invalid/Ambiguous). managerChainMaxDepth=20.
- manager-chain rows use `id` (NOT user_id) to dodge printer auto-enrichment;
  manager_name pulled from the next hop's profile (no extra lookup).
- isSystemicErr: ExitAuth OR err in {missing_scope, ratelimited, rate_limited,
  account_inactive, token_revoked} → fail fast.

## Commit plan (small, conventional)

1. refactor: export resolve.IsUserID (or fold into the feat).
2. feat: add users.profile:read OAuth scope (if separable) - actually fold into feat.
3. feat: user info --full (custom profile fields).
4. feat: user manager-chain.
5. docs: SPEC + SKILL (docs: ships SKILL via skills update; SPEC docs-only).

Likely combine 1+3 and keep manager-chain separate. feat: triggers release.
