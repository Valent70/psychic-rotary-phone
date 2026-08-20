package contradiction

import "testing"

// TestBuildConflictReportRealArbitratedTimestampDisagreement runs a real
// claim through ArbitrateClaim (the worked example from the second audit
// document's item 4: AIS/port-system/NOR/SOF timestamps in disagreement)
// and confirms BuildConflictReport's output matches every field of the
// audit's own required shape.
func TestBuildConflictReportRealArbitratedTimestampDisagreement(t *testing.T) {
	eng := NewArbitrationEngine()
	eng.RegisterAuthority("AIS", 3)
	eng.RegisterAuthority("PORT_SYSTEM", 2)
	eng.RegisterAuthority("NOR", 1)
	eng.RegisterAuthority("SOF", 1)

	claimKey := "arrival:RANCHAN:AKONIKIEN"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(eng.Observe(RawObservation{ClaimKey: claimKey, SourceID: "AIS", Value: "1002", SourceReliability: 0.9, Tick: 1}))
	must(eng.Observe(RawObservation{ClaimKey: claimKey, SourceID: "PORT_SYSTEM", Value: "958", SourceReliability: 0.8, Tick: 1}))
	must(eng.Observe(RawObservation{ClaimKey: claimKey, SourceID: "NOR", Value: "951", SourceReliability: 0.7, Tick: 1}))
	must(eng.Observe(RawObservation{ClaimKey: claimKey, SourceID: "SOF", Value: "1007", SourceReliability: 0.6, Tick: 1}))

	tv, err := eng.ArbitrateClaim(claimKey, 1, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}

	report := BuildConflictReport("arrival", tv)
	if report.Event != "arrival" {
		t.Errorf("Event = %q, want arrival", report.Event)
	}
	if !report.Conflict {
		t.Fatal("expected a real conflict among 4 disagreeing timestamp sources")
	}
	if report.ConflictType != "TIMESTAMP_DISAGREEMENT" {
		t.Errorf("ConflictType = %q, want TIMESTAMP_DISAGREEMENT (all 4 values are numeric ticks)", report.ConflictType)
	}
	if report.Resolution != tv.Value {
		t.Errorf("Resolution = %q, want the real arbitrated winner %q", report.Resolution, tv.Value)
	}
	if report.Confidence != tv.Confidence {
		t.Errorf("Confidence = %v, want the real TruthVersion.Confidence %v", report.Confidence, tv.Confidence)
	}
	if len(report.Sources) != 4 {
		t.Errorf("expected all 4 sources accounted for (winning+losing), got %v", report.Sources)
	}
}

// TestBuildConflictReportNoConflictHasNoType proves a claim with a
// single, unopposed source is correctly reported as conflict=false with
// no conflict_type -- never a spurious classification.
func TestBuildConflictReportNoConflictHasNoType(t *testing.T) {
	eng := NewArbitrationEngine()
	claimKey := "single-source-claim"
	if err := eng.Observe(RawObservation{ClaimKey: claimKey, SourceID: "AIS", Value: "1000", SourceReliability: 0.9, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	tv, err := eng.ArbitrateClaim(claimKey, 1, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}
	report := BuildConflictReport("arrival", tv)
	if report.Conflict {
		t.Fatal("single-source claim must never report a conflict")
	}
	if report.ConflictType != "" {
		t.Errorf("ConflictType = %q, want empty when there is no conflict", report.ConflictType)
	}
	if report.HumanReviewRequired {
		t.Error("no conflict should never require human review")
	}
}

// TestBuildConflictReportLowConfidenceConflictRequiresHumanReview proves
// the human_review_required rule actually fires when a real conflict's
// winning confidence is low, and does not fire for a high-confidence
// real conflict.
func TestBuildConflictReportLowConfidenceConflictRequiresHumanReview(t *testing.T) {
	// Two sources, near-equal reliability -- winner confidence should
	// land near 0.5, well under the 0.75 floor.
	eng := NewArbitrationEngine()
	claimKey := "close-call"
	if err := eng.Observe(RawObservation{ClaimKey: claimKey, SourceID: "A", Value: "100", SourceReliability: 0.51, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Observe(RawObservation{ClaimKey: claimKey, SourceID: "B", Value: "200", SourceReliability: 0.49, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	tv, err := eng.ArbitrateClaim(claimKey, 1, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}
	report := BuildConflictReport("close-call", tv)
	if !report.Conflict {
		t.Fatal("expected a real conflict between two disagreeing sources")
	}
	if tv.Confidence >= humanReviewConfidenceFloor {
		t.Fatalf("test setup assumption violated: winner confidence %v is not below the floor %v", tv.Confidence, humanReviewConfidenceFloor)
	}
	if !report.HumanReviewRequired {
		t.Errorf("expected human_review_required=true for a low-confidence (%v) real conflict", tv.Confidence)
	}
}
