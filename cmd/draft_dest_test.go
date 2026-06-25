package cmd_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// wantInvalidInput asserts err is an *output.Error with Err == "invalid_input".
func wantInvalidInput(t *testing.T, err error) {
	t.Helper()
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_input" {
		t.Fatalf("expected invalid_input, got %v", err)
	}
}

// captureCreateForm stands up a drafts.create mock that records the posted
// form and replies with a minimal valid draft echoing the destinations.
func captureCreateForm(t *testing.T, gotForm *url.Values) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*gotForm, _ = url.ParseQuery(string(body))
		draftCreateResponder(t, map[string]any{
			"id": "Dr77777", "date_created": 1713300000, "last_updated_ts": "1713300000.1200000",
			"blocks": richTextBlocks("hi"), "destinations": []map[string]any{{"channel_id": "C0"}},
		})(w, r)
	}
}

func parseDests(t *testing.T, form url.Values) []map[string]any {
	t.Helper()
	var dests []map[string]any
	if err := json.Unmarshal([]byte(form.Get("destinations")), &dests); err != nil {
		t.Fatalf("destinations JSON parse: %v", err)
	}
	return dests
}

// A single user ID recipient builds a user_ids destination (new 1:1 DM),
// never a channel_id. Slack opens the conversation on send.
func TestDraftCreate_SingleUserID(t *testing.T) {
	var form url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreateForm(t, &form))
	mux.HandleFunc("/api/conversations.list", func(http.ResponseWriter, *http.Request) {
		t.Fatal("conversations.list should not be called for a user recipient")
	})

	if _, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux, "draft", "create", "U0ALICE"); err != nil {
		t.Fatal(err)
	}
	dests := parseDests(t, form)
	if dests[0]["channel_id"] != nil {
		t.Errorf("expected no channel_id, got %v", dests[0]["channel_id"])
	}
	ids, _ := dests[0]["user_ids"].([]any)
	if len(ids) != 1 || ids[0] != "U0ALICE" {
		t.Errorf("expected user_ids=[U0ALICE], got %v", dests[0]["user_ids"])
	}
}

// Multiple user IDs build an MPDM destination, order preserved, deduped.
func TestDraftCreate_MultiUserMPDM(t *testing.T) {
	var form url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreateForm(t, &form))

	if _, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux,
		"draft", "create", "U0ALICE", "U0BOB", "U0ALICE", "U0CAROL"); err != nil {
		t.Fatal(err)
	}
	dests := parseDests(t, form)
	ids, _ := dests[0]["user_ids"].([]any)
	got := make([]string, len(ids))
	for i, v := range ids {
		got[i], _ = v.(string)
	}
	want := []string{"U0ALICE", "U0BOB", "U0CAROL"}
	if len(got) != len(want) {
		t.Fatalf("expected %v (deduped, ordered), got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

// A bare name still resolves as a channel (backward compatible).
func TestDraftCreate_BareNameStaysChannel(t *testing.T) {
	var form url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreateForm(t, &form))
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                true,
			"channels":          []map[string]any{{"id": "C0GEN", "name": "general"}},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	if _, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux, "draft", "create", "general"); err != nil {
		t.Fatal(err)
	}
	dests := parseDests(t, form)
	if dests[0]["channel_id"] != "C0GEN" {
		t.Errorf("expected channel_id=C0GEN, got %v", dests[0])
	}
	if dests[0]["user_ids"] != nil {
		t.Errorf("expected no user_ids, got %v", dests[0]["user_ids"])
	}
}

// @name resolves to a user_ids destination via users.list.
func TestDraftCreate_AtNameResolvesUser(t *testing.T) {
	var form url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreateForm(t, &form))
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{"id": "U0ALICE", "name": "alice", "profile": map[string]any{"display_name": "alice"}},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	if _, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux, "draft", "create", "@alice"); err != nil {
		t.Fatal(err)
	}
	dests := parseDests(t, form)
	ids, _ := dests[0]["user_ids"].([]any)
	if len(ids) != 1 || ids[0] != "U0ALICE" {
		t.Errorf("expected user_ids=[U0ALICE], got %v", dests[0]["user_ids"])
	}
}

// Mixing a channel and a user recipient is a fatal input error.
func TestDraftCreate_MixChannelAndUserErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(http.ResponseWriter, *http.Request) {
		t.Fatal("drafts.create should not be called on a mixed-destination error")
	})
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux, "draft", "create", "#general", "U0ALICE")
	wantInvalidInput(t, err)
}

// More than one channel recipient is a fatal input error.
func TestDraftCreate_MultipleChannelsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(http.ResponseWriter, *http.Request) {
		t.Fatal("drafts.create should not be called")
	})
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux, "draft", "create", "C0ONE", "C0TWO")
	wantInvalidInput(t, err)
}

// --thread with a user destination is rejected: a user_ids set has no thread
// context to reply to.
func TestDraftCreate_ThreadWithUserErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(http.ResponseWriter, *http.Request) {
		t.Fatal("drafts.create should not be called")
	})
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux,
		"draft", "create", "U0ALICE", "--thread", "1713299000.123456")
	wantInvalidInput(t, err)
}

// Any unresolved user fails the whole command before drafts.create.
func TestDraftCreate_PartialUserResolveFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(http.ResponseWriter, *http.Request) {
		t.Fatal("drafts.create should not be called when a recipient is unresolved")
	})
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{"id": "U0ALICE", "name": "alice", "profile": map[string]any{"display_name": "alice"}},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux,
		"draft", "create", "@alice", "@nobody")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "user_not_found" {
		t.Fatalf("expected user_not_found, got %v", err)
	}
	if !strings.Contains(oe.Input, "@nobody") {
		t.Errorf("expected the unresolved input @nobody reported, got %q", oe.Input)
	}
}

// Two different inputs that resolve to the same user collapse to one ID -
// dedup operates on the resolved ID, not the raw input string.
func TestDraftCreate_DedupByResolvedID(t *testing.T) {
	var form url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", captureCreateForm(t, &form))
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{"id": "U0ALICE", "name": "alice", "profile": map[string]any{"display_name": "alice"}},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	// @alice and U0ALICE both resolve to U0ALICE; @bob is distinct.
	if _, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux,
		"draft", "create", "@alice", "U0BOB", "U0ALICE"); err != nil {
		t.Fatal(err)
	}
	dests := parseDests(t, form)
	ids, _ := dests[0]["user_ids"].([]any)
	got := make([]string, len(ids))
	for i, v := range ids {
		got[i], _ = v.(string)
	}
	want := []string{"U0ALICE", "U0BOB"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected deduped %v (first-seen order), got %v", want, got)
	}
}

// A malformed Slack URL recipient fast-fails with invalid_input (not
// channel_not_found / user_not_found) before any write, and ahead of the
// mix/arity checks - so the precise URL reason isn't masked.
func TestDraftCreate_MalformedURLInvalidInput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(http.ResponseWriter, *http.Request) {
		t.Fatal("drafts.create should not be called on a malformed-URL recipient")
	})
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux,
		"draft", "create", "https://app.slack.com/team/NOTANID")
	wantInvalidInput(t, err)
}

// A malformed URL alongside a valid user surfaces the URL error, not the
// "cannot mix recipients" error - bad URLs are rejected before bucketing.
func TestDraftCreate_MalformedURLBeatsMixError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/drafts.create", func(http.ResponseWriter, *http.Request) {
		t.Fatal("drafts.create should not be called")
	})
	const badURL = "https://app.slack.com/team/NOTANID"
	_, err := runWithMockSessionStdin(t, richTextBlocksJSON("hi"), mux,
		"draft", "create", "@alice", badURL)
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Err != "invalid_input" {
		t.Fatalf("expected invalid_input from the bad URL, got %v", err)
	}
	// The error must point at the offending URL, not the mix of recipients.
	if oe.Input != badURL {
		t.Errorf("expected Input to carry the bad URL, got %q", oe.Input)
	}
}
