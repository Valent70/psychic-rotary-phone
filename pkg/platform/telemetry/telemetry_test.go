package telemetry

import (
	"context"
	"errors"
	"testing"
)

func TestNoopTracerIsDefaultAndSafe(t *testing.T) {
	ctx := context.Background()
	newCtx, span := StartSpan(ctx, "test.span", Attribute{Key: "k", Value: "v"})
	if newCtx != ctx {
		t.Fatalf("noop tracer should return the same context")
	}
	span.SetAttribute(Attribute{Key: "extra", Value: "1"})
	span.RecordError(errors.New("boom"))
	span.End() // must not panic
}

type recordingTracer struct {
	started []string
}

type recordingSpan struct {
	name  string
	attrs []Attribute
	err   error
	ended bool
}

func (s *recordingSpan) SetAttribute(a Attribute) { s.attrs = append(s.attrs, a) }
func (s *recordingSpan) RecordError(err error)    { s.err = err }
func (s *recordingSpan) End()                     { s.ended = true }

func (rt *recordingTracer) StartSpan(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span) {
	rt.started = append(rt.started, name)
	return ctx, &recordingSpan{name: name, attrs: attrs}
}

func TestSetGlobalTracerSwapsImplementation(t *testing.T) {
	defer SetGlobalTracer(NoopTracer{}) // restore default for other tests

	rt := &recordingTracer{}
	SetGlobalTracer(rt)
	_, span := StartSpan(context.Background(), "workflow.step.fusion")
	span.End()

	if len(rt.started) != 1 || rt.started[0] != "workflow.step.fusion" {
		t.Fatalf("expected recording tracer to capture span start, got %+v", rt.started)
	}
	rs, ok := span.(*recordingSpan)
	if !ok {
		t.Fatalf("expected recordingSpan type")
	}
	if !rs.ended {
		t.Fatalf("expected span to be marked ended")
	}
}

func TestSetGlobalTracerNilFallsBackToNoop(t *testing.T) {
	defer SetGlobalTracer(NoopTracer{})
	SetGlobalTracer(nil)
	if _, ok := Global().(NoopTracer); !ok {
		t.Fatalf("expected nil tracer to fall back to NoopTracer")
	}
}
