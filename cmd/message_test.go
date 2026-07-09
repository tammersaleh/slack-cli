package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestMessageList_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "user": "U01", "text": "hello", "ts": "1709251200.000100"},
				{"type": "message", "user": "U02", "text": "world", "ts": "1709251100.000050"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 messages + meta), got %d:\n%s", len(lines), out)
	}

	msg := parseJSON(t, lines[0])
	if msg["text"] != "hello" {
		t.Errorf("expected text='hello', got %q", msg["text"])
	}
	// Timestamp enrichment.
	if _, ok := msg["ts_iso"]; !ok {
		t.Error("expected ts_iso field from timestamp enrichment")
	}
	// Each item should carry channel_id so agents can construct follow-up commands.
	if msg["channel_id"] != "C01ABC" {
		t.Errorf("expected channel_id='C01ABC', got %q", msg["channel_id"])
	}
}

func TestMessageList_HasRepliesFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "text": "has replies", "ts": "1709251200.000100", "reply_count": 3},
				{"type": "message", "text": "no replies", "ts": "1709251100.000050"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC", "--has-replies")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 message + meta), got %d:\n%s", len(lines), out)
	}

	msg := parseJSON(t, lines[0])
	if msg["text"] != "has replies" {
		t.Errorf("expected text='has replies', got %q", msg["text"])
	}
}

func TestMessageList_HasReactionsFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "text": "reacted", "ts": "1709251200.000100", "reactions": []map[string]any{{"name": "thumbsup", "count": 1}}},
				{"type": "message", "text": "no reactions", "ts": "1709251100.000050"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC", "--has-reactions")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 message + meta), got %d:\n%s", len(lines), out)
	}
}

func TestMessageGet_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		oldest := r.FormValue("oldest")
		if oldest == "1709251200.000100" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"messages": []map[string]any{{"type": "message", "user": "U01", "text": "found", "ts": "1709251200.000100"}},
			})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"messages": []map[string]any{},
			})
		}
	})

	out, err := runWithMock(t, mux, "message", "get", "C01ABC", "1709251200.000100")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	msg := parseJSON(t, lines[0])
	if msg["input"] != "1709251200.000100" {
		t.Errorf("expected input='1709251200.000100', got %q", msg["input"])
	}
	if msg["channel_id"] != "C01ABC" {
		t.Errorf("expected channel_id='C01ABC', got %v", msg["channel_id"])
	}
}

// TestMessageGet_ThreadReply exercises the fallback: conversations.history
// never returns thread replies, so an empty history triggers a chat.getPermalink
// lookup (which carries thread_ts) followed by a targeted conversations.replies.
func TestMessageGet_ThreadReply(t *testing.T) {
	const replyTS = "1783406403.451509"
	const parentTS = "1781254110.069429"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		// A reply ts is invisible to conversations.history.
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []map[string]any{}})
	})
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"channel":   "C01ABC",
			"permalink": "https://test.slack.com/archives/C01ABC/p1783406403451509?thread_ts=" + parentTS + "&cid=C01ABC",
		})
	})
	mux.HandleFunc("/api/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("ts") != parentTS {
			t.Errorf("expected replies ts=%s (thread parent), got %q", parentTS, r.FormValue("ts"))
		}
		// conversations.replies always prepends the thread parent, even with a
		// tight oldest/latest window - the fallback must scan past it.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "user": "U09", "text": "the parent", "ts": parentTS, "thread_ts": parentTS, "reply_count": 2},
				{"type": "message", "user": "U01", "text": "the reply", "ts": replyTS, "thread_ts": parentTS},
			},
		})
	})

	out, err := runWithMock(t, mux, "message", "get", "C01ABC", replyTS)
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (reply + meta), got %d:\n%s", len(lines), out)
	}

	msg := parseJSON(t, lines[0])
	if msg["text"] != "the reply" {
		t.Errorf("expected text='the reply', got %q", msg["text"])
	}
	if msg["ts"] != replyTS {
		t.Errorf("expected ts=%q, got %q", replyTS, msg["ts"])
	}
	if msg["input"] != replyTS {
		t.Errorf("expected input=%q, got %q", replyTS, msg["input"])
	}
	if msg["channel_id"] != "C01ABC" {
		t.Errorf("expected channel_id='C01ABC', got %v", msg["channel_id"])
	}
}

// TestMessageGet_ThreadReplyURL: a reply *permalink* already carries thread_ts,
// so the fallback must skip chat.getPermalink and go straight to replies.
func TestMessageGet_ThreadReplyURL(t *testing.T) {
	const replyTS = "1783406403.451509"
	const parentTS = "1781254110.069429"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []map[string]any{}})
	})
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("chat.getPermalink must not be called when the input URL already carries thread_ts")
	})
	mux.HandleFunc("/api/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		// conversations.replies always prepends the thread parent, even with a
		// tight oldest/latest window - the fallback must scan past it.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "user": "U09", "text": "the parent", "ts": parentTS, "thread_ts": parentTS, "reply_count": 2},
				{"type": "message", "user": "U01", "text": "the reply", "ts": replyTS, "thread_ts": parentTS},
			},
		})
	})

	url := "https://test.slack.com/archives/C01ABC/p1783406403451509?thread_ts=" + parentTS + "&cid=C01ABC"
	out, err := runWithMock(t, mux, "message", "get", url)
	if err != nil {
		t.Fatal(err)
	}

	msg := parseJSON(t, nonEmptyLines(out)[0])
	if msg["text"] != "the reply" {
		t.Errorf("expected text='the reply', got %q", msg["text"])
	}
}

func TestMessageGet_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"messages": []map[string]any{},
		})
	})
	// A genuinely missing ts: history is empty and the fallback's permalink
	// lookup 404s. The fallback must collapse this back to message_not_found,
	// not surface getPermalink's error.
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "message_not_found"})
	})

	out, err := runWithMock(t, mux, "message", "get", "C01ABC", "9999999999.999999")
	if err == nil {
		t.Fatal("expected error for not found message")
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (error + meta), got %d:\n%s", len(lines), out)
	}

	errLine := parseJSON(t, lines[0])
	if errLine["error"] != "message_not_found" {
		t.Errorf("expected error='message_not_found', got %q", errLine["error"])
	}
	if errLine["channel_id"] != "C01ABC" {
		t.Errorf("expected channel_id='C01ABC' on error row, got %v", errLine["channel_id"])
	}
}

// TestMessageGet_FallbackSystemicError: a non-miss error from the fallback
// (here missing_scope, which classifies as ExitGeneral just like
// message_not_found) must abort fatally, NOT collapse into a soft
// message_not_found row. Guards the isMessageMiss gate against being reduced
// to an exit-code check.
func TestMessageGet_FallbackSystemicError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []map[string]any{}})
	})
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing_scope"})
	})

	out, err := runWithMock(t, mux, "message", "get", "C01ABC", "1783406403.451509")
	if err == nil {
		t.Fatal("expected a fatal error when the fallback hits missing_scope")
	}
	if strings.Contains(out, "message_not_found") {
		t.Errorf("systemic error must not be masked as message_not_found; got:\n%s", out)
	}
}

// TestMessageGet_FallbackParseDrift: if chat.getPermalink returns a permalink
// slackurl.Parse can't handle (Slack changed the format), that's drift - it
// must surface, not silently regress to the original not-found bug.
func TestMessageGet_FallbackParseDrift(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []map[string]any{}})
	})
	mux.HandleFunc("/api/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		// Not a slack.com host - slackurl.Parse returns matched=true, err!=nil.
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permalink": "https://example.com/nope"})
	})

	out, err := runWithMock(t, mux, "message", "get", "C01ABC", "1783406403.451509")
	if err == nil {
		t.Fatal("expected a fatal error on permalink parse drift")
	}
	if strings.Contains(out, "message_not_found") {
		t.Errorf("parse drift must not be masked as message_not_found; got:\n%s", out)
	}
}

func TestMessageList_Pagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": true,
			"messages": []map[string]any{{"type": "message", "text": "msg1", "ts": "1709251200.000100"}},
			"response_metadata": map[string]string{"next_cursor": "nextpage"},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	meta := parseJSON(t, lines[1])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != true {
		t.Error("expected has_more=true")
	}
	if m["next_cursor"] != "nextpage" {
		t.Errorf("expected next_cursor='nextpage', got %q", m["next_cursor"])
	}
}

func TestMessageList_EnrichesUserName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"has_more": false,
			"messages": []map[string]any{
				{"type": "message", "user": "U01", "text": "hello", "ts": "1709251200.000100"},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	// Enrichment now fetches single-user metadata via users.info rather than
	// paginating users.list, to avoid the 17MB bulk load on large workspaces.
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("user") != "U01" {
			http.Error(w, "wrong user id", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"user": map[string]any{
				"id": "U01", "name": "tammer", "real_name": "Tammer Saleh",
				"profile": map[string]any{"email": "t@example.com", "display_name": "tammer"},
			},
		})
	})
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("users.list should not be called during enrichment of a single user")
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	msg := parseJSON(t, lines[0])
	if msg["user"] != "U01" {
		t.Errorf("expected user='U01', got %q", msg["user"])
	}
	if msg["user_name"] == nil || msg["user_name"] == "" {
		t.Error("expected user_name to be enriched via users.info")
	}
}
