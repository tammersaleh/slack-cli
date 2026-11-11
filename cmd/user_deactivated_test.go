package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

// deactivatedUsersMux serves users.info, users.list, and users.profile.get
// for a deactivated U01OLD (empty real_name, as Slack returns it) who reports
// to a live U02MGR.
func deactivatedUsersMux() *http.ServeMux {
	users := map[string]map[string]any{
		"U01OLD": {"id": "U01OLD", "name": "aadams-old", "real_name": "", "deleted": true,
			"profile": map[string]any{"display_name": "Alice Adams"}},
		"U02MGR": {"id": "U02MGR", "name": "bbrown", "real_name": "Bob Brown", "deleted": false,
			"profile": map[string]any{"display_name": "Bob Brown"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		u, ok := users[r.FormValue("user")]
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "user_not_found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": u})
	})
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "members": []any{users["U01OLD"], users["U02MGR"]},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		profile := map[string]any{"real_name": "Bob Brown", "fields": map[string]any{}}
		if r.FormValue("user") == "U01OLD" {
			profile = map[string]any{"real_name": "Alice Adams", "fields": map[string]any{"Xf01AAA": profileField("Manager", "U02MGR")}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "profile": profile})
	})
	return mux
}

func assertKeys(t *testing.T, row map[string]any, want ...string) {
	t.Helper()
	if len(row) != len(want) {
		t.Errorf("row = %v, want exactly keys %v", row, want)
	}
	for _, k := range want {
		if _, ok := row[k]; !ok {
			t.Errorf("row = %v, missing %q", row, k)
		}
	}
}

// The reported confusion: --fields id,real_name on a deactivated account
// printed a blank name and nothing else. deleted: true now survives the
// projection; a live account's row is exactly what was asked for.
func TestUserInfo_DeletedSurvivesFieldsOnlyWhenTrue(t *testing.T) {
	out, err := runWithMock(t, deactivatedUsersMux(), "user", "info", "U01OLD", "U02MGR", "--fields", "id,real_name")
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2:\n%s", len(rows), out)
	}
	assertKeys(t, rows[0], "id", "real_name", "input", "deleted")
	if rows[0]["deleted"] != true {
		t.Errorf("deactivated row = %v, want deleted: true", rows[0])
	}
	assertKeys(t, rows[1], "id", "real_name", "input")
}

// manager-chain builds rows from users.profile.get, which does not report
// deactivation. The row gains deleted: true for a deactivated account, with
// or without --fields, and no key at all for a live one.
func TestManagerChain_MarksDeactivatedAccounts(t *testing.T) {
	for _, fields := range [][]string{nil, {"--fields", "id,real_name"}} {
		out, err := runWithMock(t, deactivatedUsersMux(), append([]string{"user", "manager-chain", "U01OLD"}, fields...)...)
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := rowsAndMeta(t, out)
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2:\n%s", len(rows), out)
		}
		if rows[0]["id"] != "U01OLD" || rows[0]["deleted"] != true {
			t.Errorf("fields %v: root row = %v, want deleted: true", fields, rows[0])
		}
		if _, has := rows[1]["deleted"]; has || rows[1]["id"] != "U02MGR" {
			t.Errorf("fields %v: manager row = %v, want no deleted key", fields, rows[1])
		}
		if fields != nil {
			assertKeys(t, rows[0], "id", "real_name", "input", "deleted")
			assertKeys(t, rows[1], "id", "real_name", "input")
		}
	}
}

// A rate limit that outlasts its retries on the deactivation lookup stops the
// chain like a failed profile fetch does; the marker is not silently skipped.
func TestManagerChain_DeactivationLookupRateLimitAborts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "profile": map[string]any{"real_name": "Alice Adams", "fields": map[string]any{}}})
	})
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	r := runWithMockFull(t, mux, "user", "manager-chain", "U01OLD")
	assertFatalAbort(t, r.err, r.stdout, "rate_limited", output.ExitRateLimit, 0)
}

// The rule is scoped to the two commands. user list is asked for id alone and
// prints id alone, deactivated account or not.
func TestUserList_DeletedIsNotStickyUnderFields(t *testing.T) {
	out, err := runWithMock(t, deactivatedUsersMux(), "user", "list", "--fields", "id")
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := rowsAndMeta(t, out)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2:\n%s", len(rows), out)
	}
	for _, row := range rows {
		assertKeys(t, row, "id")
	}
}
