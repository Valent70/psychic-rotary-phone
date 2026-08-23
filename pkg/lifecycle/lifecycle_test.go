package lifecycle

import (
	"context"
	"strings"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	bayescalibration "veriqo/pkg/governance/calibration"
	"veriqo/pkg/identity"
	"veriqo/pkg/lineage"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/moat/intelligence/risk"
	"veriqo/pkg/platform/telemetry"
)

// fixtureTemporalCalibration registers the SAME clearly-labelled,
// synthetic AIS_STATUS calibration pkg/governance/calibration's own
// tests use (mirroring test/integration/dark_vessel_test.go's "clearly
// labeled synthetic" convention) -- proving the wiring is real without
// ever claiming this is production-calibrated data.
func fixtureTemporalCalibration(t *testing.T) *bayescalibration.Registry {
	t.Helper()
	reg := bayescalibration.NewRegistry()
	table := bayescalibration.LikelihoodTable{
		Predicate: "AIS_STATUS",
		Record: bayescalibration.CalibrationRecord{
			CalibrationSource: "fixture:synthetic-dark-vessel-dataset-v1 (NOT real production calibration)",
			ModelVersion:      "temporal-v1",
			Prior:             map[hbayes.State]float64{"DARK": 0.2, "NORMAL": 0.8},
			EffectiveTick:     0,
			DatasetProvenance: "fixture:sha256:0000000000000000000000000000000000000000000000000000000000000",
		},
		Likelihood: map[string]map[hbayes.State]float64{
			"OFF": {"DARK": 0.9, "NORMAL": 0.1},
			"ON":  {"DARK": 0.05, "NORMAL": 0.95},
		},
	}
	if err := reg.Register(table); err != nil {
		t.Fatalf("Register: %v", err)
	}
	tr := hbayes.Transition{States: []hbayes.State{"DARK", "NORMAL"}, P: map[hbayes.State]map[hbayes.State]float64{
		"DARK":   {"DARK": 0.7, "NORMAL": 0.3},
		"NORMAL": {"DARK": 0.1, "NORMAL": 0.9},
	}}
	if err := reg.RegisterTemporalModel("AIS_STATUS", tr, nil, 0); err != nil {
		t.Fatalf("RegisterTemporalModel: %v", err)
	}
	return reg
}

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
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("placeholder-overwritten-by-entity-resolution"))
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

	// P0-D Test 3 production wiring: the DAG's own INTENT node must
	// independently re-verify plan.Requirements, not merely attribute
	// tenant/actor/caseID -- proof this is a real causal gate wired
	// into the actual production RunUnified path, not only reachable
	// via a hand-built execution.Input in pkg/execution's own tests.
	var intentNode execution.Node
	for _, n := range res.Execution.Trace.Nodes {
		if n.StageID == execution.StageIntent {
			intentNode = n
		}
	}
	if intentNode.Status != execution.StatusOK {
		t.Fatalf("expected INTENT to succeed with a satisfied plan, got %s: %s", intentNode.Status, intentNode.Error)
	}
}

// TestRunUnifiedRejectsMismatchedPlan proves a plan built for a
// different Intent cannot silently be reused.
func TestRunUnifiedRejectsMismatchedPlan(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	otherIntent := testIntent()
	otherIntent.ActorID = "someone-else"
	plan := PlanEvidence(otherIntent, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 1}})

	_, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err == nil {
		t.Fatalf("expected an error using a plan built for a different intent")
	}
}

// TestRunUnifiedEnforcesRequiredEvidence proves a required
// EvidenceRequirement that isn't met blocks the run rather than
// silently proceeding.
func TestRunUnifiedEnforcesRequiredEvidence(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 10}})

	_, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err == nil {
		t.Fatalf("expected ErrPlanUnsatisfied when MinSources is not met")
	}
}

// TestRunUnifiedPreservesOptionalUnmetAsUncertainty proves an
// optional (non-Required) unmet requirement does NOT block the run —
// it is recorded as uncertainty, per the audit's explicit instruction
// not to silently drop missing-evidence information.
func TestRunUnifiedPreservesOptionalUnmetAsUncertainty(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{
		{Kind: "AIS_STATUS", Required: true, MinSources: 2},
		{Kind: "SANCTIONS_HIT", Required: false, MinSources: 1}, // no submissions supply this
	})
	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
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
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
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

	o1 := NewOrchestrator(nil, nil)
	res1, err := o1.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}
	o2 := NewOrchestrator(nil, nil)
	res2, err := o2.RunUnified(context.Background(), in, plan, testCaseInput("x"))
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
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
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
// fail-safe: an alias Kind identity.Kind does not model ("DUNS" -- the
// Dun & Bradstreet D-U-N-S Number, a real, well-known business
// identifier convention, but one this codebase has not modeled) must
// NOT be forced through identity via a fabricated Kind mapping.
// RunUnified must still succeed, using the legacy union-find for this
// one call, exactly as it did before P0-B's change. This test formerly
// used "LEI" as its unmodeled example; LEI is now a real, modeled
// identity.Kind (see identity.KindLEI's own doc comment) — the
// fallback mechanism this test proves is still real and still needed
// for whatever kind is NOT modeled, so the example was swapped rather
// than the test deleted.
func TestRunUnifiedFallsBackToUnionFindForUnknownAliasKind(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	in.EntityAliases = []entity.Alias{
		{Kind: "DUNS", Value: "DUNS-CORP-1"},
		{Kind: "NAME", Value: "SHELLCORP HOLDINGS"},
	}
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatalf("expected an unmodeled alias Kind to fall back, not fail: %v", err)
	}
	if got, ok := o.Entities.Resolve(entity.Alias{Kind: "DUNS", Value: "DUNS-CORP-1"}); !ok || got != res.EntityID {
		t.Fatalf("expected the legacy union-find to have actually resolved this call, got ok=%v id=%s want=%s", ok, got, res.EntityID)
	}
}

// TestRunUnifiedResolvesLEIThroughIdentityNotUnionFindFallback closes
// the specific residual gap a follow-up audit named for P0-B: LEI
// ("pkg/moat/entity.Registry not deleted because alias Kind LEI is
// unmodeled") is now a real identity.Kind (identity.KindLEI), so an LEI
// alias must resolve through Identity like IMO/CALLSIGN/MMSI/... do —
// NOT fall back to the legacy union-find. This does not eliminate the
// fallback mechanism itself (entity.Alias.Kind remains a free-form
// string; some future, still-unmodeled kind will always need it — see
// TestRunUnifiedFallsBackToUnionFindForUnknownAliasKind, now using
// DUNS as its example instead), but it does mean pkg/moat/entity.Registry
// is never consulted for THIS specific, previously-named example again.
func TestRunUnifiedResolvesLEIThroughIdentityNotUnionFindFallback(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	in.EntityAliases = []entity.Alias{
		{Kind: "LEI", Value: "529900T8BM49AURSDO55"},
		{Kind: "NAME", Value: "SHELLCORP HOLDINGS"},
	}
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatalf("expected LEI, now a modeled identity.Kind, to resolve through Identity: %v", err)
	}
	resolved, err := o.Identity.EntityIDAt(identity.Identifier{Kind: identity.KindLEI, Value: "529900T8BM49AURSDO55"}, in.Tick)
	if err != nil {
		t.Fatalf("expected Identity to have actually resolved this LEI alias: %v", err)
	}
	if string(resolved) != string(res.EntityID) {
		t.Fatalf("Identity's own resolution (%s) must match RunUnified's reported EntityID (%s)", resolved, res.EntityID)
	}
	if _, ok := o.Entities.Resolve(entity.Alias{Kind: "LEI", Value: "529900T8BM49AURSDO55"}); ok {
		t.Fatal("LEI must no longer be written into the legacy union-find fallback now that it is a modeled identity.Kind")
	}
	var idNode execution.Node
	found := false
	for _, n := range res.Execution.Trace.Nodes {
		if n.StageID == execution.StageIdentityResolution {
			idNode, found = n, true
		}
	}
	if !found {
		t.Fatal("expected an IDENTITY_RESOLUTION node")
	}
	if !strings.Contains(idNode.Detail, "independently re-resolved") {
		t.Fatalf("expected LEI resolution (now through Identity) to be independently re-verified by pkg/execution, got detail: %q", idNode.Detail)
	}
}

// TestRunUnifiedThreadsARealIdentityKeyIntoTheExecutionDAG is P0-4's
// end-to-end proof: when entity resolution goes through Identity (not
// the union-find fallback), RunUnified's execution.Input carries the
// SAME primary Identifier into pkg/execution, whose IDENTITY_RESOLUTION
// stage independently re-resolves it and only succeeds because the two
// computations genuinely agree -- not because one trusts the other.
func TestRunUnifiedThreadsARealIdentityKeyIntoTheExecutionDAG(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Execution == nil {
		t.Fatal("expected RunUnified to populate the full pkg/execution DAG result")
	}
	var idNode execution.Node
	found := false
	for _, n := range res.Execution.Trace.Nodes {
		if n.StageID == execution.StageIdentityResolution {
			idNode, found = n, true
		}
	}
	if !found {
		t.Fatal("expected an IDENTITY_RESOLUTION node")
	}
	if idNode.Status != execution.StatusOK {
		t.Fatalf("expected IDENTITY_RESOLUTION to succeed, got %s: %s", idNode.Status, idNode.Error)
	}
	if !strings.Contains(idNode.Detail, "independently re-resolved") {
		t.Fatalf("expected proof of the real P0-4 re-resolution call in the node detail, got %q", idNode.Detail)
	}
}

// TestRunUnifiedDoesNotThreadAnIdentityKeyOnUnionFindFallback proves
// the fallback path (unmodeled alias Kind) correctly leaves
// IdentityAliases empty -- attempting to re-resolve a Kind identity.Kind
// does not model would be a guaranteed, misleading mismatch, not a
// real check. Uses DUNS, not LEI, as its unmodeled example -- see
// TestRunUnifiedResolvesLEIThroughIdentityNotUnionFindFallback for why.
func TestRunUnifiedDoesNotThreadAnIdentityKeyOnUnionFindFallback(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	in.EntityAliases = []entity.Alias{{Kind: "DUNS", Value: "DUNS-CORP-2"}}
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatalf("expected the fallback path to still succeed: %v", err)
	}
	var idNode execution.Node
	for _, n := range res.Execution.Trace.Nodes {
		if n.StageID == execution.StageIdentityResolution {
			idNode = n
		}
	}
	if strings.Contains(idNode.Detail, "independently re-resolved") {
		t.Fatal("must not claim independent re-resolution when entity resolution used the union-find fallback")
	}
}

// TestRunUnifiedTemporalBayesianStageIsHonestlySkippedInProduction is
// the explicit, enforced statement of the DEFAULT, unmodified
// production behavior: an Orchestrator whose TemporalCalibration is
// left nil (as every production Orchestrator this repository
// constructs today does -- veriqo/kernel.New included) must record
// StageTemporal as SKIPPED, never a fabricated OK, because no
// calibrated bridge has been wired in. TestRunUnifiedTemporalBayesian-
// StageExecutesWhenCalibrationRegistered below proves the OTHER half:
// when an operator DOES register real calibration through
// Orchestrator.TemporalCalibration, the exact same code path executes
// hbayes.Model.Infer for real. Together the two tests pin down the
// honest, current, unambiguous truth: the WIRING is real and tested,
// production deployments simply have not opted into it because no real
// calibration corpus exists to opt in with (see pkg/governance/
// calibration's own package comment for the WIRING-vs-CORPUS
// distinction this pair of tests exists to keep visible).
func TestRunUnifiedTemporalBayesianStageIsHonestlySkippedInProduction(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}
	var temporalNode execution.Node
	found := false
	for _, n := range res.Execution.Trace.Nodes {
		if n.StageID == execution.StageTemporal {
			temporalNode = n
			found = true
		}
	}
	if !found {
		t.Fatal("expected a TEMPORAL_BAYESIAN node in the execution trace")
	}
	if temporalNode.Status != execution.StatusSkipped {
		t.Fatalf("RunUnified's real production path supplies no TemporalModel/TemporalObservations today -- StageTemporal must be SKIPPED, not %s; a non-skipped status here without a real production caller populating those fields would mean a fabricated result slipped through", temporalNode.Status)
	}
}

// TestRunUnifiedTemporalBayesianStageExecutesWhenCalibrationRegistered
// is the real closure of the WIRING half of audit item P0-C: with a
// real (if fixture-labelled) LikelihoodTable + temporal Model
// registered on Orchestrator.TemporalCalibration, RunUnified must map
// every caseIn.Submission through BuildObservation, call
// hbayes.Model.Infer for real, and record StageTemporal as OK with a
// genuine, non-empty trace hash -- proving the bridge this round added
// is not merely present in the source tree but actually load-bearing
// on the real RunUnified path.
func TestRunUnifiedTemporalBayesianStageExecutesWhenCalibrationRegistered(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	o.TemporalCalibration = fixtureTemporalCalibration(t)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}
	var temporalNode execution.Node
	found := false
	for _, n := range res.Execution.Trace.Nodes {
		if n.StageID == execution.StageTemporal {
			temporalNode = n
			found = true
		}
	}
	if !found {
		t.Fatal("expected a TEMPORAL_BAYESIAN node in the execution trace")
	}
	if temporalNode.Status != execution.StatusOK {
		t.Fatalf("with real calibration registered, StageTemporal must execute (OK), got %s: %s", temporalNode.Status, temporalNode.Detail)
	}
	if !strings.Contains(temporalNode.Detail, "temporal inference over") {
		t.Fatalf("expected a genuine inference detail describing the real hbayes run, got %q", temporalNode.Detail)
	}
	if temporalNode.Hash == "" {
		t.Fatal("expected a non-empty node hash")
	}
}

// TestRunUnifiedTemporalBayesianIsReplayDeterministic is the
// adversarial companion: two RunUnified calls over byte-identical
// input and the SAME calibration registry must produce an identical
// TEMPORAL_BAYESIAN trace hash -- hbayes.Model.Infer's own purity
// (documented on the package itself) must survive being called through
// this new production bridge, not just in isolation.
func TestRunUnifiedTemporalBayesianIsReplayDeterministic(t *testing.T) {
	reg := fixtureTemporalCalibration(t)
	run := func() string {
		o := NewOrchestrator(nil, nil)
		o.TemporalCalibration = reg
		in := testIntent()
		plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
		res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range res.Execution.Trace.Nodes {
			if n.StageID == execution.StageTemporal {
				return n.Hash
			}
		}
		t.Fatal("expected a TEMPORAL_BAYESIAN node")
		return ""
	}
	h1, h2 := run(), run()
	if h1 == "" || h1 != h2 {
		t.Fatalf("expected identical, non-empty temporal trace hashes across two runs, got %q vs %q", h1, h2)
	}
}

// TestRunUnifiedTemporalBayesianExcludesUncalibratedValuesHonestly is
// the fail-closed-per-submission adversarial case: a submission whose
// Value has no entry in the registered LikelihoodTable must be
// honestly excluded from the observation set, not cause the whole
// case to error out or silently fabricate a likelihood for it. The
// stage must still execute over the submissions that DO have real
// calibration.
func TestRunUnifiedTemporalBayesianExcludesUncalibratedValuesHonestly(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	o.TemporalCalibration = fixtureTemporalCalibration(t)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
	caseIn := testCaseInput("x")
	caseIn.Submissions = append(caseIn.Submissions, canonical.SourceSubmission{
		SourceID: fusion.SourceID("uncalibrated-source"), Value: "UNKNOWN_VALUE", BaseReliability: 0.5,
	})

	res, err := o.RunUnified(context.Background(), in, plan, caseIn)
	if err != nil {
		t.Fatalf("an uncalibrated submission value must not fail the whole case: %v", err)
	}
	var temporalNode execution.Node
	for _, n := range res.Execution.Trace.Nodes {
		if n.StageID == execution.StageTemporal {
			temporalNode = n
		}
	}
	if temporalNode.Status != execution.StatusOK {
		t.Fatalf("the calibrated submissions must still drive real inference despite one uncalibrated submission, got %s", temporalNode.Status)
	}
}

// TestRunUnifiedProducesARealCorrelatedSpanAndKey is the direct proof
// for audit item P1-07 ("Unified Observability"): RunUnified previously
// carried zero telemetry of its own (confirmed by grep before this
// change), and pkg/platform/correlation.Key had zero production callers
// anywhere in the repository. Both gaps are closed together here: a
// real span named "lifecycle.RunUnified" must exist, carrying the same
// real IDs (not placeholders) the returned Result.Correlation also
// carries -- proving the span and the Key are reporting the SAME
// underlying execution, not two independently-plausible-looking values.
func TestRunUnifiedProducesARealCorrelatedSpanAndKey(t *testing.T) {
	prior := telemetry.Global()
	rec := telemetry.NewRecorder()
	telemetry.SetGlobalTracer(rec)
	defer telemetry.SetGlobalTracer(prior)

	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})

	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("x"))
	if err != nil {
		t.Fatal(err)
	}

	spans := rec.Spans()
	var span *telemetry.SpanRecord
	for i := range spans {
		if spans[i].Name == "lifecycle.RunUnified" {
			span = &spans[i]
		}
	}
	if span == nil {
		t.Fatal("expected a real lifecycle.RunUnified span to be recorded")
	}
	if !span.Ended {
		t.Fatal("expected the span to have been ended (defer span.End())")
	}

	attr := map[string]string{}
	for _, a := range span.Attributes {
		attr[a.Key] = a.Value
	}
	want := map[string]string{
		"intent_id":                   res.Correlation.IntentID,
		"tenant_id":                   in.Tenant,
		"entity_id":                   string(res.EntityID),
		"execution_id":                res.Correlation.ExecutionID,
		"decision_id":                 res.Correlation.DecisionID,
		"evidence_package_id":         res.Correlation.EvidencePackageID,
		"verification_certificate_id": res.Correlation.VerificationCertificateID,
		"replay_package_id":           res.Correlation.ReplayPackageID,
		"entity_identity_ledger_head": res.Correlation.EntityIdentityLedgerHead,
	}
	for key, wantVal := range want {
		if wantVal == "" {
			t.Fatalf("test setup produced an empty expected value for %q -- cannot prove correlation with a placeholder", key)
		}
		if attr[key] != wantVal {
			t.Fatalf("span attribute %q = %q, want %q (must match Result.Correlation exactly -- same execution, same IDs)", key, attr[key], wantVal)
		}
	}
	// P0-F: EntityIdentityLedgerHead must be the SAME real ledger head
	// pkg/execution's own IDENTITY_RESOLUTION stage committed into its
	// node hash -- not a second, disconnected value that merely looks
	// non-empty.
	var idNode execution.Node
	for _, n := range res.Execution.Trace.Nodes {
		if n.StageID == execution.StageIdentityResolution {
			idNode = n
		}
	}
	if !strings.Contains(idNode.Detail, shortHeadFor(res.Correlation.EntityIdentityLedgerHead)) {
		t.Fatalf("expected IDENTITY_RESOLUTION's own node detail (%q) to reference the same ledger head correlation reports (%q)",
			idNode.Detail, res.Correlation.EntityIdentityLedgerHead)
	}
}

// shortHeadFor mirrors pkg/execution's own unexported shortHash
// truncation (12 chars + "...") so this test can check the ledger head
// correlation reports is the SAME one IDENTITY_RESOLUTION's human-
// readable node detail already displays, without needing that
// unexported helper to be exported just for a test.
func shortHeadFor(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12] + "..."
}

// --- PHASE D2 (P0-5): case lineage ------------------------------------

// TestCaseLineageWalksARealRunEndToEndFromOneCaseID is PHASE D2's
// acceptance criterion proved against a REAL RunUnified execution, not
// a hand-built fixture: one CaseID, and Intent, Entity, Evidence,
// Policy, Decision, Verification, Replay, the identity ledger head and
// (after RecordOutcome) the Outcome are all reachable from it, in
// dependency order, with completeness = true and a verifying hash
// chain.
func TestCaseLineageWalksARealRunEndToEndFromOneCaseID(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	o.Lineage = lineage.NewLedger()

	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("placeholder"))
	if err != nil {
		t.Fatalf("RunUnified: %v", err)
	}
	if res.CaseID == "" {
		t.Fatal("a real run produced no CaseID")
	}
	if string(res.CaseID) != in.ID() {
		t.Fatalf("CaseID = %q, want the Intent's own ID %q -- a case must not mint a second identity", res.CaseID, in.ID())
	}

	// Before ground truth exists, the case is honestly incomplete.
	if comp := o.Lineage.Completeness(res.CaseID); comp.Complete {
		t.Fatal("a case reported Complete before any Outcome was recorded")
	} else if len(comp.MissingKinds) != 1 || comp.MissingKinds[0] != lineage.KindOutcome {
		t.Fatalf("MissingKinds = %v, want exactly [OUTCOME]", comp.MissingKinds)
	}

	if _, err := o.RecordOutcome(res, "OFF", "port-state-inspection", 100); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	comp := o.Lineage.Completeness(res.CaseID)
	if !comp.Complete {
		t.Fatalf("case lineage completeness = false after a full run: missing=%v dangling=%v chain=%v",
			comp.MissingKinds, comp.Dangling, comp.ChainVerified)
	}

	nodes, err := o.Lineage.Walk(res.CaseID)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	seen := map[string]bool{}
	byKind := map[lineage.Kind]int{}
	for i, n := range nodes {
		for _, u := range n.Upstream {
			if !seen[u] {
				t.Fatalf("node %d (%s/%s) depends on %s before it appears", i, n.Kind, n.Ref, u)
			}
		}
		seen[n.Ref] = true
		byKind[n.Kind]++
	}
	for _, want := range []lineage.Kind{
		lineage.KindIntent, lineage.KindEntity, lineage.KindEvidence, lineage.KindEvent,
		lineage.KindPolicy, lineage.KindDecision, lineage.KindVerification,
		lineage.KindReplay, lineage.KindOutcome,
	} {
		if byKind[want] == 0 {
			t.Errorf("kind %s is not reachable from the CaseID of a real run", want)
		}
	}
	// One EVIDENCE node per real source submission of this case, plus
	// the one the execution's own EvidencePackageID contributes.
	if want := len(testCaseInput("x").Submissions) + 1; byKind[lineage.KindEvidence] != want {
		t.Errorf("EVIDENCE nodes = %d, want %d (one per source submission plus the evidence package)",
			byKind[lineage.KindEvidence], want)
	}
	if err := o.Lineage.VerifyChain(res.CaseID); err != nil {
		t.Fatalf("VerifyChain on a real run's lineage: %v", err)
	}

	// Every identifier in the lineage is one the run genuinely produced.
	if !seen[res.Correlation.DecisionID] {
		t.Error("the lineage's DECISION node does not carry this run's real DecisionID")
	}
	if !seen[res.Correlation.VerificationCertificateID] {
		t.Error("the lineage's VERIFICATION node does not carry this run's real certificate ID")
	}
	if !seen[res.Correlation.EntityIdentityLedgerHead] {
		t.Error("the lineage does not carry this run's real identity ledger head")
	}
}

// TestCaseLineageIsOptInAndInertWhenUnset records the deliberate
// design: an Orchestrator with no Lineage ledger behaves exactly as it
// did before this phase, so nothing about adding case lineage changes
// any existing caller's results.
func TestCaseLineageIsOptInAndInertWhenUnset(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("placeholder"))
	if err != nil {
		t.Fatalf("RunUnified with no lineage ledger: %v", err)
	}
	if res.CaseID == "" {
		t.Fatal("CaseID must still be populated even with no ledger attached")
	}
	if _, err := o.RecordOutcome(res, "OFF", "port-state-inspection", 100); err != nil {
		t.Fatalf("RecordOutcome with no lineage ledger: %v", err)
	}
}

// TestTwoCasesDoNotShareALineage proves the CaseID is genuinely the
// aggregation boundary: two different Intents produce two separate,
// independently-verifiable lineages.
func TestTwoCasesDoNotShareALineage(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	o.Lineage = lineage.NewLedger()

	first := testIntent()
	second := testIntent()
	second.Objective = "assess sanctions-evasion risk"
	second.Tick = 2

	for i, in := range []Intent{first, second} {
		plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
		// Distinct ticks per case: the shared fusion ledger content-
		// addresses each submission, so replaying byte-identical
		// submissions is (correctly) refused as a duplicate. That refusal
		// is pre-existing, load-bearing behaviour of pkg/moat/fusion, not
		// anything case lineage introduced.
		caseIn := testCaseInput("placeholder")
		caseIn.Tick = uint64(i + 1)
		if _, err := o.RunUnified(context.Background(), in, plan, caseIn); err != nil {
			t.Fatalf("RunUnified: %v", err)
		}
	}
	ids := o.Lineage.CaseIDs()
	if len(ids) != 2 {
		t.Fatalf("got %d cases, want 2 -- two Intents must not collapse into one case", len(ids))
	}
	for _, id := range ids {
		if err := o.Lineage.VerifyChain(id); err != nil {
			t.Fatalf("VerifyChain(%s): %v", id, err)
		}
	}
}

// --- PHASE B (P0-2): legacy identity fallback is never silent --------

// TestLegacyIdentityFallbackIsLoudlyMarked proves the fallback path
// announces itself. An entity resolved outside the canonical identity
// authority is a real answer, but it must never be indistinguishable
// from a canonically-resolved one.
func TestLegacyIdentityFallbackIsLoudlyMarked(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent()
	// A Kind pkg/identity deliberately does not model. Using an alias
	// vocabulary identity.Kind covers would take the canonical path and
	// prove nothing.
	in.EntityAliases = []entity.Alias{{Kind: "TERMINAL_CRANE_SERIAL", Value: "TC-88"}}

	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("placeholder"))
	if err != nil {
		t.Fatalf("RunUnified: %v", err)
	}

	if !res.LegacyIdentityFallbackUsed {
		t.Fatal("an unmodelled alias Kind resolved through the fallback without LegacyIdentityFallbackUsed being set")
	}
	if !res.HumanReviewRequired {
		t.Fatal("the fallback was used but HumanReviewRequired is false")
	}
	if len(res.UnmappedAliasKinds) != 1 || res.UnmappedAliasKinds[0] != "TERMINAL_CRANE_SERIAL" {
		t.Fatalf("UnmappedAliasKinds = %v, want [TERMINAL_CRANE_SERIAL]", res.UnmappedAliasKinds)
	}
	// The fallback must NOT have written canonical identity: the ledger
	// head is unchanged from an untouched resolver's.
	if o.Identity.Head() != identity.NewResolver().Head() {
		t.Fatal("the legacy fallback wrote to the canonical identity ledger -- it must never do that")
	}
}

// TestCanonicalIdentityPathIsNeverMarkedAsAFallback is the control: a
// run whose aliases pkg/identity does model must report no fallback and
// no review requirement, so the markers above mean something.
func TestCanonicalIdentityPathIsNeverMarkedAsAFallback(t *testing.T) {
	o := NewOrchestrator(nil, nil)
	in := testIntent() // IMO + CALLSIGN, both modelled
	plan := PlanEvidence(in, []EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
	res, err := o.RunUnified(context.Background(), in, plan, testCaseInput("placeholder"))
	if err != nil {
		t.Fatalf("RunUnified: %v", err)
	}
	if res.LegacyIdentityFallbackUsed {
		t.Fatal("a canonically-resolved run was marked as a legacy fallback")
	}
	// HumanReviewRequired has TWO independent causes since the
	// canonical-truth-path round wired trust into every decision: an
	// identity fallback, and a trust evaluation that placed any source
	// in a RESTRICTED/EXCLUDED posture. This test's subject is the
	// former, so it asserts on the IDENTITY cause specifically. A run
	// whose sources have never been assessed for trust legitimately does
	// demand review, and asserting otherwise here would have made this
	// test a barrier to trust participating at all -- which is exactly
	// the inert-module failure mode the mandate exists to close.
	if res.HumanReviewRequired && !res.Canonical.Trust.ReviewRequired {
		t.Fatal("a canonically-resolved run demanded human review for a reason other than trust")
	}
	if len(res.TrustReviewReasons) != len(res.Canonical.Trust.ReviewReasons) {
		t.Fatalf("trust review reasons were not surfaced on the lifecycle result: %v vs %v",
			res.TrustReviewReasons, res.Canonical.Trust.ReviewReasons)
	}
	if len(res.UnmappedAliasKinds) != 0 {
		t.Fatalf("UnmappedAliasKinds = %v on a fully-modelled run", res.UnmappedAliasKinds)
	}
	if o.Identity.Head() == "" {
		t.Fatal("the canonical path did not write the identity ledger at all")
	}
}
