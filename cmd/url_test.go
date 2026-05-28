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
