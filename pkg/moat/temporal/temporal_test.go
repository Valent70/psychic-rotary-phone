package temporal

import "testing"

func TestAssertAndSnapshotAtTransactionTick(t *testing.T) {
	e := NewEngine()
	f := NewFactID("VESSEL:1", "FLAG_STATE")
	e.Assert(Version{Fact: f, Value: "PANAMA", ValidFrom: 100, ValidTo: 0, TxTick: 1, ActorID: "ingest"})
	snap := e.SnapshotAtTransactionTick(1)
	if snap[f].Value != "PANAMA" {
		t.Fatalf("expected PANAMA at tx tick 1, got %+v", snap[f])
	}
}

func TestBitemporalCorrectionDoesNotEraseHistory(t *testing.T) {
	e := NewEngine()
	f := NewFactID("VESSEL:1", "FLAG_STATE")
	// original belief: PANAMA valid from t=100, recorded at tx=1
	e.Assert(Version{Fact: f, Value: "PANAMA", ValidFrom: 100, ValidTo: 0, TxTick: 1, ActorID: "ingest"})
	// correction: actually it was LIBERIA the whole time, recorded later at tx=5
	e.Assert(Version{Fact: f, Value: "LIBERIA", ValidFrom: 100, ValidTo: 0, TxTick: 5, ActorID: "corrector"})

	// "what did we know when" — transaction-time snapshot BEFORE the correction
	beforeCorrection := e.SnapshotAtTransactionTick(3)
	if beforeCorrection[f].Value != "PANAMA" {
		t.Fatalf("transaction-time snapshot before correction should still show PANAMA, got %+v", beforeCorrection[f])
	}

	// "what do we now believe about valid-time 100" — should reflect the correction
	nowBelief := e.AsOf(100)
	if nowBelief[f].Value != "LIBERIA" {
		t.Fatalf("AsOf(100) after correction should reflect LIBERIA, got %+v", nowBelief[f])
	}

	hist := e.History(f)
	if len(hist) != 2 {
		t.Fatalf("expected full 2-version history preserved, got %d", len(hist))
	}
	if hist[0].Value != "PANAMA" {
		t.Fatalf("history should preserve original PANAMA assertion, got %+v", hist[0])
	}
}

func TestAsOfRespectsValidWindow(t *testing.T) {
	e := NewEngine()
	f := NewFactID("VESSEL:1", "OWNER")
	e.Assert(Version{Fact: f, Value: "OWNER_A", ValidFrom: 0, ValidTo: 50, TxTick: 1, ActorID: "a"})
	e.Assert(Version{Fact: f, Value: "OWNER_B", ValidFrom: 50, ValidTo: 0, TxTick: 2, ActorID: "a"})

	early := e.AsOf(10)
	if early[f].Value != "OWNER_A" {
		t.Fatalf("expected OWNER_A at valid tick 10, got %+v", early[f])
	}
	late := e.AsOf(100)
	if late[f].Value != "OWNER_B" {
		t.Fatalf("expected OWNER_B at valid tick 100, got %+v", late[f])
	}
	// exactly at boundary of OWNER_A's [0,50): 50 is exclusive end
	boundary := e.AsOf(50)
	if boundary[f].Value != "OWNER_B" {
		t.Fatalf("expected OWNER_B at valid tick 50 (boundary), got %+v", boundary[f])
	}
}

func TestCorrectionsDetection(t *testing.T) {
	e := NewEngine()
	f := NewFactID("VESSEL:1", "FLAG_STATE")
	e.Assert(Version{Fact: f, Value: "PANAMA", ValidFrom: 0, ValidTo: 0, TxTick: 1, ActorID: "a"})
	e.Assert(Version{Fact: f, Value: "LIBERIA", ValidFrom: 0, ValidTo: 0, TxTick: 2, ActorID: "a"})
	corrections := e.Corrections(f)
	if len(corrections) != 1 || corrections[0].Value != "LIBERIA" {
		t.Fatalf("expected 1 correction (LIBERIA), got %+v", corrections)
	}
}

func TestInvalidWindowRejected(t *testing.T) {
	e := NewEngine()
	_, err := e.Assert(Version{Fact: "F", Value: "V", ValidFrom: 100, ValidTo: 50, TxTick: 1})
	if err == nil {
		t.Fatalf("expected error for ValidTo <= ValidFrom")
	}
}

func TestVerifyChainAndTamperDetection(t *testing.T) {
	e := NewEngine()
	e.Assert(Version{Fact: "F1", Value: "V1", ValidFrom: 0, TxTick: 1})
	e.Assert(Version{Fact: "F2", Value: "V2", ValidFrom: 0, TxTick: 2})
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain: %v", err)
	}
	e.log[0].Version.Value = "TAMPERED"
	if err := e.VerifyChain(); err == nil {
		t.Fatalf("expected tamper detection")
	}
}

func TestRebuildDeterministic(t *testing.T) {
	e1 := NewEngine()
	f := NewFactID("VESSEL:2", "CARGO")
	e1.Assert(Version{Fact: f, Value: "OIL", ValidFrom: 10, ValidTo: 20, TxTick: 1})
	e1.Assert(Version{Fact: f, Value: "GRAIN", ValidFrom: 20, ValidTo: 0, TxTick: 2})

	e2, err := Rebuild(e1.Snapshot())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if e1.Head() != e2.Head() {
		t.Fatalf("head mismatch: %s vs %s", e1.Head(), e2.Head())
	}
	a1 := e1.AsOf(25)
	a2 := e2.AsOf(25)
	if a1[f].Value != a2[f].Value {
		t.Fatalf("AsOf mismatch after rebuild: %s vs %s", a1[f].Value, a2[f].Value)
	}
}
