package cmd_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
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

func fileDownloadMux(t *testing.T, fileContent string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"file": map[string]any{
				"id":                   "F01ABC",
				"name":                 "report.pdf",
				"size":                 len(fileContent),
				"url_private_download": "http://" + r.Host + "/download/report.pdf",
			},
			"comments": []any{},
			"paging":   map[string]any{"count": 0, "total": 0, "page": 1, "pages": 1},
		})
	})
	mux.HandleFunc("/download/report.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte(fileContent))
	})
	return mux
}

func TestFileDownload_ToDisk(t *testing.T) {
	content := "fake-pdf-content"
	mux := fileDownloadMux(t, content)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("SLACK_TOKEN", "xoxb-test")
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "downloaded.pdf")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer

	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}

	kctx, err := parser.Parse([]string{"file", "download", "F01ABC", "-o", outPath})
	if err != nil {
		t.Fatal(err)
	}

	cli.SetOutput(&outBuf, &errBuf)
	if err := kctx.Run(&cli); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected content %q, got %q", content, string(data))
	}

	lines := nonEmptyLines(outBuf.String())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (result + meta), got %d:\n%s", len(lines), outBuf.String())
	}
	item := parseJSON(t, lines[0])
	if item["path"] != outPath {
		t.Errorf("expected path=%q, got %q", outPath, item["path"])
	}
}

func TestFileDownload_ToStdout(t *testing.T) {
	content := "stdout-content"
	mux := fileDownloadMux(t, content)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("SLACK_TOKEN", "xoxb-test")
	t.Setenv("SLACK_API_URL", srv.URL+"/api/")

	var cli cmd.CLI
	var outBuf, errBuf bytes.Buffer

	parser, err := kong.New(&cli, kong.Name("slack"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}

	kctx, err := parser.Parse([]string{"file", "download", "F01ABC", "-o", "-"})
	if err != nil {
		t.Fatal(err)
	}

	cli.SetOutput(&outBuf, &errBuf)
	if err := kctx.Run(&cli); err != nil {
		t.Fatal(err)
	}

	if outBuf.String() != content {
		t.Errorf("expected stdout content %q, got %q", content, outBuf.String())
	}
}

func TestFileDownload_NoDownloadURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files.info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"file": map[string]any{
				"id":   "F01ABC",
				"name": "snippet.txt",
			},
			"comments": []any{},
			"paging":   map[string]any{"count": 0, "total": 0, "page": 1, "pages": 1},
		})
	})

	_, err := runWithMock(t, mux, "file", "download", "F01ABC")
	if err == nil {
		t.Fatal("expected error for no download URL")
	}
}
