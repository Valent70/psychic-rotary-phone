package scale

import (
	"context"
	"testing"
	"time"
)

func TestMerkleRootChangesWithDataset(t *testing.T) {
	a := []ProcessedRecord{
		{NodeID: "n0", RecordID: "ev-1"},
		{NodeID: "n1", RecordID: "ev-2"},
	}
	b := []ProcessedRecord{
		{NodeID: "n0", RecordID: "ev-1"},
		{NodeID: "n1", RecordID: "ev-3"}, // one record differs
	}
	rootA := merkleRoot(a)
	rootB := merkleRoot(b)
	if rootA == "" || rootB == "" {
		t.Fatal("expected non-empty merkle roots for non-empty input")
	}
	if rootA == rootB {
		t.Fatal("expected the merkle root to change when the underlying dataset changes")
	}

	// Order independence: shuffling the input must not change the root
	// (the reconciled dataset's identity, not the collection order, is
	// what the root fingerprints).
	shuffled := []ProcessedRecord{a[1], a[0]}
	if merkleRoot(shuffled) != rootA {
		t.Fatal("expected the merkle root to be independent of input order")
	}
}

func TestMerkleRootHandlesOddCounts(t *testing.T) {
	for n := 1; n <= 7; n++ {
		recs := make([]ProcessedRecord, n)
		for i := range recs {
			recs[i] = ProcessedRecord{NodeID: "n0", RecordID: string(rune('a' + i))}
		}
		if merkleRoot(recs) == "" {
			t.Fatalf("expected a non-empty merkle root for %d records", n)
		}
	}
}

func TestMerkleRootEmptyForNoRecords(t *testing.T) {
	if got := merkleRoot(nil); got != "" {
		t.Fatalf("expected an empty root for zero records, got %q", got)
	}
}

func TestLedgerHashChangesWithOrder(t *testing.T) {
	h1 := ledgerHash([]string{"a", "b", "c"})
	h2 := ledgerHash([]string{"c", "b", "a"})
	if h1 == h2 {
		t.Fatal("expected the ledger hash to be order-sensitive (unlike merkleRoot)")
	}
	if h1 != ledgerHash([]string{"a", "b", "c"}) {
		t.Fatal("expected the ledger hash to be deterministic for the same input")
	}
}

func TestCheckAuthorizationRejectsUnregisteredNode(t *testing.T) {
	nodes := []Node{{ID: "n0"}, {ID: "n1"}}
	processed := []ProcessedRecord{
		{NodeID: "n0", RecordID: "ev-1"},
		{NodeID: "n1", RecordID: "ev-2"},
		{NodeID: "intruder", RecordID: "ev-3"}, // never provisioned
	}
	unauthorized := checkAuthorization(nodes, processed)
	if len(unauthorized) != 1 || unauthorized[0] != "intruder" {
		t.Fatalf("expected exactly [\"intruder\"] to be flagged, got %v", unauthorized)
	}
}

func TestCheckAuthorizationCleanForRegisteredNodesOnly(t *testing.T) {
	nodes := []Node{{ID: "n0"}, {ID: "n1"}}
	processed := []ProcessedRecord{
		{NodeID: "n0", RecordID: "ev-1"},
		{NodeID: "n1", RecordID: "ev-2"},
	}
	if got := checkAuthorization(nodes, processed); len(got) != 0 {
		t.Fatalf("expected no unauthorized nodes, got %v", got)
	}
}

func TestIntegrityReportPolicyIsExplicitReject(t *testing.T) {
	report := checkIntegrity([]string{"ev-1"}, []ProcessedRecord{
		{NodeID: "n0", RecordID: "ev-1"}, {NodeID: "n0", RecordID: "ev-1"},
	})
	if report.Policy != PolicyReject {
		t.Fatalf("expected the explicit duplicate policy to be PolicyReject, got %q", report.Policy)
	}
	if report.Clean() {
		t.Fatal("expected PolicyReject to fail Clean() when a duplicate is present")
	}
}

func TestRunQualificationDetectsUnauthorizedAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := goodContract()
	provider := &FaultInjectingNodeProvider{Inner: NewSimulatedNodeProvider(), Faults: Faults{InjectUnauthorized: true}}
	result, err := RunQualification(ctx, c, provider, 4, 50)
	if err == nil {
		t.Fatal("expected RunQualification to return an error for a failed fixture run")
	}
	if result.Pass {
		t.Fatal("expected Pass=false when an unauthorized node's submission is accepted")
	}
	if result.Measurements["unauthorized_count"] != "1" {
		t.Fatalf("expected unauthorized_count=1, got %s", result.Measurements["unauthorized_count"])
	}
}

func TestRunQualificationDetectsInjectedLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := goodContract()
	provider := &FaultInjectingNodeProvider{Inner: NewSimulatedNodeProvider(), Faults: Faults{DropEveryNth: 7}}
	result, err := RunQualification(ctx, c, provider, 4, 200)
	if err == nil {
		t.Fatal("expected RunQualification to return an error for a failed fixture run")
	}
	if result.Pass {
		t.Fatal("expected Pass=false when records are genuinely dropped")
	}
}

func TestRunQualificationDetectsInjectedDuplication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := goodContract()
	provider := &FaultInjectingNodeProvider{Inner: NewSimulatedNodeProvider(), Faults: Faults{DuplicateEveryNth: 11}}
	result, err := RunQualification(ctx, c, provider, 4, 200)
	if err == nil {
		t.Fatal("expected RunQualification to return an error for a failed fixture run")
	}
	if result.Pass {
		t.Fatal("expected Pass=false when records are genuinely duplicated")
	}
}

func TestRunQualificationCleanRunReportsFullEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := goodContract()
	provider := NewSimulatedNodeProvider()
	result, err := RunQualification(ctx, c, provider, 6, 1000)
	if err != nil {
		t.Fatalf("RunQualification: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected a clean run to pass: %s", result.FailureReason)
	}
	for _, key := range []string{"merkle_root", "input_ledger_hash", "output_ledger_hash", "duplicate_policy", "reconciliation_consistent"} {
		if result.Measurements[key] == "" {
			t.Fatalf("expected measurement %q to be populated, measurements=%v", key, result.Measurements)
		}
	}
	if result.Measurements["duplicate_policy"] != string(PolicyReject) {
		t.Fatalf("expected duplicate_policy=%q, got %q", PolicyReject, result.Measurements["duplicate_policy"])
	}
	if result.Measurements["unauthorized_count"] != "0" {
		t.Fatalf("expected unauthorized_count=0 for a clean run, got %s", result.Measurements["unauthorized_count"])
	}
	// Release identity resolution: either it resolved (non-empty commit
	// or source hash) or it honestly recorded why it could not, never
	// silently absent.
	if result.Measurements["release_commit"] == "" && result.Measurements["release_identity_error"] == "" {
		t.Fatal("expected either a resolved release identity or an explicit release_identity_error")
	}
}

func TestReconcileFlagsIdleNodes(t *testing.T) {
	nodes := []Node{{ID: "n0"}, {ID: "n1"}, {ID: "n2"}}
	submitted := []string{"ev-1", "ev-2"}
	processed := []ProcessedRecord{
		{NodeID: "n0", RecordID: "ev-1"},
		{NodeID: "n1", RecordID: "ev-2"},
	}
	report := checkIntegrity(submitted, processed)
	rec := reconcile(nodes, submitted, processed, report)
	if len(rec.NodesWithNoTraffic) != 1 || rec.NodesWithNoTraffic[0] != "n2" {
		t.Fatalf("expected n2 to be flagged idle, got %v", rec.NodesWithNoTraffic)
	}
	if !rec.Consistent {
		t.Fatalf("expected reconciliation to still be consistent (idle nodes are not a failure): %+v", rec)
	}
}

// TestScaleQualification100Nodes1MEnvelopes runs this package's own
// SimulatedNodeProvider at the mandate's literal acceptance scale --
// 100 distinct nodes, 1,000,000 envelopes -- and asserts zero loss,
// zero unauthorized acceptance, zero duplication, and a clean final
// reconciliation. This is explicitly SIMULATED: it proves the
// distribution and integrity-checking logic scales to this volume on
// one process's goroutines, NOT the real 100-node physical/cloud
// infrastructure acceptance the scale_qualification blocker ultimately
// requires -- see RunQualification's own doc comment for that
// distinction. Skipped under `go test -short`, matching this repo's
// existing convention (test/stress, test/integration) for tests that
// are real but too heavy for every routine run.
func TestScaleQualification100Nodes1MEnvelopes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100-node/1M-envelope simulated scale run in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c := goodContract()
	provider := NewSimulatedNodeProvider()
	result, err := RunQualification(ctx, c, provider, 100, 1_000_000)
	if err != nil {
		t.Fatalf("RunQualification: %v (measurements=%v)", err, result.Measurements)
	}
	if !result.Pass {
		t.Fatalf("expected pass at 100 nodes / 1,000,000 envelopes, got failure: %s (measurements=%v)", result.FailureReason, result.Measurements)
	}
	if result.Measurements["node_count"] != "100" {
		t.Fatalf("expected node_count=100, got %s", result.Measurements["node_count"])
	}
	if result.Measurements["records_processed"] != "1000000" {
		t.Fatalf("expected records_processed=1000000, got %s", result.Measurements["records_processed"])
	}
	if result.Measurements["unauthorized_count"] != "0" {
		t.Fatalf("expected zero unauthorized acceptance, got %s", result.Measurements["unauthorized_count"])
	}
	if result.Measurements["merkle_root"] == "" {
		t.Fatal("expected a non-empty merkle root over 1,000,000 reconciled records")
	}
	t.Logf("100-node/1M-envelope simulated run: elapsed=%s throughput=%s merkle_root=%s",
		result.Measurements["elapsed"], result.Measurements["throughput_per_s"], result.Measurements["merkle_root"])
}
