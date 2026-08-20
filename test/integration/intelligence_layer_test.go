package integration

import (
	"testing"

	"veriqo/pkg/moat/calibration"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/domain/maritime"
	"veriqo/pkg/moat/intelligence/patterns"
	"veriqo/pkg/moat/intelligence/pricing"
	"veriqo/pkg/moat/intelligence/risk"
)

// TestIntelligenceLayerEndToEnd proves the Tier-2 "Intelligence Layer"
// (CRITICAL_FINDINGS.docx) is a real, wired pipeline rather than
// isolated packages: a synthetic dark-vessel + under-invoiced-cargo
// scenario flows through the sanctions-evasion/transshipment pattern
// library, the commodity price-anomaly detector, the composite TBML
// risk model, the EXISTING decision.Engine (unmodified — reused as-is,
// per this repo's established discipline of composing rather than
// duplicating engines), and finally the EXISTING calibration.Engine as
// a Prediction awaiting ground truth — closing the same "existence ->
// caller -> integration test -> adversarial test" pattern the repo's
// own history (hbayes, IVF) establishes.
func TestIntelligenceLayerEndToEnd(t *testing.T) {
	// 1. Domain ontology: register a vessel and a beneficial-ownership
	// chain that changed ownership shortly before a sanctions event —
	// exercises the expanded entity/relation kinds.
	reg := maritime.NewRegistry()
	vessel, err := reg.RegisterEntity(maritime.KindVessel, map[string]string{"imo": "9999001"}, nil, 1)
	if err != nil {
		t.Fatalf("register vessel: %v", err)
	}
	shell, err := reg.RegisterEntity(maritime.KindShellCompany, map[string]string{"name": "OPAQUE_HOLDINGS_LLC"}, nil, 1)
	if err != nil {
		t.Fatalf("register shell: %v", err)
	}
	ubo, err := reg.RegisterEntity(maritime.KindBeneficialOwner, map[string]string{"name": "UBO_JOHN_DOE"}, nil, 1)
	if err != nil {
		t.Fatalf("register ubo: %v", err)
	}
	og := maritime.NewOwnershipGraph(reg)
	if err := og.RecordOwnership(ubo.ID, shell.ID, 1.0, maritime.OwnershipBeneficial, 1); err != nil {
		t.Fatalf("record ubo->shell: %v", err)
	}
	if err := og.RecordOwnership(shell.ID, vessel.ID, 1.0, maritime.OwnershipBeneficial, 2); err != nil {
		t.Fatalf("record shell->vessel: %v", err)
	}
	ubos := og.UltimateBeneficialOwners(vessel.ID, maritime.OwnershipBeneficial, 0.25, 5)
	if len(ubos) != 1 || ubos[0].OwnerID != ubo.ID {
		t.Fatalf("expected UBO piercing to find exactly the ultimate person (not the intermediate shell), got %+v", ubos)
	}

	// 2. Sanctions-evasion / transshipment pattern library evaluation.
	lib := patterns.NewLibrary(patterns.StandardPatterns())
	sig := patterns.Signal{
		AISGapHours:                      60,
		DarkPortCallCount:                1,
		STSTransferCount:                 2,
		STSNearSanctionedJurisdiction:    true,
		OwnershipChangeCount:             1,
		OwnershipChangeNearSanctionEvent: true,
	}
	matches, err := lib.Evaluate(vessel.ID, sig, 5)
	if err != nil {
		t.Fatalf("pattern evaluate: %v", err)
	}
	patternScore := patterns.AggregateScore(matches)
	if patternScore <= 0.5 {
		t.Fatalf("expected high aggregate pattern score for multi-signal dark vessel, got %v", patternScore)
	}
	if err := lib.VerifyChain(); err != nil {
		t.Fatalf("pattern library chain verification failed: %v", err)
	}

	// 3. Commodity price anomaly: cargo declared at a fraction of
	// reference market price (classic under-invoicing TBML signature).
	det := pricing.NewDetector()
	referencePrices := []float64{620, 615, 630, 625, 618, 622, 628, 619, 621, 624} // USD/ton reference band
	anomaly, err := det.Evaluate(180, referencePrices)                             // declared far below market
	if err != nil {
		t.Fatalf("price anomaly evaluation: %v", err)
	}
	if !anomaly.IsAnomaly || anomaly.Direction != "UNDER" {
		t.Fatalf("expected under-invoicing anomaly, got %+v", anomaly)
	}

	// 4. Composite TBML/sanctions-evasion risk model.
	riskModel := risk.NewModel()
	composite := riskModel.Score(risk.CompositeInput{
		PatternScore:                patternScore,
		PriceAnomalyScore:           anomaly.AnomalyScore(),
		HasProvenanceData:           true,
		ProvenanceIndependenceRatio: 0.9, // mostly independent sources in this scenario
	})
	if composite.Label != risk.LabelHigh && composite.Label != risk.LabelCritical {
		t.Fatalf("expected HIGH or CRITICAL composite label for dark-vessel + under-invoicing scenario, got %s (score=%v)",
			composite.Label, composite.Score)
	}

	// 5. Feed into the EXISTING decision.Engine — proves the
	// intelligence layer is not a parallel decision system but an
	// evidence source for the one that already exists in the repo.
	decEngine := decision.NewEngine()
	policy := risk.DefaultPolicy()
	if err := decEngine.RegisterPolicy(policy); err != nil {
		t.Fatalf("register policy: %v", err)
	}
	values := risk.ToDecisionValues(composite)
	decision, err := decEngine.Decide("intelligence-layer-test", vessel.ID, policy.Name, values, 6)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Action == "MONITOR" {
		t.Fatalf("expected decision engine to escalate beyond MONITOR for this scenario, got %s (score=%v)",
			decision.Action, decision.RiskScore)
	}
	if err := decEngine.VerifyChain(); err != nil {
		t.Fatalf("decision chain verification failed: %v", err)
	}

	// 6. Feed into the EXISTING calibration.Engine as a Prediction —
	// proves the intelligence layer closes into the same ground-truth
	// feedback loop the audit's Priority #1/#2 work already built,
	// rather than living as an unscored side-channel score.
	calEngine := calibration.NewEngine()
	pred := risk.PredictionFromResult(vessel.ID+"|tbml_sanctions_risk", composite, []string{"pattern_lib", "price_ref_feed"}, 6)
	if err := calEngine.RecordPrediction(pred); err != nil {
		t.Fatalf("record prediction: %v", err)
	}
	pending := calEngine.PendingClaims()
	found := false
	for _, c := range pending {
		if c == pred.ClaimKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected prediction to be pending calibration, claim key %q not found in %v", pred.ClaimKey, pending)
	}
	// Independent ground truth arrives later (e.g. a real law-
	// enforcement/port-authority feedback loop, CRITICAL_FINDINGS'
	// still-open "Missing Ground Truth" gap) — simulated here as a
	// confirmed SUSPECTED outcome from a source that did NOT
	// contribute to the original prediction.
	calEngine.RegisterIndependentSource("port_authority_case_2026_0091")
	rec, err := calEngine.RecordGroundTruth(calibration.GroundTruth{
		ClaimKey: pred.ClaimKey,
		Value:    "SUSPECTED",
		SourceID: "port_authority_case_2026_0091",
		Tick:     20,
	})
	if err != nil {
		t.Fatalf("record ground truth: %v", err)
	}
	if rec.BrierScore < 0 {
		t.Fatalf("expected a valid non-negative Brier score, got %v", rec.BrierScore)
	}
	if err := calEngine.VerifyLedger(); err != nil {
		t.Fatalf("calibration ledger verification failed: %v", err)
	}
}
