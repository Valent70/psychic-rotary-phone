package reconciliation

import "testing"

func mkEnv(id string, seq uint64, payload string, authorized bool) Envelope {
	return Envelope{ID: id, Sequence: seq, Hash: ComputeHash(payload), Payload: payload, Authorized: authorized}
}

func TestCleanRunPassesAllInvariants(t *testing.T) {
	e := NewEngine()
	for i := 0; i < 10; i++ {
		accepted, reason := e.Process(mkEnv(itoa(i), uint64(i), "payload-"+itoa(i), true))
		if !accepted {
			t.Fatalf("envelope %d unexpectedly rejected: %s", i, reason)
		}
	}
	report := e.Finalize(10)
	if !report.Pass() {
		t.Fatalf("expected all invariants to hold, violations=%v report=%+v", report.Verify(), report)
	}
	if report.InputCount != 10 || report.AcceptedCount != 10 || report.RejectedCount != 0 {
		t.Fatalf("unexpected counts: %+v", report)
	}
	if report.FinalRoot == "" {
		t.Fatal("FinalRoot must be non-empty for a non-empty accepted set")
	}
}

func TestInputEqualsAcceptedPlusRejectedAlwaysHolds(t *testing.T) {
	e := NewEngine()
	e.Process(mkEnv("a", 0, "p1", true))
	e.Process(mkEnv("a", 1, "p1", true)) // duplicate ID
	e.Process(mkEnv("b", 2, "p2", false)) // unauthorized
	bad := mkEnv("c", 3, "p3", true)
	bad.Hash = "not-the-real-hash"
	e.Process(bad) // hash mismatch

	report := e.Finalize(4)
	if report.InputCount != report.AcceptedCount+report.RejectedCount {
		t.Fatalf("invariant violated: input=%d accepted=%d rejected=%d", report.InputCount, report.AcceptedCount, report.RejectedCount)
	}
	if report.DuplicateCount != 1 {
		t.Errorf("expected 1 duplicate, got %d", report.DuplicateCount)
	}
	if report.HashMismatchCount != 1 {
		t.Errorf("expected 1 hash mismatch, got %d", report.HashMismatchCount)
	}
	if report.UnauthorizedAcceptCount != 0 {
		t.Errorf("unauthorized envelope must be rejected, not accepted: UnauthorizedAcceptCount=%d", report.UnauthorizedAcceptCount)
	}
	violations := report.Verify()
	foundHashMismatch, foundInputInvariant := false, false
	for _, v := range violations {
		if v == InvariantZeroHashMismatch {
			foundHashMismatch = true
		}
		if v == InvariantInputEqualsAcceptedPlusRejected {
			foundInputInvariant = true
		}
	}
	if !foundHashMismatch {
		t.Errorf("expected InvariantZeroHashMismatch to be reported violated, got %v", violations)
	}
	if foundInputInvariant {
		t.Errorf("input==accepted+rejected must ALWAYS hold structurally, got violation in %v", violations)
	}
}

func TestLostCountDerivedFromExpectedNotGuessed(t *testing.T) {
	e := NewEngine()
	e.Process(mkEnv("a", 0, "p1", true))
	e.Process(mkEnv("b", 1, "p2", true))
	// Only 2 of the expected 5 envelopes ever arrived.
	report := e.Finalize(5)
	if report.LostCount != 3 {
		t.Fatalf("expected lost=3 (5 expected - 2 arrived), got %d", report.LostCount)
	}
	if report.Pass() {
		t.Fatal("a run with lost envelopes must not pass acceptance")
	}
}

func TestReorderedCountsOutOfOrderSequenceWithoutRejecting(t *testing.T) {
	e := NewEngine()
	e.Process(mkEnv("a", 5, "p1", true))
	accepted, _ := e.Process(mkEnv("b", 2, "p2", true)) // arrives after a higher sequence was already seen
	if !accepted {
		t.Fatal("a reordered-but-authentic envelope must still be accepted, not rejected")
	}
	report := e.Finalize(2)
	if report.ReorderedCount != 1 {
		t.Fatalf("expected ReorderedCount=1, got %d", report.ReorderedCount)
	}
	if report.AcceptedCount != 2 {
		t.Fatalf("reordering must not cause rejection, AcceptedCount=%d", report.AcceptedCount)
	}
}

func TestFinalRootIsDeterministicAndOrderIndependent(t *testing.T) {
	e1 := NewEngine()
	e1.Process(mkEnv("a", 0, "p1", true))
	e1.Process(mkEnv("b", 1, "p2", true))
	r1 := e1.Finalize(2)

	// Same envelopes, submitted in reverse order.
	e2 := NewEngine()
	e2.Process(mkEnv("b", 1, "p2", true))
	e2.Process(mkEnv("a", 0, "p1", true))
	r2 := e2.Finalize(2)

	if r1.FinalRoot != r2.FinalRoot {
		t.Fatalf("FinalRoot must be order-independent: %s vs %s", r1.FinalRoot, r2.FinalRoot)
	}
}

func itoa(i int) string {
	digits := "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return string(b)
}
