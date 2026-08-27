package soak

import (
	"testing"

	"veriqo/pkg/blockers"
)

func goodContract() *blockers.Contract {
	c := &blockers.Contract{
		ID: "soak_72h", Name: "72-hour continuous soak test", Version: "v1",
		RequiredCapabilities:      []string{"soak_harness", "leak_detection", "signed_checkpoint_chain"},
		RealWorldDependencies:     []string{"72_continuous_wall_clock_hours_on_a_long_lived_host"},
		TestProfile:               blockers.TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements:      []string{"checkpoint_chain", "goroutine_count_series"},
		FailureConditions:         []string{"goroutine_growth", "memory_growth"},
		AcceptanceCriteria:        []string{"duration >= 259200s"},
		RealQualificationRequired: true,
	}
	c.MarkImplemented()
	c.MarkIntegrationTested()
	return c
}

func TestRunQualificationShortFixtureRunPasses(t *testing.T) {
	c := goodContract()
	result, report, err := RunQualification(c, 0.02, func(i int) error { return nil })
	if err != nil {
		t.Fatalf("RunQualification: %v (measurements=%v)", err, result.Measurements)
	}
	if !result.Pass {
		t.Fatalf("expected pass, got failure: %s", result.FailureReason)
	}
	if report.IsFullSoak {
		t.Fatal("a short fixture run must never claim IsFullSoak")
	}
	if !report.ChainVerified {
		t.Fatal("expected the checkpoint chain to verify")
	}
	if c.Status != blockers.StatusQualificationTested {
		t.Fatalf("expected QUALIFICATION_TESTED, got %s", c.Status)
	}
}

func TestRunQualificationFailsOnWorkloadError(t *testing.T) {
	c := goodContract()
	calls := 0
	result, _, err := RunQualification(c, 0.02, func(i int) error {
		calls++
		if calls == 2 {
			return errWorkload
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected RunQualification to return an error when a fixture run fails")
	}
	if result.Pass {
		t.Fatal("expected Pass=false when a workload iteration errors")
	}
	if c.Status == blockers.StatusQualificationTested {
		t.Fatal("status must not advance on a failed run")
	}
}

func TestVerifyChainDetectsTampering(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()
	h.Checkpoint()
	h.Checkpoint()
	h.Checkpoint()

	valid := append([]Checkpoint(nil), h.checkpoints...)
	if err := VerifyChain(valid); err != nil {
		t.Fatalf("expected a genuine chain to verify, got %v", err)
	}

	tampered := append([]Checkpoint(nil), h.checkpoints...)
	tampered[1].Iterations = 99999 // mutate a field without recomputing the hash
	if err := VerifyChain(tampered); err == nil {
		t.Fatal("expected VerifyChain to detect a tampered checkpoint")
	}
}

func TestDetectLeakFlagsGoroutineGrowth(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()

	stop := make(chan struct{})
	leaked := make(chan struct{}, 60)
	for i := 0; i < 60; i++ {
		go func() {
			leaked <- struct{}{}
			<-stop
		}()
	}
	for i := 0; i < 60; i++ {
		<-leaked
	}
	defer close(stop)

	verdict := h.DetectLeak(50)
	if !verdict.Leaked {
		t.Fatalf("expected a leak to be flagged: baseline=%d final=%d", verdict.BaselineGoroutines, verdict.FinalGoroutines)
	}
}

func TestDetectDriftFlagsHeapGrowth(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()
	h.samples = []Sample{
		{AtSeconds: 0, HeapAllocMB: 10},
		{AtSeconds: 1, HeapAllocMB: 10},
		{AtSeconds: 2, HeapAllocMB: 40},
		{AtSeconds: 3, HeapAllocMB: 45},
	}
	verdict := h.DetectDrift(0.5)
	if !verdict.Drifted {
		t.Fatalf("expected drift to be flagged, got %+v", verdict)
	}
}

func TestDetectDriftDoesNotFlagStableHeap(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()
	h.samples = []Sample{
		{AtSeconds: 0, HeapAllocMB: 10},
		{AtSeconds: 1, HeapAllocMB: 11},
		{AtSeconds: 2, HeapAllocMB: 10},
		{AtSeconds: 3, HeapAllocMB: 11},
	}
	verdict := h.DetectDrift(0.5)
	if verdict.Drifted {
		t.Fatalf("expected no drift on a stable heap series, got %+v", verdict)
	}
}

func TestCheckpointChainLinksSequentially(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()
	first := h.Checkpoint()
	second := h.Checkpoint()
	if first.PrevHash != "" {
		t.Fatalf("expected the first checkpoint's PrevHash to be empty, got %q", first.PrevHash)
	}
	if second.PrevHash != first.Hash {
		t.Fatalf("expected the second checkpoint to chain from the first")
	}
	if first.Hash == second.Hash {
		t.Fatal("expected distinct checkpoints to hash differently")
	}
}

var errWorkload = &workloadError{"injected fixture failure"}

type workloadError struct{ msg string }

func (e *workloadError) Error() string { return e.msg }
