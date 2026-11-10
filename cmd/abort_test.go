package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// Every multi-input command that stops on a fatal error must still end stdout
// with a trailer. Without one, a truncated run is a few rows and nothing
// else, which a caller that filters out _meta reads as a complete answer.
// The first input fails with a per-row error, the second with missing_scope
// (fatal, exit 2, no retry wait), and the third must go unasked. The trailer
// carries the per-row failure counted before the abort.
func TestBulkCommands_FatalAbortWritesTrailer(t *testing.T) {
	const ts1, ts2, ts3 = "1709251200.000100", "1709251300.000200", "1709251400.000300"
	tests := []struct {
		name     string
		endpoint string
		args     []string
	}{
		{"message get", "conversations.history", []string{"message", "get", "C01ABC", ts1, ts2, ts3}},
		{"message permalink", "chat.getPermalink", []string{"message", "permalink", "C01ABC", ts1, ts2, ts3}},
		{"channel info", "conversations.info", []string{"channel", "info", "C01ABC", "C02DEF", "C03GHI"}},
		{"file info", "files.info", []string{"file", "info", "F01ABC", "F02DEF", "F03GHI"}},
		{"dnd info", "dnd.info", []string{"dnd", "info", "U01XYZ", "U02MGR", "U03ABC"}},
		{"presence get", "users.getPresence", []string{"presence", "get", "U01XYZ", "U02MGR", "U03ABC"}},
		{"reaction list", "reactions.get", []string{"reaction", "list", "C01ABC", ts1, ts2, ts3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("/api/"+tt.endpoint, func(w http.ResponseWriter, r *http.Request) {
				code := "missing_scope"
				if calls.Add(1) == 1 {
					code = "fatal_error"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
			})

			r := runWithMockFull(t, mux, tt.args...)
			meta := assertFatalAbort(t, r.err, r.stdout, "missing_scope", output.ExitAuth, 1)
			if meta["error_count"] != float64(1) {
				t.Errorf("_meta.error_count = %v, want 1", meta["error_count"])
			}
			if got := calls.Load(); got != 2 {
				t.Errorf("%s requests = %d, want 2 (inputs after the abort must not be asked)", tt.endpoint, got)
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

// An expired --timeout is fatal for the whole run: the context is dead, so
// every remaining input would fail the same way. One trailer, not a timeout
// row per input.
func TestBulkCommands_TimeoutAbortsWithTrailer(t *testing.T) {
	var calls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})

	r := runWithMockFull(t, mux, "--timeout", "300ms", "message", "permalink", "C01ABC", "1709251200.000100", "1709251300.000200")
	assertFatalAbort(t, r.err, r.stdout, "timeout", output.ExitGeneral, 0)
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

// Commands that take users resolve an email through users.lookupByEmail. A
// systemic failure there is not a missing user: it stops the run.
func TestUserCommands_ResolveSystemicAbortWritesTrailer(t *testing.T) {
	for _, args := range [][]string{
		{"dnd", "info"},
		{"presence", "get"},
		{"status", "get"},
	} {
		t.Run(args[0], func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "members": []any{}})
			})
			mux.HandleFunc("/api/users.lookupByEmail", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing_scope"})
			})

			r := runWithMockFull(t, mux, append(args, "alice@example.com", "U02MGR")...)
			assertFatalAbort(t, r.err, r.stdout, "missing_scope", output.ExitAuth, 0)
		})
	}
}

// status get shares user info's lookup: a systemic users.info failure stops
// the run, and any other failure keeps its own code.
func TestStatusGet_LookupFailures(t *testing.T) {
	t.Run("systemic aborts", func(t *testing.T) {
		m := &profileMock{infoErr: func(user string) string {
			if user == "U02" {
				return "invalid_auth"
			}
			return ""
		}}
		r := runWithMockFull(t, m.mux(), "status", "get", "U01", "U02", "U03")
		assertFatalAbort(t, r.err, r.stdout, "invalid_auth", output.ExitAuth, 1)
	})
	t.Run("per-user failure keeps its code", func(t *testing.T) {
		m := &profileMock{infoErr: func(user string) string {
			if user == "U02" {
				return "fatal_error"
			}
			return ""
		}}
		r := runWithMockFull(t, m.mux(), "status", "get", "U01", "U02")
		assertExitError(t, r.err)
		rows, _ := rowsAndMeta(t, r.stdout)
		if len(rows) != 2 || rows[1]["input"] != "U02" || rows[1]["error"] != "fatal_error" {
			t.Errorf("expected {input:U02, error:fatal_error}, got %v", rows)
		}
	})
}
