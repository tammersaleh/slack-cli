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
	// Respect XDG_CONFIG_HOME on Linux runners (otherwise UserConfigDir
	// ignores HOME and looks at the real $XDG_CONFIG_HOME).
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// Create a fake cache file in the per-OS config dir that production code
	// resolves via os.UserConfigDir.
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	cacheDir := filepath.Join(configDir, "slack-cli", "cache")
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
