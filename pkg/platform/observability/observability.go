// Package observability is a dependency-free metrics + tracing shim.
// It is NOT OpenTelemetry — OTel requires an external module unreachable
// from this sandbox's Go proxy allowlist. This gives Veriqo real counters,
// gauges, and span timing today, with an interface narrow enough that
// swapping in a real OTel exporter later is a small, isolated change
// (see Exporter interface) rather than a rewrite.
package observability

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Counter is a monotonically increasing metric.
type Counter struct{ v int64 }

func (c *Counter) Inc()         { atomic.AddInt64(&c.v, 1) }
func (c *Counter) Add(n int64)  { atomic.AddInt64(&c.v, n) }
func (c *Counter) Value() int64 { return atomic.LoadInt64(&c.v) }

// Gauge is a metric that can go up or down.
type Gauge struct{ v int64 }

func (g *Gauge) Set(n int64)  { atomic.StoreInt64(&g.v, n) }
func (g *Gauge) Add(n int64)  { atomic.AddInt64(&g.v, n) }
func (g *Gauge) Value() int64 { return atomic.LoadInt64(&g.v) }

// Registry is a process-wide named-metric collector.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
}

func NewRegistry() *Registry {
	return &Registry{counters: map[string]*Counter{}, gauges: map[string]*Gauge{}}
}

func (r *Registry) Counter(name string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[name]
	if !ok {
		c = &Counter{}
		r.counters[name] = c
	}
	return c
}

func (r *Registry) Gauge(name string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gauges[name]
	if !ok {
		g = &Gauge{}
		r.gauges[name] = g
	}
	return g
}

// Snapshot returns a deterministic, sorted-by-name text dump — suitable
// for a /metrics-style endpoint without pulling in a Prometheus client dep.
func (r *Registry) Snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.counters)+len(r.gauges))
	for n := range r.counters {
		names = append(names, "counter:"+n)
	}
	for n := range r.gauges {
		names = append(names, "gauge:"+n)
	}
	sort.Strings(names)
	out := ""
	for _, n := range names {
		if len(n) > 8 && n[:8] == "counter:" {
			out += fmt.Sprintf("%s %d\n", n, r.counters[n[8:]].Value())
		} else {
			out += fmt.Sprintf("%s %d\n", n, r.gauges[n[6:]].Value())
		}
	}
	return out
}

// --- Lightweight span tracer ---

type Span struct {
	Name   string
	Start  time.Time
	End    time.Time
	Tags   map[string]string
	tracer *Tracer
}

func (s *Span) SetTag(k, v string) {
	if s.Tags == nil {
		s.Tags = map[string]string{}
	}
	s.Tags[k] = v
}

func (s *Span) Finish() {
	s.End = time.Now()
	if s.tracer != nil {
		s.tracer.record(s)
	}
}

func (s *Span) Duration() time.Duration { return s.End.Sub(s.Start) }

// Exporter is the seam a real OTel exporter would implement later.
type Exporter interface {
	Export(s *Span)
}

// Tracer collects finished spans in-memory and optionally forwards them to
// an Exporter (e.g. a future OTel bridge).
type Tracer struct {
	mu       sync.Mutex
	finished []*Span
	exporter Exporter
}

func NewTracer(exp Exporter) *Tracer {
	return &Tracer{exporter: exp}
}

func (t *Tracer) StartSpan(name string) *Span {
	return &Span{Name: name, Start: time.Now(), tracer: t}
}

func (t *Tracer) record(s *Span) {
	t.mu.Lock()
	t.finished = append(t.finished, s)
	t.mu.Unlock()
	if t.exporter != nil {
		t.exporter.Export(s)
	}
}

func (t *Tracer) FinishedSpans() []*Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*Span, len(t.finished))
	copy(out, t.finished)
	return out
}
