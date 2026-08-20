package soak

import "testing"

// TestQueueDepthFuncIsSampled proves audit item A9's queue-depth
// requirement is real: a registered QueueDepthFunc's value shows up in
// both Sample and Checkpoint, and a workload with none registered
// reports 0 rather than an error.
func TestQueueDepthFuncIsSampled(t *testing.T) {
	h := NewHarness(func(i int) error { return nil })
	depth := 7
	h.QueueDepthFunc = func() int { return depth }
	h.Start()
	s := h.Monitor()
	if s.QueueDepth != 7 {
		t.Fatalf("expected QueueDepth=7, got %d", s.QueueDepth)
	}
	depth = 3
	cp := h.Checkpoint()
	if cp.QueueDepth != 3 {
		t.Fatalf("expected QueueDepth=3 after change, got %d", cp.QueueDepth)
	}

	h2 := NewHarness(func(i int) error { return nil })
	h2.Start()
	s2 := h2.Monitor()
	if s2.QueueDepth != 0 {
		t.Fatalf("no QueueDepthFunc registered: expected 0, got %d", s2.QueueDepth)
	}
}

// TestErrorRateReflectsFailuresOverIterations proves error rate is a
// genuine ratio, not merely a re-exposed count.
func TestErrorRateReflectsFailuresOverIterations(t *testing.T) {
	calls := 0
	h := NewHarness(func(i int) error {
		calls++
		if calls%4 == 0 {
			return errTestWorkload
		}
		return nil
	})
	h.Start()
	for i := 0; i < 8; i++ {
		_ = h.RunOnce()
	}
	s := h.Monitor()
	wantRate := 2.0 / 8.0
	if s.ErrorRate != wantRate {
		t.Fatalf("expected ErrorRate=%.4f (2 errors / 8 iterations), got %.4f", wantRate, s.ErrorRate)
	}
	elapsed := h.Finalize()
	report := h.GenerateEvidence(elapsed, 50, 0.5)
	if report.ErrorRate != wantRate {
		t.Fatalf("Report.ErrorRate: expected %.4f, got %.4f", wantRate, report.ErrorRate)
	}
}

// TestSimulateRestartPreservesChainAndCountsRestart is audit item A9's
// restart-handling requirement: the evidence chain must survive a
// restart event unbroken, and Report.Restarts must reflect it.
func TestSimulateRestartPreservesChainAndCountsRestart(t *testing.T) {
	h := NewHarness(func(i int) error { return nil })
	h.Start()
	for i := 0; i < 3; i++ {
		_ = h.RunOnce()
	}
	cp1 := h.Checkpoint()
	restartCP := h.SimulateRestart()
	if !restartCP.Restarted {
		t.Fatal("SimulateRestart's own checkpoint must have Restarted=true")
	}
	if restartCP.PrevHash != cp1.Hash {
		t.Fatalf("restart checkpoint must chain from the immediately preceding checkpoint: PrevHash=%s want=%s", restartCP.PrevHash, cp1.Hash)
	}
	for i := 0; i < 3; i++ {
		_ = h.RunOnce()
	}
	cp2 := h.Checkpoint()
	if cp2.PrevHash != restartCP.Hash {
		t.Fatalf("post-restart checkpoint must chain from the restart checkpoint")
	}

	elapsed := h.Finalize()
	report := h.GenerateEvidence(elapsed, 50, 0.5)
	if report.Restarts != 1 {
		t.Fatalf("expected Restarts=1, got %d", report.Restarts)
	}
	if !report.ChainVerified {
		t.Fatal("checkpoint chain must remain verified across a restart event")
	}
}

// TestReconcileAccountsForEveryIteration ties audit item A6's
// reconciliation engine into A9's soak harness: every completed
// iteration must be accounted for, and the reconciliation must pass its
// own invariants for a clean run.
func TestReconcileAccountsForEveryIteration(t *testing.T) {
	h := NewHarness(func(i int) error { return nil })
	h.Start()
	for i := 0; i < 20; i++ {
		_ = h.RunOnce()
	}
	recon := h.Reconcile(20)
	if !recon.Pass() {
		t.Fatalf("expected reconciliation to pass for a clean run, violations=%v report=%+v", recon.Verify(), recon)
	}
	if recon.InputCount != 20 || recon.AcceptedCount != 20 {
		t.Fatalf("expected input=accepted=20, got %+v", recon)
	}

	// A caller that declares MORE iterations expected than actually ran
	// (e.g. the run was cut short) must show a real, non-zero LostCount.
	reconShort := h.Reconcile(50)
	if reconShort.LostCount != 30 {
		t.Fatalf("expected LostCount=30 (50 expected - 20 ran), got %d", reconShort.LostCount)
	}
	if reconShort.Pass() {
		t.Fatal("a run short of its expected iteration count must not pass reconciliation")
	}
}

var errTestWorkload = &testWorkloadError{}

type testWorkloadError struct{}

func (e *testWorkloadError) Error() string { return "simulated workload failure" }
