package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// Every multi-input command that stops on a fatal error must still end stdout
// with a trailer. Without one, a truncated run is a few rows and nothing
// else, which a caller that filters out _meta reads as a complete answer.
// missing_scope is the trigger because it is fatal (exit 2) and needs no
// retry wait; the first input fails, so the remaining inputs must go unasked.
func TestBulkCommands_FatalAbortWritesTrailer(t *testing.T) {
	const ts1, ts2 = "1709251200.000100", "1709251300.000200"
	tests := []struct {
		name     string
		endpoint string
		args     []string
	}{
		{"message get", "conversations.history", []string{"message", "get", "C01ABC", ts1, ts2}},
		{"message permalink", "chat.getPermalink", []string{"message", "permalink", "C01ABC", ts1, ts2}},
		{"channel info", "conversations.info", []string{"channel", "info", "C01ABC", "C02DEF"}},
		{"file info", "files.info", []string{"file", "info", "F01ABC", "F02DEF"}},
		{"dnd info", "dnd.info", []string{"dnd", "info", "U01XYZ", "U02MGR"}},
		{"presence get", "users.getPresence", []string{"presence", "get", "U01XYZ", "U02MGR"}},
		{"reaction list", "reactions.get", []string{"reaction", "list", "C01ABC", ts1, ts2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("/api/"+tt.endpoint, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing_scope"})
			})

			r := runWithMockFull(t, mux, tt.args...)
			assertFatalAbort(t, r.err, r.stdout, "missing_scope", output.ExitAuth, 0)
			if got := calls.Load(); got != 1 {
				t.Errorf("%s requests = %d, want 1 (inputs after the abort must not be asked)", tt.endpoint, got)
			}
		})
	}
}

// assertFatalAbort checks the abort contract: the classified error is
// returned for stderr, and stdout is wantRows rows then a trailer that says
// rows are missing and why, with no cursor.
func assertFatalAbort(t *testing.T, err error, stdout, wantCode string, wantExit, wantRows int) map[string]any {
	t.Helper()
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error (stderr), got %T: %v", err, err)
	}
	if oErr.Err != wantCode || oErr.Code != wantExit {
		t.Errorf("got %q exit %d, want %q exit %d", oErr.Err, oErr.Code, wantCode, wantExit)
	}
	rows, meta := rowsAndMeta(t, stdout)
	if len(rows) != wantRows {
		t.Errorf("rows = %d, want %d:\n%s", len(rows), wantRows, stdout)
	}
	if meta["has_more"] != true || meta["error"] != wantCode {
		t.Errorf("_meta = %v, want has_more true and error %q", meta, wantCode)
	}
	if _, has := meta["next_cursor"]; has {
		t.Errorf("_meta must carry no cursor: %v", meta)
	}
	return meta
}

// message get's thread-reply fallback has its own abort, separate from the
// history call's.
func TestMessageGet_FallbackFatalAbortWritesTrailer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []map[string]any{}})
	})
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing_scope"})
	})

	r := runWithMockFull(t, mux, "message", "get", "C01ABC", "1709251200.000100")
	assertFatalAbort(t, r.err, r.stdout, "missing_scope", output.ExitAuth, 0)
}

// draft delete: the first delete lands, the second hits a fatal error. The
// row for the first stays and the trailer says the run is incomplete.
func TestDraftDelete_FatalAbortWritesTrailer(t *testing.T) {
	drafts := []map[string]any{
		{"id": "Dr01234", "last_updated_ts": "1709251200.12", "blocks": richTextBlocks("a"), "destinations": []map[string]any{{"channel_id": "C01ABC"}}},
		{"id": "Dr05678", "last_updated_ts": "1709251300.34", "blocks": richTextBlocks("b"), "destinations": []map[string]any{{"channel_id": "C01ABC"}}},
	}
	var deletes atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.list", draftsListHandler(t, drafts, false))
	mux.HandleFunc("/api/drafts.delete", func(w http.ResponseWriter, r *http.Request) {
		if deletes.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
	})

	out, err := runWithMockSession(t, mux, "draft", "delete", "Dr01234", "Dr05678")
	assertFatalAbort(t, err, out, "invalid_auth", output.ExitAuth, 1)
}
