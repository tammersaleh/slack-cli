package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestCall_Success(t *testing.T) {
	calls := 0
	err := Call(context.Background(), testEndpoint, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("got %d calls, want 1", calls)
	}
}

func TestCall_NonRateLimitErrorReturnsImmediately(t *testing.T) {
	calls := 0
	want := errors.New("cant_invite")
	err := Call(context.Background(), testEndpoint, func() error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected the fn error back unchanged, got %v", err)
	}
	if calls != 1 {
		t.Errorf("got %d calls, want 1 (no retry for a non-rate-limit error)", calls)
	}
}

func TestCall_RateLimitRetrySucceeds(t *testing.T) {
	calls := 0
	err := Call(context.Background(), testEndpoint, func() error {
		calls++
		if calls == 1 {
			return &slack.RateLimitedError{RetryAfter: time.Millisecond}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("got %d calls, want 2", calls)
	}
}

func TestCall_RateLimitExhausted(t *testing.T) {
	calls := 0
	err := Call(context.Background(), "write.endpoint", func() error {
		calls++
		return &slack.RateLimitedError{RetryAfter: time.Millisecond}
	})
	if calls != maxAttempts {
		t.Errorf("got %d calls, want %d", calls, maxAttempts)
	}
	var rlErr *RateLimitExhaustedError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *RateLimitExhaustedError, got %T: %v", err, err)
	}
	if rlErr.Endpoint != "write.endpoint" || rlErr.Attempts != maxAttempts {
		t.Errorf("got endpoint=%q attempts=%d", rlErr.Endpoint, rlErr.Attempts)
	}
}

// A cancelled context must stop the call before spending a request, the same
// guarantee FetchPage gives --timeout.
func TestCall_ContextCancelledBeforeCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := Call(ctx, testEndpoint, func() error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if calls != 0 {
		t.Errorf("fn ran %d times after cancellation, want 0", calls)
	}
}

// attrTracer keeps each event's attributes so payloads can be asserted.
type attrTracer struct {
	kinds []string
	attrs []map[string]any
}

func (a *attrTracer) Event(kind string, attrs map[string]any) {
	a.kinds = append(a.kinds, kind)
	a.attrs = append(a.attrs, attrs)
}

// Call emits "call" events, not "page" events: it has no items or cursor, and
// a --trace reader sorting writes from pagination needs to tell them apart.
// The retry event's attempt is the request that was rate limited.
func TestCall_TraceEvents(t *testing.T) {
	rec := &attrTracer{}
	ctx := WithTracer(context.Background(), rec)
	calls := 0
	err := Call(ctx, testEndpoint, func() error {
		calls++
		if calls == 1 {
			return &slack.RateLimitedError{RetryAfter: 2 * time.Millisecond}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"call", "retry", "call"}
	if len(rec.kinds) != len(want) {
		t.Fatalf("events = %v, want %v", rec.kinds, want)
	}
	for i := range want {
		if rec.kinds[i] != want[i] {
			t.Fatalf("events = %v, want %v", rec.kinds, want)
		}
	}
	first := rec.attrs[0]
	if first["endpoint"] != testEndpoint || first["attempt"] != 1 || first["error"] != "slack rate limit exceeded, retry after 2ms" {
		t.Errorf("first call attrs = %v", first)
	}
	if _, ok := first["latency_ms"]; !ok {
		t.Errorf("first call attrs missing latency_ms: %v", first)
	}
	for _, k := range []string{"items", "has_more"} {
		if _, ok := first[k]; ok {
			t.Errorf("call event carries pagination field %s: %v", k, first)
		}
	}
	retry := rec.attrs[1]
	if retry["endpoint"] != testEndpoint || retry["attempt"] != 1 || retry["wait_ms"] != int64(2) {
		t.Errorf("retry attrs = %v", retry)
	}
	last := rec.attrs[2]
	if last["attempt"] != 2 || last["error"] != "" {
		t.Errorf("second call attrs = %v", last)
	}
}

// Retry-After: 0 still waits the one-second floor, as FetchPage does.
func TestCall_RateLimitZeroRetryAfterStillWaits(t *testing.T) {
	calls := 0
	start := time.Now()
	err := Call(context.Background(), testEndpoint, func() error {
		calls++
		if calls == 1 {
			return &slack.RateLimitedError{RetryAfter: 0}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("got %d calls, want 2", calls)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Errorf("retried after %v, want at least the 1s floor", time.Since(start))
	}
}

// Cancelling during the retry wait returns promptly with the context error.
func TestCall_RateLimitContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := Call(ctx, testEndpoint, func() error {
		calls++
		cancel()
		return &slack.RateLimitedError{RetryAfter: time.Hour}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Errorf("got %d calls, want 1", calls)
	}
}
