package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search.files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(searchFilesResponse(
			[]map[string]any{
				fileMatch("F01ABC", "file.pdf", "File", "pdf", "https://acme.slack.com/files/F01"),
			},
			1, 3, 5,
		))
	})

	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	out, err := runWithMock(t, mux, "search", "files", "test", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	meta := parseJSON(t, lines[len(lines)-1])
	m := meta["_meta"].(map[string]any)
	if m["has_more"] != true {
		t.Error("expected has_more=true")
	}
	if m["next_cursor"] == nil || m["next_cursor"] == "" {
		t.Error("expected non-empty next_cursor")
	}
}

func TestSearchFiles_MissingUserToken(t *testing.T) {
	mux := http.NewServeMux()
	t.Setenv("SLACK_USER_TOKEN", "")

	_, err := runWithMock(t, mux, "search", "files", "test query")
	if err == nil {
		t.Fatal("expected error for missing user token")
	}
}

func TestSearchFiles_LimitExceeded(t *testing.T) {
	mux := http.NewServeMux()
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	_, err := runWithMock(t, mux, "search", "files", "test", "--limit", "150")
	if err == nil {
		t.Fatal("expected error for limit > 100")
	}
}
