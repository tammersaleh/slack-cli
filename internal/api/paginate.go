package api

import (
	"context"
	"errors"
	"time"

	"github.com/slack-go/slack"
)

const maxRetries = 5

// PageFunc fetches a single page of results given a cursor.
// Return an empty nextCursor to signal the last page.
type PageFunc[T any] func(cursor string) (items []T, nextCursor string, err error)

// Paginate collects pages of results until the cursor is exhausted or limit
// items have been collected. A limit of 0 means no limit. Rate-limited
// requests are retried up to maxRetries times.
func Paginate[T any](ctx context.Context, limit uint, fetch PageFunc[T]) ([]T, error) {
	var all []T
	cursor := ""

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		items, next, err := fetchWithRetry(ctx, cursor, fetch)
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

func fetchWithRetry[T any](ctx context.Context, cursor string, fetch PageFunc[T]) ([]T, string, error) {
	for attempt := range maxRetries {
		items, next, err := fetch(cursor)
		if err == nil {
			return items, next, nil
		}

		var rlErr *slack.RateLimitedError
		if !errors.As(err, &rlErr) {
			return nil, "", err
		}

		// Last attempt exhausted.
		if attempt == maxRetries-1 {
			return nil, "", err
		}

		wait := rlErr.RetryAfter
		if wait == 0 {
			wait = time.Second
		}

		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	// unreachable
	return nil, "", nil
}
