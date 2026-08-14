package lifecycle

import (
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

func testIntent() Intent {
	return Intent{
		ActorID: "analyst-1", Tenant: "acme", Objective: "assess dark-vessel risk",
		EntityAliases: []entity.Alias{
			{Kind: "IMO", Value: "9998887"},
			{Kind: "CALLSIGN", Value: "ABCD1"},
		},
		RequiredConfidence: 0.6, TemporalScope: "last-7-days", Tick: 1,
	}
}

func testCaseInput(subject string) canonical.CaseInput {
	return canonical.CaseInput{
		Subject: subject, Predicate: "AIS_STATUS", Tick: 1, Policy: risk.DefaultPolicy(),
		Submissions: []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("ais-vendor-a"), Value: "OFF", BaseReliability: 0.8},
			{SourceID: fusion.SourceID("ais-vendor-b"), Value: "OFF", BaseReliability: 0.8},
			{SourceID: fusion.SourceID("port-authority"), Value: "ON", BaseReliability: 0.95},
		},
		PatternScore: 0.7,
	}
}

// TestUnifiedIntentTrustDecisionLifecycle is the audit's mandated
// central acceptance test: starts from an Intent (not evidence
// directly), and finishes with Decision, Digital Twin consequence,
// IVF proof, and replay identity.
func TestUnifiedIntentTrustDecisionLifecycle(t *testing.T) {
	o := NewOrchestrator(nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(in, plan, testCaseInput("placeholder-overwritten-by-entity-resolution"))
	if err != nil {
		t.Fatalf("RunUnified: %v", err)
	}

	// Started from Intent, not evidence: entity resolved from the
	// Intent's own aliases, not from a caller-supplied subject string.
	if res.EntityID == "" {
		t.Fatalf("expected entity resolution to have run from the Intent's aliases")
	}
	if string(res.Canonical.Twin.Entity) != string(res.EntityID) {
		t.Fatalf("expected the Digital Twin to be keyed by the resolved canonical entity, got twin=%s resolved=%s",
			res.Canonical.Twin.Entity, res.EntityID)
	}

	// Finishes with Decision.
	if res.Canonical.Decision.Action == "" {
		t.Fatalf("expected a Decision action")
	}
	// Digital Twin consequence.
	if len(res.Canonical.Twin.Risk) == 0 {
		t.Fatalf("expected the twin to carry a risk consequence")
	}
	// IVF proof.
	if !res.Certificate.IVFVerified {
		t.Fatalf("expected IVF to independently verify this case's arbitration")
	}
	if res.Certificate.IVFCertificateHash == "" {
		t.Fatalf("expected an IVF AuditCertificate to be issued")
	}
	// Replay identity.
	if res.Certificate.ReplayID == "" || res.Certificate.ReplayID != res.Canonical.Certificate.Hash {
		t.Fatalf("expected ReplayID to equal the canonical certificate hash, got %s vs %s", res.Certificate.ReplayID, res.Canonical.Certificate.Hash)
	}

	// Whole-chain ID lineage: IntentID must be exactly Intent.ID(),
	// and the plan must be provably tied to this exact intent.
	if res.Certificate.IntentID != in.ID() {
		t.Fatalf("IntentID mismatch: %s vs %s", res.Certificate.IntentID, in.ID())
	}
	if res.Certificate.EvidencePlanHash != plan.Hash {
		t.Fatalf("EvidencePlanHash mismatch")
	}
}

// TestRunUnifiedRejectsMismatchedPlan proves a plan built for a
// different Intent cannot silently be reused.
func TestRunUnifiedRejectsMismatchedPlan(t *testing.T) {
	o := NewOrchestrator(nil)
	in := testIntent()
	otherIntent := testIntent()
	otherIntent.ActorID = "someone-else"
	plan := PlanEvidence(otherIntent, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 1}})

	_, err := o.RunUnified(in, plan, testCaseInput("x"))
	if err == nil {
		t.Fatalf("expected an error using a plan built for a different intent")
	}
}

// TestRunUnifiedEnforcesRequiredEvidence proves a required
// EvidenceRequirement that isn't met blocks the run rather than
// silently proceeding.
func TestRunUnifiedEnforcesRequiredEvidence(t *testing.T) {
	o := NewOrchestrator(nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 10}})

	_, err := o.RunUnified(in, plan, testCaseInput("x"))
	if err == nil {
		t.Fatalf("expected ErrPlanUnsatisfied when MinSources is not met")
	}
}

// TestRunUnifiedPreservesOptionalUnmetAsUncertainty proves an
// optional (non-Required) unmet requirement does NOT block the run —
// it is recorded as uncertainty, per the audit's explicit instruction
// not to silently drop missing-evidence information.
func TestRunUnifiedPreservesOptionalUnmetAsUncertainty(t *testing.T) {
	o := NewOrchestrator(nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{
		{Kind: "AIS_STATUS", Required: true, MinSources: 2},
		{Kind: "SANCTIONS_HIT", Required: false, MinSources: 1}, // no submissions supply this
	})
	res, err := o.RunUnified(in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatalf("expected optional unmet requirement not to block: %v", err)
	}
	if len(res.Certificate.UnmetRequirements) != 1 || res.Certificate.UnmetRequirements[0].Kind != "SANCTIONS_HIT" {
		t.Fatalf("expected the optional unmet requirement to be recorded, got %+v", res.Certificate.UnmetRequirements)
	}
}

// TestRecordOutcomeFeedsCalibration closes the audit's Outcome ->
// Calibration -> Trust Reassessment loop end to end.
func TestRecordOutcomeFeedsCalibration(t *testing.T) {
	o := NewOrchestrator(nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
	res, err := o.RunUnified(in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := o.RecordOutcome(res, res.Canonical.Arbitration.Winner, "ground-truth-inspector", 2)
	if err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if !rec.Matched {
		t.Fatalf("expected the recorded outcome to match the prediction (both %q)", res.Canonical.Arbitration.Winner)
	}
	if err := o.Calibration.VerifyLedger(); err != nil {
		t.Fatalf("expected calibration ledger to verify clean: %v", err)
	}
}

// TestLifecycleCertificateDeterministic mirrors pkg/canonical's own
// determinism proof at the lifecycle layer: identical Intent+Plan+Case
// on independent Orchestrators must produce identical certificates.
func TestLifecycleCertificateDeterministic(t *testing.T) {
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	o1 := NewOrchestrator(nil)
	res1, err := o1.RunUnified(in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}
	o2 := NewOrchestrator(nil)
	res2, err := o2.RunUnified(in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}
	if res1.Certificate.Hash != res2.Certificate.Hash {
		t.Fatalf("expected identical LifecycleCertificate hashes from identical input, got %s vs %s",
			res1.Certificate.Hash, res2.Certificate.Hash)
	}
}

// TestRunUnifiedResolvesEntityThroughIdentityNotUnionFind is the P0-B
// property that matters: for an alias set identity.Kind models (IMO +
// CALLSIGN, the exact vocabulary this repo's own dark-vessel scenarios
// use), RunUnified's canonical entity ID must come from o.Identity, and
// o.Entities (the legacy union-find, now a fallback-only authority)
// must NEVER be written to for this case -- proving identity, not
// pkg/moat/entity, is what actually decided the entity for this call.
func TestRunUnifiedResolvesEntityThroughIdentityNotUnionFind(t *testing.T) {
	o := NewOrchestrator(nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := o.Entities.Resolve(entity.Alias{Kind: "IMO", Value: "9998887"}); ok {
		t.Fatal("expected the legacy union-find to be untouched for an identity-modeled alias set")
	}

	want, err := o.Identity.EntityIDAt(identity.Identifier{Kind: identity.KindIMO, Value: "9998887"}, in.Tick)
	if err != nil {
		t.Fatalf("EntityIDAt: %v", err)
	}
	if string(res.EntityID) != want {
		t.Fatalf("expected RunUnified's EntityID to equal Identity's own resolution: got %s want %s", res.EntityID, want)
	}
}

// TestRunUnifiedFallsBackToUnionFindForUnknownAliasKind proves the
// fail-safe: an alias Kind identity.Kind does not model ("LEI" -- named
// in pkg/moat/entity.Alias's own doc comment as an example, but never
// added to identity.Kind's fixed vocabulary) must NOT be forced through
// identity via a fabricated Kind mapping. RunUnified must still
// succeed, using the legacy union-find for this one call, exactly as it
// did before P0-B's change.
func TestRunUnifiedFallsBackToUnionFindForUnknownAliasKind(t *testing.T) {
	o := NewOrchestrator(nil)
	in := testIntent()
	in.EntityAliases = []entity.Alias{
		{Kind: "LEI", Value: "LEI-CORP-1"},
		{Kind: "NAME", Value: "SHELLCORP HOLDINGS"},
	}
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatalf("expected an unmodeled alias Kind to fall back, not fail: %v", err)
	}
	if got, ok := o.Entities.Resolve(entity.Alias{Kind: "LEI", Value: "LEI-CORP-1"}); !ok || got != res.EntityID {
		t.Fatalf("expected the legacy union-find to have actually resolved this call, got ok=%v id=%s want=%s", ok, got, res.EntityID)
	}
}
