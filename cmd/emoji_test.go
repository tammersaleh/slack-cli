package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestEmojiList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/emoji.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"emoji": map[string]string{
				"partyparrot": "https://emoji.slack-edge.com/partyparrot.gif",
				"thumbsup":    "alias:+1",
			},
		})
	})

	out, err := runWithMock(t, mux, "emoji", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 emoji + meta), got %d:\n%s", len(lines), out)
	}
}

func TestEmojiList_Query(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/emoji.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"emoji": map[string]string{
				"partyparrot": "https://emoji.slack-edge.com/partyparrot.gif",
				"party_blob":  "https://emoji.slack-edge.com/party_blob.png",
				"thumbsup":    "alias:+1",
			},
		})
	})

	out, err := runWithMock(t, mux, "emoji", "list", "--query", "party")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	// 2 matches (partyparrot, party_blob) + meta
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 matches + meta), got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["name"] == "thumbsup" {
		t.Error("thumbsup should be filtered out by --query party")
	}
}
