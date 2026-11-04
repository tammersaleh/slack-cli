package cmd

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	m, err := decodeJSONMap([]byte(s))
	if err != nil {
		t.Fatalf("bad test JSON %q: %v", s, err)
	}
	return m
}

func col(id, name, typ string) listColumn {
	return listColumn{ID: id, Name: name, Key: "key_" + id, Type: typ}
}

func TestColumnKeys_DedupesByProbing(t *testing.T) {
	schema := []listColumn{
		col("Col01AAA", "Foo", "text"),
		col("Col01BBB", "Foo_2", "text"),
		col("Col01CCC", "Foo", "text"),
		col("Col01DDD", "Bar", "text"),
		col("Col01EEE", "Foo", "text"),
		col("Col01FFF", "", "text"),
	}
	got := columnKeys(schema)
	// The third Foo cannot take Foo_2 (a real column owns it), so it probes on
	// to Foo_3; the fourth Foo lands on Foo_4. A nameless column falls back to
	// its id so the row still carries every column.
	want := []string{"Foo", "Foo_2", "Foo_3", "Bar", "Foo_4", "Col01FFF"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("columnKeys = %v, want %v", got, want)
	}

	// Assignment is in schema order with no reservation pass: a literal
	// "Foo_2" that comes after the duplicate loses its name. Pinned so SPEC
	// and the code say the same thing.
	later := []listColumn{col("a", "Foo", "text"), col("b", "Foo", "text"), col("c", "Foo_2", "text")}
	if got, want := columnKeys(later), []string{"Foo", "Foo_2", "Foo_2_2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("columnKeys(later Foo_2) = %v, want %v", got, want)
	}
}

func TestRenderFieldValue(t *testing.T) {
	statusCol := listColumn{ID: "Col01CCC", Name: "Status", Key: "Col01BBB", Type: "select", Options: &listColumnOptions{
		Format: "single_select",
		Choices: []listChoice{
			{Value: "todo", Label: "To do", Color: "gray"},
			{Value: "done", Label: "Done", Color: "green"},
		},
	}}

	tests := []struct {
		name  string
		col   listColumn
		field string
		want  string // JSON
	}{
		{"text uses rendered text", col("c", "Name", "text"),
			`{"key":"name","column_id":"c","value":"[{\"type\":\"rich_text\"}]","text":"Ship it","rich_text":[{"type":"rich_text"}]}`,
			`"Ship it"`},
		{"rich_text uses rendered text", col("c", "Notes", "rich_text"),
			`{"key":"k","column_id":"c","value":"x","text":"• one\n• two","rich_text":[]}`,
			`"• one\n• two"`},
		{"text without text is null, never the block JSON in value", col("c", "Name", "text"),
			`{"key":"name","column_id":"c","value":"[{\"type\":\"rich_text\"}]"}`,
			`null`},
		{"rich_text without text is null", col("c", "Notes", "rich_text"),
			`{"column_id":"c","value":"[]","rich_text":[]}`,
			`null`},
		{"select that is not an array is null", col("c", "S", "select"),
			`{"column_id":"c","select":"done"}`,
			`null`},
		{"link with a non-object entry passes it through", col("c", "Link", "link"),
			`{"column_id":"c","link":["https://example.com"]}`,
			`["https://example.com"]`},
		{"message with a non-object entry passes it through", col("c", "Msg", "message"),
			`{"column_id":"c","message":["x"]}`,
			`["x"]`},
		{"todo_due_date is unobserved so it takes the raw path", col("c", "Due", "todo_due_date"),
			`{"key":"todo_due_date","column_id":"c","date":["2026-01-02"]}`,
			`{"date":["2026-01-02"]}`},
		{"select maps values to labels", statusCol,
			`{"key":"Col01BBB","column_id":"Col01CCC","value":"done","select":["done"]}`,
			`["Done"]`},
		{"select unknown value falls back to raw", statusCol,
			`{"key":"Col01BBB","column_id":"Col01CCC","value":"Opt99","select":["Opt99","todo"]}`,
			`["Opt99","To do"]`},
		{"multi_select maps every value", listColumn{ID: "c", Name: "Tags", Type: "multi_select", Options: statusCol.Options},
			`{"column_id":"c","select":["todo","done"]}`,
			`["To do","Done"]`},
		{"select with no choices keeps raw values", col("c", "S", "select"),
			`{"column_id":"c","select":["a"]}`,
			`["a"]`},
		{"user emits ids", col("c", "Owner", "user"),
			`{"column_id":"c","user":["U01XYZ","U02MGR"]}`,
			`["U01XYZ","U02MGR"]`},
		{"created_by emits ids", col("c", "Creator", "created_by"),
			`{"column_id":"c","user":["U01XYZ"]}`,
			`["U01XYZ"]`},
		{"todo_assignee emits ids", col("c", "Assignee", "todo_assignee"),
			`{"column_id":"c","user":["U01XYZ"]}`,
			`["U01XYZ"]`},
		{"channel emits ids", col("c", "Chan", "channel"),
			`{"column_id":"c","channel":["C01ABC"]}`,
			`["C01ABC"]`},
		{"date emits first date", col("c", "Due", "date"),
			`{"column_id":"c","date":["2026-05-21"],"timestamp":[-1]}`,
			`"2026-05-21"`},
		{"date with empty array is null", col("c", "Due", "date"),
			`{"column_id":"c","date":[]}`,
			`null`},
		{"created_time emits first timestamp verbatim", col("c", "Created", "created_time"),
			`{"column_id":"c","timestamp":[1787681564]}`,
			`1787681564`},
		{"checkbox emits bool", col("c", "Done", "checkbox"),
			`{"column_id":"c","checkbox":true}`,
			`true`},
		{"todo_completed emits bool", col("c", "Done", "todo_completed"),
			`{"column_id":"c","checkbox":false}`,
			`false`},
		{"number emits first number verbatim", col("c", "Count", "number"),
			`{"column_id":"c","number":[3.50]}`,
			`3.50`},
		{"email emits array", col("c", "Mail", "email"),
			`{"column_id":"c","email":["a@example.com"]}`,
			`["a@example.com"]`},
		{"link emits urls", col("c", "Link", "link"),
			`{"column_id":"c","link":[{"originalUrl":"https://example.com","attachment":{},"displayAsUrl":true},{"original_url":"https://example.org"}]}`,
			`["https://example.com","https://example.org"]`},
		{"message emits permalink shape", col("c", "Msg", "message"),
			`{"column_id":"c","message":[{"value":"https://acme.slack.com/archives/C01ABC/p1709251200000100","channel_id":"C01ABC","ts":"1709251200.000100","thread_ts":"0000000000.000000"}]}`,
			`[{"channel_id":"C01ABC","permalink":"https://acme.slack.com/archives/C01ABC/p1709251200000100","thread_ts":"0000000000.000000","ts":"1709251200.000100"}]`},
		{"unknown type emits raw field minus key and column_id", col("c", "Mystery", "hologram"),
			`{"key":"k","column_id":"c","value":"v","hologram":[1,2]}`,
			`{"hologram":[1,2],"value":"v"}`},
		{"typed key missing is null", col("c", "Owner", "user"),
			`{"column_id":"c","value":"U01XYZ"}`,
			`null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderFieldValue(tt.col, mustJSON(t, tt.field))
			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(gotJSON) != tt.want {
				t.Errorf("renderFieldValue = %s, want %s", gotJSON, tt.want)
			}
		})
	}
}

func TestRenderRecord(t *testing.T) {
	schema := []listColumn{
		col("Col01AAA", "Name", "text"),
		col("Col01BBB", "Owner", "user"),
		col("Col01CCC", "Name", "text"),
	}
	rr := newRecordRenderer(schema, false)
	rec := mustJSON(t, `{"id":"Rec01AAA","list_id":"F01ABC","date_created":1709251200,"created_by":"U01XYZ","updated_by":"U02MGR","updated_timestamp":"1709251300","position":"1709251200.12345","thread_ts":"1709251201.000100","platform_refs":{},"view_positions":{"View01AAA":"x"},"fields":[{"key":"name","column_id":"Col01AAA","value":"x","text":"Ship it"},{"key":"k","column_id":"Col01CCC","text":"dup"}]}`)

	row, err := rr.render(rec)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(row)
	want := `{"created_by":"U01XYZ","date_created":1709251200,"fields":{"Name":"Ship it","Name_2":"dup","Owner":null},"id":"Rec01AAA","position":"1709251200.12345","thread_ts":"1709251201.000100","updated_by":"U02MGR","updated_timestamp":"1709251300"}`
	if string(got) != want {
		t.Errorf("renderRecord =\n%s\nwant\n%s", got, want)
	}
	if _, ok := row["raw_fields"]; ok {
		t.Error("raw_fields present without --raw")
	}
	if _, ok := row["platform_refs"]; ok {
		t.Error("platform_refs should not leak into the row")
	}

	// thread_ts is only emitted when the record carries one.
	delete(rec, "thread_ts")
	row, err = newRecordRenderer(schema, true).render(rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := row["thread_ts"]; ok {
		t.Error("thread_ts emitted for a record without one")
	}
	raw, ok := row["raw_fields"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("raw_fields = %#v, want the 2 original field objects", row["raw_fields"])
	}
	if first, _ := raw[0].(map[string]any); first["column_id"] != "Col01AAA" || first["key"] != "name" {
		t.Errorf("raw_fields[0] lost its original keys: %#v", raw[0])
	}
}

func TestRenderRecord_UnknownColumnIsSchemaMismatch(t *testing.T) {
	schema := []listColumn{col("Col01AAA", "Name", "text")}
	rec := mustJSON(t, `{"id":"Rec01AAA","fields":[{"column_id":"Col09ZZZ","text":"orphan"}]}`)

	_, err := newRecordRenderer(schema, false).render(rec)
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T %v", err, err)
	}
	if oErr.Err != "list_schema_mismatch" {
		t.Errorf("error = %q, want list_schema_mismatch", oErr.Err)
	}
	for _, want := range []string{"Col09ZZZ", "Rec01AAA"} {
		if !strings.Contains(oErr.Detail, want) {
			t.Errorf("detail %q should name %s", oErr.Detail, want)
		}
	}
}

func TestRenderRecord_MalformedFields(t *testing.T) {
	schema := []listColumn{col("Col01AAA", "Name", "text")}
	rr := newRecordRenderer(schema, true)

	tests := []struct {
		name    string
		rec     string
		wantErr string // "" means success with an all-null row
	}{
		{"fields absent is a record with no values", `{"id":"Rec01AAA"}`, ""},
		{"fields null is a record with no values", `{"id":"Rec01AAA","fields":null}`, ""},
		{"fields not an array is a parse error, not a blank row", `{"id":"Rec01AAA","fields":{"column_id":"Col01AAA"}}`, "parse_error"},
		{"non-object entry is a parse error, not a schema mismatch", `{"id":"Rec01AAA","fields":["Col01AAA"]}`, "parse_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row, err := rr.render(mustJSON(t, tt.rec))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				fields, _ := row["fields"].(map[string]any)
				if v, ok := fields["Name"]; !ok || v != nil {
					t.Errorf("fields = %v, want Name present and null", fields)
				}
				if raw, ok := row["raw_fields"].([]any); !ok || len(raw) != 0 {
					t.Errorf("raw_fields = %#v, want an empty array", row["raw_fields"])
				}
				return
			}
			var oErr *output.Error
			if !errors.As(err, &oErr) || oErr.Err != tt.wantErr {
				t.Fatalf("expected %s, got %v", tt.wantErr, err)
			}
			if !strings.Contains(oErr.Detail, "Rec01AAA") {
				t.Errorf("detail %q should name the record", oErr.Detail)
			}
		})
	}
}
