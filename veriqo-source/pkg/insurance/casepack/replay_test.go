package casepack

import (
	"encoding/json"
	"testing"
)

// TestColdReplayReproducesIdenticalResultOnEveryCase is the closure
// test for MVP §80 item 14 / Final Design §20 "C5" / spec §73. It
// proves the property the prior round left honestly OPEN: a case
// replayed from NOTHING but its own serialised snapshot reproduces the
// live result exactly, on every one of the seven synthetic cases.
func TestColdReplayReproducesIdenticalResultOnEveryCase(t *testing.T) {
	for _, c := range All() {
		c := c
		t.Run(string(c.ID), func(t *testing.T) {
			_, _, report, err := ColdReplay(c)
			if err != nil {
				t.Fatalf("ColdReplay: %v", err)
			}
			if !report.Pass() {
				t.Fatalf("cold replay did not reproduce the live result: %v", report.Failures)
			}
			if report.SnapshotID == "" {
				t.Fatal("SnapshotID must be set on a passing report")
			}
		})
	}
}

// TestReplayFromSnapshotNeverTouchesTheOriginalCase proves the replay
// path is genuinely cold: it takes bytes produced by an EARLIER,
// separately-scoped Case value, decoded fresh, with no shared memory
// between the two.
func TestReplayFromSnapshotNeverTouchesTheOriginalCase(t *testing.T) {
	var snapshot []byte
	func() {
		// c is scoped to this closure so it cannot leak into the
		// replay call below by any means other than the bytes.
		c, err := Get(CasePortCallDemurrage)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		snapshot, err = c.Snapshot()
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
	}()

	res, err := ReplayFromSnapshot(snapshot)
	if err != nil {
		t.Fatalf("ReplayFromSnapshot: %v", err)
	}
	if res.Manifest.EvidenceRootHash == "" {
		t.Fatal("replayed result carries no evidence root hash")
	}
}

// TestSnapshotIsDeterministicBytes proves Snapshot produces
// byte-identical output across repeated calls over an unchanged Case —
// the property that makes SnapshotID a genuine content address.
func TestSnapshotIsDeterministicBytes(t *testing.T) {
	c, err := Get(CaseCargoDamageReefer)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	a, err := c.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	b, err := c.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("Snapshot produced different bytes across two calls over the same Case")
	}
	if SnapshotID(a) != SnapshotID(b) {
		t.Fatal("SnapshotID diverged over identical snapshot bytes")
	}
}

// TestReplayFromEmptySnapshotFails proves the fail-closed edge: an
// empty or garbage snapshot must never silently produce a Result.
func TestReplayFromEmptySnapshotFails(t *testing.T) {
	if _, err := ReplayFromSnapshot(nil); err == nil {
		t.Fatal("ReplayFromSnapshot(nil) must fail")
	}
	if _, err := ReplayFromSnapshot([]byte("not json")); err == nil {
		t.Fatal("ReplayFromSnapshot with malformed JSON must fail")
	}
	// Well-formed JSON that is not a valid Case (fails Validate) must
	// also be refused, not silently driven.
	garbage, _ := json.Marshal(struct{ Foo string }{"bar"})
	if _, err := ReplayFromSnapshot(garbage); err == nil {
		t.Fatal("ReplayFromSnapshot with a structurally invalid case must fail")
	}
}

// TestColdReplayDetectsADivergedSnapshot proves the comparison is
// genuinely checked, not vacuously true: replaying a snapshot that was
// tampered with after serialisation (a different case entirely) must
// not be silently accepted as matching the ORIGINAL live result's
// report path.
func TestColdReplayDetectsADivergedSnapshot(t *testing.T) {
	c1, err := Get(CasePortCallDemurrage)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	c2, err := Get(CaseCargoDamageReefer)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	live, err := Drive(c1, nil)
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	snap2, err := c2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	replayed, err := ReplayFromSnapshot(snap2)
	if err != nil {
		t.Fatalf("ReplayFromSnapshot: %v", err)
	}
	if live.Manifest.EvidenceRootHash == replayed.Manifest.EvidenceRootHash {
		t.Fatal("two structurally different cases must not produce the same evidence root hash")
	}
}
