# Biometric confirmation

`slack channel create` is gated behind a macOS Touch ID prompt before
the API call. The gate is meant to prevent an agent in YOLO mode from
accidentally creating channels - not to defeat an attacker. The bar is
"unlikely to fire accidentally," not "uncircumventable by a determined
adversary."

This document captures why the design landed where it did and the UX
problem that remains. If you come back to this and want to change the
mechanism, start here.

## Threat model

The user runs Claude in YOLO mode. The agent has full Bash, network,
and filesystem access. It can read any file the user can read. It can
spawn any process. It can be prompt-injected from anything it reads
(Slack messages, web pages, search results, file contents).

What the agent can't do is satisfy a physical action: a biometric
sensor, a mouse click on a system dialog, a keystroke into another
application's window. Those are the only out-of-band channels
available, and the gate has to use one of them.

## What ships

`internal/confirm` defines a `Confirmer` interface:

```go
type Confirmer interface {
    Confirm(ctx context.Context, reason string) error
}
```

The darwin implementation is a small cgo wrapper around
`LAContext.evaluateAccessControl` with `SecAccessControlCreateWithFlags
+ kSecAccessControlBiometryAny` and `LAAccessControlOperationUseItem`.
A fresh `LAContext` per call (no reuse - Apple lets a previously
authenticated context bless later keychain ops without prompting). On
non-darwin builds, `Confirm` returns `ErrUnsupported` immediately. No
env-var bypass, no `--no-confirm` flag.

The reason string is built from resolved data only: workspace name
from `ResolvedCredentials.TeamName`, channel name with `#`, public or
private. That string is the user's defense - whatever it says is what
they're approving. Never put `T01ABC` or `C01XYZ` in it.

Commands that produce side effects others can see must call
`cli.Confirm(ctx, reason)` before the destructive Slack call. Drafts
and section ops don't - drafts are agent-safe by design (the user
reviews and sends in Slack) and section ops only touch the personal
sidebar.

## The UX problem

The Touch ID dialog is a system-modal popup. macOS shows it on
whichever app has focus at the time, not on the terminal that
triggered it. If the user is typing in another window when the prompt
appears, the next keystroke can dismiss it. The CLI fails closed
(no channel created), but the user has to retry, and meanwhile they
have no record in the terminal of what was just dismissed.

`LocalAuthentication` doesn't expose a knob for "anchor to the calling
process" or "be harder to dismiss." That framework was designed for
GUI apps with a foreground window of their own.

## Alternatives considered

### `/dev/tty` prompt (verified blocked)

The sudo model: write the prompt to `/dev/tty`, read the answer from
`/dev/tty`. Defeats agent because subprocesses can't satisfy a tty
read without a real keyboard. Doesn't work here. Tested 2026-05-27
from inside a Claude Code Bash subprocess:

```
$ tty < /dev/tty
device not configured: /dev/tty
$ echo x > /dev/tty
device not configured: /dev/tty
```

The Bash tool spawns subprocesses without a controlling terminal.
There is no `/dev/tty` to defend with. This rules out every
terminal-anchored approach, including PAM Touch ID (`pam_tid.so`) -
PAM needs a terminal too.

### Slack DM with reaction approval (deferred, viable)

The CLI DMs the user with the resolved reason. The user reacts with
`:+1:` or `:x:`. The CLI polls until it sees a reaction or times out.
Slow (a few seconds round-trip per write), heavier infra, but
completely sidesteps the popup model. Works on any platform, not just
darwin - shedding the cgo would let release builds go back to a
single Linux runner.

This is the realistic next step if the popup remains annoying in
daily use. Sketch: a `confirm.Slack` implementation that posts via the
bot token to the user's self-DM, polls `reactions.get` on the
posted message, and resolves on the first `:+1:` or `:x:` from the
authenticated user. Fail closed on timeout.

### Print reason to stderr before triggering the popup (cheap, deferred)

Currently the user only sees the reason inside the Touch ID dialog.
If they dismiss it without reading, they have no terminal-side record
of what was attempted. A one-line stderr print before the popup would
make accidental dismissals recoverable - they'd at least know what to
retry.

Doesn't fix focus-stealing. Trivial follow-up if we don't replace the
whole mechanism.

### Retry once on cancel (tradeoff, deferred)

Accidental dismissals look identical to explicit cancels at the
`LAError` level (both surface `LAErrorUserCancel`). Auto-retrying
helps the accidental case but doubles the popup count when the user
really meant to cancel. Pass for now.

## Where to look next time

If the popup model has worn out its welcome, the path forward is
`confirm.Slack`. Sketch the Confirmer first, write its tests with a
fake reaction-poller, wire it up alongside the biometric confirmer
behind a `--confirm-via` flag (default `touch-id` for backward
compat, `slack-dm` as opt-in). If the Slack DM model proves better,
swap the default.

The cgo build complexity exists only because of Touch ID. If we
replace it with a Slack-DM confirmer, `.goreleaser.yml` can drop the
darwin/linux split and CI can drop the macos matrix entry. Worth it
if you go that direction.

Either way, the `Confirmer` interface stays put. Anything new plugs
into the same seam.
