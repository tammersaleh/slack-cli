package output

import (
	"bytes"
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
	// RowCount and ArchivedRowCount come from the limits block of a Slack
	// List walk (list records). Archived records never appear in the stream,
	// so ArchivedRowCount is the only signal that the list holds rows the
	// output does not. Pointers so zero is emitted when Slack reported it.
	RowCount         *int `json:"row_count,omitempty"`
	ArchivedRowCount *int `json:"archived_row_count,omitempty"`
	// Total is the size of the result set as Slack reported it on the first
	// page a page-number command fetched (search, file list). Nil (key absent)
	// for cursor-based commands and when no page arrived. A pointer because 0
	// is the meaningful value: it is Slack saying nothing matched, as opposed
	// to rows that went missing.
	Total *int `json:"total,omitempty"`
	// SuppressedSent and SuppressedDeleted count the drafts `draft list`
	// fetched and withheld because they were sent or deleted. Pointers so
	// zero is emitted: a zero says the page held nothing hidden.
	SuppressedSent    *int `json:"suppressed_sent,omitempty"`
	SuppressedDeleted *int `json:"suppressed_deleted,omitempty"`
}

type metaWrapper struct {
	Meta Meta `json:"_meta"`
}

// Printer writes JSONL output to stdout and errors to stderr.
type Printer struct {
	Out        io.Writer
	Err        io.Writer
	Quiet      bool
	Fields     []string               // top-level field whitelist; empty means all fields
	EnrichFunc func(m map[string]any) // optional enrichment called on each item
	// EnrichSources maps each key EnrichFunc can add to the source keys it reads
	// (user_name: user, user_id). It is what lets --fields name a derived key:
	// the projection keeps the sources long enough to derive from them.
	// Timestamp siblings need no entry; X_iso reverses to X by rule.
	EnrichSources map[string][]string
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

	// Decode numbers as json.Number so a value round-trips exactly as the
	// command built it: a float64 detour would turn 3.50 into 3.5 and lose
	// precision above 2^53 (list records emits Slack's numbers verbatim).
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		// Not an object - write as-is.
		raw = append(raw, '\n')
		_, err := p.Out.Write(raw)
		return err
	}

	// Project, then enrich, then drop what the projection only borrowed.
	// Enrichment runs on the projected row so it spends lookups only on ids
	// the caller asked about, and a requested source key keeps its derived
	// sibling: --fields=user emits user_name too.
	proj := newProjection(p.Fields, p.EnrichSources)
	m = proj.filter(m)
	enrichTimestamps(m)
	if p.EnrichFunc != nil {
		p.EnrichFunc(m)
	}
	proj.dropBorrowed(m)

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
//
// "replaced_draft_id" is the same kind of marker on a success row: draft update
// printed a different draft from the one the caller named. Without it a
// filtered row shows a new id and nothing to say the draft was re-keyed.
var alwaysKeptFields = map[string]bool{
	"input":             true,
	"error":             true,
	"enrich_error":      true,
	"replaced_draft_id": true,
}

// projection is one row's --fields selection. A derived name (user_name,
// ts_iso) is not in the row until enrichment runs, so naming one borrows its
// source keys: they pass the filter, enrichment derives from them, and
// dropBorrowed removes them again.
type projection struct {
	active    bool
	requested map[string]bool
	borrowed  map[string]bool     // source keys kept only to derive from
	sources   map[string][]string // derived name -> source keys
	present   map[string]bool     // keys in the row before enrichment
}

func newProjection(fields []string, sources map[string][]string) *projection {
	p := &projection{sources: sources}
	if len(fields) == 0 {
		return p
	}
	p.active = true
	p.requested = make(map[string]bool, len(fields))
	for _, f := range fields {
		p.requested[strings.TrimSpace(f)] = true
	}
	p.borrowed = map[string]bool{}
	for f := range p.requested {
		for _, src := range p.sourcesOf(f) {
			if !p.requested[src] && !alwaysKeptFields[src] {
				p.borrowed[src] = true
			}
		}
	}
	return p
}

// sourcesOf returns the keys a derived name is built from, or nil when the
// name is not derived. It reads the same table enrichment is wired with, and
// reverses the timestamp rule enrichMap applies, so neither can drift.
func (p *projection) sourcesOf(name string) []string {
	srcs := p.sources[name]
	if key, ok := strings.CutSuffix(name, isoSuffix); ok && isTimestampKey(key) {
		srcs = append(srcs[:len(srcs):len(srcs)], key)
	}
	return srcs
}

// filter returns the row cut down to the requested keys, the borrowed source
// keys, and alwaysKeptFields. With no --fields it returns m unchanged.
func (p *projection) filter(m map[string]any) map[string]any {
	if !p.active {
		return m
	}
	filtered := make(map[string]any, len(p.requested)+len(p.borrowed))
	p.present = make(map[string]bool, len(m))
	for k, v := range m {
		if p.requested[k] || p.borrowed[k] || alwaysKeptFields[k] {
			filtered[k] = v
			p.present[k] = true
		}
	}
	return filtered
}

// dropBorrowed removes, after enrichment, the source keys the projection
// borrowed and any derived key that exists only because of them and was not
// requested. A key enrichment added from a requested source stays, and so does
// one no table explains.
func (p *projection) dropBorrowed(m map[string]any) {
	if len(p.borrowed) == 0 {
		return
	}
	for k := range m {
		switch {
		case p.borrowed[k]:
			delete(m, k)
		case p.present[k] || p.requested[k]:
		case p.onlyFromBorrowed(k):
			delete(m, k)
		}
	}
}

// onlyFromBorrowed reports whether every source of derived key k that the row
// held was borrowed. False when k has no source in the row.
func (p *projection) onlyFromBorrowed(k string) bool {
	found := false
	for _, src := range p.sourcesOf(k) {
		if !p.present[src] {
			continue
		}
		if !p.borrowed[src] {
			return false
		}
		found = true
	}
	return found
}

// filterFields returns a copy of m containing only the specified fields.
// If fields is empty, returns m unchanged. alwaysKeptFields survive regardless.
func filterFields(m map[string]any, fields []string) map[string]any {
	return newProjection(fields, nil).filter(m)
}
