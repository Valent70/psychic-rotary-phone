package correlation

import (
	"context"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

func testCaseInput() canonical.CaseInput {
	return canonical.CaseInput{
		Entity: digitaltwin.EntityID("IMO9999999"), Subject: "IMO9999999", Predicate: "port_call",
		Submissions: []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("ais-vendor-a"), Value: "singapore", BaseReliability: 0.8},
			{SourceID: fusion.SourceID("ais-vendor-b"), Value: "singapore", BaseReliability: 0.8},
			{SourceID: fusion.SourceID("port-authority"), Value: "singapore", BaseReliability: 0.9},
		},
		Policy: risk.DefaultPolicy(), Tick: 42,
	}
}

func testContext() execution.Context {
	return execution.Context{
		ExecutionID: "exec-correlation-1", CaseID: "case-1", Tenant: "acme", Actor: "analyst",
		PolicyVersion: "sanctions-screen@3", EvidencePackageID: "evp-correlation-1",
		IdentityResolutionVersion: "ident@2", LedgerPosition: 0, Tick: 42,
		ReplayMetadata: "correlation-unit-test",
	}
}

func realExecutionResult(t *testing.T) execution.Result {
	t.Helper()
	e := execution.NewEngine(nil)
	res, err := e.Run(context.Background(), execution.Input{Context: testContext(), Case: testCaseInput()})
	if err != nil {
		t.Fatalf("execution.Run: %v", err)
	}
	return *res
}

// TestFromExecutionResultCarriesTheRealIdentifiers is the property
// that matters: every non-empty field must be traceable back to the
// SAME execution.Result it was built from, not a coincidentally
// similar value.
func TestFromExecutionResultCarriesTheRealIdentifiers(t *testing.T) {
	res := realExecutionResult(t)
	key := FromExecutionResult(res)

	if key.ExecutionID != res.Trace.Context.ExecutionID || key.ExecutionID != "exec-correlation-1" {
		t.Fatalf("ExecutionID mismatch: got %q", key.ExecutionID)
	}
	if key.EvidencePackageID != res.Trace.Context.EvidencePackageID || key.EvidencePackageID != "evp-correlation-1" {
		t.Fatalf("EvidencePackageID mismatch: got %q", key.EvidencePackageID)
	}
	if key.EntityID == "" || key.EntityID != res.Canonical.Certificate.Subject {
		t.Fatalf("EntityID mismatch: got %q want %q", key.EntityID, res.Canonical.Certificate.Subject)
	}
	if key.DecisionID == "" || key.DecisionID != res.Explanation.DecisionID {
		t.Fatalf("DecisionID mismatch: got %q want %q", key.DecisionID, res.Explanation.DecisionID)
	}
	if key.VerificationCertificateID == "" || key.VerificationCertificateID != res.Certificate.VerificationCertificateID {
		t.Fatalf("VerificationCertificateID mismatch: got %q want %q", key.VerificationCertificateID, res.Certificate.VerificationCertificateID)
	}
	if key.ReplayPackageID == "" || key.ReplayPackageID != res.ReplayPackage.ReplayPackageID {
		t.Fatalf("ReplayPackageID mismatch: got %q want %q", key.ReplayPackageID, res.ReplayPackage.ReplayPackageID)
	}
}

// TestFromExecutionResultLeavesIntentIDEmpty is the honesty property:
// IntentID must never be silently filled with a fabricated value (e.g.
// reusing ExecutionID) just to look complete.
func TestFromExecutionResultLeavesIntentIDEmpty(t *testing.T) {
	key := FromExecutionResult(realExecutionResult(t))
	if key.IntentID != "" {
		t.Fatalf("expected IntentID to be left empty (no real per-execution intent exists in the wired DAG today), got %q", key.IntentID)
	}
}

// TestDecisionIDIsIndependentOfExecutionID is P0-7's positive property,
// replacing the pre-P0-7 canary (TestDecisionIDCurrentlyAliasesExecutionID)
// that this test's own doc comment predicted would need updating the
// day pkg/execution grew a real independent DecisionID: it now does
// (execution.go's decisionID function), so DecisionID must differ from
// ExecutionID and must be a genuine hash, not a copy.
func TestDecisionIDIsIndependentOfExecutionID(t *testing.T) {
	res := realExecutionResult(t)
	key := FromExecutionResult(res)
	if key.DecisionID == "" {
		t.Fatal("expected a non-empty DecisionID")
	}
	if key.DecisionID == key.ExecutionID {
		t.Fatalf("expected DecisionID to be independently derived, got it equal to ExecutionID %q", key.ExecutionID)
	}
}

// TestDecisionIDIsDeterministicAndContentAddressed proves the
// independence claim is real, not merely "different by coincidence":
// two runs with byte-identical inputs produce the SAME DecisionID
// (deterministic), and a run whose decision content genuinely differs
// (a different risk-driving PatternScore, which changes canon.Decision.
// RiskScore and can change canon.Decision.Action) produces a DIFFERENT
// one.
func TestDecisionIDIsDeterministicAndContentAddressed(t *testing.T) {
	e1 := execution.NewEngine(nil)
	res1, err := e1.Run(context.Background(), execution.Input{Context: testContext(), Case: testCaseInput()})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	e2 := execution.NewEngine(nil)
	res2, err := e2.Run(context.Background(), execution.Input{Context: testContext(), Case: testCaseInput()})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	k1, k2 := FromExecutionResult(*res1), FromExecutionResult(*res2)
	if k1.DecisionID != k2.DecisionID {
		t.Fatalf("expected identical inputs to produce the same DecisionID, got %q vs %q", k1.DecisionID, k2.DecisionID)
	}

	perturbedCase := testCaseInput()
	perturbedCase.PatternScore = 0.99
	e3 := execution.NewEngine(nil)
	res3, err := e3.Run(context.Background(), execution.Input{Context: testContext(), Case: perturbedCase})
	if err != nil {
		t.Fatalf("run 3: %v", err)
	}
	k3 := FromExecutionResult(*res3)
	if k3.DecisionID == k1.DecisionID {
		t.Fatal("expected a genuinely different case (different risk-driving PatternScore) to change DecisionID")
	}
}

// TestWithIdentityLedgerHeadCarriesTheSameValueBoundIntoTheDAGStage is
// the property that matters: the ledger head a caller attaches via
// WithIdentityLedgerHead must be the EXACT value pkg/execution's own
// IDENTITY_RESOLUTION stage already committed into its node hash (see
// execution.Engine.Identity) -- not a coincidentally similar one. This
// proves the two P0-D/P0-F additions are genuinely the same real
// commitment viewed from two places, not two independent claims that
// happen to agree today.
func TestWithIdentityLedgerHeadCarriesTheSameValueBoundIntoTheDAGStage(t *testing.T) {
	resolver := identity.NewResolver()
	if err := resolver.RegisterAuthority(identity.Authority{SourceID: "test-authority", Weight: 1}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	if _, err := resolver.Merge("analyst-1", "test-authority",
		identity.Identifier{Kind: identity.KindIMO, Value: "9998887"},
		identity.Identifier{Kind: identity.KindCallsign, Value: "ABCD1"}, 1, "test merge"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	wantHead := resolver.Head()
	if wantHead == "" {
		t.Fatal("expected a real, non-empty ledger head after a real merge")
	}

	e := execution.NewEngine(nil)
	e.Identity = resolver
	res, err := e.Run(context.Background(), execution.Input{Context: testContext(), Case: testCaseInput()})
	if err != nil {
		t.Fatalf("execution.Run: %v", err)
	}

	key := FromExecutionResult(*res).WithIdentityLedgerHead(resolver.Head())
	if key.EntityIdentityLedgerHead != wantHead {
		t.Fatalf("expected EntityIdentityLedgerHead to equal the resolver's own Head(): got %q want %q",
			key.EntityIdentityLedgerHead, wantHead)
	}

	var idNode execution.Node
	found := false
	for _, n := range res.Trace.Nodes {
		if n.StageID == execution.StageIdentityResolution {
			idNode, found = n, true
			break
		}
	}
	if !found {
		t.Fatal("expected an IDENTITY_RESOLUTION node in the trace")
	}
	// Re-running with a DIFFERENT (but still real) ledger state must
	// change the stage hash -- confirming the value key now carries is
	// genuinely load-bearing in the DAG, not a decorative duplicate.
	otherResolver := identity.NewResolver()
	if err := otherResolver.RegisterAuthority(identity.Authority{SourceID: "test-authority", Weight: 1}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	e2 := execution.NewEngine(nil)
	e2.Identity = otherResolver
	res2, err := e2.Run(context.Background(), execution.Input{Context: testContext(), Case: testCaseInput()})
	if err != nil {
		t.Fatalf("execution.Run (2nd): %v", err)
	}
	var idNode2 execution.Node
	for _, n := range res2.Trace.Nodes {
		if n.StageID == execution.StageIdentityResolution {
			idNode2 = n
			break
		}
	}
	if idNode.Hash == idNode2.Hash {
		t.Fatal("a genuinely different identity ledger state must change the IDENTITY_RESOLUTION node hash")
	}
}

// TestFromExecutionResultHandlesNilCanonical proves the nil-Canonical
// defensive check is real, not dead code: a zero-value Result must not
// panic.
func TestFromExecutionResultHandlesNilCanonical(t *testing.T) {
	key := FromExecutionResult(execution.Result{})
	if key.EntityID != "" {
		t.Fatalf("expected EntityID empty for a Result with nil Canonical, got %q", key.EntityID)
	}
}

// TestDifferentExecutionsProduceDifferentExecutionIDs is a basic
// non-collision sanity check.
func TestDifferentExecutionsProduceDifferentExecutionIDs(t *testing.T) {
	ctx1 := testContext()
	ctx2 := testContext()
	ctx2.ExecutionID = "exec-correlation-2"
	ctx2.EvidencePackageID = "evp-correlation-2"

	// Independent engines: a real Engine's fusion ledger rejects
	// resubmitting the exact same (source, subject, predicate, value,
	// tick) evidence twice, so two distinct runs of otherwise-identical
	// input need distinct engine instances -- the same reasoning
	// pkg/execution's own determinism tests use two engines for.
	res1, err := execution.NewEngine(nil).Run(context.Background(), execution.Input{Context: ctx1, Case: testCaseInput()})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	res2, err := execution.NewEngine(nil).Run(context.Background(), execution.Input{Context: ctx2, Case: testCaseInput()})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	k1, k2 := FromExecutionResult(*res1), FromExecutionResult(*res2)
	if k1.ExecutionID == k2.ExecutionID {
		t.Fatal("expected distinct ExecutionIDs for two distinct executions")
	}
	if k1.ReplayPackageID == k2.ReplayPackageID {
		t.Fatal("expected distinct ReplayPackageIDs for two distinct executions")
	}
}
