package output

import (
	"encoding/json"
	"io"
	"strings"
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
	Err      string `json:"error"`
	Detail   string `json:"detail,omitempty"`
	Hint     string `json:"hint,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Code     int    `json:"-"`
}

func (e *Error) Error() string {
	return e.Err
}

// ExitError carries an exit code without being printed to stderr.
// Used for partial failures where per-item errors are already on stdout.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return "exit"
}

// Meta is the _meta trailer emitted after all data lines.
type Meta struct {
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
	ErrorCount int    `json:"error_count,omitempty"`
}

type metaWrapper struct {
	Meta Meta `json:"_meta"`
}

// Printer writes JSONL output to stdout and errors to stderr.
type Printer struct {
	Out    io.Writer
	Err    io.Writer
	Quiet  bool
	Fields []string // top-level field whitelist; empty means all fields
}

// PrintItem writes a single data line as compact JSON with timestamp enrichment
// and field filtering. Each call produces one line of JSONL.
func (p *Printer) PrintItem(v any) error {
	if p.Quiet {
		return nil
	}

	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		// Not an object - write as-is.
		raw = append(raw, '\n')
		_, err := p.Out.Write(raw)
		return err
	}

	enrichTimestamps(m)
	m = filterFields(m, p.Fields)

	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	out = append(out, '\n')
	_, err = p.Out.Write(out)
	return err
}

// PrintMeta writes the _meta trailer line.
func (p *Printer) PrintMeta(meta Meta) error {
	if p.Quiet {
		return nil
	}
	out, err := json.Marshal(metaWrapper{Meta: meta})
	if err != nil {
		return err
	}
	out = append(out, '\n')
	_, err = p.Out.Write(out)
	return err
}

// PrintError writes an Error as compact JSON to stderr. Not affected by quiet mode.
func (p *Printer) PrintError(e *Error) error {
	return json.NewEncoder(p.Err).Encode(e)
}

// filterFields returns a copy of m containing only the specified fields.
// If fields is empty, returns m unchanged. The "input" field is always preserved
// (spec requirement for info commands).
func filterFields(m map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		return m
	}

	allowed := make(map[string]bool, len(fields)+1)
	for _, f := range fields {
		allowed[strings.TrimSpace(f)] = true
	}
	allowed["input"] = true

	filtered := make(map[string]any, len(allowed))
	for k, v := range m {
		if allowed[k] {
			filtered[k] = v
		}
	}
	return filtered
}
