//go:build !darwin || !cgo

package confirm

import (
	"context"
	"fmt"
)

// biometric is the non-darwin Confirmer: it hard-fails. There is no env
// var bypass on purpose - the whole point of the gate is that it cannot
// be sidestepped by an agent setting an env var. If you need this on
// Linux later, add a real LocalAuth-equivalent here, not a bypass.
type biometric struct{}

// NewBiometric returns the platform's biometric Confirmer. On non-darwin
// builds every call to Confirm fails with ErrUnsupported.
func NewBiometric() Confirmer { return biometric{} }

// Confirm always returns ErrUnsupported on non-darwin builds. The reason
// is included in the error detail so log/error output makes it clear
// what was being attempted.
func (biometric) Confirm(_ context.Context, reason string) error {
	return fmt.Errorf("%w: biometric confirmation requires macOS Touch ID (attempted: %s)", ErrUnsupported, reason)
}
