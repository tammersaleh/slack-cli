package api

import "context"

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
