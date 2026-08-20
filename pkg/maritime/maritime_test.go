package maritime

import (
	"context"
	"testing"
)

// TestAllAdaptersImplementInterface is a compile-time-backed runtime
// check that every one of the 5 required adapters satisfies
// MaritimeEvidenceSource -- the var _ assertions in each file already
// enforce this at compile time; this test additionally exercises the
// interface methods through the interface value itself, not the
// concrete type, so a subtle method-set mismatch (e.g. a pointer vs
// value receiver slip) would still be caught even if a var _ assertion
// were ever accidentally removed.
func TestAllAdaptersImplementInterface(t *testing.T) {
	sources := map[string]MaritimeEvidenceSource{
		"AIS":            NewAISAdapterFixture(),
		"PortCall":       NewPortCallAdapterFixture(),
		"NOR":            NewNORAdapterFixture(),
		"SOF":            NewSOFAdapterFixture(),
		"CustomerClaims": NewCustomerClaimsAdapterFixture(),
	}
	ctx := context.Background()
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			id, err := src.SourceID(ctx)
			if err != nil || id == "" {
				t.Fatalf("SourceID: err=%v id=%q", err, id)
			}
			caps, err := src.Capabilities(ctx)
			if err != nil {
				t.Fatalf("Capabilities: %v", err)
			}
			if !caps.SupportsFixture {
				t.Fatalf("expected SupportsFixture=true for a fixture-mode adapter, got %+v", caps)
			}
			if caps.SupportsRealFile || caps.SupportsRealAPI {
				t.Fatalf("fixture-mode constructor must not claim real-mode support: %+v", caps)
			}
			evidence, err := src.Fetch(ctx, FetchRequest{})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if len(evidence) == 0 {
				t.Fatal("expected at least one fixture Evidence record")
			}
			for _, e := range evidence {
				if e.EventType == "" || e.Hash == "" || e.SourceID == "" {
					t.Fatalf("incomplete Evidence record: %+v", e)
				}
			}
		})
	}
}

func TestAISAdapterFetchIsDeterministic(t *testing.T) {
	ctx := context.Background()
	a := NewAISAdapterFixture()
	r1, err := a.Fetch(ctx, FetchRequest{VesselID: "RWC-001-CANDIDATE-A"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	r2, err := a.Fetch(ctx, FetchRequest{VesselID: "RWC-001-CANDIDATE-A"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(r1) != len(r2) {
		t.Fatalf("non-deterministic record count: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i].Hash != r2[i].Hash {
			t.Fatalf("non-deterministic hash at index %d: %s vs %s", i, r1[i].Hash, r2[i].Hash)
		}
	}
}

func TestAISAdapterArrivalTickMatchesRWC001Corpus(t *testing.T) {
	ctx := context.Background()
	a := NewAISAdapterFixture()
	records, err := a.Fetch(ctx, FetchRequest{VesselID: "RWC-001-CANDIDATE-A", PortID: "AKONIKIEN"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	tick, ok := ArrivalTick(records)
	if !ok {
		t.Fatal("expected an arrival record for RWC-001-CANDIDATE-A at AKONIKIEN")
	}
	// pkg/rwc/rwc001_test.go hardcodes tick=1 for every RWC-001 candidate
	// (BuildRWC001Case(c, 1)) -- this fixture's arrival tick must match
	// that exact value, per this package's own doc comment on
	// aisArrivalTick, so wiring it into case construction cannot change
	// the number, only where it structurally comes from.
	if tick != 1 {
		t.Fatalf("expected AIS fixture arrival tick to match RWC-001's own hardcoded tick=1, got %d", tick)
	}
}

func TestAISAdapterFilters(t *testing.T) {
	ctx := context.Background()
	a := NewAISAdapterFixture()
	records, err := a.Fetch(ctx, FetchRequest{VesselID: "NO-SUCH-VESSEL"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected zero records for an unknown vessel, got %d", len(records))
	}
}

func TestRealModeIsAnHonestStub(t *testing.T) {
	// Every adapter's mode is fixed to SIMULATED by its fixture
	// constructor today (no real-mode constructor exists in this
	// package), so the honest "not implemented" path is exercised
	// directly through the internal mode field rather than a
	// non-existent public constructor -- this proves the guard is real
	// code, not dead code, without inventing a real-mode entry point
	// this environment cannot back.
	a := &AISAdapter{mode: "REAL_API"}
	_, err := a.Fetch(context.Background(), FetchRequest{})
	if err == nil {
		t.Fatal("expected a REAL_API-mode Fetch to fail honestly, got nil error")
	}
}
