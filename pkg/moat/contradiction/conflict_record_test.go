package contradiction

import "testing"

// TestConflictRecordMatchesTheMandatesOwnWorkedExample reproduces
// PART 5's literal example: "AIS says arrival = T1, NOR says
// arrival = T2, SOF says arrival = T3" -- three genuinely
// disagreeing sources on one claim, none of which should be dropped.
func TestConflictRecordMatchesTheMandatesOwnWorkedExample(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "VESSEL:1|ARRIVAL_TIME"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "ais", Value: "T1", SourceReliability: 0.6, Tick: 100}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "nor", Value: "T2", SourceReliability: 0.6, Tick: 102}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "sof", Value: "T3", SourceReliability: 0.6, Tick: 105}))

	rec, err := e.ConflictRecords(claim, 105, 1000)
	must(t, err)

	if len(rec.Sources) != 3 || len(rec.Values) != 3 {
		t.Fatalf("expected all 3 sources/values preserved, got sources=%v values=%v", rec.Sources, rec.Values)
	}
	wantPairs := map[string]string{"ais": "T1", "nor": "T2", "sof": "T3"}
	for i, src := range rec.Sources {
		if wantPairs[src] != rec.Values[i] {
			t.Fatalf("source %s: expected value %s, got %s", src, wantPairs[src], rec.Values[i])
		}
	}
	if rec.TimeDelta != 5 { // 105 - 100
		t.Fatalf("expected time_delta=5, got %d", rec.TimeDelta)
	}
	// Three equally-reliable, mutually-exclusive sources: no candidate
	// reaches the 2/3 confidence bar, so this must be CONTESTED and
	// flagged for human review -- NOT silently arbitrated away.
	if rec.ResolutionState != ResolutionContested {
		t.Fatalf("expected CONTESTED, got %s", rec.ResolutionState)
	}
	if !rec.HumanReviewRequired {
		t.Fatal("expected human_review_required=true for a genuine three-way disagreement")
	}
	if rec.ConflictID == "" {
		t.Fatal("expected a non-empty conflict_id")
	}
}

func TestConflictRecordUncontestedWhenAllSourcesAgree(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "VESSEL:2|FLAG_STATE"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "ais-a", Value: "PANAMA", SourceReliability: 0.9, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "ais-b", Value: "PANAMA", SourceReliability: 0.85, Tick: 1}))

	rec, err := e.ConflictRecords(claim, 1, 100)
	must(t, err)
	if rec.ResolutionState != ResolutionUncontested {
		t.Fatalf("expected UNCONTESTED, got %s", rec.ResolutionState)
	}
	if rec.HumanReviewRequired {
		t.Fatal("agreement between sources must never require human review")
	}
	if len(rec.Sources) != 2 {
		t.Fatalf("expected both agreeing sources preserved, got %v", rec.Sources)
	}
}

func TestConflictRecordArbitratedWhenWinnerIsConfidentButDisagreementExisted(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "VESSEL:3|FLAG_STATE"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "ais-a", Value: "PANAMA", SourceReliability: 0.9, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "ais-b", Value: "PANAMA", SourceReliability: 0.9, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "ais-c", Value: "PANAMA", SourceReliability: 0.9, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "stale-scrape", Value: "LIBERIA", SourceReliability: 0.2, Tick: 1}))

	rec, err := e.ConflictRecords(claim, 1, 1000)
	must(t, err)
	if rec.ResolutionState != ResolutionArbitrated {
		t.Fatalf("expected ARBITRATED (high-confidence winner despite a weak dissent), got %s (confidence=%f)", rec.ResolutionState, rec.Confidence)
	}
	if rec.HumanReviewRequired {
		t.Fatal("a confident arbitration should not require human review")
	}
	// The dissenting source must still be present -- ARBITRATED is not
	// "the conflict never happened".
	found := false
	for i, s := range rec.Sources {
		if s == "stale-scrape" && rec.Values[i] == "LIBERIA" {
			found = true
		}
	}
	if !found {
		t.Fatal("ARBITRATED must still preserve the losing/dissenting source, not erase it")
	}
}

// TestConflictRecordPreservesEverySourceEvenTheLosingOnes is the
// mechanical anti-false-green proof for PART 5's "do not silently
// resolve conflicts": even in the CONTESTED case where the record's
// ResolutionState clearly names a winner via Confidence, ALL original
// sources -- including every one that lost -- remain enumerated.
func TestConflictRecordPreservesEverySourceEvenTheLosingOnes(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "SHIPMENT:9|DESTINATION"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "customer-record", Value: "ROTTERDAM", SourceReliability: 0.95, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "port-call-log", Value: "ANTWERP", SourceReliability: 0.4, Tick: 3}))

	rec, err := e.ConflictRecords(claim, 3, 1000)
	must(t, err)
	if len(rec.Sources) != 2 {
		t.Fatalf("expected both sources preserved regardless of resolution, got %v", rec.Sources)
	}
}

func TestConflictRecordsFailsClosedWithNoObservations(t *testing.T) {
	e := NewArbitrationEngine()
	if _, err := e.ConflictRecords("nonexistent", 1, 10); err == nil {
		t.Fatal("expected an error for a claim with no pending observations")
	}
}

func TestConflictIDIsDeterministicAndChangesWithContent(t *testing.T) {
	e1 := NewArbitrationEngine()
	claim := "VESSEL:4|FLAG_STATE"
	must(t, e1.Observe(RawObservation{ClaimKey: claim, SourceID: "a", Value: "X", SourceReliability: 0.9, Tick: 1}))
	rec1, err := e1.ConflictRecords(claim, 1, 100)
	must(t, err)

	e2 := NewArbitrationEngine()
	must(t, e2.Observe(RawObservation{ClaimKey: claim, SourceID: "a", Value: "X", SourceReliability: 0.9, Tick: 1}))
	rec2, err := e2.ConflictRecords(claim, 1, 100)
	must(t, err)

	if rec1.ConflictID != rec2.ConflictID {
		t.Fatal("identical inputs must produce the same conflict_id")
	}

	must(t, e2.Observe(RawObservation{ClaimKey: claim, SourceID: "b", Value: "Y", SourceReliability: 0.9, Tick: 1}))
	rec3, err := e2.ConflictRecords(claim, 1, 100)
	must(t, err)
	if rec3.ConflictID == rec1.ConflictID {
		t.Fatal("adding a new disagreeing source must change the conflict_id")
	}
}
