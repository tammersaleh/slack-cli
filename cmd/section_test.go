package cmd_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func sectionListHandler(t *testing.T, sections []map[string]any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"channel_sections": sections,
		})
	}
}

func TestSectionList(t *testing.T) {
	sections := []map[string]any{
		{
			"channel_section_id": "S01ABC",
			"name":               "Channels",
			"type":               "channels",
			"channel_ids_page":   map[string]any{"channel_ids": []string{"C01", "C02", "C03"}},
		},
		{
			"channel_section_id": "S02DEF",
			"name":               "Customers",
			"type":               "channels",
			"channel_ids_page":   map[string]any{"channel_ids": []string{"C04", "C05"}},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.list", sectionListHandler(t, sections))

	out, err := runWithMockSession(t, mux, "section", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 sections + meta), got %d:\n%s", len(lines), out)
	}

	s := parseJSON(t, lines[0])
	if s["id"] != "S01ABC" {
		t.Errorf("expected id='S01ABC', got %q", s["id"])
	}
	if s["name"] != "Channels" {
		t.Errorf("expected name='Channels', got %q", s["name"])
	}
	if s["channel_count"] != float64(3) {
		t.Errorf("expected channel_count=3, got %v", s["channel_count"])
	}
}

func TestSectionCreate(t *testing.T) {
	var gotName string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotName = r.FormValue("name")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                 true,
			"channel_section_id": "S04JKL",
		})
	})

	out, err := runWithMockSession(t, mux, "section", "create", "Archive")
	if err != nil {
		t.Fatal(err)
	}

	if gotName != "Archive" {
		t.Errorf("expected name='Archive' in request, got %q", gotName)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["id"] != "S04JKL" {
		t.Errorf("expected id='S04JKL', got %q", item["id"])
	}
	if item["name"] != "Archive" {
		t.Errorf("expected name='Archive', got %q", item["name"])
	}
}

func TestSectionChannels(t *testing.T) {
	sections := []map[string]any{
		{
			"channel_section_id": "S01ABC",
			"name":               "Customers",
			"type":               "channels",
			"channel_ids_page":   map[string]any{"channel_ids": []string{"C01", "C02"}},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.list", sectionListHandler(t, sections))
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ch := r.FormValue("channel")
		name := "unknown"
		if ch == "C01" {
			name = "ext-acme"
		} else if ch == "C02" {
			name = "ext-globex"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channel": map[string]any{
				"id":          ch,
				"name":        name,
				"is_archived": false,
			},
		})
	})

	out, err := runWithMockSession(t, mux, "section", "channels", "S01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 channels + meta), got %d:\n%s", len(lines), out)
	}

	// Channels may arrive in either order due to concurrent resolution,
	// but the iteration is over the deterministic channelIDs list.
	names := make(map[string]bool)
	for _, line := range lines[:2] {
		ch := parseJSON(t, line)
		names[ch["name"].(string)] = true
		if ch["id"] == nil {
			t.Error("expected non-empty id field")
		}
	}
	if !names["ext-acme"] {
		t.Error("expected ext-acme in results")
	}
	if !names["ext-globex"] {
		t.Error("expected ext-globex in results")
	}
}

func TestSectionChannels_NotFound(t *testing.T) {
	sections := []map[string]any{
		{
			"channel_section_id": "S01ABC",
			"name":               "Channels",
			"type":               "channels",
			"channel_ids_page":   map[string]any{"channel_ids": []string{}},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.list", sectionListHandler(t, sections))

	_, err := runWithMockSession(t, mux, "section", "channels", "S99NONEXISTENT")
	if err == nil {
		t.Fatal("expected error for nonexistent section")
	}
}

func TestSectionFind(t *testing.T) {
	sections := []map[string]any{
		{
			"channel_section_id": "S01ABC",
			"name":               "Customers",
			"type":               "channels",
			"channel_ids_page":   map[string]any{"channel_ids": []string{"C01", "C02", "C03"}},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.list", sectionListHandler(t, sections))
	mux.HandleFunc("/api/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ch := r.FormValue("channel")
		names := map[string]string{"C01": "ext-acme", "C02": "ext-globex", "C03": "internal-ops"}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channel": map[string]any{
				"id":          ch,
				"name":        names[ch],
				"is_archived": false,
			},
		})
	})

	out, err := runWithMockSession(t, mux, "section", "find", "ext-")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 matches + meta), got %d:\n%s", len(lines), out)
	}

	foundNames := make(map[string]bool)
	for _, line := range lines[:2] {
		item := parseJSON(t, line)
		name, _ := item["name"].(string)
		foundNames[name] = true
		if !strings.HasPrefix(name, "ext-") {
			t.Errorf("expected name starting with 'ext-', got %q", name)
		}
		if item["section_name"] != "Customers" {
			t.Errorf("expected section_name='Customers', got %q", item["section_name"])
		}
		if item["section_id"] != "S01ABC" {
			t.Errorf("expected section_id='S01ABC', got %q", item["section_id"])
		}
	}
	if !foundNames["ext-acme"] {
		t.Error("expected ext-acme in find results")
	}
	if !foundNames["ext-globex"] {
		t.Error("expected ext-globex in find results")
	}
}

func TestSectionMove(t *testing.T) {
	var gotPayload map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.channels.bulkUpdate", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/users.channelSections.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"channel_sections": []map[string]any{
				{
					"channel_section_id": "S01ABC",
					"name":               "Customers",
					"type":               "channels",
					"channel_ids_page":   map[string]any{"channel_ids": []string{"C01", "C03"}},
				},
				{
					"channel_section_id": "S02DEF",
					"name":               "Archive",
					"type":               "channels",
					"channel_ids_page":   map[string]any{"channel_ids": []string{}},
				},
			},
		})
	})

	out, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01", "--section", "S02DEF")
	if err != nil {
		t.Fatal(err)
	}

	// Verify the bulkUpdate payload.
	if gotPayload == nil {
		t.Fatal("expected bulkUpdate to be called")
	}
	sections, ok := gotPayload["channel_sections"].([]any)
	if !ok {
		t.Fatalf("expected channel_sections array, got %T", gotPayload["channel_sections"])
	}
	// S01ABC should have C01 removed (only C03 left).
	// S02DEF should have C01 added.
	for _, raw := range sections {
		s := raw.(map[string]any)
		sid := s["channel_section_id"].(string)
		page := s["channel_ids_page"].(map[string]any)
		ids := page["channel_ids"].([]any)
		if sid == "S01ABC" {
			if len(ids) != 1 || ids[0] != "C03" {
				t.Errorf("S01ABC should have [C03], got %v", ids)
			}
		}
		if sid == "S02DEF" {
			if len(ids) != 1 || ids[0] != "C01" {
				t.Errorf("S02DEF should have [C01], got %v", ids)
			}
		}
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (result + meta), got %d:\n%s", len(lines), out)
	}
	result := parseJSON(t, lines[0])
	if result["moved_count"] != float64(1) {
		t.Errorf("expected moved_count=1, got %v", result["moved_count"])
	}
	if result["target_section"] != "Archive" {
		t.Errorf("expected target_section='Archive', got %q", result["target_section"])
	}
}

func TestSectionMove_MissingFlags(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01")
	if err == nil {
		t.Fatal("expected error when neither --section nor --new-section provided")
	}
}

func TestSectionMove_ConflictingFlags(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01", "--section", "S01", "--new-section", "New")
	if err == nil {
		t.Fatal("expected error when both --section and --new-section provided")
	}
}

func TestSectionMove_NewSection(t *testing.T) {
	var createdName string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		createdName = r.FormValue("name")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                 true,
			"channel_section_id": "S99NEW",
		})
	})
	mux.HandleFunc("/api/users.channelSections.channels.bulkUpdate", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/users.channelSections.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"channel_sections": []map[string]any{},
		})
	})

	out, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01", "--new-section", "Archive")
	if err != nil {
		t.Fatal(err)
	}

	if createdName != "Archive" {
		t.Errorf("expected section create with name='Archive', got %q", createdName)
	}

	lines := nonEmptyLines(out)
	if len(lines) < 1 {
		t.Fatalf("expected at least 1 line, got %d:\n%s", len(lines), out)
	}
}

func TestSectionList_SessionTokenRequired(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMock(t, mux, "section", "list")
	if err == nil {
		t.Fatal("expected error for non-session token")
	}
}
