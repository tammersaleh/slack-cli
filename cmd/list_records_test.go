package cmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// recordStore extends listWriteStore with the record endpoints. It models
// the two Slack behaviors the CLI must guard against: lists.records.create
// silently drops a field whose key is not in the schema and still creates
// the record, and it stores a select value that is not one of the choices.
// lists.cells.update patches stored fields and echoes only the changed
// cells; lists.records.info returns the full record.
type recordStore struct {
	*listWriteStore
	records map[string]map[string]any
	nextRec int
}

func newRecordStore(t *testing.T) *recordStore {
	t.Helper()
	return &recordStore{listWriteStore: newListWriteStore(t).seed(), records: map[string]map[string]any{}, nextRec: 1}
}

// seededRecID is the fixture record's id, long enough for Slack's
// ^Rec[A-Z0-9]{8,}$ shape check (the read fixtures use a shorter one).
const seededRecID = "Rec01AAAAAAA"

// seedRecord stores the shared recordJSON fixture under seededRecID.
func (s *recordStore) seedRecord() *recordStore {
	dec := json.NewDecoder(strings.NewReader(recordJSON))
	dec.UseNumber()
	var rec map[string]any
	if err := dec.Decode(&rec); err != nil {
		s.t.Fatal(err)
	}
	rec["id"] = seededRecID
	s.records[seededRecID] = rec
	return s
}

// write answers a mutating method with body, or a bare {ok} when the test
// asked for one via bareOK.
func (s *recordStore) write(method string, w http.ResponseWriter, body map[string]any) {
	if s.bareOK[method] {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	data, err := json.Marshal(body)
	if err != nil {
		s.t.Fatal(err)
	}
	_, _ = w.Write(data)
}

func (s *recordStore) column(match func(col map[string]any) bool) map[string]any {
	for _, c := range s.schema() {
		col := c.(map[string]any)
		if match(col) {
			return col
		}
	}
	return nil
}

// storedField is the typed field object Slack stores for a scalar create
// value, by column type.
func storedField(col map[string]any, v any) map[string]any {
	f := map[string]any{"key": col["key"], "column_id": col["id"]}
	switch col["type"] {
	case "text", "rich_text":
		f["value"] = fmt.Sprintf("%q", v)
		f["text"] = v
		f["rich_text"] = []any{map[string]any{"type": "rich_text", "elements": []any{map[string]any{"type": "rich_text_section", "elements": []any{map[string]any{"type": "text", "text": v}}}}}}
	case "select", "multi_select":
		f["value"] = v
		f["select"] = []any{v}
	case "user":
		f["value"] = v
		f["user"] = []any{v}
	case "channel":
		f["value"] = v
		f["channel"] = []any{v}
	case "date":
		f["date"] = []any{v}
		f["timestamp"] = []any{-1}
	case "checkbox":
		f["checkbox"] = v
	case "number":
		f["number"] = []any{v}
	case "link":
		f["link"] = []any{map[string]any{"originalUrl": v, "displayAsUrl": true}}
	case "email":
		f["email"] = []any{v}
	}
	return f
}

func (s *recordStore) mux() *http.ServeMux {
	mux := s.listWriteStore.mux()
	mux.HandleFunc("/api/lists.records.create", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("lists.records.create", w, r) {
			return
		}
		if r.FormValue("list_id") != s.file["id"] {
			_, _ = w.Write([]byte(`{"ok":false,"error":"list_not_found"}`))
			return
		}
		var fields []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		dec := json.NewDecoder(strings.NewReader(r.FormValue("fields")))
		dec.UseNumber()
		if err := dec.Decode(&fields); err != nil {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
			return
		}
		id := fmt.Sprintf("Rec%02dNEW000000", s.nextRec)
		s.nextRec++
		stored := []any{}
		for _, f := range fields {
			col := s.column(func(c map[string]any) bool { return c["key"] == f.Key })
			if col == nil {
				continue // Slack drops unknown keys silently
			}
			stored = append(stored, storedField(col, f.Value))
		}
		rec := map[string]any{
			"id": id, "list_id": s.file["id"], "date_created": 1709251200, "created_by": "U01XYZ", "updated_by": "U01XYZ",
			"updated_timestamp": "1709251300", "position": "1709251200.00001", "platform_refs": map[string]any{}, "view_positions": map[string]any{},
			"fields": stored,
		}
		s.records[id] = rec
		s.write("lists.records.create", w, map[string]any{"ok": true, "record": rec})
	})
	mux.HandleFunc("/api/lists.cells.update", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("lists.cells.update", w, r) {
			return
		}
		var cells []map[string]any
		dec := json.NewDecoder(strings.NewReader(r.FormValue("cells")))
		dec.UseNumber()
		if err := dec.Decode(&cells); err != nil {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
			return
		}
		echoed := []any{}
		for _, cell := range cells {
			rec, ok := s.records[fmt.Sprint(cell["row_id"])]
			if !ok {
				_, _ = w.Write([]byte(`{"ok":false,"error":"record_not_found"}`))
				return
			}
			col := s.column(func(c map[string]any) bool { return c["id"] == cell["column_id"] })
			if col == nil {
				_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
				return
			}
			field := map[string]any{"key": col["key"], "column_id": col["id"]}
			for k, v := range cell {
				if k == "row_id" || k == "column_id" {
					continue
				}
				field[k] = v
				if k == "rich_text" {
					field["text"] = richTextPlain(v)
				}
				if k == "link" {
					// Slack stores camelCase and reads emit it.
					links := []any{}
					for _, l := range v.([]any) {
						links = append(links, map[string]any{"originalUrl": l.(map[string]any)["original_url"], "displayAsUrl": true})
					}
					field["link"] = links
				}
			}
			kept := []any{}
			for _, f := range rec["fields"].([]any) {
				if f.(map[string]any)["column_id"] != col["id"] {
					kept = append(kept, f)
				}
			}
			rec["fields"] = append(kept, field)
			echoed = append(echoed, cell)
		}
		s.write("lists.cells.update", w, map[string]any{"ok": true, "cells": echoed})
	})
	mux.HandleFunc("/api/lists.records.info", func(w http.ResponseWriter, r *http.Request) {
		if !s.handle("lists.records.info", w, r) {
			return
		}
		rec, ok := s.records[r.FormValue("id")]
		if !ok || r.FormValue("list_id") != s.file["id"] {
			_, _ = w.Write([]byte(`{"ok":false,"error":"record_not_found"}`))
			return
		}
		s.write("lists.records.info", w, map[string]any{"ok": true, "list": s.file, "record": rec, "subtasks": []any{}})
	})
	return mux
}

// richTextPlain concatenates the text inlines of a rich_text block array.
func richTextPlain(v any) string {
	var b strings.Builder
	for _, blk := range v.([]any) {
		for _, sec := range blk.(map[string]any)["elements"].([]any) {
			for _, el := range sec.(map[string]any)["elements"].([]any) {
				b.WriteString(fmt.Sprint(el.(map[string]any)["text"]))
			}
		}
	}
	return b.String()
}

func usersListHandler(members ...map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "members": members, "response_metadata": map[string]string{"next_cursor": ""}})
	}
}

func fieldsOf(t *testing.T, row map[string]any) map[string]any {
	t.Helper()
	f, ok := row["fields"].(map[string]any)
	if !ok {
		t.Fatalf("row has no fields object: %v", row)
	}
	return f
}

func assertNoWrites(t *testing.T, s *recordStore, out string) {
	t.Helper()
	if out != "" {
		t.Errorf("stdout = %q, want none", out)
	}
	for _, m := range s.methods() {
		if m != "files.info" {
			t.Errorf("unexpected call %s (calls = %v)", m, s.methods())
		}
	}
}

// --- list record add ---

func TestListRecordAdd_EveryTypeByKeyAsScalars(t *testing.T) {
	s := newRecordStore(t)
	// Add link and email and channel columns to cover every writable type.
	s.setSchema(append(s.schema(),
		map[string]any{"id": "Col01KKK", "name": "Contact", "key": "Col01KKL", "type": "email", "is_primary_column": false},
		map[string]any{"id": "Col01LLL", "name": "Room", "key": "Col01LLM", "type": "channel", "is_primary_column": false},
	))
	out, err := runWithMockSession(t, s.mux(), "list", "record", "add", "F01ABC",
		"name=Ship it", "Status=Done", "Col01DDD=U01XYZ", "Due=2026-12-01", "Shipped=true",
		"Link=https://example.com/a?b=c", "Estimate=3.50", "Contact=a@example.com", "Room=C01ABC")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(s.methods()) != "[files.info lists.records.create]" {
		t.Fatalf("calls = %v", s.methods())
	}
	formOnly(t, s.calls[1].form, map[string]string{
		"list_id": "F01ABC",
		// Addressed by column key, scalar values, in input order. The select
		// is sent as the choice value, not the label; the number stays as
		// typed; the value holding '=' survives the first-'=' split.
		"fields": `[{"key":"name","value":"Ship it"},{"key":"Col01BBB","value":"done"},{"key":"Col01DDK","value":"U01XYZ"},{"key":"Col01EEK","value":"2026-12-01"},{"key":"Col01FFK","value":true},{"key":"Col01GGK","value":"https://example.com/a?b=c"},{"key":"Col01IIK","value":3.50},{"key":"Col01KKL","value":"a@example.com"},{"key":"Col01LLM","value":"C01ABC"}]`,
	})
	rows := rowsOf(t, out)
	if len(rows) != 1 {
		t.Fatalf("rows = %d:\n%s", len(rows), out)
	}
	row := rows[0]
	if row["id"] != "Rec01NEW000000" {
		t.Errorf("id = %v, want the server-assigned id", row["id"])
	}
	f := fieldsOf(t, row)
	if f["Name"] != "Ship it" || fmt.Sprint(f["Status"]) != "[Done]" || fmt.Sprint(f["Owner"]) != "[U01XYZ]" ||
		f["Due"] != "2026-12-01" || f["Shipped"] != true || fmt.Sprint(f["Link"]) != "[https://example.com/a?b=c]" ||
		fmt.Sprint(f["Contact"]) != "[a@example.com]" || fmt.Sprint(f["Room"]) != "[C01ABC]" {
		t.Errorf("fields = %v", f)
	}
	// The number re-encodes as Slack stored it (rowsOf would read it as 3.5).
	if !strings.Contains(out, `"Estimate":3.50`) {
		t.Errorf("output lost the number's form:\n%s", out)
	}
	// Columns not assigned are present and null, same shape as list records.
	if v, ok := f["Thread"]; !ok || v != nil {
		t.Errorf("Thread = %v, %v; want present and null", v, ok)
	}
}

func TestListRecordAdd_SelectByValueAndCaseInsensitiveLabel(t *testing.T) {
	for _, in := range []string{"done", "Done", "DONE"} {
		t.Run(in, func(t *testing.T) {
			s := newRecordStore(t)
			if _, err := runWithMockSession(t, s.mux(), "list", "record", "add", "F01ABC", "Status="+in); err != nil {
				t.Fatal(err)
			}
			formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "fields": `[{"key":"Col01BBB","value":"done"}]`})
		})
	}
}

func TestListRecordAdd_StdinJSON(t *testing.T) {
	s := newRecordStore(t)
	stdin := `{"name":"a=b\nc","Shipped":false,"Estimate":3.50,"Status":"To do"}`
	out, err := runWithMockSessionStdin(t, stdin, s.mux(), "list", "record", "add", "F01ABC")
	if err != nil {
		t.Fatal(err)
	}
	// Exact payload: source order kept, the bool stays a bool, the number is
	// sent as typed (not re-encoded as 3.5), the select is the choice value.
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC",
		"fields": `[{"key":"name","value":"a=b\nc"},{"key":"Col01FFK","value":false},{"key":"Col01IIK","value":3.50},{"key":"Col01BBB","value":"todo"}]`})
	if rows := rowsOf(t, out); fieldsOf(t, rows[0])["Name"] != "a=b\nc" {
		t.Errorf("row = %v", rows[0])
	}
}

func TestListRecordAdd_RefusesBeforeTheCall(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin string
		code  string
		input string
	}{
		{name: "unknown column", args: []string{"Nope=x"}, code: "column_not_found", input: "Nope"},
		{name: "ambiguous name", args: []string{"Name=x"}, code: "ambiguous_column", input: "Name"},
		{name: "select not a choice", args: []string{"Status=Blocked"}, code: "invalid_choice", input: "Blocked"},
		{name: "two values for single select", args: []string{"Status=To do,Done"}, code: "invalid_choice", input: "To do,Done"},
		{name: "checkbox not bool", args: []string{"Shipped=yes"}, code: "invalid_input"},
		{name: "checkbox capitalized", args: []string{"Shipped=True"}, code: "invalid_input"},
		{name: "number not numeric", args: []string{"Estimate=three"}, code: "invalid_input"},
		{name: "number hex", args: []string{"Estimate=0x10"}, code: "invalid_input"},
		{name: "number infinite", args: []string{"Estimate=1e999"}, code: "invalid_input"},
		{name: "date not a date", args: []string{"Due=2026-02-30"}, code: "invalid_input"},
		{name: "date wrong format", args: []string{"Due=12/01/2026"}, code: "invalid_input"},
		{name: "link not a url", args: []string{"Link=example.com"}, code: "invalid_input"},
		{name: "no equals", args: []string{"Status"}, code: "invalid_input", input: "Status"},
		{name: "empty column", args: []string{"=x"}, code: "invalid_input"},
		{name: "duplicate column by key and name", args: []string{"Status=Done", "Col01BBB=To do"}, code: "invalid_input"},
		{name: "duplicate column by id and name", args: []string{"Status=Done", "Col01CCC=To do"}, code: "invalid_input"},
		{name: "zero assignments", args: nil, code: "invalid_input"},
		{name: "positional and stdin", args: []string{"Status=Done"}, stdin: `{"Shipped":true}`, code: "invalid_input"},
		{name: "stdin not an object", stdin: `["Status"]`, code: "invalid_input"},
		{name: "stdin not json", stdin: `{`, code: "invalid_input"},
		{name: "stdin empty object", stdin: `{}`, code: "invalid_input"},
		{name: "stdin trailing garbage", stdin: `{"Status":"Done"} x`, code: "invalid_input"},
		{name: "stdin two objects", stdin: `{"Status":"Done"}{"Shipped":true}`, code: "invalid_input"},
		{name: "stdin duplicate key", stdin: `{"Status":"Done","Status":"To do"}`, code: "invalid_input"},
		{name: "stdin keys differing by case", stdin: `{"Status":"Done","status":"To do"}`, code: "invalid_input"},
		{name: "duplicate column by key and id", args: []string{"Col01BBB=Done", "Col01CCC=To do"}, code: "invalid_input"},
		{name: "stdin null clears nothing on add", stdin: `{"Status":null}`, code: "invalid_input"},
		{name: "empty value on add", args: []string{"Status="}, code: "invalid_input"},
		{name: "stdin array value", stdin: `{"Status":["Done"]}`, code: "invalid_input"},
		{name: "stdin number for text", stdin: `{"name":3}`, code: "invalid_input"},
		{name: "unwritable type", args: []string{"Thread=https://acme.slack.com/archives/C01ABC/p1709251200000100"}, code: "invalid_input"},
		{name: "bad list url", args: []string{"Status=Done"}, code: "invalid_input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newRecordStore(t)
			list := "F01ABC"
			if tt.name == "bad list url" {
				list = "https://acme.slack.com/archives/C01ABC"
			}
			out, err := runWithMockSessionStdin(t, tt.stdin, s.mux(), append([]string{"list", "record", "add", list}, tt.args...)...)
			assertOutputErrorCode(t, err, tt.code)
			if tt.input != "" {
				var oErr *output.Error
				if errors.As(err, &oErr) && oErr.Input != tt.input {
					t.Errorf("input = %q, want %q", oErr.Input, tt.input)
				}
			}
			assertNoWrites(t, s, out)
		})
	}
}

func TestListRecordAdd_MultiSelectRejected(t *testing.T) {
	s := newRecordStore(t)
	s.setSchema(append(s.schema(), map[string]any{"id": "Col01MMM", "name": "Tags", "key": "Col01MMK", "type": "multi_select", "is_primary_column": false,
		"options": map[string]any{"format": "multi_select", "choices": []any{map[string]any{"value": "a", "label": "A", "color": "blue"}}}}))
	out, err := runWithMockSessionStdin(t, "", s.mux(), "list", "record", "add", "F01ABC", "Tags=A")
	assertOutputErrorCode(t, err, "invalid_input")
	assertNoWrites(t, s, out)
}

func TestListRecordAdd_ResolvesUserAndChannelNames(t *testing.T) {
	s := newRecordStore(t)
	s.setSchema(append(s.schema(), map[string]any{"id": "Col01LLL", "name": "Room", "key": "Col01LLM", "type": "channel", "is_primary_column": false}))
	mux := s.mux()
	mux.HandleFunc("/api/users.list", usersListHandler(map[string]any{"id": "U02MGR", "name": "bob", "profile": map[string]any{"display_name": "bob"}}))
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channels": []map[string]any{{"id": "C02DEF", "name": "general", "is_member": true}}, "response_metadata": map[string]string{"next_cursor": ""}})
	})
	if _, err := runWithMockSession(t, mux, "list", "record", "add", "F01ABC", "Owner=@bob", "Room=#general"); err != nil {
		t.Fatal(err)
	}
	formOnly(t, s.calls[len(s.calls)-1].form, map[string]string{"list_id": "F01ABC", "fields": `[{"key":"Col01DDK","value":"U02MGR"},{"key":"Col01LLM","value":"C02DEF"}]`})
}

func TestListRecordAdd_UnresolvedUserMakesNoWrite(t *testing.T) {
	s := newRecordStore(t)
	mux := s.mux()
	mux.HandleFunc("/api/users.list", usersListHandler())
	out, err := runWithMockSession(t, mux, "list", "record", "add", "F01ABC", "name=x", "Owner=@nobody")
	assertOutputErrorCode(t, err, "user_not_found")
	assertNoWrites(t, s, out)
}

func TestListRecordAdd_SlackError(t *testing.T) {
	s := newRecordStore(t)
	s.fail["lists.records.create"] = "invalid_arguments"
	out, err := runWithMockSession(t, s.mux(), "list", "record", "add", "F01ABC", "name=x")
	assertOutputErrorCode(t, err, "invalid_arguments")
	if out != "" {
		t.Errorf("stdout = %q", out)
	}
}

func TestListRecordAdd_UnreadableResponseIsPartialRow(t *testing.T) {
	s := newRecordStore(t)
	s.bareOK["lists.records.create"] = true
	out, err := runWithMockSession(t, s.mux(), "list", "record", "add", "F01ABC", "name=x")
	assertExitError(t, err)
	rows := rowsOf(t, out)
	if len(rows) != 1 || rows[0]["error"] != "parse_error" || rows[0]["list_id"] != "F01ABC" {
		t.Errorf("rows = %v", rows)
	}
}

// --- list record update ---

func TestListRecordUpdate_TypedCellsThenReread(t *testing.T) {
	s := newRecordStore(t).seedRecord()
	s.setSchema(append(s.schema(),
		map[string]any{"id": "Col01KKK", "name": "Contact", "key": "Col01KKL", "type": "email", "is_primary_column": false},
		map[string]any{"id": "Col01LLL", "name": "Room", "key": "Col01LLM", "type": "channel", "is_primary_column": false},
	))
	out, err := runWithMockSession(t, s.mux(), "list", "record", "update", "F01ABC", seededRecID,
		"name=edited", "Status=To do", "Due=2027-01-02", "Owner=U02MGR", "Shipped=false",
		"Estimate=7", "Link=https://example.org", "Contact=b@example.com", "Room=C01ABC")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(s.methods()) != "[files.info lists.cells.update lists.records.info]" {
		t.Fatalf("calls = %v", s.methods())
	}
	formOnly(t, s.calls[1].form, map[string]string{
		"list_id": "F01ABC",
		"cells": `[{"column_id":"Col01AAA","rich_text":[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"edited"}]}]}],"row_id":"` + seededRecID + `"},` +
			`{"column_id":"Col01CCC","row_id":"` + seededRecID + `","select":["todo"]},` +
			`{"column_id":"Col01EEE","date":["2027-01-02"],"row_id":"` + seededRecID + `"},` +
			`{"column_id":"Col01DDD","row_id":"` + seededRecID + `","user":["U02MGR"]},` +
			`{"checkbox":false,"column_id":"Col01FFF","row_id":"` + seededRecID + `"},` +
			`{"column_id":"Col01III","number":[7],"row_id":"` + seededRecID + `"},` +
			`{"column_id":"Col01GGG","link":[{"original_url":"https://example.org"}],"row_id":"` + seededRecID + `"},` +
			`{"column_id":"Col01KKK","email":["b@example.com"],"row_id":"` + seededRecID + `"},` +
			`{"channel":["C01ABC"],"column_id":"Col01LLL","row_id":"` + seededRecID + `"}]`,
	})
	formOnly(t, s.calls[2].form, map[string]string{"list_id": "F01ABC", "id": seededRecID})
	rows := rowsOf(t, out)
	if len(rows) != 1 {
		t.Fatalf("rows = %d:\n%s", len(rows), out)
	}
	row := rows[0]
	if row["id"] != seededRecID || row["created_by"] != "U01XYZ" {
		t.Errorf("row metadata = %v", row)
	}
	f := fieldsOf(t, row)
	if f["Name"] != "edited" || fmt.Sprint(f["Status"]) != "[To do]" || f["Due"] != "2027-01-02" || fmt.Sprint(f["Owner"]) != "[U02MGR]" ||
		f["Shipped"] != false || fmt.Sprint(f["Estimate"]) != "7" || fmt.Sprint(f["Link"]) != "[https://example.org]" {
		t.Errorf("fields = %v", f)
	}
	// The untouched message column comes from the re-read, not a null pad.
	if f["Thread"] == nil {
		t.Errorf("Thread was lost: %v", f)
	}
}

func TestListRecordUpdate_NullAndEmptyClearSelectAndUser(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin string
	}{
		{"stdin null", nil, `{"Status":null,"Owner":null,"Tags":null}`},
		{"positional empty", []string{"Status=", "Owner=", "Tags="}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newRecordStore(t).seedRecord()
			s.setSchema(append(s.schema(), map[string]any{"id": "Col01MMM", "name": "Tags", "key": "Col01MMK", "type": "multi_select", "is_primary_column": false,
				"options": map[string]any{"format": "multi_select", "choices": []any{map[string]any{"value": "a", "label": "A", "color": "blue"}}}}))
			out, err := runWithMockSessionStdin(t, tt.stdin, s.mux(), append([]string{"list", "record", "update", "F01ABC", seededRecID}, tt.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC",
				"cells": `[{"column_id":"Col01CCC","row_id":"` + seededRecID + `","select":[]},{"column_id":"Col01DDD","row_id":"` + seededRecID + `","user":[]},{"column_id":"Col01MMM","row_id":"` + seededRecID + `","select":[]}]`})
			f := fieldsOf(t, rowsOf(t, out)[0])
			if fmt.Sprint(f["Status"]) != "[]" || fmt.Sprint(f["Owner"]) != "[]" || fmt.Sprint(f["Tags"]) != "[]" {
				t.Errorf("fields = %v", f)
			}
		})
	}
}

func TestListRecordUpdate_MultiSelectSeveralValues(t *testing.T) {
	addTags := func(s *recordStore) {
		s.setSchema(append(s.schema(), map[string]any{"id": "Col01MMM", "name": "Tags", "key": "Col01MMK", "type": "multi_select", "is_primary_column": false,
			"options": map[string]any{"format": "multi_select", "choices": []any{
				map[string]any{"value": "a", "label": "Alpha", "color": "blue"},
				map[string]any{"value": "b", "label": "Beta, really", "color": "green"}}}}))
	}
	t.Run("positional comma split", func(t *testing.T) {
		s := newRecordStore(t).seedRecord()
		addTags(s)
		out, err := runWithMockSession(t, s.mux(), "list", "record", "update", "F01ABC", seededRecID, "Tags=Alpha, a")
		if err != nil {
			t.Fatal(err)
		}
		// Duplicates collapse: the label and the value name the same choice.
		formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "cells": `[{"column_id":"Col01MMM","row_id":"` + seededRecID + `","select":["a"]}]`})
		if f := fieldsOf(t, rowsOf(t, out)[0]); fmt.Sprint(f["Tags"]) != "[Alpha]" {
			t.Errorf("fields = %v", f)
		}
	})
	t.Run("stdin array keeps a comma in a label", func(t *testing.T) {
		s := newRecordStore(t).seedRecord()
		addTags(s)
		_, err := runWithMockSessionStdin(t, `{"Tags":["Beta, really","Alpha"]}`, s.mux(), "list", "record", "update", "F01ABC", seededRecID)
		if err != nil {
			t.Fatal(err)
		}
		formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "cells": `[{"column_id":"Col01MMM","row_id":"` + seededRecID + `","select":["b","a"]}]`})
	})
	t.Run("unknown choice among several", func(t *testing.T) {
		s := newRecordStore(t).seedRecord()
		addTags(s)
		out, err := runWithMockSession(t, s.mux(), "list", "record", "update", "F01ABC", seededRecID, "Tags=Alpha,Gamma")
		assertOutputErrorCode(t, err, "invalid_choice")
		assertNoWrites(t, s, out)
	})
}

func TestListRecordUpdate_RefusesBeforeTheCall(t *testing.T) {
	tests := []struct {
		name  string
		rec   string
		args  []string
		stdin string
		code  string
	}{
		{name: "bad record id", rec: "Rec01", args: []string{"name=x"}, code: "invalid_input"},
		{name: "lowercase record id", rec: "rec01aaaaaa", args: []string{"name=x"}, code: "invalid_input"},
		{name: "zero assignments", rec: seededRecID, code: "invalid_input"},
		{name: "clear text", rec: seededRecID, stdin: `{"name":null}`, code: "invalid_input"},
		{name: "clear date", rec: seededRecID, args: []string{"Due="}, code: "invalid_input"},
		{name: "clear checkbox", rec: seededRecID, args: []string{"Shipped="}, code: "invalid_input"},
		{name: "clear number", rec: seededRecID, args: []string{"Estimate="}, code: "invalid_input"},
		{name: "clear link", rec: seededRecID, args: []string{"Link="}, code: "invalid_input"},
		{name: "select not a choice", rec: seededRecID, args: []string{"Status=Blocked"}, code: "invalid_choice"},
		{name: "single select with two values", rec: seededRecID, stdin: `{"Status":["Done","To do"]}`, code: "invalid_input"},
		{name: "unknown column", rec: seededRecID, args: []string{"Nope=x"}, code: "column_not_found"},
		{name: "duplicate column", rec: seededRecID, args: []string{"Status=Done", "status=Done"}, code: "invalid_input"},
		{name: "positional and stdin", rec: seededRecID, args: []string{"Status=Done"}, stdin: `{"Shipped":true}`, code: "invalid_input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newRecordStore(t).seedRecord()
			out, err := runWithMockSessionStdin(t, tt.stdin, s.mux(), append([]string{"list", "record", "update", "F01ABC", tt.rec}, tt.args...)...)
			assertOutputErrorCode(t, err, tt.code)
			assertNoWrites(t, s, out)
		})
	}
}

func TestListRecordUpdate_BadRecordIDCostsNoCall(t *testing.T) {
	s := newRecordStore(t)
	_, err := runWithMockSession(t, s.mux(), "list", "record", "update", "F01ABC", "Rec01", "name=x")
	assertOutputErrorCode(t, err, "invalid_input")
	if len(s.calls) != 0 {
		t.Errorf("calls = %v, want none (record id is checked before files.info)", s.methods())
	}
}

func TestListRecordUpdate_SlackErrorOnWrite(t *testing.T) {
	s := newRecordStore(t)
	out, err := runWithMockSession(t, s.mux(), "list", "record", "update", "F01ABC", "Rec99ZZZZZZ", "name=x")
	assertOutputErrorCode(t, err, "record_not_found")
	if out != "" {
		t.Errorf("stdout = %q", out)
	}
	if fmt.Sprint(s.methods()) != "[files.info lists.cells.update]" {
		t.Errorf("calls = %v", s.methods())
	}
}

func TestListRecordUpdate_RereadFailureIsPartialRow(t *testing.T) {
	s := newRecordStore(t).seedRecord()
	s.fail["lists.records.info"] = "ratelimited"
	out, err := runWithMockSession(t, s.mux(), "list", "record", "update", "F01ABC", seededRecID, "Status=Done")
	assertExitError(t, err)
	rows := rowsOf(t, out)
	if len(rows) != 1 || rows[0]["id"] != seededRecID || rows[0]["list_id"] != "F01ABC" || rows[0]["error"] != "ratelimited" {
		t.Errorf("rows = %v", rows)
	}
	// The write did land.
	if fmt.Sprint(s.methods()) != "[files.info lists.cells.update lists.records.info]" {
		t.Errorf("calls = %v", s.methods())
	}
}

func TestListRecordUpdate_ResolvesUserName(t *testing.T) {
	s := newRecordStore(t).seedRecord()
	mux := s.mux()
	mux.HandleFunc("/api/users.list", usersListHandler(map[string]any{"id": "U02MGR", "name": "bob", "profile": map[string]any{"display_name": "bob"}}))
	if _, err := runWithMockSession(t, mux, "list", "record", "update", "F01ABC", seededRecID, "Owner=@bob"); err != nil {
		t.Fatal(err)
	}
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "cells": `[{"column_id":"Col01DDD","row_id":"` + seededRecID + `","user":["U02MGR"]}]`})
}

func TestListRecordUpdate_StdinNumberSentVerbatim(t *testing.T) {
	s := newRecordStore(t).seedRecord()
	if _, err := runWithMockSessionStdin(t, `{"Estimate":12345678901234567890.50}`, s.mux(), "list", "record", "update", "F01ABC", seededRecID); err != nil {
		t.Fatal(err)
	}
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "cells": `[{"column_id":"Col01III","number":[12345678901234567890.50],"row_id":"` + seededRecID + `"}]`})
}

func TestListRecordUpdate_UnreadableRereadIsPartialRow(t *testing.T) {
	s := newRecordStore(t).seedRecord()
	s.bareOK["lists.records.info"] = true
	out, err := runWithMockSession(t, s.mux(), "list", "record", "update", "F01ABC", seededRecID, "Status=Done")
	assertExitError(t, err)
	rows := rowsOf(t, out)
	if len(rows) != 1 || rows[0]["id"] != seededRecID || rows[0]["list_id"] != "F01ABC" || rows[0]["error"] != "parse_error" {
		t.Errorf("rows = %v", rows)
	}
}

// --- token routing on Enterprise Grid ---

// A channel name resolves on the workspace (T) client: users.conversations
// and conversations.list are enterprise_is_restricted on the org (E) token
// the session client carries, so a #name could never resolve there. The
// lists.* calls, files.info, and user-name resolution stay on the session
// token. runWithMockSession cannot see this - one SLACK_TOKEN backs both
// clients - so this test uses two stored credentials.
func TestListRecordAdd_ChannelNameResolvesOnWorkspaceToken(t *testing.T) {
	s := newRecordStore(t)
	s.setSchema(append(s.schema(), map[string]any{"id": "Col01LLL", "name": "Room", "key": "Col01LLM", "type": "channel", "is_primary_column": false}))
	mux := s.mux()
	mux.HandleFunc("/api/users.list", usersListHandler(map[string]any{"id": "U02MGR", "name": "bob", "profile": map[string]any{"display_name": "bob"}}))
	// Output enrichment looks up created_by, which users.list above omits.
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "user": map[string]any{"id": "U01XYZ", "name": "alice"}})
	})
	for _, ep := range []string{"/api/users.conversations", "/api/conversations.list"} {
		mux.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
			if requestToken(r) != teamToken {
				slackErr(w, "enterprise_is_restricted")
				return
			}
			writeJSON(w, map[string]any{"ok": true, "channels": []map[string]any{{"id": "C02DEF", "name": "general", "is_member": true}}, "response_metadata": map[string]string{"next_cursor": ""}})
		})
	}
	wrapped, snapshot := tokenRecorder(t, mux)
	_, err := runWithTwoCredentials(t, wrapped, "list", "record", "add", "F01ABC", "Owner=@bob", "Room=#general")
	if err != nil {
		t.Fatal(err)
	}
	formOnly(t, s.calls[len(s.calls)-1].form, map[string]string{"list_id": "F01ABC", "fields": `[{"key":"Col01DDK","value":"U02MGR"},{"key":"Col01LLM","value":"C02DEF"}]`})
	assertEndpointTokens(t, snapshot(), map[string]string{
		"files.info":           orgToken,
		"lists.records.create": orgToken,
		"users.list":           orgToken,
		"users.info":           orgToken, // output enrichment stays on the session resolver
		"users.conversations":  teamToken,
	})
}

// The workspace client is built mid-command and overwrites cli.authMethod;
// the write still runs on the session credential, so an auth failure there
// must hint at the session's (desktop) re-auth, not the workspace's (oauth).
// Deleting the authMethod restore in resolveCells fails this.
func TestListRecordAdd_SessionAuthHintSurvivesSplit(t *testing.T) {
	s := newRecordStore(t)
	s.setSchema(append(s.schema(), map[string]any{"id": "Col01LLL", "name": "Room", "key": "Col01LLM", "type": "channel", "is_primary_column": false}))
	s.fail["lists.records.create"] = "invalid_auth"
	mux := s.mux()
	mux.HandleFunc("/api/users.conversations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "channels": []map[string]any{{"id": "C02DEF", "name": "general", "is_member": true}}, "response_metadata": map[string]string{"next_cursor": ""}})
	})
	_, err := runWithTwoCredentials(t, mux, "list", "record", "add", "F01ABC", "Room=#general")
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "invalid_auth" {
		t.Fatalf("expected invalid_auth, got %v", err)
	}
	if !strings.Contains(oErr.Hint, "--desktop") {
		t.Errorf("hint should name the session credential's desktop method, got %q", oErr.Hint)
	}
}

// echo adds a newline after the object; it must not count as trailing content.
func TestListRecordAdd_StdinTrailingNewline(t *testing.T) {
	s := newRecordStore(t)
	if _, err := runWithMockSessionStdin(t, "{\"Status\":\"Done\"}\n", s.mux(), "list", "record", "add", "F01ABC"); err != nil {
		t.Fatal(err)
	}
	formOnly(t, s.calls[1].form, map[string]string{"list_id": "F01ABC", "fields": `[{"key":"Col01BBB","value":"done"}]`})
}

// With only an org session credential stored, ids keep working (they never
// needed the workspace client) and a channel name is not_authed rather than
// a misleading channel_not_found.
func TestListRecordAdd_OrgOnlyCredential(t *testing.T) {
	orgOnly := map[string]any{orgWorkspaceID: map[string]any{
		"bot_token": orgToken, "cookie": "d=xyz", "auth_method": "desktop", "team_id": orgWorkspaceID, "team_name": "Acme Org",
	}}
	t.Run("channel id", func(t *testing.T) {
		s := newRecordStore(t)
		s.setSchema(append(s.schema(), map[string]any{"id": "Col01LLL", "name": "Room", "key": "Col01LLM", "type": "channel", "is_primary_column": false}))
		if _, err := runWithCredentialsStdin(t, "", orgOnly, teamWorkspaceID, s.mux(), "list", "record", "add", "F01ABC", "Room=C01ABC"); err != nil {
			t.Fatal(err)
		}
		formOnly(t, s.calls[len(s.calls)-1].form, map[string]string{"list_id": "F01ABC", "fields": `[{"key":"Col01LLM","value":"C01ABC"}]`})
	})
	t.Run("channel name", func(t *testing.T) {
		s := newRecordStore(t)
		s.setSchema(append(s.schema(), map[string]any{"id": "Col01LLL", "name": "Room", "key": "Col01LLM", "type": "channel", "is_primary_column": false}))
		out, err := runWithCredentialsStdin(t, "", orgOnly, teamWorkspaceID, s.mux(), "list", "record", "add", "F01ABC", "Room=#general")
		assertOutputErrorCode(t, err, "not_authed")
		assertNoWrites(t, s, out)
	})
}
