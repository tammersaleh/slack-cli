package cmd_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/output"
)

func searchFilesResponse(matches []map[string]any, page, pageCount, total int) map[string]any {
	return map[string]any{
		"ok": true,
		"files": map[string]any{
			"matches": matches,
			"paging": map[string]any{
				"count": len(matches),
				"total": total,
				"page":  page,
				"pages": pageCount,
			},
			"total": total,
		},
	}
}

func fileMatch(id, name, title, filetype, permalink string) map[string]any {
	return map[string]any{
		"id":        id,
		"name":      name,
		"title":     title,
		"filetype":  filetype,
		"permalink": permalink,
	}
}

func TestSearchFiles_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if q := r.Form.Get("query"); q != "quarterly report" {
			t.Errorf("expected query 'quarterly report', got %q", q)
		}
		_ = json.NewEncoder(w).Encode(searchFilesResponse(
			[]map[string]any{
				fileMatch("F01ABC", "Q1-report.pdf", "Q1 Quarterly Report", "pdf", "https://acme.slack.com/files/U01/F01ABC/q1.pdf"),
				fileMatch("F02DEF", "Q4-report.xlsx", "Q4 Quarterly Report", "xlsx", "https://acme.slack.com/files/U02/F02DEF/q4.xlsx"),
			},
			1, 1, 2,
		))
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "files", "quarterly report")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 files + meta), got %d:\n%s", len(lines), out)
	}

	f := parseJSON(t, lines[0])
	if f["id"] != "F01ABC" {
		t.Errorf("expected id='F01ABC', got %q", f["id"])
	}
	if f["name"] != "Q1-report.pdf" {
		t.Errorf("expected name='Q1-report.pdf', got %q", f["name"])
	}
}

func TestSearchFiles_Pagination(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		_ = r.ParseForm()
		page := r.Form.Get("page")

		if page == "" || page == "1" {
			_ = json.NewEncoder(w).Encode(searchFilesResponse(
				[]map[string]any{fileMatch("F01", "page1.pdf", "Page 1", "pdf", "https://acme.slack.com/files/F01")},
				1, 2, 2,
			))
		} else if page == "2" {
			_ = json.NewEncoder(w).Encode(searchFilesResponse(
				[]map[string]any{fileMatch("F02", "page2.pdf", "Page 2", "pdf", "https://acme.slack.com/files/F02")},
				2, 2, 2,
			))
		} else {
			t.Fatalf("unexpected page %q", page)
		}
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")

	// First page should show has_more=true with a cursor.
	out, err := runWithMock(t, mux, "search", "files", "test", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	meta := parseJSON(t, lines[len(lines)-1])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != true {
		t.Error("expected has_more=true on first page")
	}
	cursor := m["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("expected non-empty cursor")
	}

	// Second page using the cursor.
	out2, err := runWithMock(t, mux, "search", "files", "test", "--cursor", cursor)
	if err != nil {
		t.Fatal(err)
	}
	lines2 := nonEmptyLines(out2)
	meta2 := parseJSON(t, lines2[len(lines2)-1])
	m2 := meta2["_meta"].(map[string]any)
	if m2["has_more"] != false {
		t.Error("expected has_more=false on last page")
	}

	f := parseJSON(t, lines2[0])
	if f["id"] != "F02" {
		t.Errorf("expected page 2 file F02, got %q", f["id"])
	}
}

func TestSearchFiles_All(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		page := r.Form.Get("page")
		if page == "" || page == "1" {
			_ = json.NewEncoder(w).Encode(searchFilesResponse(
				[]map[string]any{fileMatch("F01", "a.pdf", "A", "pdf", "")},
				1, 2, 2,
			))
		} else {
			_ = json.NewEncoder(w).Encode(searchFilesResponse(
				[]map[string]any{fileMatch("F02", "b.pdf", "B", "pdf", "")},
				2, 2, 2,
			))
		}
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "files", "test", "--all")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 files + meta), got %d:\n%s", len(lines), out)
	}

	meta := parseJSON(t, lines[2])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != false {
		t.Error("expected has_more=false after --all")
	}
}

func TestSearchFiles_MissingUserToken(t *testing.T) {
	mux := http.NewServeMux()
	t.Setenv("SLACK_USER_TOKEN", "")

	_, err := runWithMock(t, mux, "search", "files", "test query")
	if err == nil {
		t.Fatal("expected error for missing user token")
	}

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T", err)
	}
	if oErr.Err != "missing_user_token" {
		t.Errorf("expected error='missing_user_token', got %q", oErr.Err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("expected exit code %d, got %d", output.ExitAuth, oErr.Code)
	}
}

func TestSearchFiles_LimitExceeded(t *testing.T) {
	mux := http.NewServeMux()
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	_, err := runWithMock(t, mux, "search", "files", "test", "--limit", "150")
	if err == nil {
		t.Fatal("expected error for limit > 100")
	}

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T", err)
	}
	if oErr.Err != "invalid_input" {
		t.Errorf("expected error='invalid_input', got %q", oErr.Err)
	}
}

func TestSearchFiles_AllAndCursorMutuallyExclusive(t *testing.T) {
	mux := http.NewServeMux()
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	_, err := runWithMock(t, mux, "search", "files", "test", "--all", "--cursor", "abc")
	if err == nil {
		t.Fatal("expected error for --all and --cursor together")
	}
}

func TestSearchFiles_NotAuthed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "not_authed",
		})
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-expired")
	_, err := runWithMock(t, mux, "search", "files", "test query")
	if err == nil {
		t.Fatal("expected error for not_authed")
	}

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T", err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("expected exit code %d, got %d", output.ExitAuth, oErr.Code)
	}
}
