package acceptance

import (
	"context"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/entity"
)

// ===== Category 2/5 — ADVERSARIAL (10) ======================================

func TestAcceptance11_EmptySubmissionsRejected(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-1")
	cs := baseCase("x")
	cs.Submissions = nil
	if _, err := o.RunUnified(context.Background(), in, standardPlan(in), cs); err == nil {
		t.Fatalf("expected an error for empty submissions")
	}
}

func TestAcceptance12_CorrelatedSourcesShareProvider(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-2")
	cs := baseCase("x", []canonical.SourceSubmission{
		{SourceID: "sat-a", Value: "OFF", BaseReliability: 0.9, Provider: "shared-upstream"},
		{SourceID: "sat-b", Value: "OFF", BaseReliability: 0.9, Provider: "shared-upstream"},
	}...)
	res := mustRun(t, o, in, standardPlan(in), cs)
	if res.Canonical.Provenance.Status == "" {
		t.Fatalf("expected a computed provenance status even for correlated sources")
	}
}

func TestAcceptance13_ContradictorySourcesDetected(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-3")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x")) // baseCase has 2 OFF vs 1 ON
	if !res.Canonical.Arbitration.Contradiction {
		t.Fatalf("expected contradiction to be detected between disagreeing sources")
	}
}

func TestAcceptance14_ExactDuplicateEvidenceRejected(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-4")
	cs := baseCase("x", []canonical.SourceSubmission{
		{SourceID: "dup", Value: "OFF", BaseReliability: 0.8},
		{SourceID: "dup2", Value: "OFF", BaseReliability: 0.8},
	}...)
	// Submit the exact same (source, claim, value, tick) evidence twice
	// by duplicating a submission verbatim — fusion.Engine's own
	// deriveEvidenceID-based dedup must reject the second one rather
	// than silently double-counting identical evidence.
	cs.Submissions = append(cs.Submissions, cs.Submissions[0])
	_, err := o.RunUnified(context.Background(), in, standardPlan(in), cs)
	if err == nil {
		t.Fatalf("expected an error submitting byte-identical duplicate evidence")
	}
}

func TestAcceptance15_ZeroReliabilitySourceStillProcessed(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-5")
	cs := baseCase("x", []canonical.SourceSubmission{
		{SourceID: "weak", Value: "OFF", BaseReliability: 0.01},
		{SourceID: "strong", Value: "ON", BaseReliability: 0.99},
	}...)
	res := mustRun(t, o, in, standardPlan(in), cs)
	if res.Canonical.Arbitration.Winner != "ON" {
		t.Fatalf("expected the high-reliability source to win, got %s", res.Canonical.Arbitration.Winner)
	}
}

func TestAcceptance16_MalformedAliasRejected(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-6", entity.Alias{Kind: "", Value: "bad"})
	_, err := o.RunUnified(context.Background(), in, standardPlan(in), baseCase("x"))
	if err == nil {
		t.Fatalf("expected an error resolving a malformed (empty-Kind) alias")
	}
}

func TestAcceptance17_UnsatisfiableRequiredEvidenceBlocked(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-7")
	plan := lifecycle.PlanEvidence(in, []lifecycle.EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 999}})
	if _, err := o.RunUnified(context.Background(), in, plan, baseCase("x")); err == nil {
		t.Fatalf("expected ErrPlanUnsatisfied for an impossible MinSources requirement")
	}
}

func TestAcceptance18_GroundTruthFromArbitrationInputRejected(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-8")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	// "ais-vendor-a" was one of the case's own arbitration inputs —
	// using it as the ground-truth source would be the self-
	// reinforcing loop calibration.Engine is specifically built to
	// reject.
	if _, err := o.RecordOutcome(res, "OFF", "ais-vendor-a", 2); err == nil {
		t.Fatalf("expected RecordOutcome to reject a ground-truth source that was itself an arbitration input")
	}
}

func TestAcceptance19_MismatchedOutcomeStillRecordsPoorCalibration(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-9")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	wrong := "ON"
	if res.Canonical.Arbitration.Winner == "ON" {
		wrong = "OFF"
	}
	rec, err := o.RecordOutcome(res, wrong, "inspector-adv-9", 2)
	if err != nil {
		t.Fatalf("RecordOutcome should still succeed on a mismatch (that's the point of calibration): %v", err)
	}
	if rec.Matched {
		t.Fatalf("expected Matched=false when ground truth disagrees with the prediction")
	}
	if rec.BrierScore <= 0 {
		t.Fatalf("expected a nonzero Brier score penalty for a miscalibrated confident-and-wrong prediction")
	}
}

func TestAcceptance20_HighPatternScoreEscalatesDecision(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("adv-10")
	cs := baseCase("x")
	cs.PatternScore = 1.0
	cs.PriceAnomaly = 1.0
	res := mustRun(t, o, in, standardPlan(in), cs)
	if res.Canonical.Risk.Score < 0.3 {
		t.Fatalf("expected an elevated risk score under maximal pattern/anomaly signal, got %f", res.Canonical.Risk.Score)
	}
}
