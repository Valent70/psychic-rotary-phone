package metaintelligence

import (
	"testing"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/trust"
)

func darkVesselPolicy() decision.Policy {
	return decision.Policy{
		Name: "dark_vessel_v1",
		Factors: []decision.FactorWeight{
			{Name: "ais_gap_hours", Weight: 0.6},
			{Name: "route_deviation", Weight: 0.25},
			{Name: "known_associate_flag", Weight: 0.15},
		},
		FlagThreshold:     0.4,
		EscalateThreshold: 0.7,
	}
}

func aisFreshnessTrustPolicy() trust.TrustPolicy {
	return trust.TrustPolicy{
		Name: "source_reliability_v1",
		Rules: []trust.TrustRule{
			trust.RuleFromEvidenceKey("ais_freshness", 1.0, "ais_freshness"),
		},
		CertifyThreshold: 0.5,
	}
}

func TestDiagnose_CorrectDecisionNeedsNoAttribution(t *testing.T) {
	policy := darkVesselPolicy()
	values := map[string]float64{"ais_gap_hours": 0.9, "route_deviation": 0.3, "known_associate_flag": 0.0}
	predicted := decision.Decision{Subject: "vessel-1", PolicyName: policy.Name, RiskScore: 0.665, Action: decision.ActionFlag}

	report, err := Diagnose(DiagnoseInput{
		Policy: policy, Values: values, Predicted: predicted, Observed: decision.ActionFlag,
	})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if !report.WasCorrect {
		t.Fatal("expected WasCorrect=true when predicted == observed")
	}
	if report.TrustDelta != 0 {
		t.Fatalf("expected zero trust delta for a correct decision, got %v", report.TrustDelta)
	}
}

func TestDiagnose_WrongDecisionProducesRealTrustDropOnImplicatedEvidence(t *testing.T) {
	policy := darkVesselPolicy()
	values := map[string]float64{"ais_gap_hours": 0.9, "route_deviation": 0.1, "known_associate_flag": 0.0}
	predicted := decision.Decision{Subject: "vessel-1", PolicyName: policy.Name, RiskScore: 0.565, Action: decision.ActionFlag}

	kernel := trust.NewKernel()
	if err := kernel.RegisterPolicy(aisFreshnessTrustPolicy()); err != nil {
		t.Fatalf("RegisterPolicy: %v", err)
	}
	trustEvidence := trust.TrustEvidence{"ais_freshness": 0.95} // this source looked very reliable...

	report, err := Diagnose(DiagnoseInput{
		Policy: policy, Values: values, Predicted: predicted,
		Observed:             decision.ActionEscalate, // ...but the real outcome was worse than predicted
		EvidenceRefs:         map[string][]string{"ais_gap_hours": {"kg:evidence:ais-report-88231"}},
		TrustKernel:          kernel,
		TrustSubjectID:       "source:ais-feed-7",
		TrustPolicyName:      aisFreshnessTrustPolicy().Name,
		TrustEvidence:        trustEvidence,
		FactorToTrustRuleKey: map[string]string{"ais_gap_hours": "ais_freshness"},
		Tick:                 1,
	})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if report.WasCorrect {
		t.Fatal("expected WasCorrect=false: predicted FLAG, observed ESCALATE")
	}
	if report.TopFactor.FactorName != "ais_gap_hours" {
		t.Fatalf("expected ais_gap_hours as the top factor (highest weight*value), got %q", report.TopFactor.FactorName)
	}
	if len(report.TopFactor.EvidenceRefs) != 1 || report.TopFactor.EvidenceRefs[0] != "kg:evidence:ais-report-88231" {
		t.Fatalf("expected the supplied evidence ref to be attributed, got %v", report.TopFactor.EvidenceRefs)
	}
	if report.TrustDelta >= 0 {
		t.Fatalf("expected a NEGATIVE trust delta after discounting the implicated evidence source, got %v (before=%.4f after=%.4f)",
			report.TrustDelta, report.TrustBefore.Score, report.TrustAfter.Score)
	}
	if len(report.Narrative) < 5 {
		t.Fatalf("expected a full Decision->Evidence->Policy->Trust narrative chain, got %d lines: %v", len(report.Narrative), report.Narrative)
	}

	// The trust ledger itself must remain untouched by Diagnose (pure
	// with respect to Kernel state) — verify no certificate was issued.
	if len(kernel.Ledger()) != 0 {
		t.Fatalf("Diagnose must not mutate the trust ledger, found %d certificates", len(kernel.Ledger()))
	}
}

func TestDiagnose_NoMappingSuppliedYieldsZeroDeltaHonestly(t *testing.T) {
	policy := darkVesselPolicy()
	values := map[string]float64{"ais_gap_hours": 0.9, "route_deviation": 0.1, "known_associate_flag": 0.0}
	predicted := decision.Decision{Subject: "vessel-2", PolicyName: policy.Name, RiskScore: 0.565, Action: decision.ActionFlag}

	kernel := trust.NewKernel()
	if err := kernel.RegisterPolicy(aisFreshnessTrustPolicy()); err != nil {
		t.Fatalf("RegisterPolicy: %v", err)
	}
	report, err := Diagnose(DiagnoseInput{
		Policy: policy, Values: values, Predicted: predicted, Observed: decision.ActionEscalate,
		TrustKernel: kernel, TrustSubjectID: "source:x", TrustPolicyName: aisFreshnessTrustPolicy().Name,
		TrustEvidence: trust.TrustEvidence{"ais_freshness": 0.95},
		// FactorToTrustRuleKey intentionally omitted.
	})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if report.TrustDelta != 0 {
		t.Fatalf("with no factor->rule mapping supplied, delta must honestly be zero (nothing was discounted), got %v", report.TrustDelta)
	}
}

func TestTopNFactors(t *testing.T) {
	policy := darkVesselPolicy()
	values := map[string]float64{"ais_gap_hours": 0.9, "route_deviation": 0.3, "known_associate_flag": 1.0}
	top2 := TopNFactors(policy, values, 2)
	if len(top2) != 2 {
		t.Fatalf("expected 2 factors, got %d", len(top2))
	}
	if top2[0].PercentOfTotal < top2[1].PercentOfTotal {
		t.Fatalf("expected descending order, got %+v then %+v", top2[0], top2[1])
	}
}
