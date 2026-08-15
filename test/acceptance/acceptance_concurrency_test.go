package acceptance

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/entity"
)

// ===== Category 5/5 — DISTRIBUTED / CONCURRENCY (10) ========================
// Run with -race in CI (scripts/verify.sh already does); these tests
// are meaningful specifically under the race detector.

func TestAcceptance41_ConcurrentRunUnifiedOnSharedOrchestrator(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := baseIntent(fmt.Sprintf("conc-1-%d", i), entity.Alias{Kind: "IMO", Value: fmt.Sprintf("9%06d", i)})
			cs := baseCase(fmt.Sprintf("subject-%d", i))
			if _, err := o.RunUnified(context.Background(), in, standardPlan(in), cs); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent RunUnified failed: %v", err)
	}
}

func TestAcceptance42_ConcurrentEntityMergesOnSameAlias(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	alias := entity.Alias{Kind: "IMO", Value: "9700000"}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other := entity.Alias{Kind: "CALLSIGN", Value: fmt.Sprintf("C%03d", i)}
			_, _ = o.Entities.Merge("conc-2", alias, other, uint64(i))
		}(i)
	}
	wg.Wait()
	if err := o.Entities.VerifyChain(); err != nil {
		t.Fatalf("expected a clean entity chain after concurrent merges, got %v", err)
	}
}

func TestAcceptance43_ConcurrentCalibrationOutcomeRecording(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := baseIntent(fmt.Sprintf("conc-3-%d", i), entity.Alias{Kind: "IMO", Value: fmt.Sprintf("9%06d", 900000+i)})
			res, err := o.RunUnified(context.Background(), in, standardPlan(in), baseCase(fmt.Sprintf("subj-%d", i)))
			if err != nil {
				t.Errorf("RunUnified: %v", err)
				return
			}
			if _, err := o.RecordOutcome(res, res.Canonical.Arbitration.Winner, fmt.Sprintf("inspector-%d", i), uint64(i)); err != nil {
				t.Errorf("RecordOutcome: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if err := o.Calibration.VerifyLedger(); err != nil {
		t.Fatalf("expected a clean calibration ledger after concurrent outcome recording: %v", err)
	}
}

func TestAcceptance44_ConcurrentReadsOfSameTwin(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("conc-4", entity.Alias{Kind: "IMO", Value: "9800000"})
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = o.Pipeline.Twin.Get(res.Canonical.Twin.Entity)
		}()
	}
	wg.Wait()
}

func TestAcceptance45_ConcurrentDistinctPipelinesAreIsolated(t *testing.T) {
	o1 := lifecycle.NewOrchestrator(nil, nil)
	o2 := lifecycle.NewOrchestrator(nil, nil)
	var wg sync.WaitGroup
	wg.Add(2)
	var res1, res2 *lifecycle.Result
	// Same ActorID+Intent+Case on both: determinism holds for
	// identical input including the actor performing the action —
	// ActorID is correctly part of the hash-chained audit trail
	// (digitaltwin's ledger records who acted), so it must be held
	// constant here, not varied, to isolate the "two independent
	// pipeline instances" variable this test actually targets.
	go func() {
		defer wg.Done()
		in := baseIntent("conc-5-shared-actor")
		res1 = mustRun(t, o1, in, standardPlan(in), baseCase("x"))
	}()
	go func() {
		defer wg.Done()
		in := baseIntent("conc-5-shared-actor")
		res2 = mustRun(t, o2, in, standardPlan(in), baseCase("x"))
	}()
	wg.Wait()
	if res1.Certificate.Hash != res2.Certificate.Hash {
		t.Fatalf("expected two isolated orchestrators given identical input (including actor) to still reach identical certificates (isolation should not affect determinism)")
	}
}

func TestAcceptance46_ConcurrentEvidenceGraphReads(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("conc-6")
	mustRun(t, o, in, standardPlan(in), baseCase("x"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = o.Pipeline.Fusion.ReplayAll()
		}()
	}
	wg.Wait()
}

func TestAcceptance47_ConcurrentTrustCalculusObserveIsRaceSafe(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o.Pipeline.Trust.Observe(fmt.Sprintf("subj-%d", i%3), i%2 == 0, 1.0)
		}(i)
	}
	wg.Wait()
}

func TestAcceptance48_HighConcurrencyStressNoPanicNoDeadlock(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := baseIntent(fmt.Sprintf("conc-8-%d", i), entity.Alias{Kind: "IMO", Value: fmt.Sprintf("9%06d", 800000+i)})
			_, _ = o.RunUnified(context.Background(), in, standardPlan(in), baseCase(fmt.Sprintf("s-%d", i)))
		}(i)
	}
	wg.Wait()
}

func TestAcceptance49_ConcurrentVerifyChainCallsAreReadSafe(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("conc-9")
	mustRun(t, o, in, standardPlan(in), baseCase("x"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = o.Pipeline.Fusion.VerifyChain()
			_ = o.Pipeline.Twin.VerifyChain()
			_ = o.Entities.VerifyChain()
		}()
	}
	wg.Wait()
}

func TestAcceptance50_ChainIntegrityHoldsAfterConcurrentNoiseThenSequentialCase(t *testing.T) {
	// Note on what this test does NOT assert: comparing a certificate
	// against a fresh orchestrator's certificate for "the same case"
	// is NOT a valid determinism check here, because every ledger in
	// this repo is hash-CHAINED (index + PrevHash are part of the
	// hash by design — see e.g. pkg/moat/digitaltwin's hashUpdate).
	// After concurrent noise, o1's chain position genuinely differs
	// from a fresh o2's, so their hashes SHOULD differ — that is
	// tamper-evidence working as intended, not a determinism bug.
	// TestAcceptance45 already covers genuine cross-instance
	// determinism correctly (two orchestrators with NO prior
	// activity). What this test verifies instead: a burst of
	// concurrent activity followed by one more sequential case leaves
	// every ledger internally consistent and independently
	// verifiable — no corruption, no lost update, no race-induced
	// chain break.
	o1 := lifecycle.NewOrchestrator(nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := baseIntent(fmt.Sprintf("conc-10-noise-%d", i), entity.Alias{Kind: "IMO", Value: fmt.Sprintf("9%06d", 700000+i)})
			_, _ = o1.RunUnified(context.Background(), in, standardPlan(in), baseCase(fmt.Sprintf("noise-%d", i)))
		}(i)
	}
	wg.Wait()

	in := baseIntent("conc-10-final")
	res := mustRun(t, o1, in, standardPlan(in), baseCase("final-subject"))

	if err := lifecycle.VerifyCertificate(res.Certificate); err != nil {
		t.Fatalf("expected the final case's certificate to verify clean after prior concurrent noise: %v", err)
	}
	if err := o1.Pipeline.Twin.VerifyChain(); err != nil {
		t.Fatalf("expected twin chain integrity after concurrent noise + sequential case: %v", err)
	}
	if err := o1.Pipeline.Fusion.VerifyChain(); err != nil {
		t.Fatalf("expected fusion chain integrity after concurrent noise + sequential case: %v", err)
	}
	if err := o1.Entities.VerifyChain(); err != nil {
		t.Fatalf("expected entity chain integrity after concurrent noise + sequential case: %v", err)
	}
}
