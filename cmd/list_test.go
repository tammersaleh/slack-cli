package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// listSchemaJSON is a files.info file object for a seven-column list, with
// every shape the renderer handles: a primary text column, a select with
// choices, a user column, a date, a checkbox, a link, and a duplicate name.
const listSchemaJSON = `{
  "id":"F01ABC","title":"Launch tasks","name":"list","filetype":"list","mimetype":"application/vnd.slack-list",
  "user":"U01XYZ","user_team":"T01ABC","created":1709251200,"updated":1709337600,
  "permalink":"https://acme.slack.com/lists/T01ABC/F01ABC",
  "list_metadata":{
    "description":"Everything before launch","icon":":rocket:",
    "schema":[
      {"id":"Col01AAA","name":"Name","key":"name","type":"text","is_primary_column":true,"options":{"format":"text"}},
      {"id":"Col01CCC","name":"Status","key":"Col01BBB","type":"select","is_primary_column":false,
       "options":{"format":"single_select","default_value":"todo","choices":[
         {"value":"todo","label":"To do","color":"gray"},{"value":"done","label":"Done","color":"green"}]}},
      {"id":"Col01DDD","name":"Owner","key":"Col01DDK","type":"user","is_primary_column":false,"options":null},
      {"id":"Col01EEE","name":"Due","key":"Col01EEK","type":"date","is_primary_column":false},
      {"id":"Col01FFF","name":"Shipped","key":"Col01FFK","type":"checkbox","is_primary_column":false},
      {"id":"Col01GGG","name":"Link","key":"Col01GGK","type":"link","is_primary_column":false},
      {"id":"Col01HHH","name":"Name","key":"Col01HHK","type":"text","is_primary_column":false},
      {"id":"Col01III","name":"Estimate","key":"Col01IIK","type":"number","is_primary_column":false},
      {"id":"Col01JJJ","name":"Thread","key":"Col01JJK","type":"message","is_primary_column":false}
    ],
    "views":[{"id":"View01AAA","name":"All","type":"table"}],
    "subtask_schema":[]
  },
  "list_limits":{"over_row_maximum":false,"row_count_limit":5000,"row_count":53,"archived_row_count":2,
    "over_column_maximum":false,"column_count":9,"column_count_limit":30,
    "over_view_maximum":false,"view_count":1,"view_count_limit":50,"max_attachments_per_cell":10}
}`

const recordJSON = `{"id":"Rec01AAA","list_id":"F01ABC","date_created":1709251200,"created_by":"U01XYZ","updated_by":"U01XYZ",
  "updated_timestamp":"1709251300","position":"1709251200.12345","platform_refs":{},"view_positions":{},
  "fields":[
    {"key":"name","column_id":"Col01AAA","value":"[]","text":"Ship it","rich_text":[]},
    {"key":"Col01BBB","column_id":"Col01CCC","value":"done","select":["done"]},
    {"key":"Col01DDK","column_id":"Col01DDD","value":"U01XYZ","user":["U01XYZ"]},
    {"key":"Col01EEK","column_id":"Col01EEE","date":["2026-05-21"],"timestamp":[-1]},
    {"key":"Col01GGK","column_id":"Col01GGG","link":[{"originalUrl":"https://example.com","displayAsUrl":true}]},
    {"key":"Col01IIK","column_id":"Col01III","value":"3.50","number":[3.50]},
    {"key":"Col01JJK","column_id":"Col01JJJ","message":[{"value":"https://acme.slack.com/archives/C01ABC/p1709251200000100","channel_id":"C01ABC","ts":"1709251200.000100","thread_ts":"0000000000.000000"}]}
  ]}`

// listFixture serves files.info for F01ABC and lists.records.list from pages,
// recording every records.list form it saw.
type listFixture struct {
	t         *testing.T
	fileJSON  string
	pages     map[string]string // cursor -> response body
	seenForms []map[string][]string
	fileCalls int
}

func newListFixture(t *testing.T) *listFixture {
	t.Helper()
	return &listFixture{t: t, fileJSON: listSchemaJSON, pages: map[string]string{}}
}

func (f *listFixture) page(cursor string, next string, records ...string) {
	f.pages[cursor] = `{"ok":true,"records":[` + strings.Join(records, ",") + `],` +
		`"limits":{"row_count":53,"archived_row_count":2,"row_count_limit":5000},` +
		`"response_metadata":{"next_cursor":"` + next + `"}}`
}

func (f *listFixture) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.fileCalls++
		if got := r.FormValue("file"); got != "F01ABC" {
			_, _ = w.Write([]byte(`{"ok":false,"error":"file_not_found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"file":` + f.fileJSON + `}`))
	})
	mux.HandleFunc("/api/lists.records.list", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.seenForms = append(f.seenForms, r.PostForm)
		body, ok := f.pages[r.FormValue("cursor")]
		if !ok {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_cursor"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func lastLine(t *testing.T, out string) map[string]any {
	t.Helper()
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		t.Fatal("no output")
	}
	return parseJSON(t, lines[len(lines)-1])
}

func metaOf(t *testing.T, out string) map[string]any {
	t.Helper()
	last := lastLine(t, out)
	meta, ok := last["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("last line is not a _meta trailer: %v", last)
	}
	return meta
}

func TestListRecords_RendersRowsAndLimits(t *testing.T) {
	f := newListFixture(t)
	f.page("", "", recordJSON)

	out, err := runWithMockSession(t, f.mux(), "list", "records", "https://acme.slack.com/lists/T01ABC/F01ABC")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + trailer, got %d lines:\n%s", len(lines), out)
	}

	// Decoded with UseNumber so the test itself cannot round 3.50 to 3.5.
	dec := json.NewDecoder(strings.NewReader(lines[0]))
	dec.UseNumber()
	var row map[string]any
	if err := dec.Decode(&row); err != nil {
		t.Fatal(err)
	}
	if row["id"] != "Rec01AAA" || row["created_by"] != "U01XYZ" || row["updated_timestamp"] != "1709251300" {
		t.Errorf("record metadata wrong: %v", row)
	}
	if _, ok := row["thread_ts"]; ok {
		t.Error("thread_ts emitted for a record without one")
	}
	if _, ok := row["raw_fields"]; ok {
		t.Error("raw_fields emitted without --raw")
	}
	fields, _ := row["fields"].(map[string]any)
	gotFields, _ := json.Marshal(fields)
	// 3.50 must survive as written: the printer decodes with UseNumber, so
	// no float64 detour rounds it to 3.5. The message value shows the real
	// end-to-end shape, including the ts_iso siblings the standard timestamp
	// enrichment adds to any nested ts key (the 0000000000.000000 non-reply
	// sentinel sits below the enrichment's year-2000 floor, so it gets none).
	want := `{"Due":"2026-05-21","Estimate":3.50,"Link":["https://example.com"],"Name":"Ship it","Name_2":null,"Owner":["U01XYZ"],"Shipped":null,"Status":["Done"],` +
		`"Thread":[{"channel_id":"C01ABC","permalink":"https://acme.slack.com/archives/C01ABC/p1709251200000100","thread_ts":"0000000000.000000","ts":"1709251200.000100","ts_iso":"2024-03-01T00:00:00Z"}]}`
	if string(gotFields) != want {
		t.Errorf("fields =\n%s\nwant\n%s", gotFields, want)
	}

	meta := metaOf(t, out)
	if meta["has_more"] != false || meta["row_count"] != float64(53) || meta["archived_row_count"] != float64(2) {
		t.Errorf("trailer = %v, want has_more=false row_count=53 archived_row_count=2", meta)
	}

	if f.fileCalls != 1 {
		t.Errorf("files.info called %d times, want 1", f.fileCalls)
	}
	form := f.seenForms[0]
	if got := form["list_id"]; len(got) != 1 || got[0] != "F01ABC" {
		t.Errorf("list_id = %v", got)
	}
	if got := form["limit"]; len(got) != 1 || got[0] != "100" {
		t.Errorf("default limit = %v, want 100", got)
	}
	if _, ok := form["view_id"]; ok {
		t.Error("view_id sent without --view")
	}
	if _, ok := form["cursor"]; ok {
		t.Error("cursor sent on the first page")
	}
}

func TestListRecords_RawAddsOriginalFields(t *testing.T) {
	f := newListFixture(t)
	f.page("", "", recordJSON)

	out, err := runWithMockSession(t, f.mux(), "list", "records", "F01ABC", "--raw")
	if err != nil {
		t.Fatal(err)
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	raw, ok := row["raw_fields"].([]any)
	if !ok || len(raw) != 7 {
		t.Fatalf("raw_fields = %#v, want 7 field objects", row["raw_fields"])
	}
	first, _ := raw[0].(map[string]any)
	if first["column_id"] != "Col01AAA" || first["key"] != "name" {
		t.Errorf("raw_fields[0] = %v, want the untouched field object", first)
	}
}

func TestListRecords_AllWalksCursors(t *testing.T) {
	f := newListFixture(t)
	rec2 := strings.Replace(recordJSON, "Rec01AAA", "Rec01BBB", 1)
	f.page("", "cur2", recordJSON)
	f.page("cur2", "", rec2)

	out, err := runWithMockSession(t, f.mux(), "list", "records", "F01ABC", "--all", "--limit", "2", "--view", "View01AAA")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + trailer, got %d:\n%s", len(lines), out)
	}
	if parseJSON(t, lines[0])["id"] != "Rec01AAA" || parseJSON(t, lines[1])["id"] != "Rec01BBB" {
		t.Errorf("rows out of order or missing:\n%s", out)
	}
	if meta := metaOf(t, out); meta["has_more"] != false || meta["row_count"] != float64(53) {
		t.Errorf("trailer = %v", meta)
	}

	if len(f.seenForms) != 2 {
		t.Fatalf("records.list called %d times, want 2", len(f.seenForms))
	}
	if got := f.seenForms[1]["cursor"]; len(got) != 1 || got[0] != "cur2" {
		t.Errorf("second page cursor = %v, want cur2", got)
	}
	for i, form := range f.seenForms {
		if got := form["view_id"]; len(got) != 1 || got[0] != "View01AAA" {
			t.Errorf("page %d view_id = %v, want View01AAA", i, got)
		}
		if got := form["limit"]; len(got) != 1 || got[0] != "2" {
			t.Errorf("page %d limit = %v, want 2", i, got)
		}
	}
	if f.fileCalls != 1 {
		t.Errorf("files.info called %d times across the walk, want 1", f.fileCalls)
	}
}

func TestListRecords_SinglePageReportsCursor(t *testing.T) {
	f := newListFixture(t)
	f.page("", "cur2", recordJSON)

	out, err := runWithMockSession(t, f.mux(), "list", "records", "F01ABC")
	if err != nil {
		t.Fatal(err)
	}
	if meta := metaOf(t, out); meta["has_more"] != true || meta["next_cursor"] != "cur2" {
		t.Errorf("trailer = %v, want has_more=true next_cursor=cur2", meta)
	}
}

func TestListRecords_SchemaMismatchIsTerminalWithTrailer(t *testing.T) {
	f := newListFixture(t)
	orphan := strings.Replace(recordJSON, `"column_id":"Col01GGG"`, `"column_id":"Col09ZZZ"`, 1)
	// A good record ahead of the bad one: the whole page must be withheld,
	// which a render-and-print-per-record implementation would not do.
	f.page("", "cur2", recordJSON, orphan)

	out, err := runWithMockSession(t, f.mux(), "list", "records", "F01ABC")
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "list_schema_mismatch" {
		t.Fatalf("expected list_schema_mismatch, got %v", err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("expected only a trailer (no rows from the failed page), got %d lines:\n%s", len(lines), out)
	}
	meta := metaOf(t, out)
	if meta["error"] != "list_schema_mismatch" || meta["has_more"] != false {
		t.Errorf("trailer = %v, want error=list_schema_mismatch has_more=false (not resumable)", meta)
	}
	if _, ok := meta["next_cursor"]; ok {
		t.Error("a schema mismatch must not hand back a resume cursor")
	}
	// The limits arrived with the page that failed to render, so the
	// failure trailer still reports them.
	if meta["row_count"] != float64(53) || meta["archived_row_count"] != float64(2) {
		t.Errorf("failure trailer lost the limits: %v", meta)
	}
}

func TestListRecords_MidWalkFailures(t *testing.T) {
	orphan := strings.Replace(recordJSON, `"column_id":"Col01GGG"`, `"column_id":"Col09ZZZ"`, 1)

	t.Run("schema mismatch on page 2 keeps page 1 and is terminal", func(t *testing.T) {
		f := newListFixture(t)
		f.page("", "cur2", recordJSON)
		f.page("cur2", "cur3", orphan)

		out, err := runWithMockSession(t, f.mux(), "list", "records", "F01ABC", "--all")
		var oErr *output.Error
		if !errors.As(err, &oErr) || oErr.Err != "list_schema_mismatch" {
			t.Fatalf("expected list_schema_mismatch, got %v", err)
		}
		lines := nonEmptyLines(out)
		if len(lines) != 2 || parseJSON(t, lines[0])["id"] != "Rec01AAA" {
			t.Fatalf("expected page-1 row + trailer, got:\n%s", out)
		}
		meta := metaOf(t, out)
		if meta["error"] != "list_schema_mismatch" || meta["has_more"] != false || meta["next_cursor"] != nil {
			t.Errorf("trailer = %v", meta)
		}
		if meta["row_count"] != float64(53) {
			t.Errorf("trailer lost the limits read on page 1: %v", meta)
		}
	})

	t.Run("slack error on page 2 is resumable from that page", func(t *testing.T) {
		f := newListFixture(t)
		f.page("", "cur2", recordJSON)
		// Page 2 answers a Slack error outside api.nonResumableErrors, so
		// the trailer must keep the cursor that failed.
		f.pages["cur2"] = `{"ok":false,"error":"internal_error"}`

		out, err := runWithMockSession(t, f.mux(), "list", "records", "F01ABC", "--all")
		var oErr *output.Error
		if !errors.As(err, &oErr) || oErr.Err != "internal_error" {
			t.Fatalf("expected internal_error, got %v", err)
		}
		lines := nonEmptyLines(out)
		if len(lines) != 2 {
			t.Fatalf("expected page-1 row + trailer, got:\n%s", out)
		}
		meta := metaOf(t, out)
		if meta["error"] != "internal_error" || meta["has_more"] != true || meta["next_cursor"] != "cur2" {
			t.Errorf("trailer = %v, want has_more=true next_cursor=cur2", meta)
		}
		if meta["row_count"] != float64(53) || meta["archived_row_count"] != float64(2) {
			t.Errorf("trailer lost the limits read on page 1: %v", meta)
		}
	})
}

func TestListRecords_LocalValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"limit 1 rejected", []string{"list", "records", "F01ABC", "--limit", "1"}, "invalid_input"},
		{"all and cursor exclusive", []string{"list", "records", "F01ABC", "--all", "--cursor", "x"}, "invalid_input"},
		{"wrong url kind", []string{"list", "records", "https://acme.slack.com/archives/C01ABC"}, "invalid_input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newListFixture(t)
			out, err := runWithMockSession(t, f.mux(), tt.args...)
			var oErr *output.Error
			if !errors.As(err, &oErr) || oErr.Err != tt.want {
				t.Fatalf("expected %s, got %v", tt.want, err)
			}
			if out != "" {
				t.Errorf("expected no stdout, got %q", out)
			}
			if f.fileCalls != 0 || len(f.seenForms) != 0 {
				t.Error("local validation must make no API calls")
			}
		})
	}
}

func TestListRecords_LimitZeroIsDefault(t *testing.T) {
	f := newListFixture(t)
	f.page("", "", recordJSON)
	if _, err := runWithMockSession(t, f.mux(), "list", "records", "F01ABC", "--limit", "0"); err != nil {
		t.Fatal(err)
	}
	if got := f.seenForms[0]["limit"]; len(got) != 1 || got[0] != "100" {
		t.Errorf("--limit 0 sent limit=%v, want the default 100", got)
	}
}

func TestListInfo_NoLimitsBlock(t *testing.T) {
	f := newListFixture(t)
	f.fileJSON = `{"id":"F01ABC","title":"Bare","filetype":"list","list_metadata":{"schema":[]}}`
	out, err := runWithMockSession(t, f.mux(), "list", "info", "F01ABC")
	if err != nil {
		t.Fatal(err)
	}
	row := parseJSON(t, nonEmptyLines(out)[0])
	if v, ok := row["limits"]; !ok || v != nil {
		t.Errorf("limits = %#v, want present and null when Slack sent none", v)
	}
}

func TestListRecords_NotAList(t *testing.T) {
	f := newListFixture(t)
	f.fileJSON = `{"id":"F01ABC","title":"Q1 report","filetype":"pdf","mimetype":"application/pdf"}`

	_, err := runWithMockSession(t, f.mux(), "list", "records", "F01ABC")
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "not_a_list" {
		t.Fatalf("expected not_a_list, got %v", err)
	}
	if len(f.seenForms) != 0 {
		t.Error("records.list must not be called for a non-list file")
	}
}

func TestListRecords_FileNotFound(t *testing.T) {
	f := newListFixture(t)
	_, err := runWithMockSession(t, f.mux(), "list", "records", "F09ZZZ")
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "file_not_found" {
		t.Fatalf("expected file_not_found, got %v", err)
	}
}

func TestList_RequiresSessionToken(t *testing.T) {
	for _, args := range [][]string{
		{"list", "records", "F01ABC"},
		{"list", "info", "F01ABC"},
		{"list", "schema", "F01ABC"},
		{"list", "find", "Launch"},
	} {
		t.Run(args[1], func(t *testing.T) {
			_, err := runWithMock(t, emptyMux(), args...)
			var oErr *output.Error
			if !errors.As(err, &oErr) || oErr.Err != "session_token_required" {
				t.Fatalf("expected session_token_required on a bot token, got %v", err)
			}
		})
	}
}

func TestListInfo(t *testing.T) {
	f := newListFixture(t)
	out, err := runWithMockSession(t, f.mux(), "list", "info", "F01ABC")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + trailer, got %d:\n%s", len(lines), out)
	}
	row := parseJSON(t, lines[0])
	for k, want := range map[string]any{
		"id":          "F01ABC",
		"title":       "Launch tasks",
		"description": "Everything before launch",
		"icon":        ":rocket:",
		"user":        "U01XYZ",
		"created":     float64(1709251200),
		"updated":     float64(1709337600),
		"permalink":   "https://acme.slack.com/lists/T01ABC/F01ABC",
	} {
		if row[k] != want {
			t.Errorf("%s = %v, want %v", k, row[k], want)
		}
	}
	limits, _ := row["limits"].(map[string]any)
	if limits["row_count"] != float64(53) || limits["archived_row_count"] != float64(2) || limits["row_count_limit"] != float64(5000) {
		t.Errorf("limits = %v", limits)
	}
	if _, ok := row["schema"]; ok {
		t.Error("info must not dump the schema; that is list schema's job")
	}
	if meta := metaOf(t, out); meta["has_more"] != false {
		t.Errorf("trailer = %v", meta)
	}
}

func TestListSchema(t *testing.T) {
	f := newListFixture(t)
	out, err := runWithMockSession(t, f.mux(), "list", "schema", "F01ABC")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 10 {
		t.Fatalf("expected 9 columns + trailer, got %d:\n%s", len(lines), out)
	}
	first := parseJSON(t, lines[0])
	wantFirst := `{"column_id":"Col01AAA","format":"text","is_primary_column":true,"key":"name","name":"Name","type":"text"}`
	if got, _ := json.Marshal(first); string(got) != wantFirst {
		t.Errorf("row 0 = %s, want %s", got, wantFirst)
	}
	status := parseJSON(t, lines[1])
	wantStatus := `{"choices":[{"color":"gray","label":"To do","value":"todo"},{"color":"green","label":"Done","value":"done"}],"column_id":"Col01CCC","format":"single_select","is_primary_column":false,"key":"Col01BBB","name":"Status","type":"select"}`
	if got, _ := json.Marshal(status); string(got) != wantStatus {
		t.Errorf("row 1 = %s, want %s", got, wantStatus)
	}
	owner := parseJSON(t, lines[2])
	if _, ok := owner["format"]; ok {
		t.Error("format emitted for a column with null options")
	}
	if _, ok := owner["choices"]; ok {
		t.Error("choices emitted for a non-select column")
	}
	if meta := metaOf(t, out); meta["has_more"] != false {
		t.Errorf("trailer = %v", meta)
	}
}

func TestListFind(t *testing.T) {
	var gotQuery, gotCount, gotPage string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotQuery = r.FormValue("query")
		gotCount = r.FormValue("count")
		gotPage = r.FormValue("page")
		_, _ = w.Write([]byte(`{"ok":true,"query":"x","files":{"total":2,"pagination":{},"paging":{"count":20,"total":2,"page":1,"pages":1},"matches":[
		  {"id":"F01ABC","title":"Launch tasks","name":"list","filetype":"list","user":"U01XYZ","created":1709251200,"permalink":"https://acme.slack.com/lists/T01ABC/F01ABC","size":0},
		  {"id":"F02DEF","title":"Launch tasks","name":"list","filetype":"list","user":"U02MGR","created":1709164800,"permalink":"https://acme.slack.com/lists/T01ABC/F02DEF","size":0}]}}`))
	})

	out, err := runWithMockSession(t, mux, "list", "find", "Launch tasks")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != `type:list "Launch tasks"` {
		t.Errorf("query = %q", gotQuery)
	}
	if gotCount != "20" || gotPage != "1" {
		t.Errorf("count=%q page=%q, want 20 and 1", gotCount, gotPage)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + trailer (duplicates are reported, never collapsed), got %d:\n%s", len(lines), out)
	}
	want := `{"created":1709251200,"id":"F01ABC","permalink":"https://acme.slack.com/lists/T01ABC/F01ABC","title":"Launch tasks","user":"U01XYZ"}`
	if got, _ := json.Marshal(parseJSON(t, lines[0])); string(got) != want {
		t.Errorf("row 0 = %s, want %s", got, want)
	}
	if meta := metaOf(t, out); meta["has_more"] != false {
		t.Errorf("trailer = %v", meta)
	}
}

func TestListFind_Paging(t *testing.T) {
	var pages []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		pages = append(pages, r.FormValue("page"))
		_, _ = w.Write([]byte(`{"ok":true,"files":{"paging":{"count":1,"total":2,"page":` + r.FormValue("page") + `,"pages":2},"matches":[
		  {"id":"F0` + r.FormValue("page") + `ABC","title":"Launch","user":"U01XYZ","created":1,"permalink":"p"}]}}`))
	})

	out, err := runWithMockSession(t, mux, "list", "find", "Launch", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if meta := metaOf(t, out); meta["has_more"] != true || meta["next_cursor"] != "2" {
		t.Errorf("single page trailer = %v, want has_more=true next_cursor=2", meta)
	}

	pages = nil
	out, err = runWithMockSession(t, mux, "list", "find", "Launch", "--limit", "1", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if len(nonEmptyLines(out)) != 3 || strings.Join(pages, ",") != "1,2" {
		t.Errorf("--all walked pages %v with output:\n%s", pages, out)
	}
}

func TestListFind_LocalValidation(t *testing.T) {
	for name, args := range map[string][]string{
		"blank title":        {"list", "find", "  "},
		"limit over 100":     {"list", "find", "Launch", "--limit", "101"},
		"all and cursor":     {"list", "find", "Launch", "--all", "--cursor", "2"},
		"non-numeric cursor": {"list", "find", "Launch", "--cursor", "abc"},
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { called = true })
			out, err := runWithMockSession(t, mux, args...)
			var oErr *output.Error
			if !errors.As(err, &oErr) || (oErr.Err != "invalid_input" && oErr.Err != "invalid_cursor") {
				t.Fatalf("expected a local invalid_input/invalid_cursor, got %v", err)
			}
			if called || out != "" {
				t.Errorf("local validation must make no call and print nothing; out=%q called=%v", out, called)
			}
		})
	}
}

func TestListFind_QuotesInTitleBecomeSpaces(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotQuery = r.FormValue("query")
		_, _ = w.Write([]byte(`{"ok":true,"files":{"paging":{"count":20,"total":0,"page":1,"pages":1},"matches":[]}}`))
	})
	if _, err := runWithMockSession(t, mux, "list", "find", `Say "hi" team`); err != nil {
		t.Fatal(err)
	}
	// Go's %q would send \"hi\", which Slack reads literally.
	if gotQuery != `type:list "Say  hi  team"` {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestListFind_CursorIsPageNumber(t *testing.T) {
	var gotPage string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotPage = r.FormValue("page")
		_, _ = w.Write([]byte(`{"ok":true,"files":{"paging":{"count":20,"total":60,"page":3,"pages":3},"matches":[]}}`))
	})
	out, err := runWithMockSession(t, mux, "list", "find", "Launch", "--cursor", "3")
	if err != nil {
		t.Fatal(err)
	}
	if gotPage != "3" {
		t.Errorf("page = %q, want 3", gotPage)
	}
	if meta := metaOf(t, out); meta["has_more"] != false {
		t.Errorf("last page trailer = %v", meta)
	}
}
