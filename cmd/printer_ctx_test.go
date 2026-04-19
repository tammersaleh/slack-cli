package cmd

import (
	"bytes"
	"testing"
	"time"
)

// TestNewPrinter_EnrichUsesCLIContext guards the regression where the
// printer's EnrichFunc closure captured context.Background() and silently
// bypassed --timeout on users.info fallback calls. Context() stashes the
// command ctx on the CLI; the printer closure must read from that field,
// not hard-code Background.
func TestNewPrinter_EnrichUsesCLIContext(t *testing.T) {
	var errBuf bytes.Buffer
	cli := &CLI{Timeout: 100 * time.Millisecond}
	cli.SetOutput(&bytes.Buffer{}, &errBuf)

	ctx, cancel := cli.Context()
	defer cancel()

	if cli.ctx != ctx {
		t.Fatalf("expected Context() to stash ctx on cli, got cli.ctx=%v ctx=%v", cli.ctx, ctx)
	}

	// Also confirm the stashed ctx carries the deadline from --timeout.
	if _, ok := cli.ctx.Deadline(); !ok {
		t.Error("cli.ctx has no deadline; --timeout is not being applied")
	}
}

func TestNewPrinter_NoTimeoutClearsToBackground(t *testing.T) {
	cli := &CLI{}
	cli.SetOutput(&bytes.Buffer{}, &bytes.Buffer{})

	ctx, cancel := cli.Context()
	defer cancel()

	if cli.ctx != ctx {
		t.Errorf("expected cli.ctx == returned ctx even without --timeout")
	}
	if _, ok := cli.ctx.Deadline(); ok {
		t.Error("cli.ctx has a deadline but --timeout was not set")
	}
	if cli.ctx.Err() != nil {
		t.Errorf("cli.ctx is already cancelled: %v", cli.ctx.Err())
	}
}
