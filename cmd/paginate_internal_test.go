package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// scriptedPages returns a fetch func that serves pages from a script keyed by
// cursor, and records the cursor of every call it receives.
func scriptedPages(t *testing.T, script map[string]struct {
	items []string
	next  string
	err   error
}, seen *[]string) func(string) ([]string, string, error) {
	t.Helper()
	return func(cursor string) ([]string, string, error) {
		*seen = append(*seen, cursor)
		page, ok := script[cursor]
		if !ok {
			t.Fatalf("fetch called with unscripted cursor %q", cursor)
		}
		return page.items, page.next, page.err
	}
}

type page = struct {
	items []string
	next  string
	err   error
}

func newTestPrinter() (*output.Printer, *bytes.Buffer) {
	var buf bytes.Buffer
	return &output.Printer{Out: &buf, Err: &bytes.Buffer{}}, &buf
}

func printAll(p *output.Printer) func([]string) error {
	return func(items []string) error {
		for _, it := range items {
			if err := p.PrintItem(map[string]any{"v": it}); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestStreamPages_SinglePageNoContinuation(t *testing.T) {
	p, buf := newTestPrinter()
	var seen []string
	fetch := scriptedPages(t, map[string]page{"": {items: []string{"a", "b"}}}, &seen)

	if err := streamPages(context.Background(), &CLI{}, p, "test.list", "", true, fetch, printAll(p)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `{"v":"a"}
{"v":"b"}
{"_meta":{"has_more":false}}
`
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// Without --all the loop stops after one page and reports the continuation.
func TestStreamPages_StopsWithoutAll(t *testing.T) {
	p, buf := newTestPrinter()
	var seen []string
	fetch := scriptedPages(t, map[string]page{"": {items: []string{"a"}, next: "c2"}}, &seen)

	if err := streamPages(context.Background(), &CLI{}, p, "test.list", "", false, fetch, printAll(p)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != 1 {
		t.Errorf("expected exactly one fetch without --all, got cursors %v", seen)
	}
	if !strings.Contains(buf.String(), `{"_meta":{"has_more":true,"next_cursor":"c2"}}`) {
		t.Errorf("expected a continuation trailer, got:\n%s", buf.String())
	}
}

// --all walks the exact cursor chain and writes one trailer at the end.
func TestStreamPages_WalksCursorChain(t *testing.T) {
	p, buf := newTestPrinter()
	var seen []string
	fetch := scriptedPages(t, map[string]page{
		"":   {items: []string{"a"}, next: "c2"},
		"c2": {items: []string{"b"}, next: "c3"},
		"c3": {items: []string{"c"}},
	}, &seen)

	if err := streamPages(context.Background(), &CLI{}, p, "test.list", "", true, fetch, printAll(p)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(seen, ",") != ",c2,c3" {
		t.Errorf("unexpected cursor sequence: %v", seen)
	}
	if n := strings.Count(buf.String(), "_meta"); n != 1 {
		t.Errorf("expected exactly one trailer, got %d in:\n%s", n, buf.String())
	}
	if !strings.HasSuffix(buf.String(), `{"_meta":{"has_more":false}}`+"\n") {
		t.Errorf("expected the trailer last, got:\n%s", buf.String())
	}
}

// A page whose items are all filtered out still continues the walk: Slack's
// cursor, not the count of emitted lines, decides whether more pages exist.
func TestStreamPages_EmptyFilteredPageContinues(t *testing.T) {
	p, buf := newTestPrinter()
	var seen []string
	fetch := scriptedPages(t, map[string]page{
		"":   {items: []string{"skip"}, next: "c2"},
		"c2": {items: []string{"keep"}},
	}, &seen)

	emit := func(items []string) error {
		for _, it := range items {
			if it == "skip" {
				continue
			}
			if err := p.PrintItem(map[string]any{"v": it}); err != nil {
				return err
			}
		}
		return nil
	}

	if err := streamPages(context.Background(), &CLI{}, p, "test.list", "", true, fetch, emit); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != 2 {
		t.Errorf("expected the walk to continue past a fully filtered page, got %v", seen)
	}
	if !strings.Contains(buf.String(), `{"v":"keep"}`) {
		t.Errorf("expected the second page's item, got:\n%s", buf.String())
	}
}

// A transport failure is resumable: the trailer names the failed page so the
// caller can continue from there.
func TestStreamPages_TransportFailureIsResumable(t *testing.T) {
	p, buf := newTestPrinter()
	var seen []string
	boom := errors.New("connection reset")
	fetch := scriptedPages(t, map[string]page{
		"":   {items: []string{"a"}, next: "c2"},
		"c2": {err: boom},
	}, &seen)

	err := streamPages(context.Background(), &CLI{}, p, "test.list", "", true, fetch, printAll(p))
	if err == nil {
		t.Fatal("expected the fetch failure to propagate")
	}

	if !strings.Contains(buf.String(), `{"v":"a"}`) {
		t.Errorf("expected the fetched page to remain on stdout, got:\n%s", buf.String())
	}
	// unknown_error, not "connection reset": _meta.error is a machine code, and
	// a bare error is exactly the shape ClassifyError has no recognizer for.
	if !strings.HasSuffix(buf.String(), `{"_meta":{"has_more":true,"next_cursor":"c2","error":"unknown_error"}}`+"\n") {
		t.Errorf("expected a resumable failure trailer, got:\n%s", buf.String())
	}
}

// An error the fetch closure raised itself describes the request, not a page
// that failed to arrive - so the trailer marks the stream over with no resume
// point rather than inviting a pointless retry.
func TestStreamPages_DomainFailureIsNotResumable(t *testing.T) {
	p, buf := newTestPrinter()
	var seen []string
	domain := &output.Error{Err: "thread_not_found", Detail: "no such thread", Code: output.ExitGeneral}
	fetch := scriptedPages(t, map[string]page{"": {err: domain}}, &seen)

	err := streamPages(context.Background(), &CLI{}, p, "test.list", "", true, fetch, printAll(p))

	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Detail != "no such thread" {
		t.Fatalf("expected the structured error to pass through intact, got %#v", err)
	}
	if buf.String() != `{"_meta":{"has_more":false,"error":"thread_not_found"}}`+"\n" {
		t.Errorf("expected a terminal failure trailer, got:\n%s", buf.String())
	}
}

// A cursor that repeats would loop forever under --all, and --timeout is off
// by default. The walk stops and says so.
func TestStreamPages_RejectsRepeatedCursor(t *testing.T) {
	p, buf := newTestPrinter()
	var seen []string
	fetch := scriptedPages(t, map[string]page{
		"":   {items: []string{"a"}, next: "c2"},
		"c2": {items: []string{"b"}, next: "c2"},
	}, &seen)

	err := streamPages(context.Background(), &CLI{}, p, "test.list", "", true, fetch, printAll(p))

	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "pagination_error" {
		t.Fatalf("expected a pagination_error, got %#v", err)
	}
	if len(seen) != 2 {
		t.Errorf("expected the walk to stop on the repeat, got cursors %v", seen)
	}
	if !strings.Contains(buf.String(), `"error":"pagination_error"`) {
		t.Errorf("expected the trailer to name the failure, got:\n%s", buf.String())
	}
}

// A cycle that returns to the caller's own --cursor resume point is caught on
// the first lap, not after going round again.
func TestStreamPages_RejectsCycleBackToStartCursor(t *testing.T) {
	p, _ := newTestPrinter()
	var seen []string
	fetch := scriptedPages(t, map[string]page{
		"start": {items: []string{"a"}, next: "start"},
	}, &seen)

	err := streamPages(context.Background(), &CLI{}, p, "test.list", "start", true, fetch, printAll(p))

	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "pagination_error" {
		t.Fatalf("expected a pagination_error, got %#v", err)
	}
	if len(seen) != 1 {
		t.Errorf("expected the cycle caught after one fetch, got cursors %v", seen)
	}
}

// failingWriter fails every write after the first n.
type failingWriter struct {
	ok  int
	err error
}

func (w *failingWriter) Write(b []byte) (int, error) {
	if w.ok > 0 {
		w.ok--
		return len(b), nil
	}
	return 0, w.err
}

// When stdout itself fails there is nobody to read a trailer, and the failed
// line may be half-written - so the helper stops rather than appending one.
func TestStreamPages_StdoutFailureWritesNoTrailer(t *testing.T) {
	w := &failingWriter{ok: 1, err: errors.New("broken pipe")}
	p := &output.Printer{Out: w, Err: &bytes.Buffer{}}
	var seen []string
	fetch := scriptedPages(t, map[string]page{
		"": {items: []string{"a", "b"}, next: "c2"},
	}, &seen)

	err := streamPages(context.Background(), &CLI{}, p, "test.list", "", true, fetch, printAll(p))
	if err == nil || err.Error() != "broken pipe" {
		t.Fatalf("expected the write error to propagate unchanged, got %v", err)
	}
	if len(seen) != 1 {
		t.Errorf("expected no further page fetch after a stdout failure, got %v", seen)
	}
}

// Items returned alongside an error are discarded, never printed.
func TestStreamPages_DiscardsItemsOnError(t *testing.T) {
	p, buf := newTestPrinter()
	var seen []string
	fetch := scriptedPages(t, map[string]page{
		"": {items: []string{"ghost"}, err: errors.New("boom")},
	}, &seen)

	if err := streamPages(context.Background(), &CLI{}, p, "test.list", "", true, fetch, printAll(p)); err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(buf.String(), "ghost") {
		t.Errorf("items returned with an error must not be emitted, got:\n%s", buf.String())
	}
}

// pageCursorFetch maps the page-number APIs onto the same walk. An empty
// cursor is page 1, and the next page comes from the response's paging block.
func TestPageCursorFetch_TranslatesPages(t *testing.T) {
	tests := []struct {
		name     string
		cursor   string
		page     int
		pages    int
		wantPage int
		wantNext string
	}{
		{"empty cursor is page 1", "", 1, 3, 1, "2"},
		{"explicit page", "2", 2, 3, 2, "3"},
		{"last page has no next", "3", 3, 3, 3, ""},
		{"single page", "", 1, 1, 1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPage := 0
			fetch := pageCursorFetch(func(page int) ([]string, *slack.Paging, error) {
				gotPage = page
				return []string{"x"}, &slack.Paging{Page: tt.page, Pages: tt.pages}, nil
			})
			_, next, err := fetch(tt.cursor)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotPage != tt.wantPage {
				t.Errorf("requested page %d, want %d", gotPage, tt.wantPage)
			}
			if next != tt.wantNext {
				t.Errorf("got next cursor %q, want %q", next, tt.wantNext)
			}
		})
	}
}

func TestPageCursorFetch_RejectsBadCursor(t *testing.T) {
	for _, cursor := range []string{"0", "-1", "2x", "abc"} {
		t.Run(cursor, func(t *testing.T) {
			called := false
			fetch := pageCursorFetch(func(page int) ([]string, *slack.Paging, error) {
				called = true
				return nil, nil, nil
			})
			if _, _, err := fetch(cursor); err == nil {
				t.Errorf("expected cursor %q to be rejected", cursor)
			}
			if called {
				t.Errorf("cursor %q must be rejected before any API call", cursor)
			}
		})
	}
}
