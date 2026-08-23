package rwc

import (
	"context"
	"math"
	"testing"

	"veriqo/pkg/lineage"
	"veriqo/pkg/moat/intelligence/risk"
	"veriqo/pkg/moat/provenance"
	"veriqo/veriqo/kernel"
)

// TestPolicyThresholdsMatchTheRealRiskModel checks VesselPortPolicy's
// doc comment against the REAL risk model on this branch instead of
// trusting the arithmetic written in that comment. If a future change to
// risk.Model.Score's weights or discount floor moves the composite away
// from ViolationRatio, this fails and the policy's calibration claim
// stops being true silently.
func TestPolicyThresholdsMatchTheRealRiskModel(t *testing.T) {
	m := risk.NewModel()
	g := provenance.New()

	for _, ratio := range []float64{0.0, 0.5, 1.0} {
		res, err := m.ScoreWithProvenance(risk.CompositeInput{
			PatternScore: ratio, PriceAnomalyScore: ratio,
			SourceIDs: []string{"ONLY_SOURCE"},
		}, g)
		if err != nil {
			t.Fatalf("ScoreWithProvenance(%v): %v", ratio, err)
		}
		if math.Abs(res.Score-ratio) > 1e-9 {
			t.Fatalf("single-source composite for ViolationRatio=%v is %v, want %v — "+
				"VesselPortPolicy's threshold calibration comment is no longer true",
				ratio, res.Score, ratio)
		}
		want := "MONITOR"
		switch {
		case res.Score >= VesselPortPolicy.EscalateThreshold:
			want = "ESCALATE"
		case res.Score >= VesselPortPolicy.FlagThreshold:
			want = "FLAG"
		}
		byBand := map[float64]string{0.0: "MONITOR", 0.5: "FLAG", 1.0: "ESCALATE"}
		if want != byBand[ratio] {
			t.Fatalf("ViolationRatio=%v scores %v, which lands in band %s, want %s",
				ratio, res.Score, want, byBand[ratio])
		}
	}
}

// TestRWCCaseRegistersRealCaseLineage proves the pkg/lineage wiring is
// real: after one RWC case runs on a Kernel with a lineage ledger
// attached, the ledger holds a hash-verified case under the run's own
// CaseID carrying the structural node kinds RunUnified registers.
//
// It also asserts the honest negative: the case reports Complete=false,
// because lineage requires an OUTCOME node and no ground truth exists
// for a vessel/port suitability judgment at case-run time.
func TestRWCCaseRegistersRealCaseLineage(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown

	ledger := EnableCaseLineage(k)

	req, _, err := BuildRWC001Case(RWC001Candidates()[0], 1)
	if err != nil {
		t.Fatalf("BuildRWC001Case: %v", err)
	}
	res, err := Run(context.Background(), k, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.LineageCaseID == "" {
		t.Fatal("CaseResult.LineageCaseID is empty — the case has no lineage identity")
	}
	if err := ledger.VerifyChain(res.LineageCaseID); err != nil {
		t.Fatalf("lineage chain does not verify for %s: %v", res.LineageCaseID, err)
	}

	comp := ledger.Completeness(res.LineageCaseID)
	if !comp.ChainVerified {
		t.Error("lineage Completeness reports ChainVerified=false")
	}
	present := map[lineage.Kind]bool{}
	for _, kind := range comp.PresentKinds {
		present[kind] = true
	}
	for _, want := range []lineage.Kind{
		lineage.KindIntent, lineage.KindEntity, lineage.KindEvidence,
		lineage.KindPolicy, lineage.KindDecision, lineage.KindVerification, lineage.KindReplay,
	} {
		if !present[want] {
			t.Errorf("lineage for %s is missing node kind %s (present=%v)", res.LineageCaseID, want, comp.PresentKinds)
		}
	}
	if comp.Complete {
		t.Error("lineage reports Complete=true, but no OUTCOME node can exist at case-run time — " +
			"a complete case here would mean ground truth was fabricated")
	}
}

// TestRWCCaseCarriesRealCorrelationKey proves the pkg/platform/correlation
// wiring is real rather than a struct that happens to be copied: the
// seven identifiers must be non-empty and must equal the values the
// corresponding subsystems independently reported for the same run.
//
// EntityIdentityLedgerHead is deliberately NOT in the must-be-non-empty
// set, and that is a real property rather than a tolerance: pkg/identity
// appends a ledger event only when a MERGE happens, so a case carrying a
// single alias (every RWC-001 candidate, which declares only NAME)
// resolves through the canonical authority without writing anything to
// the ledger, and Head() is correctly "". The multi-alias RWC-002 case
// below is where a real merge occurs, so that is where a non-empty head
// is required.
func TestRWCCaseCarriesRealCorrelationKey(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown

	req, _, err := BuildRWC001Case(RWC001Candidates()[0], 1)
	if err != nil {
		t.Fatalf("BuildRWC001Case: %v", err)
	}
	res, err := Run(context.Background(), k, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	corr := res.Correlation
	for name, got := range map[string]string{
		"IntentID":                  corr.IntentID,
		"ExecutionID":               corr.ExecutionID,
		"EvidencePackageID":         corr.EvidencePackageID,
		"EntityID":                  corr.EntityID,
		"DecisionID":                corr.DecisionID,
		"VerificationCertificateID": corr.VerificationCertificateID,
		"ReplayPackageID":           corr.ReplayPackageID,
	} {
		if got == "" {
			t.Errorf("correlation key field %s is empty for a real RWC run", name)
		}
	}
	if corr.ExecutionID != res.ExecutionID {
		t.Errorf("correlation ExecutionID=%s but execution trace reported %s", corr.ExecutionID, res.ExecutionID)
	}
	if corr.EntityIdentityLedgerHead != k.Identity.Head() {
		t.Errorf("correlation identity ledger head %q does not match the kernel's own resolver head %q",
			corr.EntityIdentityLedgerHead, k.Identity.Head())
	}
	if corr.EntityIdentityLedgerHead != "" {
		t.Errorf("RWC-001 declares one alias and performs no merge, so the identity ledger "+
			"must still be empty; got head %q — either the alias set or the resolution path changed",
			corr.EntityIdentityLedgerHead)
	}
}

// TestRWC002MultiAliasMergeWritesTheIdentityLedger is the positive
// counterpart to the negative assertion above: RWC-002 submits IMO and
// MMSI as two aliases of one vessel, which forces a real
// pkg/identity.Resolver.Merge, which must leave a verifiable event in
// the identity ledger and surface as the run's correlation head.
func TestRWC002MultiAliasMergeWritesTheIdentityLedger(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown

	req, _, err := BuildRWC002VesselIdentityCase(1)
	if err != nil {
		t.Fatalf("BuildRWC002VesselIdentityCase: %v", err)
	}
	res, err := Run(context.Background(), k, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Correlation.EntityIdentityLedgerHead == "" {
		t.Fatal("RWC-002 merges IMO and MMSI onto one entity, so the identity ledger must be non-empty")
	}
	if err := k.Identity.VerifyChain(); err != nil {
		t.Fatalf("identity ledger does not verify after the RWC-002 merge: %v", err)
	}
	if len(k.Identity.Ledger()) == 0 {
		t.Fatal("identity ledger head is set but the ledger is empty")
	}
}

// TestRWC002AliasesResolveThroughTheCanonicalIdentityAuthority proves
// RWC-002's IMO+MMSI aliases are resolved by pkg/identity (the canonical
// entity authority) and not by the legacy union-find fallback. A
// fallback resolution would still produce an entity ID that looks
// identical, which is exactly why pkg/lifecycle surfaces the distinction
// on its Result and why this test asserts on it.
func TestRWC002AliasesResolveThroughTheCanonicalIdentityAuthority(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown

	req, _, err := BuildRWC002VesselIdentityCase(1)
	if err != nil {
		t.Fatalf("BuildRWC002VesselIdentityCase: %v", err)
	}
	res, err := Run(context.Background(), k, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Lifecycle.LegacyIdentityFallbackUsed {
		t.Errorf("RWC-002 fell back to the legacy union-find; unmapped alias kinds=%v",
			res.Lifecycle.UnmappedAliasKinds)
	}
	// HumanReviewRequired is no longer set only by a fallback resolution:
	// since the canonical-truth-path round, a trust evaluation that finds
	// any source in a RESTRICTED or EXCLUDED posture sets it too, and
	// every RWC source is a never-assessed provider (RESTRICTED). This
	// test's subject is identity resolution, so it asserts that identity
	// contributed no review requirement, which is what it always meant.
	if res.Lifecycle.HumanReviewRequired && !res.Lifecycle.Canonical.Trust.ReviewRequired {
		t.Error("RWC-002 was flagged HumanReviewRequired for a reason other than trust; " +
			"a fallback resolution is the only other cause and this run did not fall back")
	}
	if res.Lifecycle.EntityID == "" {
		t.Error("RWC-002 produced no canonical entity ID from its IMO/MMSI aliases")
	}
}
