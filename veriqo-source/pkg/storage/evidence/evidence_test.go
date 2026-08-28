package evidence_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"unsafe"

	"veriqo/pkg/core"
	"veriqo/pkg/storage/evidence"
)

func newStore() *evidence.Store { return evidence.NewStore("test-store") }

func append1(t *testing.T, s *evidence.Store, kind, payload string) *evidence.Record {
	t.Helper()
	rec, err := s.Append(core.NewTraceID(), "tenant1", "domain1", kind, []byte(payload), nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	return rec
}

func TestStore_AppendAndGet(t *testing.T) {
	s := newStore()
	r := append1(t, s, "test.event", "payload1")
	if r.Seq != 1 {
		t.Fatalf("seq should be 1, got %d", r.Seq)
	}
	got, err := s.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != "payload1" {
		t.Errorf("wrong payload: %q", got.Payload)
	}
}

func TestStore_ChainLinking(t *testing.T) {
	s := newStore()
	r1 := append1(t, s, "event.a", "alpha")
	r2 := append1(t, s, "event.b", "beta")
	if r2.PrevHash != r1.Hash {
		t.Fatal("chain link broken: r2.PrevHash != r1.Hash")
	}
}

func TestStore_VerifyChain(t *testing.T) {
	s := newStore()
	for i := range 100 {
		append1(t, s, "event", fmt.Sprintf("payload-%d", i))
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("chain verify failed: %v", err)
	}
}

func TestStore_TamperDetection(t *testing.T) {
	s := newStore()
	r := append1(t, s, "event", "original")

	// Tamper: overwrite payload bytes in the live record.
	// We access internal fields via unsafe to simulate disk tampering.
	// In production the store is immutable; this proves Verify() catches it.
	tampered := (*record)(unsafe.Pointer(r))
	tampered.payload[0] ^= 0xFF

	err := s.Verify()
	if err == nil {
		t.Fatal("expected chain verification to fail after tampering")
	}
}

// record mirrors the unexported fields for tampering in tests.
// Only used to validate that hashes catch corruption.
type record struct {
	seq         uint64
	traceID     [16]byte
	tenantID    string
	domainID    string
	kind        string
	payload     []byte
	payloadHash [32]byte
	prevHash    [32]byte
	hash        [32]byte
}

func TestStore_SingleRecordVerify(t *testing.T) {
	s := newStore()
	r := append1(t, s, "test.verify", "hello")
	if err := r.Verify(); err != nil {
		t.Fatalf("record Verify failed: %v", err)
	}
}

func TestStore_NotFound(t *testing.T) {
	s := newStore()
	_, err := s.Get(99)
	var notFound *evidence.ErrNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_Query(t *testing.T) {
	s := newStore()
	append1(t, s, "risk.assessment", "r1")
	append1(t, s, "compliance.check", "c1")
	append1(t, s, "risk.assessment", "r2")

	results := s.Query(func(r *evidence.Record) bool {
		return r.Kind == "risk.assessment"
	})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestStore_Audit(t *testing.T) {
	s := newStore()
	for range 10 {
		append1(t, s, "ev", "data")
	}
	rep := s.Audit()
	if !rep.Intact {
		t.Fatalf("audit says not intact: %s", rep.Error)
	}
	if rep.TotalRecords != 10 {
		t.Errorf("expected 10 records, got %d", rep.TotalRecords)
	}
}

func TestStore_TenantIsolation(t *testing.T) {
	s := newStore()
	traceA := core.NewTraceID()
	traceB := core.NewTraceID()

	s.Append(traceA, "tenantA", "domain1", "event", []byte("data-a"), nil)
	s.Append(traceB, "tenantB", "domain1", "event", []byte("data-b"), nil)

	resultsA := s.Query(func(r *evidence.Record) bool { return r.TenantID == "tenantA" })
	resultsB := s.Query(func(r *evidence.Record) bool { return r.TenantID == "tenantB" })

	if len(resultsA) != 1 || len(resultsB) != 1 {
		t.Fatalf("tenant isolation broken: A=%d B=%d", len(resultsA), len(resultsB))
	}
}

func TestStore_ConcurrentAppend(t *testing.T) {
	s := newStore()
	var wg sync.WaitGroup
	n := 500
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Append(core.NewTraceID(), "tenant", "domain", "concurrent", []byte("data"), nil)
		}()
	}
	wg.Wait()
	if s.Len() != n {
		t.Fatalf("expected %d records, got %d", n, s.Len())
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("chain broken after concurrent appends: %v", err)
	}
}

// ─── Fuzz ─────────────────────────────────────────────────────────────────────

func FuzzEvidenceAppend(f *testing.F) {
	f.Add("kind1", []byte("payload"))
	f.Add("", []byte(""))
	f.Fuzz(func(t *testing.T, kind string, payload []byte) {
		s := evidence.NewStore("fuzz")
		if kind == "" {
			_, err := s.Append(core.NewTraceID(), "t", "d", kind, payload, nil)
			if err == nil {
				t.Fatal("expected error for empty kind")
			}
			return
		}
		rec, err := s.Append(core.NewTraceID(), "t", "d", kind, payload, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := rec.Verify(); err != nil {
			t.Fatalf("fuzz verify: %v", err)
		}
	})
}

// ─── Benchmarks ───────────────────────────────────────────────────────────────

func BenchmarkStore_Append(b *testing.B) {
	s := evidence.NewStore("bench")
	payload := []byte(`{"event":"benchmark","data":"payload"}`)
	b.ResetTimer()
	for range b.N {
		s.Append(core.NewTraceID(), "tenant", "domain", "bench.event", payload, nil)
	}
}

func BenchmarkStore_VerifyChain(b *testing.B) {
	s := evidence.NewStore("bench-verify")
	for range 10_000 {
		s.Append(core.NewTraceID(), "t", "d", "e", []byte("data"), nil)
	}
	b.ResetTimer()
	for range b.N {
		_ = s.Verify()
	}
}
