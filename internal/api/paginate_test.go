package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

const testEndpoint = "test.method"

func TestPaginate_SinglePage(t *testing.T) {
	calls := 0
	items, err := Paginate(context.Background(), testEndpoint, 0, func(cursor string) ([]string, string, error) {
		calls++
		return []string{"a", "b", "c"}, "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Errorf("got %d items, want 3", len(items))
	}
	if calls != 1 {
		t.Errorf("got %d calls, want 1", calls)
	}
}

func TestPaginate_MultiplePages(t *testing.T) {
	calls := 0
	items, err := Paginate(context.Background(), testEndpoint, 0, func(cursor string) ([]string, string, error) {
		calls++
		switch cursor {
		case "":
			return []string{"a", "b"}, "page2", nil
		case "page2":
			return []string{"c", "d"}, "page3", nil
		case "page3":
			return []string{"e"}, "", nil
		default:
			t.Fatalf("unexpected cursor %q", cursor)
			return nil, "", nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Errorf("got %d items, want 5", len(items))
	}
	if calls != 3 {
		t.Errorf("got %d calls, want 3", calls)
	}
}

func TestPaginate_WithLimit(t *testing.T) {
	calls := 0
	items, err := Paginate(context.Background(), testEndpoint, 3, func(cursor string) ([]string, string, error) {
		calls++
		switch cursor {
		case "":
			return []string{"a", "b"}, "page2", nil
		case "page2":
			return []string{"c", "d"}, "page3", nil
		default:
			t.Fatalf("should not fetch page with cursor %q", cursor)
			return nil, "", nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Errorf("got %d items, want 3", len(items))
	}
	// Should stop after page2 since we have enough items.
	if calls != 2 {
		t.Errorf("got %d calls, want 2", calls)
	}
}

func TestPaginate_LimitExactPage(t *testing.T) {
	items, err := Paginate(context.Background(), testEndpoint, 2, func(cursor string) ([]string, string, error) {
		return []string{"a", "b"}, "more", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Errorf("got %d items, want 2", len(items))
	}
}

func TestPaginate_EmptyResult(t *testing.T) {
	items, err := Paginate(context.Background(), testEndpoint, 0, func(cursor string) ([]string, string, error) {
		return nil, "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

func TestPaginate_FetchError(t *testing.T) {
	_, err := Paginate(context.Background(), testEndpoint, 0, func(cursor string) ([]string, string, error) {
		return nil, "", errors.New("api error")
	})
	if err == nil {
		t.Error("expected error")
	}
}

func TestPaginate_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Paginate(ctx, testEndpoint, 0, func(cursor string) ([]string, string, error) {
		return []string{"a"}, "more", nil
	})
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestPaginate_RateLimitRetrySucceeds(t *testing.T) {
	calls := 0
	items, err := Paginate(context.Background(), testEndpoint, 0, func(cursor string) ([]string, string, error) {
		calls++
		if calls == 1 {
			return nil, "", &slack.RateLimitedError{RetryAfter: time.Millisecond}
		}
		return []string{"a"}, "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("got %d items, want 1", len(items))
	}
	if calls != 2 {
		t.Errorf("got %d calls, want 2", calls)
	}
}

func TestPaginate_RateLimitExhausted(t *testing.T) {
	calls := 0
	_, err := Paginate(context.Background(), "test.endpoint", 0, func(cursor string) ([]string, string, error) {
		calls++
		return nil, "", &slack.RateLimitedError{RetryAfter: time.Millisecond}
	})
	if err == nil {
		t.Error("expected error after exhausting retries")
	}
	if calls != maxRetries {
		t.Errorf("got %d calls, want %d", calls, maxRetries)
	}
	var rlErr *RateLimitExhaustedError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *RateLimitExhaustedError, got %T", err)
	}
	if rlErr.Endpoint != "test.endpoint" {
		t.Errorf("got endpoint=%q, want %q", rlErr.Endpoint, "test.endpoint")
	}
	if rlErr.Retries != maxRetries {
		t.Errorf("got retries=%d, want %d", rlErr.Retries, maxRetries)
	}
}

func TestPaginateEach_AllPages(t *testing.T) {
	calls := 0
	var collected []string
	err := PaginateEach(context.Background(), testEndpoint, func(cursor string) ([]string, string, error) {
		calls++
		switch cursor {
		case "":
			return []string{"a", "b"}, "page2", nil
		case "page2":
			return []string{"c"}, "", nil
		default:
			t.Fatalf("unexpected cursor %q", cursor)
			return nil, "", nil
		}
	}, func(items []string) bool {
		collected = append(collected, items...)
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(collected) != 3 {
		t.Errorf("got %d items, want 3", len(collected))
	}
	if calls != 2 {
		t.Errorf("got %d calls, want 2", calls)
	}
}

func TestPaginateEach_EarlyExit(t *testing.T) {
	calls := 0
	err := PaginateEach(context.Background(), testEndpoint, func(cursor string) ([]string, string, error) {
		calls++
		switch cursor {
		case "":
			return []string{"a", "b"}, "page2", nil
		case "page2":
			return []string{"target", "d"}, "page3", nil
		default:
			t.Fatalf("should not fetch cursor %q", cursor)
			return nil, "", nil
		}
	}, func(items []string) bool {
		for _, item := range items {
			if item == "target" {
				return true
			}
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("got %d calls, want 2 (should stop after finding target)", calls)
	}
}

func TestPaginateEach_FetchError(t *testing.T) {
	err := PaginateEach(context.Background(), testEndpoint, func(cursor string) ([]string, string, error) {
		return nil, "", errors.New("api error")
	}, func(items []string) bool {
		return false
	})
	if err == nil {
		t.Error("expected error")
	}
}

func TestPaginateEach_RateLimitRetry(t *testing.T) {
	calls := 0
	var collected []string
	err := PaginateEach(context.Background(), testEndpoint, func(cursor string) ([]string, string, error) {
		calls++
		if calls == 1 {
			return nil, "", &slack.RateLimitedError{RetryAfter: time.Millisecond}
		}
		return []string{"a"}, "", nil
	}, func(items []string) bool {
		collected = append(collected, items...)
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(collected) != 1 {
		t.Errorf("got %d items, want 1", len(collected))
	}
	if calls != 2 {
		t.Errorf("got %d calls, want 2", calls)
	}
}

func TestPaginate_RateLimitContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	_, err := Paginate(ctx, testEndpoint, 0, func(cursor string) ([]string, string, error) {
		calls++
		if calls == 1 {
			// Cancel context before the retry sleep.
			cancel()
			return nil, "", &slack.RateLimitedError{RetryAfter: time.Hour}
		}
		return []string{"a"}, "", nil
	})
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}
