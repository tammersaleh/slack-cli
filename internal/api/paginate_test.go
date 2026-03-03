package api

import (
	"context"
	"errors"
	"testing"
)

func TestPaginate_SinglePage(t *testing.T) {
	calls := 0
	items, err := Paginate(context.Background(), 0, func(cursor string) ([]string, string, error) {
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
	items, err := Paginate(context.Background(), 0, func(cursor string) ([]string, string, error) {
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
	items, err := Paginate(context.Background(), 3, func(cursor string) ([]string, string, error) {
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
	items, err := Paginate(context.Background(), 2, func(cursor string) ([]string, string, error) {
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
	items, err := Paginate(context.Background(), 0, func(cursor string) ([]string, string, error) {
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
	_, err := Paginate(context.Background(), 0, func(cursor string) ([]string, string, error) {
		return nil, "", errors.New("api error")
	})
	if err == nil {
		t.Error("expected error")
	}
}

func TestPaginate_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Paginate(ctx, 0, func(cursor string) ([]string, string, error) {
		return []string{"a"}, "more", nil
	})
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}
