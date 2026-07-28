package api

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// recordingTracer captures events for assertions.
type recordingTracer struct {
	mu     sync.Mutex
	events []string
}

func (r *recordingTracer) Event(kind string, attrs map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, kind)
}

// The tracer a caller attaches must be the one TracerFrom hands back. Every
// paginating call site reaches its tracer this way, so a broken round-trip
// silently disables --trace rather than failing.
func TestTracerFrom_RoundTripsAttachedTracer(t *testing.T) {
	rec := &recordingTracer{}
	ctx := WithTracer(context.Background(), rec)

	got := TracerFrom(ctx)
	if got != Tracer(rec) {
		t.Fatalf("TracerFrom returned %T, want the attached *recordingTracer", got)
	}

	got.Event("page", map[string]any{"endpoint": testEndpoint})
	if len(rec.events) != 1 || rec.events[0] != "page" {
		t.Errorf("got events %v, want exactly [page]", rec.events)
	}
}

// TracerFrom promises callers never need a nil check, so both "nothing
// attached" and "nil attached" must yield a usable no-op tracer. Event is
// called because returning a nil Tracer would only show up as a panic at the
// call site, not as a bad return value.
func TestTracerFrom_NoTracerIsUsableNoop(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{name: "nothing attached", ctx: context.Background()},
		{name: "nil tracer attached", ctx: WithTracer(context.Background(), nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TracerFrom(tt.ctx)
			if got == nil {
				t.Fatal("TracerFrom returned nil, want a no-op Tracer")
			}
			if _, isNoop := got.(noopTracer); !isNoop {
				t.Errorf("TracerFrom returned %T, want noopTracer", got)
			}
			got.Event("page", map[string]any{"endpoint": testEndpoint})
		})
	}
}

// WithTracer(ctx, nil) must not shadow a tracer already on the context. It
// returns ctx untouched rather than storing a nil that TracerFrom would have to
// defend against.
func TestWithTracer_NilDoesNotDisplaceExistingTracer(t *testing.T) {
	rec := &recordingTracer{}
	ctx := WithTracer(context.Background(), rec)

	if got := TracerFrom(WithTracer(ctx, nil)); got != Tracer(rec) {
		t.Errorf("got %T, want the previously attached *recordingTracer", got)
	}
}

func TestJSONLinesTracer_WritesOneObjectPerEvent(t *testing.T) {
	var buf bytes.Buffer
	tr := NewJSONLinesTracer(&buf)

	tr.Event("page", map[string]any{"endpoint": testEndpoint, "items": 2})
	tr.Event("retry", map[string]any{"endpoint": testEndpoint, "wait_ms": 1000})

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), buf.String())
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 1 is not valid JSON: %v", err)
	}
	if first["kind"] != "page" {
		t.Errorf("got kind=%v, want page", first["kind"])
	}
	if first["endpoint"] != testEndpoint {
		t.Errorf("got endpoint=%v, want %s", first["endpoint"], testEndpoint)
	}
	if _, ok := first["ts"].(string); !ok {
		t.Error("event is missing the ts field")
	}

	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("line 2 is not valid JSON: %v", err)
	}
	if second["kind"] != "retry" {
		t.Errorf("got kind=%v, want retry", second["kind"])
	}
}

// The caller's attrs map must survive the call unchanged - Event copies it
// before adding kind/ts. A pagination loop reuses its attrs map shape across
// pages, so mutating it would leak trace bookkeeping into the caller.
func TestJSONLinesTracer_DoesNotMutateCallerAttrs(t *testing.T) {
	var buf bytes.Buffer
	tr := NewJSONLinesTracer(&buf)

	attrs := map[string]any{"endpoint": testEndpoint}
	tr.Event("page", attrs)

	if len(attrs) != 1 {
		t.Errorf("caller attrs grew to %v, want the original single key", attrs)
	}
}

// An unmarshalable attr value drops the event instead of writing a broken line
// or panicking. --trace shares stderr with real error output, so a malformed
// line is worse than a missing one.
func TestJSONLinesTracer_SkipsUnmarshalableEvent(t *testing.T) {
	var buf bytes.Buffer
	tr := NewJSONLinesTracer(&buf)

	tr.Event("page", map[string]any{"fetch": func() {}})

	if buf.Len() != 0 {
		t.Errorf("wrote %q, want nothing for an unmarshalable event", buf.String())
	}
}
