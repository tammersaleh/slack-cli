package output

import (
	"encoding/json"
	"io"
)

const (
	ExitSuccess   = 0
	ExitGeneral   = 1
	ExitAuth      = 2
	ExitRateLimit = 3
	ExitNetwork   = 4
)

// Error represents a structured CLI error written to stderr as JSON.
type Error struct {
	Err    string `json:"error"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
	Code   int    `json:"-"`
}

func (e *Error) Error() string {
	return e.Err
}

// Printer writes command output in the configured format.
type Printer struct {
	Out   io.Writer
	Err   io.Writer
	Quiet bool
	Raw   bool
}

// Print writes v as indented JSON to Out. Slack timestamps are enriched
// with _iso siblings. In quiet mode, output is suppressed.
func (p *Printer) Print(v any) error {
	if p.Quiet {
		return nil
	}
	return writeEnrichedJSON(p.Out, v)
}

// PrintRaw writes raw JSON bytes to Out with a trailing newline.
// In quiet mode, output is suppressed.
func (p *Printer) PrintRaw(data json.RawMessage) error {
	if p.Quiet {
		return nil
	}
	if _, err := p.Out.Write(data); err != nil {
		return err
	}
	_, err := p.Out.Write([]byte("\n"))
	return err
}

// PrintError writes an Error as JSON to Err (stderr). Not affected by quiet mode.
func (p *Printer) PrintError(e *Error) error {
	enc := json.NewEncoder(p.Err)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}
