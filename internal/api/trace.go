package api

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Tracer emits structured diagnostic events. Implementations must be
// safe for concurrent use.
type Tracer interface {
	Event(kind string, attrs map[string]any)
}

type ctxKey int

const tracerKey ctxKey = iota

// WithTracer returns a context carrying the given tracer. TracerFrom retrieves
// it. Callers that want to stay tracer-agnostic can rely on TracerFrom returning
// a no-op Tracer when none is attached.
func WithTracer(ctx context.Context, t Tracer) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, tracerKey, t)
}

// TracerFrom returns the tracer in ctx, or a no-op Tracer if none is attached.
// Callers never need a nil check.
func TracerFrom(ctx context.Context) Tracer {
	if t, ok := ctx.Value(tracerKey).(Tracer); ok && t != nil {
		return t
	}
	return noopTracer{}
}

type noopTracer struct{}

func (noopTracer) Event(string, map[string]any) {}

// NewJSONLinesTracer returns a Tracer that writes one compact JSON object per
// event to w. Each event gets `kind` and `ts` (RFC3339Nano) fields; attrs are
// merged in directly. Safe for concurrent writers.
func NewJSONLinesTracer(w io.Writer) Tracer {
	return &jsonTracer{w: w}
}

type jsonTracer struct {
	mu sync.Mutex
	w  io.Writer
}

func (t *jsonTracer) Event(kind string, attrs map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Build event payload. Don't mutate caller's map.
	ev := make(map[string]any, len(attrs)+2)
	ev["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	ev["kind"] = kind
	for k, v := range attrs {
		ev[k] = v
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = t.w.Write(data)
}
