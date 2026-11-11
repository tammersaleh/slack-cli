package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// draftPage is one drafts.list answer.
type draftPage struct {
	drafts  []map[string]any
	hasMore bool
}

// draftReachMux models the measured drafts.list: the plain request answers
// with the most recently updated drafts of every state, and is_active=true
// answers with the drafts neither sent nor deleted, filtered before the
// limit. calls records each request as "all" or "active", with its limit.
func draftReachMux(all, active draftPage, calls *[]string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		page, kind := all, "all"
		if r.PostForm.Get("is_active") == "true" {
			page, kind = active, "active"
		}
		*calls = append(*calls, kind+":"+r.PostForm.Get("limit"))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "drafts": page.drafts, "has_more": page.hasMore})
	})
	return mux
}

func reachDraft(id string, extra map[string]any) map[string]any {
	m := liveDraft(map[string]any{"id": id, "is_sent": false, "is_deleted": false})
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// An account whose newest drafts are all sent or deleted: the plain page holds
// nothing to show, and the active request reaches the live draft behind it.
func TestDraftList_ReachesActiveDraftsBeyondThePage(t *testing.T) {
	all := draftPage{hasMore: true, drafts: []map[string]any{
		reachDraft("DrSENT", map[string]any{"is_sent": true}),
		reachDraft("DrGONE", map[string]any{"is_deleted": true}),
	}}
	active := draftPage{drafts: []map[string]any{reachDraft("DrOLD", nil)}}
	var calls []string
	out, err := runWithMockSession(t, draftReachMux(all, active, &calls), "draft", "list", "--limit", "20")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "all:20" || calls[1] != "active:20" {
		t.Errorf("expected a plain request then an active one at the same limit, got %v", calls)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected one row and a trailer, got %d:\n%s", len(lines), out)
	}
	if row := parseJSON(t, lines[0]); row["id"] != "DrOLD" {
		t.Errorf("expected the old live draft, got %v", row)
	}
	meta, _ := parseJSON(t, lines[1])["_meta"].(map[string]any)
	if meta["has_more"] != false {
		t.Errorf("the active page was complete, has_more should be false: %v", meta)
	}
	if meta["suppressed_sent"] != float64(1) || meta["suppressed_deleted"] != float64(1) {
		t.Errorf("suppressed counts should describe the plain page: %v", meta)
	}
}

// A live draft in both answers is one row, and the plain page's order leads.
func TestDraftList_UnionDoesNotDuplicate(t *testing.T) {
	all := draftPage{hasMore: true, drafts: []map[string]any{
		reachDraft("DrNEW", nil),
		reachDraft("DrSENT", map[string]any{"is_sent": true}),
	}}
	active := draftPage{hasMore: true, drafts: []map[string]any{reachDraft("DrNEW", nil), reachDraft("DrOLD", nil)}}
	var calls []string
	out, err := runWithMockSession(t, draftReachMux(all, active, &calls), "draft", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected two rows and a trailer, got %d:\n%s", len(lines), out)
	}
	if a, b := parseJSON(t, lines[0])["id"], parseJSON(t, lines[1])["id"]; a != "DrNEW" || b != "DrOLD" {
		t.Errorf("rows = %v, %v, want DrNEW then DrOLD", a, b)
	}
	meta, _ := parseJSON(t, lines[2])["_meta"].(map[string]any)
	if meta["has_more"] != true {
		t.Errorf("a truncated active page means more live drafts exist: %v", meta)
	}
}

// The active request is made only when it can add something: the plain page
// was truncated and the listing is the default one.
func TestDraftList_ActiveRequestOnlyWhenNeeded(t *testing.T) {
	sent := reachDraft("DrSENT", map[string]any{"is_sent": true})
	tests := []struct {
		name     string
		hasMore  bool
		args     []string
		want     []string
		wantMore bool
	}{
		{"complete plain page", false, nil, []string{"all:50"}, false},
		{"include sent", true, []string{"--include-sent"}, []string{"all:50"}, true},
		{"include deleted", true, []string{"--include-deleted"}, []string{"all:50"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			mux := draftReachMux(draftPage{drafts: []map[string]any{sent}, hasMore: tt.hasMore}, draftPage{drafts: []map[string]any{reachDraft("DrOLD", nil)}}, &calls)
			out, err := runWithMockSession(t, mux, append([]string{"draft", "list"}, tt.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != len(tt.want) || calls[0] != tt.want[0] {
				t.Errorf("calls = %v, want %v", calls, tt.want)
			}
			lines := nonEmptyLines(out)
			meta, _ := parseJSON(t, lines[len(lines)-1])["_meta"].(map[string]any)
			if meta["has_more"] != tt.wantMore {
				t.Errorf("has_more = %v, want %v", meta["has_more"], tt.wantMore)
			}
		})
	}
}

func TestDraftList_ActiveFlagIsGone(t *testing.T) {
	var calls []string
	_, err := runWithMockSession(t, draftReachMux(draftPage{}, draftPage{}, &calls), "draft", "list", "--active")
	if err == nil {
		t.Fatal("--active should be an unknown flag")
	}
	if len(calls) != 0 {
		t.Errorf("no request expected, got %v", calls)
	}
}

// draft update finds a live draft that the plain page no longer holds.
func TestDraftUpdate_FindsActiveDraftBeyondThePage(t *testing.T) {
	all := draftPage{hasMore: true, drafts: []map[string]any{reachDraft("DrSENT", map[string]any{"is_sent": true})}}
	active := draftPage{drafts: []map[string]any{reachDraft("DrOLD", nil)}}
	var calls []string
	var updated string
	mux := draftReachMux(all, active, &calls)
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		updated = r.PostForm.Get("draft_id")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": reachDraft("DrOLD", nil)})
	})
	out, err := runWithMockSessionStdin(t, richTextBlocksJSON("x"), mux, "draft", "update", "DrOLD")
	if err != nil {
		t.Fatal(err)
	}
	if updated != "DrOLD" {
		t.Errorf("drafts.update draft_id = %q, want DrOLD", updated)
	}
	if len(calls) != 2 || calls[0] != "all:100" || calls[1] != "active:100" {
		t.Errorf("calls = %v", calls)
	}
	if row := parseJSON(t, nonEmptyLines(out)[0]); row["id"] != "DrOLD" {
		t.Errorf("row = %v", row)
	}
}

// A draft in the plain page costs one request, truncated or not.
func TestDraftUpdate_PlainPageHitSkipsActiveRequest(t *testing.T) {
	all := draftPage{hasMore: true, drafts: []map[string]any{reachDraft("DrNEW", nil)}}
	var calls []string
	mux := draftReachMux(all, draftPage{}, &calls)
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": reachDraft("DrNEW", nil)})
	})
	if _, err := runWithMockSessionStdin(t, richTextBlocksJSON("x"), mux, "draft", "update", "DrNEW"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Errorf("calls = %v, want one plain request", calls)
	}
}

// Absent from both answers while Slack reports more: the draft may be a sent
// or deleted one past the page, so the miss stays inconclusive.
func TestDraftUpdate_MissInBothAnswersIsIncomplete(t *testing.T) {
	all := draftPage{hasMore: true, drafts: []map[string]any{reachDraft("DrSENT", map[string]any{"is_sent": true})}}
	var calls []string
	mux := draftReachMux(all, draftPage{drafts: []map[string]any{reachDraft("DrOLD", nil)}}, &calls)
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("x"), mux, "draft", "update", "DrMISSING")
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "draft_lookup_incomplete" {
		t.Fatalf("expected draft_lookup_incomplete, got %v", err)
	}
	if len(calls) != 2 {
		t.Errorf("both requests should be tried before giving up, got %v", calls)
	}
}

// draft delete shares one lookup across its ids: the active request is made
// once, on the first id the plain page lacks.
func TestDraftDelete_FindsActiveDraftsBeyondThePage(t *testing.T) {
	all := draftPage{hasMore: true, drafts: []map[string]any{reachDraft("DrNEW", nil)}}
	active := draftPage{drafts: []map[string]any{reachDraft("DrNEW", nil), reachDraft("DrOLD1", nil), reachDraft("DrOLD2", nil)}}
	var calls []string
	var deleted []string
	mux := draftReachMux(all, active, &calls)
	mux.HandleFunc("/api/drafts.delete", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		deleted = append(deleted, r.PostForm.Get("draft_id"))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	out, err := runWithMockSession(t, mux, "draft", "delete", "DrNEW", "DrOLD1", "DrOLD2", "DrMISSING")
	assertExitError(t, err)
	if len(deleted) != 3 {
		t.Errorf("expected three deletes, got %v", deleted)
	}
	if len(calls) != 2 {
		t.Errorf("expected one plain and one active request, got %v", calls)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 5 {
		t.Fatalf("expected 4 rows and a trailer, got %d:\n%s", len(lines), out)
	}
	for i, id := range []string{"DrNEW", "DrOLD1", "DrOLD2"} {
		if row := parseJSON(t, lines[i]); row["id"] != id || row["status"] != "deleted" {
			t.Errorf("row %d = %v, want %s deleted", i, row, id)
		}
	}
	if row := parseJSON(t, lines[3]); row["error"] != "draft_lookup_incomplete" || row["input"] != "DrMISSING" {
		t.Errorf("expected draft_lookup_incomplete for DrMISSING, got %v", row)
	}
}

// A failed active request is not a miss: the run stops with a trailer.
func TestDraftDelete_ActiveRequestFailureAborts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("is_active") == "true" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "drafts": []any{}, "has_more": true})
	})
	out, err := runWithMockSession(t, mux, "draft", "delete", "DrOLD")
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "invalid_auth" {
		t.Fatalf("expected invalid_auth, got %v", err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("expected only a trailer, got:\n%s", out)
	}
	meta, _ := parseJSON(t, lines[0])["_meta"].(map[string]any)
	if meta["error"] != "invalid_auth" {
		t.Errorf("trailer should name the failure, got %v", meta)
	}
}
