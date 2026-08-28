// Golden-case tests exercise pkg/insurance/cre's GenerateFindings end to
// end (not the lower-level BuildFinding pkg/insurance/cre's own unit
// tests use) across domains distinct from vtecp_cre_integration_test.go's
// maritime cargo-damage scenario, proving the engine generalizes rather
// than being tuned to one worked example. Both scenarios below are
// clearly synthetic test fixtures, not claims about real cases.
package integration

import (
	"testing"

	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
)

// TestGoldenCase_CommodityContaminationDispute: a crude-oil cargo is
// rejected at discharge for off-spec water content. Two hypotheses
// compete: contamination during ship-to-shore transfer (well
// evidenced) vs. contamination already present at the load port
// (evidenced against by an independent load-port lab certificate).
func TestGoldenCase_CommodityContaminationDispute(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-crude-042", "claim-042", "When did the water contamination enter the cargo?")
	if err != nil {
		t.Fatal(err)
	}
	const hTransfer causation.HypothesisID = "H1-TRANSFER-CONTAMINATION"
	const hLoadPort causation.HypothesisID = "H2-LOAD-PORT-CONTAMINATION"
	if err := hs.Add(causation.Hypothesis{ID: hTransfer, Description: "Water contamination entered during ship-to-shore transfer at the discharge port."}); err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: hLoadPort, Description: "The cargo was already contaminated at the load port before shipment."}); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence(hTransfer, "ev-discharge-lab-report"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence(hTransfer, "ev-hose-inspection-report"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddContradictingEvidence(hLoadPort, "ev-load-port-lab-certificate"); err != nil {
		t.Fatal(err)
	}

	dg := evidence.NewDependencyGraph()
	authorized, err := cre.GenerateFindings(hs, dg, cre.FindingInput{
		CaseID: "case-crude-042", ContractBasis: "clause-12.1-quality-warranty",
		ObligationRef: "obl-quality-notice-042", EventRef: "event-discharge-sampling-042",
		QuantumRef: "calc-rejection-loss-042", HumanReviewRequired: true, HumanReviewedBy: "surveyor-lead-1",
	}, nil, "case-042-finding", 500)
	if err != nil {
		t.Fatalf("GenerateFindings: %v", err)
	}
	if len(authorized) != 1 {
		t.Fatalf("expected exactly 1 candidate finding (only the transfer-contamination hypothesis qualifies), got %d", len(authorized))
	}
	if authorized[0].IsZero() {
		t.Fatal("expected a populated, authorized finding")
	}
	f := authorized[0].Finding()
	if f.Status != finding.StatusFinding {
		t.Fatalf("expected StatusFinding, got %s (missing=%v)", f.Status, finding.MissingFields(f))
	}
	if authorized[0].HypothesisID() != hTransfer {
		t.Fatalf("expected the AuthorizedFinding to be authorized against %s, got %s", hTransfer, authorized[0].HypothesisID())
	}
	// The load-port hypothesis must appear as a considered alternative,
	// not be silently dropped.
	found := false
	for _, alt := range f.Alternatives {
		if alt == string(hLoadPort) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the load-port hypothesis to appear in Alternatives, got %v", f.Alternatives)
	}
}

// TestGoldenCase_WarehouseFireElectricalVsArson: a warehouse fire
// claim. Electrical-fault ignition is well evidenced; arson is a
// theory with zero supporting evidence and must not become a Finding.
func TestGoldenCase_WarehouseFireElectricalVsArson(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-fire-777", "claim-777", "What ignited the warehouse fire?")
	if err != nil {
		t.Fatal(err)
	}
	const hElectrical causation.HypothesisID = "H1-ELECTRICAL-FAULT"
	const hArson causation.HypothesisID = "H2-ARSON"
	if err := hs.Add(causation.Hypothesis{ID: hElectrical, Description: "An electrical fault in the mezzanine distribution panel ignited the fire."}); err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: hArson, Description: "The fire was deliberately set."}); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence(hElectrical, "ev-fire-investigator-report"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence(hElectrical, "ev-electrical-maintenance-log"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence(hElectrical, "ev-cctv-timestamp-correlation"); err != nil {
		t.Fatal(err)
	}
	// Deliberately no evidence added for hArson at all.

	dg := evidence.NewDependencyGraph()
	authorized, err := cre.GenerateFindings(hs, dg, cre.FindingInput{
		CaseID: "case-fire-777", ContractBasis: "clause-5.2-fire-peril",
		ObligationRef: "obl-notice-of-loss-777", EventRef: "event-fire-discovery-777",
		QuantumRef: "calc-warehouse-loss-777", HumanReviewRequired: true, HumanReviewedBy: "claims-lead-2",
	}, nil, "case-777-finding", 900)
	if err != nil {
		t.Fatalf("GenerateFindings: %v", err)
	}
	if len(authorized) != 1 {
		t.Fatalf("expected exactly 1 finding (arson has zero evidence and must not qualify), got %d", len(authorized))
	}
	if authorized[0].HypothesisID() != hElectrical {
		t.Fatalf("expected the AuthorizedFinding to be authorized against %s, got %s", hElectrical, authorized[0].HypothesisID())
	}
	// Explicitly confirm arson was never silently promoted.
	arson, ok := hs.Get(hArson)
	if !ok {
		t.Fatal("expected the arson hypothesis to still exist in the set")
	}
	if arson.Status == causation.StatusSupported || arson.Status == causation.StatusPartiallySupported {
		t.Fatalf("expected the zero-evidence arson hypothesis to remain unsupported, got status %s", arson.Status)
	}
	for _, got := range authorized {
		if got.Finding().ConfidenceBasis == causation.StatusUnproven {
			t.Fatal("expected no finding to be built from the unproven arson hypothesis")
		}
	}
}
