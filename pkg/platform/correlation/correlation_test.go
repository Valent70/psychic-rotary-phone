package correlation

import (
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
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
	res, err := e.Run(execution.Input{Context: testContext(), Case: testCaseInput()})
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

// TestDecisionIDCurrentlyAliasesExecutionID documents, via a real
// assertion rather than only a comment, the exact honesty caveat this
// package's doc comment makes: DecisionID is not yet an independently
// derived identifier. If pkg/execution ever changes this, this test
// will fail and force the doc comment to be corrected rather than
// silently going stale.
func TestDecisionIDCurrentlyAliasesExecutionID(t *testing.T) {
	res := realExecutionResult(t)
	key := FromExecutionResult(res)
	if key.DecisionID != key.ExecutionID {
		t.Fatalf("expected DecisionID to currently alias ExecutionID (got DecisionID=%q ExecutionID=%q) -- "+
			"if this now differs, pkg/execution has grown a real independent DecisionID and this package's "+
			"doc comment (and P0-D/P0-F's honest-gap tracking) should be updated to say so",
			key.DecisionID, key.ExecutionID)
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
	res1, err := execution.NewEngine(nil).Run(execution.Input{Context: ctx1, Case: testCaseInput()})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	res2, err := execution.NewEngine(nil).Run(execution.Input{Context: ctx2, Case: testCaseInput()})
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
