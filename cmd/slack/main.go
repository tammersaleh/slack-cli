package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/slack-cli/cmd"
	"github.com/tammersaleh/slack-cli/internal/confirm"
	"github.com/tammersaleh/slack-cli/internal/output"
)

func main() {
	code := Run(os.Args[1:], os.Stdout, os.Stderr, confirm.NewBiometric())
	os.Exit(code)
}

// Run is the testable entry point. It parses args, installs the given
// Confirmer, runs the command, and returns the process exit code.
//
// Stream conventions (must match SPEC.md - stdout for data, stderr for diagnostics):
//   - kong --help text (explicit user request) → stdout
//   - structured JSON error envelopes → stderr (sole stderr content)
//   - command stdout/stderr → stdout/stderr as the command writes them
//
// Parse errors emit only the JSON envelope on stderr; no usage block,
// no plain-text preamble. The envelope's `hint` field points the user
// at `slack --help` so they can self-recover without us breaking the
// "stdout for data" rule by dumping usage there during a failure.
//
// We deliberately do NOT enable kong.UsageOnError() or call
// kong.FatalIfErrorf - both would write plain text to stderr.
//
// Lives here, not in cmd/, so cmd/slack/main_test.go can drive it
// without exec'ing a subprocess - that gives us a real test of the
// structured-error contract (confirm_denied / confirm_unsupported /
// output.Error / general_error) plus the kong help/parse paths.
func Run(args []string, stdout, stderr io.Writer, cf confirm.Confirmer) int {
	var cli cmd.CLI

	// kong.Exit is overridden so kong's --help and parse-error paths
	// return control to us instead of calling os.Exit. The captured
	// code distinguishes "help printed, exit 0" from "parse failed".
	var (
		kongExitCalled bool
		kongExitCode   int
	)
	parser, parseErr := kong.New(&cli,
		kong.Name("slack"),
		kong.Description("CLI for Slack."),
		// NOTE: we deliberately do NOT pass kong.UsageOnError(). It
		// would route through FatalIfErrorf, which writes "slack:
		// error: <msg>" to stderr in plain text - violating the
		// stderr-is-JSON contract. We handle parse errors manually
		// below.
		kong.Exit(func(code int) {
			kongExitCalled = true
			kongExitCode = code
		}),
		kong.Writers(stdout, stderr),
	)
	if parseErr != nil {
		// Constructing the parser failed - shouldn't happen for static
		// command graphs, but render structured JSON if it does.
		_ = json.NewEncoder(stderr).Encode(map[string]string{
			"error":  "general_error",
			"detail": parseErr.Error(),
		})
		return output.ExitGeneral
	}

	kctx, parseErr := parser.Parse(args)
	if kongExitCalled {
		// Help flag fired. kong already printed usage to stdout and
		// requested exit 0. The parseErr we may see here is an artifact
		// ("expected <subcommand>") of overriding kong.Exit; suppress
		// it and return the code kong asked for.
		return kongExitCode
	}
	if parseErr != nil {
		// Genuine parse error (unknown flag, bad value, missing arg).
		// Per SPEC.md the contract is "stdout for data, stderr for
		// diagnostics" - so usage text would be wrong on stdout, and
		// plain-text "slack: error: ..." would be wrong on stderr. We
		// emit only the structured JSON envelope on stderr and point
		// the user at --help via the hint field.
		_ = json.NewEncoder(stderr).Encode(map[string]string{
			"error":  "invalid_input",
			"detail": parseErr.Error(),
			"hint":   "Run 'slack --help' for usage, or 'slack <command> --help' for a specific command.",
		})
		return output.ExitGeneral
	}

	cli.SetConfirmer(cf)

	err := kctx.Run(&cli)
	if err == nil {
		return 0
	}

	var exitErr *output.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}

	var oErr *output.Error
	if errors.As(err, &oErr) {
		_ = json.NewEncoder(stderr).Encode(oErr)
		return oErr.Code
	}

	// Confirmer sentinel errors get a structured shape so agent
	// consumers see a stable `error` field. Without this they fall
	// through to the generic `general_error` branch below and the
	// reason / wrapped detail end up in the opaque `detail` field.
	if errors.Is(err, confirm.ErrConfirmDenied) {
		_ = json.NewEncoder(stderr).Encode(map[string]string{
			"error":  "confirm_denied",
			"detail": err.Error(),
		})
		return output.ExitGeneral
	}
	if errors.Is(err, confirm.ErrUnsupported) {
		_ = json.NewEncoder(stderr).Encode(map[string]string{
			"error":  "confirm_unsupported",
			"detail": err.Error(),
			"hint":   "Biometric confirmation requires macOS with Touch ID enabled and enrolled. There is no bypass.",
		})
		return output.ExitGeneral
	}

	// Unstructured errors: wrap in JSON for consistency.
	_ = json.NewEncoder(stderr).Encode(map[string]string{
		"error":  "general_error",
		"detail": err.Error(),
	})
	return output.ExitGeneral
}
