package api

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestHTTP2LegacyImplementation guards the http2legacy build tag. The tag only
// works while golang.org/x/net/http2 still ships its original implementation,
// and a tag that matches no file is not an error.
func TestHTTP2LegacyImplementation(t *testing.T) {
	const help = "The build no longer uses x/net's own HTTP/2 implementation, so Slack sees a different HTTP/2 client. " +
		"See the http2legacy paragraphs under Testing in CLAUDE.md."

	if !builtWithHTTP2Legacy {
		t.Fatalf("tests were built without -tags http2legacy. Run them with mise run test. %s", help)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH, cannot list the files golang.org/x/net/http2 compiles")
	}
	out, err := exec.Command(goBin, "list", "-tags=http2legacy", "-f", `{{join .GoFiles "\n"}}`, "golang.org/x/net/http2").CombinedOutput()
	if err != nil {
		t.Fatalf("go list golang.org/x/net/http2: %v\n%s", err, out)
	}
	files := strings.Fields(string(out))
	if !slices.Contains(files, "transport.go") || slices.Contains(files, "transport_wrap.go") {
		t.Fatalf("with -tags http2legacy, golang.org/x/net/http2 compiles %v, want transport.go and not transport_wrap.go. %s", files, help)
	}
}
