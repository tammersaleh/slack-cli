package cmd_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

func savedFixture(n int) []map[string]any {
	items := make([]map[string]any, 0, n)
	for i := range n {
		items = append(items, map[string]any{
			"item_id":      "C01ABC",
			"ts":           fmt.Sprintf("17092512%02d.000100", i),
			"date_created": 1709251200,
			"todo_state":   "uncompleted",
		})
	}
	return items
}

func savedRows(t *testing.T, out string) (rows []map[string]any, meta map[string]any) {
	t.Helper()
	for _, l := range nonEmptyLines(out) {
		m := parseJSON(t, l)
		if mm, ok := m["_meta"].(map[string]any); ok {
			if meta != nil {
				t.Fatalf("more than one _meta trailer:\n%s", out)
			}
			meta = mm
			continue
		}
		rows = append(rows, m)
	}
	if meta == nil {
		t.Fatalf("no _meta trailer:\n%s", out)
	}
	return rows, meta
}

// assertSavedLimit checks every recorded saved.list body carries `limit` with
// the wanted value and no `count`, the name Slack ignores.
func assertSavedLimit(t *testing.T, bodies []map[string]any, wantRequests, wantLimit int) {
	t.Helper()
	if len(bodies) != wantRequests {
		t.Fatalf("saved.list requests = %d, want %d", len(bodies), wantRequests)
	}
	for i, b := range bodies {
		if got, ok := b["limit"].(float64); !ok || int(got) != wantLimit {
			t.Errorf("request %d: limit = %v, want %d", i, b["limit"], wantLimit)
		}
		if _, ok := b["count"]; ok {
			t.Errorf("request %d: sent count, which saved.list ignores", i)
		}
	}
}

func TestSavedList_LimitSetsThePageSize(t *testing.T) {
	rec := &savedListRequests{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", savedListRecordingHandler(t, rec, savedFixture(25), map[string]any{}, ""))

	out, err := runWithMockSession(t, mux, "saved", "list", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}

	rows, meta := savedRows(t, out)
	if len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
	if meta["has_more"] != true || meta["next_cursor"] != "2" {
		t.Errorf("trailer = %v, want has_more with next_cursor 2", meta)
	}
	assertSavedLimit(t, rec.all(), 1, 2)
}

func TestSavedList_DefaultLimit(t *testing.T) {
	for _, args := range [][]string{
		{"saved", "list"},
		{"saved", "list", "--limit", "0"},
	} {
		rec := &savedListRequests{}
		mux := http.NewServeMux()
		mux.HandleFunc("/api/saved.list", savedListRecordingHandler(t, rec, savedFixture(25), map[string]any{}, ""))

		out, err := runWithMockSession(t, mux, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		rows, _ := savedRows(t, out)
		if len(rows) != 20 {
			t.Errorf("%v: rows = %d, want 20", args, len(rows))
		}
		assertSavedLimit(t, rec.all(), 1, 20)
	}
}

func TestSavedList_MaxLimit(t *testing.T) {
	rec := &savedListRequests{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", savedListRecordingHandler(t, rec, savedFixture(60), map[string]any{}, ""))

	out, err := runWithMockSession(t, mux, "saved", "list", "--limit", "50")
	if err != nil {
		t.Fatal(err)
	}
	rows, meta := savedRows(t, out)
	if len(rows) != 50 {
		t.Errorf("rows = %d, want 50", len(rows))
	}
	if meta["has_more"] != true {
		t.Errorf("trailer = %v, want has_more", meta)
	}
	assertSavedLimit(t, rec.all(), 1, 50)
}

// The limit is paging state: a page that omits it falls back to Slack's 20.
func TestSavedList_LimitIsSentOnEveryPage(t *testing.T) {
	rec := &savedListRequests{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", savedListRecordingHandler(t, rec, savedFixture(5), map[string]any{}, ""))

	out, err := runWithMockSession(t, mux, "saved", "list", "--limit", "2", "--all")
	if err != nil {
		t.Fatal(err)
	}

	rows, meta := savedRows(t, out)
	if len(rows) != 5 {
		t.Errorf("rows = %d, want 5", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r["message_ts"].(string)] = true
	}
	if len(seen) != 5 {
		t.Errorf("distinct rows = %d, want 5", len(seen))
	}
	if meta["has_more"] != false {
		t.Errorf("trailer = %v, want has_more false", meta)
	}
	assertSavedLimit(t, rec.all(), 3, 2)
}

// Slack answers invalid_arguments above 50, so the command refuses before
// sending anything.
func TestSavedList_LimitAboveMaxIsRejectedLocally(t *testing.T) {
	for _, limit := range []string{"51", "100"} {
		rec := &savedListRequests{}
		mux := http.NewServeMux()
		mux.HandleFunc("/api/saved.list", savedListRecordingHandler(t, rec, savedFixture(5), map[string]any{}, ""))

		out, err := runWithMockSession(t, mux, "saved", "list", "--limit", limit)
		var oe *output.Error
		if !errors.As(err, &oe) {
			t.Fatalf("--limit %s: err = %T %v, want *output.Error", limit, err, err)
		}
		if oe.Err != "invalid_input" || oe.Detail != "--limit must be between 1 and 50" {
			t.Errorf("--limit %s: error = %q / %q", limit, oe.Err, oe.Detail)
		}
		if len(nonEmptyLines(out)) != 0 {
			t.Errorf("--limit %s: stdout = %q, want nothing", limit, out)
		}
		if n := len(rec.all()); n != 0 {
			t.Errorf("--limit %s: saved.list requests = %d, want 0", limit, n)
		}
	}
}

// saved counts reads only the counts block, so it asks for one row.
func TestSavedCounts_AsksForOneRow(t *testing.T) {
	rec := &savedListRequests{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/saved.list", savedListRecordingHandler(t, rec, savedFixture(25), map[string]any{"total_count": 25}, ""))

	out, err := runWithMockSession(t, mux, "saved", "counts")
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := savedRows(t, out)
	if len(rows) != 1 || rows[0]["total_count"] != float64(25) {
		t.Errorf("rows = %v, want the counts block", rows)
	}
	assertSavedLimit(t, rec.all(), 1, 1)
}
