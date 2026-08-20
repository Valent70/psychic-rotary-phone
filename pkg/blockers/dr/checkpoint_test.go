package dr

import (
	"context"
	"testing"
	"time"

	"veriqo/pkg/governance/reconciliation"
)

// TestCheckpointChainVerifiesAcrossFullDRScenario runs the full DR
// qualification scenario end-to-end and confirms RunQualification took
// a real, hash-verifiable checkpoint at every named point (baseline,
// after-failover-write, post-heal-convergence) -- not just a single
// before/after RPO number.
func TestCheckpointChainVerifiesAcrossFullDRScenario(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c := goodContract()
	provider := NewLocalRegionProvider()
	result, err := RunQualification(ctx, c, provider, 3)
	if err != nil {
		t.Fatalf("RunQualification: %v (result=%+v)", err, result)
	}
	if !result.Pass {
		t.Fatalf("expected pass, got failure: %s (measurements=%v)", result.FailureReason, result.Measurements)
	}
	if result.Measurements["checkpoint_chain_verified"] != "true" {
		t.Fatalf("expected checkpoint_chain_verified=true, got %s", result.Measurements["checkpoint_chain_verified"])
	}
	if result.Measurements["checkpoint_chain_length"] != "3" {
		t.Fatalf("expected 3 checkpoints per region (baseline, after-failover-write, post-heal-convergence), got %s",
			result.Measurements["checkpoint_chain_length"])
	}
	if result.Measurements["reconciliation_pass"] != "true" {
		t.Fatalf("expected reconciliation_pass=true, got %s", result.Measurements["reconciliation_pass"])
	}
	if result.Measurements["reconciliation_final_root"] == "" {
		t.Fatal("expected a non-empty reconciliation_final_root")
	}
}

// TestCheckpointChainDetectsTamper proves VerifyChain is a real
// tamper-detection check, not a decoration: corrupting one stored field
// on an already-appended checkpoint must make VerifyChain fail, exactly
// like fusion.Engine.VerifyChain and kg.Graph.VerifyChain.
func TestCheckpointChainDetectsTamper(t *testing.T) {
	chain := newCheckpointChain("region-0")
	chain.Append("hash-a", "baseline")
	chain.Append("hash-b", "after-failover-write")
	chain.Append("hash-c", "post-heal-convergence")

	if err := chain.VerifyChain(); err != nil {
		t.Fatalf("expected an untampered chain to verify cleanly, got %v", err)
	}

	// Corrupt the middle entry's StateHash directly -- same package, so
	// this reaches into the chain's own unexported storage the way a
	// real tamper (e.g. disk corruption, a malicious rewrite) would.
	chain.mu.Lock()
	chain.entries[1].StateHash = "tampered-hash"
	chain.mu.Unlock()

	if err := chain.VerifyChain(); err != ErrCheckpointTampered {
		t.Fatalf("expected ErrCheckpointTampered after tampering, got %v", err)
	}
}

// TestCheckpointChainDetectsBrokenLinkage proves the PrevHash linkage
// itself is checked, not just each record's own hash.
func TestCheckpointChainDetectsBrokenLinkage(t *testing.T) {
	chain := newCheckpointChain("region-0")
	chain.Append("hash-a", "baseline")
	chain.Append("hash-b", "after-failover-write")

	chain.mu.Lock()
	chain.entries[1].PrevHash = "not-the-real-prev-hash"
	chain.mu.Unlock()

	if err := chain.VerifyChain(); err != ErrCheckpointTampered {
		t.Fatalf("expected ErrCheckpointTampered after breaking PrevHash linkage, got %v", err)
	}
}

// TestReconciliationAccountsForBothDRWrites exercises drWriteEnvelope +
// reconciliation.Engine directly: both the before-failure and
// after-failure writes must be individually accounted for (accepted),
// and Finalize's invariants must all hold when both survived correctly.
func TestReconciliationAccountsForBothDRWrites(t *testing.T) {
	engine := reconciliation.NewEngine()
	accepted1, reason1 := engine.Process(drWriteEnvelope("k1", "before-failure", "before-failure", true, 0))
	accepted2, reason2 := engine.Process(drWriteEnvelope("k2", "after-failure", "after-failure", true, 1))
	if !accepted1 || reason1 != "" {
		t.Fatalf("expected k1 accepted, got accepted=%v reason=%s", accepted1, reason1)
	}
	if !accepted2 || reason2 != "" {
		t.Fatalf("expected k2 accepted, got accepted=%v reason=%s", accepted2, reason2)
	}

	report := engine.Finalize(2)
	if !report.Pass() {
		t.Fatalf("expected all acceptance invariants to hold, violations=%v report=%+v", report.Verify(), report)
	}
	if report.InputCount != 2 || report.AcceptedCount != 2 || report.RejectedCount != 0 {
		t.Fatalf("unexpected counters: %+v", report)
	}
}

// TestReconciliationCatchesLostOrCorruptedWrite proves the reconciliation
// pass is strictly stronger than the old bare string comparison: a write
// that was acknowledged but reads back as something else (or not found
// at all) is rejected as a hash mismatch, not silently accepted.
func TestReconciliationCatchesLostOrCorruptedWrite(t *testing.T) {
	engine := reconciliation.NewEngine()
	// k1 committed "before-failure" but the region actually holds a
	// different value after recovery -- must be rejected.
	accepted, reason := engine.Process(drWriteEnvelope("k1", "before-failure", "SOMETHING-ELSE", true, 0))
	if accepted || reason != reconciliation.ReasonHashMismatch {
		t.Fatalf("expected a hash-mismatch rejection for a corrupted write, got accepted=%v reason=%s", accepted, reason)
	}

	// k2 committed "after-failure" but was never found on read-back
	// (found=false) -- must also be rejected, not silently treated as a
	// pass because the empty string happens to be returned.
	accepted2, reason2 := engine.Process(drWriteEnvelope("k2", "after-failure", "", false, 1))
	if accepted2 || reason2 != reconciliation.ReasonHashMismatch {
		t.Fatalf("expected a hash-mismatch rejection for a lost write, got accepted=%v reason=%s", accepted2, reason2)
	}

	report := engine.Finalize(2)
	if report.Pass() {
		t.Fatalf("expected acceptance invariants to fail for two corrupted/lost writes, got a clean report: %+v", report)
	}
	if report.HashMismatchCount != 2 {
		t.Fatalf("expected 2 hash mismatches, got %d", report.HashMismatchCount)
	}
}

// interfaceAssertion is a compile-time check that LocalRegionProvider
// satisfies Checkpointer -- if this ever stops compiling, RunQualification's
// checkpointer, hasCheckpointer := provider.(Checkpointer) type
// assertion in dr.go would silently stop finding a real implementation.
var _ Checkpointer = (*LocalRegionProvider)(nil)
var _ RegionProvider = (*LocalRegionProvider)(nil)
