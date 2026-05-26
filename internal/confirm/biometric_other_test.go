//go:build !darwin || !cgo

package confirm

import (
	"context"
	"errors"
	"testing"
)

func TestBiometric_NonDarwinStub_AlwaysUnsupported(t *testing.T) {
	c := NewBiometric()
	err := c.Confirm(context.Background(), "create channel #foo in Acme")
	if err == nil {
		t.Fatal("expected ErrUnsupported on non-darwin / !cgo build, got nil")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("got %v, want ErrUnsupported", err)
	}
}
