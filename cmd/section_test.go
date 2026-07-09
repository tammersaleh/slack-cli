package cmd_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

// bulkOp mirrors one entry in the insert/remove arrays sent to
// users.channelSections.channels.bulkUpdate.
type bulkOp struct {
	SectionID  string   `json:"channel_section_id"`
	ChannelIDs []string `json:"channel_ids"`
}

// sectionStore is a stateful in-memory model of a workspace's sidebar
// sections, used to faithfully exercise the section-move round-trip. It
// reproduces the real API's insert/remove semantics: remove pulls a channel
// out of a section; insert adds a channel to a section ONLY if that channel
// is not currently held by any other section (insert on an already-sectioned
// channel is a no-op, which is the whole bug under test).
type sectionStore struct {
	mu       sync.Mutex
	order    []string            // stable section id order
	names    map[string]string   // section id -> name
	channels map[string][]string // section id -> channel ids
}

func newSectionStore() *sectionStore {
	return &sectionStore{names: map[string]string{}, channels: map[string][]string{}}
}

func (s *sectionStore) addSection(id, name string, chans []string) {
	s.order = append(s.order, id)
	s.names[id] = name
	s.channels[id] = append([]string{}, chans...)
}

func (s *sectionStore) sectionOf(ch string) string {
	for _, sid := range s.order {
		for _, c := range s.channels[sid] {
			if c == ch {
				return sid
			}
		}
	}
	return ""
}

func (s *sectionStore) applyRemove(ops []bulkOp) {
	for _, op := range ops {
		remaining := s.channels[op.SectionID][:0:0]
		drop := map[string]bool{}
		for _, c := range op.ChannelIDs {
			drop[c] = true
		}
		for _, c := range s.channels[op.SectionID] {
			if !drop[c] {
				remaining = append(remaining, c)
			}
		}
		s.channels[op.SectionID] = remaining
	}
}

func (s *sectionStore) applyInsert(ops []bulkOp) {
	for _, op := range ops {
		for _, c := range op.ChannelIDs {
			// Slack ignores an insert while the channel still lives in
			// another section.
			if cur := s.sectionOf(c); cur != "" && cur != op.SectionID {
				continue
			}
			if s.sectionOf(c) == op.SectionID {
				continue
			}
			s.channels[op.SectionID] = append(s.channels[op.SectionID], c)
		}
	}
}

// mux builds a handler serving list + bulkUpdate (remove applied before
// insert, matching the real single-call move). Captured insert/remove ops
// are written to the provided pointers.
func (s *sectionStore) mux(gotInsert, gotRemove *[]bulkOp) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.list", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var secs []map[string]any
		for _, sid := range s.order {
			secs = append(secs, map[string]any{
				"channel_section_id": sid,
				"name":               s.names[sid],
				"type":               "channels",
				"channel_ids_page":   map[string]any{"channel_ids": s.channels[sid]},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel_sections": secs})
	})
	mux.HandleFunc("/api/users.channelSections.channels.bulkUpdate", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		defer s.mu.Unlock()
		if v := r.FormValue("remove"); v != "" {
			var ops []bulkOp
			_ = json.Unmarshal([]byte(v), &ops)
			*gotRemove = ops
			s.applyRemove(ops)
		}
		if v := r.FormValue("insert"); v != "" {
			var ops []bulkOp
			_ = json.Unmarshal([]byte(v), &ops)
			*gotInsert = ops
			s.applyInsert(ops)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	return mux
}

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
	store := newSectionStore()
	store.addSection("S01ABC", "Customers", []string{"C01", "C03"})
	store.addSection("S02DEF", "Archive", nil)

	var gotInsert, gotRemove []bulkOp
	mux := store.mux(&gotInsert, &gotRemove)

	out, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01", "--section", "S02DEF")
	if err != nil {
		t.Fatal(err)
	}

	// The move must both insert C01 into the target and remove it from its
	// current section - insert alone is a silent no-op on the real API.
	if len(gotInsert) != 1 || gotInsert[0].SectionID != "S02DEF" ||
		len(gotInsert[0].ChannelIDs) != 1 || gotInsert[0].ChannelIDs[0] != "C01" {
		t.Errorf("expected insert [{S02DEF:[C01]}], got %+v", gotInsert)
	}
	if len(gotRemove) != 1 || gotRemove[0].SectionID != "S01ABC" ||
		len(gotRemove[0].ChannelIDs) != 1 || gotRemove[0].ChannelIDs[0] != "C01" {
		t.Errorf("expected remove [{S01ABC:[C01]}], got %+v", gotRemove)
	}

	// The channel actually landed in the target and left the source.
	if got := store.sectionOf("C01"); got != "S02DEF" {
		t.Errorf("C01 should now be in S02DEF, got %q", got)
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

// TestSectionMove_MultiSource covers moving several channels that live in
// different source sections in one call: remove groups per source, insert
// into the single target.
func TestSectionMove_MultiSource(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Customers", []string{"C01", "C02"})
	store.addSection("S02DEF", "Partners", []string{"C03"})
	store.addSection("S03GHI", "Archive", nil)

	var gotInsert, gotRemove []bulkOp
	mux := store.mux(&gotInsert, &gotRemove)

	// Move C01 (from S01ABC) and C03 (from S02DEF) into S03GHI.
	out, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01,C03", "--section", "S03GHI")
	if err != nil {
		t.Fatal(err)
	}

	if len(gotInsert) != 1 || gotInsert[0].SectionID != "S03GHI" {
		t.Fatalf("expected single insert into S03GHI, got %+v", gotInsert)
	}
	// remove is grouped by source section, sorted by section id.
	sort.Slice(gotRemove, func(i, j int) bool { return gotRemove[i].SectionID < gotRemove[j].SectionID })
	if len(gotRemove) != 2 {
		t.Fatalf("expected 2 remove groups, got %+v", gotRemove)
	}
	if gotRemove[0].SectionID != "S01ABC" || gotRemove[0].ChannelIDs[0] != "C01" {
		t.Errorf("expected S01ABC removes C01, got %+v", gotRemove[0])
	}
	if gotRemove[1].SectionID != "S02DEF" || gotRemove[1].ChannelIDs[0] != "C03" {
		t.Errorf("expected S02DEF removes C03, got %+v", gotRemove[1])
	}

	if store.sectionOf("C01") != "S03GHI" || store.sectionOf("C03") != "S03GHI" {
		t.Errorf("both channels should be in S03GHI: C01=%q C03=%q", store.sectionOf("C01"), store.sectionOf("C03"))
	}

	result := parseJSON(t, nonEmptyLines(out)[0])
	if result["moved_count"] != float64(2) {
		t.Errorf("expected moved_count=2, got %v", result["moved_count"])
	}
}

// TestSectionMove_SectionNotFound guards against a typo'd --section silently
// no-opping (a sibling of the original bug).
func TestSectionMove_SectionNotFound(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Customers", []string{"C01"})
	var gotInsert, gotRemove []bulkOp
	mux := store.mux(&gotInsert, &gotRemove)

	_, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01", "--section", "S99NOPE")
	if err == nil {
		t.Fatal("expected error for nonexistent target section")
	}
	if gotInsert != nil || gotRemove != nil {
		t.Error("bulkUpdate must not be called when the target section is invalid")
	}
}

// TestSectionMove_AlreadyInTarget: a channel already in the target and one
// unsectioned channel. The insert-only path must add the unsectioned channel,
// and re-running against a channel already in target is an idempotent success
// (no remove, still counted as moved).
func TestSectionMove_AlreadyInTarget(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Customers", []string{"C01"}) // C01 already in target
	// C02 exists in no section at all.

	var gotInsert, gotRemove []bulkOp
	mux := store.mux(&gotInsert, &gotRemove)

	out, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01,C02", "--section", "S01ABC")
	if err != nil {
		t.Fatal(err)
	}

	// Neither channel is in a *different* section, so there is nothing to
	// remove and remove must be omitted entirely.
	if gotRemove != nil {
		t.Errorf("expected no remove ops, got %+v", gotRemove)
	}
	if len(gotInsert) != 1 || gotInsert[0].SectionID != "S01ABC" || len(gotInsert[0].ChannelIDs) != 2 {
		t.Errorf("expected insert of both channels into S01ABC, got %+v", gotInsert)
	}
	if store.sectionOf("C01") != "S01ABC" || store.sectionOf("C02") != "S01ABC" {
		t.Errorf("both channels should be in S01ABC: C01=%q C02=%q", store.sectionOf("C01"), store.sectionOf("C02"))
	}
	result := parseJSON(t, nonEmptyLines(out)[0])
	if result["moved_count"] != float64(2) {
		t.Errorf("expected moved_count=2 (both now in target), got %v", result["moved_count"])
	}
}

// TestSectionMove_EmptyChannels: --channels that normalizes to nothing is a
// fatal invalid_input and must not touch the API.
func TestSectionMove_EmptyChannels(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Customers", nil)
	var gotInsert, gotRemove []bulkOp
	mux := store.mux(&gotInsert, &gotRemove)

	_, err := runWithMockSession(t, mux, "section", "move", "--channels", " , ,", "--section", "S01ABC")
	if err == nil {
		t.Fatal("expected error for empty channel list")
	}
	if gotInsert != nil || gotRemove != nil {
		t.Error("bulkUpdate must not be called with no channels")
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
	store := newSectionStore()
	store.addSection("S01ABC", "Customers", []string{"C01"})

	var createdName string
	var gotInsert, gotRemove []bulkOp
	mux := store.mux(&gotInsert, &gotRemove)
	mux.HandleFunc("/api/users.channelSections.create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		createdName = r.FormValue("name")
		store.mu.Lock()
		store.addSection("S99NEW", createdName, nil)
		store.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                 true,
			"channel_section_id": "S99NEW",
		})
	})

	out, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01", "--new-section", "Archive")
	if err != nil {
		t.Fatal(err)
	}

	if createdName != "Archive" {
		t.Errorf("expected section create with name='Archive', got %q", createdName)
	}
	// C01 was in S01ABC, so the move must remove it there and insert into the
	// freshly created section.
	if len(gotInsert) != 1 || gotInsert[0].SectionID != "S99NEW" {
		t.Errorf("expected insert into S99NEW, got %+v", gotInsert)
	}
	if len(gotRemove) != 1 || gotRemove[0].SectionID != "S01ABC" {
		t.Errorf("expected remove from S01ABC, got %+v", gotRemove)
	}
	if store.sectionOf("C01") != "S99NEW" {
		t.Errorf("C01 should be in S99NEW, got %q", store.sectionOf("C01"))
	}

	result := parseJSON(t, nonEmptyLines(out)[0])
	if result["moved_count"] != float64(1) {
		t.Errorf("expected moved_count=1, got %v", result["moved_count"])
	}
	if result["target_section_id"] != "S99NEW" {
		t.Errorf("expected target_section_id='S99NEW', got %q", result["target_section_id"])
	}
}

func TestSectionList_SessionTokenRequired(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMock(t, mux, "section", "list")
	if err == nil {
		t.Fatal("expected error for non-session token")
	}
}
