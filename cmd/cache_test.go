package cmd_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheInfo_Empty(t *testing.T) {
	// With no cache files, should emit just _meta.
	t.Setenv("HOME", t.TempDir())

	mux := emptyMux()
	out, err := runWithMock(t, mux, "cache", "info")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (meta only), got %d:\n%s", len(lines), out)
	}
}

func TestCacheClear(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a fake cache file.
	cacheDir := filepath.Join(home, "Library", "Application Support", "slack-cli", "cache")
	_ = os.MkdirAll(cacheDir, 0700)
	_ = os.WriteFile(filepath.Join(cacheDir, "channels-T123.json"), []byte(`{}`), 0600)

	mux := emptyMux()
	out, err := runWithMock(t, mux, "cache", "clear")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["removed"] != float64(1) {
		t.Errorf("expected removed=1, got %v", item["removed"])
	}
}
