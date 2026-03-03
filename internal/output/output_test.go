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

func TestPrintJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	p := &Printer{Out: &out, Err: &errOut}

	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	err := p.Print(item{ID: "C123", Name: "general"})
	if err != nil {
		t.Fatal(err)
	}

	want := "{\n  \"id\": \"C123\",\n  \"name\": \"general\"\n}\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	if errOut.Len() != 0 {
		t.Errorf("unexpected stderr output: %s", errOut.String())
	}
}

func TestPrintJSONList(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	items := []map[string]string{
		{"id": "C1"},
		{"id": "C2"},
	}
	if err := p.Print(items); err != nil {
		t.Fatal(err)
	}

	var got []map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d items, want 2", len(got))
	}
}

func TestPrintJSONTimestampEnrichment(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{
		"ts":   "1705312200.123456",
		"text": "hello",
	}
	if err := p.Print(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["ts_iso"]; !ok {
		t.Error("missing ts_iso field")
	}
	iso, ok := got["ts_iso"].(string)
	if !ok {
		t.Fatal("ts_iso is not a string")
	}
	if iso != "2024-01-15T09:50:00Z" {
		t.Errorf("got ts_iso=%q, want %q", iso, "2024-01-15T09:50:00Z")
	}
}

func TestPrintJSONTimestampEnrichmentNested(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{
		"ts": "1705312200.123456",
		"latest_reply": map[string]any{
			"ts": "1705312260.654321",
		},
	}
	if err := p.Print(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Top-level ts_iso
	if _, ok := got["ts_iso"]; !ok {
		t.Error("missing top-level ts_iso")
	}

	// Nested ts_iso
	nested, ok := got["latest_reply"].(map[string]any)
	if !ok {
		t.Fatal("latest_reply is not a map")
	}
	if _, ok := nested["ts_iso"]; !ok {
		t.Error("missing nested ts_iso")
	}
}

func TestPrintJSONTimestampEnrichmentInList(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msgs := []map[string]any{
		{"ts": "1705312200.123456", "text": "first"},
		{"ts": "1705312260.654321", "text": "second"},
	}
	if err := p.Print(msgs); err != nil {
		t.Fatal(err)
	}

	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for i, item := range got {
		if _, ok := item["ts_iso"]; !ok {
			t.Errorf("item %d missing ts_iso", i)
		}
	}
}

func TestPrintJSONTimestampEnrichmentSuffixedKeys(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{
		"thread_ts":      "1705312200.123456",
		"latest_reply_ts": "1705312260.654321",
	}
	if err := p.Print(msg); err != nil {
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

func TestPrintJSONNoEnrichmentForNonTimestamps(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	msg := map[string]any{
		"ts":     "1705312200.123456",
		"events": "not-a-ts-field",
	}
	if err := p.Print(msg); err != nil {
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

func TestPrintJSONStructWithTimestamp(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	type message struct {
		Ts   string `json:"ts"`
		Text string `json:"text"`
	}
	if err := p.Print(message{Ts: "1705312200.123456", Text: "hello"}); err != nil {
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
	if got["detail"] != "No channel matching '#nonexistent'" {
		t.Errorf("wrong detail: %q", got["detail"])
	}
	if got["hint"] != "Run 'slack channel list' to see available channels" {
		t.Errorf("wrong hint: %q", got["hint"])
	}
	// Code should not appear in JSON
	if _, ok := got["code"]; ok {
		t.Error("code field should not be in JSON output")
	}
}

func TestPrintErrorOmitsEmpty(t *testing.T) {
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

func TestQuietSuppressesPrint(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Quiet: true}

	if err := p.Print(map[string]string{"id": "C123"}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("quiet mode should suppress Print output, got: %s", out.String())
	}
}

func TestQuietSuppressesPrintRaw(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Quiet: true}

	if err := p.PrintRaw(json.RawMessage(`{"id":"C123"}`)); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("quiet mode should suppress PrintRaw output, got: %s", out.String())
	}
}

func TestQuietDoesNotSuppressPrintError(t *testing.T) {
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

func TestPrintRaw(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	raw := json.RawMessage(`{"ok":true,"channels":[]}`)
	if err := p.PrintRaw(raw); err != nil {
		t.Fatal(err)
	}
	if out.String() != `{"ok":true,"channels":[]}`+"\n" {
		t.Errorf("got %q, want raw JSON with trailing newline", out.String())
	}
}
