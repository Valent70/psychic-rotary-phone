package audit

import (
	"errors"
	"testing"
)

func fillStore(t *testing.T, n int) *AuditStore {
	t.Helper()
	s := NewAuditStore()
	for i := 0; i < n; i++ {
		if _, err := s.Append("actor", "action", "payload"); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return s
}

func TestMerkleRootRefusesEmptyLeafSet(t *testing.T) {
	if _, err := MerkleRoot(nil); err != ErrEmptyLeafSet {
		t.Fatalf("expected ErrEmptyLeafSet, got %v", err)
	}
}

func TestMerkleRootIsDeterministicAndOrderSensitive(t *testing.T) {
	s := fillStore(t, 5)
	records := s.Snapshot()
	r1, err := MerkleRoot(records)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	r2, err := MerkleRoot(records)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	if r1 != r2 {
		t.Fatal("expected the same record set to produce the same root every time")
	}
	reversed := append([]AuditRecord(nil), records...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	r3, err := MerkleRoot(reversed)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	if r1 == r3 {
		t.Fatal("expected reordering the leaves to change the root")
	}
}

func TestMerkleRootSingleLeaf(t *testing.T) {
	s := fillStore(t, 1)
	root, err := MerkleRoot(s.Snapshot())
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	if len(root) != 64 {
		t.Fatalf("expected a 64-hex-char SHA-256 root, got %d chars", len(root))
	}
}

func TestInclusionProofVerifiesForEveryLeafAcrossVariousSizes(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 9, 16, 17, 33} {
		s := fillStore(t, n)
		records := s.Snapshot()
		root, err := MerkleRoot(records)
		if err != nil {
			t.Fatalf("n=%d: MerkleRoot: %v", n, err)
		}
		for i := 0; i < n; i++ {
			proof, err := GenerateInclusionProof(records, i)
			if err != nil {
				t.Fatalf("n=%d i=%d: GenerateInclusionProof: %v", n, i, err)
			}
			if proof.Root != root {
				t.Fatalf("n=%d i=%d: proof root %s does not match tree root %s", n, i, proof.Root, root)
			}
			if err := VerifyInclusionProof(proof); err != nil {
				t.Fatalf("n=%d i=%d: VerifyInclusionProof: %v", n, i, err)
			}
			if err := VerifyRecordInclusion(records[i], proof); err != nil {
				t.Fatalf("n=%d i=%d: VerifyRecordInclusion: %v", n, i, err)
			}
		}
	}
}

func TestInclusionProofRejectsTamperedLeafData(t *testing.T) {
	s := fillStore(t, 8)
	records := s.Snapshot()
	proof, err := GenerateInclusionProof(records, 3)
	if err != nil {
		t.Fatal(err)
	}
	proof.LeafData = records[4].Hash // swap in a different, still-valid leaf hash
	if err := VerifyInclusionProof(proof); err == nil {
		t.Fatal("expected a proof with a substituted leaf to fail verification")
	}
}

func TestInclusionProofRejectsTamperedSibling(t *testing.T) {
	s := fillStore(t, 8)
	records := s.Snapshot()
	proof, err := GenerateInclusionProof(records, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.Steps) == 0 {
		t.Fatal("expected at least one proof step for 8 leaves")
	}
	proof.Steps[0].Hash = "0000000000000000000000000000000000000000000000000000000000000"
	if err := VerifyInclusionProof(proof); err == nil {
		t.Fatal("expected a proof with a tampered sibling hash to fail verification")
	}
}

func TestInclusionProofRejectsTamperedRoot(t *testing.T) {
	s := fillStore(t, 8)
	records := s.Snapshot()
	proof, err := GenerateInclusionProof(records, 3)
	if err != nil {
		t.Fatal(err)
	}
	proof.Root = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := VerifyInclusionProof(proof); err == nil {
		t.Fatal("expected a proof asserting a wrong root to fail verification")
	}
}

func TestVerifyRecordInclusionRejectsMismatchedRecord(t *testing.T) {
	s := fillStore(t, 5)
	records := s.Snapshot()
	proof, err := GenerateInclusionProof(records, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecordInclusion(records[3], proof); err == nil {
		t.Fatal("expected a proof generated for record 2 to fail verification against record 3")
	}
}

func TestGenerateInclusionProofRejectsOutOfRangeIndex(t *testing.T) {
	s := fillStore(t, 3)
	records := s.Snapshot()
	if _, err := GenerateInclusionProof(records, -1); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("expected ErrIndexOutOfRange for index -1, got %v", err)
	}
	if _, err := GenerateInclusionProof(records, 3); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("expected ErrIndexOutOfRange for index 3 (len=3), got %v", err)
	}
}

func TestLocalAnchorerLabelsItselfAsASimulator(t *testing.T) {
	a := NewLocalAnchorer()
	receipt, err := a.Anchor("deadbeef", 42)
	if err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	if receipt.Root != "deadbeef" || receipt.Tick != 42 {
		t.Fatalf("unexpected receipt %v", receipt)
	}
	if receipt.AnchoredBy == "" {
		t.Fatal("expected AnchoredBy to be set")
	}
	// Must not read as a real external anchor -- this is the honesty
	// invariant the whole point of LocalAnchorer rests on.
	found := false
	for _, want := range []string{"simulator", "not a real external"} {
		if contains(receipt.AnchoredBy, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected AnchoredBy to self-identify as a simulator, got %q", receipt.AnchoredBy)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

func TestLocalAnchorerSequenceIsMonotonicAndLogged(t *testing.T) {
	a := NewLocalAnchorer()
	r1, _ := a.Anchor("root1", 1)
	r2, _ := a.Anchor("root2", 2)
	if r1.Reference == r2.Reference {
		t.Fatal("expected distinct anchor calls to produce distinct references")
	}
	log := a.Log()
	if len(log) != 2 {
		t.Fatalf("expected 2 logged receipts, got %d", len(log))
	}
}

func TestCheckpointSealsCurrentLedgerAndVerifies(t *testing.T) {
	s := fillStore(t, 6)
	anchorer := NewLocalAnchorer()
	cp, err := s.Checkpoint(anchorer, 100)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if cp.FromIndex != 1 || cp.ToIndex != 6 {
		t.Fatalf("expected range [1,6], got [%d,%d]", cp.FromIndex, cp.ToIndex)
	}
	if err := VerifyCheckpoint(s.Snapshot(), cp); err != nil {
		t.Fatalf("VerifyCheckpoint: %v", err)
	}
}

func TestCheckpointOnEmptyStoreRefuses(t *testing.T) {
	s := NewAuditStore()
	if _, err := s.Checkpoint(NewLocalAnchorer(), 1); err != ErrEmptyLeafSet {
		t.Fatalf("expected ErrEmptyLeafSet, got %v", err)
	}
}

func TestVerifyCheckpointDetectsLedgerGrowthSinceSealing(t *testing.T) {
	s := fillStore(t, 4)
	anchorer := NewLocalAnchorer()
	cp, err := s.Checkpoint(anchorer, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("actor", "action", "new record after checkpoint"); err != nil {
		t.Fatal(err)
	}
	// Verifying against the now-larger record set must fail: the range
	// no longer matches what the checkpoint claims.
	if err := VerifyCheckpoint(s.Snapshot(), cp); err == nil {
		t.Fatal("expected verification to fail once the ledger has grown past the checkpoint's range")
	}
	// Verifying against exactly the sealed range still succeeds.
	sealedRange := s.Snapshot()[:cp.ToIndex]
	if err := VerifyCheckpoint(sealedRange, cp); err != nil {
		t.Fatalf("expected the originally sealed range to still verify: %v", err)
	}
}

// TestVerifyCheckpointDetectsTamperedHashWithinSealedRange proves the
// Merkle checkpoint itself catches a tampered AuditRecord.Hash within
// the sealed range. Tampering Payload alone (leaving Hash untouched)
// is NOT caught here -- that is Auditor.VerifyChain's job, since
// MerkleRoot commits to each record's own Hash, and Hash is what
// hash-chains to Payload. This is deliberate defense in depth, not a
// gap: a verifier who wants full assurance runs VerifyChain (payload
// integrity) AND VerifyCheckpoint (range/order integrity against the
// anchored root), exactly as this test and
// TestPayloadTamperWithoutHashChangeIsCaughtByVerifyChainNotCheckpoint
// together demonstrate.
func TestVerifyCheckpointDetectsTamperedHashWithinSealedRange(t *testing.T) {
	s := fillStore(t, 4)
	anchorer := NewLocalAnchorer()
	cp, err := s.Checkpoint(anchorer, 1)
	if err != nil {
		t.Fatal(err)
	}
	records := s.Snapshot()
	records[1].Hash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := VerifyCheckpoint(records, cp); err == nil {
		t.Fatal("expected a tampered record hash within the sealed range to fail checkpoint verification")
	}
}

func TestPayloadTamperWithoutHashChangeIsCaughtByVerifyChainNotCheckpoint(t *testing.T) {
	s := fillStore(t, 4)
	anchorer := NewLocalAnchorer()
	cp, err := s.Checkpoint(anchorer, 1)
	if err != nil {
		t.Fatal(err)
	}
	records := s.Snapshot()
	records[1].Payload = "tampered" // Hash left untouched -- an inconsistent record

	if err := VerifyCheckpoint(records, cp); err != nil {
		t.Fatalf("VerifyCheckpoint alone does not (and cannot) detect a Payload/Hash inconsistency -- got an unexpected error: %v", err)
	}
	if err := (Auditor{}).VerifyChain(records); err == nil {
		t.Fatal("expected Auditor.VerifyChain to catch the Payload/Hash inconsistency that VerifyCheckpoint cannot see")
	}
}
