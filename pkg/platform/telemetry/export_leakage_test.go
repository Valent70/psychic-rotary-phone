package telemetry

// PHASE H (P1-12) — the data-leakage suite.
//
// The program's acceptance criterion is three zeros: secret leakage =
// 0, PII leakage = 0, restricted payload leakage = 0. This file proves
// them the only way they can honestly be proved — by putting real
// sensitive-looking values into real spans, running them through the
// real exporter into the real collector, and then searching everything
// the collector holds for those exact values.
//
// The assertion is deliberately "the secret does not appear ANYWHERE in
// the collector", not "the attribute was redacted". Checking the
// specific field would pass even if the same value leaked through a
// different field, which is exactly the failure mode a leakage suite
// exists to catch.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// leakCanary is one sensitive value plus the class it belongs to. The
// Value strings are constructed to look real to a scanner while being
// obviously synthetic to a human reader: none of them is, or has ever
// been, a live credential.
type leakCanary struct {
	class SensitiveClass
	key   string
	value string
	// note explains what real-world data this stands in for.
	note string
}

func canaries() []leakCanary {
	return []leakCanary{
		{ClassSecret, "signing_private_key",
			"-----BEGIN PRIVATE KEY-----\nMIIBVAIBADANBgkqhkiG9w0BAQEFAASCAT4wggE6AgEAAkEA\n-----END PRIVATE KEY-----",
			"a PEM private key of the kind pkg/platform/security/keys handles"},
		{ClassSecret, "authorization",
			"Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			"a JWT bearer token of the kind pkg/api verifies"},
		{ClassSecret, "cloud_credential", "AKIAIOSFODNN7EXAMPLE",
			"an AWS access key id of the kind the KMS blocker's credential retest used"},
		{ClassSecret, "config_line", "api_key=sk-live-9f3a2b7c1d4e6f8a0b2c",
			"a credential embedded in a config or error string"},
		{ClassPII, "master_email", "captain.andersen@example-shipping.test",
			"a named individual's contact details from a crew or ownership record"},
		{ClassPII, "beneficiary_iban", "GB29NWBK60161331926819",
			"a bank account from a payment record (pkg/connector/payment)"},
		{ClassPII, "card_number", "4111 1111 1111 1111",
			"a payment card number"},
		{ClassRestrictedPayload, "bl_document_body",
			"SHIPPER: Acme Trading Ltd\nCONSIGNEE: Beta Imports SA\nNOTIFY PARTY: Gamma Agents",
			"a raw bill-of-lading body (pkg/connector/bol operates on these)"},
		{ClassRestrictedPayload, "ais_raw_payload", "!AIVDM,1,1,,A,15MgK45P3@G?fl0E`JbR0OwT0@MS,0*4E",
			"a restricted raw AIS NMEA sentence (pkg/connector/aisstream operates on these)"},
		{ClassCommercial, "settlement_value", "USD 4,250,000.00",
			"a contract value from a commercial record"},
		{ClassCommercial, "pricing_field", "freight_rate",
			"a commercial pricing field name from pkg/moat/intelligence/pricing"},
		{ClassCustomerConfidential, "handling_marking", "CUSTOMER-CONFIDENTIAL: do not disclose to third parties",
			"a customer-confidential handling marking"},
	}
}

// exportThrough runs spans through a real exporter into a real
// collector with the DEFAULT (zero-value) redactor — the posture a
// deployment gets without configuring anything.
func exportThrough(t *testing.T, spans []SpanRecord) (*Collector, map[SensitiveClass]int) {
	t.Helper()
	c := NewCollector()
	e, err := NewExporter("veriqo-leakage-test", c, Redactor{})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	n, tally, err := e.Export(spans)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if n != len(spans) {
		t.Fatalf("exported %d spans, submitted %d", n, len(spans))
	}
	return c, tally
}

// TestNoSensitiveValueLeavesTheProcessInATraceAttribute is the
// program's three zeros, proved across every sensitive class at once.
func TestNoSensitiveValueLeavesTheProcessInATraceAttribute(t *testing.T) {
	all := canaries()
	span := SpanRecord{
		Name: "execution.run", Domain: DomainExecution, Ended: true, Sequence: 1,
		Correlation: Correlation{ExecutionID: "exec-1", CaseID: "case-1", TenantID: "acme"},
	}
	for _, c := range all {
		span.Attributes = append(span.Attributes, Attribute{Key: c.key, Value: c.value})
	}

	collector, tally := exportThrough(t, []SpanRecord{span})

	haystack := strings.Join(collector.AllValues(), "\n")
	for _, c := range all {
		if strings.Contains(haystack, c.value) {
			t.Errorf("%s leaked: %s (%s) reached the collector verbatim", c.class, c.key, c.note)
		}
		// Substring check too: a partial leak of a private key body is
		// still a leak.
		if frag := significantFragment(c.value); frag != "" && strings.Contains(haystack, frag) {
			t.Errorf("%s partially leaked: a distinctive fragment of %s reached the collector", c.class, c.key)
		}
	}

	// Every class the program names must actually have been exercised,
	// so a future refactor cannot quietly stop testing one.
	for _, class := range AllSensitiveClasses() {
		if tally[class] == 0 {
			t.Errorf("no redaction was recorded for class %s -- either the canary set or the detection stopped covering it", class)
		}
	}
}

// significantFragment returns a distinctive middle slice of a value,
// used to catch partial leaks. Short values return "" because a short
// fragment would false-positive against ordinary text.
func significantFragment(v string) string {
	trimmed := strings.TrimSpace(v)
	if len(trimmed) < 24 {
		return ""
	}
	mid := len(trimmed) / 2
	return trimmed[mid-8 : mid+8]
}

// TestSecretsInErrorMessagesDoNotLeak covers the highest-risk vector:
// error strings are free text produced by arbitrary code paths, and a
// wrapped error carrying a credential is the classic way a secret ends
// up in a trace.
func TestSecretsInErrorMessagesDoNotLeak(t *testing.T) {
	secret := "sk-live-9f3a2b7c1d4e6f8a0b2c3d5e"
	span := SpanRecord{
		Name: "evidence.submit", Domain: DomainEvidence, Ended: true, Sequence: 1,
		Correlation: Correlation{ExecutionID: "exec-2"},
		Errors: []string{
			"connector: authenticating to upstream feed: api_key=" + secret + " rejected",
			"payment: parsing record for beneficiary_iban=GB29NWBK60161331926819 failed",
		},
	}
	collector, _ := exportThrough(t, []SpanRecord{span})

	haystack := strings.Join(collector.AllValues(), "\n")
	for _, forbidden := range []string{secret, "GB29NWBK60161331926819"} {
		if strings.Contains(haystack, forbidden) {
			t.Errorf("an error message carried %q out of the process", forbidden)
		}
	}
	// The span itself still arrived, and still reports that it errored:
	// redaction must not silently drop the observation.
	spans := collector.QueryByExecution("exec-2")
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1 -- redaction must not discard the span", len(spans))
	}
	if spans[0].Status != "STATUS_CODE_ERROR" {
		t.Errorf("status = %q, want STATUS_CODE_ERROR -- the fact of the error must survive redaction", spans[0].Status)
	}
	if spans[0].DroppedAttributes == 0 {
		t.Error("no dropped-attribute count recorded; an operator cannot tell redaction happened")
	}
}

// TestCorrelationIdentifiersSurviveRedaction is the counterweight to
// every test above: a redactor that removed everything would trivially
// pass a leakage suite and destroy observability. The identifiers the
// whole system joins on must come through intact.
func TestCorrelationIdentifiersSurviveRedaction(t *testing.T) {
	span := SpanRecord{
		Name: "execution.run", Domain: DomainExecution, Ended: true, Sequence: 1,
		Correlation: Correlation{
			ExecutionID: "exec-3", CaseID: "case-3", TenantID: "acme",
			EvidencePackageID: "evp-3", ReplayPackageID: "rpkg-3",
			DecisionID: "dec-3", CertificateID: "cert-3",
		},
		Attributes: []Attribute{
			{Key: "stage", Value: "TRUTH_ARBITRATION"},
			{Key: "policy_hash", Value: "sha256:abc123"},
			{Key: "status", Value: "OK"},
		},
	}
	collector, _ := exportThrough(t, []SpanRecord{span})

	got := collector.QueryByExecution("exec-3")
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	values := map[string]string{}
	for _, a := range got[0].Attributes {
		values[a.Key] = a.Value.StringValue
	}
	for key, want := range map[string]string{
		"execution_id": "exec-3", "case_id": "case-3", "tenant_id": "acme",
		"evidence_package_id": "evp-3", "replay_package_id": "rpkg-3",
		"decision_id": "dec-3", "certificate_id": "cert-3",
		"stage": "TRUTH_ARBITRATION", "policy_hash": "sha256:abc123", "status": "OK",
	} {
		if values[key] != want {
			t.Errorf("%s = %q, want %q -- redaction destroyed an identifier the system joins on", key, values[key], want)
		}
	}
}

// TestDefaultPostureIsDenyNotAllow proves the allow-list, not a
// deny-list, is what protects the boundary: an attribute nobody
// anticipated is redacted by default.
func TestDefaultPostureIsDenyNotAllow(t *testing.T) {
	span := SpanRecord{
		Name: "some.new.operation", Domain: DomainRisk, Ended: true, Sequence: 1,
		Correlation: Correlation{ExecutionID: "exec-4"},
		Attributes: []Attribute{
			{Key: "a_field_nobody_thought_about", Value: "quite possibly sensitive"},
		},
	}
	collector, _ := exportThrough(t, []SpanRecord{span})
	if strings.Contains(strings.Join(collector.AllValues(), "\n"), "quite possibly sensitive") {
		t.Fatal("an unanticipated attribute travelled with its value intact -- the boundary is deny-listed, not allow-listed")
	}
}

// TestSensitiveValueUnderAnAllowListedKeyIsStillCaught is the
// defence-in-depth case: an allow-list alone cannot help when a secret
// arrives in a field that is normally safe.
func TestSensitiveValueUnderAnAllowListedKeyIsStillCaught(t *testing.T) {
	span := SpanRecord{
		Name: "execution.run", Domain: DomainExecution, Ended: true, Sequence: 1,
		Correlation: Correlation{ExecutionID: "exec-5"},
		Attributes: []Attribute{
			// "status" is on the allow-list; the value is not safe.
			{Key: "status", Value: "failed: Bearer eyJhbGciOiJIUzI1NiJ9.eyJhIjoxfQ.abcdefgh"},
		},
	}
	collector, tally := exportThrough(t, []SpanRecord{span})
	if strings.Contains(strings.Join(collector.AllValues(), "\n"), "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatal("a token under an allow-listed key reached the collector")
	}
	if tally[ClassSecret] == 0 {
		t.Error("the value-pattern scan did not record a SECRET redaction")
	}
}

// TestFindingsNeverCarryTheRedactedValue: the redaction report must not
// itself become the leak.
func TestFindingsNeverCarryTheRedactedValue(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	c := NewCollector()
	e, err := NewExporter("veriqo", c, Redactor{})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	if _, _, err := e.Export([]SpanRecord{{
		Name: "x", Domain: DomainExecution, Ended: true, Sequence: 1,
		Correlation: Correlation{ExecutionID: "exec-6"},
		Attributes:  []Attribute{{Key: "cloud_credential", Value: secret}},
	}}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	payload := c.payloads[0]
	raw, err := payload.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("the serialized payload -- including its own redaction findings -- carries the secret")
	}
	if len(payload.Findings) == 0 {
		t.Fatal("no finding recorded")
	}
	for _, f := range payload.Findings {
		if strings.Contains(f.ValueDigest, secret) || f.ValueDigest == "" {
			t.Errorf("finding digest %q is unusable or reversible", f.ValueDigest)
		}
		if f.Reason == "" {
			t.Error("a finding with no reason is not actionable")
		}
	}
}

// TestDisablingValueScanningIsAnExplicitChoiceWithAMeasuredCost proves
// the opt-out is real and documents what it costs, rather than leaving
// a flag whose effect nobody has measured.
func TestDisablingValueScanningIsAnExplicitChoiceWithAMeasuredCost(t *testing.T) {
	span := SpanRecord{
		Name: "execution.run", Domain: DomainExecution, Ended: true, Sequence: 1,
		Correlation: Correlation{ExecutionID: "exec-7"},
		Attributes:  []Attribute{{Key: "status", Value: "failed: Bearer eyJhbGciOiJIUzI1NiJ9.eyJhIjoxfQ.abcdefgh"}},
	}
	c := NewCollector()
	e, err := NewExporter("veriqo", c, Redactor{DisableValueScanning: true})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	if _, _, err := e.Export([]SpanRecord{span}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !strings.Contains(strings.Join(c.AllValues(), "\n"), "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatal("DisableValueScanning had no effect -- either the flag is dead or the default is not what the other tests assume")
	}
	// The allow-list still holds even with scanning off: the two
	// mechanisms are independent.
	c2 := NewCollector()
	e2, err := NewExporter("veriqo", c2, Redactor{DisableValueScanning: true})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	if _, _, err := e2.Export([]SpanRecord{{
		Name: "x", Domain: DomainExecution, Ended: true, Sequence: 1,
		Correlation: Correlation{ExecutionID: "exec-8"},
		Attributes:  []Attribute{{Key: "unlisted_field", Value: "still sensitive"}},
	}}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if strings.Contains(strings.Join(c2.AllValues(), "\n"), "still sensitive") {
		t.Fatal("turning off value scanning also disabled the allow-list; the two must be independent")
	}
}

// TestEmptyAllowListRedactsEverything covers the maximum-paranoia
// posture, so it is a supported configuration rather than an accident.
func TestEmptyAllowListRedactsEverything(t *testing.T) {
	c := NewCollector()
	e, err := NewExporter("veriqo", c, Redactor{AllowedKeys: map[string]bool{}})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	if _, _, err := e.Export([]SpanRecord{{
		Name: "x", Domain: DomainExecution, Ended: true, Sequence: 1,
		Correlation: Correlation{ExecutionID: "exec-9"},
		Attributes:  []Attribute{{Key: "stage", Value: "TRUTH_ARBITRATION"}},
	}}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	for _, v := range c.AllValues() {
		if v == "exec-9" || v == "TRUTH_ARBITRATION" {
			t.Fatalf("an empty allow-list still emitted %q", v)
		}
	}
}

// --- P1-11: the pipeline itself ---------------------------------------

// TestExportPipelineDeliversPersistsAndQueries proves the four stages
// the program names -- exporter, collector, persistence, queryability
// -- actually connect, using the real Recorder as the source.
func TestExportPipelineDeliversPersistsAndQueries(t *testing.T) {
	rec := NewRecorder()
	for i, domain := range []Domain{DomainExecution, DomainEvidence, DomainDecision} {
		_, span := rec.StartVeriqoSpan(context.Background(), "stage-"+string(domain), domain,
			Correlation{ExecutionID: "exec-pipeline", CaseID: "case-pipeline"},
			Attribute{Key: "stage", Value: string(domain)},
			Attribute{Key: "tick", Value: "42"})
		_ = i
		span.End()
	}
	spans := rec.Spans()
	if len(spans) != 3 {
		t.Fatalf("recorder holds %d spans, want 3", len(spans))
	}

	collector, _ := exportThrough(t, spans)

	if collector.SpanCount() != 3 {
		t.Fatalf("collector persisted %d spans, want 3", collector.SpanCount())
	}
	if collector.PayloadCount() != 1 {
		t.Fatalf("collector received %d payloads, want 1", collector.PayloadCount())
	}
	joined := collector.QueryByExecution("exec-pipeline")
	if len(joined) != 3 {
		t.Fatalf("querying by execution id returned %d spans, want 3 -- the join the whole system relies on is broken", len(joined))
	}
	if ids := collector.TraceIDs(); len(ids) != 1 {
		t.Fatalf("3 spans of one execution produced %d trace ids, want 1", len(ids))
	}
}

func TestExporterBatchesLargeSpanSets(t *testing.T) {
	var spans []SpanRecord
	for i := 0; i < 25; i++ {
		spans = append(spans, SpanRecord{
			Name: "s", Domain: DomainExecution, Ended: true, Sequence: i + 1,
			Correlation: Correlation{ExecutionID: "exec-batch"},
		})
	}
	c := NewCollector()
	e, err := NewExporter("veriqo", c, Redactor{})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	e.BatchSize = 10

	n, _, err := e.Export(spans)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if n != 25 {
		t.Fatalf("exported %d, want 25", n)
	}
	if c.PayloadCount() != 3 {
		t.Fatalf("got %d payloads for 25 spans at batch size 10, want 3", c.PayloadCount())
	}
	if c.SpanCount() != 25 {
		t.Fatalf("collector persisted %d spans, want 25 -- batching lost data", c.SpanCount())
	}
}

// TestExporterFailsLoudlyRatherThanDiscarding covers the worst possible
// observability outcome: everything looks instrumented and nothing is
// recorded.
func TestExporterFailsLoudlyRatherThanDiscarding(t *testing.T) {
	if _, err := NewExporter("veriqo", nil, Redactor{}); !errors.Is(err, ErrNoSink) {
		t.Fatalf("a nil sink was accepted: %v", err)
	}
	c := NewCollector()
	e, err := NewExporter("veriqo", c, Redactor{})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	e.Close()
	if _, _, err := e.Export([]SpanRecord{{Name: "x", Domain: DomainExecution, Sequence: 1}}); !errors.Is(err, ErrExporterClosed) {
		t.Fatalf("a closed exporter silently accepted spans: %v", err)
	}
	if c.SpanCount() != 0 {
		t.Fatal("a closed exporter still delivered")
	}
}

// TestPayloadIsOTelShaped checks the wire structure a real collector
// would parse. It is a structural check, and it is explicitly NOT a
// conformance claim -- see PipelineQualification.
func TestPayloadIsOTelShaped(t *testing.T) {
	c := NewCollector()
	e, err := NewExporter("veriqo-service", c, Redactor{})
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	if _, _, err := e.Export([]SpanRecord{{
		Name: "execution.run", Domain: DomainExecution, Ended: true, Sequence: 1, ParentSeq: 0,
		Correlation: Correlation{ExecutionID: "exec-shape"},
		Attributes:  []Attribute{{Key: "stage", Value: "INTENT"}},
	}}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	p := c.payloads[0]
	if p.SchemaVersion != ExporterSchemaVersion {
		t.Errorf("SchemaVersion = %q", p.SchemaVersion)
	}
	if len(p.ResourceSpans) != 1 {
		t.Fatalf("got %d resourceSpans, want 1", len(p.ResourceSpans))
	}
	rs := p.ResourceSpans[0]
	var sawService bool
	for _, a := range rs.Resource.Attributes {
		if a.Key == "service.name" && a.Value.StringValue == "veriqo-service" {
			sawService = true
		}
	}
	if !sawService {
		t.Error("resource carries no service.name attribute")
	}
	if len(rs.ScopeSpans) != 1 {
		t.Fatalf("got %d scopeSpans, want 1", len(rs.ScopeSpans))
	}
	ss := rs.ScopeSpans[0]
	if ss.Scope.Name != "veriqo/execution" {
		t.Errorf("scope name = %q, want veriqo/execution", ss.Scope.Name)
	}
	span := ss.Spans[0]
	if len(span.TraceID) != 32 {
		t.Errorf("traceId is %d hex chars, want 32 (128-bit)", len(span.TraceID))
	}
	if len(span.SpanID) != 16 {
		t.Errorf("spanId is %d hex chars, want 16 (64-bit)", len(span.SpanID))
	}
	if span.Status != "STATUS_CODE_OK" {
		t.Errorf("status = %q", span.Status)
	}
}

// TestTraceIDIsStablePerExecution proves the join key is derived
// deterministically, so two processes exporting spans of the same
// execution agree on the trace without coordinating.
func TestTraceIDIsStablePerExecution(t *testing.T) {
	a := traceIDFor(Correlation{ExecutionID: "exec-stable"})
	b := traceIDFor(Correlation{ExecutionID: "exec-stable", CaseID: "different-case"})
	if a != b {
		t.Fatal("the trace id depends on something other than the execution id")
	}
	if a == traceIDFor(Correlation{ExecutionID: "exec-other"}) {
		t.Fatal("two different executions share a trace id")
	}
}

// TestPipelineQualificationIsHonestlyCapped is the anti-false-green
// assertion for this whole phase: nothing here may claim more than
// INTERNAL_QUALIFIED, and the reason must be stated.
func TestPipelineQualificationIsHonestlyCapped(t *testing.T) {
	q := PipelineQualification()
	if q.Status != "INTERNAL_QUALIFIED" {
		t.Fatalf("Status = %q -- an in-process collector can never establish more than INTERNAL_QUALIFIED", q.Status)
	}
	if len(q.NotProven) == 0 {
		t.Fatal("the qualification lists nothing as unproven, which cannot be true of an in-process pipeline")
	}
	if q.RaisedBy == "" {
		t.Fatal("the qualification does not say what would raise it")
	}
	if len(q.Proven) == 0 {
		t.Fatal("the qualification claims nothing proven, which would make the whole phase pointless")
	}
}
