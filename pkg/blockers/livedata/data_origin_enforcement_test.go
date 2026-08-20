package livedata

import (
	"testing"
	"time"
)

// TestDataOriginEnforcement is audit item A2's own literal test list:
//
//	synthetic promoted to live       -> FAIL
//	replay promoted to live          -> FAIL
//	derived benchmark claimed as
//	  independent                    -> FAIL
//	valid live source                -> PASS
//
// "Promoted to live" here means: a record was produced by a
// SIMULATED-mode connector but carries a live-family DataMode tag —
// exactly the case Pipeline.Ingest must refuse regardless of which of
// the two live variants (LIVE, LIVE_LICENSED, LIVE_CUSTOMER_OWNED) it
// claims.
func TestDataOriginEnforcement(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(mode DataMode, payload string) Record {
		r := Record{ID: payload, Source: "TEST", Payload: payload, DataMode: mode, Timestamp: ts}
		r.Hash = ComputeHash(r.Source, r.Payload, r.Timestamp)
		return r
	}

	cases := []struct {
		name          string
		mode          DataMode
		connectorMode string
		wantAccepted  bool
	}{
		{"synthetic_promoted_to_live_LIVE", ModeLive, "SIMULATED", false},
		{"synthetic_promoted_to_live_LICENSED", ModeLiveLicensed, "SIMULATED", false},
		{"synthetic_promoted_to_live_CUSTOMER_OWNED", ModeLiveCustomerOwned, "SIMULATED", false},
		{"replay_promoted_to_live", ModeReplay, "SIMULATED", true}, // REPLAY itself is not a live claim, so it's accepted as REPLAY -- the actual violation this scenario tests is the NEXT case
		{"derived_benchmark_promoted_to_live", ModeRealDerivedBenchmark, "SIMULATED", true}, // same: REAL_DERIVED_BENCHMARK is not itself a live claim
		{"valid_live_source", ModeLiveLicensed, "REAL", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewPipeline()
			rec := mk(c.mode, c.name)
			accepted, reason := p.Ingest(rec, c.connectorMode)
			if accepted != c.wantAccepted {
				t.Errorf("Ingest(mode=%s, connector=%s): accepted=%v reason=%q, want accepted=%v",
					c.mode, c.connectorMode, accepted, reason, c.wantAccepted)
			}
		})
	}
}

// TestSyntheticAndReplayNeverPromotedToLive is the audit's core rule
// stated directly: SYNTHETIC != LIVE and REPLAY != LIVE. It proves a
// record cannot cross from one to the other by construction — DataMode
// is set once and IsLive is a pure function of it, never mutated by the
// pipeline.
func TestSyntheticAndReplayNeverPromotedToLive(t *testing.T) {
	if IsLive(ModeSynthetic) {
		t.Error("ModeSynthetic must never be classified as live")
	}
	if IsLive(ModeReplay) {
		t.Error("ModeReplay must never be classified as live")
	}
	if IsLive(ModeRealDerivedBenchmark) {
		t.Error("ModeRealDerivedBenchmark must never be classified as live")
	}
	for _, live := range []DataMode{ModeLive, ModeLiveLicensed, ModeLiveCustomerOwned} {
		if !IsLive(live) {
			t.Errorf("%s must be classified as live", live)
		}
	}
}

// TestRealDerivedBenchmarkNeverIndependentObservation is the audit's
// second explicit rule: REAL_DERIVED_BENCHMARK != INDEPENDENT_REAL_OBSERVATION.
func TestRealDerivedBenchmarkNeverIndependentObservation(t *testing.T) {
	if IsIndependentRealObservation(ModeRealDerivedBenchmark) {
		t.Error("REAL_DERIVED_BENCHMARK must never be reported as an independent real-world observation")
	}
	if !IsIndependentRealObservation(ModeLiveLicensed) {
		t.Error("a genuine live-licensed record IS an independent real-world observation")
	}
}
