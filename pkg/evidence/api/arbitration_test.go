package api

import (
	"reflect"
	"testing"

	"veriqo/pkg/moat/contradiction"
)

func obs(claimKey, sourceID, value string, reliability float64, tick uint64) contradiction.RawObservation {
	return contradiction.RawObservation{ClaimKey: claimKey, SourceID: sourceID, Value: value, SourceReliability: reliability, Tick: tick}
}

func TestFacadeObserveRawAndArbitrateClaim(t *testing.T) {
	f := newFacade()
	must(t, f.ObserveRaw(obs("VESSEL:IMO1|AIS_STATUS", "port-monitor", "OFF", 0.9, 5)))
	must(t, f.ObserveRaw(obs("VESSEL:IMO1|AIS_STATUS", "ais-vendor-a", "OFF", 0.8, 5)))

	tv, err := f.ArbitrateClaim("VESSEL:IMO1|AIS_STATUS", 10, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}
	if tv.Value != "OFF" {
		t.Fatalf("expected winner OFF, got %q", tv.Value)
	}
	if tv.Hash == "" {
		t.Fatal("expected a real hash-chained TruthVersion")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestFacadeArbitrationMatchesDirectEngineUse is the property that
// justifies calling this a genuine facade rather than a differently-
// behaving parallel mechanism: identical observations fed through the
// facade and through a raw contradiction.ArbitrationEngine directly
// must produce byte-identical TruthVersions (same winner, same
// confidence, same hash) -- proving ObserveRaw/ArbitrateClaim are real
// pass-throughs, not a reimplementation that happens to look similar.
func TestFacadeArbitrationMatchesDirectEngineUse(t *testing.T) {
	observations := []contradiction.RawObservation{
		obs("VESSEL:IMO2|BENEFICIAL_OWNER", "sanctions-registry", "SHELLCORP-Y", 0.97, 3),
		obs("VESSEL:IMO2|BENEFICIAL_OWNER", "aggregator", "SHELLCORP-X", 0.4, 4),
	}

	f := newFacade()
	direct := contradiction.NewArbitrationEngine()
	for _, o := range observations {
		must(t, f.ObserveRaw(o))
		must(t, direct.Observe(o))
	}

	viaFacade, err := f.ArbitrateClaim("VESSEL:IMO2|BENEFICIAL_OWNER", 10, 100)
	if err != nil {
		t.Fatalf("facade ArbitrateClaim: %v", err)
	}
	viaDirect, err := direct.ArbitrateClaim("VESSEL:IMO2|BENEFICIAL_OWNER", 10, 100)
	if err != nil {
		t.Fatalf("direct ArbitrateClaim: %v", err)
	}
	if !reflect.DeepEqual(viaFacade, viaDirect) {
		t.Fatalf("facade-mediated arbitration must be byte-identical to direct engine use:\nfacade=%+v\ndirect=%+v",
			viaFacade, viaDirect)
	}
}

func TestFacadeRawObservationsReturnsWhatWasObserved(t *testing.T) {
	f := newFacade()
	must(t, f.ObserveRaw(obs("c1", "s1", "v1", 0.9, 1)))
	must(t, f.ObserveRaw(obs("c1", "s2", "v2", 0.8, 2)))

	got := f.RawObservations("c1")
	if len(got) != 2 {
		t.Fatalf("expected 2 raw observations, got %d", len(got))
	}
}

func TestFacadeVerifyRawTruthLedgerDetectsNoTamperOnARealLedger(t *testing.T) {
	f := newFacade()
	must(t, f.ObserveRaw(obs("c1", "s1", "v1", 0.9, 1)))
	if _, err := f.ArbitrateClaim("c1", 5, 100); err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}
	if err := f.VerifyRawTruthLedger("c1"); err != nil {
		t.Fatalf("a genuine, untampered ledger must verify: %v", err)
	}
}

func TestFacadeRankHypothesesRanksEveryCandidate(t *testing.T) {
	f := newFacade()
	must(t, f.ObserveRaw(obs("c1", "s1", "PANAMA", 0.9, 1)))
	must(t, f.ObserveRaw(obs("c1", "s2", "LIBERIA", 0.5, 1)))

	ranked, err := f.RankHypotheses("c1", 5, 100)
	if err != nil {
		t.Fatalf("RankHypotheses: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked candidates, got %d", len(ranked))
	}
	if ranked[0].Value != "PANAMA" || ranked[0].Rank != 1 {
		t.Fatalf("expected PANAMA ranked first (higher reliability), got %+v", ranked[0])
	}
}

// TestFacadeArbitrationIsIndependentOfSubmitArbitrate proves the new
// ObserveRaw/ArbitrateClaim path and the pre-existing Submit/Arbitrate
// path (backed by the separate, ingest-only contradiction.Engine) do
// not interfere with each other -- adding this field changed nothing
// about existing behavior.
func TestFacadeArbitrationIsIndependentOfSubmitArbitrate(t *testing.T) {
	f := newFacade()
	// Exercise the pre-existing Submit path.
	e := ais("ais-vendor-a", "OFF", "prov", "up", 0.9, 5)
	if _, err := f.Submit(e); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// Exercise the new raw-arbitration path under an unrelated claim key.
	must(t, f.ObserveRaw(obs("unrelated-claim", "s1", "v1", 0.9, 1)))
	tv, err := f.ArbitrateClaim("unrelated-claim", 5, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}
	if tv.Value != "v1" {
		t.Fatalf("expected v1, got %q", tv.Value)
	}
	// The pre-existing Submit path's evidence must be completely
	// unaffected by the new path being exercised.
	stored := f.EvidenceFor("VESSEL:IMO4444444", "AIS_STATUS")
	if len(stored) != 1 || stored[0].Object != "OFF" {
		t.Fatalf("Submit-path evidence must be unaffected by ObserveRaw/ArbitrateClaim, got %+v", stored)
	}
}

func TestFacadeArbitrateClaimFailsClosedWithoutObservations(t *testing.T) {
	f := newFacade()
	if _, err := f.ArbitrateClaim("never-observed", 5, 100); err == nil {
		t.Fatal("expected an error arbitrating a claim with zero pending observations")
	}
}
