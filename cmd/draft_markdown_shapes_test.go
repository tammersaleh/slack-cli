package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tammersaleh/slack-cli/internal/mdblocks"
)

// Every converter golden must clear the same gate hand-written JSON does.
// This is a backstop: the goldens' exact-JSON assertions carry correctness,
// this pins that the converter never emits a shape the CLI would then reject
// as invalid_blocks (which, after a successful conversion, means a converter
// defect).
func TestMarkdownGoldensPassValidateBlockShapes(t *testing.T) {
	inputs, err := filepath.Glob("../internal/mdblocks/testdata/*.md")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no goldens found: %v", err)
	}
	for _, in := range inputs {
		src, _ := os.ReadFile(in)
		blocks, err := mdblocks.Convert(src)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		hasBody, err := validateBlockShapes(blocks)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(in), err)
		}
		if !hasBody {
			t.Errorf("%s: converter output has no rich_text body", filepath.Base(in))
		}
	}
}
