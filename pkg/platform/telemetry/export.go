package telemetry

// PHASE H (P1-11 + P1-12) — the export pipeline and the leakage
// boundary.
//
// Reconciliation first, per rule 0. The INSTRUMENTATION half already
// exists and is NOT rebuilt: veriqo_telemetry.go's Domain/Correlation/
// SpanRecord/Metric schema, its Recorder, and its own honest statement
// that it "is not an OTLP exporter and does not pretend to be" because
// "a fake exporter marked 'observability: done' is exactly the false
// green the assurance plane exists to prevent".
//
// This file is the transport adapter that comment anticipated:
//
//	VERIQO Recorder -> Exporter -> Collector -> Store -> Query
//
// Four things about it are deliberate and worth stating plainly.
//
// 1. It is OTel-COMPATIBLE, not OTel. The wire shape below is the
//    OpenTelemetry resource/scope/span JSON structure (resourceSpans ->
//    scopeSpans -> spans, with key/value attributes), hand-built,
//    because this repository's zero_dependency gate is a real, enforced
//    architectural property and importing go.opentelemetry.io would
//    trade it away. What this buys is that a real OTLP/HTTP collector
//    can consume this payload; what it does NOT buy is conformance
//    testing against a real collector, which nothing in this sandbox
//    can perform. The gate reports INTERNAL_QUALIFIED for exactly that
//    reason and never more.
//
// 2. The Collector and Store here are in-process. That is honest
//    infrastructure for testing the pipeline's semantics (batching,
//    redaction, ordering, queryability) end to end. It is NOT a claim
//    that a production collector deployment exists. It does not.
//
// 3. Redaction is applied at the EXPORTER, not at the call site. A
//    redaction policy that depends on every caller remembering to
//    redact is not a policy. Everything crossing this boundary is
//    filtered by one Redactor, and the leakage suite
//    (export_leakage_test.go) proves the filter holds for every
//    sensitive class the program names.
//
// 4. The default posture is deny. Redactor's zero value redacts every
//    attribute whose key is not on an explicit allow-list, because the
//    failure mode of a deny-list is silently leaking the one field
//    nobody thought of.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// ExporterSchemaVersion identifies this wire contract.
const ExporterSchemaVersion = "veriqo.telemetry.export/v1"

// RedactedPlaceholder is what replaces a redacted value. It is a fixed
// constant rather than an empty string so a reader can tell "this field
// was removed on purpose" from "this field was never set".
const RedactedPlaceholder = "[REDACTED]"

var (
	// ErrExporterClosed is returned by an exporter that has been shut
	// down. Exporting into a closed exporter is a bug worth surfacing,
	// not a no-op worth hiding.
	ErrExporterClosed = errors.New("telemetry: exporter is closed")
	// ErrNoSink refuses an exporter with nowhere to send. A silently
	// discarding exporter is the worst possible observability outcome:
	// everything looks instrumented and nothing is recorded.
	ErrNoSink = errors.New("telemetry: exporter requires a sink")
)

// --- redaction --------------------------------------------------------

// SensitiveClass names one category of data that must never reach a
// trace, a log or a metric label. The list is the program's own
// enumeration.
type SensitiveClass string

const (
	ClassSecret               SensitiveClass = "SECRET"             // private keys, tokens, passwords
	ClassPII                  SensitiveClass = "PII"                // names, addresses, national IDs, contact details
	ClassRestrictedPayload    SensitiveClass = "RESTRICTED_PAYLOAD" // raw B/L, restricted AIS payloads
	ClassCommercial           SensitiveClass = "COMMERCIAL"         // prices, rates, contract values
	ClassCustomerConfidential SensitiveClass = "CUSTOMER_CONFIDENTIAL"
)

// AllSensitiveClasses is the declared inventory the leakage suite
// iterates over, so adding a class without adding detection for it is a
// test failure rather than an oversight.
func AllSensitiveClasses() []SensitiveClass {
	return []SensitiveClass{
		ClassSecret, ClassPII, ClassRestrictedPayload,
		ClassCommercial, ClassCustomerConfidential,
	}
}

// defaultAllowedKeys is the allow-list: the ONLY attribute keys that
// travel with their values intact. Every one is a correlation
// identifier, a stage name, a hash, or a status — all of which are
// opaque by construction and carry no business content.
//
// This is deliberately short. Anything not here is redacted, which
// means adding a genuinely-safe attribute requires an explicit,
// reviewable edit to this list rather than happening by default.
var defaultAllowedKeys = map[string]bool{
	"execution_id": true, "case_id": true, "tenant_id": true,
	"evidence_package_id": true, "replay_package_id": true,
	"decision_id": true, "certificate_id": true, "intent_id": true,
	"entity_id": true, "verification_certificate_id": true,
	"entity_identity_ledger_head": true,
	"stage":                       true, "stage_id": true, "status": true, "domain": true,
	"policy_version": true, "policy_hash": true, "schema_version": true,
	"source_id": true, "predicate": true, "gate_id": true,
	"legacy_identity_fallback_used": true, "human_review_required": true,
	"unmapped_alias_kinds": true,
	"result":               true, "outcome": true, "error_kind": true,
	"node_hash": true, "root_hash": true, "tick": true,
}

// valuePatterns catch sensitive content that arrives under an ALLOWED
// key — the case an allow-list alone cannot cover. A correlation ID
// field containing a PEM private key is still a leak, and the
// allow-list would have waved it through.
//
// These are a defence in depth, not the primary mechanism. Each is
// deliberately conservative: false positives cost a redacted debug
// field, false negatives cost a leaked secret.
var valuePatterns = []struct {
	class   SensitiveClass
	name    string
	pattern *regexp.Regexp
}{
	{ClassSecret, "pem private key", regexp.MustCompile(`(?i)-----BEGIN[ A-Z]*PRIVATE KEY-----`)},
	{ClassSecret, "bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{12,}`)},
	{ClassSecret, "jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{4,}`)},
	{ClassSecret, "aws access key id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{ClassSecret, "generic api key assignment", regexp.MustCompile(`(?i)\b(api[_-]?key|secret|password|passwd|token)\b\s*[:=]\s*\S{6,}`)},
	{ClassPII, "email address", regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)},
	{ClassPII, "iban", regexp.MustCompile(`\b[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}\b`)},
	{ClassPII, "payment card number", regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)},
	{ClassRestrictedPayload, "bill of lading body", regexp.MustCompile(`(?i)\b(shipper|consignee|notify\s+party)\b\s*:`)},
	{ClassRestrictedPayload, "raw ais nmea sentence", regexp.MustCompile(`!AIVD[MO],\d`)},
	{ClassCommercial, "priced amount", regexp.MustCompile(`(?i)\b(?:usd|eur|gbp)\s?[0-9][0-9,.]*\b`)},
	{ClassCommercial, "rate or price field", regexp.MustCompile(`(?i)\b(unit_price|freight_rate|contract_value|premium_amount)\b`)},
	{ClassCustomerConfidential, "customer-confidential marking", regexp.MustCompile(`(?i)\b(customer[_-]confidential|commercial[_-]in[_-]confidence)\b`)},
}

// Finding is one redaction the exporter performed, recorded so the
// pipeline can report HOW MUCH it redacted without recording WHAT it
// redacted. The value never appears in a Finding — only its class, its
// key and a salted digest, which is enough to correlate repeat
// occurrences without being able to recover the content.
type Finding struct {
	Key    string         `json:"key"`
	Class  SensitiveClass `json:"class"`
	Reason string         `json:"reason"`
	// ValueDigest is a SHA-256 over the redacted value. It exists so an
	// operator can tell "the same secret leaked 400 times" from "400
	// different secrets leaked", without the digest being reversible to
	// anything useful for an attacker who does not already know the
	// value.
	ValueDigest string `json:"value_digest"`
}

// Redactor decides what crosses the export boundary. Its zero value is
// safe: it uses the default allow-list and every value pattern.
type Redactor struct {
	// AllowedKeys overrides defaultAllowedKeys when non-nil. Supplying
	// an EMPTY non-nil map means "allow nothing", which is a legitimate
	// maximum-paranoia posture, not a mistake.
	AllowedKeys map[string]bool
	// DisableValueScanning turns off the value patterns. It exists so a
	// deployment with a genuinely different threat model can opt out
	// explicitly; the leakage suite proves what that costs.
	DisableValueScanning bool
}

func (r Redactor) allowed(key string) bool {
	if r.AllowedKeys != nil {
		return r.AllowedKeys[key]
	}
	return defaultAllowedKeys[key]
}

// Inspect classifies one key/value pair without mutating anything. It
// returns the value to emit and, when redaction occurred, the Finding
// describing it.
func (r Redactor) Inspect(key, value string) (string, *Finding) {
	if !r.allowed(key) {
		// The redaction is already decided by the allow-list. The value
		// patterns still run, but only to CLASSIFY it: a class derived
		// from the content is more accurate than one guessed from the
		// key, and an accurate class is what makes the redaction tally
		// worth reading. Nothing about whether to redact depends on
		// this, which is why it runs even when value scanning is
		// disabled for the allow-listed path.
		class, reason := ClassSecret, ""
		if p, ok := matchValuePattern(value); ok {
			class = p.class
			reason = "attribute key is not on the export allow-list (default-deny); value also matched the " + p.name + " pattern"
		} else {
			class = classifyKey(key)
			reason = "attribute key is not on the export allow-list (default-deny)"
		}
		return RedactedPlaceholder, &Finding{
			Key: key, Class: class, Reason: reason, ValueDigest: digest(value),
		}
	}
	if r.DisableValueScanning {
		return value, nil
	}
	if p, ok := matchValuePattern(value); ok {
		return RedactedPlaceholder, &Finding{
			Key: key, Class: p.class,
			Reason:      "value matched the " + p.name + " pattern under an allow-listed key",
			ValueDigest: digest(value),
		}
	}
	return value, nil
}

// matchValuePattern returns the first sensitive pattern a value
// matches. Order in valuePatterns is therefore significant and
// deliberate: the more specific classes are listed before the broader
// ones.
func matchValuePattern(value string) (struct {
	class   SensitiveClass
	name    string
	pattern *regexp.Regexp
}, bool) {
	for _, p := range valuePatterns {
		if p.pattern.MatchString(value) {
			return p, true
		}
	}
	return struct {
		class   SensitiveClass
		name    string
		pattern *regexp.Regexp
	}{}, false
}

// classifyKey guesses a sensitive class from an attribute key alone,
// for the default-deny path where no value pattern matched. It is a
// best-effort LABEL on a redaction that has already happened — the
// redaction does not depend on it.
func classifyKey(key string) SensitiveClass {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "secret"), strings.Contains(k, "token"),
		strings.Contains(k, "password"), strings.Contains(k, "key"),
		strings.Contains(k, "credential"):
		return ClassSecret
	case strings.Contains(k, "email"), strings.Contains(k, "name"),
		strings.Contains(k, "address"), strings.Contains(k, "phone"),
		strings.Contains(k, "passport"), strings.Contains(k, "national_id"):
		return ClassPII
	case strings.Contains(k, "price"), strings.Contains(k, "rate"),
		strings.Contains(k, "amount"), strings.Contains(k, "value"),
		strings.Contains(k, "premium"):
		return ClassCommercial
	case strings.Contains(k, "payload"), strings.Contains(k, "raw"),
		strings.Contains(k, "body"), strings.Contains(k, "document"):
		return ClassRestrictedPayload
	case strings.Contains(k, "customer"), strings.Contains(k, "client"):
		return ClassCustomerConfidential
	default:
		return ClassRestrictedPayload
	}
}

func digest(v string) string {
	sum := sha256.Sum256([]byte("veriqo.telemetry.redaction/v1|" + v))
	return "sha256:" + hex.EncodeToString(sum[:8])
}

// --- OTel-compatible wire shape ---------------------------------------

// KeyValue is OTel's attribute encoding.
type KeyValue struct {
	Key   string   `json:"key"`
	Value AnyValue `json:"value"`
}

// AnyValue is OTel's value wrapper. Only the string form is emitted:
// every VERIQO attribute is already a string, and inventing typed
// variants would mean guessing types at the boundary.
type AnyValue struct {
	StringValue string `json:"stringValue"`
}

// ExportedSpan is one span in the OTel-compatible shape.
type ExportedSpan struct {
	TraceID           string     `json:"traceId"`
	SpanID            string     `json:"spanId"`
	ParentSpanID      string     `json:"parentSpanId,omitempty"`
	Name              string     `json:"name"`
	Attributes        []KeyValue `json:"attributes"`
	Status            string     `json:"status"`
	DroppedAttributes int        `json:"droppedAttributesCount"`
}

// ScopeSpans groups spans by instrumentation scope (here: the VERIQO
// domain).
type ScopeSpans struct {
	Scope struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"scope"`
	Spans []ExportedSpan `json:"spans"`
}

// ResourceSpans is the top-level OTLP structure.
type ResourceSpans struct {
	Resource struct {
		Attributes []KeyValue `json:"attributes"`
	} `json:"resource"`
	ScopeSpans []ScopeSpans `json:"scopeSpans"`
}

// Payload is one export batch.
type Payload struct {
	SchemaVersion string          `json:"schema_version"`
	ResourceSpans []ResourceSpans `json:"resourceSpans"`
	// Redactions reports how much was redacted, by class. It carries no
	// redacted content — see Finding.
	Redactions map[SensitiveClass]int `json:"redactions,omitempty"`
	Findings   []Finding              `json:"findings,omitempty"`
}

// JSON renders the payload deterministically.
func (p Payload) JSON() ([]byte, error) { return json.MarshalIndent(p, "", "  ") }

// --- exporter / collector / store / query ------------------------------

// Sink is where an exporter delivers batches. A real deployment
// implements this over OTLP/HTTP to a collector endpoint; Collector
// below implements it in-process for testing the pipeline's semantics.
type Sink interface {
	Consume(Payload) error
}

// Exporter converts Recorder spans into OTel-compatible payloads,
// applying redaction at this boundary and nowhere else.
type Exporter struct {
	mu       sync.Mutex
	sink     Sink
	redactor Redactor
	service  string
	closed   bool
	// BatchSize caps how many spans travel in one payload. Zero means
	// one payload per Export call regardless of size.
	BatchSize int
}

// NewExporter builds an exporter over a sink. A nil sink is refused
// rather than silently discarding: see ErrNoSink.
func NewExporter(service string, sink Sink, r Redactor) (*Exporter, error) {
	if sink == nil {
		return nil, ErrNoSink
	}
	if strings.TrimSpace(service) == "" {
		service = "veriqo"
	}
	return &Exporter{sink: sink, redactor: r, service: service}, nil
}

// Export converts and delivers spans. It returns the number of spans
// exported and the redaction tally, so a caller can assert on both.
func (e *Exporter) Export(spans []SpanRecord) (int, map[SensitiveClass]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, nil, ErrExporterClosed
	}

	batches := [][]SpanRecord{spans}
	if e.BatchSize > 0 && len(spans) > e.BatchSize {
		batches = nil
		for i := 0; i < len(spans); i += e.BatchSize {
			end := i + e.BatchSize
			if end > len(spans) {
				end = len(spans)
			}
			batches = append(batches, spans[i:end])
		}
	}

	total := 0
	tally := map[SensitiveClass]int{}
	for _, batch := range batches {
		payload, findings := e.buildPayload(batch)
		for _, f := range findings {
			tally[f.Class]++
		}
		if err := e.sink.Consume(payload); err != nil {
			return total, tally, fmt.Errorf("telemetry: sink: %w", err)
		}
		total += len(batch)
	}
	return total, tally, nil
}

func (e *Exporter) buildPayload(spans []SpanRecord) (Payload, []Finding) {
	byDomain := map[Domain][]ExportedSpan{}
	var findings []Finding

	for _, s := range spans {
		out := ExportedSpan{
			TraceID: traceIDFor(s.Correlation),
			SpanID:  spanIDFor(s),
			Name:    s.Name,
			Status:  spanStatus(s),
		}
		if s.ParentSeq > 0 {
			out.ParentSpanID = spanIDForSeq(s.ParentSeq)
		}
		// Correlation fields travel through the SAME redactor as every
		// other attribute -- there is no privileged path.
		for _, a := range s.Correlation.attributes() {
			value, f := e.redactor.Inspect(a.Key, a.Value)
			if f != nil {
				findings = append(findings, *f)
				out.DroppedAttributes++
			}
			out.Attributes = append(out.Attributes, kv(a.Key, value))
		}
		for _, a := range s.Attributes {
			value, f := e.redactor.Inspect(a.Key, a.Value)
			if f != nil {
				findings = append(findings, *f)
				out.DroppedAttributes++
			}
			out.Attributes = append(out.Attributes, kv(a.Key, value))
		}
		// Error strings are free text produced by arbitrary code paths,
		// so they are the single most likely leak vector. They go
		// through the redactor under a key that is deliberately NOT on
		// the allow-list, which means the default posture redacts them
		// entirely.
		for _, msg := range s.Errors {
			value, f := e.redactor.Inspect("error_message", msg)
			if f != nil {
				findings = append(findings, *f)
				out.DroppedAttributes++
			}
			out.Attributes = append(out.Attributes, kv("error_message", value))
		}
		sort.Slice(out.Attributes, func(i, j int) bool { return out.Attributes[i].Key < out.Attributes[j].Key })
		byDomain[s.Domain] = append(byDomain[s.Domain], out)
	}

	rs := ResourceSpans{}
	rs.Resource.Attributes = []KeyValue{
		kv("service.name", e.service),
		kv("veriqo.schema", ExporterSchemaVersion),
	}
	domains := make([]Domain, 0, len(byDomain))
	for d := range byDomain {
		domains = append(domains, d)
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i] < domains[j] })
	for _, d := range domains {
		ss := ScopeSpans{Spans: byDomain[d]}
		ss.Scope.Name = "veriqo/" + string(d)
		ss.Scope.Version = ExporterSchemaVersion
		rs.ScopeSpans = append(rs.ScopeSpans, ss)
	}

	p := Payload{SchemaVersion: ExporterSchemaVersion, ResourceSpans: []ResourceSpans{rs}, Findings: findings}
	if len(findings) > 0 {
		p.Redactions = map[SensitiveClass]int{}
		for _, f := range findings {
			p.Redactions[f.Class]++
		}
	}
	return p, findings
}

// Close marks the exporter shut down. Subsequent exports fail loudly.
func (e *Exporter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
}

func kv(k, v string) KeyValue { return KeyValue{Key: k, Value: AnyValue{StringValue: v}} }

func spanStatus(s SpanRecord) string {
	switch {
	case len(s.Errors) > 0:
		return "STATUS_CODE_ERROR"
	case s.Ended:
		return "STATUS_CODE_OK"
	default:
		return "STATUS_CODE_UNSET"
	}
}

// traceIDFor derives a stable 128-bit trace ID from the correlation
// key, so every span of one execution shares a trace without a
// distributed ID generator. Deterministic by design: this repository
// has no unseeded randomness in its deterministic packages.
func traceIDFor(c Correlation) string {
	seed := c.ExecutionID
	if seed == "" {
		seed = c.CaseID
	}
	if seed == "" {
		seed = "unjoinable"
	}
	sum := sha256.Sum256([]byte("veriqo.trace/v1|" + seed))
	return hex.EncodeToString(sum[:16])
}

func spanIDFor(s SpanRecord) string { return spanIDForSeq(s.Sequence) }

func spanIDForSeq(seq int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("veriqo.span/v1|%d", seq)))
	return hex.EncodeToString(sum[:8])
}

// Collector is an in-process Sink that stores what it receives and
// answers queries about it. It is the "collector -> storage -> query"
// third of the pipeline, made real enough to test the semantics end to
// end — and it is NOT a production collector deployment.
type Collector struct {
	mu       sync.Mutex
	payloads []Payload
	spans    []ExportedSpan
	byTrace  map[string][]ExportedSpan
}

// NewCollector returns an empty collector.
func NewCollector() *Collector {
	return &Collector{byTrace: map[string][]ExportedSpan{}}
}

// Consume implements Sink.
func (c *Collector) Consume(p Payload) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.payloads = append(c.payloads, p)
	for _, rs := range p.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				c.spans = append(c.spans, s)
				c.byTrace[s.TraceID] = append(c.byTrace[s.TraceID], s)
			}
		}
	}
	return nil
}

// SpanCount is the persistence half: how many spans actually landed.
func (c *Collector) SpanCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.spans)
}

// PayloadCount reports how many batches arrived.
func (c *Collector) PayloadCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.payloads)
}

// QueryByTrace is the queryability half: retrieve one execution's whole
// trace by its derived trace ID.
func (c *Collector) QueryByTrace(traceID string) []ExportedSpan {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ExportedSpan, len(c.byTrace[traceID]))
	copy(out, c.byTrace[traceID])
	return out
}

// QueryByExecution is the join callers actually want: every span of one
// execution, found by the same correlation identifier the rest of the
// system uses.
func (c *Collector) QueryByExecution(executionID string) []ExportedSpan {
	return c.QueryByTrace(traceIDFor(Correlation{ExecutionID: executionID}))
}

// TraceIDs lists every stored trace, deterministically.
func (c *Collector) TraceIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.byTrace))
	for id := range c.byTrace {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// AllValues returns every attribute value the collector holds. It is
// the leakage suite's primary assertion surface: whatever a test put
// into a span, if it can be found here, it left the process.
func (c *Collector) AllValues() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, s := range c.spans {
		out = append(out, s.Name, s.Status, s.TraceID, s.SpanID)
		for _, a := range s.Attributes {
			out = append(out, a.Key, a.Value.StringValue)
		}
	}
	return out
}

// Qualification is the honest status of this pipeline. It is a
// constant, not a computation, precisely because the thing that would
// raise it is a real collector deployment, which no code path here can
// create.
type Qualification struct {
	Status    string   `json:"status"`
	Proven    []string `json:"proven"`
	NotProven []string `json:"not_proven"`
	RaisedBy  string   `json:"raised_by"`
}

// PipelineQualification states exactly what this file's tests
// establish and what they do not. INTERNAL_QUALIFIED is the ceiling: an
// in-process collector proves the semantics, and proves nothing about
// production observability.
func PipelineQualification() Qualification {
	return Qualification{
		Status: "INTERNAL_QUALIFIED",
		Proven: []string{
			"spans convert to an OpenTelemetry-shaped resource/scope/span payload",
			"redaction is applied at the export boundary, on every attribute, with no privileged path",
			"batches are delivered to a sink, persisted, and retrievable by trace and by execution id",
			"a closed exporter and a nil sink both fail loudly rather than silently discarding",
		},
		NotProven: []string{
			"conformance against a real OpenTelemetry collector implementation",
			"delivery over a real network, including backpressure, retry and partial-batch failure",
			"retention, cardinality and query performance at production volume",
			"that any production deployment of a collector exists at all",
		},
		RaisedBy: "a real OTLP collector endpoint plus a deployment that exports to it under production load; " +
			"no code change in this repository can raise it",
	}
}
