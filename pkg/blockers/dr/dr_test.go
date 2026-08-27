package dr

import (
	"context"
	"testing"
	"time"

	"veriqo/pkg/blockers"
)

func goodContract() *blockers.Contract {
	c := &blockers.Contract{
		ID: "multi_region_dr", Name: "Multi-region disaster recovery qualification", Version: "v1",
		RequiredCapabilities:      []string{"region_provisioning", "partition_injection"},
		RealWorldDependencies:     []string{"real_multi_region_infrastructure"},
		TestProfile:               blockers.TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements:      []string{"rpo_measurement", "rto_measurement"},
		FailureConditions:         []string{"rpo_exceeds_target", "rto_exceeds_target"},
		AcceptanceCriteria:        []string{"no_data_loss"},
		RealQualificationRequired: true,
	}
	c.MarkImplemented()
	c.MarkIntegrationTested()
	return c
}

func TestDRQualificationFailoverNoDataLoss(t *testing.T) {
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
	if result.Measurements["rpo_lost_writes"] != "0" {
		t.Fatalf("expected zero RPO loss for an acknowledged write, got %s", result.Measurements["rpo_lost_writes"])
	}
	if result.Measurements["healed_convergence"] != "true" {
		t.Fatal("expected the healed region to converge with the surviving majority")
	}
	if c.Status != blockers.StatusQualificationTested {
		t.Fatalf("expected QUALIFICATION_TESTED, got %s", c.Status)
	}
}

func TestRunQualificationRejectsRealModeProvider(t *testing.T) {
	ctx := context.Background()
	c := goodContract()
	_, err := RunQualification(ctx, c, &fakeRealProvider{}, 3)
	if err == nil {
		t.Fatal("RunQualification must refuse a REAL-mode provider")
	}
}

func TestCreateRegionsRejectsBelowThreeRegions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p := NewLocalRegionProvider()
	if _, err := p.CreateRegions(ctx, 2); err == nil {
		t.Fatal("expected CreateRegions to refuse fewer than 3 regions -- no quorum survives one failure")
	}
}

// fakeRealProvider exists only to prove RunQualification's REAL-mode
// refusal actually short-circuits before touching the provider.
type fakeRealProvider struct{}

func (f *fakeRealProvider) Mode() string { return "REAL" }
func (f *fakeRealProvider) CreateRegions(ctx context.Context, n int) ([]Region, error) {
	panic("must not be called")
}
func (f *fakeRealProvider) CurrentLeader(ctx context.Context) (Region, error) {
	panic("must not be called")
}
func (f *fakeRealProvider) Write(ctx context.Context, key, value string) error {
	panic("must not be called")
}
func (f *fakeRealProvider) Read(regionID, key string) (string, bool) { panic("must not be called") }
func (f *fakeRealProvider) Partition(regionID string) error          { panic("must not be called") }
func (f *fakeRealProvider) Heal(regionID string) error               { panic("must not be called") }
func (f *fakeRealProvider) Destroy(ctx context.Context) error        { panic("must not be called") }
