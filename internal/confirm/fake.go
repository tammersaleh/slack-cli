package confirm

import (
	"context"
	"sync"
)

// AlwaysApprove is a Confirmer that approves every call. Test-only.
type AlwaysApprove struct{}

// Confirm always returns nil.
func (AlwaysApprove) Confirm(_ context.Context, _ string) error { return nil }

// AlwaysDeny is a Confirmer that denies every call. Test-only.
type AlwaysDeny struct{}

// Confirm always returns ErrConfirmDenied.
func (AlwaysDeny) Confirm(_ context.Context, _ string) error { return ErrConfirmDenied }

// Recorder is a test Confirmer that captures the reason strings it was
// asked to display. Set Deny=true to make every call return
// ErrConfirmDenied; the reason is still recorded so a test can assert
// the prompt happened before the denial.
type Recorder struct {
	// Deny, when true, makes Confirm return ErrConfirmDenied instead of nil.
	Deny bool

	mu      sync.Mutex
	reasons []string
}

// Confirm records reason and returns nil (or ErrConfirmDenied when Deny is set).
func (r *Recorder) Confirm(_ context.Context, reason string) error {
	r.mu.Lock()
	r.reasons = append(r.reasons, reason)
	r.mu.Unlock()
	if r.Deny {
		return ErrConfirmDenied
	}
	return nil
}

// Reasons returns a snapshot of every reason string passed to Confirm,
// in call order.
func (r *Recorder) Reasons() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.reasons))
	copy(out, r.reasons)
	return out
}
