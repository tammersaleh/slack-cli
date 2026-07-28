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
// Input records which of the caller's inputs triggered the error; it's
// populated by the helpers in errors.go so per-item emission via AsItem
// doesn't need to re-pass the same string.
type Error struct {
	Err      string `json:"error"`
	Detail   string `json:"detail,omitempty"`
	Hint     string `json:"hint,omitempty"`
	Input    string `json:"input,omitempty"`
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
//
// Error marks a stream that ended early: the data lines above it are a
// partial result, not the whole set. It carries the same error code the
// command writes to stderr, so a consumer reading only stdout can still
// tell a truncated listing from a complete one. When Error is set,
// HasMore is true and NextCursor is the page to resume from (empty if
// the very first page failed).
type Meta struct {
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
	ErrorCount int    `json:"error_count,omitempty"`
	Error      string `json:"error,omitempty"`
	// FilterExhaustive reports whether a client-side filter (--query,
	// --has-unread) saw every page. False means the command filtered a subset,
	// so an empty result does not mean nothing matches. Nil (key absent) when no
	// client-side filter was active. A pointer because false is meaningful and
	// must still be emitted.
	FilterExhaustive *bool `json:"filter_exhaustive,omitempty"`
}

type metaWrapper struct {
	Meta Meta `json:"_meta"`
}

// Printer writes JSONL output to stdout and errors to stderr.
type Printer struct {
	Out        io.Writer
	Err        io.Writer
	Quiet      bool
	Fields     []string                // top-level field whitelist; empty means all fields
	EnrichFunc func(m map[string]any)  // optional enrichment called on each item
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

	// Filter first, then enrich: enrichment fields (`user_name`,
	// `channel_name`, `ts_iso`) are always attached to surviving source
	// fields, so `--fields=user` keeps the resolved `user_name` too.
	m = filterFields(m, p.Fields)
	enrichTimestamps(m)
	if p.EnrichFunc != nil {
		p.EnrichFunc(m)
	}

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

// alwaysKeptFields survive --fields no matter what the caller asked for.
//
// "input" is a spec requirement for info commands. The error markers are here
// because they are a row's only signal that its data is incomplete: filtering
// one away leaves a row that looks whole, which is the failure mode --fields
// should never be able to manufacture. The command still exits nonzero, but the
// exit code cannot say which row was affected.
var alwaysKeptFields = map[string]bool{
	"input":        true,
	"error":        true,
	"enrich_error": true,
}

// filterFields returns a copy of m containing only the specified fields.
// If fields is empty, returns m unchanged. alwaysKeptFields survive regardless.
func filterFields(m map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		return m
	}

	allowed := make(map[string]bool, len(fields)+len(alwaysKeptFields))
	for _, f := range fields {
		allowed[strings.TrimSpace(f)] = true
	}
	for f := range alwaysKeptFields {
		allowed[f] = true
	}

	filtered := make(map[string]any, len(allowed))
	for k, v := range m {
		if allowed[k] {
			filtered[k] = v
		}
	}
	return filtered
}
