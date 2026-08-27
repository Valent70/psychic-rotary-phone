package telemetry

import (
	"context"
	"errors"
	"testing"
)

func corr(exec string) Correlation {
	return Correlation{ExecutionID: exec, CaseID: "case-1", TenantID: "acme",
		EvidencePackageID: "evp-1", DecisionID: "d-1"}
}

func TestRecorderSatisfiesTheExistingTracerInterface(t *testing.T) {
	var tr Tracer = NewRecorder()
	ctx, span := tr.StartSpan(context.Background(), "anything")
	span.End()
	if ctx == nil {
		t.Fatal("StartSpan must return a context")
	}
}

func TestVeriqoSpanCarriesDomainAndCorrelation(t *testing.T) {
	r := NewRecorder()
	_, s := r.StartVeriqoSpan(context.Background(), "evaluate", DomainDependency, corr("x1"))
	s.SetAttribute(Attribute{Key: "shared_fraction", Value: "0.5"})
	s.End()

	spans := r.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected one span, got %d", len(spans))
	}
	if spans[0].Domain != DomainDependency {
		t.Fatalf("domain must be lifted out of the attributes, got %q", spans[0].Domain)
	}
	if spans[0].Correlation.ExecutionID != "x1" || spans[0].Correlation.TenantID != "acme" {
		t.Fatalf("correlation must be queryable: %+v", spans[0].Correlation)
	}
	if !spans[0].Ended {
		t.Fatal("End must be recorded")
	}
}

func TestSpansJoinByExecutionInOrder(t *testing.T) {
	r := NewRecorder()
	for _, d := range []Domain{DomainEvidence, DomainDependency, DomainFusion, DomainDecision} {
		_, s := r.StartVeriqoSpan(context.Background(), string(d), d, corr("exec-A"))
		s.End()
	}
	_, other := r.StartVeriqoSpan(context.Background(), "noise", DomainRisk, corr("exec-B"))
	other.End()

	got := r.ByExecution("exec-A")
	if len(got) != 4 {
		t.Fatalf("expected four spans for exec-A, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Sequence <= got[i-1].Sequence {
			t.Fatal("spans of one execution must be returned in sequence order")
		}
	}
}

func TestNestedSpansRecordTheirParent(t *testing.T) {
	r := NewRecorder()
	ctx, outer := r.StartVeriqoSpan(context.Background(), "execution", DomainExecution, corr("x"))
	_, inner := r.StartVeriqoSpan(ctx, "fusion", DomainFusion, corr("x"))
	inner.End()
	outer.End()

	spans := r.Spans()
	if spans[1].ParentSeq != spans[0].Sequence {
		t.Fatalf("the inner span must name its parent: %+v", spans[1])
	}
	if spans[0].ParentSeq != -1 {
		t.Fatal("a root span has no parent")
	}
}

func TestUnendedAndOrphanSpansAreDetectable(t *testing.T) {
	r := NewRecorder()
	_, leaked := r.StartVeriqoSpan(context.Background(), "leaks", DomainTruth, corr("x"))
	_ = leaked // deliberately never ended
	_, orphan := r.StartSpan(context.Background(), "unattributed")
	orphan.End()

	if len(r.Unended()) != 1 || r.Unended()[0].Name != "leaks" {
		t.Fatalf("a leaked span must be detectable: %+v", r.Unended())
	}
	if len(r.Orphans()) != 1 || r.Orphans()[0].Name != "unattributed" {
		t.Fatalf("a span with no correlation must be flagged: %+v", r.Orphans())
	}
}

func TestErrorsAreAttachedToTheSpan(t *testing.T) {
	r := NewRecorder()
	_, s := r.StartVeriqoSpan(context.Background(), "replay", DomainReplay, corr("x"))
	s.RecordError(nil)
	s.RecordError(errors.New("divergence at RISK"))
	s.End()
	if len(r.Spans()[0].Errors) != 1 {
		t.Fatalf("a nil error must be ignored and a real one kept: %+v", r.Spans()[0].Errors)
	}
}

func TestMissingDomainsReportsUninstrumentedSubsystems(t *testing.T) {
	r := NewRecorder()
	if len(r.MissingDomains()) != 13 {
		t.Fatalf("with no spans every domain is missing, got %d", len(r.MissingDomains()))
	}
	for _, d := range []Domain{DomainExecution, DomainEvidence, DomainDependency, DomainIdentity,
		DomainFusion, DomainTruth, DomainContradiction, DomainCausal, DomainRisk,
		DomainDecision, DomainTrust, DomainReplay, DomainCertificate} {
		_, s := r.StartVeriqoSpan(context.Background(), string(d), d, corr("x"))
		s.End()
	}
	if got := r.MissingDomains(); len(got) != 0 {
		t.Fatalf("full instrumentation must report nothing missing, got %v", got)
	}
	if len(r.CoveredDomains()) != 13 {
		t.Fatalf("expected 13 covered domains, got %d", len(r.CoveredDomains()))
	}
	if r.Summary() == "" {
		t.Fatal("a summary is required as gate evidence")
	}
}

func TestCounterAccumulatesAndGaugeReplaces(t *testing.T) {
	m := NewMetrics()
	labels := map[string]string{"tenant": "acme"}
	m.Inc(DecisionTotal, labels)
	m.Inc(DecisionTotal, labels)
	m.Inc(DecisionTotal, labels)
	s, _ := m.Get(DecisionTotal, labels)
	if s.Sum != 3 || s.Last != 3 {
		t.Fatalf("a counter must accumulate: %+v", s)
	}

	m.Observe(HumanReviewPending, 7, labels)
	m.Observe(HumanReviewPending, 4, labels)
	g, _ := m.Get(HumanReviewPending, labels)
	if g.Last != 4 || g.Sum != 4 {
		t.Fatalf("a gauge must replace rather than accumulate: %+v", g)
	}
}

func TestObservationMetricKeepsMeanMinMax(t *testing.T) {
	m := NewMetrics()
	for _, v := range []float64{0.2, 0.8, 0.5} {
		m.Observe(DecisionConfidence, v, nil)
	}
	s, _ := m.Get(DecisionConfidence, nil)
	if s.Count != 3 || s.Min != 0.2 || s.Max != 0.8 {
		t.Fatalf("min/max/count must be tracked: %+v", s)
	}
	if mean := s.Mean(); mean < 0.49 || mean > 0.51 {
		t.Fatalf("mean must be Sum/Count, got %v", mean)
	}
}

func TestLabelsPartitionSeriesAndOrderDoesNotMatter(t *testing.T) {
	m := NewMetrics()
	m.Inc(DecisionTotal, map[string]string{"tenant": "a", "action": "FLAG"})
	m.Inc(DecisionTotal, map[string]string{"action": "FLAG", "tenant": "a"})
	m.Inc(DecisionTotal, map[string]string{"tenant": "b", "action": "FLAG"})
	s, _ := m.Get(DecisionTotal, map[string]string{"action": "FLAG", "tenant": "a"})
	if s.Sum != 2 {
		t.Fatalf("label order must not create a second series: %+v", s)
	}
	if len(m.Snapshot()) != 2 {
		t.Fatalf("distinct labels must create distinct series, got %d", len(m.Snapshot()))
	}
}

func TestSnapshotIsDeterministic(t *testing.T) {
	build := func() []Series {
		m := NewMetrics()
		m.Inc(DecisionTotal, map[string]string{"t": "b"})
		m.Inc(EvidenceIngestedTotal, map[string]string{"t": "a"})
		m.Observe(CalibrationECE, 0.03, nil)
		return m.Snapshot()
	}
	a, b := build(), build()
	if len(a) != len(b) {
		t.Fatal("snapshot length must be stable")
	}
	for i := range a {
		if a[i].LabelKey != b[i].LabelKey {
			t.Fatalf("snapshot ordering must be deterministic at %d", i)
		}
	}
}

func TestMissingMetricsIsTheObservabilityGate(t *testing.T) {
	m := NewMetrics()
	if len(m.MissingMetrics()) != len(AllMetrics()) {
		t.Fatal("an empty registry must report every declared metric as missing")
	}
	for _, want := range AllMetrics() {
		m.Record(Sample{Metric: want, Value: 1})
	}
	if got := m.MissingMetrics(); len(got) != 0 {
		t.Fatalf("every declared metric must be emittable, missing %v", got)
	}
}
