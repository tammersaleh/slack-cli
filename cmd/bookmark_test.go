package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestBookmarkList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/bookmarks.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if ch := r.FormValue("channel_id"); ch != "C01ABC" {
			t.Errorf("expected channel_id='C01ABC', got %q", ch)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"bookmarks": []map[string]any{
				{
					"id":         "Bk01",
					"channel_id": "C01ABC",
					"title":      "Team Wiki",
					"link":       "https://wiki.example.com",
					"type":       "link",
				},
			},
		})
	})

	out, err := runWithMock(t, mux, "bookmark", "list", "C01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 bookmark + meta), got %d:\n%s", len(lines), out)
	}

	bm := parseJSON(t, lines[0])
	if bm["id"] != "Bk01" {
		t.Errorf("expected id='Bk01', got %q", bm["id"])
	}
	if bm["title"] != "Team Wiki" {
		t.Errorf("expected title='Team Wiki', got %q", bm["title"])
	}
	if bm["link"] != "https://wiki.example.com" {
		t.Errorf("expected link, got %q", bm["link"])
	}

	meta := parseJSON(t, lines[1])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != false {
		t.Error("expected has_more=false")
	}
}

func TestBookmarkList_ChannelResolution(t *testing.T) {
	bookmarksCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C01ABC", "name": "general", "is_member": true},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/bookmarks.list", func(w http.ResponseWriter, r *http.Request) {
		bookmarksCalled = true
		_ = r.ParseForm()
		if ch := r.FormValue("channel_id"); ch != "C01ABC" {
			t.Errorf("expected resolved channel_id C01ABC, got %q", ch)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"bookmarks": []map[string]any{},
		})
	})

	_, err := runWithMock(t, mux, "bookmark", "list", "#general")
	if err != nil {
		t.Fatal(err)
	}
	if !bookmarksCalled {
		t.Error("expected bookmarks.list to be called")
	}
}
