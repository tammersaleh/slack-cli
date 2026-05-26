// Package confirm gates destructive CLI actions behind an out-of-band
// confirmation step. The motivating use case is preventing an LLM agent
// running in YOLO mode from accidentally invoking write commands (e.g.
// creating a Slack channel) without an explicit human touch.
//
// The interface is deliberately tiny: a single Confirm(ctx, reason) call
// that blocks until the user approves or denies. Implementations are
// expected to surface the reason string verbatim to the user - the
// reason is the user's only defense against a malicious or confused
// caller, so it must reach the OS-level dialog unaltered.
//
// The production implementation lives in biometric_darwin.go and wraps
// macOS LocalAuthentication (Touch ID / Apple Watch). Non-darwin builds
// get a hard-fail stub (biometric_other.go); there is intentionally no
// env-var bypass.
//
// Tests use the fakes in fake.go (AlwaysApprove / AlwaysDeny / Recorder).
package confirm

import (
	"context"
	"errors"
)

// Confirmer prompts the user to approve a destructive action.
//
// reason is the human-visible justification - implementations MUST pass
// it through to the OS prompt without modification so the user sees
// exactly what they are approving. Callers should build reason from
// fully resolved data (workspace name, channel name) rather than
// internal IDs.
//
// Returns nil on approval. Returns ErrConfirmDenied if the user denies
// or cancels. Returns ctx.Err() if the context is cancelled before the
// user responds; implementations must abort the prompt in that case.
type Confirmer interface {
	Confirm(ctx context.Context, reason string) error
}

// ErrConfirmDenied indicates the user denied or cancelled the
// confirmation prompt. Commands should propagate this so main.go
// surfaces it as a structured CLI error with exit code 1.
var ErrConfirmDenied = errors.New("confirm_denied")

// ErrUnsupported indicates the platform cannot perform a biometric
// confirmation - no Touch ID hardware, biometry locked out, or running
// on a build without Touch ID support (non-darwin, or darwin without
// cgo). Distinct from ErrConfirmDenied so callers / users can tell
// "user said no" from "this machine can't ask." There is intentionally
// no bypass.
var ErrUnsupported = errors.New("confirm_unsupported")
