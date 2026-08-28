package observability

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCounterConcurrentInc(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("requests")
	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Inc() }()
	}
	wg.Wait()
	if c.Value() != 1000 {
		t.Fatalf("expected 1000, got %d", c.Value())
	}
}

func TestGaugeSetAndAdd(t *testing.T) {
	r := NewRegistry()
	g := r.Gauge("inflight")
	g.Set(10)
	g.Add(-3)
	if g.Value() != 7 {
		t.Fatalf("expected 7, got %d", g.Value())
	}
}

func TestSnapshot_SortedDeterministic(t *testing.T) {
	r := NewRegistry()
	r.Counter("b").Inc()
	r.Counter("a").Inc()
	r.Gauge("z").Set(5)
	snap := r.Snapshot()
	if !strings.Contains(snap, "counter:a 1") || !strings.Contains(snap, "counter:b 1") || !strings.Contains(snap, "gauge:z 5") {
		t.Fatalf("snapshot missing expected entries: %s", snap)
	}
	idxA := strings.Index(snap, "counter:a")
	idxB := strings.Index(snap, "counter:b")
	if idxA > idxB {
		t.Fatal("expected sorted (deterministic) snapshot ordering")
	}
}

type fakeExporter struct {
	mu    sync.Mutex
	spans []*Span
}

func (f *fakeExporter) Export(s *Span) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spans = append(f.spans, s)
}

func TestTracer_SpanTimingAndExport(t *testing.T) {
	exp := &fakeExporter{}
	tr := NewTracer(exp)
	span := tr.StartSpan("raft.replicate")
	time.Sleep(5 * time.Millisecond)
	span.SetTag("peer", "B")
	span.Finish()

	if span.Duration() <= 0 {
		t.Fatal("expected positive span duration")
	}
	if len(tr.FinishedSpans()) != 1 {
		t.Fatal("expected 1 finished span")
	}
	exp.mu.Lock()
	defer exp.mu.Unlock()
	if len(exp.spans) != 1 || exp.spans[0].Tags["peer"] != "B" {
		t.Fatal("expected exporter to receive the finished span with tags")
	}
}
