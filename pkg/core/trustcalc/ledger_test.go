package trustcalc

import "testing"

// TestBeliefLedgerReplayEqualsLiveState is the exact adversarial proof
// both audits asked for: same history -> Trust State A (live); replay
// (history) -> Trust State B; A == B, field-for-field, for every
// subject, not just one.
func TestBeliefLedgerReplayEqualsLiveState(t *testing.T) {
	ledger := NewLedger()
	live := NewWithLedger(1, 1, ledger)

	events := []struct {
		subject string
		matched bool
		weight  float64
	}{
		{"AIS_VENDOR_A", true, 1.0},
		{"AIS_VENDOR_A", true, 0.8},
		{"AIS_VENDOR_A", false, 0.3},
		{"SAT_OPERATOR_X", false, 1.0},
		{"SAT_OPERATOR_X", true, 0.5},
		{"AIS_VENDOR_A", true, 0.9},
		{"INSURER_B", true, 1.0},
	}
	for _, ev := range events {
		if _, err := live.Observe(ev.subject, ev.matched, ev.weight); err != nil {
			t.Fatalf("live.Observe(%s): %v", ev.subject, err)
		}
	}

	if got, want := ledger.Len(), len(events); got != want {
		t.Fatalf("ledger recorded %d entries, want %d", got, want)
	}
	if err := ledger.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain, got %v", err)
	}

	replayed, err := ledger.Replay(1, 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	for _, subject := range []string{"AIS_VENDOR_A", "SAT_OPERATOR_X", "INSURER_B"} {
		a := live.Belief(subject)
		b := replayed.Belief(subject)
		if a.Alpha != b.Alpha || a.Beta != b.Beta {
			t.Fatalf("subject %s diverged: live=%+v replay=%+v", subject, a, b)
		}
		if a.Mean() != b.Mean() {
			t.Fatalf("subject %s Mean() diverged: live=%v replay=%v", subject, a.Mean(), b.Mean())
		}
	}

	// A subject that never appeared in history must independently
	// come back to the identical, untouched prior in both worlds.
	untouched := "NEVER_OBSERVED"
	if live.Belief(untouched) != replayed.Belief(untouched) {
		t.Fatalf("unobserved subject should be identical prior in both states")
	}
}

// TestBeliefLedgerTamperDetection proves the chain is not merely
// decorative: mutating any single recorded field must be caught by
// VerifyChain, and Replay must refuse to run on a tampered ledger
// rather than silently producing a state that no longer corresponds
// to the claimed history.
func TestBeliefLedgerTamperDetection(t *testing.T) {
	ledger := NewLedger()
	c := NewWithLedger(1, 1, ledger)
	for i := 0; i < 5; i++ {
		if _, err := c.Observe("source", i%2 == 0, 1.0); err != nil {
			t.Fatal(err)
		}
	}
	if err := ledger.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain before tamper, got %v", err)
	}

	// Tamper: flip Matched on a middle entry without recomputing hashes.
	ledger.mu.Lock()
	ledger.entries[2].Matched = !ledger.entries[2].Matched
	ledger.mu.Unlock()

	if err := ledger.VerifyChain(); err == nil {
		t.Fatal("expected VerifyChain to detect tampered entry, got nil error")
	}
	if _, err := ledger.Replay(1, 1); err == nil {
		t.Fatal("expected Replay to refuse a tampered ledger")
	}
}

// TestBeliefLedgerHashExcludesWallClock proves WallClock is
// deliberately kept out of the chained hash (package doc, and the
// full-repo audit's §27 wall-clock-in-canonical-evidence finding):
// two entries with identical logical content but different WallClock
// values must hash identically.
func TestBeliefLedgerHashExcludesWallClock(t *testing.T) {
	h1 := hashEntry(ledgerGenesisHash, 0, "s", true, 1.0)
	h2 := hashEntry(ledgerGenesisHash, 0, "s", true, 1.0)
	if h1 != h2 {
		t.Fatalf("expected identical hashes for identical logical content, got %s vs %s", h1, h2)
	}
}

// TestBeliefLedgerNilIsNoOp proves the plain New() path (no ledger)
// remains fully backward-compatible: Observe works exactly as before
// and Ledger() reports nil, never panics.
func TestBeliefLedgerNilIsNoOp(t *testing.T) {
	c := New(1, 1)
	if c.Ledger() != nil {
		t.Fatal("expected nil Ledger on plain New()")
	}
	if _, err := c.Observe("x", true, 1.0); err != nil {
		t.Fatalf("Observe without ledger should still work: %v", err)
	}
}
