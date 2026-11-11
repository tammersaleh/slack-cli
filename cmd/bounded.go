package cmd

import (
	"context"
	"sync"
)

// runBounded runs fn for every index in [0, n) with at most limit in flight,
// and returns once every worker has finished.
//
// It is the fail-closed half of a per-item lookup: fn returns nil when it has
// recorded its own index's outcome (resolved, or marked item-local), and an
// error when the failure voids the whole batch. The first such error cancels
// the context the remaining workers run on and is the one returned - siblings
// woken by the cancel report context.Canceled, which must not overwrite it.
// With no such error, the caller's own context error is returned if it has
// one, since a deadline expiring is not a batch of missing items.
//
// This bounds concurrent work, not request rate: it is not a rate limiter.
// Workers are always joined before returning, because a caller reads the
// outcomes fn wrote and a live worker would still be mutating them and still
// calling Slack.
func runBounded(ctx context.Context, n, limit int, fn func(ctx context.Context, i int) error) error {
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		fatalOnce sync.Once
		fatalErr  error
	)
	fail := func(err error) {
		fatalOnce.Do(func() {
			fatalErr = err
			cancel()
		})
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-batchCtx.Done():
				return
			}
			defer func() { <-sem }()

			// A select with both cases ready can pick the slot over a done
			// context, so re-check before spending a request.
			if batchCtx.Err() != nil {
				return
			}

			if err := fn(batchCtx, i); err != nil {
				fail(err)
			}
		}(i)
	}

	wg.Wait()

	if fatalErr != nil {
		return fatalErr
	}
	return ctx.Err()
}
