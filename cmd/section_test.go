package cmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
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
	emojis   map[string]string   // section id -> emoji
	channels map[string][]string // section id -> channel ids
	setCalls []map[string][]string
	// setHook runs after a successful applySet, under the lock, so a test can
	// corrupt the stored state and simulate an ok:true that didn't take.
	setHook func()
	// invalidEmoji, when non-empty, is rejected with emoji_invalid by both
	// create and set, modelling Slack's server-side validation (verified live
	// 2026-10-16: unknown names, colon-wrapped names, wrong case, and unicode
	// are all rejected and the section is left unchanged).
	invalidEmoji string
}

func newSectionStore() *sectionStore {
	return &sectionStore{names: map[string]string{}, emojis: map[string]string{}, channels: map[string][]string{}}
}

func (s *sectionStore) addSection(id, name string, chans []string) {
	s.order = append(s.order, id)
	s.names[id] = name
	s.channels[id] = append([]string{}, chans...)
}

func (s *sectionStore) addSectionWithEmoji(id, name, emoji string, chans []string) {
	s.addSection(id, name, chans)
	s.emojis[id] = emoji
}

// applySet models users.channelSections.set as observed live 2026-10-16: a
// field whose key is OMITTED from the form is cleared, present-but-empty is
// stored as empty, a name another section holds is already_exists (no
// mutation), an unknown id is section_not_found. The write path accepts
// whatever the handler is told to store, so tests can also force a
// "Slack said ok but did nothing" outcome through setHook.
func (s *sectionStore) applySet(form map[string][]string) string {
	id := ""
	if v, ok := form["channel_section_id"]; ok && len(v) > 0 {
		id = v[0]
	}
	if _, ok := s.names[id]; !ok {
		return "section_not_found"
	}
	name, emoji := "", ""
	if v, ok := form["name"]; ok && len(v) > 0 {
		name = v[0]
	}
	if v, ok := form["emoji"]; ok && len(v) > 0 {
		emoji = v[0]
	}
	if s.invalidEmoji != "" && emoji == s.invalidEmoji {
		return "emoji_invalid"
	}
	// Case-insensitive, and a section's own name never collides (both
	// verified live 2026-10-16).
	for _, sid := range s.order {
		if sid != id && strings.EqualFold(s.names[sid], name) {
			return "already_exists"
		}
	}
	s.names[id] = name
	s.emojis[id] = emoji
	return ""
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
				"emoji":              s.emojis[sid],
				"type":               "channels",
				"channel_ids_page":   map[string]any{"channel_ids": s.channels[sid]},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel_sections": secs})
	})
	mux.HandleFunc("/api/users.channelSections.set", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.setCalls = append(s.setCalls, r.PostForm)
		if code := s.applySet(r.PostForm); code != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
			return
		}
		if s.setHook != nil {
			s.setHook()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
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

// createHandler models users.channelSections.create: it records the form,
// stores a new section under id with the given name and emoji, and answers
// emoji_invalid (no mutation) when the emoji matches invalidEmoji. Register
// it on the store's mux for tests that create sections.
func (s *sectionStore) createHandler(id string, gotForm *map[string][]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		defer s.mu.Unlock()
		*gotForm = r.PostForm
		emoji := r.PostForm.Get("emoji")
		if s.invalidEmoji != "" && emoji == s.invalidEmoji {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "emoji_invalid"})
			return
		}
		s.addSectionWithEmoji(id, r.PostForm.Get("name"), emoji, nil)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel_section_id": id})
	}
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
	var gotEmoji []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotName = r.FormValue("name")
		gotEmoji = r.PostForm["emoji"]
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
	// Slack answers invalid_arguments when the emoji key is absent (verified
	// live 2026-10-16), so the key must be PRESENT, even if empty. FormValue
	// cannot tell absent from empty - check the form map directly.
	if len(gotEmoji) != 1 || gotEmoji[0] != "" {
		t.Errorf("expected exactly one empty emoji param in request, got %q", gotEmoji)
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
	// The row always carries emoji, "" when the flag is absent.
	if v, ok := item["emoji"]; !ok || v != "" {
		t.Errorf("expected emoji=\"\" in row, got %v (present=%v)", v, ok)
	}
}

func TestSectionCreate_Emoji(t *testing.T) {
	store := newSectionStore()
	var gotInsert, gotRemove []bulkOp
	var form map[string][]string
	mux := store.mux(&gotInsert, &gotRemove)
	mux.HandleFunc("/api/users.channelSections.create", store.createHandler("S04JKL", &form))

	out, err := runWithMockSession(t, mux, "section", "create", "Archive", "--emoji", "tada")
	if err != nil {
		t.Fatal(err)
	}
	if got := form["emoji"]; len(got) != 1 || got[0] != "tada" {
		t.Errorf("expected emoji=tada sent, got %q", got)
	}
	if got := form["name"]; len(got) != 1 || got[0] != "Archive" {
		t.Errorf("expected name=Archive sent, got %q", got)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
	item := parseJSON(t, lines[0])
	want := map[string]any{"id": "S04JKL", "name": "Archive", "emoji": "tada"}
	for k, v := range want {
		if item[k] != v {
			t.Errorf("row[%s] = %v, want %v", k, item[k], v)
		}
	}
}

// Slack rejects :name: (verified live 2026-10-16); one wrapping pair of
// colons is stripped before sending, and the row shows the stripped value.
func TestSectionCreate_EmojiColonsStripped(t *testing.T) {
	store := newSectionStore()
	var gotInsert, gotRemove []bulkOp
	var form map[string][]string
	mux := store.mux(&gotInsert, &gotRemove)
	mux.HandleFunc("/api/users.channelSections.create", store.createHandler("S04JKL", &form))

	out, err := runWithMockSession(t, mux, "section", "create", "Archive", "--emoji", ":tada:")
	if err != nil {
		t.Fatal(err)
	}
	if got := form["emoji"]; len(got) != 1 || got[0] != "tada" {
		t.Errorf("expected emoji=tada sent, got %q", got)
	}
	if item := parseJSON(t, nonEmptyLines(out)[0]); item["emoji"] != "tada" {
		t.Errorf("expected emoji=tada in row, got %v", item["emoji"])
	}
}

// Slack's own emoji_invalid passes through as the error, exit 1, no row.
func TestSectionCreate_EmojiInvalid(t *testing.T) {
	store := newSectionStore()
	store.invalidEmoji = "notanemoji"
	var gotInsert, gotRemove []bulkOp
	var form map[string][]string
	mux := store.mux(&gotInsert, &gotRemove)
	mux.HandleFunc("/api/users.channelSections.create", store.createHandler("S04JKL", &form))

	out, err := runWithMockSession(t, mux, "section", "create", "Archive", "--emoji", "notanemoji")
	assertOutputErrorCode(t, err, "emoji_invalid")
	if out != "" {
		t.Errorf("expected no stdout, got:\n%s", out)
	}
	if len(store.order) != 0 {
		t.Errorf("no section should have been created, got %v", store.order)
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
	var createdEmoji []string
	var gotInsert, gotRemove []bulkOp
	mux := store.mux(&gotInsert, &gotRemove)
	mux.HandleFunc("/api/users.channelSections.create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		createdName = r.FormValue("name")
		createdEmoji = r.PostForm["emoji"]
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
	if len(createdEmoji) != 1 || createdEmoji[0] != "" {
		t.Errorf("expected exactly one empty emoji param on create, got %q", createdEmoji)
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

func TestSectionMove_NewSectionEmoji(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{"tada", "tada"},
		{":tada:", "tada"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			store := newSectionStore()
			store.addSection("S01ABC", "Customers", []string{"C01"})
			var gotInsert, gotRemove []bulkOp
			var form map[string][]string
			mux := store.mux(&gotInsert, &gotRemove)
			mux.HandleFunc("/api/users.channelSections.create", store.createHandler("S99NEW", &form))

			out, err := runWithMockSession(t, mux, "section", "move", "--channels", "C01", "--new-section", "Archive", "--emoji", tc.flag)
			if err != nil {
				t.Fatal(err)
			}
			if got := form["emoji"]; len(got) != 1 || got[0] != tc.want {
				t.Errorf("expected emoji=%q on create, got %q", tc.want, got)
			}
			if store.emojis["S99NEW"] != tc.want {
				t.Errorf("new section emoji = %q, want %q", store.emojis["S99NEW"], tc.want)
			}
			if store.sectionOf("C01") != "S99NEW" {
				t.Errorf("C01 should be in S99NEW, got %q", store.sectionOf("C01"))
			}
			result := parseJSON(t, nonEmptyLines(out)[0])
			if result["moved_count"] != float64(1) || result["target_section_id"] != "S99NEW" {
				t.Errorf("unexpected result row: %v", result)
			}
		})
	}
}

// --emoji belongs to the section being created. With --section it is
// invalid_input before any API call - including --emoji "", which a plain
// string flag could not tell from an absent flag.
func TestSectionMove_EmojiWithoutNewSection(t *testing.T) {
	for _, emoji := range []string{"tada", ""} {
		t.Run(fmt.Sprintf("%q", emoji), func(t *testing.T) {
			store := newSectionStore()
			store.addSection("S01ABC", "Customers", []string{"C01"})
			var calls atomic.Int32
			var gotInsert, gotRemove []bulkOp
			mux := store.mux(&gotInsert, &gotRemove)
			counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				mux.ServeHTTP(w, r)
			})
			out, err := runWithMockSession(t, counting, "section", "move", "--channels", "C01", "--section", "S01ABC", "--emoji", emoji)
			assertOutputErrorCode(t, err, "invalid_input")
			if n := calls.Load(); n != 0 {
				t.Errorf("expected zero API calls, got %d", n)
			}
			if out != "" {
				t.Errorf("expected no stdout, got:\n%s", out)
			}
		})
	}
}

func TestSectionList_SessionTokenRequired(t *testing.T) {
	mux := http.NewServeMux()
	_, err := runWithMock(t, mux, "section", "list")
	if err == nil {
		t.Fatal("expected error for non-session token")
	}
}

// --- section rename ---

func TestSectionList_EmitsEmoji(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Customers", "tada", []string{"C01"})
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "list")
	if err != nil {
		t.Fatal(err)
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	if row["emoji"] != "tada" {
		t.Errorf("expected emoji='tada', got %v", row["emoji"])
	}
}

func TestSectionRename(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "dog", []string{"C01", "C02"})
	store.addSection("S02DEF", "Archive", nil)
	var gotInsert, gotRemove []bulkOp

	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S01ABC", "Westies Customers")
	if err != nil {
		t.Fatal(err)
	}

	// Omitted keys clear fields on the real endpoint, so the current emoji
	// must ride along with the new name.
	if len(store.setCalls) != 1 {
		t.Fatalf("expected one set call, got %d", len(store.setCalls))
	}
	form := store.setCalls[0]
	if got := form["channel_section_id"]; len(got) != 1 || got[0] != "S01ABC" {
		t.Errorf("expected channel_section_id=S01ABC, got %q", got)
	}
	if got := form["name"]; len(got) != 1 || got[0] != "Westies Customers" {
		t.Errorf("expected name='Westies Customers', got %q", got)
	}
	if got := form["emoji"]; len(got) != 1 || got[0] != "dog" {
		t.Errorf("expected emoji='dog' resent, got %q", got)
	}
	if store.names["S01ABC"] != "Westies Customers" || store.emojis["S01ABC"] != "dog" {
		t.Errorf("store should hold renamed section with emoji, got name=%q emoji=%q", store.names["S01ABC"], store.emojis["S01ABC"])
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (row + meta), got %d:\n%s", len(lines), out)
	}
	row := parseJSON(t, lines[0])
	want := map[string]any{"id": "S01ABC", "name": "Westies Customers", "emoji": "dog", "type": "channels", "channel_count": float64(2)}
	for k, v := range want {
		if row[k] != v {
			t.Errorf("row[%s] = %v, want %v", k, row[k], v)
		}
	}
	if !strings.Contains(lines[1], "_meta") {
		t.Errorf("expected _meta trailer, got %s", lines[1])
	}
}

func TestSectionRename_SameName(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "", []string{"C01"})
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S01ABC", "Westies")
	if err != nil {
		t.Fatal(err)
	}
	// State is identical before and after, so the row alone proves nothing -
	// the write must actually have gone out.
	if len(store.setCalls) != 1 {
		t.Fatalf("expected one set call, got %d", len(store.setCalls))
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	if row["name"] != "Westies" {
		t.Errorf("expected name='Westies', got %v", row["name"])
	}
}

// Whitespace-only names are rejected locally. Slack stores an empty name as
// "" and says ok (verified live 2026-10-16), so this must never reach the API.
func TestSectionRename_BlankName(t *testing.T) {
	for _, name := range []string{"", "  ", "\t\n"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			store := newSectionStore()
			store.addSection("S01ABC", "Westies", nil)
			var calls atomic.Int32
			var gotInsert, gotRemove []bulkOp
			mux := store.mux(&gotInsert, &gotRemove)
			counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				mux.ServeHTTP(w, r)
			})
			out, err := runWithMockSession(t, counting, "section", "rename", "S01ABC", name)
			assertOutputErrorCode(t, err, "invalid_input")
			if n := calls.Load(); n != 0 {
				t.Errorf("expected zero API calls, got %d", n)
			}
			if out != "" {
				t.Errorf("expected no stdout, got:\n%s", out)
			}
		})
	}
}

func TestSectionRename_SectionNotFound(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Westies", nil)
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S99NOPE", "New")
	assertOutputErrorCode(t, err, "section_not_found")
	if out != "" {
		t.Errorf("expected no stdout, got:\n%s", out)
	}
	if len(store.setCalls) != 0 {
		t.Errorf("set must not be called for an unknown id, got %d calls", len(store.setCalls))
	}
}

func TestSectionRename_AlreadyExists(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Westies", nil)
	store.addSection("S02DEF", "Archive", nil)
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S01ABC", "Archive")
	assertOutputErrorCode(t, err, "already_exists")
	if out != "" {
		t.Errorf("expected no stdout, got:\n%s", out)
	}
	if store.names["S01ABC"] != "Westies" {
		t.Errorf("name must be unchanged after already_exists, got %q", store.names["S01ABC"])
	}
}

// Slack's duplicate check is case-insensitive (verified live 2026-10-16).
func TestSectionRename_AlreadyExists_CaseInsensitive(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Westies", nil)
	store.addSection("S02DEF", "Archive", nil)
	var gotInsert, gotRemove []bulkOp
	_, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S01ABC", "ARCHIVE")
	assertOutputErrorCode(t, err, "already_exists")
}

// Slack stores surrounding whitespace verbatim (verified live 2026-10-16),
// so the name is sent untrimmed and verified by exact match.
func TestSectionRename_WhitespacePreserved(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Westies", nil)
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S01ABC", "  Westies 2 ")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.setCalls[0]["name"]; len(got) != 1 || got[0] != "  Westies 2 " {
		t.Errorf("expected untrimmed name sent, got %q", got)
	}
	if row := parseJSON(t, nonEmptyLines(out)[0]); row["name"] != "  Westies 2 " {
		t.Errorf("expected untrimmed name in row, got %v", row["name"])
	}
}

func TestSectionRename_SessionTokenRequired(t *testing.T) {
	_, err := runWithMock(t, http.NewServeMux(), "section", "rename", "S01ABC", "New")
	assertOutputErrorCode(t, err, "session_token_required")
}

// The endpoint returns a bare ok:true, so the re-fetch is the only proof.
// A rename that didn't take must be an error, not a success row.
func TestSectionRename_IgnoredWrite(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "dog", nil)
	store.setHook = func() { store.names["S01ABC"] = "Westies" }
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S01ABC", "New")
	assertOutputErrorCode(t, err, "rename_failed")
	if len(nonEmptyLines(out)) != 0 {
		t.Errorf("expected no stdout on rename_failed, got:\n%s", out)
	}
}

func TestSectionRename_EmojiWiped(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "dog", nil)
	store.setHook = func() { store.emojis["S01ABC"] = "" }
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S01ABC", "New")
	assertOutputErrorCode(t, err, "rename_failed")
	if out != "" {
		t.Errorf("expected no stdout, got:\n%s", out)
	}
}

func TestSectionRename_SectionVanished(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Westies", nil)
	store.setHook = func() { delete(store.names, "S01ABC"); store.order = nil }
	var gotInsert, gotRemove []bulkOp
	_, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "rename", "S01ABC", "New")
	assertOutputErrorCode(t, err, "rename_failed")
}

// --- section emoji ---

func TestSectionEmoji(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "dog", []string{"C01", "C02"})
	store.addSection("S02DEF", "Archive", nil)
	var gotInsert, gotRemove []bulkOp

	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S01ABC", "heart")
	if err != nil {
		t.Fatal(err)
	}
	if len(store.setCalls) != 1 {
		t.Fatalf("expected one set call, got %d", len(store.setCalls))
	}
	form := store.setCalls[0]
	if got := form["channel_section_id"]; len(got) != 1 || got[0] != "S01ABC" {
		t.Errorf("expected channel_section_id=S01ABC, got %q", got)
	}
	if got := form["emoji"]; len(got) != 1 || got[0] != "heart" {
		t.Errorf("expected emoji=heart, got %q", got)
	}
	if store.names["S01ABC"] != "Westies" || store.emojis["S01ABC"] != "heart" {
		t.Errorf("store should hold name=Westies emoji=heart, got name=%q emoji=%q", store.names["S01ABC"], store.emojis["S01ABC"])
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (row + meta), got %d:\n%s", len(lines), out)
	}
	row := parseJSON(t, lines[0])
	want := map[string]any{"id": "S01ABC", "name": "Westies", "emoji": "heart", "type": "channels", "channel_count": float64(2)}
	for k, v := range want {
		if row[k] != v {
			t.Errorf("row[%s] = %v, want %v", k, row[k], v)
		}
	}
	if !strings.Contains(lines[1], "_meta") {
		t.Errorf("expected _meta trailer, got %s", lines[1])
	}
}

// Omitted keys clear fields on the real endpoint, so the current name must
// ride along with the new emoji.
func TestSectionEmoji_NamePreserved(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "  Westies 2 ", "dog", nil)
	var gotInsert, gotRemove []bulkOp
	_, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S01ABC", "heart")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.setCalls[0]["name"]; len(got) != 1 || got[0] != "  Westies 2 " {
		t.Errorf("expected current name resent verbatim, got %q", got)
	}
	if store.names["S01ABC"] != "  Westies 2 " {
		t.Errorf("name must be unchanged, got %q", store.names["S01ABC"])
	}
}

// An empty argument clears the emoji: the key must still be PRESENT (empty)
// in the form, since an omitted key would also clear it by accident and the
// create endpoint rejects a missing key outright.
func TestSectionEmoji_Clear(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "dog", nil)
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S01ABC", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.setCalls[0]["emoji"]; len(got) != 1 || got[0] != "" {
		t.Errorf("expected exactly one empty emoji param, got %q", got)
	}
	if store.emojis["S01ABC"] != "" {
		t.Errorf("emoji should be cleared, got %q", store.emojis["S01ABC"])
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	if v, ok := row["emoji"]; !ok || v != "" {
		t.Errorf("expected emoji=\"\" in row, got %v (present=%v)", v, ok)
	}
}

// Exactly one wrapping pair of colons is stripped when something sits
// between them. ":" and "::" go verbatim so a malformed value gets Slack's
// emoji_invalid rather than silently clearing the emoji. Everything else
// (case, whitespace, unicode) is untouched.
func TestSectionEmoji_ColonsStripped(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{":heart:", "heart"},
		{":wave::skin-tone-2:", "wave::skin-tone-2"},
		{"wave::skin-tone-2", "wave::skin-tone-2"},
		{":", ":"},
		{"::", "::"},
		{":::", ":"},
		{":heart", ":heart"},
		{"heart:", "heart:"},
		{"Heart", "Heart"},
		{"heart ", "heart "},
		{": heart :", " heart "},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			store := newSectionStore()
			store.addSectionWithEmoji("S01ABC", "Westies", "dog", nil)
			var gotInsert, gotRemove []bulkOp
			out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S01ABC", tc.arg)
			if err != nil {
				t.Fatal(err)
			}
			if got := store.setCalls[0]["emoji"]; len(got) != 1 || got[0] != tc.want {
				t.Errorf("expected emoji=%q sent, got %q", tc.want, got)
			}
			if row := parseJSON(t, nonEmptyLines(out)[0]); row["emoji"] != tc.want {
				t.Errorf("expected emoji=%q in row, got %v", tc.want, row["emoji"])
			}
		})
	}
}

func TestSectionEmoji_SectionNotFound(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Westies", nil)
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S99NOPE", "heart")
	assertOutputErrorCode(t, err, "section_not_found")
	if out != "" {
		t.Errorf("expected no stdout, got:\n%s", out)
	}
	if len(store.setCalls) != 0 {
		t.Errorf("set must not be called for an unknown id, got %d calls", len(store.setCalls))
	}
}

func TestSectionEmoji_SessionTokenRequired(t *testing.T) {
	_, err := runWithMock(t, http.NewServeMux(), "section", "emoji", "S01ABC", "heart")
	assertOutputErrorCode(t, err, "session_token_required")
}

// Slack's own emoji_invalid passes through unchanged, exit 1, no row, and
// the section is untouched.
func TestSectionEmoji_EmojiInvalid(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "dog", nil)
	store.invalidEmoji = "notanemoji"
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S01ABC", "notanemoji")
	assertOutputErrorCode(t, err, "emoji_invalid")
	if out != "" {
		t.Errorf("expected no stdout, got:\n%s", out)
	}
	if store.emojis["S01ABC"] != "dog" {
		t.Errorf("emoji must be unchanged after emoji_invalid, got %q", store.emojis["S01ABC"])
	}
}

// The endpoint returns a bare ok:true, so the re-fetch is the only proof.
// A write that didn't take must be an error, not a success row.
func TestSectionEmoji_IgnoredWrite(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "dog", nil)
	store.setHook = func() { store.emojis["S01ABC"] = "dog" }
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S01ABC", "heart")
	assertOutputErrorCode(t, err, "emoji_failed")
	if out != "" {
		t.Errorf("expected no stdout on emoji_failed, got:\n%s", out)
	}
}

// The emoji landed but the name did not survive: still emoji_failed, since
// the postcondition is the whole requested state.
func TestSectionEmoji_NameChangedAfterWrite(t *testing.T) {
	store := newSectionStore()
	store.addSectionWithEmoji("S01ABC", "Westies", "dog", nil)
	store.setHook = func() { store.names["S01ABC"] = "Other" }
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S01ABC", "heart")
	assertOutputErrorCode(t, err, "emoji_failed")
	if out != "" {
		t.Errorf("expected no stdout, got:\n%s", out)
	}
}

func TestSectionEmoji_SectionVanished(t *testing.T) {
	store := newSectionStore()
	store.addSection("S01ABC", "Westies", nil)
	store.setHook = func() { delete(store.names, "S01ABC"); store.order = nil }
	var gotInsert, gotRemove []bulkOp
	out, err := runWithMockSession(t, store.mux(&gotInsert, &gotRemove), "section", "emoji", "S01ABC", "heart")
	assertOutputErrorCode(t, err, "emoji_failed")
	if out != "" {
		t.Errorf("expected no stdout, got:\n%s", out)
	}
}

// assertOutputErrorCode requires a fatal *output.Error with the given code.
func assertOutputErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var oe *output.Error
	if !errors.As(err, &oe) {
		t.Fatalf("expected *output.Error %q, got %T: %v", code, err, err)
	}
	if oe.Err != code {
		t.Fatalf("expected error code %q, got %q (%s)", code, oe.Err, oe.Detail)
	}
	if oe.Code == 0 {
		t.Fatalf("expected a nonzero exit code on %q", code)
	}
}
