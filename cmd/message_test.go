package cmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
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
			"ok":                true,
			"has_more":          true,
			"messages":          []map[string]any{{"type": "message", "text": "msg1", "ts": "1709251200.000100"}},
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

// historyWindowMux emulates the paging behavior of conversations.history that
// `message list --after/--before` depends on. Verified live against Slack on
// 2026-07-27 (see CLAUDE.md, "Time bounds are paging state"):
//
//   - oldest/latest filter the returned messages.
//   - With latest set, or with no bounds at all, the walk runs newest-first.
//   - With only oldest set, the walk runs oldest-first.
//   - A cursor replaces the anchor bound; the opposite bound still filters.
//
// The last two rules are why the bounds have to ride along on every request:
// drop them on page two and the window stops being applied, so the walk runs
// straight past the requested range into the rest of the channel.
//
// Simplifications against real Slack: bounds are inclusive on both ends, and
// the cursor is the ts of the next message rather than an opaque token. Both
// are self-consistent within the mock, which is all the assertions rest on.
func historyWindowMux(t *testing.T, all []string, seen *[]url.Values) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		oldest, latest := r.FormValue("oldest"), r.FormValue("latest")
		anchor := r.FormValue("cursor")
		if seen != nil {
			*seen = append(*seen, r.PostForm)
		}

		ordered := append([]string(nil), all...)
		sort.Sort(sort.Reverse(sort.StringSlice(ordered)))
		ascending := oldest != "" && latest == ""
		if ascending {
			sort.Strings(ordered)
		}

		var match []string
		for _, ts := range ordered {
			switch {
			case ascending && anchor != "" && ts < anchor:
				continue
			case ascending && anchor == "" && ts < oldest:
				continue
			case !ascending && anchor != "" && ts > anchor:
				continue
			case !ascending && anchor == "" && latest != "" && ts > latest:
				continue
			case !ascending && oldest != "" && ts < oldest:
				continue
			case ascending && latest != "" && ts > latest:
				continue
			}
			match = append(match, ts)
		}

		limit := 2
		if n, err := strconv.Atoi(r.FormValue("limit")); err == nil && n > 0 {
			limit = n
		}
		next := ""
		if len(match) > limit {
			next = match[limit]
			match = match[:limit]
		}

		// Slack serializes every page newest-first, even when the walk
		// itself is ascending - which is what makes an oldest-only --all run
		// a sawtooth rather than one sorted stream.
		if ascending {
			sort.Sort(sort.Reverse(sort.StringSlice(match)))
		}
		msgs := make([]map[string]any, 0, len(match))
		for _, ts := range match {
			msgs = append(msgs, map[string]any{"type": "message", "text": "m" + ts, "ts": ts})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"has_more":          next != "",
			"messages":          msgs,
			"response_metadata": map[string]string{"next_cursor": next},
		})
	})
	return mux
}

// windowTimestamps returns 1700000001.000000 .. 1700000011.000000.
func windowTimestamps() []string {
	var out []string
	for i := 1; i <= 11; i++ {
		out = append(out, fmt.Sprintf("17000000%02d.000000", i))
	}
	return out
}

func emittedTimestamps(t *testing.T, out string) []string {
	t.Helper()
	var got []string
	for _, line := range nonEmptyLines(out) {
		m := parseJSON(t, line)
		if ts, ok := m["ts"].(string); ok {
			got = append(got, ts)
		}
	}
	return got
}

// TestMessageList_AllKeepsTimeBounds pins the whole point of --after/--before:
// an --all walk must stay inside the window. Sending the bounds only on the
// first page makes Slack forget the window and stream the rest of the channel.
func TestMessageList_AllKeepsTimeBounds(t *testing.T) {
	var reqs []url.Values
	mux := historyWindowMux(t, windowTimestamps(), &reqs)

	out, err := runWithMock(t, mux, "message", "list", "C01ABC",
		"--after", "1700000004", "--before", "1700000008", "--all", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"1700000008.000000", "1700000007.000000", "1700000006.000000",
		"1700000005.000000", "1700000004.000000",
	}
	got := emittedTimestamps(t, out)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("walk left the requested window\n got: %v\nwant: %v", got, want)
	}

	if len(reqs) < 2 {
		t.Fatalf("expected the walk to span at least 2 requests, got %d", len(reqs))
	}
	for i, req := range reqs {
		if req.Get("oldest") != "1700000004.000000" || req.Get("latest") != "1700000008.000000" {
			t.Errorf("request %d dropped its time bounds: oldest=%q latest=%q",
				i, req.Get("oldest"), req.Get("latest"))
		}
	}

	lines := nonEmptyLines(out)
	meta := parseJSON(t, lines[len(lines)-1])["_meta"].(map[string]any)
	if meta["has_more"] != false {
		t.Errorf("expected the window to be exhausted, got has_more=%v", meta["has_more"])
	}
}

// TestMessageList_CursorResumeKeepsTimeBounds guards the fix that was
// *proposed* for this and rejected: dropping oldest/latest whenever --cursor
// is set, on the theory that the cursor already encodes the filtered position.
// It does not. The cursor names a position; only the bounds say where to stop
// and which way to walk, so a resume has to repeat the bounds its cursor was
// produced under. This test fails if that suppression is ever added; the
// window is exhausted at the resume point, so dropping the bounds turns a
// terminal page into an open-ended walk.
func TestMessageList_CursorResumeKeepsTimeBounds(t *testing.T) {
	var reqs []url.Values
	mux := historyWindowMux(t, windowTimestamps(), &reqs)

	out, err := runWithMock(t, mux, "message", "list", "C01ABC",
		"--after", "1700000004", "--before", "1700000008",
		"--cursor", "1700000005.000000", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"1700000005.000000", "1700000004.000000"}
	if got := emittedTimestamps(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("resumed page left the requested window\n got: %v\nwant: %v", got, want)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Get("oldest") != "1700000004.000000" || reqs[0].Get("latest") != "1700000008.000000" {
		t.Errorf("resume request dropped its bounds: oldest=%q latest=%q",
			reqs[0].Get("oldest"), reqs[0].Get("latest"))
	}

	lines := nonEmptyLines(out)
	meta := parseJSON(t, lines[len(lines)-1])["_meta"].(map[string]any)
	if meta["has_more"] != false {
		t.Errorf("expected the window to be exhausted, got has_more=%v", meta["has_more"])
	}
}

// TestMessageList_AllKeepsTimeBoundsOldestOnly covers the other paging
// direction. With oldest set and latest absent, conversations.history walks
// forward in time instead of backward, and it was the worse of the two
// failures live: dropping the bounds on page two resumed the same cursor
// *backwards*, re-emitting messages page one had already printed and never
// terminating. The expected order is the documented sawtooth - newest-first
// within a page, pages ascending.
func TestMessageList_AllKeepsTimeBoundsOldestOnly(t *testing.T) {
	var reqs []url.Values
	mux := historyWindowMux(t, windowTimestamps(), &reqs)

	out, err := runWithMock(t, mux, "message", "list", "C01ABC",
		"--after", "1700000008", "--all", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"1700000009.000000", "1700000008.000000",
		"1700000011.000000", "1700000010.000000",
	}
	if got := emittedTimestamps(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("forward walk left the requested window\n got: %v\nwant: %v", got, want)
	}

	if len(reqs) < 2 {
		t.Fatalf("expected the walk to span at least 2 requests, got %d", len(reqs))
	}
	for i, req := range reqs {
		if req.Get("oldest") != "1700000008.000000" {
			t.Errorf("request %d dropped its lower bound: oldest=%q", i, req.Get("oldest"))
		}
		if req.Get("latest") != "" {
			t.Errorf("request %d invented an upper bound: latest=%q", i, req.Get("latest"))
		}
	}

	lines := nonEmptyLines(out)
	meta := parseJSON(t, lines[len(lines)-1])["_meta"].(map[string]any)
	if meta["has_more"] != false {
		t.Errorf("expected the window to be exhausted, got has_more=%v", meta["has_more"])
	}
}

// TestMessageList_AllKeepsUnreadBound is the same forward walk reached the way
// users actually reach it. --unread reads oldest from the channel's last_read
// marker and never sets latest, so every --unread --all run takes the
// oldest-only path.
func TestMessageList_AllKeepsUnreadBound(t *testing.T) {
	var reqs []url.Values
	mux := historyWindowMux(t, windowTimestamps(), &reqs)
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"channel": map[string]any{"id": "C01ABC", "name": "general", "last_read": "1700000008.000000"},
		})
	})

	out, err := runWithMock(t, mux, "message", "list", "C01ABC", "--unread", "--all", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"1700000009.000000", "1700000008.000000",
		"1700000011.000000", "1700000010.000000",
	}
	if got := emittedTimestamps(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("unread walk left the last_read window\n got: %v\nwant: %v", got, want)
	}
	for i, req := range reqs {
		if req.Get("oldest") != "1700000008.000000" {
			t.Errorf("request %d dropped the last_read bound: oldest=%q", i, req.Get("oldest"))
		}
	}
}
