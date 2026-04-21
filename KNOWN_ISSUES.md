# Known Issues

## `thread list` with narrow `--fields` is CPU-bound (2026-04-21)

On a 62-message thread, `--fields ts` takes ~5s of CPU;
`--fields user,ts,blocks` takes ~0.2s. Scales linearly with message
count (~80ms per message). Whenever `user` is absent from `--fields`,
something in the per-item pipeline does ~80ms of extra CPU work. Not
tied to enrichment - happens even when no resolver fields are present.

### Reproduction

```bash
# 5s CPU
time slack thread list C07TYQ7LVUH 1773191599.103689 --all \
  --fields ts --workspace E06F3U108F3 >/dev/null

# 0.2s CPU
time slack thread list C07TYQ7LVUH 1773191599.103689 --all \
  --fields user,ts,blocks --workspace E06F3U108F3 >/dev/null

# 0.2s CPU (no --fields at all)
time slack thread list C07TYQ7LVUH 1773191599.103689 --all \
  --workspace E06F3U108F3 >/dev/null
```

### Workaround

Include `user` in `--fields` if you need a narrow field set.
