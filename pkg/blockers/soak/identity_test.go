package soak

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStartGeneratesImmutableRunID(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()
	id1 := h.Identity()
	if id1.RunID == "" {
		t.Fatal("expected Start to generate a non-empty run id")
	}
	h.Start() // calling Start again must not mint a new run id
	id2 := h.Identity()
	if id1.RunID != id2.RunID {
		t.Fatalf("expected run id to stay immutable across a second Start() call, got %q then %q", id1.RunID, id2.RunID)
	}
}

func TestDistinctHarnessesGetDistinctRunIDs(t *testing.T) {
	h1 := NewHarness(func(int) error { return nil })
	h2 := NewHarness(func(int) error { return nil })
	h1.Start()
	h2.Start()
	if h1.Identity().RunID == h2.Identity().RunID {
		t.Fatal("expected two independently-started harnesses to get different run ids")
	}
}

func TestStartPopulatesHostIdentity(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()
	id := h.Identity()
	if id.Host.Hostname == "" {
		t.Fatal("expected host identity to carry a non-empty hostname")
	}
	if id.HostIdentityHash == "" {
		t.Fatal("expected a non-empty host identity hash")
	}
	if id.StartUnix == 0 || id.StartTime == "" {
		t.Fatal("expected Start to record start time")
	}
}

func TestComputeSourceIdentityHashesRealFiles(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()
	if err := h.ComputeSourceIdentity("."); err != nil {
		t.Fatalf("ComputeSourceIdentity: %v", err)
	}
	id := h.Identity()
	if id.SourceHash == "" {
		t.Fatal("expected a non-empty source hash")
	}
	if id.SourceFileCount == 0 {
		t.Fatal("expected at least one file to be hashed from this package's own directory")
	}
	// A binary hash is best-effort (os.Executable can fail in unusual
	// environments), but under `go test` it resolves to the real test
	// binary, so this environment should always populate it.
	if id.BinaryHash == "" {
		t.Fatal("expected a non-empty binary hash under go test")
	}
}

func TestComputeSourceIdentityChangesWithDifferentRoot(t *testing.T) {
	h1 := NewHarness(func(int) error { return nil })
	h1.Start()
	if err := h1.ComputeSourceIdentity("."); err != nil {
		t.Fatalf("ComputeSourceIdentity: %v", err)
	}
	h2 := NewHarness(func(int) error { return nil })
	h2.Start()
	if err := h2.ComputeSourceIdentity(".."); err != nil {
		t.Fatalf("ComputeSourceIdentity: %v", err)
	}
	if h1.Identity().SourceHash == h2.Identity().SourceHash {
		t.Fatal("expected hashing a different, larger source root to produce a different source hash")
	}
	if h2.Identity().SourceFileCount <= h1.Identity().SourceFileCount {
		t.Fatalf("expected the parent directory (%d files) to contain at least as many files as this package alone (%d)",
			h2.Identity().SourceFileCount, h1.Identity().SourceFileCount)
	}
}

func TestStartResumableDetectsRestartAndContinuesChain(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "soak-state.json")

	// First process instance: starts a fresh run, takes two checkpoints,
	// and persists state after each -- exactly what a real long-running
	// process would do periodically so a crash never loses more than one
	// checkpoint interval of evidence.
	h1 := NewHarness(func(int) error { return nil })
	resumed1, err := h1.StartResumable(statePath, "")
	if err != nil {
		t.Fatalf("StartResumable (first instance): %v", err)
	}
	if resumed1 {
		t.Fatal("expected the first StartResumable call against a fresh path to NOT report a resume")
	}
	if h1.Identity().RestartCount != 0 {
		t.Fatalf("expected RestartCount=0 for a fresh run, got %d", h1.Identity().RestartCount)
	}
	_ = h1.RunOnce()
	h1.Checkpoint()
	if err := h1.SaveState(statePath); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	_ = h1.RunOnce()
	h1.Checkpoint()
	if err := h1.SaveState(statePath); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	firstRunID := h1.Identity().RunID
	firstCheckpointCount := len(h1.checkpoints)
	if firstCheckpointCount != 2 {
		t.Fatalf("expected 2 checkpoints from the first instance, got %d", firstCheckpointCount)
	}

	// Second process instance: simulates a fresh process (e.g. after a
	// crash) picking up the SAME run via the SAME state path.
	h2 := NewHarness(func(int) error { return nil })
	resumed2, err := h2.StartResumable(statePath, "")
	if err != nil {
		t.Fatalf("StartResumable (second instance): %v", err)
	}
	if !resumed2 {
		t.Fatal("expected the second StartResumable call to detect and report a resume")
	}
	if h2.Identity().RunID != firstRunID {
		t.Fatalf("expected the resumed run to keep the same run id %q, got %q", firstRunID, h2.Identity().RunID)
	}
	if h2.Identity().RestartCount != 1 {
		t.Fatalf("expected RestartCount=1 after one detected restart, got %d", h2.Identity().RestartCount)
	}
	if len(h2.checkpoints) != firstCheckpointCount {
		t.Fatalf("expected the resumed harness to load %d prior checkpoints, got %d", firstCheckpointCount, len(h2.checkpoints))
	}

	// Continue the run under the second instance and confirm the WHOLE
	// chain (spanning both process instances) still verifies -- proving
	// continuity is real, not just a matching label.
	_ = h2.RunOnce()
	h2.Checkpoint()
	if err := VerifyChain(h2.checkpoints); err != nil {
		t.Fatalf("expected the checkpoint chain to verify across the simulated restart, got %v", err)
	}
	if len(h2.checkpoints) != firstCheckpointCount+1 {
		t.Fatalf("expected %d total checkpoints after resuming and adding one more, got %d", firstCheckpointCount+1, len(h2.checkpoints))
	}

	// A THIRD instance resuming again must see RestartCount=2.
	h3 := NewHarness(func(int) error { return nil })
	if err := h2.SaveState(statePath); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	resumed3, err := h3.StartResumable(statePath, "")
	if err != nil {
		t.Fatalf("StartResumable (third instance): %v", err)
	}
	if !resumed3 || h3.Identity().RestartCount != 2 {
		t.Fatalf("expected a second detected restart (RestartCount=2), got resumed=%v restartCount=%d", resumed3, h3.Identity().RestartCount)
	}
}

func TestLoadStateRejectsMissingOrInvalidFile(t *testing.T) {
	if _, err := LoadState(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatal("expected LoadState to fail for a nonexistent path")
	}
}

func TestRunOnceCountsDroppedEventsSeparatelyFromErrors(t *testing.T) {
	calls := 0
	h := NewHarness(func(int) error {
		calls++
		switch calls {
		case 2:
			return ErrDroppedEvent
		case 4:
			return errors.New("a real, non-dropped failure")
		default:
			return nil
		}
	})
	h.Start()

	for i := 0; i < 3; i++ { // iterations 0,1,2 -- the 2nd (calls==2) is a drop
		if err := h.RunOnce(); err != nil && !errors.Is(err, ErrDroppedEvent) {
			t.Fatalf("unexpected real error on iteration %d: %v", i, err)
		}
	}
	if h.dropped != 1 {
		t.Fatalf("expected 1 dropped event, got %d", h.dropped)
	}
	if h.errors != 0 {
		t.Fatalf("expected 0 real errors so far, got %d", h.errors)
	}

	err := h.RunOnce() // iteration 3 -> calls==4 -> real error
	if err == nil || errors.Is(err, ErrDroppedEvent) {
		t.Fatalf("expected a real (non-dropped) error, got %v", err)
	}
	if h.errors != 1 {
		t.Fatalf("expected 1 real error after the 4th call, got %d", h.errors)
	}
	if h.dropped != 1 {
		t.Fatalf("expected dropped count to stay at 1, got %d", h.dropped)
	}
}

func TestRunQualificationAtContinuesPastDroppedEvents(t *testing.T) {
	c := goodContract()
	calls := 0
	result, report, err := RunQualificationAt(c, 0.02, func(i int) error {
		calls++
		if calls%3 == 0 {
			return ErrDroppedEvent
		}
		return nil
	}, "")
	if err != nil {
		t.Fatalf("RunQualificationAt: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected dropped events to not fail the run, got: %s", result.FailureReason)
	}
	if report.DroppedEvents == 0 {
		t.Fatal("expected at least one dropped event to have been recorded")
	}
	if report.Errors != 0 {
		t.Fatalf("expected zero real errors, got %d", report.Errors)
	}
	if report.Iterations < 3 {
		t.Fatalf("expected the loop to keep running past dropped events, got only %d iterations", report.Iterations)
	}
}

func TestCheckpointIncludesRunIDAndResourceFields(t *testing.T) {
	h := NewHarness(func(int) error { return nil })
	h.Start()
	cp := h.Checkpoint()
	if cp.RunID == "" || cp.RunID != h.Identity().RunID {
		t.Fatalf("expected checkpoint to carry the harness's run id, got %q want %q", cp.RunID, h.Identity().RunID)
	}
	// CPU seconds must be non-negative real readings (they may
	// legitimately be 0.0 on a very fast, freshly-started process, but
	// must never be negative -- a sign the syscall reading itself was
	// broken rather than just small).
	if cp.CPUUserSeconds < 0 || cp.CPUSystemSeconds < 0 {
		t.Fatalf("expected non-negative CPU readings, got user=%f system=%f", cp.CPUUserSeconds, cp.CPUSystemSeconds)
	}
}

func TestVerifyChainDetectsRunIDGrafting(t *testing.T) {
	h1 := NewHarness(func(int) error { return nil })
	h1.Start()
	h1.Checkpoint()
	h1.Checkpoint()

	h2 := NewHarness(func(int) error { return nil })
	h2.Start()
	grafted := h2.Checkpoint() // a genuine checkpoint from a DIFFERENT run

	tampered := append([]Checkpoint(nil), h1.checkpoints...)
	grafted.Seq = uint64(len(tampered))
	grafted.PrevHash = tampered[len(tampered)-1].Hash
	grafted.Hash = hashCheckpoint(grafted) // re-hashed honestly for ITS OWN (wrong) run id, exactly what an attacker splicing evidence between two real runs would produce
	tampered = append(tampered, grafted)

	if err := VerifyChain(tampered); err == nil {
		t.Fatal("expected VerifyChain to reject a checkpoint grafted in from a different run even though its own hash is internally consistent")
	}
}

func TestReconcileFlagsInconsistentCheckpointSpan(t *testing.T) {
	// A checkpoint sequence claiming to span only 1 second while the
	// harness's own independently-measured elapsed time is 100 seconds
	// is a real inconsistency -- e.g. checkpointing silently stopped
	// long before the run actually ended.
	checkpoints := []Checkpoint{
		{Seq: 0, AtSeconds: 0},
		{Seq: 1, AtSeconds: 0.5},
		{Seq: 2, AtSeconds: 1.0},
		{Seq: 3, AtSeconds: 1.2},
	}
	rec := Reconcile(checkpoints, 100*time.Second)
	if rec.Consistent {
		t.Fatalf("expected an inconsistent reconciliation, got %+v", rec)
	}
}

func TestReconcileAcceptsConsistentCheckpointSpan(t *testing.T) {
	checkpoints := []Checkpoint{
		{Seq: 0, AtSeconds: 0},
		{Seq: 1, AtSeconds: 0.3},
		{Seq: 2, AtSeconds: 0.6},
		{Seq: 3, AtSeconds: 0.95},
	}
	rec := Reconcile(checkpoints, time.Second)
	if !rec.Consistent {
		t.Fatalf("expected a consistent reconciliation, got %+v", rec)
	}
}

func TestDetectLeakFromCheckpointsMirrorsLiveDetection(t *testing.T) {
	checkpoints := []Checkpoint{
		{Seq: 0, Goroutines: 5},
		{Seq: 1, Goroutines: 6},
		{Seq: 2, Goroutines: 80},
	}
	verdict := DetectLeakFromCheckpoints(checkpoints, 10)
	if !verdict.Leaked {
		t.Fatalf("expected a leak to be flagged from checkpoint data, got %+v", verdict)
	}
	if verdict.BaselineGoroutines != 5 || verdict.FinalGoroutines != 80 {
		t.Fatalf("unexpected baseline/final goroutines: %+v", verdict)
	}
}

func TestDetectDriftFromCheckpointsMirrorsLiveDetection(t *testing.T) {
	checkpoints := []Checkpoint{
		{AtSeconds: 0, HeapAllocMB: 10},
		{AtSeconds: 1, HeapAllocMB: 10},
		{AtSeconds: 2, HeapAllocMB: 40},
		{AtSeconds: 3, HeapAllocMB: 45},
	}
	verdict := DetectDriftFromCheckpoints(checkpoints, 0.5)
	if !verdict.Drifted {
		t.Fatalf("expected drift to be flagged from checkpoint data, got %+v", verdict)
	}
}

func TestRunIdentityCarriesThroughGeneratedEvidence(t *testing.T) {
	c := goodContract()
	_, report, err := RunQualificationAt(c, 0.02, func(i int) error { return nil }, ".")
	if err != nil {
		t.Fatalf("RunQualificationAt: %v", err)
	}
	if report.Identity.RunID == "" {
		t.Fatal("expected the final report to carry a non-empty run id")
	}
	if report.Identity.SourceHash == "" {
		t.Fatal("expected the final report to carry a non-empty source hash when sourceRoot is set")
	}
	if report.Identity.Host.Hostname == "" {
		t.Fatal("expected the final report to carry host identity")
	}
	every := report.Checkpoints
	if len(every) == 0 {
		t.Fatal("expected at least one checkpoint")
	}
	for _, cp := range every {
		if cp.RunID != report.Identity.RunID {
			t.Fatalf("expected every checkpoint to carry the run's own run id, got %q want %q", cp.RunID, report.Identity.RunID)
		}
	}
}
