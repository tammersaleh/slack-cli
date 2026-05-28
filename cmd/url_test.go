package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// A channel URL in a channel arg resolves to the embedded ID without any
// name-resolution API call.
func TestChannelArg_AcceptsURL(t *testing.T) {
	var gotChannel string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.members", func(w http.ResponseWriter, r *http.Request) {
		gotChannel = r.FormValue("channel")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"members":           []string{"U01"},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	if _, err := runWithMock(t, mux, "channel", "members", "https://acme.slack.com/archives/C01ABC"); err != nil {
		t.Fatal(err)
	}
	if gotChannel != "C01ABC" {
		t.Errorf("conversations.members channel = %q, want C01ABC", gotChannel)
	}
}

// A wrong-kind URL (user link where a channel is expected) is a fatal
// invalid_input, and no API call is made.
func TestChannelArg_WrongKindURLIsInvalidInput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.members", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected API call for wrong-kind URL: %s", r.URL.Path)
	})

	r := runWithMockFull(t, mux, "channel", "members", "https://acme.slack.com/team/U01ABC")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", r.err, r.err)
	}
	if oErr.Err != "invalid_input" {
		t.Errorf("error = %q, want invalid_input", oErr.Err)
	}
}

// Per-item commands surface a wrong-kind URL as an inline invalid_input row
// (not channel_not_found) and still count it.
func TestChannelInfo_WrongKindURLPerItem(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected conversations.info call for wrong-kind URL")
	})

	out, err := runWithMock(t, mux, "channel", "info", "https://acme.slack.com/files/U01/F02/x.png")
	if err == nil {
		t.Fatal("expected error for partial failure")
	}
	lines := nonEmptyLines(out)
	errLine := parseJSON(t, lines[0])
	if errLine["error"] != "invalid_input" {
		t.Errorf("error = %v, want invalid_input", errLine["error"])
	}
}

// A single message permalink fills both the channel and ts slots.
func TestMessageGet_AcceptsMessageURL(t *testing.T) {
	var gotChannel, gotTS string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.FormValue("channel")
		gotTS = r.FormValue("oldest")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"messages": []map[string]any{{"type": "message", "user": "U01", "text": "hi", "ts": gotTS}},
		})
	})

	url := "https://acme.slack.com/archives/C01ABC/p1709251200000100"
	out, err := runWithMock(t, mux, "message", "get", url)
	if err != nil {
		t.Fatal(err)
	}
	if gotChannel != "C01ABC" {
		t.Errorf("channel = %q, want C01ABC", gotChannel)
	}
	if gotTS != "1709251200.000100" {
		t.Errorf("oldest = %q, want 1709251200.000100", gotTS)
	}
	msg := parseJSON(t, nonEmptyLines(out)[0])
	if msg["input"] != url {
		t.Errorf("input = %v, want %q", msg["input"], url)
	}
	if msg["channel_id"] != "C01ABC" {
		t.Errorf("channel_id = %v, want C01ABC", msg["channel_id"])
	}
}

// A list of message permalinks may span different channels in one call - a
// form the channel + ts grammar can't express.
func TestMessageGet_CrossChannelURLs(t *testing.T) {
	seen := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ch := r.FormValue("channel")
		seen[ch] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"messages": []map[string]any{{"type": "message", "text": "in " + ch, "ts": r.FormValue("oldest")}},
		})
	})

	out, err := runWithMock(t, mux, "message", "get",
		"https://acme.slack.com/archives/C01ABC/p1709251200000100",
		"https://acme.slack.com/archives/C02DEF/p1709251300000200")
	if err != nil {
		t.Fatal(err)
	}
	if !seen["C01ABC"] || !seen["C02DEF"] {
		t.Errorf("expected both channels queried, saw %v", seen)
	}
	if lines := nonEmptyLines(out); len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 messages + meta), got %d:\n%s", len(lines), out)
	}
}

// A channel URL in the channel slot still pairs with bare timestamps.
func TestMessageGet_ChannelURLWithTimestamp(t *testing.T) {
	var gotChannel, gotTS string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.FormValue("channel")
		gotTS = r.FormValue("oldest")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"messages": []map[string]any{{"type": "message", "text": "x", "ts": gotTS}},
		})
	})

	if _, err := runWithMock(t, mux, "message", "get",
		"https://acme.slack.com/archives/C01ABC", "1709251200.000100"); err != nil {
		t.Fatal(err)
	}
	if gotChannel != "C01ABC" {
		t.Errorf("channel = %q, want C01ABC", gotChannel)
	}
	if gotTS != "1709251200.000100" {
		t.Errorf("oldest = %q, want 1709251200.000100", gotTS)
	}
}

// Mixing a message URL with a bare timestamp is a fatal invalid_input before
// any API call.
func TestMessageGet_MixingIsInvalidInput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected conversations.history call for mixed args")
	})

	r := runWithMockFull(t, mux, "message", "get",
		"https://acme.slack.com/archives/C01ABC/p1709251200000100", "1709251300.000200")
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", r.err, r.err)
	}
	if oErr.Err != "invalid_input" {
		t.Errorf("error = %q, want invalid_input", oErr.Err)
	}
}

// reaction list accepts a message permalink and tags rows with channel_id.
func TestReactionList_AcceptsMessageURL(t *testing.T) {
	var gotChannel, gotTS string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/reactions.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.FormValue("channel")
		gotTS = r.FormValue("timestamp")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"type": "message",
			"message": map[string]any{
				"reactions": []map[string]any{{"name": "thumbsup", "count": 2, "users": []string{"U01", "U02"}}},
			},
		})
	})

	url := "https://acme.slack.com/archives/C01ABC/p1709251200000100"
	out, err := runWithMock(t, mux, "reaction", "list", url)
	if err != nil {
		t.Fatal(err)
	}
	if gotChannel != "C01ABC" {
		t.Errorf("channel = %q, want C01ABC", gotChannel)
	}
	if gotTS != "1709251200.000100" {
		t.Errorf("timestamp = %q, want 1709251200.000100", gotTS)
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	if row["input"] != url {
		t.Errorf("input = %v, want %q", row["input"], url)
	}
	if row["channel_id"] != "C01ABC" {
		t.Errorf("channel_id = %v, want C01ABC", row["channel_id"])
	}
	if row["name"] != "thumbsup" {
		t.Errorf("name = %v, want thumbsup", row["name"])
	}
}

// thread list accepts a link to any reply: thread_ts resolves to the parent,
// so the whole thread is returned.
func TestThreadList_AcceptsReplyURL(t *testing.T) {
	var gotChannel, gotTS string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.FormValue("channel")
		gotTS = r.FormValue("ts")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"type": "message", "text": "parent", "ts": "1709251200.000100", "thread_ts": "1709251200.000100", "reply_count": 1},
				{"type": "message", "text": "reply", "ts": "1709251300.000200", "thread_ts": "1709251200.000100"},
			},
		})
	})

	// Link points at the reply (p...0200); thread_ts is the parent (...0100).
	url := "https://acme.slack.com/archives/C01ABC/p1709251300000200?thread_ts=1709251200.000100&cid=C01ABC"
	out, err := runWithMock(t, mux, "thread", "list", url)
	if err != nil {
		t.Fatal(err)
	}
	if gotChannel != "C01ABC" {
		t.Errorf("channel = %q, want C01ABC", gotChannel)
	}
	if gotTS != "1709251200.000100" {
		t.Errorf("ts = %q, want parent 1709251200.000100", gotTS)
	}
	if lines := nonEmptyLines(out); len(lines) != 3 {
		t.Fatalf("expected 3 lines (parent + reply + meta), got %d:\n%s", len(lines), out)
	}
}

// A user profile URL resolves to the embedded ID for user args.
func TestUserArg_AcceptsURL(t *testing.T) {
	var gotUser string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.getPresence", func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.FormValue("user")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "presence": "active"})
	})

	if _, err := runWithMock(t, mux, "presence", "get", "https://acme.slack.com/team/U07XYZ"); err != nil {
		t.Fatal(err)
	}
	if gotUser != "U07XYZ" {
		t.Errorf("users.getPresence user = %q, want U07XYZ", gotUser)
	}
}
