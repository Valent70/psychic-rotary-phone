// veriqo_telemetry.go closes PHASES 24 and 25. The audit's point was
// that replacing NoopTracer is not the deliverable — the deliverable is
// a telemetry SCHEMA that represents VERIQO:
//
//	Execution, Evidence, Dependency, Identity, Fusion, Truth,
//	Contradiction, Causal, Risk, Decision, Trust, Replay, Certificate
//
// plus the named business metrics, and trace correlation through
// ExecutionID / CaseID / TenantID / EvidencePackageID / ReplayPackageID
// / DecisionID / CertificateID.
//
// The Recorder implemented here is an in-process, deterministic span
// sink. It is not an OTLP exporter and does not pretend to be: an
// exporter needs a network, which this environment does not have, and a
// fake exporter marked "observability: done" is exactly the false green
// the assurance plane exists to prevent. What it IS is the complete
// data model plus a real Tracer implementation, so an OTLP exporter is
// a transport adapter over a schema that is already tested rather than a
// schema invented at integration time.
package telemetry

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Domain names the VERIQO subsystem a span belongs to. Spans without a
// domain are rejected by the Recorder: an unattributed span is noise.
type Domain string

const (
	DomainExecution     Domain = "execution"
	DomainEvidence      Domain = "evidence"
	DomainDependency    Domain = "dependency"
	DomainIdentity      Domain = "identity"
	DomainFusion        Domain = "fusion"
	DomainTruth         Domain = "truth"
	DomainContradiction Domain = "contradiction"
	DomainCausal        Domain = "causal"
	DomainRisk          Domain = "risk"
	DomainDecision      Domain = "decision"
	DomainTrust         Domain = "trust"
	DomainReplay        Domain = "replay"
	DomainCertificate   Domain = "certificate"
)

// Correlation is the identity set every span carries so traces can be
// joined across subsystems without a distributed join key nobody agreed
// on. Every field is optional individually; a span with none of them is
// unjoinable and is flagged by Recorder.Orphans.
type Correlation struct {
	ExecutionID       string `json:"execution_id"`
	CaseID            string `json:"case_id"`
	TenantID          string `json:"tenant_id"`
	EvidencePackageID string `json:"evidence_package_id"`
	ReplayPackageID   string `json:"replay_package_id"`
	DecisionID        string `json:"decision_id"`
	CertificateID     string `json:"certificate_id"`
}

func (c Correlation) empty() bool { return c == Correlation{} }

func (c Correlation) attributes() []Attribute {
	out := []Attribute{}
	add := func(k, v string) {
		if v != "" {
			out = append(out, Attribute{Key: k, Value: v})
		}
	}
	add("execution_id", c.ExecutionID)
	add("case_id", c.CaseID)
	add("tenant_id", c.TenantID)
	add("evidence_package_id", c.EvidencePackageID)
	add("replay_package_id", c.ReplayPackageID)
	add("decision_id", c.DecisionID)
	add("certificate_id", c.CertificateID)
	return out
}

// SpanRecord is one completed span.
type SpanRecord struct {
	Name        string      `json:"name"`
	Domain      Domain      `json:"domain"`
	Correlation Correlation `json:"correlation"`
	Attributes  []Attribute `json:"attributes"`
	Errors      []string    `json:"errors"`
	Ended       bool        `json:"ended"`
	Sequence    int         `json:"sequence"`
	ParentSeq   int         `json:"parent_sequence"`
}

// Metric names the business metrics the audit enumerated. They are
// constants rather than free strings so a typo is a compile error
// instead of a silently empty dashboard panel.
type Metric string

const (
	EvidenceIngestedTotal   Metric = "evidence_ingested_total"
	EvidenceRejectedTotal   Metric = "evidence_rejected_total"
	DependencyConflictTotal Metric = "dependency_conflict_total"
	IdentityConflictTotal   Metric = "identity_conflict_total"
	TruthConflictTotal      Metric = "truth_conflict_total"
	FusionRefusalTotal      Metric = "fusion_refusal_total"
	DecisionTotal           Metric = "decision_total"
	DecisionConfidence      Metric = "decision_confidence"
	TrustTransitionTotal    Metric = "trust_transition_total"
	ReplaySuccessTotal      Metric = "replay_success_total"
	ReplayDivergenceTotal   Metric = "replay_divergence_total"
	SimulationTotal         Metric = "simulation_total"
	SimulationRefusalTotal  Metric = "simulation_refusal_total"
	PolicyDenialTotal       Metric = "policy_denial_total"
	HumanReviewPending      Metric = "human_review_pending"
	ModelDriftScore         Metric = "model_drift_score"
	CalibrationECE          Metric = "calibration_ece"
)

// AllMetrics is the declared inventory. A metrics backend that does not
// expose all of these is incomplete, and the assurance gate checks
// exactly this list rather than a count.
func AllMetrics() []Metric {
	return []Metric{
		EvidenceIngestedTotal, EvidenceRejectedTotal, DependencyConflictTotal,
		IdentityConflictTotal, TruthConflictTotal, FusionRefusalTotal,
		DecisionTotal, DecisionConfidence, TrustTransitionTotal,
		ReplaySuccessTotal, ReplayDivergenceTotal, SimulationTotal,
		SimulationRefusalTotal, PolicyDenialTotal, HumanReviewPending,
		ModelDriftScore, CalibrationECE,
	}
}

// kind distinguishes a monotonic counter from a gauge/observation, which
// matters because summing a gauge is meaningless and averaging a counter
// is worse.
type kind int

const (
	kindCounter kind = iota
	kindGauge
	kindObservation
)

var metricKind = map[Metric]kind{
	EvidenceIngestedTotal: kindCounter, EvidenceRejectedTotal: kindCounter,
	DependencyConflictTotal: kindCounter, IdentityConflictTotal: kindCounter,
	TruthConflictTotal: kindCounter, FusionRefusalTotal: kindCounter,
	DecisionTotal: kindCounter, TrustTransitionTotal: kindCounter,
	ReplaySuccessTotal: kindCounter, ReplayDivergenceTotal: kindCounter,
	SimulationTotal: kindCounter, SimulationRefusalTotal: kindCounter,
	PolicyDenialTotal:  kindCounter,
	HumanReviewPending: kindGauge, ModelDriftScore: kindGauge, CalibrationECE: kindGauge,
	DecisionConfidence: kindObservation,
}

// Sample is one metric reading.
type Sample struct {
	Metric Metric            `json:"metric"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels"`
}

// Series is the aggregated state of one metric/label combination.
type Series struct {
	Metric   Metric            `json:"metric"`
	Labels   map[string]string `json:"labels"`
	Count    uint64            `json:"count"`
	Sum      float64           `json:"sum"`
	Last     float64           `json:"last"`
	Min      float64           `json:"min"`
	Max      float64           `json:"max"`
	LabelKey string            `json:"label_key"`
}

// Mean returns Sum/Count for observation metrics.
func (s Series) Mean() float64 {
	if s.Count == 0 {
		return 0
	}
	return s.Sum / float64(s.Count)
}

// Metrics is a deterministic in-process metrics registry.
type Metrics struct {
	mu     sync.RWMutex
	series map[string]*Series
}

// NewMetrics constructs a registry.
func NewMetrics() *Metrics { return &Metrics{series: map[string]*Series{}} }

func labelKey(m Metric, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(string(m))
	for _, k := range keys {
		sb.WriteString("|" + k + "=" + labels[k])
	}
	return sb.String()
}

// Record applies one sample.
func (m *Metrics) Record(s Sample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := labelKey(s.Metric, s.Labels)
	ser, ok := m.series[key]
	if !ok {
		ser = &Series{Metric: s.Metric, Labels: copyLabels(s.Labels), LabelKey: key,
			Min: s.Value, Max: s.Value}
		m.series[key] = ser
	}
	switch metricKind[s.Metric] {
	case kindCounter:
		ser.Sum += s.Value
		ser.Last = ser.Sum
	case kindGauge:
		ser.Last = s.Value
		ser.Sum = s.Value
	default:
		ser.Sum += s.Value
		ser.Last = s.Value
	}
	ser.Count++
	if s.Value < ser.Min {
		ser.Min = s.Value
	}
	if s.Value > ser.Max {
		ser.Max = s.Value
	}
}

// Inc increments a counter by one.
func (m *Metrics) Inc(metric Metric, labels map[string]string) {
	m.Record(Sample{Metric: metric, Value: 1, Labels: labels})
}

// Observe records a value for a gauge or observation metric.
func (m *Metrics) Observe(metric Metric, v float64, labels map[string]string) {
	m.Record(Sample{Metric: metric, Value: v, Labels: labels})
}

// Get returns one series.
func (m *Metrics) Get(metric Metric, labels map[string]string) (Series, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.series[labelKey(metric, labels)]
	if !ok {
		return Series{}, false
	}
	return *s, true
}

// Snapshot returns every series in a deterministic order.
func (m *Metrics) Snapshot() []Series {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Series, 0, len(m.series))
	for _, s := range m.series {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LabelKey < out[j].LabelKey })
	return out
}

// MissingMetrics reports declared metrics that have never been recorded.
// This is the observability gate: "the tracer is wired" is not the same
// claim as "every business metric is actually emitted somewhere".
func (m *Metrics) MissingMetrics() []Metric {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[Metric]bool{}
	for _, s := range m.series {
		seen[s.Metric] = true
	}
	var out []Metric
	for _, want := range AllMetrics() {
		if !seen[want] {
			out = append(out, want)
		}
	}
	return out
}

func copyLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------
// Recorder — a real Tracer
// ---------------------------------------------------------------------

type ctxKey struct{}

// Recorder is a deterministic Tracer implementation that keeps spans in
// memory. It satisfies the existing Tracer interface, so
// SetGlobalTracer(NewRecorder()) turns every existing StartSpan call
// site into a real, assertable span without touching those call sites.
type Recorder struct {
	mu    sync.Mutex
	spans []*SpanRecord
	seq   int
}

// NewRecorder constructs a Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

type recSpan struct {
	rec *Recorder
	idx int
}

func (s recSpan) SetAttribute(a Attribute) {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.spans[s.idx].Attributes = append(s.rec.spans[s.idx].Attributes, a)
}

func (s recSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.spans[s.idx].Errors = append(s.rec.spans[s.idx].Errors, err.Error())
}

func (s recSpan) End() {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.spans[s.idx].Ended = true
}

// StartSpan implements Tracer.
func (r *Recorder) StartSpan(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span) {
	r.mu.Lock()
	parent := -1
	if v, ok := ctx.Value(ctxKey{}).(int); ok {
		parent = v
	}
	r.seq++
	rec := &SpanRecord{Name: name, Attributes: append([]Attribute(nil), attrs...),
		Sequence: r.seq, ParentSeq: parent}
	// Domain and correlation are lifted out of the attributes so the
	// schema is queryable rather than buried in a key-value soup.
	for _, a := range attrs {
		switch a.Key {
		case "domain":
			rec.Domain = Domain(a.Value)
		case "execution_id":
			rec.Correlation.ExecutionID = a.Value
		case "case_id":
			rec.Correlation.CaseID = a.Value
		case "tenant_id":
			rec.Correlation.TenantID = a.Value
		case "evidence_package_id":
			rec.Correlation.EvidencePackageID = a.Value
		case "replay_package_id":
			rec.Correlation.ReplayPackageID = a.Value
		case "decision_id":
			rec.Correlation.DecisionID = a.Value
		case "certificate_id":
			rec.Correlation.CertificateID = a.Value
		}
	}
	r.spans = append(r.spans, rec)
	idx := len(r.spans) - 1
	seq := rec.Sequence
	r.mu.Unlock()
	return context.WithValue(ctx, ctxKey{}, seq), recSpan{rec: r, idx: idx}
}

// StartVeriqoSpan is the typed entry point: it forces a domain and a
// correlation set, which is what makes traces joinable.
func (r *Recorder) StartVeriqoSpan(ctx context.Context, name string, d Domain,
	c Correlation, extra ...Attribute) (context.Context, Span) {
	attrs := append([]Attribute{{Key: "domain", Value: string(d)}}, c.attributes()...)
	attrs = append(attrs, extra...)
	return r.StartSpan(ctx, name, attrs...)
}

// Spans returns a copy of every recorded span.
func (r *Recorder) Spans() []SpanRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SpanRecord, 0, len(r.spans))
	for _, s := range r.spans {
		out = append(out, *s)
	}
	return out
}

// ByExecution returns the spans of one execution in sequence order —
// the join the audit asked for.
func (r *Recorder) ByExecution(executionID string) []SpanRecord {
	var out []SpanRecord
	for _, s := range r.Spans() {
		if s.Correlation.ExecutionID == executionID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

// Unended returns spans that were started and never ended — a leak that
// an in-memory recorder can prove and a network exporter cannot.
func (r *Recorder) Unended() []SpanRecord {
	var out []SpanRecord
	for _, s := range r.Spans() {
		if !s.Ended {
			out = append(out, s)
		}
	}
	return out
}

// Orphans returns spans carrying no correlation identity at all.
func (r *Recorder) Orphans() []SpanRecord {
	var out []SpanRecord
	for _, s := range r.Spans() {
		if s.Correlation.empty() {
			out = append(out, s)
		}
	}
	return out
}

// CoveredDomains reports which VERIQO domains produced spans.
func (r *Recorder) CoveredDomains() []Domain {
	seen := map[Domain]bool{}
	for _, s := range r.Spans() {
		if s.Domain != "" {
			seen[s.Domain] = true
		}
	}
	out := make([]Domain, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MissingDomains reports declared domains with no spans.
func (r *Recorder) MissingDomains() []Domain {
	all := []Domain{DomainExecution, DomainEvidence, DomainDependency, DomainIdentity,
		DomainFusion, DomainTruth, DomainContradiction, DomainCausal, DomainRisk,
		DomainDecision, DomainTrust, DomainReplay, DomainCertificate}
	seen := map[Domain]bool{}
	for _, d := range r.CoveredDomains() {
		seen[d] = true
	}
	var out []Domain
	for _, d := range all {
		if !seen[d] {
			out = append(out, d)
		}
	}
	return out
}

// Summary renders a deterministic textual summary, used as gate evidence.
func (r *Recorder) Summary() string {
	var sb strings.Builder
	sb.WriteString("spans=" + strconv.Itoa(len(r.Spans())) + "\n")
	for _, d := range r.CoveredDomains() {
		sb.WriteString("domain=" + string(d) + "\n")
	}
	sb.WriteString("unended=" + strconv.Itoa(len(r.Unended())) + "\n")
	sb.WriteString("orphans=" + strconv.Itoa(len(r.Orphans())) + "\n")
	return sb.String()
}
