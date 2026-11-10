package output

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// testSources mirrors the shape cmd wires in from the resolver's enrichment
// tables: derived name -> the source keys it is built from.
var testSources = map[string][]string{
	"user_name":      {"user", "user_id"},
	"from_user_name": {"from_user"},
	"channel_name":   {"channel", "channel_id"},
}

// testEnrich stands in for resolve.Enrich: it names a user or channel id and
// skips a name already present. lookups counts how many ids it resolved.
func testEnrich(lookups *int) func(map[string]any) {
	names := map[string]string{"U01XYZ": "alice", "U02MGR": "bob", "C01ABC": "general"}
	return func(m map[string]any) {
		for name, sources := range testSources {
			if _, already := m[name]; already {
				continue
			}
			for _, src := range sources {
				if id, ok := m[src].(string); ok {
					if n, found := names[id]; found {
						if lookups != nil {
							*lookups++
						}
						m[name] = n
						break
					}
				}
			}
		}
	}
}

func printRow(t *testing.T, fields []string, row map[string]any, lookups *int) map[string]any {
	t.Helper()
	var out bytes.Buffer
	p := &Printer{
		Out:           &out,
		Err:           &bytes.Buffer{},
		Fields:        fields,
		EnrichFunc:    testEnrich(lookups),
		EnrichSources: testSources,
	}
	if err := p.PrintItem(row); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), err)
	}
	return got
}

func messageRow() map[string]any {
	return map[string]any{
		"user":      "U01XYZ",
		"channel":   "C01ABC",
		"text":      "hello",
		"ts":        "1709251200.000100",
		"thread_ts": "1709251200.000100",
	}
}

// TestFields_NestedBehaviorPinned pins what --fields does below the top level.
// It passed before derived names became requestable and must keep passing: a
// selected parent comes back whole, timestamps inside it gain _iso siblings,
// user and channel ids inside it are not named (Enrich is top-level only), and
// a nested key or dotted path named in --fields selects nothing.
func TestFields_NestedBehaviorPinned(t *testing.T) {
	row := func() map[string]any {
		return map[string]any{
			"id": "F01ABC",
			"latest_reply": map[string]any{
				"ts":   "1709251200.000100",
				"user": "U01XYZ",
			},
			"replies": []any{
				map[string]any{"thread_ts": "1709251200.000100"},
			},
		}
	}

	t.Run("parent selected", func(t *testing.T) {
		got := printRow(t, []string{"latest_reply", "replies"}, row(), nil)
		want := map[string]any{
			"latest_reply": map[string]any{
				"ts":     "1709251200.000100",
				"ts_iso": "2024-03-01T00:00:00Z",
				"user":   "U01XYZ",
			},
			"replies": []any{
				map[string]any{
					"thread_ts":     "1709251200.000100",
					"thread_ts_iso": "2024-03-01T00:00:00Z",
				},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	for _, name := range []string{"ts", "ts_iso", "user", "user_name", "thread_ts_iso", "latest_reply.ts", "latest_reply.ts_iso"} {
		t.Run("nested name "+name, func(t *testing.T) {
			got := printRow(t, []string{name}, row(), nil)
			if len(got) != 0 {
				t.Errorf("--fields %s selected %v from a row that has it only nested", name, got)
			}
		})
	}
}

func TestFields_DerivedNames(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
		row    map[string]any
		want   map[string]any
	}{
		{
			name:   "user_name without user",
			fields: []string{"user_name"},
			row:    messageRow(),
			want:   map[string]any{"user_name": "alice"},
		},
		{
			name:   "ts_iso without ts",
			fields: []string{"ts_iso"},
			row:    messageRow(),
			want:   map[string]any{"ts_iso": "2024-03-01T00:00:00Z"},
		},
		{
			name:   "suffixed timestamp",
			fields: []string{"thread_ts_iso"},
			row:    messageRow(),
			want:   map[string]any{"thread_ts_iso": "2024-03-01T00:00:00Z"},
		},
		{
			name:   "channel_name without channel",
			fields: []string{"channel_name"},
			row:    messageRow(),
			want:   map[string]any{"channel_name": "general"},
		},
		{
			name:   "second source key",
			fields: []string{"user_name"},
			row:    map[string]any{"user_id": "U02MGR", "text": "x"},
			want:   map[string]any{"user_name": "bob"},
		},
		{
			name:   "derived names beside a plain field",
			fields: []string{"user_name", "ts_iso", "text"},
			row:    messageRow(),
			want:   map[string]any{"user_name": "alice", "ts_iso": "2024-03-01T00:00:00Z", "text": "hello"},
		},
		{
			name:   "a requested source keeps its derived sibling",
			fields: []string{"user"},
			row:    messageRow(),
			want:   map[string]any{"user": "U01XYZ", "user_name": "alice"},
		},
		{
			name:   "source and derived both requested",
			fields: []string{"ts", "ts_iso", "user", "user_name"},
			row:    messageRow(),
			want: map[string]any{
				"ts": "1709251200.000100", "ts_iso": "2024-03-01T00:00:00Z",
				"user": "U01XYZ", "user_name": "alice",
			},
		},
		{
			name:   "one derived name does not bring the other",
			fields: []string{"ts", "user_name"},
			row:    messageRow(),
			want: map[string]any{
				"ts": "1709251200.000100", "ts_iso": "2024-03-01T00:00:00Z",
				"user_name": "alice",
			},
		},
		{
			name:   "source key absent",
			fields: []string{"user_name", "ts_iso", "text"},
			row:    map[string]any{"text": "hello", "id": "F01ABC"},
			want:   map[string]any{"text": "hello"},
		},
		{
			name:   "source present but unresolvable",
			fields: []string{"user_name", "ts_iso"},
			row:    map[string]any{"user": "U09NOPE", "ts": "not-a-ts", "text": "x"},
			want:   map[string]any{},
		},
		{
			name:   "derived key the command set itself",
			fields: []string{"channel_name", "date_created_iso"},
			row: map[string]any{
				"channel_id": "C02DEF", "channel_name": "preset",
				"date_created": 1709251200, "date_created_iso": "2024-03-01T00:00:00Z",
			},
			want: map[string]any{"channel_name": "preset", "date_created_iso": "2024-03-01T00:00:00Z"},
		},
		{
			name:   "always-kept fields survive",
			fields: []string{"user_name"},
			row: map[string]any{
				"user": "U01XYZ", "text": "x",
				"input": "@alice", "error": "e", "enrich_error": "ee", "replaced_draft_id": "Dr01234",
			},
			want: map[string]any{
				"user_name": "alice",
				"input":     "@alice", "error": "e", "enrich_error": "ee", "replaced_draft_id": "Dr01234",
			},
		},
		{
			name:   "unknown name still selects nothing",
			fields: []string{"bogus", "channel.name"},
			row:    messageRow(),
			want:   map[string]any{},
		},
		{
			name:   "_iso on a non-timestamp key is not a derived name",
			fields: []string{"text_iso"},
			row:    messageRow(),
			want:   map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := printRow(t, tt.fields, tt.row, nil)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("--fields %v\n got %v\nwant %v", tt.fields, got, tt.want)
			}
		})
	}
}

// TestFields_UnrequestedSourcesAreNotEnriched pins the cost side: a source key
// reaches enrichment only when it or its derived name was requested, so
// --fields text spends no lookups and --fields user_name spends one, not one
// per id in the row.
func TestFields_UnrequestedSourcesAreNotEnriched(t *testing.T) {
	tests := []struct {
		fields []string
		want   int
	}{
		{[]string{"text"}, 0},
		{[]string{"ts_iso"}, 0},
		{[]string{"user_name"}, 1},
		{[]string{"user_name", "channel_name"}, 2},
	}
	for _, tt := range tests {
		lookups := 0
		printRow(t, tt.fields, messageRow(), &lookups)
		if lookups != tt.want {
			t.Errorf("--fields %v: %d lookups, want %d", tt.fields, lookups, tt.want)
		}
	}
}

// TestFields_UnrequestedSiblingOfABorrowedSource covers a source that feeds two
// derived names: asking for one must not emit the other.
func TestFields_UnrequestedSiblingOfABorrowedSource(t *testing.T) {
	sources := map[string][]string{"user_name": {"user"}, "user_handle": {"user"}}
	enrich := func(m map[string]any) {
		if _, ok := m["user"]; ok {
			m["user_name"] = "Alice Adams"
			m["user_handle"] = "alice"
		}
	}
	run := func(fields ...string) map[string]any {
		var out bytes.Buffer
		p := &Printer{Out: &out, Err: &bytes.Buffer{}, Fields: fields, EnrichFunc: enrich, EnrichSources: sources}
		if err := p.PrintItem(map[string]any{"user": "U01XYZ", "text": "x"}); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got, want := run("user_name"), map[string]any{"user_name": "Alice Adams"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// A requested source keeps every sibling.
	want := map[string]any{"user": "U01XYZ", "user_name": "Alice Adams", "user_handle": "alice"}
	if got := run("user", "user_name"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestFields_NoSourcesTableKeepsUntabledEnrichment covers a printer built
// without EnrichSources: timestamps still reverse, and a key EnrichFunc adds
// that no table explains is kept, as before.
func TestFields_NoSourcesTableKeepsUntabledEnrichment(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{
		Out:    &out,
		Err:    &bytes.Buffer{},
		Fields: []string{"ts_iso", "id"},
		EnrichFunc: func(m map[string]any) {
			m["extra"] = true
		},
	}
	if err := p.PrintItem(map[string]any{"id": "F01ABC", "ts": "1709251200.000100", "text": "x"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "F01ABC", "ts_iso": "2024-03-01T00:00:00Z", "extra": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
