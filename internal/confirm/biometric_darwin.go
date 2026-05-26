//go:build darwin && cgo

package confirm

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework LocalAuthentication -framework Security -framework CoreFoundation -framework Foundation

#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>
#import <Security/Security.h>

// confirm_status mirrors a small enum the Go side uses to translate the
// outcome of evaluateAccessControl into the right Go error type. Keeping
// this on the cgo side means we don't leak Apple's numeric LAError codes
// out of this file.
typedef enum {
    CONFIRM_OK            = 0,
    CONFIRM_DENIED        = 1,  // user-visible denial or cancellation
    CONFIRM_UNSUPPORTED   = 2,  // biometry unavailable / not enrolled / locked out
    CONFIRM_ERROR         = 3,  // anything else
} confirm_status;

typedef struct {
    confirm_status status;
    char          *err;  // optional NUL-terminated description; Go must free
} confirm_result;

// confirm_new_context allocates an LAContext (retained) and returns it.
// Returns NULL on allocation failure. The caller must release with
// confirm_release_handle() exactly once.
//
// We split LAContext allocation from evaluateAccessControl so the Go
// side can publish the handle to the cancellation watchdog BEFORE the
// blocking system prompt starts. That eliminates the publication race
// that a single-call API would have.
static void *confirm_new_context(void) {
    @autoreleasepool {
        LAContext *ctx = [[LAContext alloc] init];
        if (ctx == nil) return NULL;
        return (void *)CFBridgingRetain(ctx);
    }
}

// confirm_release_handle releases an LAContext retained by
// confirm_new_context(). Safe to call with NULL.
static void confirm_release_handle(void *handle) {
    if (handle == NULL) return;
    CFBridgingRelease(handle);
}

// confirm_invalidate aborts an in-flight evaluateAccessControl on the
// LAContext at handle. Safe to call concurrently with confirm_evaluate
// from another thread - that's the whole point: a watchdog goroutine
// calls this when the Go ctx is cancelled to dismiss the system prompt.
// Safe to call with NULL.
static void confirm_invalidate(void *handle) {
    if (handle == NULL) return;
    LAContext *ctx = (__bridge LAContext *)handle;
    [ctx invalidate];
}

// confirm_evaluate runs LAContext.evaluateAccessControl with
// LAAccessControlOperationUseItem against a SecAccessControl flagged
// kSecAccessControlBiometryAny. Blocks until the user responds or the
// context is invalidated.
//
// reason is forwarded verbatim to the system prompt as the localized
// reason - it is the user's only defense against an unwanted prompt,
// so we never alter it.
static confirm_result confirm_evaluate(void *handle, const char *c_reason) {
    confirm_result r = {CONFIRM_ERROR, NULL};
    @autoreleasepool {
        if (handle == NULL) {
            r.err = strdup("nil LAContext");
            return r;
        }
        LAContext *ctx = (__bridge LAContext *)handle;
        NSString *reason = [NSString stringWithUTF8String:c_reason];

        // SecAccessControl gated on biometryAny only. We deliberately
        // omit kSecAccessControlOr / DevicePasscode - the threat model
        // wants a physical biometric tap, not a password fallback.
        // biometryAny (not biometryCurrentSet) means re-enrolling a
        // finger doesn't invalidate the gate; that suits a
        // "prevent accidents" model fine.
        CFErrorRef cfErr = NULL;
        SecAccessControlRef accessControl = SecAccessControlCreateWithFlags(
            kCFAllocatorDefault,
            kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
            kSecAccessControlBiometryAny,
            &cfErr);
        if (accessControl == NULL) {
            NSString *desc = cfErr ? (NSString *)CFBridgingRelease(CFErrorCopyDescription(cfErr)) : @"SecAccessControl creation failed";
            if (cfErr) CFRelease(cfErr);
            r.err = strdup([desc UTF8String]);
            return r;
        }

        dispatch_semaphore_t sem = dispatch_semaphore_create(0);
        __block BOOL success = NO;
        __block NSError *evalErr = nil;

        [ctx evaluateAccessControl:accessControl
                         operation:LAAccessControlOperationUseItem
                   localizedReason:reason
                             reply:^(BOOL ok, NSError * _Nullable error) {
            success = ok;
            evalErr = error;
            dispatch_semaphore_signal(sem);
        }];

        dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);

        CFRelease(accessControl);

        if (success) {
            r.status = CONFIRM_OK;
            return r;
        }
        if (evalErr != nil) {
            // Map LAError codes onto our coarse-grained status enum.
            // The Go side uses these to wrap the appropriate sentinel
            // error (ErrConfirmDenied vs ErrUnsupported).
            switch (evalErr.code) {
                case LAErrorUserCancel:
                case LAErrorAppCancel:
                case LAErrorSystemCancel:
                case LAErrorUserFallback:
                case LAErrorAuthenticationFailed:
                    r.status = CONFIRM_DENIED;
                    break;

                case LAErrorBiometryNotAvailable:
                case LAErrorBiometryNotEnrolled:
                case LAErrorBiometryLockout:
                    // "We literally cannot ask" - not the same as
                    // "user said no." Report it as unsupported so
                    // callers / users can tell the difference.
                    r.status = CONFIRM_UNSUPPORTED;
                    break;

                default:
                    r.status = CONFIRM_ERROR;
                    break;
            }
            r.err = strdup([[evalErr localizedDescription] UTF8String]);
        }
        return r;
    }
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// biometric is the macOS Touch ID Confirmer.
type biometric struct{}

// NewBiometric returns a Confirmer backed by macOS LocalAuthentication
// (Touch ID, or Apple Watch on Macs that support it).
func NewBiometric() Confirmer { return biometric{} }

// Confirm prompts the user via Touch ID and blocks until they tap or
// deny. Honors ctx: if ctx is cancelled (e.g. --timeout fires) the
// LAContext is invalidated and ctx.Err() is returned.
//
// Error returns:
//   - nil on approval
//   - ctx.Err() if ctx was cancelled
//   - ErrConfirmDenied (wrapped) on user cancellation, denial, or auth failure
//   - ErrUnsupported (wrapped) when biometry is unavailable / not enrolled / locked out
//   - generic error for unexpected LAError codes or SecAccessControl failures
//
// A fresh LAContext is created for every call - we never reuse one
// across Confirm invocations, so a previously approved prompt cannot
// silently bless a subsequent destructive operation.
func (biometric) Confirm(ctx context.Context, reason string) error {
	if reason == "" {
		return errors.New("confirm: empty reason; Confirm requires a user-visible justification")
	}

	// Fast-path: if the caller's context is already cancelled before
	// we even start, never enter the OS auth path. Without this, an
	// already-expired ctx still allocates an LAContext, spawns the
	// evaluate goroutine, and only catches cancellation via the
	// watchdog - which would briefly enter LocalAuthentication and
	// potentially flash the system prompt before being invalidated.
	// The whole point of honoring ctx is to NOT prompt the user.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Allocate the LAContext synchronously before launching the
	// blocking goroutine. This is the key fix for the cancellation
	// race: by the time the watchdog reads `handle`, it has already
	// been written under the only goroutine that ever writes it.
	handle := C.confirm_new_context()
	if handle == nil {
		return errors.New("confirm: failed to allocate LAContext")
	}

	cReason := C.CString(reason)
	defer C.free(unsafe.Pointer(cReason))

	// Run evaluate on a goroutine so the watchdog can race it. The
	// channel is buffered so the cgo goroutine can write even if the
	// watchdog has already won and the main path has moved on.
	resultCh := make(chan C.confirm_result, 1)
	go func() {
		resultCh <- C.confirm_evaluate(handle, cReason)
	}()

	// Track cancellation so we can prefer ctx.Err() over a stale
	// "denied" result if the user happened to tap right as ctx
	// cancelled. The mutex is for the cancelled bool only - `handle`
	// itself is set above before any goroutine reads it and is never
	// rewritten, so no synchronization is needed on the pointer.
	var (
		mu        sync.Mutex
		cancelled bool
	)
	done := make(chan struct{})
	// watchdogDone closes when the cancellation goroutine has fully
	// exited, including any in-flight C.confirm_invalidate call. We
	// MUST wait on this before releasing the LAContext - otherwise
	// invalidate(handle) could race with release_handle(handle) and
	// dereference freed memory.
	watchdogDone := make(chan struct{})
	defer func() {
		close(done)
		<-watchdogDone
		C.confirm_release_handle(handle)
	}()
	go func() {
		defer close(watchdogDone)
		select {
		case <-ctx.Done():
			mu.Lock()
			cancelled = true
			mu.Unlock()
			// Invalidate is documented as thread-safe and idempotent.
			C.confirm_invalidate(handle)
		case <-done:
		}
	}()

	r := <-resultCh

	var errMsg string
	if r.err != nil {
		errMsg = C.GoString(r.err)
		C.free(unsafe.Pointer(r.err))
	}

	mu.Lock()
	wasCancelled := cancelled
	mu.Unlock()

	// Cancellation wins over any other outcome - if the caller cared
	// enough to cancel, surfacing a stale "ok" or "denied" would be
	// confusing.
	if wasCancelled {
		return ctx.Err()
	}

	switch r.status {
	case C.CONFIRM_OK:
		return nil
	case C.CONFIRM_DENIED:
		if errMsg != "" {
			return fmt.Errorf("%w: %s", ErrConfirmDenied, errMsg)
		}
		return ErrConfirmDenied
	case C.CONFIRM_UNSUPPORTED:
		if errMsg != "" {
			return fmt.Errorf("%w: %s", ErrUnsupported, errMsg)
		}
		return ErrUnsupported
	default:
		if errMsg != "" {
			return fmt.Errorf("biometric confirmation failed: %s", errMsg)
		}
		return errors.New("biometric confirmation failed for unknown reason")
	}
}
