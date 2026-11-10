package cmd

import "github.com/tammersaleh/slack-cli/internal/output"

// abortWithTrailer ends a multi-input command that hit a fatal error partway
// through. The rows already on stdout are authoritative; the trailer's
// has_more:true says some inputs have no row, and error names why. There is
// no cursor: the recovery is to rerun, or to rerun the inputs that have no
// row. The returned error is fatal itself, which main prints to stderr with
// its exit code.
//
// Without the trailer a truncated run is a few good rows and nothing else,
// which reads as a complete answer to any caller that filters out _meta.
func abortWithTrailer(p *output.Printer, fatal *output.Error, errorCount int) error {
	if err := p.PrintMeta(output.Meta{HasMore: true, Error: fatal.Err, ErrorCount: errorCount}); err != nil {
		return err
	}
	return fatal
}
