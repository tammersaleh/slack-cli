package cmd

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunBounded_Empty(t *testing.T) {
	if err := runBounded(context.Background(), 0, 3, func(context.Context, int) error {
		t.Error("fn must not run for n = 0")
		return nil
	}); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

// The first workers wait until limit of them are in flight, so the limit is
// shown to be reached as well as respected: an unbounded loop lets more than
// limit pile up at the gate, and a tighter one never opens it.
func TestRunBounded_RunsEveryIndexWithinLimit(t *testing.T) {
	const n, limit = 40, 4
	seen := make([]int32, n)
	var inFlight, peak int32
	gate := make(chan struct{})
	var open sync.Once
	err := runBounded(context.Background(), n, limit, func(_ context.Context, i int) error {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
				break
			}
		}
		if cur >= limit {
			open.Do(func() { close(gate) })
		}
		select {
		case <-gate:
		case <-time.After(2 * time.Second):
			t.Errorf("index %d: never saw %d workers in flight", i, limit)
		}
		atomic.AddInt32(&seen[i], 1)
		atomic.AddInt32(&inFlight, -1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range seen {
		if c != 1 {
			t.Errorf("index %d ran %d times, want 1", i, c)
		}
	}
	if peak != limit {
		t.Errorf("peak in flight = %d, want %d", peak, limit)
	}
}

// The sibling fails only because the first failure cancelled it, so its
// context.Canceled always arrives second and must not replace the cause.
func TestRunBounded_FirstCauseSurvivesSiblingCancel(t *testing.T) {
	cause := errors.New("cause")
	started := make(chan struct{})
	err := runBounded(context.Background(), 2, 2, func(ctx context.Context, i int) error {
		if i == 0 {
			<-started
			return cause
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, cause) {
		t.Fatalf("got %v, want the first cause", err)
	}
}

// With no worker failing, the caller's own dead context is the error.
func TestRunBounded_CallerContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := runBounded(ctx, 3, 3, func(context.Context, int) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
