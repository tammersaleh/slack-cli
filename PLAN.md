# QA Fixes Plan

Status: Changes 1 and 3 complete. Change 2 deferred to Phase 2.

## 1. Channel resolver: early exit + file cache - DONE

Page-by-page resolution with early exit. File cache at `~/.config/slack-cli/cache/channels-{teamID}.json` (1h TTL). In-memory cache (5min TTL) for session reuse.

## 2. Email lookup on Enterprise Grid - DEFERRED

`users.lookupByEmail` fails with `xoxc-` (desktop) tokens on Enterprise Grid. Root cause: scope limitation on session tokens.

Switching to username/display-name resolution requires paginating `users.list` (expensive, same problem as channels). This is Phase 2 work per CLAUDE.md.

## 3. Rate limit error endpoint field - DONE

`Paginate` and `PaginateEach` accept an endpoint name. `RateLimitExhaustedError` carries the endpoint through to `ClassifyError` output.
