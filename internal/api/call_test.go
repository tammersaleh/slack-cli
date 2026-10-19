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

// Call emits "call" events, not "page" events: it has no items or cursor, and
// a --trace reader sorting writes from pagination needs to tell them apart.
func TestCall_TraceEvents(t *testing.T) {
	rec := &recordingTracer{}
	ctx := WithTracer(context.Background(), rec)
	calls := 0
	err := Call(ctx, testEndpoint, func() error {
		calls++
		if calls == 1 {
			return &slack.RateLimitedError{RetryAfter: time.Millisecond}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"call", "retry", "call"}
	if len(rec.events) != len(want) {
		t.Fatalf("events = %v, want %v", rec.events, want)
	}
	for i := range want {
		if rec.events[i] != want[i] {
			t.Errorf("events = %v, want %v", rec.events, want)
			break
		}
	}
}
