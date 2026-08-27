package audit

import (
	"sync"
	"testing"
)

func TestAppend_Basic(t *testing.T) {
	s := NewAuditStore()
	r, err := s.Append("valentino", "DEPLOY", "v1.0.0")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if r.Index != 1 {
		t.Fatalf("expected index 1, got %d", r.Index)
	}
}

func TestConcurrentAppend_Serialized(t *testing.T) {
	s := NewAuditStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = s.Append("actor", "ACTION", "payload")
		}(i)
	}
	wg.Wait()
	recs := s.Snapshot()
	if len(recs) != 50 {
		t.Fatalf("expected 50 records, got %d", len(recs))
	}
	seen := map[uint64]bool{}
	for _, r := range recs {
		if seen[r.Index] {
			t.Fatalf("duplicate index %d — store corrupted under concurrency", r.Index)
		}
		seen[r.Index] = true
	}
	if err := (Auditor{}).VerifyChain(recs); err != nil {
		t.Fatalf("chain invalid after concurrent append: %v", err)
	}
}

func TestVerifyChain_RejectsTamperedPayload(t *testing.T) {
	s := NewAuditStore()
	_, _ = s.Append("a", "ACTION", "original")
	recs := s.Snapshot()
	recs[0].Payload = "tampered"
	if err := (Auditor{}).VerifyChain(recs); err == nil {
		t.Fatal("expected tamper detection")
	}
}

func TestVerifySnapshot_MatchesRootHash(t *testing.T) {
	s := NewAuditStore()
	_, _ = s.Append("a", "ACTION1", "p1")
	_, _ = s.Append("a", "ACTION2", "p2")
	recs := s.Snapshot()
	root := s.RootHash()
	if err := (Auditor{}).VerifySnapshot(recs, root); err != nil {
		t.Fatalf("expected valid snapshot, got %v", err)
	}
	if err := (Auditor{}).VerifySnapshot(recs, "wronghash"); err == nil {
		t.Fatal("expected root hash mismatch to be detected")
	}
}

func TestVerifyChain_RejectsBrokenLinkage(t *testing.T) {
	s := NewAuditStore()
	_, _ = s.Append("a", "A1", "p1")
	_, _ = s.Append("a", "A2", "p2")
	recs := s.Snapshot()
	recs[1].PrevHash = "wrong"
	if err := (Auditor{}).VerifyChain(recs); err == nil {
		t.Fatal("expected broken linkage detection")
	}
}

func TestVerifySnapshot_EmptyLog(t *testing.T) {
	s := NewAuditStore()
	if err := (Auditor{}).VerifySnapshot(s.Snapshot(), s.RootHash()); err != nil {
		t.Fatalf("expected empty log to verify cleanly, got %v", err)
	}
}

func TestAppend_SequentialIndicesUnderLoad(t *testing.T) {
	s := NewAuditStore()
	for i := 0; i < 1000; i++ {
		r, err := s.Append("a", "A", "p")
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if r.Index != uint64(i+1) {
			t.Fatalf("expected sequential index %d, got %d", i+1, r.Index)
		}
	}
}

func TestRootHash_ChangesOnEveryAppend(t *testing.T) {
	s := NewAuditStore()
	h0 := s.RootHash()
	_, _ = s.Append("a", "A", "p")
	h1 := s.RootHash()
	if h0 == h1 {
		t.Fatal("root hash must change after append")
	}
}
