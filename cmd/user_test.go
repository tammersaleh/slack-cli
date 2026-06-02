package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

func TestUserList_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"members": []map[string]any{
				{"id": "U01", "name": "tammer", "real_name": "Tammer Saleh", "profile": map[string]any{"email": "tammer@example.com"}},
				{"id": "U02", "name": "alice", "real_name": "Alice Smith", "profile": map[string]any{"email": "alice@example.com"}},
			},
			"response_metadata": map[string]string{"next_cursor": ""},
		})
	})

	out, err := runWithMock(t, mux, "user", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 users + meta), got %d:\n%s", len(lines), out)
	}

	user := parseJSON(t, lines[0])
	if user["name"] != "tammer" {
		t.Errorf("expected name='tammer', got %q", user["name"])
	}
}

func TestUserList_QueryFilter(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"bare name", "tammer"},
		{"with @ prefix", "@tammer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/users.list", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": true,
					"members": []map[string]any{
						{"id": "U01", "name": "tammer", "real_name": "Tammer Saleh", "profile": map[string]any{"email": "tammer@example.com"}},
						{"id": "U02", "name": "alice", "real_name": "Alice Smith", "profile": map[string]any{"email": "alice@example.com"}},
					},
					"response_metadata": map[string]string{"next_cursor": ""},
				})
			})

			out, err := runWithMock(t, mux, "user", "list", "--query", tt.query)
			if err != nil {
				t.Fatal(err)
			}

			lines := nonEmptyLines(out)
			if len(lines) != 2 {
				t.Fatalf("expected 2 lines (1 user + meta), got %d:\n%s", len(lines), out)
			}
			u := parseJSON(t, lines[0])
			if u["name"] != "tammer" {
				t.Errorf("expected name='tammer', got %q", u["name"])
			}
		})
	}
}

func TestUserInfo_MockAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"user": map[string]any{
				"id": "U01", "name": "tammer", "real_name": "Tammer Saleh",
				"profile": map[string]any{"email": "tammer@example.com"},
			},
		})
	})

	out, err := runWithMock(t, mux, "user", "info", "U01")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 user + meta), got %d:\n%s", len(lines), out)
	}

	user := parseJSON(t, lines[0])
	if user["input"] != "U01" {
		t.Errorf("expected input='U01', got %q", user["input"])
	}
	if user["name"] != "tammer" {
		t.Errorf("expected name='tammer', got %q", user["name"])
	}
}

func TestUserInfo_ByEmail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.lookupByEmail", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"user": map[string]any{"id": "U01", "name": "tammer"},
		})
	})
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"user": map[string]any{"id": "U01", "name": "tammer", "real_name": "Tammer Saleh"},
		})
	})

	out, err := runWithMock(t, mux, "user", "info", "tammer@example.com")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	user := parseJSON(t, lines[0])
	if user["input"] != "tammer@example.com" {
		t.Errorf("expected input='tammer@example.com', got %q", user["input"])
	}
}

func TestUserInfo_PartialFailure_NoStderr(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		uid := r.FormValue("user")
		if uid == "U01" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"user": map[string]any{"id": "U01", "name": "tammer"},
			})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "user_not_found",
			})
		}
	})

	r := runWithMockFull(t, mux, "user", "info", "U01", "U99INVALID")
	if r.err == nil {
		t.Fatal("expected error for partial failure")
	}
	var oErr *output.Error
	if errors.As(r.err, &oErr) {
		t.Errorf("partial failure should not return *output.Error (would be printed to stderr), got: %v", r.err)
	}
}

func TestUserInfo_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "user_not_found",
		})
	})

	_, err := runWithMock(t, mux, "user", "info", "U99INVALID")
	if err == nil {
		t.Fatal("expected error for not found user")
	}
}

// profileField is the {value,alt,label} shape Slack returns per custom field.
func profileField(label, value string) map[string]any {
	return map[string]any{"value": value, "alt": "", "label": label}
}

func TestUserInfo_Full(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		uid := r.FormValue("user")
		names := map[string]string{"U01": "tammer", "U02": "jjones"}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"user": map[string]any{
				"id": uid, "name": names[uid], "real_name": names[uid],
				"profile": map[string]any{"real_name": names[uid]},
			},
		})
	})
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("user") != "U01" {
			// Manager lookup for value_name resolves via users.info, not here.
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "user_not_found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"profile": map[string]any{
				"real_name": "Tammer Saleh", "title": "Engineer",
				"fields": map[string]any{
					"Xf01": profileField("Manager", "U02"),
					"Xf02": profileField("Division", "Technology"),
					"Xf03": profileField("Employee ID", "445"),
				},
			},
		})
	})

	out, err := runWithMock(t, mux, "user", "info", "--full", "U01")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 user + meta), got %d:\n%s", len(lines), out)
	}
	user := parseJSON(t, lines[0])

	// Base user fields stay intact.
	if user["name"] != "tammer" {
		t.Errorf("expected base name='tammer', got %q", user["name"])
	}

	cf, ok := user["custom_fields"].(map[string]any)
	if !ok {
		t.Fatalf("expected custom_fields object, got %T: %v", user["custom_fields"], user["custom_fields"])
	}
	mgr, ok := cf["manager"].(map[string]any)
	if !ok {
		t.Fatalf("expected custom_fields.manager, got %v", cf)
	}
	if mgr["value"] != "U02" {
		t.Errorf("expected manager value U02, got %q", mgr["value"])
	}
	if mgr["value_name"] != "jjones" {
		t.Errorf("expected manager value_name resolved to jjones, got %q", mgr["value_name"])
	}
	if cf["division"].(map[string]any)["value"] != "Technology" {
		t.Errorf("expected division Technology, got %v", cf["division"])
	}
}

// A systemic profile failure (missing scope) must fail fast as a fatal
// *output.Error, not spam a per-input error row.
func TestUserInfo_Full_SystemicMissingScope(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"user": map[string]any{"id": "U01", "name": "tammer"},
		})
	})
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing_scope"})
	})

	r := runWithMockFull(t, mux, "user", "info", "--full", "U01")
	if r.err == nil {
		t.Fatal("expected fatal error for missing_scope")
	}
	var oErr *output.Error
	if !errors.As(r.err, &oErr) {
		t.Fatalf("expected *output.Error (systemic, printed to stderr), got %T: %v", r.err, r.err)
	}
}

func TestManagerChain_WalksUp(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		chain := map[string]struct{ real, title, mgr string }{
			"U01": {"Alice", "AE", "U02"},
			"U02": {"Jon Jones", "CRO", "U03"},
			"U03": {"Michael Intrator", "CEO", ""},
		}
		c := chain[r.FormValue("user")]
		fields := map[string]any{}
		if c.mgr != "" {
			fields["Xf01"] = profileField("Manager", c.mgr)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"profile": map[string]any{"real_name": c.real, "title": c.title, "fields": fields},
		})
	})

	out, err := runWithMock(t, mux, "user", "manager-chain", "U01")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (3 levels + meta), got %d:\n%s", len(lines), out)
	}
	l0 := parseJSON(t, lines[0])
	if l0["level"].(float64) != 0 || l0["id"] != "U01" || l0["title"] != "AE" {
		t.Errorf("level 0 wrong: %v", l0)
	}
	if l0["manager_id"] != "U02" || l0["manager_name"] != "Jon Jones" {
		t.Errorf("level 0 manager wrong: %v", l0)
	}
	if _, hasUserID := l0["user_id"]; hasUserID {
		t.Errorf("row must use id, not user_id (avoids printer re-enrichment): %v", l0)
	}
	l1 := parseJSON(t, lines[1])
	if l1["level"].(float64) != 1 || l1["id"] != "U02" || l1["manager_id"] != "U03" {
		t.Errorf("level 1 wrong: %v", l1)
	}
	if l1["manager_name"] != "Michael Intrator" {
		t.Errorf("level 1 manager_name should come from the terminal hop's profile, got %v", l1["manager_name"])
	}
	l2 := parseJSON(t, lines[2])
	if l2["id"] != "U03" || l2["stop_reason"] != "no_manager" {
		t.Errorf("terminal row wrong: %v", l2)
	}
	if _, hasMgr := l2["manager_id"]; hasMgr {
		t.Errorf("terminal row should have no manager_id: %v", l2)
	}
}

func TestManagerChain_Cycle(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mgr := map[string]string{"U01": "U02", "U02": "U01"}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"profile": map[string]any{
				"real_name": r.FormValue("user"),
				"fields":    map[string]any{"Xf01": profileField("Manager", mgr[r.FormValue("user")])},
			},
		})
	})

	r := runWithMockFull(t, mux, "user", "manager-chain", "U01")
	if r.err == nil {
		t.Fatal("expected nonzero exit for cycle")
	}
	lines := nonEmptyLines(r.stdout)
	last := parseJSON(t, lines[len(lines)-2]) // last data row before meta
	if last["stop_reason"] != "cycle_detected" {
		t.Errorf("expected cycle_detected, got %v", last["stop_reason"])
	}
	meta := parseJSON(t, lines[len(lines)-1])
	m := meta["_meta"].(map[string]any)
	if m["error_count"].(float64) != 1 {
		t.Errorf("expected error_count 1, got %v", m["error_count"])
	}
}

func TestManagerChain_AmbiguousField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"profile": map[string]any{
				"real_name": "Alice",
				"fields": map[string]any{
					"Xf01": profileField("Manager", "U02"),
					"Xf02": profileField("Manager", "U03"),
				},
			},
		})
	})

	r := runWithMockFull(t, mux, "user", "manager-chain", "U01")
	if r.err == nil {
		t.Fatal("expected nonzero exit for ambiguous manager field")
	}
	lines := nonEmptyLines(r.stdout)
	row := parseJSON(t, lines[0])
	if row["stop_reason"] != "ambiguous_manager_field" {
		t.Errorf("expected ambiguous_manager_field, got %v", row["stop_reason"])
	}
}

func TestManagerChain_InvalidManagerValue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"profile": map[string]any{
				"real_name": "Alice",
				"fields":    map[string]any{"Xf01": profileField("Manager", "not-a-user-id")},
			},
		})
	})

	r := runWithMockFull(t, mux, "user", "manager-chain", "U01")
	if r.err == nil {
		t.Fatal("expected nonzero exit for invalid manager value")
	}
	row := parseJSON(t, nonEmptyLines(r.stdout)[0])
	if row["stop_reason"] != "invalid_manager_value" {
		t.Errorf("expected invalid_manager_value, got %v", row["stop_reason"])
	}
}

// An unbounded chain must terminate at the depth cap (20 rows: levels 0-19),
// with the last row marked max_depth.
func TestManagerChain_MaxDepth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// Every user U<n> reports to U<n+1>, forever.
		cur := r.FormValue("user")
		n, _ := strconv.Atoi(strings.TrimPrefix(cur, "U"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"profile": map[string]any{
				"real_name": cur,
				"fields":    map[string]any{"Xf01": profileField("Manager", "U"+strconv.Itoa(n+1))},
			},
		})
	})

	r := runWithMockFull(t, mux, "user", "manager-chain", "U0")
	if r.err == nil {
		t.Fatal("expected nonzero exit for max_depth")
	}
	lines := nonEmptyLines(r.stdout)
	dataRows := lines[:len(lines)-1] // drop _meta
	if len(dataRows) != 20 {
		t.Fatalf("expected 20 rows at the depth cap, got %d", len(dataRows))
	}
	last := parseJSON(t, dataRows[len(dataRows)-1])
	if last["level"].(float64) != 19 {
		t.Errorf("expected terminal level 19, got %v", last["level"])
	}
	if last["stop_reason"] != "max_depth" {
		t.Errorf("expected stop_reason max_depth, got %v", last["stop_reason"])
	}
}

// On --full, a non-systemic profile failure for one input emits a per-item
// error row (no base row for that user) and does not abort the command.
func TestUserInfo_Full_PerItemFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		uid := r.FormValue("user")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"user": map[string]any{"id": uid, "name": "u" + uid},
		})
	})
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("user") == "U01" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":      true,
				"profile": map[string]any{"real_name": "One", "fields": map[string]any{}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "user_not_found"})
	})

	r := runWithMockFull(t, mux, "user", "info", "--full", "U01", "U02")
	if r.err == nil {
		t.Fatal("expected nonzero exit for partial failure")
	}
	var oErr *output.Error
	if errors.As(r.err, &oErr) {
		t.Errorf("per-item failure must be ExitError, not *output.Error (stderr), got %v", r.err)
	}

	lines := nonEmptyLines(r.stdout)
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d:\n%s", len(lines), r.stdout)
	}
	row0 := parseJSON(t, lines[0])
	if row0["input"] != "U01" || row0["name"] != "uU01" {
		t.Errorf("expected U01 base row, got %v", row0)
	}
	row1 := parseJSON(t, lines[1])
	// The failing input gets an error row, NOT a base user row.
	if row1["error"] == nil {
		t.Errorf("expected error row for U02, got %v", row1)
	}
	if _, hasName := row1["name"]; hasName {
		t.Errorf("failing input must not emit a base user row: %v", row1)
	}
	meta := parseJSON(t, lines[2])["_meta"].(map[string]any)
	if meta["error_count"].(float64) != 1 {
		t.Errorf("expected error_count 1, got %v", meta["error_count"])
	}
}

// A manager shared across multiple inputs is fetched once (command-local cache).
func TestManagerChain_SharedManagerCache(t *testing.T) {
	mux := http.NewServeMux()
	calls := map[string]int{}
	var mu sync.Mutex
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		cur := r.FormValue("user")
		mu.Lock()
		calls[cur]++
		mu.Unlock()
		// U01 and U02 both report to U99 (shared manager); U99 has none.
		mgr := map[string]string{"U01": "U99", "U02": "U99", "U99": ""}
		fields := map[string]any{}
		if m := mgr[cur]; m != "" {
			fields["Xf01"] = profileField("Manager", m)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"profile": map[string]any{"real_name": cur, "fields": fields},
		})
	})

	out, err := runWithMock(t, mux, "user", "manager-chain", "U01", "U02")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["U99"] != 1 {
		t.Errorf("shared manager U99 should be fetched once, got %d calls", calls["U99"])
	}
	// Both chains resolved the shared manager's name.
	for _, ln := range nonEmptyLines(out) {
		row := parseJSON(t, ln)
		if row["manager_id"] == "U99" && row["manager_name"] != "U99" {
			t.Errorf("expected manager_name U99 from cached profile, got %v", row["manager_name"])
		}
	}
}

// --manager-field overrides the label used to find the manager reference.
func TestManagerChain_CustomManagerField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.profile.get", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mgr := map[string]string{"U01": "U02", "U02": ""}
		fields := map[string]any{}
		if m := mgr[r.FormValue("user")]; m != "" {
			fields["Xf01"] = profileField("Reports To", m)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"profile": map[string]any{"real_name": r.FormValue("user"), "fields": fields},
		})
	})

	out, err := runWithMock(t, mux, "user", "manager-chain", "--manager-field", "Reports To", "U01")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 2 levels + meta, got %d:\n%s", len(lines), out)
	}
	if parseJSON(t, lines[0])["manager_id"] != "U02" {
		t.Errorf("expected manager_id U02 via custom field, got %v", lines[0])
	}
}
