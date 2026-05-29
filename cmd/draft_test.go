package cmd_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

func draftsListHandler(t *testing.T, drafts []map[string]any, hasMore bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/drafts.list" {
			http.Error(w, "not found", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"drafts":   drafts,
			"has_more": hasMore,
		})
	}
}

// richTextBlocks builds a minimal rich_text Block Kit structure wrapping text.
// Used for both fixtures on the mock (server returns blocks to us) and
// as the stdin input for create/update tests.
func richTextBlocks(text string) []map[string]any {
	return []map[string]any{
		{
			"type": "rich_text",
			"elements": []map[string]any{
				{
					"type": "rich_text_section",
					"elements": []map[string]any{
						{"type": "text", "text": text},
					},
				},
			},
		},
	}
}

// richTextBlocksJSON returns richTextBlocks rendered as a JSON string.
// Callers use this as the stdin payload for draft create/update.
func richTextBlocksJSON(text string) string {
	b, _ := json.Marshal(richTextBlocks(text))
	return string(b)
}

func singleDraftListHandler(t *testing.T, draft map[string]any) http.HandlerFunc {
	t.Helper()
	return draftsListHandler(t, []map[string]any{draft}, false)
}

func draftCreateResponder(t *testing.T, draft map[string]any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"draft": draft,
		})
	}
}

func TestDraftList_Basic(t *testing.T) {
	drafts := []map[string]any{
		{
			"id":              "Dr01234",
			"client_msg_id":   "uuid-1",
			"date_created":    1709251200,
			"date_scheduled":  0,
			"last_updated_ts": "1709251200.1200000",
			"blocks":          richTextBlocks("hello world"),
			"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", draftsListHandler(t, drafts, false))

	out, err := runWithMockSession(t, mux, "draft", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
	item := parseJSON(t, lines[0])
	if item["id"] != "Dr01234" {
		t.Errorf("expected id='Dr01234', got %q", item["id"])
	}
}

func TestDraftList_ActiveFilter(t *testing.T) {
	var gotIsActive string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotIsActive = form.Get("is_active")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"drafts":   []any{},
			"has_more": false,
		})
	})

	_, err := runWithMockSession(t, mux, "draft", "list", "--active")
	if err != nil {
		t.Fatal(err)
	}
	if gotIsActive != "true" {
		t.Errorf("expected is_active=true, got %q", gotIsActive)
	}
}

func TestDraftList_ScheduledEnrichment(t *testing.T) {
	drafts := []map[string]any{
		{
			"id":              "Dr99999",
			"date_created":    1709251200,
			"date_scheduled":  1709337600,
			"last_updated_ts": "1709251200.1234567",
			"blocks":          richTextBlocks("later"),
			"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", draftsListHandler(t, drafts, false))

	out, err := runWithMockSession(t, mux, "draft", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	item := parseJSON(t, lines[0])
	iso, ok := item["date_scheduled_iso"].(string)
	if !ok {
		t.Fatalf("expected date_scheduled_iso, got %v", item["date_scheduled_iso"])
	}
	if !strings.HasPrefix(iso, "2024-03-02T") {
		t.Errorf("expected ISO around 2024-03-02, got %q", iso)
	}
}

func TestDraftList_FiltersSentAndDeleted(t *testing.T) {
	drafts := []map[string]any{
		{"id": "DrACTIVE", "date_created": 1709251200, "last_updated_ts": "1709251200.1200000", "blocks": richTextBlocks("active"), "destinations": []map[string]any{{"channel_id": "C01ABC"}}},
		{"id": "DrSENT", "is_sent": true, "last_updated_ts": "1709251100.1200000", "blocks": richTextBlocks("already sent"), "destinations": []map[string]any{{"channel_id": "C01ABC"}}},
		{"id": "DrDELETED", "is_deleted": true, "last_updated_ts": "1709251000.1200000", "blocks": richTextBlocks("tombstoned"), "destinations": []map[string]any{{"channel_id": "C01ABC"}}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", draftsListHandler(t, drafts, false))

	out, err := runWithMockSession(t, mux, "draft", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 active + meta), got %d:\n%s", len(lines), out)
	}
	item := parseJSON(t, lines[0])
	if item["id"] != "DrACTIVE" {
		t.Errorf("expected only DrACTIVE, got %q", item["id"])
	}
}

func TestDraftList_IncludeSentAndDeleted(t *testing.T) {
	drafts := []map[string]any{
		{"id": "DrA", "last_updated_ts": "1.0", "blocks": richTextBlocks("a"), "destinations": []map[string]any{{"channel_id": "C01"}}},
		{"id": "DrS", "is_sent": true, "last_updated_ts": "2.0", "blocks": richTextBlocks("b"), "destinations": []map[string]any{{"channel_id": "C01"}}},
		{"id": "DrD", "is_deleted": true, "last_updated_ts": "3.0", "blocks": richTextBlocks("c"), "destinations": []map[string]any{{"channel_id": "C01"}}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", draftsListHandler(t, drafts, false))

	out, err := runWithMockSession(t, mux, "draft", "list", "--include-sent", "--include-deleted")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(nonEmptyLines(out)); got != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", got, out)
	}
}

func TestDraftList_HasMoreMeta(t *testing.T) {
	drafts := []map[string]any{{
		"id": "DrX", "last_updated_ts": "1.0", "blocks": richTextBlocks("first"),
		"destinations": []map[string]any{{"channel_id": "C01ABC"}},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", draftsListHandler(t, drafts, true))

	out, err := runWithMockSession(t, mux, "draft", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	meta := parseJSON(t, lines[len(lines)-1])
	metaField, _ := meta["_meta"].(map[string]any)
	if hasMore, _ := metaField["has_more"].(bool); !hasMore {
		t.Errorf("expected has_more=true, got %v", metaField["has_more"])
	}
}

func TestDraftCreate_Basic(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr99999", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": richTextBlocks("hello world"), "destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	out, err := runWithMockSessionStdin(t, richTextBlocksJSON("hello world"), mux, "draft", "create", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	var dests []map[string]any
	if err := json.Unmarshal([]byte(gotForm.Get("destinations")), &dests); err != nil {
		t.Fatalf("destinations JSON parse: %v", err)
	}
	if dests[0]["channel_id"] != "C01ABC" {
		t.Errorf("expected channel_id=C01ABC, got %v", dests[0])
	}

	// Blocks field should be the exact JSON we piped in.
	if gotForm.Get("blocks") != richTextBlocksJSON("hello world") {
		t.Errorf("blocks should be passed through verbatim, got %q", gotForm.Get("blocks"))
	}

	if gotForm.Get("client_msg_id") == "" {
		t.Error("expected client_msg_id set")
	}
	if gotForm.Get("file_ids") != "[]" {
		t.Errorf("expected file_ids='[]', got %q", gotForm.Get("file_ids"))
	}
	// Must be composer-originated so Slack's channel composer will load
	// the draft from drafts.listActive; otherwise "Edit draft" opens an
	// empty compose box.
	if gotForm.Get("is_from_composer") != "true" {
		t.Errorf("expected is_from_composer='true', got %q", gotForm.Get("is_from_composer"))
	}

	lines := nonEmptyLines(out)
	item := parseJSON(t, lines[0])
	if item["id"] != "Dr99999" {
		t.Errorf("expected id='Dr99999', got %q", item["id"])
	}
}

func TestDraftCreate_Thread(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr01111", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": richTextBlocks("reply"),
			"destinations": []map[string]any{
				{"channel_id": "C01ABC", "thread_ts": "1713299000.123456", "broadcast": true},
			},
		})(w, r)
	})

	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("reply"), mux,
		"draft", "create", "C01ABC",
		"--thread", "1713299000.123456",
		"--broadcast",
	)
	if err != nil {
		t.Fatal(err)
	}

	var dests []map[string]any
	_ = json.Unmarshal([]byte(gotForm.Get("destinations")), &dests)
	if dests[0]["thread_ts"] != "1713299000.123456" {
		t.Errorf("expected thread_ts, got %v", dests[0]["thread_ts"])
	}
	if dests[0]["broadcast"] != true {
		t.Errorf("expected broadcast=true, got %v", dests[0]["broadcast"])
	}
}

func TestDraftCreate_ScheduledAt(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr02222", "date_created": 1713300000, "date_scheduled": 1745148000,
			"last_updated_ts": "1713300000.1200000",
			"blocks":          richTextBlocks("later"),
			"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("later"), mux,
		"draft", "create", "C01ABC", "--at", "2025-04-20T09:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("date_scheduled") != "1745139600" {
		t.Errorf("expected date_scheduled=1745139600, got %q", gotForm.Get("date_scheduled"))
	}
}

func TestDraftCreate_ChannelResolution(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          []map[string]any{{"id": "C01ABC", "name": "general", "is_member": true}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr04444", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": richTextBlocks("hello"), "destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("hello"), mux,
		"draft", "create", "#general",
	)
	if err != nil {
		t.Fatal(err)
	}
	var dests []map[string]any
	_ = json.Unmarshal([]byte(gotForm.Get("destinations")), &dests)
	if dests[0]["channel_id"] != "C01ABC" {
		t.Errorf("expected resolved channel_id=C01ABC, got %v", dests[0])
	}
}

func TestDraftCreate_MissingBlocks(t *testing.T) {
	// Empty stdin: readDraftPayload trims and returns an empty payload, and
	// create (no --table) maps that to missing_blocks. We pass "  "
	// (whitespace) so cli.in is non-nil and the TTY-check branch is
	// skipped, exercising the explicit empty-input path.
	mux := http.NewServeMux()
	_, err := runWithMockSessionStdin(t, "  ", mux, "draft", "create", "C01ABC")
	if err == nil {
		t.Fatal("expected missing_blocks error")
	}
	if !strings.Contains(err.Error(), "missing_blocks") {
		t.Errorf("expected missing_blocks error, got %v", err)
	}
}

func TestDraftCreate_InvalidBlocksJSON(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMockSessionStdin(t, "not json", mux, "draft", "create", "C01ABC")
	if err == nil {
		t.Fatal("expected invalid_blocks error")
	}
	if !strings.Contains(err.Error(), "invalid_blocks") {
		t.Errorf("expected invalid_blocks, got %v", err)
	}
}

func TestDraftCreate_InvalidBlocksMissingType(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMockSessionStdin(t, `[{"not_a_type_field":"x"}]`, mux, "draft", "create", "C01ABC")
	if err == nil {
		t.Fatal("expected invalid_blocks error")
	}
	if !strings.Contains(err.Error(), "invalid_blocks") {
		t.Errorf("expected invalid_blocks, got %v", err)
	}
}

func TestDraftCreate_InvalidBlocksEmptyArray(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMockSessionStdin(t, `[]`, mux, "draft", "create", "C01ABC")
	if err == nil {
		t.Fatal("expected invalid_blocks error for empty array")
	}
	if !strings.Contains(err.Error(), "invalid_blocks") {
		t.Errorf("expected invalid_blocks, got %v", err)
	}
}

func TestDraftCreate_InvalidBlocksNoRichText(t *testing.T) {
	// section+mrkdwn alone - Slack accepts, but Desktop's Drafts-panel
	// reconciliation will tombstone. CLI rejects upfront.
	mux := http.NewServeMux()
	_, err := runWithMockSessionStdin(t,
		`[{"type":"section","text":{"type":"mrkdwn","text":"hi"}}]`,
		mux, "draft", "create", "C01ABC",
	)
	if err == nil {
		t.Fatal("expected invalid_blocks error for missing rich_text")
	}
	var oe *output.Error
	if !errors.As(err, &oe) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oe.Err != "invalid_blocks" {
		t.Errorf("expected err=invalid_blocks, got %q", oe.Err)
	}
	if !strings.Contains(oe.Detail, "rich_text") {
		t.Errorf("expected Detail mentioning rich_text, got %q", oe.Detail)
	}
	// Hint must steer the agent to the skill install command - the
	// recovery path for "I don't know the rich_text shape."
	if !strings.Contains(oe.Hint, "skills add tammersaleh/slack-cli") {
		t.Errorf("expected Hint to point at skill install, got %q", oe.Hint)
	}
}

func TestDraftCreate_InvalidBlocksEmptyRichText(t *testing.T) {
	// rich_text with an empty elements array doesn't help - Desktop still
	// tombstones.
	mux := http.NewServeMux()
	_, err := runWithMockSessionStdin(t,
		`[{"type":"rich_text","elements":[]}]`,
		mux, "draft", "create", "C01ABC",
	)
	if err == nil {
		t.Fatal("expected invalid_blocks error for empty rich_text")
	}
	if !strings.Contains(err.Error(), "invalid_blocks") {
		t.Errorf("expected invalid_blocks, got %v", err)
	}
}

func TestDraftCreate_RejectsNonRichTextBlocks(t *testing.T) {
	// Non-rich_text top-level blocks survive the wire but get silently
	// stripped by Slack Desktop's Drafts compose editor when the user
	// opens the draft. Rejecting them prevents silent content loss.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.create should not be called when validation rejects")
	})

	for _, tc := range []struct {
		name, blocks string
	}{
		{
			name:   "section_alongside_rich_text",
			blocks: `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"a"}]}]},{"type":"section","text":{"type":"mrkdwn","text":"extra"}}]`,
		},
		{
			name:   "divider_alongside_rich_text",
			blocks: `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"a"}]}]},{"type":"divider"}]`,
		},
		{
			name:   "header_before_rich_text",
			blocks: `[{"type":"header","text":{"type":"plain_text","text":"Hi"}},{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"a"}]}]}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runWithMockSessionStdin(t, tc.blocks, mux, "draft", "create", "C01ABC")
			if err == nil {
				t.Fatal("expected invalid_blocks error for non-rich_text top-level block")
			}
			var oe *output.Error
			if !errors.As(err, &oe) {
				t.Fatalf("expected *output.Error, got %T: %v", err, err)
			}
			if oe.Err != "invalid_blocks" {
				t.Errorf("expected err=invalid_blocks, got %q", oe.Err)
			}
			// Detail must steer the caller toward the correct fix.
			if !strings.Contains(oe.Detail, "rich_text") || !strings.Contains(oe.Detail, "strip") {
				t.Errorf("expected Detail mentioning rich_text and stripping, got %q", oe.Detail)
			}
			// Pin these to the generic branch. The table-specific message also
			// mentions rich_text + strip, but only it names the preformatted
			// fallback - the generic message must not, or this test would pass
			// against the wrong branch.
			if strings.Contains(oe.Detail, "rich_text_preformatted") {
				t.Errorf("non-table block should hit the generic message, not the table-specific one: %q", oe.Detail)
			}
			if !strings.Contains(oe.Hint, "skills add tammersaleh/slack-cli") {
				t.Errorf("expected Hint to point at skill install, got %q", oe.Hint)
			}
		})
	}
}

func TestDraftCreate_RejectsTableBlocks(t *testing.T) {
	// A table/data_table block in TOP-LEVEL blocks is stripped by Slack's
	// draft compose editor (verified 2026-05-29, see docs/draft-messages.md).
	// Tables belong in attachments[].blocks[], where they survive. The CLI
	// rejects a top-level table up front with a message that names the block
	// and steers the caller to attachments / --table.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.create should not be called when validation rejects")
	})

	// pin is a substring unique to the expected branch's message, so a case
	// can't pass against the wrong branch (e.g. "table" alone would match the
	// data_table message too).
	for _, tc := range []struct {
		name, blocks, pin string
	}{
		{
			name:   "table_only",
			blocks: `[{"type":"table","rows":[[{"type":"raw_text","text":"A"},{"type":"raw_text","text":"B"}]]}]`,
			pin:    "Put the table in an attachment",
		},
		{
			name:   "table_alongside_rich_text",
			blocks: `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"a"}]}]},{"type":"table","rows":[[{"type":"raw_text","text":"A"}]]}]`,
			pin:    "Put the table in an attachment",
		},
		{
			name:   "data_table_only",
			blocks: `[{"type":"data_table","caption":"c","rows":[[{"type":"raw_text","text":"A"}]]}]`,
			pin:    "data_table is stripped from drafts",
		},
		{
			name:   "data_table_alongside_rich_text",
			blocks: `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"a"}]}]},{"type":"data_table","caption":"c","rows":[[{"type":"raw_text","text":"A"}]]}]`,
			pin:    "data_table is stripped from drafts",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runWithMockSessionStdin(t, tc.blocks, mux, "draft", "create", "C01ABC")
			if err == nil {
				t.Fatal("expected invalid_blocks error for table block")
			}
			var oe *output.Error
			if !errors.As(err, &oe) {
				t.Fatalf("expected *output.Error, got %T: %v", err, err)
			}
			if oe.Err != "invalid_blocks" {
				t.Errorf("expected err=invalid_blocks, got %q", oe.Err)
			}
			// Pin to the correct branch's message.
			if !strings.Contains(oe.Detail, tc.pin) {
				t.Errorf("expected Detail to contain %q (wrong branch fired?), got %q", tc.pin, oe.Detail)
			}
			if !strings.Contains(oe.Hint, "skills add tammersaleh/slack-cli") {
				t.Errorf("expected Hint to point at skill install, got %q", oe.Hint)
			}
		})
	}
}

// tableAttachmentJSON is a minimal valid table-in-attachment payload: one
// attachment whose blocks hold a single table block. Tables ride in
// attachments[].blocks[], never top-level blocks.
func tableAttachmentJSON() string {
	return `[{"blocks":[{"type":"table","rows":[[{"type":"raw_text","text":"A"},{"type":"raw_text","text":"B"}]]}]}]`
}

func TestDraftCreate_AttachmentsPassthrough(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr3", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks":       richTextBlocks("intro"),
			"attachments":  json.RawMessage(tableAttachmentJSON()),
			"destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	body := `{"blocks":` + richTextBlocksJSON("intro") + `,"attachments":` + tableAttachmentJSON() + `}`
	out, err := runWithMockSessionStdin(t, body, mux, "draft", "create", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("blocks") != richTextBlocksJSON("intro") {
		t.Errorf("blocks should pass through verbatim, got %q", gotForm.Get("blocks"))
	}
	if gotForm.Get("attachments") != tableAttachmentJSON() {
		t.Errorf("attachments should pass through verbatim, got %q", gotForm.Get("attachments"))
	}
	// Output surfaces attachments so the caller can inspect what was staged.
	item := parseJSON(t, nonEmptyLines(out)[0])
	if _, ok := item["attachments"]; !ok {
		t.Errorf("expected attachments in create output, got %v", item)
	}
}

func TestDraftCreate_AttachmentsOnly(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr4", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks":       []map[string]any{},
			"attachments":  json.RawMessage(tableAttachmentJSON()),
			"destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	body := `{"attachments":` + tableAttachmentJSON() + `}`
	if _, err := runWithMockSessionStdin(t, body, mux, "draft", "create", "C01ABC"); err != nil {
		t.Fatal(err)
	}
	// A table-only draft sends an empty blocks array; it survives reconciliation
	// on the strength of the table attachment alone (verified 2026-05-29).
	if gotForm.Get("blocks") != "[]" {
		t.Errorf("expected blocks='[]' for attachments-only draft, got %q", gotForm.Get("blocks"))
	}
	if gotForm.Get("attachments") != tableAttachmentJSON() {
		t.Errorf("attachments should pass through verbatim, got %q", gotForm.Get("attachments"))
	}
}

func TestDraftCreate_RejectsNonTableAttachment(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.create should not be called when validation rejects")
	})
	body := `{"blocks":` + richTextBlocksJSON("x") + `,"attachments":[{"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"hi"}}]}]}`
	_, err := runWithMockSessionStdin(t, body, mux, "draft", "create", "C01ABC")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("expected invalid_blocks, got %v", err)
	}
	if !strings.Contains(oe.Detail, "table") {
		t.Errorf("expected Detail to explain only table/data_table allowed in attachments, got %q", oe.Detail)
	}
}

func TestDraftCreate_RejectsNoRenderableContent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.create should not be called when validation rejects")
	})
	// Empty attachments array, no blocks: nothing renderable, so Slack would
	// tombstone it. Reject up front.
	_, err := runWithMockSessionStdin(t, `{"attachments":[]}`, mux, "draft", "create", "C01ABC")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("expected invalid_blocks, got %v", err)
	}
	if !strings.Contains(oe.Detail, "renderable content") {
		t.Errorf("expected renderable-content message, got %q", oe.Detail)
	}
}

func TestDraftUpdate_AddAttachmentPreservesBlocks(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("body"),
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

	body := `{"attachments":` + tableAttachmentJSON() + `}`
	if _, err := runWithMockSessionStdin(t, body, mux, "draft", "update", "Dr01234"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotUpdate.Get("blocks"), "body") {
		t.Errorf("existing blocks should be preserved, got %q", gotUpdate.Get("blocks"))
	}
	if gotUpdate.Get("attachments") != tableAttachmentJSON() {
		t.Errorf("attachments should be the provided table, got %q", gotUpdate.Get("attachments"))
	}
}

func TestDraftUpdate_ReplaceBlocksPreservesAttachments(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("old"),
		"attachments":     json.RawMessage(tableAttachmentJSON()),
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

	// Bare array = replace blocks only; existing attachments must be preserved.
	if _, err := runWithMockSessionStdin(t, richTextBlocksJSON("new"), mux, "draft", "update", "Dr01234"); err != nil {
		t.Fatal(err)
	}
	if gotUpdate.Get("blocks") != richTextBlocksJSON("new") {
		t.Errorf("blocks should be replaced, got %q", gotUpdate.Get("blocks"))
	}
	if !strings.Contains(gotUpdate.Get("attachments"), `"table"`) {
		t.Errorf("existing table attachment should be preserved, got %q", gotUpdate.Get("attachments"))
	}
}

func TestDraftUpdate_ClearAttachments(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("body"),
		"attachments":     json.RawMessage(tableAttachmentJSON()),
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

	if _, err := runWithMockSessionStdin(t, `{"attachments":[]}`, mux, "draft", "update", "Dr01234"); err != nil {
		t.Fatal(err)
	}
	if gotUpdate.Get("attachments") != "[]" {
		t.Errorf("attachments should be cleared to '[]', got %q", gotUpdate.Get("attachments"))
	}
	if !strings.Contains(gotUpdate.Get("blocks"), "body") {
		t.Errorf("blocks should be preserved, got %q", gotUpdate.Get("blocks"))
	}
}

func TestDraftUpdate_TombstoneReplacementCarriesAttachments(t *testing.T) {
	// Table-only draft (empty blocks): the only renderable content is the
	// table attachment, so this exercises the attachment content path AND the
	// replacement carrying attachments across the tombstone.
	existing := map[string]any{
		"id": "Dr01234", "is_deleted": true, "client_msg_id": "uuid-old",
		"last_updated_ts": "1709251200.12",
		"blocks":          []map[string]any{},
		"attachments":     json.RawMessage(tableAttachmentJSON()),
		"destinations":    []map[string]any{{"channel_id": "D01"}},
	}
	var createForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("drafts.update must not be called for a tombstoned draft")
	})
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		createForm, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": map[string]any{
			"id": "DrNEW", "date_created": 1709260000, "last_updated_ts": "1709260000.1234567",
			"blocks": richTextBlocks("old"), "destinations": []map[string]any{{"channel_id": "D01"}},
		}})
	})

	// Schedule-only update of a tombstoned draft -> the replacement create must
	// carry the existing table attachment, or the table is silently lost on the
	// very path meant to preserve the draft.
	if _, err := runWithMockSessionStdin(t, "", mux, "draft", "update", "Dr01234", "--at", "2026-04-20"); err != nil {
		t.Fatalf("expected auto-replace success, got %v", err)
	}
	if !strings.Contains(createForm.Get("attachments"), `"table"`) {
		t.Errorf("replacement create must carry the table attachment, got %q", createForm.Get("attachments"))
	}
}

func TestDraftCreate_TableFlagTSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.tsv")
	if err := os.WriteFile(path, []byte("Region\tStatus\nus-east\tgreen\nus-west\tdegraded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr9", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": []map[string]any{}, "attachments": json.RawMessage(gotForm.Get("attachments")),
			"destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	if _, err := runWithMockSession(t, mux, "draft", "create", "C01ABC", "--table", path); err != nil {
		t.Fatal(err)
	}
	// --table alone makes an attachments-only draft: empty blocks, table attached.
	if gotForm.Get("blocks") != "[]" {
		t.Errorf("table-only draft should send blocks='[]', got %q", gotForm.Get("blocks"))
	}
	var atts []struct {
		Blocks []struct {
			Type string             `json:"type"`
			Rows [][]map[string]any `json:"rows"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(gotForm.Get("attachments")), &atts); err != nil {
		t.Fatalf("attachments JSON: %v", err)
	}
	if len(atts) != 1 || len(atts[0].Blocks) != 1 || atts[0].Blocks[0].Type != "table" {
		t.Fatalf("expected one table block, got %+v", atts)
	}
	rows := atts[0].Blocks[0].Rows
	if len(rows) != 3 || len(rows[0]) != 2 {
		t.Fatalf("expected a 3x2 table (tab-delimited), got %d rows", len(rows))
	}
	// Header row bold (rich_text); body raw_text.
	if rows[0][0]["type"] != "rich_text" {
		t.Errorf("header cell should be bold rich_text, got %v", rows[0][0]["type"])
	}
	if rows[1][0]["type"] != "raw_text" || rows[1][0]["text"] != "us-east" {
		t.Errorf("body cell should be raw_text 'us-east', got %v", rows[1][0])
	}
}

func TestDraftCreate_TableFlagNoHeaderCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.csv")
	if err := os.WriteFile(path, []byte("a,b\nc,d\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr10", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": []map[string]any{}, "attachments": json.RawMessage(gotForm.Get("attachments")),
			"destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	if _, err := runWithMockSession(t, mux, "draft", "create", "C01ABC", "--table", path, "--no-header"); err != nil {
		t.Fatal(err)
	}
	var atts []struct {
		Blocks []struct {
			Rows [][]map[string]any `json:"rows"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(gotForm.Get("attachments")), &atts); err != nil {
		t.Fatalf("attachments JSON: %v", err)
	}
	// --no-header: the first row is data, so every cell is plain raw_text.
	if atts[0].Blocks[0].Rows[0][0]["type"] != "raw_text" {
		t.Errorf("with --no-header the first row should be raw_text, got %v", atts[0].Blocks[0].Rows[0][0]["type"])
	}
}

func TestDraftCreate_TableFlagConflictsWithStdinAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.csv")
	if err := os.WriteFile(path, []byte("a,b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.create should not be called when validation rejects")
	})
	body := `{"blocks":` + richTextBlocksJSON("x") + `,"attachments":` + tableAttachmentJSON() + `}`
	_, err := runWithMockSessionStdin(t, body, mux, "draft", "create", "C01ABC", "--table", path)
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_input" {
		t.Fatalf("expected invalid_input for --table + stdin attachments, got %v", err)
	}
}

func TestDraftUpdate_ScheduleOnlyPreservesTableOnlyDraft(t *testing.T) {
	// A schedule-only update round-trips the existing draft. For a table-only
	// draft (empty blocks), the table attachment alone must satisfy the
	// renderable-content rule - otherwise the doomed-content guard would
	// wrongly reject it.
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks":          []map[string]any{},
		"attachments":     json.RawMessage(tableAttachmentJSON()),
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

	if _, err := runWithMockSession(t, mux, "draft", "update", "Dr01234", "--at", "2026-06-01"); err != nil {
		t.Fatalf("schedule-only update of a table-only draft should succeed, got %v", err)
	}
	if !strings.Contains(gotUpdate.Get("attachments"), `"table"`) {
		t.Errorf("the table attachment should be preserved, got %q", gotUpdate.Get("attachments"))
	}
}

func TestDraftCreate_RejectsAttachmentWithoutTable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.create should not be called when validation rejects")
	})
	// An attachment that carries no table block is unsupported content.
	body := `{"blocks":` + richTextBlocksJSON("x") + `,"attachments":[{"fallback":"x"}]}`
	_, err := runWithMockSessionStdin(t, body, mux, "draft", "create", "C01ABC")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("expected invalid_blocks for an attachment with no table block, got %v", err)
	}
}

func TestDraftUpdate_RejectsScheduleOnlyDataTableOnlyDraft(t *testing.T) {
	// A draft created under v3.2.0 with only a data_table attachment (no
	// rich_text body) has no renderable content under the corrected rule
	// (data_table doesn't count - it's stripped/tombstoned). A schedule-only
	// update must fail rather than round-trip a dead draft.
	existing := map[string]any{
		"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
		"last_updated_ts": "1709251200.12",
		"blocks":          []map[string]any{},
		"attachments":     json.RawMessage(`[{"blocks":[{"type":"data_table","caption":"c","rows":[[{"type":"raw_text","text":"a"}]]}]}]`),
		"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("drafts.update must not be called when the draft has no renderable content")
	})
	_, err := runWithMockSession(t, mux, "draft", "update", "Dr01234", "--at", "2026-06-01")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("expected invalid_blocks (no renderable content), got %v", err)
	}
	if !strings.Contains(oe.Detail, "renderable content") {
		t.Errorf("expected renderable-content message, got %q", oe.Detail)
	}
}

func TestDraftCreate_RejectsDataTableAttachment(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.create should not be called when validation rejects")
	})
	// data_table (the interactive variant) is stripped from drafts and a
	// data_table-only draft is tombstoned (verified 2026-05-29), so it is
	// rejected even inside an attachment - unlike a plain table.
	body := `{"blocks":` + richTextBlocksJSON("x") + `,"attachments":[{"blocks":[{"type":"data_table","caption":"c","rows":[[{"type":"raw_text","text":"a"}]]}]}]}`
	_, err := runWithMockSessionStdin(t, body, mux, "draft", "create", "C01ABC")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_blocks" {
		t.Fatalf("expected invalid_blocks for a data_table attachment, got %v", err)
	}
	if !strings.Contains(oe.Detail, "data_table") || !strings.Contains(oe.Detail, "table block") {
		t.Errorf("expected Detail to reject data_table and point at a table block, got %q", oe.Detail)
	}
}

func TestDraftCreate_RejectsAbsorbingContainerAfterUnterminatedSection(t *testing.T) {
	// A rich_text_list, rich_text_preformatted, or rich_text_quote that
	// immediately follows a rich_text_section absorbs the section's text
	// when the section's element stream does not terminate with a text
	// inline whose `text` ends with "\n". Desktop flattens adjacent
	// top-level rich_text blocks before rendering, so this fires both
	// within a single rich_text block and across multiple top-level blocks.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.create should not be called when validation rejects")
	})

	containers := []struct {
		name, json string
	}{
		{"list", `{"type":"rich_text_list","style":"bullet","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"item"}]}]}`},
		{"preformatted", `{"type":"rich_text_preformatted","elements":[{"type":"text","text":"code body"}]}`},
		{"quote", `{"type":"rich_text_quote","elements":[{"type":"text","text":"quoted body"}]}`},
	}
	unterminatedSection := `{"type":"rich_text_section","elements":[{"type":"text","text":"heading"}]}`

	for _, c := range containers {
		t.Run("intra_block_"+c.name, func(t *testing.T) {
			blocks := `[{"type":"rich_text","elements":[` + unterminatedSection + `,` + c.json + `]}]`
			assertRejectsAbsorption(t, mux, blocks, c.name)
		})
		t.Run("cross_block_"+c.name, func(t *testing.T) {
			blocks := `[` +
				`{"type":"rich_text","elements":[` + unterminatedSection + `]},` +
				`{"type":"rich_text","elements":[` + c.json + `]}` +
				`]`
			assertRejectsAbsorption(t, mux, blocks, c.name)
		})
	}

	t.Run("cross_block_empty_separator", func(t *testing.T) {
		// Empty rich_text block between section and list still flattens
		// on Desktop, so the rejection must still fire.
		blocks := `[` +
			`{"type":"rich_text","elements":[` + unterminatedSection + `]},` +
			`{"type":"rich_text","elements":[]},` +
			`{"type":"rich_text","elements":[` + containers[0].json + `]}` +
			`]`
		assertRejectsAbsorption(t, mux, blocks, "list")
	})

	t.Run("cross_block_untyped_element_between", func(t *testing.T) {
		// An untyped element in between must not silently reset the
		// section accumulator. Defense against a malformed payload
		// defeating the check.
		blocks := `[` +
			`{"type":"rich_text","elements":[` + unterminatedSection + `]},` +
			`{"type":"rich_text","elements":[{"no_type_field":"x"}]},` +
			`{"type":"rich_text","elements":[` + containers[0].json + `]}` +
			`]`
		assertRejectsAbsorption(t, mux, blocks, "list")
	})

	t.Run("non_text_trailing_inline", func(t *testing.T) {
		// A section ending with a non-text inline (link, emoji, user,
		// channel, broadcast) still triggers absorption because the
		// rendered stream doesn't end with a newline. Caller must append
		// a final {"type":"text","text":"\n"} inline.
		blocks := `[{"type":"rich_text","elements":[` +
			`{"type":"rich_text_section","elements":[{"type":"text","text":"see "},{"type":"link","url":"https://example.com"}]},` +
			containers[0].json +
			`]}]`
		assertRejectsAbsorption(t, mux, blocks, "list")
	})
}

func assertRejectsAbsorption(t *testing.T, mux *http.ServeMux, blocks, containerLabel string) {
	t.Helper()
	_, err := runWithMockSessionStdin(t, blocks, mux, "draft", "create", "C01ABC")
	if err == nil {
		t.Fatalf("expected invalid_blocks error for unterminated rich_text_section before %s", containerLabel)
	}
	var oe *output.Error
	if !errors.As(err, &oe) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oe.Err != "invalid_blocks" {
		t.Errorf("expected err=invalid_blocks, got %q", oe.Err)
	}
	// Detail must name the offending shape and the trailing-\n fix.
	if !strings.Contains(oe.Detail, "rich_text_section") {
		t.Errorf("expected Detail to mention rich_text_section, got %q", oe.Detail)
	}
	if !strings.Contains(oe.Hint, "skills add tammersaleh/slack-cli") {
		t.Errorf("expected Hint to point at skill install, got %q", oe.Hint)
	}
	if !strings.Contains(oe.Detail, `\n`) {
		t.Errorf("expected Detail to mention the trailing newline rule, got %q", oe.Detail)
	}
}

func TestDraftCreate_AcceptsAbsorbingContainerAfterTerminatedSection(t *testing.T) {
	// Positive: when the section's text terminates with "\n", absorption
	// doesn't trigger. Slack's own editor produces this shape - see the
	// reference draft captured by driving the web compose editor.
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr99999", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": richTextBlocks("x"), "destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	containers := []struct {
		name, json string
	}{
		{"list", `{"type":"rich_text_list","style":"bullet","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"item"}]}]}`},
		{"preformatted", `{"type":"rich_text_preformatted","elements":[{"type":"text","text":"code body"}]}`},
		{"quote", `{"type":"rich_text_quote","elements":[{"type":"text","text":"quoted body"}]}`},
	}

	terminated := `{"type":"rich_text_section","elements":[{"type":"text","text":"heading:\n"}]}`
	for _, c := range containers {
		t.Run("intra_block_"+c.name, func(t *testing.T) {
			gotForm = nil
			blocks := `[{"type":"rich_text","elements":[` + terminated + `,` + c.json + `]}]`
			if _, err := runWithMockSessionStdin(t, blocks, mux, "draft", "create", "C01ABC"); err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if gotForm.Get("blocks") != blocks {
				t.Errorf("blocks should pass through verbatim")
			}
		})
		t.Run("cross_block_"+c.name, func(t *testing.T) {
			gotForm = nil
			blocks := `[` +
				`{"type":"rich_text","elements":[` + terminated + `]},` +
				`{"type":"rich_text","elements":[` + c.json + `]}` +
				`]`
			if _, err := runWithMockSessionStdin(t, blocks, mux, "draft", "create", "C01ABC"); err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if gotForm.Get("blocks") != blocks {
				t.Errorf("blocks should pass through verbatim; got %q", gotForm.Get("blocks"))
			}
		})
	}

	t.Run("trailing_empty_text_ignored", func(t *testing.T) {
		// A trailing empty text inline shouldn't defeat the check - the
		// effective terminator is the last non-empty text inline.
		blocks := `[{"type":"rich_text","elements":[` +
			`{"type":"rich_text_section","elements":[{"type":"text","text":"heading\n"},{"type":"text","text":""}]},` +
			containers[0].json +
			`]}]`
		if _, err := runWithMockSessionStdin(t, blocks, mux, "draft", "create", "C01ABC"); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})

	t.Run("separate_newline_text_inline", func(t *testing.T) {
		// A common shape: styled heading text followed by a separate
		// "\n" text inline. Codex flagged this; it must pass.
		blocks := `[{"type":"rich_text","elements":[` +
			`{"type":"rich_text_section","elements":[{"type":"text","text":"heading","style":{"bold":true}},{"type":"text","text":"\n"}]},` +
			containers[0].json +
			`]}]`
		if _, err := runWithMockSessionStdin(t, blocks, mux, "draft", "create", "C01ABC"); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})

	t.Run("non_text_inline_with_trailing_newline_text", func(t *testing.T) {
		// A section that contains a non-text inline (link, emoji, etc.)
		// is still safe as long as a final text inline ending in "\n"
		// follows it.
		blocks := `[{"type":"rich_text","elements":[` +
			`{"type":"rich_text_section","elements":[{"type":"text","text":"see "},{"type":"link","url":"https://example.com"},{"type":"text","text":"\n"}]},` +
			containers[0].json +
			`]}]`
		if _, err := runWithMockSessionStdin(t, blocks, mux, "draft", "create", "C01ABC"); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})
}

func TestDraftCreate_AcceptsListWithoutLeadingSection(t *testing.T) {
	// Positive: a bare list, and list-then-section, must not trip the
	// new check.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		draftCreateResponder(t, map[string]any{
			"id": "Dr99999", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": richTextBlocks("x"), "destinations": []map[string]any{{"channel_id": "C01ABC"}},
		})(w, r)
	})

	for _, tc := range []struct {
		name, blocks string
	}{
		{
			name:   "list_only",
			blocks: `[{"type":"rich_text","elements":[{"type":"rich_text_list","style":"bullet","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"item"}]}]}]}]`,
		},
		{
			name: "list_then_section",
			blocks: `[{"type":"rich_text","elements":[` +
				`{"type":"rich_text_list","style":"bullet","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"item"}]}]},` +
				`{"type":"rich_text_section","elements":[{"type":"text","text":"tldr"}]}` +
				`]}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runWithMockSessionStdin(t, tc.blocks, mux, "draft", "create", "C01ABC"); err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestDraftCreate_BroadcastWithoutThread(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("x"), mux,
		"draft", "create", "C01ABC", "--broadcast",
	)
	if err == nil {
		t.Fatal("expected error for --broadcast without --thread")
	}
	if !strings.Contains(err.Error(), "invalid_input") {
		t.Errorf("expected invalid_input, got %v", err)
	}
}

func TestDraftCreate_InvalidTimestamp(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("x"), mux,
		"draft", "create", "C01ABC", "--at", "not-a-date",
	)
	if err == nil {
		t.Fatal("expected invalid_timestamp error")
	}
	if !strings.Contains(err.Error(), "invalid_timestamp") {
		t.Errorf("expected invalid_timestamp, got %v", err)
	}
}

func TestDraftUpdate_Blocks(t *testing.T) {
	existing := map[string]any{
		"id":              "Dr01234",
		"client_msg_id":   "uuid-existing",
		"date_created":    1709251200,
		"date_scheduled":  0,
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("old text"),
		"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
	}

	var gotUpdate url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotUpdate, _ = url.ParseQuery(string(body))
		updated := map[string]any{
			"id": "Dr01234", "client_msg_id": "uuid-existing", "date_created": 1709251200,
			"last_updated_ts": "1709251201.1234567",
			"blocks":          richTextBlocks("new text"),
			"destinations":    []map[string]any{{"channel_id": "C01ABC"}},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": updated})
	})

	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("new text"), mux,
		"draft", "update", "Dr01234",
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotUpdate.Get("draft_id") != "Dr01234" {
		t.Errorf("expected draft_id='Dr01234', got %q", gotUpdate.Get("draft_id"))
	}
	if gotUpdate.Get("client_last_updated_ts") != "1709251200.1200000" {
		t.Errorf("expected padded ts, got %q", gotUpdate.Get("client_last_updated_ts"))
	}
	if gotUpdate.Get("blocks") != richTextBlocksJSON("new text") {
		t.Errorf("blocks should be the piped JSON, got %q", gotUpdate.Get("blocks"))
	}
}

func TestDraftUpdate_HealsOldIsFromComposer(t *testing.T) {
	// Drafts created by the CLI before the fix have is_from_composer=false
	// stored server-side, which hides them from the Slack composer. Update
	// must rewrite the flag to "true" rather than round-tripping the stale
	// value.
	existing := map[string]any{
		"id":               "Dr01234",
		"client_msg_id":    "uuid-existing",
		"date_created":     1709251200,
		"date_scheduled":   0,
		"last_updated_ts":  "1709251200.12",
		"blocks":           richTextBlocks("old text"),
		"destinations":     []map[string]any{{"channel_id": "C01ABC"}},
		"is_from_composer": false,
	}

	var gotUpdate url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotUpdate, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "draft": existing})
	})

	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("new text"), mux,
		"draft", "update", "Dr01234",
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotUpdate.Get("is_from_composer") != "true" {
		t.Errorf("update must heal is_from_composer to 'true', got %q", gotUpdate.Get("is_from_composer"))
	}
}

func TestDraftUpdate_Reschedule(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "date_scheduled": 1700000000, "last_updated_ts": "1709251200.12",
		"blocks":       richTextBlocks("body"),
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

	// No stdin - blocks untouched.
	_, err := runWithMockSession(t, mux, "draft", "update", "Dr01234", "--at", "2025-04-20T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if gotUpdate.Get("date_scheduled") != "1745139600" {
		t.Errorf("expected date_scheduled=1745139600, got %q", gotUpdate.Get("date_scheduled"))
	}
	// Blocks should reflect the existing draft's blocks, not overwritten.
	if gotUpdate.Get("blocks") == "" {
		t.Error("blocks should round-trip existing draft content")
	}
}

func TestDraftUpdate_RejectsScheduleOnlyWhenExistingLacksRichText(t *testing.T) {
	// If the existing draft was created without rich_text (perhaps by an
	// older CLI or another client), a schedule-only update would round-trip
	// those blocks and Desktop would re-tombstone. Guard up front.
	existing := map[string]any{
		"id": "Dr01234", "date_scheduled": 0, "last_updated_ts": "1709251200.12",
		"blocks": []map[string]any{
			{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "legacy"}},
		},
		"destinations": []map[string]any{{"channel_id": "C01ABC"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.update should not be called when existing blocks fail validation")
	})

	_, err := runWithMockSession(t, mux, "draft", "update", "Dr01234", "--at", "2025-04-20T09:00:00Z")
	if err == nil {
		t.Fatal("expected invalid_blocks error")
	}
	var oe *output.Error
	if !errors.As(err, &oe) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oe.Err != "invalid_blocks" {
		t.Errorf("expected err=invalid_blocks, got %q", oe.Err)
	}
}

func TestDraftUpdate_RejectsScheduleOnlyWhenExistingHasAbsorption(t *testing.T) {
	// If the existing draft already contains a section + list/preformatted
	// /quote without the trailing-newline terminator, a schedule-only
	// update would round-trip the absorption-prone blocks and the user
	// would see the bad rendering again. Reject up front so the caller
	// pipes a corrected payload.
	existing := map[string]any{
		"id": "Dr01234", "date_scheduled": 0, "last_updated_ts": "1709251200.12",
		"blocks": []map[string]any{
			{
				"type": "rich_text",
				"elements": []map[string]any{
					{
						"type": "rich_text_section",
						"elements": []map[string]any{
							{"type": "text", "text": "Heading"},
						},
					},
					{
						"type":  "rich_text_list",
						"style": "bullet",
						"elements": []map[string]any{
							{
								"type": "rich_text_section",
								"elements": []map[string]any{
									{"type": "text", "text": "item"},
								},
							},
						},
					},
				},
			},
		},
		"destinations": []map[string]any{{"channel_id": "C01ABC"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("drafts.update should not be called when existing blocks fail validation")
	})

	_, err := runWithMockSession(t, mux, "draft", "update", "Dr01234", "--at", "2025-04-20T09:00:00Z")
	if err == nil {
		t.Fatal("expected invalid_blocks error")
	}
	var oe *output.Error
	if !errors.As(err, &oe) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oe.Err != "invalid_blocks" {
		t.Errorf("expected err=invalid_blocks, got %q", oe.Err)
	}
}

func TestDraftUpdate_ClearSchedule(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "date_scheduled": 1700000000, "last_updated_ts": "1709251200.12",
		"blocks":       richTextBlocks("body"),
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

	_, err := runWithMockSession(t, mux, "draft", "update", "Dr01234", "--clear-schedule")
	if err != nil {
		t.Fatal(err)
	}
	if gotUpdate.Get("date_scheduled") != "0" {
		t.Errorf("expected date_scheduled=0, got %q", gotUpdate.Get("date_scheduled"))
	}
}

func TestDraftUpdate_NotFound(t *testing.T) {
	existing := map[string]any{
		"id": "Dr09999", "last_updated_ts": "1709251200.12", "blocks": richTextBlocks("x"),
		"destinations": []map[string]any{{"channel_id": "C01ABC"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))

	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("x"), mux,
		"draft", "update", "DrMISSING",
	)
	if err == nil {
		t.Fatal("expected draft_not_found error")
	}
	if !strings.Contains(err.Error(), "draft_not_found") {
		t.Errorf("expected draft_not_found, got %v", err)
	}
}

func TestDraftUpdate_NoChanges(t *testing.T) {
	mux := http.NewServeMux()
	// No stdin, no flags.
	_, err := runWithMockSession(t, mux, "draft", "update", "Dr01234")
	if err == nil {
		t.Fatal("expected error for no changes")
	}
	if !strings.Contains(err.Error(), "invalid_input") {
		t.Errorf("expected invalid_input, got %v", err)
	}
}

func TestDraftUpdate_ConflictingScheduleFlags(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMockSession(t, mux, "draft", "update", "Dr01234",
		"--at", "2025-04-20T09:00:00Z", "--clear-schedule",
	)
	if err == nil {
		t.Fatal("expected error for conflicting schedule flags")
	}
	if !strings.Contains(err.Error(), "invalid_input") {
		t.Errorf("expected invalid_input, got %v", err)
	}
}

func TestDraftUpdate_AutoReplaceOnTombstone(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "is_deleted": true, "client_msg_id": "uuid-old",
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("old text"),
		"destinations":    []map[string]any{{"channel_id": "D01", "user_ids": []string{"U01"}}},
	}
	var updateCalled bool
	var createForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "drafts": []map[string]any{existing}})
	})
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		updateCalled = true
	})
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		createForm, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"draft": map[string]any{
				"id": "DrNEW5678", "client_msg_id": "uuid-new",
				"date_created": 1709260000, "last_updated_ts": "1709260000.1234567",
				"blocks":       richTextBlocks("new text"),
				"destinations": []map[string]any{{"channel_id": "D01"}},
			},
		})
	})

	out, err := runWithMockSessionStdin(t, richTextBlocksJSON("new text"), mux,
		"draft", "update", "Dr01234",
	)
	if err != nil {
		t.Fatalf("expected success (auto-replace), got %v", err)
	}
	if updateCalled {
		t.Error("drafts.update should NOT be called for tombstoned drafts")
	}

	var dests []map[string]any
	_ = json.Unmarshal([]byte(createForm.Get("destinations")), &dests)
	if _, hasUserIDs := dests[0]["user_ids"]; hasUserIDs {
		t.Errorf("user_ids should be stripped on replacement, got %v", dests[0])
	}
	if createForm.Get("client_msg_id") == "uuid-old" {
		t.Error("replacement should have fresh client_msg_id")
	}
	if createForm.Get("is_from_composer") != "true" {
		t.Errorf("replacement must be composer-originated to be visible to the Slack composer, got is_from_composer=%q", createForm.Get("is_from_composer"))
	}

	lines := nonEmptyLines(out)
	item := parseJSON(t, lines[0])
	if item["id"] != "DrNEW5678" {
		t.Errorf("expected new id DrNEW5678, got %q", item["id"])
	}
}

func TestDraftUpdate_AutoReplaceOnAPIError(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "is_deleted": false, "client_msg_id": "uuid-old",
		"last_updated_ts": "1709251200.12",
		"blocks":          richTextBlocks("old"),
		"destinations":    []map[string]any{{"channel_id": "C01"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "drafts": []map[string]any{existing}})
	})
	mux.HandleFunc("/api/drafts.update", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "draft_update_invalid"})
	})
	mux.HandleFunc("/api/drafts.create", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"draft": map[string]any{
				"id": "DrNEW", "client_msg_id": "uuid-new",
				"date_created": 1709260000, "last_updated_ts": "1709260000.1234567",
				"blocks":       richTextBlocks("new"),
				"destinations": []map[string]any{{"channel_id": "C01"}},
			},
		})
	})

	out, err := runWithMockSessionStdin(t, richTextBlocksJSON("new"), mux,
		"draft", "update", "Dr01234",
	)
	if err != nil {
		t.Fatalf("expected success (auto-replace on API error), got %v", err)
	}
	lines := nonEmptyLines(out)
	item := parseJSON(t, lines[0])
	if item["id"] != "DrNEW" {
		t.Errorf("expected new id, got %q", item["id"])
	}
}

func TestDraftDelete_Single(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "last_updated_ts": "1709251200.12",
		"blocks":       richTextBlocks("body"),
		"destinations": []map[string]any{{"channel_id": "C01ABC"}},
	}
	var gotDelete url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.delete", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotDelete, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	out, err := runWithMockSession(t, mux, "draft", "delete", "Dr01234")
	if err != nil {
		t.Fatal(err)
	}
	if gotDelete.Get("draft_id") != "Dr01234" {
		t.Errorf("expected draft_id=Dr01234, got %q", gotDelete.Get("draft_id"))
	}
	if gotDelete.Get("client_last_updated_ts") != "1709251200.1200000" {
		t.Errorf("expected padded ts, got %q", gotDelete.Get("client_last_updated_ts"))
	}
	lines := nonEmptyLines(out)
	item := parseJSON(t, lines[0])
	if item["deleted"] != true {
		t.Errorf("expected deleted=true, got %v", item["deleted"])
	}
}

func TestDraftDelete_Bulk(t *testing.T) {
	drafts := []map[string]any{
		{"id": "Dr01234", "last_updated_ts": "1709251200.12", "blocks": richTextBlocks("a"), "destinations": []map[string]any{{"channel_id": "C01ABC"}}},
		{"id": "Dr05678", "last_updated_ts": "1709251300.34", "blocks": richTextBlocks("b"), "destinations": []map[string]any{{"channel_id": "C01ABC"}}},
	}
	var deletedIDs []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", draftsListHandler(t, drafts, false))
	mux.HandleFunc("/api/drafts.delete", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		deletedIDs = append(deletedIDs, form.Get("draft_id"))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	out, err := runWithMockSession(t, mux, "draft", "delete", "Dr01234", "Dr05678")
	if err != nil {
		t.Fatal(err)
	}
	if len(deletedIDs) != 2 {
		t.Fatalf("expected 2 delete calls, got %d: %v", len(deletedIDs), deletedIDs)
	}
	if got := len(nonEmptyLines(out)); got != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", got, out)
	}
}

func TestDraftDelete_PartialFailure(t *testing.T) {
	existing := map[string]any{
		"id": "Dr01234", "last_updated_ts": "1709251200.12",
		"blocks":       richTextBlocks("a"),
		"destinations": []map[string]any{{"channel_id": "C01ABC"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", singleDraftListHandler(t, existing))
	mux.HandleFunc("/api/drafts.delete", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	out, err := runWithMockSession(t, mux, "draft", "delete", "Dr01234", "DrMISSING")
	if err == nil {
		t.Fatal("expected non-zero exit for partial failure")
	}
	var oErr *output.Error
	if errors.As(err, &oErr) {
		t.Errorf("partial failure returned *output.Error (would hit stderr): %v", err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}
	missing := parseJSON(t, lines[1])
	if missing["error"] != "draft_not_found" {
		t.Errorf("expected draft_not_found on second line, got %v", missing)
	}
	meta := parseJSON(t, lines[2])
	metaField, _ := meta["_meta"].(map[string]any)
	if metaField["error_count"].(float64) != 1 {
		t.Errorf("expected error_count=1, got %v", metaField["error_count"])
	}
}
