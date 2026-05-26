package confirm

import (
	"context"
	"errors"
	"testing"
)

func TestAlwaysApprove_ReturnsNil(t *testing.T) {
	c := AlwaysApprove{}
	if err := c.Confirm(context.Background(), "test reason"); err != nil {
		t.Errorf("AlwaysApprove.Confirm returned %v, want nil", err)
	}
}

func TestAlwaysDeny_ReturnsErrConfirmDenied(t *testing.T) {
	c := AlwaysDeny{}
	err := c.Confirm(context.Background(), "test reason")
	if !errors.Is(err, ErrConfirmDenied) {
		t.Errorf("AlwaysDeny.Confirm returned %v, want ErrConfirmDenied", err)
	}
}

func TestRecorder_CapturesReason(t *testing.T) {
	r := &Recorder{}
	if err := r.Confirm(context.Background(), "first"); err != nil {
		t.Fatalf("Recorder.Confirm returned %v, want nil", err)
	}
	if err := r.Confirm(context.Background(), "second"); err != nil {
		t.Fatalf("Recorder.Confirm returned %v, want nil", err)
	}

	got := r.Reasons()
	want := []string{"first", "second"}
	if len(got) != len(want) {
		t.Fatalf("got %d reasons, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Reasons()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRecorder_DenyMode(t *testing.T) {
	// Recorder configured to deny still captures the reason.
	r := &Recorder{Deny: true}
	err := r.Confirm(context.Background(), "denied call")
	if !errors.Is(err, ErrConfirmDenied) {
		t.Errorf("got %v, want ErrConfirmDenied", err)
	}
	got := r.Reasons()
	if len(got) != 1 || got[0] != "denied call" {
		t.Errorf("Reasons() = %v, want [\"denied call\"]", got)
	}
}

func TestRecorder_NoCallsYieldsEmptySlice(t *testing.T) {
	r := &Recorder{}
	if got := r.Reasons(); len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

// We deliberately do NOT unit-test internal/confirm.biometric_darwin.go.
// Its only entry point (LAContext.evaluateAccessControl) blocks on a real
// Touch ID press from the user; there is no documented way to simulate
// the prompt in CI. The CGo wrapper is exercised via manual smoke testing
// only; the test surface for the rest of the codebase uses these fakes.
