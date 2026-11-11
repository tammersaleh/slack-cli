package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// infoMock answers conversations.info per channel id: a name, a Slack error
// code, or 429s (a fixed number, or forever). It counts calls per id.
type infoMock struct {
	mu         sync.Mutex
	names      map[string]string
	archived   map[string]bool
	errs       map[string]string
	limitFirst map[string]int
	limitAll   map[string]bool
	calls      map[string]int
}

func newInfoMock() *infoMock {
	return &infoMock{
		names:      map[string]string{},
		archived:   map[string]bool{},
		errs:       map[string]string{},
		limitFirst: map[string]int{},
		limitAll:   map[string]bool{},
		calls:      map[string]int{},
	}
}

func (m *infoMock) callCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[id]
}

func (m *infoMock) handler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := r.FormValue("channel")
	m.mu.Lock()
	m.calls[id]++
	n := m.calls[id]
	limited := m.limitAll[id] || n <= m.limitFirst[id]
	code := m.errs[id]
	name := m.names[id]
	archived := m.archived[id]
	m.mu.Unlock()

	if limited {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if code != "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"channel": map[string]any{"id": id, "name": name, "is_archived": archived},
	})
}

// sectionNamesMux serves two sections and the mock's conversations.info.
func sectionNamesMux(t *testing.T, m *infoMock) *http.ServeMux {
	t.Helper()
	sections := []map[string]any{
		{
			"channel_section_id": "S01ABC",
			"name":               "Customers",
			"type":               "channels",
			"channel_ids_page":   map[string]any{"channel_ids": []string{"C01ABC", "C02DEF", "C03GHI"}},
		},
		{
			"channel_section_id": "S02DEF",
			"name":               "Internal",
			"type":               "channels",
			"channel_ids_page":   map[string]any{"channel_ids": []string{"C04JKL"}},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.channelSections.list", sectionListHandler(t, sections))
	mux.HandleFunc("/api/conversations.info", m.handler)
	return mux
}

func resolvingInfoMock() *infoMock {
	m := newInfoMock()
	m.names["C01ABC"] = "ext-acme"
	m.names["C02DEF"] = "ext-globex"
	m.names["C03GHI"] = "ext-initech"
	m.names["C04JKL"] = "internal-ops"
	m.archived["C03GHI"] = true
	return m
}

// A channel Slack reports as channel_not_found marks its own row: no name, no
// is_archived, an enrich_error, the other rows intact, exit 1.
func TestSectionChannels_ItemLocalMiss(t *testing.T) {
	m := resolvingInfoMock()
	m.errs["C02DEF"] = "channel_not_found"

	out, err := runWithMockSession(t, sectionNamesMux(t, m), "section", "channels", "S01ABC")
	assertExitError(t, err)

	rows, meta := rowsAndMeta(t, out)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3:\n%s", len(rows), out)
	}
	want := []map[string]any{
		{"id": "C01ABC", "name": "ext-acme", "is_archived": false},
		{"id": "C02DEF", "enrich_error": "channel_not_found"},
		{"id": "C03GHI", "name": "ext-initech", "is_archived": true},
	}
	for i, w := range want {
		assertExactRow(t, rows[i], w)
	}
	if meta["has_more"] != false || meta["error_count"] != float64(1) {
		t.Errorf("meta = %v, want has_more:false error_count:1", meta)
	}
	if _, ok := meta["error"]; ok {
		t.Errorf("a partial read is not a truncated one, meta = %v", meta)
	}
}

// --fields cannot strip the marker, and an unresolved row never gains a name.
func TestSectionChannels_ItemLocalMiss_Fields(t *testing.T) {
	m := resolvingInfoMock()
	m.errs["C02DEF"] = "channel_not_found"

	out, err := runWithMockSession(t, sectionNamesMux(t, m), "section", "channels", "S01ABC", "--fields", "id,name")
	assertExitError(t, err)

	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3:\n%s", len(rows), out)
	}
	assertExactRow(t, rows[0], map[string]any{"id": "C01ABC", "name": "ext-acme"})
	assertExactRow(t, rows[1], map[string]any{"id": "C02DEF", "enrich_error": "channel_not_found"})
}

// A conversation that resolves without a name (a DM) is a success: name is
// the empty string, no marker, exit 0.
func TestSectionChannels_NamelessConversation(t *testing.T) {
	m := resolvingInfoMock()
	m.names["C02DEF"] = ""

	out, err := runWithMockSession(t, sectionNamesMux(t, m), "section", "channels", "S01ABC")
	if err != nil {
		t.Fatalf("a nameless conversation is not a failure: %v", err)
	}
	rows, meta := rowsAndMeta(t, out)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3:\n%s", len(rows), out)
	}
	assertExactRow(t, rows[1], map[string]any{"id": "C02DEF", "name": "", "is_archived": false})
	if _, ok := meta["error_count"]; ok {
		t.Errorf("meta = %v, want no error_count", meta)
	}
}

// A rate limit that clears is retried: every row resolves, exit 0.
func TestSectionChannels_RateLimitClears(t *testing.T) {
	m := resolvingInfoMock()
	m.limitFirst["C02DEF"] = 1

	out, err := runWithMockSession(t, sectionNamesMux(t, m), "section", "channels", "S01ABC")
	if err != nil {
		t.Fatalf("a rate limit that clears must not fail the command: %v", err)
	}
	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3:\n%s", len(rows), out)
	}
	assertExactRow(t, rows[1], map[string]any{"id": "C02DEF", "name": "ext-globex", "is_archived": false})
	if got := m.callCount("C02DEF"); got != 2 {
		t.Errorf("conversations.info calls for the limited channel = %d, want 2", got)
	}
}

// A rate limit that never clears fails the whole command: no rows, a trailer
// naming rate_limited, exit 3.
func TestSectionChannels_RateLimitExhausted(t *testing.T) {
	m := resolvingInfoMock()
	m.limitAll["C02DEF"] = true

	out, err := runWithMockSession(t, sectionNamesMux(t, m), "section", "channels", "S01ABC")
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "rate_limited" || oErr.Code != output.ExitRateLimit {
		t.Fatalf("expected rate_limited exit %d, got %v", output.ExitRateLimit, err)
	}
	if oErr.Endpoint != "conversations.info" {
		t.Errorf("endpoint = %q, want conversations.info", oErr.Endpoint)
	}
	meta := assertFatalPage(t, out, "rate_limited", 0)
	if meta["has_more"] != true {
		t.Errorf("meta = %v, want has_more:true", meta)
	}
	if _, ok := meta["next_cursor"]; ok {
		t.Errorf("meta = %v, want no cursor", meta)
	}
}

// Fail closed: a code outside the item-local allowlist, including one Slack
// has not invented yet, fails the command instead of marking a row.
func TestSectionChannels_UnknownErrorFailsClosed(t *testing.T) {
	for _, code := range []string{"some_future_error", "enterprise_is_restricted", "invalid_auth", "not_in_channel"} {
		t.Run(code, func(t *testing.T) {
			m := resolvingInfoMock()
			m.errs["C02DEF"] = code

			out, err := runWithMockSession(t, sectionNamesMux(t, m), "section", "channels", "S01ABC")
			assertOutputErrorCode(t, err, code)
			meta := assertFatalPage(t, out, code, 0)
			if meta["has_more"] != true {
				t.Errorf("meta = %v, want has_more:true", meta)
			}
		})
	}
}

// find matches on the name, so a row whose name could not be read cannot be
// ruled out: it is emitted with the marker whatever the pattern, next to the
// real matches, exit 1.
func TestSectionFind_UnresolvedRow(t *testing.T) {
	m := resolvingInfoMock()
	m.errs["C04JKL"] = "channel_not_found"

	out, err := runWithMockSession(t, sectionNamesMux(t, m), "section", "find", "ext-a")
	assertExitError(t, err)

	rows, meta := rowsAndMeta(t, out)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2:\n%s", len(rows), out)
	}
	assertExactRow(t, rows[0], map[string]any{
		"id": "C01ABC", "name": "ext-acme", "is_archived": false,
		"section_id": "S01ABC", "section_name": "Customers",
	})
	assertExactRow(t, rows[1], map[string]any{
		"id": "C04JKL", "enrich_error": "channel_not_found",
		"section_id": "S02DEF", "section_name": "Internal",
	})
	if meta["has_more"] != false || meta["error_count"] != float64(1) {
		t.Errorf("meta = %v, want has_more:false error_count:1", meta)
	}
}

// find fails closed the same way channels does.
func TestSectionFind_UnknownErrorFailsClosed(t *testing.T) {
	m := resolvingInfoMock()
	m.errs["C04JKL"] = "some_future_error"

	out, err := runWithMockSession(t, sectionNamesMux(t, m), "section", "find", "ext-")
	assertOutputErrorCode(t, err, "some_future_error")
	assertFatalPage(t, out, "some_future_error", 0)
}

// assertExactRow requires the row to hold exactly these keys and values, so a
// leaked name or is_archived on an unresolved row fails.
func assertExactRow(t *testing.T, got, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("row = %v, want exactly %v", got, want)
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("row[%q] = %v, want %v (row %v)", k, got[k], v, got)
		}
	}
}
