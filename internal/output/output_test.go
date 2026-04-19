package output

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		expected int
	}{
		{"success", ExitSuccess, 0},
		{"general", ExitGeneral, 1},
		{"auth", ExitAuth, 2},
		{"rate limit", ExitRateLimit, 3},
		{"network", ExitNetwork, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code != tt.expected {
				t.Errorf("got %d, want %d", tt.code, tt.expected)
			}
		})
	}
}

func TestErrorInterface(t *testing.T) {
	err := &Error{Err: "channel_not_found", Detail: "No such channel"}
	if err.Error() != "channel_not_found" {
		t.Errorf("got %q, want %q", err.Error(), "channel_not_found")
	}
}

func TestPrintItem_CompactJSON(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := p.PrintItem(item{ID: "C123", Name: "general"}); err != nil {
		t.Fatal(err)
	}

	want := `{"id":"C123","name":"general"}` + "\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestPrintItem_TimestampEnrichment(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{
		"ts":   "1705312200.123456",
		"text": "hello",
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	iso, ok := got["ts_iso"].(string)
	if !ok {
		t.Fatal("missing ts_iso field")
	}
	if iso != "2024-01-15T09:50:00Z" {
		t.Errorf("got ts_iso=%q, want %q", iso, "2024-01-15T09:50:00Z")
	}
}

func TestPrintItem_TimestampEnrichmentNested(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{
		"ts": "1705312200.123456",
		"latest_reply": map[string]any{
			"ts": "1705312260.654321",
		},
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["ts_iso"]; !ok {
		t.Error("missing top-level ts_iso")
	}
	nested, ok := got["latest_reply"].(map[string]any)
	if !ok {
		t.Fatal("latest_reply is not a map")
	}
	if _, ok := nested["ts_iso"]; !ok {
		t.Error("missing nested ts_iso")
	}
}

func TestPrintItem_TimestampSuffixedKeys(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{
		"thread_ts":       "1705312200.123456",
		"latest_reply_ts": "1705312260.654321",
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["thread_ts_iso"]; !ok {
		t.Error("missing thread_ts_iso")
	}
	if _, ok := got["latest_reply_ts_iso"]; !ok {
		t.Error("missing latest_reply_ts_iso")
	}
}

func TestPrintItem_NoEnrichmentForNonTimestamps(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{
		"ts":     "1705312200.123456",
		"events": "not-a-ts-field",
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["ts_iso"]; !ok {
		t.Error("missing ts_iso for actual timestamp")
	}
	if _, ok := got["events_iso"]; ok {
		t.Error("should not enrich non-timestamp field 'events'")
	}
}

func TestPrintItem_StructWithTimestamp(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	type message struct {
		Ts   string `json:"ts"`
		Text string `json:"text"`
	}
	if err := p.PrintItem(message{Ts: "1705312200.123456", Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["ts_iso"]; !ok {
		t.Error("missing ts_iso on struct output")
	}
}

func TestPrintMeta(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	if err := p.PrintMeta(Meta{HasMore: true, NextCursor: "abc123"}); err != nil {
		t.Fatal(err)
	}

	want := `{"_meta":{"has_more":true,"next_cursor":"abc123"}}` + "\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestPrintMeta_NoMore(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	if err := p.PrintMeta(Meta{HasMore: false}); err != nil {
		t.Fatal(err)
	}

	want := `{"_meta":{"has_more":false}}` + "\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestPrintMeta_WithErrorCount(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	if err := p.PrintMeta(Meta{HasMore: false, ErrorCount: 2}); err != nil {
		t.Fatal(err)
	}

	want := `{"_meta":{"has_more":false,"error_count":2}}` + "\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestPrintError(t *testing.T) {
	var out, errOut bytes.Buffer
	p := &Printer{Out: &out, Err: &errOut}

	e := &Error{
		Err:    "channel_not_found",
		Detail: "No channel matching '#nonexistent'",
		Hint:   "Run 'slack channel list' to see available channels",
		Code:   ExitGeneral,
	}
	if err := p.PrintError(e); err != nil {
		t.Fatal(err)
	}

	if out.Len() != 0 {
		t.Errorf("unexpected stdout output: %s", out.String())
	}

	var got map[string]any
	if err := json.Unmarshal(errOut.Bytes(), &got); err != nil {
		t.Fatalf("invalid error JSON: %v", err)
	}
	if got["error"] != "channel_not_found" {
		t.Errorf("got error=%q, want %q", got["error"], "channel_not_found")
	}
	if _, ok := got["code"]; ok {
		t.Error("code field should not be in JSON output")
	}
}

func TestPrintError_OmitsEmpty(t *testing.T) {
	var errOut bytes.Buffer
	p := &Printer{Out: &bytes.Buffer{}, Err: &errOut}

	e := &Error{Err: "unknown_error"}
	if err := p.PrintError(e); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(errOut.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["detail"]; ok {
		t.Error("empty detail should be omitted")
	}
	if _, ok := got["hint"]; ok {
		t.Error("empty hint should be omitted")
	}
}

func TestQuiet_SuppressesPrintItem(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Quiet: true}

	if err := p.PrintItem(map[string]string{"id": "C123"}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("quiet mode should suppress PrintItem output, got: %s", out.String())
	}
}

func TestQuiet_SuppressesPrintMeta(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Quiet: true}

	if err := p.PrintMeta(Meta{HasMore: false}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("quiet mode should suppress PrintMeta output, got: %s", out.String())
	}
}

func TestQuiet_DoesNotSuppressPrintError(t *testing.T) {
	var errOut bytes.Buffer
	p := &Printer{Out: &bytes.Buffer{}, Err: &errOut, Quiet: true}

	e := &Error{Err: "something_broke"}
	if err := p.PrintError(e); err != nil {
		t.Fatal(err)
	}
	if errOut.Len() == 0 {
		t.Error("quiet mode should NOT suppress PrintError")
	}
}

func TestFieldFiltering(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Fields: []string{"id", "name"}}

	msg := map[string]any{
		"id":           "C01ABC",
		"name":         "general",
		"num_members":  142,
		"is_member":    true,
		"unread_count": 5,
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["id"]; !ok {
		t.Error("expected 'id' field")
	}
	if _, ok := got["name"]; !ok {
		t.Error("expected 'name' field")
	}
	if _, ok := got["num_members"]; ok {
		t.Error("'num_members' should be filtered out")
	}
}

func TestFieldFiltering_PreservesInput(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Fields: []string{"id"}}

	msg := map[string]any{
		"input": "#general",
		"id":    "C01ABC",
		"name":  "general",
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["input"]; !ok {
		t.Error("'input' field should always be preserved")
	}
	if _, ok := got["id"]; !ok {
		t.Error("expected 'id' field")
	}
	if _, ok := got["name"]; ok {
		t.Error("'name' should be filtered out")
	}
}

func TestFieldFiltering_EmptyMeansAll(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{"id": "C01ABC", "name": "general"}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 fields, got %d", len(got))
	}
}

// TestFieldFiltering_PreservesEnrichment verifies that --fields runs before
// enrichment, so asking for `ts` still yields `ts_iso` in output. Same for
// user/user_name and channel/channel_name via EnrichFunc.
func TestFieldFiltering_PreservesTimestampEnrichment(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Fields: []string{"ts"}}

	msg := map[string]any{
		"ts":   "1705312200.123456",
		"text": "hello",
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["ts"]; !ok {
		t.Error("expected 'ts' field")
	}
	if _, ok := got["ts_iso"]; !ok {
		t.Error("expected 'ts_iso' field to survive field filtering")
	}
	if _, ok := got["text"]; ok {
		t.Error("'text' should be filtered out")
	}
}

func TestFieldFiltering_PreservesEnrichFunc(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{
		Out:    &out,
		Err:    &bytes.Buffer{},
		Fields: []string{"user"},
		EnrichFunc: func(m map[string]any) {
			if _, ok := m["user"]; ok {
				m["user_name"] = "tammer"
			}
		},
	}

	msg := map[string]any{
		"user": "U01",
		"text": "hello",
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["user"] != "U01" {
		t.Errorf("expected user='U01', got %q", got["user"])
	}
	if got["user_name"] != "tammer" {
		t.Errorf("expected user_name='tammer' added by enrichment, got %q", got["user_name"])
	}
	if _, ok := got["text"]; ok {
		t.Error("'text' should be filtered out")
	}
}
