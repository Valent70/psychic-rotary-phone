package blockers

import (
	"encoding/json"
	"os"
	"testing"
)

func goodContract() *Contract {
	return &Contract{
		ID: "scale_qualification", Name: "100-node / 1M-evidence qualification", Version: "v1",
		RequiredCapabilities:      []string{"node_provisioning", "workload_distribution"},
		RealWorldDependencies:     []string{"100_real_nodes"},
		TestProfile:               TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements:      []string{"throughput", "latency", "convergence"},
		FailureConditions:         []string{"lost_evidence", "duplicate_identity"},
		AcceptanceCriteria:        []string{"lost_evidence == 0"},
		RealQualificationRequired: true,
	}
}

func TestContractValidate(t *testing.T) {
	c := goodContract()
	if err := c.Validate(); err != nil {
		t.Fatalf("a well-formed contract must validate: %v", err)
	}
}

func TestContractRejectsNoRealWorldDependency(t *testing.T) {
	c := goodContract()
	c.RealWorldDependencies = nil
	if err := c.Validate(); err == nil {
		t.Fatal("a blocker contract with no real-world dependency is not one of the eight blockers -- must be refused")
	}
}

func TestContractRejectsSelfExemption(t *testing.T) {
	c := goodContract()
	c.RealQualificationRequired = false
	if err := c.Validate(); err == nil {
		t.Fatal("a contract must not be able to exempt itself from real qualification")
	}
	c2 := goodContract()
	c2.TestProfile.ProductionRequiredForVerification = false
	if err := c2.Validate(); err == nil {
		t.Fatal("a contract must not be able to disable the production-required-for-verification rule")
	}
}

func TestFixtureRunNeverExceedsReadyForRealQualification(t *testing.T) {
	c := goodContract()
	c.MarkImplemented()
	c.MarkIntegrationTested()
	if err := c.RecordFixtureRun(RunResult{BlockerID: c.ID, Mode: "SIMULATED", Pass: true, ProducedAtTick: 100}); err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusQualificationTested {
		t.Fatalf("expected QUALIFICATION_TESTED, got %s", c.Status)
	}
	if err := c.PromoteToReadyForRealQualification(); err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusReadyForRealQualification {
		t.Fatalf("expected READY_FOR_REAL_QUALIFICATION, got %s", c.Status)
	}
	// The ceiling: nothing in this package can push it further.
	if statusRank[c.Status] >= statusRank[StatusVerified] {
		t.Fatal("a fixture-sourced contract must never reach VERIFIED")
	}
}

func TestRecordFixtureRunRejectsRealMode(t *testing.T) {
	c := goodContract()
	if err := c.RecordFixtureRun(RunResult{BlockerID: c.ID, Mode: "REAL", Pass: true}); err == nil {
		t.Fatal("RecordFixtureRun must refuse a result claiming Mode=REAL -- that path belongs to pkg/governance/qualification")
	}
}

func TestRecordFixtureRunPropagatesFailure(t *testing.T) {
	c := goodContract()
	err := c.RecordFixtureRun(RunResult{BlockerID: c.ID, Mode: "FIXTURE", Pass: false, FailureReason: "lost 3 evidence records"})
	if err == nil {
		t.Fatal("a failed fixture run must not silently advance status")
	}
	if c.Status == StatusQualificationTested {
		t.Fatal("status must not advance on a failed run")
	}
}

func TestPromoteRequiresQualificationTestedFirst(t *testing.T) {
	c := goodContract()
	c.MarkImplemented()
	if err := c.PromoteToReadyForRealQualification(); err == nil {
		t.Fatal("cannot promote to READY_FOR_REAL_QUALIFICATION without a passing qualification run first")
	}
}

func TestLoadRegistryFromRealProductionBlockersFile(t *testing.T) {
	data, err := os.ReadFile("../../docs/governance/production-blockers.json")
	if err != nil {
		t.Fatalf("read docs/governance/production-blockers.json: %v", err)
	}
	reg, err := LoadRegistry(data)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	for _, id := range reg.IDs() {
		c := reg.Get(id)
		if c == nil {
			t.Fatalf("registry missing contract %q", id)
		}
		if c.ID != id {
			t.Fatalf("contract keyed as %q has ID %q", id, c.ID)
		}
	}
	if len(reg.IDs()) != 8 {
		t.Fatalf("expected exactly 8 blockers, got %d", len(reg.IDs()))
	}
}

func TestLoadRegistryRejectsMissingBlocker(t *testing.T) {
	f := registryFile{Version: "test", Blockers: []Contract{*goodContract()}}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(data); err == nil {
		t.Fatal("a registry with only one of the eight blockers must be rejected")
	}
}

func TestLoadRegistryRejectsInvalidEntry(t *testing.T) {
	bad := goodContract()
	bad.RealQualificationRequired = false
	f := registryFile{Version: "test", Blockers: []Contract{*bad}}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(data); err == nil {
		t.Fatal("a registry containing a self-exempting contract must be rejected")
	}
}

func TestStatusNeverRegresses(t *testing.T) {
	c := goodContract()
	c.MarkIntegrationTested()
	c.MarkImplemented() // lower status; must not regress
	if c.Status != StatusIntegrationTested {
		t.Fatalf("status must be monotonic, got %s", c.Status)
	}
}
