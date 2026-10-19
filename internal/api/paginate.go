package api

import (
	"context"
	"errors"
	"time"

	"github.com/slack-go/slack"
)

// maxAttempts is the number of requests made for a single page before giving
// up: one initial call plus maxAttempts-1 retries, with no wait after the
// last failure.
const maxAttempts = 5

// PageFunc fetches a single page of results given a cursor.
// Return an empty nextCursor to signal the last page.
type PageFunc[T any] func(cursor string) (items []T, nextCursor string, err error)

// Paginate collects pages of results until the cursor is exhausted or limit
// items have been collected. A limit of 0 means no limit. Rate-limited
// requests are retried up to maxAttempts times. The endpoint name is included
// in rate limit errors for diagnostics.
func Paginate[T any](ctx context.Context, endpoint string, limit uint, fetch PageFunc[T]) ([]T, error) {
	var all []T
	cursor := ""

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		items, next, err := fetchWithRetry(ctx, endpoint, cursor, fetch)
		if err != nil {
			return nil, err
		}

		all = append(all, items...)

		if limit > 0 && uint(len(all)) >= limit {
			return all[:limit], nil
		}

		if next == "" {
			return all, nil
		}
		cursor = next
	}
}

// PaginateEach fetches pages one at a time, calling fn for each batch.
// If fn returns true, pagination stops early (no error). Rate-limited
// requests are retried up to maxAttempts times.
func PaginateEach[T any](ctx context.Context, endpoint string, fetch PageFunc[T], fn func(items []T) (stop bool)) error {
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		items, next, err := fetchWithRetry(ctx, endpoint, cursor, fetch)
		if err != nil {
			return err
		}

		if fn(items) {
			return nil
		}

		if next == "" {
			return nil
		}
		cursor = next
	}
}

// FetchPage fetches one page, retrying rate-limited requests up to maxAttempts
// times and honoring Retry-After. Use it for streaming pagination, where the
// caller emits each page as it arrives instead of collecting them; Paginate
// and PaginateEach are built on the same retry behavior.
func FetchPage[T any](ctx context.Context, endpoint, cursor string, fetch PageFunc[T]) ([]T, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return fetchWithRetry(ctx, endpoint, cursor, fetch)
}

func fetchWithRetry[T any](ctx context.Context, endpoint, cursor string, fetch PageFunc[T]) ([]T, string, error) {
	var items []T
	var next string
	err := retry(ctx, endpoint, "page", func() (map[string]any, error) {
		var err error
		items, next, err = fetch(cursor)
		return map[string]any{"items": len(items), "has_more": next != ""}, err
	})
	if err != nil {
		return nil, "", err
	}
	return items, next, nil
}

// retry runs attempt up to maxAttempts times, waiting Retry-After (floor one
// second) between rate-limited attempts and giving up with a
// RateLimitExhaustedError. Any other error returns immediately. Each attempt
// emits a trace event of the given kind carrying the attempt's own fields.
func retry(ctx context.Context, endpoint, event string, attempt func() (fields map[string]any, err error)) error {
	tracer := TracerFrom(ctx)
	for n := range maxAttempts {
		start := time.Now()
		fields, err := attempt()
		attrs := map[string]any{
			"endpoint":   endpoint,
			"attempt":    n + 1,
			"latency_ms": time.Since(start).Milliseconds(),
			"error":      errString(err),
		}
		for k, v := range fields {
			attrs[k] = v
		}
		tracer.Event(event, attrs)
		if err == nil {
			return nil
		}

		var rlErr *slack.RateLimitedError
		if !errors.As(err, &rlErr) {
			return err
		}

		// Last attempt exhausted.
		if n == maxAttempts-1 {
			return &RateLimitExhaustedError{Err: err, Endpoint: endpoint, Attempts: maxAttempts}
		}

		wait := rlErr.RetryAfter
		if wait == 0 {
			wait = time.Second
		}
		tracer.Event("retry", map[string]any{
			"endpoint": endpoint,
			"attempt":  n + 1,
			"wait_ms":  wait.Milliseconds(),
		})

		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	panic("retry: loop exited without returning")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
