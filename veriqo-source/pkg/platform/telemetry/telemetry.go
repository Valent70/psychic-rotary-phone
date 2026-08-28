// Package telemetry defines Veriqo's tracing seam — the interface every
// public entrypoint in pkg/workflow, pkg/moat/fusion, pkg/moat/causal,
// pkg/moat/decision, pkg/moat/digitaltwin, pkg/platform/audit should
// accept a context.Context and start a span through, per the audit
// doc's section B ("OpenTelemetry tracing ke jalur kritis").
//
// Honest scope statement: this sandbox has no network access to
// proxy.golang.org, so the real go.opentelemetry.io/otel SDK cannot be
// vendored here (confirmed blocked in every prior session of this
// project). What this package provides instead is the exact seam a real
// OTel binding plugs into with zero call-site changes: a Tracer
// interface, a NoopTracer default implementation (zero external deps),
// and a context-carried "current tracer" pattern identical to how
// go.opentelemetry.io/otel/trace itself is typically wired via context.
//
// In production, ProductionMain (or any binary's init) would call
// SetGlobalTracer(otelAdapter) once, where otelAdapter wraps a real
// go.opentelemetry.io/otel/trace.Tracer — every StartSpan call already
// scattered through the codebase then produces real spans without
// further code changes. See docs/OBSERVABILITY.md.
package telemetry

import (
	"context"
	"sync"
)

// Attribute is a single span attribute (name/value pair). Kept as a
// plain struct (not an interface{}-heavy map) so it is trivially
// convertible to attribute.KeyValue in a real OTel binding.
type Attribute struct {
	Key   string
	Value string
}

// Span is the minimal span handle callers interact with — End() is the
// only method call sites need, matching the "defer span.End()" idiom
// real OTel code already uses, so swapping NoopTracer for a real
// implementation requires no call-site changes.
type Span interface {
	// SetAttribute adds an attribute after span creation (e.g. a result
	// computed mid-function, like a risk score).
	SetAttribute(attr Attribute)
	// RecordError attaches an error to the span (no-op if err is nil).
	RecordError(err error)
	End()
}

// Tracer starts spans. A no-op default (NoopTracer) is used until a real
// implementation is installed via SetGlobalTracer.
type Tracer interface {
	StartSpan(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span)
}

// --- no-op implementation (zero external deps, default at rest) ---

type noopSpan struct{}

func (noopSpan) SetAttribute(Attribute) {}
func (noopSpan) RecordError(error)      {}
func (noopSpan) End()                   {}

// NoopTracer is the default Tracer: it starts spans that do nothing.
// This keeps every instrumented call site in the codebase valid and
// zero-dependency in the sandbox, while already carrying the exact
// context propagation shape a real tracer needs.
type NoopTracer struct{}

func (NoopTracer) StartSpan(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span) {
	return ctx, noopSpan{}
}

var (
	globalMu     sync.RWMutex
	globalTracer Tracer = NoopTracer{}
)

// SetGlobalTracer installs the process-wide Tracer implementation. In
// production this is called once at startup with a real OTel-backed
// adapter; in the sandbox / default case it is never called and
// NoopTracer is used throughout.
func SetGlobalTracer(t Tracer) {
	globalMu.Lock()
	defer globalMu.Unlock()
	if t == nil {
		t = NoopTracer{}
	}
	globalTracer = t
}

// Global returns the currently installed Tracer.
func Global() Tracer {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalTracer
}

// StartSpan is the convenience entrypoint call sites use:
//
//	ctx, span := telemetry.StartSpan(ctx, "workflow.step.fusion_arbitrate",
//	    telemetry.Attribute{Key: "run_id", Value: runID})
//	defer span.End()
func StartSpan(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span) {
	return Global().StartSpan(ctx, name, attrs...)
}
