package scale

import (
	"context"
	"testing"
	"time"

	"veriqo/pkg/blockers"
)

func goodContract() *blockers.Contract {
	c := &blockers.Contract{
		ID: "scale_qualification", Name: "100-node / 1M-evidence qualification", Version: "v1",
		RequiredCapabilities:      []string{"node_provisioning", "workload_distribution"},
		RealWorldDependencies:     []string{"100_real_nodes"},
		TestProfile:               blockers.TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements:      []string{"throughput", "latency", "convergence"},
		FailureConditions:         []string{"lost_evidence", "duplicate_identity"},
		AcceptanceCriteria:        []string{"lost_evidence == 0"},
		RealQualificationRequired: true,
	}
	c.MarkImplemented()
	c.MarkIntegrationTested()
	return c
}

func TestSimulatedProviderNoLossNoDuplication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := goodContract()
	provider := NewSimulatedNodeProvider()
	result, err := RunQualification(ctx, c, provider, 8, 5000)
	if err != nil {
		t.Fatalf("RunQualification: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected pass, got failure: %s", result.FailureReason)
	}
	if c.Status != blockers.StatusQualificationTested {
		t.Fatalf("expected QUALIFICATION_TESTED, got %s", c.Status)
	}
	if result.Measurements["records_processed"] != "5000" {
		t.Fatalf("expected 5000 processed, got %s", result.Measurements["records_processed"])
	}
}

func TestRunQualificationRejectsRealModeProvider(t *testing.T) {
	ctx := context.Background()
	c := goodContract()
	_, err := RunQualification(ctx, c, &fakeProvider{mode: "REAL"}, 4, 10)
	if err == nil {
		t.Fatal("RunQualification must refuse a REAL-mode provider")
	}
}

func TestIntegrityDetectsLostRecords(t *testing.T) {
	ctx := context.Background()
	c := goodContract()
	fp := &fakeProvider{mode: "SIMULATED", dropIDs: map[string]bool{"ev-00000003": true, "ev-00000007": true}}
	result, err := RunQualification(ctx, c, fp, 4, 10)
	if err == nil {
		t.Fatal("expected RunQualification to return an error for a failed fixture run")
	}
	if result.Pass {
		t.Fatal("expected Pass=false when records are lost")
	}
	if c.Status == blockers.StatusQualificationTested {
		t.Fatal("status must not advance when the integrity check finds loss")
	}
}

func TestIntegrityDetectsDuplicatedRecords(t *testing.T) {
	ctx := context.Background()
	c := goodContract()
	fp := &fakeProvider{mode: "SIMULATED", dupIDs: map[string]bool{"ev-00000002": true}}
	result, err := RunQualification(ctx, c, fp, 4, 10)
	if err == nil {
		t.Fatal("expected RunQualification to return an error for a failed fixture run")
	}
	if result.Pass {
		t.Fatal("expected Pass=false when a record is processed more than once")
	}
}

// fakeProvider is a directly controllable NodeProvider for exercising
// checkIntegrity's loss/duplication detection deterministically, rather
// than relying on real goroutine scheduling to reproduce a failure.
type fakeProvider struct {
	mode      string
	dropIDs   map[string]bool
	dupIDs    map[string]bool
	submitted []string
}

func (f *fakeProvider) Mode() string { return f.mode }

func (f *fakeProvider) Provision(ctx context.Context, n int) ([]Node, error) {
	nodes := make([]Node, n)
	for i := range nodes {
		nodes[i] = Node{ID: "fake-node"}
	}
	return nodes, nil
}

func (f *fakeProvider) Submit(ctx context.Context, node Node, rec EvidenceRecord) error {
	f.submitted = append(f.submitted, rec.ID)
	return nil
}

func (f *fakeProvider) Collect(ctx context.Context) ([]ProcessedRecord, error) {
	var out []ProcessedRecord
	for _, id := range f.submitted {
		if f.dropIDs[id] {
			continue
		}
		out = append(out, ProcessedRecord{NodeID: "fake-node", RecordID: id, ReceivedAt: time.Now()})
		if f.dupIDs[id] {
			out = append(out, ProcessedRecord{NodeID: "fake-node", RecordID: id, ReceivedAt: time.Now()})
		}
	}
	return out, nil
}

func (f *fakeProvider) Destroy(ctx context.Context, nodes []Node) error { return nil }
