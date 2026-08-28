package engine

import (
	"strings"
	"testing"

	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/kg"
	"veriqo/pkg/platform/observability"
	"veriqo/pkg/verification"
)

func newTestGraph() *kg.Graph { return kg.NewGraph() }

func newMetricsForTest() *observability.MOATMetrics { return observability.NewMOATMetrics() }

func contradictionObs(claimKey, sourceID, value string, reliability float64, tick uint64) contradiction.RawObservation {
	return contradiction.RawObservation{
		ClaimKey: claimKey, SourceID: sourceID, Value: value,
		SourceReliability: reliability, Tick: tick,
	}
}

func setupFusionAdapter(t *testing.T) *FusionEngineAdapter {
	t.Helper()
	a := NewFusionEngineAdapter("orchestrator")
	must(t, a.RegisterSource(fusion.SourceProfile{ID: "ais-a", BaseReliability: 0.9}))
	must(t, a.RegisterSource(fusion.SourceProfile{ID: "ais-b", BaseReliability: 0.85}))
	must(t, a.RegisterSource(fusion.SourceProfile{ID: "port", BaseReliability: 0.95}))
	claim := fusion.Claim{Subject: "VESSEL:1", Predicate: "FLAG_STATE"}
	if _, err := a.Submit("ais-a", claim, "PANAMA", 1); err != nil {
		t.Fatalf("submit ais-a: %v", err)
	}
	if _, err := a.Submit("ais-b", claim, "PANAMA", 1); err != nil {
		t.Fatalf("submit ais-b: %v", err)
	}
	if _, err := a.Submit("port", claim, "LIBERIA", 1); err != nil {
		t.Fatalf("submit port: %v", err)
	}
	return a
}

func TestFusionAdapterRunsThroughFullLifecycle(t *testing.T) {
	k := NewKernel()
	a := setupFusionAdapter(t)
	must(t, k.Register(a))

	rec, err := k.Run("fusion", Context{Subject: "VESSEL:1|FLAG_STATE", Tick: 1})
	must(t, err)
	if rec.Result.Score <= 0 {
		t.Fatalf("expected nonzero confidence score, got %f", rec.Result.Score)
	}
	if len(rec.Result.Evidence) != 1 {
		t.Fatalf("expected 1 evidence record, got %d", len(rec.Result.Evidence))
	}
	if !rec.Replayable {
		t.Fatalf("expected fusion engine to report replayable after a clean run")
	}
	if a.ClaimedResult() == "" {
		t.Fatalf("expected non-empty ClaimedResult after Evaluate")
	}
	if a.IVFDomain() != "fusion" {
		t.Fatalf("IVFDomain = %q, want %q", a.IVFDomain(), "fusion")
	}
}

func TestFusionAdapterLoadContextRejectsEmptySubject(t *testing.T) {
	a := NewFusionEngineAdapter("orchestrator")
	if err := a.LoadContext(Context{Subject: ""}); err == nil {
		t.Fatalf("expected error for empty subject")
	}
}

func TestFusionAdapterLoadContextRejectsMalformedSubject(t *testing.T) {
	a := NewFusionEngineAdapter("orchestrator")
	if err := a.LoadContext(Context{Subject: "no-pipe-here"}); err == nil {
		t.Fatalf("expected error for subject missing the subject|predicate separator")
	}
}

func TestFusionAdapterClaimedResultEmptyBeforeEvaluate(t *testing.T) {
	a := NewFusionEngineAdapter("orchestrator")
	if got := a.ClaimedResult(); got != "" {
		t.Fatalf("ClaimedResult before any Evaluate = %q, want empty", got)
	}
	if a.Replayable() {
		t.Fatalf("Replayable before any Evaluate should be false")
	}
}

func TestFusionAdapterRawEvidenceErrorsWithNoEvidence(t *testing.T) {
	a := NewFusionEngineAdapter("orchestrator")
	must(t, a.LoadContext(Context{Subject: "VESSEL:9|FLAG_STATE", Tick: 1}))
	if _, err := a.RawEvidence(); err == nil {
		t.Fatalf("expected error when no evidence has been submitted for the claim")
	}
}

func TestFusionAdapterIVFRoundTripsThroughRunAndVerify(t *testing.T) {
	k := NewKernel()
	a := setupFusionAdapter(t)
	must(t, k.Register(a))

	vrec, err := k.RunAndVerify("fusion", Context{Subject: "VESSEL:1|FLAG_STATE", Tick: 1})
	must(t, err)
	if vrec.VerificationResult == nil {
		t.Fatalf("expected a VerificationResult for the IVFCapable fusion adapter")
	}
	if !vrec.VerificationResult.ManifestValid || !vrec.VerificationResult.ReplayValid {
		t.Fatalf("expected genuine pass on untampered fusion run: %+v", vrec.VerificationResult)
	}
	if vrec.Bundle == nil {
		t.Fatalf("expected a built VerificationBundle")
	}
}

func TestFusionAdapterRawEvidenceRecordsAreReplayConsistent(t *testing.T) {
	a := setupFusionAdapter(t)
	must(t, a.LoadContext(Context{Subject: "VESSEL:1|FLAG_STATE", Tick: 1}))
	if _, err := a.Evaluate(); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	records, err := a.RawEvidence()
	must(t, err)
	if len(records) != 4 { // 3 evidence + 1 params record
		t.Fatalf("expected 4 raw evidence records (3 evidence + params), got %d", len(records))
	}
	got, err := ReplayFusion(verification.EvidencePackage{Subject: "VESSEL:1|FLAG_STATE", Records: records})
	must(t, err)
	if got != a.ClaimedResult() {
		t.Fatalf("independent replay = %q, want exact match with ClaimedResult %q", got, a.ClaimedResult())
	}
}

func TestReplayFusionMissingParamsErrors(t *testing.T) {
	if _, err := ReplayFusion(verification.EvidencePackage{Subject: "x", Records: nil}); err == nil {
		t.Fatalf("expected error when bundle has no fusion.ArbitrationParams record")
	}
}

func TestReplayContradictionMissingParamsErrors(t *testing.T) {
	if _, err := ReplayContradiction(verification.EvidencePackage{Subject: "x", Records: nil}); err == nil {
		t.Fatalf("expected error when bundle has no contradiction.ArbitrationParams record")
	}
}

func TestReplayContradictionArbitrateErrorsWithNoObservations(t *testing.T) {
	// A bundle carrying only the params record (no observations) must
	// surface ArbitrateClaim's own "no observations" error, not panic
	// or silently succeed.
	a := NewContradictionArbitrationEngine(50)
	must(t, a.LoadContext(Context{Subject: "VESSEL:9", Tick: 1}))
	must(t, a.Observe(contradictionObs("VESSEL:9", "s1", "A", 0.9, 1)))
	if _, err := a.Evaluate(); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	records, err := a.RawEvidence()
	must(t, err)
	// Keep only the trailing ArbitrationParams record, dropping every
	// raw observation, to force ArbitrateClaim to fail on replay.
	paramsOnly := records[len(records)-1:]
	if _, err := ReplayContradiction(verification.EvidencePackage{Subject: "VESSEL:9", Records: paramsOnly}); err == nil {
		t.Fatalf("expected ReplayContradiction to fail when replayed with zero observations")
	}
}

func TestReplayDecisionEmptyBundleErrors(t *testing.T) {
	replay := ReplayDecision(testPolicy())
	if _, err := replay(verification.EvidencePackage{Subject: "x", Records: nil}); err == nil {
		t.Fatalf("expected error replaying an empty decision context")
	}
}

func TestReplayDecisionUnknownPolicyErrors(t *testing.T) {
	a, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	must(t, a.LoadContext(Context{Subject: "VESSEL:1", Tick: 1, Values: map[string]float64{"ais_anomaly": 0.5, "insurance_mismatch": 0.5}}))
	if _, err := a.Evaluate(); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	records, err := a.RawEvidence()
	must(t, err)
	// Replay against a freshly-constructed engine that only knows a
	// DIFFERENT policy than the one named in the bundle's own
	// decision.Context record — this must surface
	// decision.ErrUnknownPolicy, proving replay doesn't silently
	// substitute whatever policy happens to be registered.
	otherPolicy := testPolicy()
	otherPolicy.Name = "a_different_policy_name"
	replay := ReplayDecision(otherPolicy)
	if _, err := replay(verification.EvidencePackage{Subject: "VESSEL:1", Records: records}); err == nil {
		t.Fatalf("expected ReplayDecision to fail against a mismatched policy")
	}
}

func TestReplayFusionArbitrateErrorsWithNoEvidence(t *testing.T) {
	a := setupFusionAdapter(t)
	must(t, a.LoadContext(Context{Subject: "VESSEL:1|FLAG_STATE", Tick: 1}))
	if _, err := a.Evaluate(); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	records, err := a.RawEvidence()
	must(t, err)
	paramsOnly := records[len(records)-1:]
	if _, err := ReplayFusion(verification.EvidencePackage{Subject: "VESSEL:1|FLAG_STATE", Records: paramsOnly}); err == nil {
		t.Fatalf("expected ReplayFusion to fail when replayed with zero evidence")
	}
}

func TestBuildFusionBundleDirectly(t *testing.T) {
	a := setupFusionAdapter(t)
	claim := fusion.Claim{Subject: "VESSEL:1", Predicate: "FLAG_STATE"}
	must(t, a.LoadContext(Context{Subject: "VESSEL:1|FLAG_STATE", Tick: 1}))
	res, err := a.Evaluate()
	must(t, err)
	_ = res
	// Drive fusion.Engine directly (the cmd/veriqo-demo-style entry
	// point), not through the adapter, per BuildFusionBundle's own doc
	// comment.
	inner := fusion.NewEngine(newTestGraph())
	must(t, inner.RegisterSource(fusion.SourceProfile{ID: "s1", BaseReliability: 0.9}))
	if _, err := inner.Submit("s1", claim, "PANAMA", 1); err != nil {
		t.Fatalf("submit: %v", err)
	}
	ar, err := inner.Arbitrate("orchestrator", claim, 1)
	must(t, err)
	bundle, err := BuildFusionBundle(inner, claim, "orchestrator", 1, ar)
	must(t, err)
	if bundle.Manifest.RootHash == "" {
		t.Fatalf("expected a non-empty root hash on the built bundle")
	}
}

func TestBuildFusionBundleErrorsWithNoEvidence(t *testing.T) {
	inner := fusion.NewEngine(newTestGraph())
	claim := fusion.Claim{Subject: "VESSEL:2", Predicate: "FLAG_STATE"}
	if _, err := BuildFusionBundle(inner, claim, "orchestrator", 1, fusion.ArbitrationResult{}); err == nil {
		t.Fatalf("expected error building a bundle for a claim with no submitted evidence")
	}
}

// --- Contradiction adapter: remaining error/edge branches -----------

func TestContradictionAdapterLoadContextRejectsEmptySubject(t *testing.T) {
	a := NewContradictionArbitrationEngine(50)
	if err := a.LoadContext(Context{Subject: ""}); err == nil {
		t.Fatalf("expected error for empty subject")
	}
}

func TestContradictionAdapterClaimedResultEmptyBeforeEvaluate(t *testing.T) {
	a := NewContradictionArbitrationEngine(50)
	if got := a.ClaimedResult(); got != "" {
		t.Fatalf("ClaimedResult before Evaluate = %q, want empty", got)
	}
	if a.Replayable() {
		t.Fatalf("Replayable before Evaluate should be false")
	}
}

func TestContradictionAdapterRawEvidenceErrorsWithNoObservations(t *testing.T) {
	a := NewContradictionArbitrationEngine(50)
	must(t, a.LoadContext(Context{Subject: "VESSEL:9", Tick: 1}))
	if _, err := a.RawEvidence(); err == nil {
		t.Fatalf("expected error when no observations have been fed")
	}
}

func TestContradictionAdapterIVFRoundTripsThroughRunAndVerify(t *testing.T) {
	k := NewKernel()
	a := NewContradictionArbitrationEngine(50)
	must(t, k.Register(a))
	must(t, a.Observe(contradictionObs("VESSEL:1|FLAG", "ais-a", "PANAMA", 0.9, 1)))
	must(t, a.Observe(contradictionObs("VESSEL:1|FLAG", "port", "LIBERIA", 0.95, 1)))

	vrec, err := k.RunAndVerify("contradiction", Context{Subject: "VESSEL:1|FLAG", Tick: 1})
	must(t, err)
	if vrec.VerificationResult == nil || !vrec.VerificationResult.ManifestValid || !vrec.VerificationResult.ReplayValid {
		t.Fatalf("expected genuine IVF pass for contradiction adapter, got %+v", vrec.VerificationResult)
	}
}

// --- Decision adapter: remaining error branches ----------------------

func TestDecisionAdapterLoadContextRejectsEmptySubject(t *testing.T) {
	a, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	if err := a.LoadContext(Context{Subject: ""}); err == nil {
		t.Fatalf("expected error for empty subject")
	}
}

func TestDecisionAdapterClaimedResultEmptyBeforeEvaluate(t *testing.T) {
	a, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	if got := a.ClaimedResult(); got != "" {
		t.Fatalf("ClaimedResult before Evaluate = %q, want empty", got)
	}
}

func TestNewDecisionPolicyEngineRejectsInvalidPolicy(t *testing.T) {
	bad := testPolicy()
	bad.Factors = nil // an empty-factor policy is rejected by decision.Engine.RegisterPolicy
	if _, err := NewDecisionPolicyEngine(bad, "orchestrator"); err == nil {
		t.Fatalf("expected NewDecisionPolicyEngine to surface RegisterPolicy's validation error")
	}
}

// --- Kernel: Initialize-failure and unmapped-stage branches ----------

// alwaysFailInitEngine is a minimal Engine whose Initialize always
// errors, exercising the two call sites (Kernel.InitializeAll and
// Kernel.Run's own lazy-initialize path) that a repository with only
// always-succeeding real adapters never reaches.
type alwaysFailInitEngine struct{ name string }

func (e *alwaysFailInitEngine) Name() string              { return e.name }
func (e *alwaysFailInitEngine) Initialize() error         { return errInitFailed }
func (e *alwaysFailInitEngine) LoadContext(Context) error { return nil }
func (e *alwaysFailInitEngine) Evaluate() (Result, error) { return Result{}, nil }
func (e *alwaysFailInitEngine) Publish(Result) error      { return nil }
func (e *alwaysFailInitEngine) Replayable() bool          { return false }

var errInitFailed = &initError{"forced initialize failure"}

type initError struct{ msg string }

func (e *initError) Error() string { return e.msg }

func TestKernelInitializeAllPropagatesEngineFailure(t *testing.T) {
	k := NewKernel()
	must(t, k.Register(&alwaysFailInitEngine{name: "broken"}))
	if err := k.InitializeAll(); err == nil {
		t.Fatalf("expected InitializeAll to propagate the engine's Initialize error")
	}
}

func TestKernelRunPropagatesLazyInitializeFailure(t *testing.T) {
	k := NewKernel()
	must(t, k.Register(&alwaysFailInitEngine{name: "broken"}))
	if _, err := k.Run("broken", Context{Subject: "x"}); err == nil {
		t.Fatalf("expected Run's lazy Initialize to propagate the engine's error")
	}
}

// noopEngine is a minimal always-succeeding Engine registered under a
// name stageForEngine does not recognize, exercising its default
// branch (distinct from the "decision" case, which returns the same
// value but through a different switch arm).
type noopEngine struct{}

func (noopEngine) Name() string              { return "unmapped-engine" }
func (noopEngine) Initialize() error         { return nil }
func (noopEngine) LoadContext(Context) error { return nil }
func (noopEngine) Evaluate() (Result, error) { return Result{Summary: "ok", Score: 0.5}, nil }
func (noopEngine) Publish(Result) error      { return nil }
func (noopEngine) Replayable() bool          { return true }

func TestKernelRunWithMetricsUsesDefaultStageForUnmappedEngine(t *testing.T) {
	k := NewKernel()
	m := newMetricsForTest()
	k.SetMetrics(m)
	must(t, k.Register(noopEngine{}))
	if _, err := k.Run("unmapped-engine", Context{Subject: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spans := m.Tracer.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 finished span, got %d", len(spans))
	}
	if !strings.Contains(spans[0].Name, "decision") {
		t.Fatalf("expected the unmapped engine to fall back to the decision stage, got %q", spans[0].Name)
	}
}
