package casepack

import (
	"testing"

	"veriqo/pkg/insurance/policy"
)

// TestDriveGoldenSucceeds is the mandate's own Gate 5 ("Cross-Domain
// Golden Case"): the full chain runs end to end with no error.
func TestDriveGoldenSucceeds(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	if gr.Result == nil {
		t.Fatal("expected the base Drive() Result to be embedded")
	}
}

func TestGoldenGeospatialResolvesIncidentInsideItsOwnTerminal(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	if len(gr.IncidentZones) == 0 {
		t.Fatal("expected the incident location to resolve inside at least one geofence")
	}
	if gr.IncidentZones[0].ID != "TERMINAL-CALDER-BAY" {
		t.Fatalf("expected TERMINAL-CALDER-BAY, got %s", gr.IncidentZones[0].ID)
	}
	if !gr.VesselSpeedPlausible {
		t.Fatalf("expected the golden case's own vessel track to be a physically plausible speed, got %.2f knots",
			float64(gr.VesselImpliedKnotsHundredths)/100)
	}
}

func TestGoldenRelationshipIsEffectiveAndPermissioned(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	rel, ok := gr.Relationships.Get(gr.BrokerRelationshipID)
	if !ok {
		t.Fatal("expected the broker relationship to be registered")
	}
	if !rel.ConsentGiven {
		t.Fatal("expected consent to be recorded")
	}
	if !rel.HasPermission("SUBMIT_CLAIM") {
		t.Fatal("expected the SUBMIT_CLAIM permission to be granted")
	}
	if !gr.Relationships.EffectiveAt(gr.BrokerRelationshipID, 500) {
		t.Fatal("expected the relationship to be effective")
	}
}

// TestGoldenSalvageGenuinelyReducesQuantum is the core proof this round
// exists to produce: salvage is not merely present, it CHANGES the
// authoritative figure by exactly its own net value.
func TestGoldenSalvageGenuinelyReducesQuantum(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	if gr.SalvageNetValue.Amount <= 0 {
		t.Fatalf("expected a positive salvage net value, got %s", gr.SalvageNetValue.Amount)
	}
	if len(gr.SalvageNetValue.EvidenceIDs) == 0 {
		t.Fatal("expected the salvage net value to carry evidence lineage")
	}
	diff := gr.QuantumWithoutSalvage.IndicativeClaimValue - gr.QuantumWithSalvage.IndicativeClaimValue
	if diff != gr.SalvageNetValue.Amount {
		t.Fatalf("expected the quantum figure to drop by EXACTLY the salvage net value: "+
			"without=%s with=%s diff=%s salvageNet=%s",
			gr.QuantumWithoutSalvage.IndicativeClaimValue, gr.QuantumWithSalvage.IndicativeClaimValue,
			diff, gr.SalvageNetValue.Amount)
	}
}

func TestGoldenCoInsuranceAndReinsuranceAllocationsAreExact(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	payment := gr.QuantumWithSalvage.IndicativeClaimValue
	var coSum int64
	for _, a := range gr.CoInsuranceAllocation {
		coSum += int64(a.Amount)
	}
	if coSum != int64(payment) {
		t.Fatalf("co-insurance allocations must sum to exactly the payment: expected %d, got %d",
			int64(payment), coSum)
	}

	var insurerPrimary int64
	for _, a := range gr.CoInsuranceAllocation {
		if a.Role == policy.AllocationRoleInsurerPrimary {
			insurerPrimary = int64(a.Amount)
		}
	}
	var reSum int64
	for _, a := range gr.ReinsuranceAllocation {
		reSum += int64(a.Amount)
	}
	if reSum != insurerPrimary {
		t.Fatalf("reinsurance allocations must sum to exactly the insurer's primary share: expected %d, got %d",
			insurerPrimary, reSum)
	}
}

func TestGoldenDisputePreservesBothPositionsUnreconciled(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	issue, ok := gr.DisputeMatter.Issue("ISS-GOLDEN-1")
	if !ok {
		t.Fatal("expected the dispute issue to be registered")
	}
	if len(issue.Positions) != 2 {
		t.Fatalf("expected both parties' positions recorded side by side, got %d", len(issue.Positions))
	}
	if len(issue.SupportingEvidence) == 0 || len(issue.ContradictingEvidence) == 0 {
		t.Fatal("expected both supporting and contradicting evidence to be recorded, unreconciled")
	}
}

// TestDriveGoldenIsDeterministic proves the golden extension is exactly
// as deterministic as the base Drive() path — two independent runs
// produce byte-identical (here: value-identical) monetary, geospatial
// and allocation results.
func TestDriveGoldenIsDeterministic(t *testing.T) {
	a, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden (run 1): %v", err)
	}
	b, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden (run 2): %v", err)
	}
	if a.Manifest.EvidenceRootHash != b.Manifest.EvidenceRootHash {
		t.Fatal("evidence root hash diverged between two golden runs")
	}
	if a.QuantumWithSalvage.IndicativeClaimValue != b.QuantumWithSalvage.IndicativeClaimValue {
		t.Fatal("quantum-with-salvage diverged between two golden runs")
	}
	if len(a.CoInsuranceAllocation) != len(b.CoInsuranceAllocation) {
		t.Fatal("co-insurance allocation count diverged")
	}
	for i := range a.CoInsuranceAllocation {
		if a.CoInsuranceAllocation[i] != b.CoInsuranceAllocation[i] {
			t.Fatalf("co-insurance allocation %d diverged: %+v vs %+v", i, a.CoInsuranceAllocation[i], b.CoInsuranceAllocation[i])
		}
	}
	if a.VesselImpliedKnotsHundredths != b.VesselImpliedKnotsHundredths {
		t.Fatal("vessel implied speed diverged")
	}
}

// TestGoldenColdReplay is the mandate's own P0 §37: export, discard,
// reconstruct, replay, compare. Proves the golden case's own outputs
// (salvage net value, quantum-with-salvage, allocations) reproduce
// exactly when the underlying base case is replayed cold rather than
// driven live.
func TestGoldenColdReplay(t *testing.T) {
	live, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	replayed, report, err := GoldenColdReplay()
	if err != nil {
		t.Fatalf("GoldenColdReplay: %v", err)
	}
	if !report.Pass() {
		t.Fatalf("base cold replay report did not pass: %v", report.Failures)
	}
	if live.Manifest.EvidenceRootHash != replayed.Manifest.EvidenceRootHash {
		t.Fatal("evidence root hash diverged between live and cold-replayed golden case")
	}
	if live.QuantumWithSalvage.IndicativeClaimValue != replayed.QuantumWithSalvage.IndicativeClaimValue {
		t.Fatalf("quantum-with-salvage diverged: live=%s replayed=%s",
			live.QuantumWithSalvage.IndicativeClaimValue, replayed.QuantumWithSalvage.IndicativeClaimValue)
	}
	if live.SalvageNetValue.Amount != replayed.SalvageNetValue.Amount {
		t.Fatal("salvage net value diverged between live and cold-replayed golden case")
	}
	if len(live.CoInsuranceAllocation) != len(replayed.CoInsuranceAllocation) {
		t.Fatal("co-insurance allocation count diverged")
	}
	for i := range live.CoInsuranceAllocation {
		if live.CoInsuranceAllocation[i] != replayed.CoInsuranceAllocation[i] {
			t.Fatalf("co-insurance allocation %d diverged: %+v vs %+v",
				i, live.CoInsuranceAllocation[i], replayed.CoInsuranceAllocation[i])
		}
	}
}

func TestRunGoldenAssurancePasses(t *testing.T) {
	s := RunGoldenAssurance()
	if !s.Pass() {
		t.Fatalf("RunGoldenAssurance did not pass: %v", s.Failures)
	}
	if !s.QuantumReducedBySalvageExactly || !s.CoInsuranceAllocationExact ||
		!s.ReinsuranceAllocationExact || !s.ColdReplayMatches {
		t.Fatalf("expected every cross-domain check to be true, got %+v", s)
	}
}
