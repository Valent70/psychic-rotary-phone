package canonical

import (
	"testing"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

func sharedCalculusForTest() *trustcalc.Calculus { return trustcalc.New(1, 1) }

func testPolicy() decision.Policy {
	// RunCanonical feeds decision.Engine via risk.ToDecisionValues,
	// which emits exactly ONE canonical factor
	// ("tbml_composite_risk_score" — see risk.ToDecisionValues'
	// doc comment on why: mechanical consistency between Risk.Score
	// and Decision.RiskScore). A policy must declare that same factor
	// name to actually consume it; risk.DefaultPolicy() is the
	// package's own reference policy for exactly this purpose.
	return risk.DefaultPolicy()
}

func testCase() CaseInput {
	return CaseInput{
		Entity: digitaltwin.EntityID("VESSEL:IMO9998887"), Subject: "VESSEL:IMO9998887",
		Predicate: "AIS_STATUS", Tick: 1, Policy: testPolicy(),
		Submissions: []SourceSubmission{
			{SourceID: fusion.SourceID("ais-vendor-a"), Value: "OFF", BaseReliability: 0.8, Provider: "sat-op-1"},
			{SourceID: fusion.SourceID("ais-vendor-b"), Value: "OFF", BaseReliability: 0.8, Provider: "sat-op-1"},
			{SourceID: fusion.SourceID("port-authority"), Value: "ON", BaseReliability: 0.95},
		},
	}
}

// TestRunCanonicalFullChain proves every named P0 layer actually runs,
// in order, from ONE call — the concrete "not two parallel islands"
// proof the V7.10.1 audit asked for.
func TestRunCanonicalFullChain(t *testing.T) {
	p := NewPipeline(nil)
	in := testCase()
	in.CausalLinks = []CausalLink{{Cause: causal.NodeID("source:port-authority"), Strength: 0.9}}
	in.EconomicChannels = map[string]float64{"commodity_flow_usd": 1_000_000}
	in.PatternScore = 0.7

	res, err := p.RunCanonical("case-1", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}

	if !res.Arbitration.Contradiction {
		t.Fatalf("expected a genuine contradiction between AIS vendors and port authority")
	}
	if res.Truth.Observation.WinnerConfidence <= 0 {
		t.Fatalf("expected the contradiction/truth engine to have ingested the arbitration")
	}
	if res.CausalSupport <= 0 {
		t.Fatalf("expected nonzero causal support given a supplied CausalLink")
	}
	if res.Provenance.Status == "" {
		t.Fatalf("expected a computed provenance status, got empty")
	}
	if res.Risk.Score < 0 || res.Risk.Score > 1 {
		t.Fatalf("expected risk score in [0,1], got %f", res.Risk.Score)
	}
	if res.Decision.PolicyName != in.Policy.Name {
		t.Fatalf("expected decision to be made under the supplied policy, got %s", res.Decision.PolicyName)
	}
	if res.Twin.Entity != in.Entity {
		t.Fatalf("expected the twin to reflect the case entity")
	}
	if res.Twin.Causal[in.Policy.Name].Support != res.CausalSupport {
		t.Fatalf("expected the twin's recorded causal support to match the pipeline's causal support")
	}
	if res.EconomicImpact.TotalExpectedImpact <= 0 {
		t.Fatalf("expected nonzero expected economic impact given nonzero risk and channels")
	}
	if res.Certificate.Hash == "" {
		t.Fatalf("expected a nonempty certificate hash")
	}
	if err := VerifyCertificate(res.Certificate); err != nil {
		t.Fatalf("expected a freshly-issued certificate to verify clean: %v", err)
	}

	// Every sub-engine's own ledger must also independently verify —
	// the canonical pipeline adds a certificate on top, it doesn't
	// replace each layer's own tamper-evidence.
	if err := p.Fusion.VerifyChain(); err != nil {
		t.Fatalf("fusion VerifyChain: %v", err)
	}
	if err := p.Contradiction.VerifyChain(); err != nil {
		t.Fatalf("contradiction VerifyChain: %v", err)
	}
	if err := p.Causal.VerifyChain(); err != nil {
		t.Fatalf("causal VerifyChain: %v", err)
	}
	if err := p.Decision.VerifyChain(); err != nil {
		t.Fatalf("decision VerifyChain: %v", err)
	}
	if err := p.Twin.VerifyChain(); err != nil {
		t.Fatalf("twin VerifyChain: %v", err)
	}
}

// TestRunCanonicalDomainOutputsCarryRealSchemaLevelProvenance is the
// direct answer to an external audit's explicit follow-up ask (P0-A):
// "Saya ingin audit lanjutan terhadap schema-level provenance, bukan
// hanya stage-level hashing" (schema-level provenance, not just
// stage-level hashing). TestRunCanonicalFullChain above already proves
// every domain's own hash chain verifies -- that is stage-level
// integrity, proving nothing was tampered with, but nothing about
// whether the domain output's OWN explanatory fields (why this score,
// why this action) are real and populated rather than present-but-
// empty placeholders. This test checks exactly that, for every domain
// with an explanatory field in its schema: risk.Result.Breakdown/
// Explanation and decision.Decision.Explanation.
func TestRunCanonicalDomainOutputsCarryRealSchemaLevelProvenance(t *testing.T) {
	p := NewPipeline(nil)
	in := testCase()
	in.PatternScore = 0.7 // above risk.Score's 0.5 explanation threshold

	res, err := p.RunCanonical("case-provenance", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}

	// Risk.Breakdown: every factor risk.Model.Score computes must be
	// present with a real, traceable name and a genuinely computed
	// (not zero-value-by-omission) contribution.
	wantFactors := map[string]bool{
		"sanctions_evasion_pattern_score":  false,
		"price_anomaly_score":              false,
		"provenance_independence_discount": false,
	}
	if len(res.Risk.Breakdown) != len(wantFactors) {
		t.Fatalf("expected exactly %d risk factors in the breakdown, got %d: %+v", len(wantFactors), len(res.Risk.Breakdown), res.Risk.Breakdown)
	}
	for _, f := range res.Risk.Breakdown {
		if _, known := wantFactors[f.Name]; !known {
			t.Fatalf("unexpected, unnamed risk factor in breakdown: %q", f.Name)
		}
		wantFactors[f.Name] = true
	}
	for name, seen := range wantFactors {
		if !seen {
			t.Fatalf("expected risk factor %q in the breakdown, it was missing", name)
		}
	}
	// The pattern-score factor's own RawValue must equal the real input
	// we supplied -- not a placeholder disconnected from the case.
	for _, f := range res.Risk.Breakdown {
		if f.Name == "sanctions_evasion_pattern_score" && f.RawValue != in.PatternScore {
			t.Fatalf("expected the pattern-score factor's RawValue to equal the supplied PatternScore %v, got %v", in.PatternScore, f.RawValue)
		}
	}
	if len(res.Risk.Explanation) == 0 {
		t.Fatal("expected at least one real risk explanation line, given PatternScore=0.7 crosses risk.Score's own 0.5 threshold")
	}

	// Decision.Explanation: the policy engine's own stated reasoning,
	// not just an Action label.
	if len(res.Decision.Explanation) == 0 {
		t.Fatal("expected at least one real decision explanation line")
	}
	for _, line := range res.Decision.Explanation {
		if line == "" {
			t.Fatal("expected every decision explanation line to be real text, found an empty one")
		}
	}
}

// TestRunCanonicalIsDeterministic is the core canonical-path proof:
// two independent pipelines given byte-identical input must produce
// byte-identical certificates — the whole point of a "canonical" path.
func TestRunCanonicalIsDeterministic(t *testing.T) {
	in := testCase()
	in.CausalLinks = []CausalLink{{Cause: causal.NodeID("source:port-authority"), Strength: 0.9}}

	p1 := NewPipeline(nil)
	res1, err := p1.RunCanonical("case-1", in)
	if err != nil {
		t.Fatal(err)
	}
	p2 := NewPipeline(nil)
	res2, err := p2.RunCanonical("case-1", in)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Certificate.Hash != res2.Certificate.Hash {
		t.Fatalf("expected identical certificates from identical input: %s vs %s", res1.Certificate.Hash, res2.Certificate.Hash)
	}
}

// TestVerifyCertificateDetectsTamper proves the certificate is
// actually tamper-evident, not just a decorative hash field.
func TestVerifyCertificateDetectsTamper(t *testing.T) {
	p := NewPipeline(nil)
	res, err := p.RunCanonical("case-1", testCase())
	if err != nil {
		t.Fatal(err)
	}
	cert := res.Certificate
	cert.RiskScore = cert.RiskScore + 0.5 // tamper
	if err := VerifyCertificate(cert); err == nil {
		t.Fatalf("expected VerifyCertificate to reject a tampered certificate")
	}
}

// TestRunCanonicalRejectsEmptySubmissions guards the entry contract.
func TestRunCanonicalRejectsEmptySubmissions(t *testing.T) {
	p := NewPipeline(nil)
	in := testCase()
	in.Submissions = nil
	if _, err := p.RunCanonical("case-1", in); err != ErrNoSubmissions {
		t.Fatalf("expected ErrNoSubmissions, got %v", err)
	}
}

// TestRunCanonicalSharesTrustCalculusWhenProvided proves the pipeline
// genuinely uses a shared Calculus rather than silently ignoring it —
// consistent with how veriqo/kernel.Kernel shares trust state across
// Evidence/Intelligence/Intent/Calibration.
func TestRunCanonicalSharesTrustCalculusWhenProvided(t *testing.T) {
	calc := sharedCalculusForTest()
	p := NewPipeline(calc)
	if p.Trust != calc {
		t.Fatalf("expected NewPipeline to use the supplied Calculus, not construct its own")
	}
}
