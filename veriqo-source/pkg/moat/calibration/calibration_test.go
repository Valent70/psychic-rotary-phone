package calibration

import (
	"testing"

	"veriqo/pkg/kernel/intent"
	"veriqo/pkg/moat/decision"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecordGroundTruthRejectsUnregisteredSource(t *testing.T) {
	e := NewEngine()
	must(t, e.RecordPrediction(Prediction{ClaimKey: "C1", Value: "PANAMA", Confidence: 0.9, Tick: 1}))
	_, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "C1", Value: "PANAMA", SourceID: "port-authority-1", Tick: 2})
	if err != ErrSourceNotIndependent {
		t.Fatalf("expected ErrSourceNotIndependent, got %v", err)
	}
}

// TestRecordGroundTruthRejectsArbitrationInputSource is the load-bearing
// test for PRIORITAS #1: it proves the self-reinforcing loop is
// structurally blocked, not just discouraged by convention.
func TestRecordGroundTruthRejectsArbitrationInputSource(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("ais-vendor-3") // independent in general...
	must(t, e.RecordPrediction(Prediction{
		ClaimKey: "C1", Value: "PANAMA", Confidence: 0.9,
		ArbitrationSourceIDs: []string{"ais-vendor-1", "ais-vendor-3"}, // ...but WAS an input to THIS arbitration
		Tick:                 1,
	}))
	_, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "C1", Value: "PANAMA", SourceID: "ais-vendor-3", Tick: 2})
	if err != ErrSourceIsArbitrationInput {
		t.Fatalf("expected ErrSourceIsArbitrationInput, got %v", err)
	}
}

func TestRecordGroundTruthRequiresPendingPrediction(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("port-authority-1")
	_, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "NOPE", Value: "X", SourceID: "port-authority-1", Tick: 1})
	if err != ErrNoPrediction {
		t.Fatalf("expected ErrNoPrediction, got %v", err)
	}
}

func TestRecordGroundTruthMatchedComputesZeroBrierForConfidentCorrectCall(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("port-authority-1")
	must(t, e.RecordPrediction(Prediction{
		ClaimKey: "C1", Value: "PANAMA", Confidence: 1.0,
		ArbitrationSourceIDs: []string{"ais-1", "ais-2"}, Tick: 1,
	}))
	rec, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "C1", Value: "PANAMA", SourceID: "port-authority-1", Tick: 2})
	must(t, err)
	if !rec.Matched {
		t.Fatalf("expected matched=true")
	}
	if rec.BrierScore != 0 {
		t.Fatalf("expected brier=0 for confident correct call, got %f", rec.BrierScore)
	}
	if rec.Index != 1 || rec.PrevHash != "" {
		t.Fatalf("expected first ledger entry, got %+v", rec)
	}
}

func TestRecordGroundTruthMismatchComputesMaxBrierForConfidentWrongCall(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("port-authority-1")
	must(t, e.RecordPrediction(Prediction{ClaimKey: "C1", Value: "PANAMA", Confidence: 1.0, Tick: 1}))
	rec, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "C1", Value: "LIBERIA", SourceID: "port-authority-1", Tick: 2})
	must(t, err)
	if rec.Matched {
		t.Fatalf("expected matched=false")
	}
	if rec.BrierScore != 1.0 {
		t.Fatalf("expected brier=1.0 for confident wrong call, got %f", rec.BrierScore)
	}
}

func TestPredictionConsumedAfterGroundTruth(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("s")
	must(t, e.RecordPrediction(Prediction{ClaimKey: "C1", Value: "A", Confidence: 0.7, Tick: 1}))
	if len(e.PendingClaims()) != 1 {
		t.Fatalf("expected 1 pending claim")
	}
	_, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "C1", Value: "A", SourceID: "s", Tick: 2})
	must(t, err)
	if len(e.PendingClaims()) != 0 {
		t.Fatalf("expected prediction consumed after ground truth")
	}
	// second ground truth for the same claim without a new prediction fails
	_, err = e.RecordGroundTruth(GroundTruth{ClaimKey: "C1", Value: "A", SourceID: "s", Tick: 3})
	if err != ErrNoPrediction {
		t.Fatalf("expected ErrNoPrediction on reuse, got %v", err)
	}
}

func TestLedgerHashChainVerifies(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("s")
	for i, claim := range []string{"C1", "C2", "C3"} {
		must(t, e.RecordPrediction(Prediction{ClaimKey: claim, Value: "V", Confidence: 0.6, Tick: uint64(i)}))
		_, err := e.RecordGroundTruth(GroundTruth{ClaimKey: claim, Value: "V", SourceID: "s", Tick: uint64(i) + 10})
		must(t, err)
	}
	must(t, e.VerifyLedger())
	if len(e.Ledger()) != 3 {
		t.Fatalf("expected 3 ledger entries, got %d", len(e.Ledger()))
	}
}

func TestLedgerHashChainDetectsTamper(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("s")
	must(t, e.RecordPrediction(Prediction{ClaimKey: "C1", Value: "V", Confidence: 0.6, Tick: 1}))
	_, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "C1", Value: "V", SourceID: "s", Tick: 2})
	must(t, err)
	e.ledger[0].BrierScore = 0.99 // tamper directly, bypassing the API
	if err := e.VerifyLedger(); err == nil {
		t.Fatalf("expected VerifyLedger to detect tamper")
	}
}

func TestMulticlassBrierRewardsConcentrationOnActual(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("s")
	dist := map[string]float64{"PANAMA": 0.6, "LIBERIA": 0.3, "MARSHALL_ISLANDS": 0.1}
	must(t, e.RecordPrediction(Prediction{ClaimKey: "C1", Value: "PANAMA", Confidence: 0.6, Distribution: dist, Tick: 1}))
	rec, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "C1", Value: "PANAMA", SourceID: "s", Tick: 2})
	must(t, err)
	// (0.6-1)^2 + (0.3-0)^2 + (0.1-0)^2 = 0.16+0.09+0.01 = 0.26
	if diff := rec.BrierScore - 0.26; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected multiclass brier ~0.26, got %f", rec.BrierScore)
	}
}

func TestRecordPredictionRejectsInvalidDistribution(t *testing.T) {
	e := NewEngine()
	err := e.RecordPrediction(Prediction{ClaimKey: "C1", Value: "A", Distribution: map[string]float64{"A": 0.5, "B": 0.9}})
	if err != ErrInvalidDistribution {
		t.Fatalf("expected ErrInvalidDistribution, got %v", err)
	}
}

func TestSummarizeEmptyIsWorstCase(t *testing.T) {
	ev := Summarize(nil)
	if ev["calibration_accuracy"] != 0 || ev["calibration_brier"] != 1 {
		t.Fatalf("expected worst-case evidence for empty records, got %+v", ev)
	}
}

func TestSummarizeAveragesCorrectly(t *testing.T) {
	records := []Record{
		{Matched: true, BrierScore: 0.0},
		{Matched: false, BrierScore: 1.0},
	}
	ev := Summarize(records)
	if ev["calibration_accuracy"] != 0.5 {
		t.Fatalf("expected accuracy 0.5, got %f", ev["calibration_accuracy"])
	}
	if ev["calibration_brier"] != 0.5 {
		t.Fatalf("expected mean brier 0.5, got %f", ev["calibration_brier"])
	}
}

// --- bridges ---

func TestGroundTruthFromIntentVerdictProducesUsableClassC(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("independent-auditor")
	must(t, e.RecordPrediction(Prediction{
		ClaimKey: "vessel-1-action", Value: "FLAG", Confidence: 0.8,
		ArbitrationSourceIDs: []string{"ais-1"}, Tick: 1,
	}))

	verifier, err := intent.NewVerifier()
	must(t, err)
	stmt := intent.Statement{ID: "i1", ActorID: "vessel-1", Subject: "vessel-1", ExpectedAction: decision.Action("FLAG"), ExpectedRisk: 0.7, Tick: 1}
	obs := intent.Observation{ObservedAction: decision.Action("FLAG"), ObservedRisk: 0.75, Tick: 5}
	verdict, err := verifier.Verify(stmt, obs)
	must(t, err)

	gt := GroundTruthFromIntentVerdict(verdict, string(obs.ObservedAction), "independent-auditor", "vessel-1-action")
	rec, err := e.RecordGroundTruth(gt)
	must(t, err)
	if !rec.Matched {
		t.Fatalf("expected matched prediction from intent-derived ground truth")
	}
}

func TestPredictionFromHypothesesPreservesDistribution(t *testing.T) {
	cands := []HypothesisCandidate{
		{Value: "PANAMA", Confidence: 0.6, Rank: 1},
		{Value: "LIBERIA", Confidence: 0.4, Rank: 2},
	}
	p := PredictionFromHypotheses("C1", cands, []string{"ais-1"}, 3)
	if p.Value != "PANAMA" || p.Confidence != 0.6 {
		t.Fatalf("expected top candidate as point estimate, got %+v", p)
	}
	if len(p.Distribution) != 2 || p.Distribution["LIBERIA"] != 0.4 {
		t.Fatalf("expected full distribution preserved, got %+v", p.Distribution)
	}
}

// TestCreditWeights_ProportionalToContribution is v7.8 Priority B: a
// source supplying 80% of arbitration weight must be credited far more
// than one supplying 5%, not split equally.
func TestCreditWeights_ProportionalToContribution(t *testing.T) {
	sources := []string{"A", "B", "C"}
	contrib := map[string]float64{"A": 0.8, "B": 0.15, "C": 0.05}
	w := creditWeights(sources, contrib)
	if w["A"] <= w["B"] || w["B"] <= w["C"] {
		t.Fatalf("expected weights ordered A>B>C by contribution, got %+v", w)
	}
	sum := w["A"] + w["B"] + w["C"]
	if diff := sum - 1.0; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("weights should sum to 1.0, got %v", sum)
	}
	if diff := w["A"] - 0.8; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("A's weight should equal its contribution 0.8, got %v", w["A"])
	}
}

func TestCreditWeights_FallsBackToEqualSplitWhenNoContributions(t *testing.T) {
	sources := []string{"A", "B"}
	w := creditWeights(sources, nil)
	if w["A"] != 0.5 || w["B"] != 0.5 {
		t.Fatalf("expected equal-split fallback 0.5/0.5, got %+v", w)
	}
}
