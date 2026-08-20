package scale

import (
	"context"
	"testing"

	"veriqo/pkg/blockers"
	"veriqo/pkg/blockers/livedata"
)

// TestGenerateWorkloadIsDeterministic proves GenerateWorkload's core
// claim: the same (seed, count) always produces byte-identical
// envelopes — required for "REAL_DERIVED_BENCHMARK" to mean anything
// (a benchmark that isn't reproducible from its own seed isn't a
// benchmark).
func TestGenerateWorkloadIsDeterministic(t *testing.T) {
	a := GenerateWorkload(42, 500)
	b := GenerateWorkload(42, 500)
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Envelope.ID != b[i].Envelope.ID || a[i].Envelope.Hash != b[i].Envelope.Hash {
			t.Fatalf("envelope %d diverged: %+v vs %+v", i, a[i], b[i])
		}
	}
	c := GenerateWorkload(43, 500)
	identical := true
	for i := range a {
		if a[i].Envelope.Hash != c[i].Envelope.Hash {
			identical = false
			break
		}
	}
	if identical {
		t.Fatal("different seeds must not produce identical envelope hashes")
	}
}

// TestGeneratedWorkloadIsNeverIndependentObservation is external audit
// item A7's own explicit instruction, checked directly: generated
// envelopes must always be REAL_DERIVED_BENCHMARK and must never be
// classifiable as an independent real-world observation.
func TestGeneratedWorkloadIsNeverIndependentObservation(t *testing.T) {
	for _, env := range GenerateWorkload(1, 100) {
		if env.DataOrigin != livedata.ModeRealDerivedBenchmark {
			t.Fatalf("expected DataOrigin=REAL_DERIVED_BENCHMARK, got %s", env.DataOrigin)
		}
		if livedata.IsIndependentRealObservation(env.DataOrigin) {
			t.Fatalf("generated envelope must never be classified as an independent real-world observation")
		}
	}
}

// TestRunLargeScaleQualificationCleanRun exercises the full
// provision->generate->submit->collect->reconcile pipeline at a modest,
// fast-running scale (proving the pipeline correctness this session CAN
// prove) — see this file's own top-level doc comment and
// docs/AUDIT_A1_A18_GAP_MAPPING.md's A7 entry for why 100 nodes / 1M
// envelopes was not attempted here.
func TestRunLargeScaleQualificationCleanRun(t *testing.T) {
	ctx := context.Background()
	c := &blockers.Contract{
		ID: "scale_qualification", Name: "100-node / 1M-envelope scale qualification", Version: "v1",
		RequiredCapabilities: []string{"node_provisioning", "workload_distribution", "evidence_integrity_check"},
		RealWorldDependencies: []string{"100_real_nodes_or_cloud_equivalent"},
		TestProfile:          blockers.TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements: []string{"reconciliation_report", "node_identity_manifest"},
		FailureConditions:    []string{"lost_evidence", "duplicate_identity_count"},
		AcceptanceCriteria:   []string{"reconciliation_pass"},
		RealQualificationRequired: true,
	}
	c.MarkImplemented()
	c.MarkIntegrationTested()

	provider := NewSimulatedNodeProvider()
	result, report, err := RunLargeScaleQualification(ctx, c, provider,
		10, 5000, 7, "v7.12.1", "sourcehash-test", "envhash-test", 1)
	if err != nil {
		t.Fatalf("RunLargeScaleQualification: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected pass, got failure_reason=%q recon=%+v", result.FailureReason, report.Reconciliation)
	}
	if len(report.NodeIdentities) != 10 {
		t.Fatalf("expected 10 node identities, got %d", len(report.NodeIdentities))
	}
	for _, ni := range report.NodeIdentities {
		if ni.NodeID == "" || ni.ReleaseVersion != "v7.12.1" || ni.SourceHash != "sourcehash-test" {
			t.Fatalf("incomplete node identity: %+v", ni)
		}
	}
	if report.Reconciliation.InputCount != 5000 || report.Reconciliation.AcceptedCount != 5000 {
		t.Fatalf("expected all 5000 envelopes accounted for, got %+v", report.Reconciliation)
	}
	if report.DataOrigin != livedata.ModeRealDerivedBenchmark {
		t.Fatalf("expected DataOrigin=REAL_DERIVED_BENCHMARK on the report, got %s", report.DataOrigin)
	}
	t.Logf("throughput: %.0f envelopes/sec, final_root=%s", report.ThroughputPerSec, report.Reconciliation.FinalRoot)
}

// TestRunLargeScaleQualificationRefusesRealMode matches
// RunQualification's existing discipline: this path never records a
// REAL-mode result.
func TestRunLargeScaleQualificationRefusesRealMode(t *testing.T) {
	provider := &fakeRealProvider{}
	_, _, err := RunLargeScaleQualification(context.Background(), &blockers.Contract{ID: "scale_qualification"}, provider, 1, 1, 1, "v1", "sh", "eh", 0)
	if err == nil {
		t.Fatal("expected an error refusing a REAL-mode provider")
	}
}

type fakeRealProvider struct{}

func (f *fakeRealProvider) Mode() string { return "REAL" }
func (f *fakeRealProvider) Provision(ctx context.Context, n int) ([]Node, error)        { return nil, nil }
func (f *fakeRealProvider) Submit(ctx context.Context, node Node, rec EvidenceRecord) error { return nil }
func (f *fakeRealProvider) Collect(ctx context.Context) ([]ProcessedRecord, error)       { return nil, nil }
func (f *fakeRealProvider) Destroy(ctx context.Context, nodes []Node) error              { return nil }
