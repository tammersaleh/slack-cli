package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestFileList_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"files": []map[string]any{
				{"id": "F01", "name": "report.pdf", "filetype": "pdf", "size": 1024},
				{"id": "F02", "name": "image.png", "filetype": "png", "size": 2048},
			},
			"paging": map[string]any{"count": 2, "total": 2, "page": 1, "pages": 1},
		})
	})

	out, err := runWithMock(t, mux, "file", "list")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 files + meta), got %d:\n%s", len(lines), out)
	}

	f := parseJSON(t, lines[0])
	if f["id"] != "F01" {
		t.Errorf("expected id='F01', got %q", f["id"])
	}
	if f["name"] != "report.pdf" {
		t.Errorf("expected name='report.pdf', got %q", f["name"])
	}
}

func TestFileList_Pagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files.list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"files": []map[string]any{
				{"id": "F01", "name": "file.pdf"},
			},
			"paging": map[string]any{"count": 1, "total": 5, "page": 1, "pages": 3},
		})
	})

	out, err := runWithMock(t, mux, "file", "list", "--limit", "1")
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

func TestFileInfo_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files.info", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fid := r.FormValue("file")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"file": map[string]any{
				"id":       fid,
				"name":     "report.pdf",
				"filetype": "pdf",
				"size":     1048576,
			},
			"comments": []any{},
			"paging":   map[string]any{"count": 0, "total": 0, "page": 1, "pages": 1},
		})
	})

	out, err := runWithMock(t, mux, "file", "info", "F01ABC")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 file + meta), got %d:\n%s", len(lines), out)
	}

	f := parseJSON(t, lines[0])
	if f["id"] != "F01ABC" {
		t.Errorf("expected id='F01ABC', got %q", f["id"])
	}
	if f["input"] != "F01ABC" {
		t.Errorf("expected input='F01ABC', got %q", f["input"])
	}
	if f["name"] != "report.pdf" {
		t.Errorf("expected name='report.pdf', got %q", f["name"])
	}
}

func TestFileInfo_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "file_not_found",
		})
	})

	out, err := runWithMock(t, mux, "file", "info", "F99MISSING")
	if err == nil {
		t.Fatal("expected error for not found file")
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (error + meta), got %d:\n%s", len(lines), out)
	}

	errItem := parseJSON(t, lines[0])
	if errItem["error"] != "file_not_found" {
		t.Errorf("expected error='file_not_found', got %q", errItem["error"])
	}
	if errItem["input"] != "F99MISSING" {
		t.Errorf("expected input='F99MISSING', got %q", errItem["input"])
	}
}
