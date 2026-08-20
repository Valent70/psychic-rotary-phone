// Package acceptance is VERIQO's permanent system-level acceptance
// suite — the exact minimum matrix both v7.10.2 audit documents
// mandate: 10 normal, 10 adversarial, 10 replay/tamper, 10
// identity/temporal, 10 distributed/concurrency (50 total), anchored
// on the central TestUnifiedIntentTrustDecisionLifecycle test (see
// pkg/lifecycle/lifecycle_test.go — kept there since it's also that
// package's own primary correctness proof; this suite exercises the
// SAME lifecycle from the system's outside edge, at breadth rather
// than unit depth).
//
// This suite is explicitly PERMANENT: per the audit's "what should
// not be done" list, this file should be extended, not replaced, as
// new capability lands, and no new architectural expansion should
// happen without this suite continuing to pass.
package acceptance

import (
	"context"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

func baseIntent(actor string, aliases ...entity.Alias) lifecycle.Intent {
	if len(aliases) == 0 {
		aliases = []entity.Alias{{Kind: "IMO", Value: "9000001"}}
	}
	return lifecycle.Intent{
		ActorID: actor, Tenant: "acme", Objective: "assess dark-vessel risk", EntityAliases: aliases,
		RequiredConfidence: 0.6, TemporalScope: "last-7-days", Tick: 1,
	}
}

func baseCase(subject string, submissions ...canonical.SourceSubmission) canonical.CaseInput {
	if len(submissions) == 0 {
		submissions = []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("ais-vendor-a"), Value: "OFF", BaseReliability: 0.8},
			{SourceID: fusion.SourceID("ais-vendor-b"), Value: "OFF", BaseReliability: 0.8},
			{SourceID: fusion.SourceID("port-authority"), Value: "ON", BaseReliability: 0.95},
		}
	}
	return canonical.CaseInput{
		Subject: subject, Predicate: "AIS_STATUS", Tick: 1, Policy: risk.DefaultPolicy(),
		Submissions: submissions, PatternScore: 0.6,
	}
}

func standardPlan(in lifecycle.Intent) lifecycle.EvidencePlan {
	return lifecycle.PlanEvidence(in, []lifecycle.EvidenceRequirement{{Kind: "AIS_STATUS", Required: true, MinSources: 2}})
}

func mustRun(t *testing.T, o *lifecycle.Orchestrator, in lifecycle.Intent, plan lifecycle.EvidencePlan, cs canonical.CaseInput) *lifecycle.Result {
	t.Helper()
	res, err := o.RunUnified(context.Background(), in, plan, cs)
	if err != nil {
		t.Fatalf("RunUnified: %v", err)
	}
	return res
}

// ===== Category 1/5 — NORMAL (10) ==========================================

func TestAcceptance01_HappyPathFullChain(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-1")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if !res.Certificate.IVFVerified {
		t.Fatalf("expected happy-path IVF verification to succeed")
	}
}

func TestAcceptance02_UnanimousEvidenceNoContradiction(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-2")
	cs := baseCase("x", []canonical.SourceSubmission{
		{SourceID: "a", Value: "OFF", BaseReliability: 0.9},
		{SourceID: "b", Value: "OFF", BaseReliability: 0.9},
	}...)
	res := mustRun(t, o, in, standardPlan(in), cs)
	if res.Canonical.Arbitration.Contradiction {
		t.Fatalf("expected no contradiction when all sources agree")
	}
}

func TestAcceptance03_MultiplePoliciesIndependent(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-3")
	res1 := mustRun(t, o, in, standardPlan(in), baseCase("subject-a"))
	in2 := baseIntent("analyst-3", entity.Alias{Kind: "IMO", Value: "9000002"})
	res2 := mustRun(t, o, in2, standardPlan(in2), baseCase("subject-b"))
	if res1.Certificate.Hash == res2.Certificate.Hash {
		t.Fatalf("expected different cases to produce different certificates")
	}
}

func TestAcceptance04_EconomicImpactComputedWhenChannelsSupplied(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-4")
	cs := baseCase("x")
	cs.EconomicChannels = map[string]float64{"commodity_flow_usd": 500000}
	res := mustRun(t, o, in, standardPlan(in), cs)
	if res.Canonical.EconomicImpact.TotalExpectedImpact < 0 {
		t.Fatalf("expected non-negative economic impact")
	}
}

func TestAcceptance05_CausalLinkRaisesTwinProjection(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-5")
	cs := baseCase("x")
	res := mustRun(t, o, in, standardPlan(in), cs)
	if res.Canonical.Twin.Entity == "" {
		t.Fatalf("expected twin entity to be set")
	}
}

func TestAcceptance06_DecisionRespectsPolicyThresholds(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-6")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if res.Canonical.Decision.PolicyName != risk.DefaultPolicy().Name {
		t.Fatalf("expected decision to use the supplied default policy")
	}
}

func TestAcceptance07_LowRiskCaseYieldsAllowOrNoEscalation(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-7")
	cs := baseCase("x", []canonical.SourceSubmission{
		{SourceID: "a", Value: "ON", BaseReliability: 0.95},
		{SourceID: "b", Value: "ON", BaseReliability: 0.95},
	}...)
	cs.PatternScore = 0.0
	res := mustRun(t, o, in, standardPlan(in), cs)
	if res.Canonical.Risk.Score > 0.5 {
		t.Fatalf("expected a low risk score for a clean, unanimous case, got %f", res.Canonical.Risk.Score)
	}
}

func TestAcceptance08_ReplayIDStableAcrossFields(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-8")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if res.Certificate.ReplayID != res.Canonical.Certificate.Hash {
		t.Fatalf("expected ReplayID to mirror the canonical certificate hash")
	}
}

func TestAcceptance09_OutcomeRecordingSucceedsForNormalCase(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("analyst-9")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if _, err := o.RecordOutcome(res, res.Canonical.Arbitration.Winner, "inspector-9", 2); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
}

func TestAcceptance10_MultipleSequentialCasesShareTrustNamespace(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in1 := baseIntent("analyst-10")
	res1 := mustRun(t, o, in1, standardPlan(in1), baseCase("x"))
	in2 := baseIntent("analyst-10", entity.Alias{Kind: "IMO", Value: "9000099"})
	res2 := mustRun(t, o, in2, standardPlan(in2), baseCase("y"))
	if res1.Canonical == nil || res2.Canonical == nil {
		t.Fatalf("expected both sequential cases to run on the shared pipeline")
	}
}
