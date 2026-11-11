package api

import (
	"context"
	"time"
)

// Call runs one non-paginated request with the same rate-limit retry as
// FetchPage: up to maxAttempts attempts honoring Retry-After, a
// RateLimitExhaustedError naming the endpoint when they run out, and any
// other error returned as-is. It checks the context before spending a request
// so --timeout stops a run promptly. Trace events are "call", not "page" -
// a write has no items or cursor, and a --trace reader should be able to tell
// writes from pagination.
func Call(ctx context.Context, endpoint string, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return retry(ctx, endpoint, "call", func() (map[string]any, error) {
		return nil, fn()
	})
}

// CallOnce runs one request exactly once, for a write that is not safe to
// repeat. A rate limit comes back as the bare error instead of being retried:
// if the first request did run, the repeat would report a failure (a name
// already taken, say) for work that succeeded. It checks the context first
// and emits the same "call" trace event as Call, always with attempt 1.
func CallOnce(ctx context.Context, endpoint string, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	start := time.Now()
	err := fn()
	TracerFrom(ctx).Event("call", map[string]any{
		"endpoint":   endpoint,
		"attempt":    1,
		"latency_ms": time.Since(start).Milliseconds(),
		"error":      errString(err),
	})
	return err
}
