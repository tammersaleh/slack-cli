package cmd_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Command-level coverage for divider blocks (docs/divider-blocks-plan.md).
// The validator unit tests live in draft_divider_test.go (package cmd).

const divider = `{"type":"divider"}`

// A divider-only draft is renderable content at the command level and ships
// verbatim.
func TestDraftCreate_DividerOnly(t *testing.T) {
	blocks := `[` + divider + `]`
	var gotBlocks string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotBlocks = form.Get("blocks")
		draftCreateResponder(t, map[string]any{
			"id": "Dr01234", "date_created": 1709251200, "last_updated_ts": "1709251200.1200000",
			"blocks": json.RawMessage(gotBlocks), "destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	out, err := runWithMockSessionStdin(t, blocks, mux, "draft", "create", "C01ABC")
	if err != nil {
		t.Fatalf("divider-only draft should be accepted, got %v", err)
	}
	if gotBlocks != blocks {
		t.Errorf("blocks should ship verbatim, got %s", gotBlocks)
	}
	if item := parseJSON(t, nonEmptyLines(out)[0]); item["id"] != "Dr01234" {
		t.Errorf("expected the created draft row, got %v", item)
	}
}

// A divider after a rich_text body is the composer's own shape and ships
// verbatim (it was a rejection row until 2026-11-06).
func TestDraftCreate_DividerAlongsideRichTextPassesThrough(t *testing.T) {
	blocks := `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"a"}]}]},` + divider + `]`
	var gotBlocks string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotBlocks = form.Get("blocks")
		draftCreateResponder(t, map[string]any{
			"id": "Dr01234", "date_created": 1709251200, "last_updated_ts": "1709251200.1200000",
			"blocks": json.RawMessage(gotBlocks), "destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	if _, err := runWithMockSessionStdin(t, blocks, mux, "draft", "create", "C01ABC"); err != nil {
		t.Fatalf("rich_text then divider should be accepted, got %v", err)
	}
	if gotBlocks != blocks {
		t.Errorf("blocks should ship verbatim\n got %s\nwant %s", gotBlocks, blocks)
	}
}

// A schedule-only update round-trips the existing blocks through the
// renderable-content gate; a divider-only draft must clear it.
func TestDraftUpdate_ScheduleOnlyPreservesDividerOnlyDraft(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks": []map[string]any{
			{"type": "divider", "block_id": "srv01"},
		},
		"destinations": []map[string]any{{"channel_id": "C01ABC"}},
	}
	var gotUpdate url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotUpdate, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": existing})
	})

	if _, err := runWithMockSession(t, mux, "draft", "update", "Dr01234", "--at", "2026-06-01"); err != nil {
		t.Fatalf("schedule-only update of a divider-only draft should succeed, got %v", err)
	}
	if !strings.Contains(gotUpdate.Get("blocks"), `"divider"`) || !strings.Contains(gotUpdate.Get("blocks"), `"srv01"`) {
		t.Errorf("the divider block should round-trip with its block_id, got %q", gotUpdate.Get("blocks"))
	}
}
