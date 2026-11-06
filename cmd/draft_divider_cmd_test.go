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

// The --markdown path ships a divider for a horizontal rule: the converter
// output reaches the wire through parseDraftPayload and the renderable-content
// gate accepts a divider-only document.
func TestDraftCreate_MarkdownDivider(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreate(t, &gotForm))

	if _, err := runWithMockSessionStdin(t, "---\n", mux, "draft", "create", "C01ABC", "--markdown"); err != nil {
		t.Fatalf("divider-only Markdown should create a draft, got %v", err)
	}
	if gotForm.Get("blocks") != `[`+divider+`]` {
		t.Errorf("blocks\n got %s\nwant [%s]", gotForm.Get("blocks"), divider)
	}
}

func TestDraftCreate_PrintPayloadDividerSplitsRuns(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) })
	out, err := runWithMockSessionStdin(t, "a\n\n***\n\nb\n", h, "draft", "create", "C01ABC", "--markdown", "--print-payload")
	if err != nil {
		t.Fatal(err)
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	blocks, ok := row["blocks"].([]any)
	if !ok || len(blocks) != 3 {
		t.Fatalf("want [rich_text, divider, rich_text], got %v", row["blocks"])
	}
	types := make([]string, len(blocks))
	for i, b := range blocks {
		m, _ := b.(map[string]any)
		types[i], _ = m["type"].(string)
	}
	if types[0] != "rich_text" || types[1] != "divider" || types[2] != "rich_text" {
		t.Errorf("want rich_text, divider, rich_text, got %v", types)
	}
	firstJSON, _ := json.Marshal(blocks[0])
	if !strings.Contains(string(firstJSON), `"a\n"`) {
		t.Errorf("the paragraph before the divider should keep its trailing newline, got %s", firstJSON)
	}
}

func TestDraftUpdate_MarkdownDividerReplacesBlocks(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("old"),
		"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
	}
	var gotUpdate url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotUpdate, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": existing})
	})
	if _, err := runWithMockSessionStdin(t, "new\n\n---\n", mux, "draft", "update", "Dr01234", "--markdown"); err != nil {
		t.Fatal(err)
	}
	want := `[{"elements":[{"elements":[{"text":"new\n","type":"text"}],"type":"rich_text_section"}],"type":"rich_text"},` + divider + `]`
	if gotUpdate.Get("blocks") != want {
		t.Errorf("blocks\n got %s\nwant %s", gotUpdate.Get("blocks"), want)
	}
}
