package cmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// collidingDirectory is a users.list page where "Alice Adams" is the display
// name of two active accounts, "Bob Brown" of one active and one deactivated
// account, and "alice" is a third account's handle.
func collidingDirectory(w http.ResponseWriter, _ *http.Request) {
	member := func(id, handle, display, title string, deleted bool) map[string]any {
		return map[string]any{
			"id": id, "name": handle, "real_name": display, "deleted": deleted,
			"profile": map[string]any{"display_name": display, "title": title},
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"members": []any{
			member("U02MGR", "aadams2", "Alice Adams", "", false),
			member("U01XYZ", "aadams", "Alice Adams", "Staff Engineer", false),
			member("U03OLD", "bbrown-old", "Bob Brown", "Engineer", true),
			member("U04NEW", "bbrown", "Bob Brown", "Engineer", false),
			member("U05ALI", "alice", "Ali", "", false),
		},
		"response_metadata": map[string]string{"next_cursor": ""},
	})
}

// wantAliceCandidates asserts the two "Alice Adams" accounts, in ID order,
// each with handle, title, and ID.
func wantAliceCandidates(t *testing.T, got any) {
	t.Helper()
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"id":"U01XYZ","name":"aadams","title":"Staff Engineer"},{"id":"U02MGR","name":"aadams2","title":""}]`
	if string(raw) != want {
		t.Errorf("candidates = %s\nwant         %s", raw, want)
	}
}

// A name two active users hold is that input's row, with the candidates, in
// every command that prints one row per user. Nothing is asked of Slack for
// it beyond the directory.
func TestUserRows_AmbiguousNameIsThatInputsRow(t *testing.T) {
	for _, args := range userRowCommands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var others atomic.Int64
			mux := userDirectory(collidingDirectory, &others)

			r := runWithMockFull(t, mux, append(args, "@Alice Adams")...)
			assertExitError(t, r.err)
			rows, meta := rowsAndMeta(t, r.stdout)
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want 1:\n%s", len(rows), r.stdout)
			}
			row := rows[0]
			if row["error"] != "ambiguous_user" || row["input"] != "@Alice Adams" {
				t.Errorf("row = %v, want ambiguous_user for @Alice Adams", row)
			}
			wantAliceCandidates(t, row["candidates"])
			if hint, _ := row["hint"].(string); !strings.Contains(hint, "@handle") {
				t.Errorf("hint = %q, want it to name the handle and ID forms", hint)
			}
			if meta["error_count"] != float64(1) {
				t.Errorf("_meta.error_count = %v, want 1", meta["error_count"])
			}
			if got := others.Load(); got != 0 {
				t.Errorf("requests besides users.list = %d, want 0: nothing was picked", got)
			}
		})
	}
}

// An ambiguous input keeps its place and the inputs around it still get their
// rows. Every per-ID call fails here, so each row carries its input.
func TestUserRows_AmbiguousNameKeepsItsPlaceBetweenOtherInputs(t *testing.T) {
	for _, args := range userRowCommands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var others atomic.Int64
			mux := userDirectory(collidingDirectory, &others)

			r := runWithMockFull(t, mux, append(args, "@nobody", "@Alice Adams", "@Bob Brown")...)
			assertExitError(t, r.err)
			rows, _ := rowsAndMeta(t, r.stdout)
			if len(rows) != 3 {
				t.Fatalf("rows = %d, want 3:\n%s", len(rows), r.stdout)
			}
			if rows[0]["input"] != "@nobody" || rows[0]["error"] != "user_not_found" {
				t.Errorf("row 0 = %v, want user_not_found for @nobody", rows[0])
			}
			if rows[1]["input"] != "@Alice Adams" || rows[1]["error"] != "ambiguous_user" {
				t.Errorf("row 1 = %v, want ambiguous_user for @Alice Adams", rows[1])
			}
			if rows[2]["input"] != "@Bob Brown" || rows[2]["error"] == "ambiguous_user" || rows[2]["error"] == "user_not_found" {
				t.Errorf("row 2 = %v, want @Bob Brown resolved and looked up", rows[2])
			}
		})
	}
}

// --fields cannot strip the candidates: they are the point of the row.
func TestUserInfo_AmbiguousRowKeepsCandidatesUnderFields(t *testing.T) {
	var others atomic.Int64
	r := runWithMockFull(t, userDirectory(collidingDirectory, &others), "user", "info", "@Alice Adams", "--fields", "id,real_name")
	assertExitError(t, r.err)
	rows, _ := rowsAndMeta(t, r.stdout)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1:\n%s", len(rows), r.stdout)
	}
	wantAliceCandidates(t, rows[0]["candidates"])
	if _, has := rows[0]["hint"]; has {
		t.Errorf("hint was not requested and is not a marker: %v", rows[0])
	}
}

// The reported bug: a display name shared with a deactivated account returned
// either account, a different one per run. Each run here builds a new
// resolver from the directory, so a map-order winner would show up.
func TestUserInfo_SharedNameResolvesToTheActiveAccountEveryRun(t *testing.T) {
	for range 20 {
		var others atomic.Int64
		out, err := runWithMock(t, userDirectory(collidingDirectory, &others), "user", "info", "@Bob Brown", "@bbrown-old", "--fields", "id,name")
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := rowsAndMeta(t, out)
		if len(rows) != 2 || rows[0]["id"] != "U04NEW" || rows[1]["id"] != "U03OLD" {
			t.Fatalf("rows = %v, want the active account for the name and the deactivated one for its handle", rows)
		}
	}
}

// Commands that act on the resolved user stop before any other request, with
// the candidates on the error.
func TestUserName_AmbiguousNameStopsBeforeAnyOtherCall(t *testing.T) {
	tests := []struct {
		name    string
		session bool
		stdin   string
		args    []string
		mux     func(t *testing.T) *http.ServeMux
	}{
		{name: "channel invite", args: []string{"channel", "invite", "C01ABC", "@Alice Adams"}},
		{name: "channel kick", args: []string{"channel", "kick", "C01ABC", "U04NEW", "@Alice Adams"}},
		{name: "file list --user", args: []string{"file", "list", "--user", "@Alice Adams"}},
		{name: "search messages", args: []string{"search", "messages", "from:@Alice Adams deploy"}},
		{name: "search messages quoted", args: []string{"search", "messages", `from:"Alice Adams" deploy`}},
		{name: "search files", args: []string{"search", "files", "from:@Alice Adams deploy"}},
		{name: "draft create", session: true, stdin: richTextBlocksJSON("hi"), args: []string{"draft", "create", "@nobody", "@Alice Adams"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SLACK_USER_TOKEN", "xoxp-test") // search needs one
			var others atomic.Int64
			mux := userDirectory(collidingDirectory, &others)

			var err error
			if tt.session {
				_, err = runWithMockSessionStdin(t, tt.stdin, mux, tt.args...)
			} else {
				_, err = runWithMock(t, mux, tt.args...)
			}
			oErr := assertOutputError(t, err, "ambiguous_user", output.ExitGeneral)
			wantAliceCandidates(t, oErr.Candidates)
			for _, want := range []string{"@aadams (U01XYZ, Staff Engineer)", "@aadams2 (U02MGR)"} {
				if !strings.Contains(oErr.Detail, want) {
					t.Errorf("detail = %q, want it to list %q", oErr.Detail, want)
				}
			}
			if got := others.Load(); got != 0 {
				t.Errorf("requests besides users.list = %d, want 0", got)
			}
		})
	}
}

// list record resolves a user cell before it writes. An ambiguous name
// creates no record.
func TestListRecordAdd_AmbiguousUserWritesNothing(t *testing.T) {
	s := newRecordStore(t)
	mux := s.mux()
	mux.HandleFunc("/api/users.list", collidingDirectory)

	_, err := runWithMockSession(t, mux, "list", "record", "add", "F01ABC", "Owner=@Alice Adams")
	oErr := assertOutputError(t, err, "ambiguous_user", output.ExitGeneral)
	wantAliceCandidates(t, oErr.Candidates)
	if got := fmt.Sprint(s.methods()); got != "[files.info]" {
		t.Errorf("calls = %s, want the schema read alone", got)
	}
}
