package cmd_test

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	mux := emptyMux()
	out, err := runWithMock(t, mux, "version")
	if err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (version + meta), got %d:\n%s", len(lines), out)
	}

	item := parseJSON(t, lines[0])
	if item["version"] == nil || item["version"] == "" {
		t.Error("expected non-empty version")
	}
	if !strings.Contains(lines[1], "_meta") {
		t.Error("expected _meta trailer")
	}
}
