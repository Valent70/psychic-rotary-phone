package casepack

import (
	"testing"

	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/regulatory"
	"veriqo/pkg/insurance/reserve"
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
	if !s.ReserveAuthorized || !s.ReserveReconciliationExact || !s.RecoveryTargetRegistered ||
		!s.RegulatoryFindingRecorded || !s.EvidenceSufficiencyAssessed {
		t.Fatalf("expected every Round 8 cross-domain check to be true, got %+v", s)
	}
}

// TestGoldenReserveIsApprovedAndReconciled proves the reserve is set
// from the SAME quantum-with-salvage figure attachSalvage computed
// (never a disconnected number), approved by a different party
// (segregation of duties, exercised end to end here rather than only
// in reserve's own standalone tests), and reconciles exactly against
// its own founding figure.
func TestGoldenReserveIsApprovedAndReconciled(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	if gr.Reserve == nil {
		t.Fatal("expected a reserve to be attached")
	}
	if gr.Reserve.Status() != reserve.StatusApproved {
		t.Fatalf("reserve status = %v, want APPROVED", gr.Reserve.Status())
	}
	if gr.Reserve.CurrentAmount() != gr.QuantumWithSalvage.IndicativeClaimValue {
		t.Fatalf("reserve amount %v does not match its own founding quantum-with-salvage figure %v",
			gr.Reserve.CurrentAmount(), gr.QuantumWithSalvage.IndicativeClaimValue)
	}
	rec := gr.Reserve.Reconcile(gr.QuantumWithSalvage.IndicativeClaimValue)
	if rec.Adequacy != reserve.AdequacyAdequate {
		t.Fatalf("reconciliation against its own founding figure = %v, want ADEQUATE", rec.Adequacy)
	}
	if len(gr.Reserve.History()) != 2 { // SET, then APPROVE
		t.Fatalf("expected a 2-entry history (SET, APPROVE), got %d: %+v", len(gr.Reserve.History()), gr.Reserve.History())
	}
}

// TestGoldenRecoveryTargetRegisteredAgainstCarrier proves the
// recovery/subrogation domain operates on this case with a REAL
// target — not the empty-targets mechanism the base Drive() path
// exercises on every case.
func TestGoldenRecoveryTargetRegisteredAgainstCarrier(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	if gr.RecoveryRegistry == nil || gr.RecoveryRegistry.Count() != 1 {
		t.Fatalf("expected exactly one recovery target registered, got registry=%v", gr.RecoveryRegistry)
	}
	target, ok := gr.RecoveryRegistry.Get(gr.RecoveryTargetID)
	if !ok {
		t.Fatalf("recovery target %s not found in registry", gr.RecoveryTargetID)
	}
	if target.Party != "PTY-002-CARRIER" {
		t.Fatalf("recovery target party = %s, want PTY-002-CARRIER", target.Party)
	}
	if len(target.SupportingEvidence) == 0 {
		t.Fatal("expected the recovery target to cite real supporting evidence from the case")
	}
}

// TestGoldenRegulatoryMatterReachesFindingAndClosure proves
// pkg/insurance/regulatory — genuinely unintegrated anywhere in this
// repository before this round — now operates end to end: an
// allegation is recorded, investigated, determined NOT_PROVEN by a
// real regulatory finding (never by settlement, per the package's own
// structural rule), and the matter closes.
func TestGoldenRegulatoryMatterReachesFindingAndClosure(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	if gr.RegulatoryMatter == nil {
		t.Fatal("expected a regulatory matter to be attached")
	}
	if gr.RegulatoryMatter.Stage() != regulatory.StageClosedNoAction {
		t.Fatalf("regulatory matter stage = %v, want CLOSED_NO_ACTION", gr.RegulatoryMatter.Stage())
	}
	allegations := gr.RegulatoryMatter.Allegations()
	if len(allegations) != 1 {
		t.Fatalf("expected exactly one allegation, got %d", len(allegations))
	}
	if allegations[0].Result != regulatory.ResultNotProven {
		t.Fatalf("allegation result = %v, want NOT_PROVEN", allegations[0].Result)
	}
	if allegations[0].DeterminedByKind != regulatory.FindingRegulatory {
		t.Fatalf("allegation determined by kind = %v, want REGULATORY_FINDING (never SETTLEMENT_ONLY)", allegations[0].DeterminedByKind)
	}
}

// TestGoldenEvidenceSufficiencyIsGenuinelyAssessed proves the gap
// package's evidence-sufficiency assessment — already wired into
// every case via Facade.ComputeGapAssessment — actually produces a
// real, non-empty rating on the golden case specifically, correcting
// Round 7's own overstated finding that gap was "not integrated" (it
// was; only regulatory truly had zero callers, and recovery's
// mechanism ran with zero real targets).
func TestGoldenEvidenceSufficiencyIsGenuinelyAssessed(t *testing.T) {
	gr, err := DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	if gr.Dossier == nil || len(gr.Dossier.EvidenceSufficiency) == 0 {
		t.Fatal("expected a non-empty evidence sufficiency assessment on the golden case's own dossier")
	}
}
