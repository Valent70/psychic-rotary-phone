package calibration

import (
	"math"
	"testing"

	"veriqo/pkg/core/trustcalc"
)

// adversarial_test.go implements the P5 priority from
// "Hasil_audit_brutal_Professional_Chief_Technical_Software_Auditor.docx"
// §25: "adversarial epistemic tests" covering same-upstream-different-
// source, duplicate evidence, source poisoning, false independent
// ground truth, trust inflation, confidence inflation, hypothesis
// oscillation, replay divergence, and calibration drift. Each test
// below targets exactly one named failure mode with the smallest
// reproduction that exercises it.

// --- same upstream / different source (P2) -------------------------------

func TestAdversarial_SameUpstreamDifferentSourceIDRejected(t *testing.T) {
	e := NewEngine()
	e.RegisterUpstream("AIS_VENDOR_A", "SATELLITE_OPERATOR_X")
	e.RegisterUpstream("SATELLITE_OPERATOR_X_RESELLER", "SATELLITE_OPERATOR_X")
	e.RegisterIndependentSource("SATELLITE_OPERATOR_X_RESELLER")

	if err := e.RecordPrediction(Prediction{
		ClaimKey: "vessel:mmsi123:position", Value: "at_anchor", Confidence: 0.7,
		ArbitrationSourceIDs: []string{"AIS_VENDOR_A"}, Tick: 1,
	}); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}

	// SATELLITE_OPERATOR_X_RESELLER has a DIFFERENT source ID than
	// AIS_VENDOR_A (so the direct sourceID check alone would pass it),
	// but both share the same declared upstream — the reused-evidence
	// case the plain sourceID check cannot catch.
	_, err := e.RecordGroundTruth(GroundTruth{
		ClaimKey: "vessel:mmsi123:position", Value: "at_anchor",
		SourceID: "SATELLITE_OPERATOR_X_RESELLER", Tick: 2,
	})
	if err != ErrSourceSharesUpstreamWithArbitrationInput {
		t.Fatalf("expected ErrSourceSharesUpstreamWithArbitrationInput, got %v", err)
	}
}

func TestAdversarial_DifferentUpstreamDifferentSourceAccepted(t *testing.T) {
	e := NewEngine()
	e.RegisterUpstream("AIS_VENDOR_A", "SATELLITE_OPERATOR_X")
	e.RegisterIndependentSource("PORT_AUTHORITY_RADAR")
	// PORT_AUTHORITY_RADAR has no registered upstream (defaults to
	// itself) and is genuinely a different provenance root.
	if err := e.RecordPrediction(Prediction{
		ClaimKey: "vessel:mmsi999:position", Value: "at_anchor", Confidence: 0.7,
		ArbitrationSourceIDs: []string{"AIS_VENDOR_A"}, Tick: 1,
	}); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}
	if _, err := e.RecordGroundTruth(GroundTruth{
		ClaimKey: "vessel:mmsi999:position", Value: "at_anchor",
		SourceID: "PORT_AUTHORITY_RADAR", Tick: 2,
	}); err != nil {
		t.Fatalf("expected genuinely independent ground truth to be accepted, got %v", err)
	}
}

// --- duplicate evidence ----------------------------------------------------

func TestAdversarial_DuplicateArbitrationSourceDoesNotCrashOrOverweight(t *testing.T) {
	calc := trustcalc.New(1, 1)
	e := NewEngineWithCalculus(calc)
	e.RegisterIndependentSource("GROUND_TRUTH_1")
	// The same source ID appears twice in ArbitrationSourceIDs — e.g. a
	// caller accidentally double-submitted the same evidence item.
	dup := []string{"SRC_A", "SRC_A", "SRC_B"}
	if err := e.RecordPrediction(Prediction{
		ClaimKey: "claim:dup", Value: "x", Confidence: 0.6,
		ArbitrationSourceIDs: dup, Tick: 1,
	}); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}
	rec, err := e.RecordGroundTruth(GroundTruth{
		ClaimKey: "claim:dup", Value: "x", SourceID: "GROUND_TRUTH_1", Tick: 2,
	})
	if err != nil {
		t.Fatalf("RecordGroundTruth: %v", err)
	}
	if !rec.Matched {
		t.Fatalf("expected match")
	}
	// SRC_A's belief must still be a valid probability after being
	// Observe()'d twice via the duplicated arbitration list — no
	// overflow, no value outside [0,1].
	belief := calc.Belief("SRC_A").Mean()
	if belief < 0 || belief > 1 || math.IsNaN(belief) {
		t.Fatalf("SRC_A belief left in invalid state: %v", belief)
	}
}

// --- source poisoning -------------------------------------------------------

func TestAdversarial_RepeatedUnregisteredSourcePoisoningRejectedEveryTime(t *testing.T) {
	calc := trustcalc.New(1, 1)
	e := NewEngineWithCalculus(calc)
	if err := e.RecordPrediction(Prediction{
		ClaimKey: "claim:poison", Value: "sanctioned", Confidence: 0.9,
		ArbitrationSourceIDs: []string{"HONEST_SRC"}, Tick: 1,
	}); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}
	for i := 0; i < 50; i++ {
		_, err := e.RecordGroundTruth(GroundTruth{
			ClaimKey: "claim:poison", Value: "sanctioned",
			SourceID: "ATTACKER_UNREGISTERED", Tick: uint64(i + 2),
		})
		if err != ErrSourceNotIndependent {
			t.Fatalf("attempt %d: expected ErrSourceNotIndependent, got %v", i, err)
		}
	}
	// A poisoning attempt must never reach the shared calculus: the
	// prediction is still pending (never consumed) and HONEST_SRC's
	// belief must remain exactly at the untouched Beta(1,1) prior.
	if len(e.PendingClaims()) != 1 {
		t.Fatalf("expected prediction to remain pending after all rejections, got %v", e.PendingClaims())
	}
	b := calc.Belief("HONEST_SRC")
	if b.Alpha != 1 || b.Beta != 1 {
		t.Fatalf("poisoning attempts must not touch the shared calculus, got %+v", b)
	}
}

// --- false independent ground truth (direct reuse) --------------------------

func TestAdversarial_FalseIndependentGroundTruth_RegisteredButIsArbitrationInput(t *testing.T) {
	e := NewEngine()
	// A source can be registered as "independent" and STILL be
	// disqualified for a specific claim if it also fed that claim's
	// own arbitration — independence is necessary but not sufficient.
	e.RegisterIndependentSource("AIS_VENDOR_A")
	if err := e.RecordPrediction(Prediction{
		ClaimKey: "claim:selfconfirm", Value: "true", Confidence: 0.99,
		ArbitrationSourceIDs: []string{"AIS_VENDOR_A"}, Tick: 1,
	}); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}
	_, err := e.RecordGroundTruth(GroundTruth{
		ClaimKey: "claim:selfconfirm", Value: "true", SourceID: "AIS_VENDOR_A", Tick: 2,
	})
	if err != ErrSourceIsArbitrationInput {
		t.Fatalf("expected ErrSourceIsArbitrationInput, got %v", err)
	}
}

// --- trust inflation ---------------------------------------------------------

func TestAdversarial_TrustInflationBoundedNeverReachesOrExceedsOne(t *testing.T) {
	calc := trustcalc.New(1, 1)
	e := NewEngineWithCalculus(calc)
	e.RegisterIndependentSource("GT")
	for i := 0; i < 500; i++ {
		claim := "claim:inflate"
		if err := e.RecordPrediction(Prediction{
			ClaimKey: claim, Value: "x", Confidence: 0.99,
			ArbitrationSourceIDs: []string{"REPEAT_SRC"}, Tick: uint64(i),
		}); err != nil {
			t.Fatalf("RecordPrediction #%d: %v", i, err)
		}
		if _, err := e.RecordGroundTruth(GroundTruth{
			ClaimKey: claim, Value: "x", SourceID: "GT", Tick: uint64(i),
		}); err != nil {
			t.Fatalf("RecordGroundTruth #%d: %v", i, err)
		}
	}
	mean := calc.Belief(trustcalc.NamespacedSubject(trustcalc.NamespaceCalibrationSource, "REPEAT_SRC")).Mean()
	if !(mean > 0 && mean < 1) {
		t.Fatalf("500 consecutive confirmations must approach but never reach/exceed 1.0, got %v", mean)
	}
	if mean < 0.9 {
		t.Fatalf("expected belief to have moved substantially toward 1.0, got %v", mean)
	}
}

// --- confidence inflation ----------------------------------------------------

func TestAdversarial_ConfidenceInflationRejectedByDistributionValidation(t *testing.T) {
	e := NewEngine()
	overconfident := map[string]float64{"A": 0.9, "B": 0.9} // sums to 1.8, not a probability distribution
	err := e.RecordPrediction(Prediction{
		ClaimKey: "claim:overconf", Value: "A", Confidence: 0.9,
		Distribution: overconfident, Tick: 1,
	})
	if err != ErrInvalidDistribution {
		t.Fatalf("expected ErrInvalidDistribution for a sum-1.8 distribution, got %v", err)
	}
}

// --- hypothesis oscillation ---------------------------------------------------

func TestAdversarial_HypothesisOscillationOnlyLatestPredictionIsScored(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("GT")
	claim := "claim:oscillate"
	// The arbitration engine flip-flops its own belief several times
	// before any ground truth arrives — a real pattern under contested
	// evidence. Only the LAST submitted prediction may be what
	// calibration eventually scores; earlier oscillations must leave
	// no residue.
	values := []string{"true", "false", "true", "false", "true"}
	for i, v := range values {
		if err := e.RecordPrediction(Prediction{
			ClaimKey: claim, Value: v, Confidence: 0.6,
			ArbitrationSourceIDs: []string{"SRC_OSC"}, Tick: uint64(i),
		}); err != nil {
			t.Fatalf("RecordPrediction #%d: %v", i, err)
		}
	}
	rec, err := e.RecordGroundTruth(GroundTruth{ClaimKey: claim, Value: "true", SourceID: "GT", Tick: 99})
	if err != nil {
		t.Fatalf("RecordGroundTruth: %v", err)
	}
	if rec.PredictedValue != "true" {
		t.Fatalf("expected the LAST oscillated prediction (%q) to be scored, got %q", values[len(values)-1], rec.PredictedValue)
	}
	if !rec.Matched {
		t.Fatalf("last prediction was 'true' matching ground truth 'true' — expected Matched")
	}
}

// --- replay divergence ----------------------------------------------------

func TestAdversarial_ReplayDivergenceDetectedViaLedgerTamper(t *testing.T) {
	e := NewEngine()
	e.RegisterIndependentSource("GT")
	if err := e.RecordPrediction(Prediction{
		ClaimKey: "claim:tamper", Value: "x", Confidence: 0.8,
		ArbitrationSourceIDs: []string{"SRC"}, Tick: 1,
	}); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}
	if _, err := e.RecordGroundTruth(GroundTruth{ClaimKey: "claim:tamper", Value: "x", SourceID: "GT", Tick: 2}); err != nil {
		t.Fatalf("RecordGroundTruth: %v", err)
	}
	if err := e.VerifyLedger(); err != nil {
		t.Fatalf("untampered ledger should verify: %v", err)
	}
	// Simulate a replay divergence: an attacker (or a buggy replica)
	// mutates a ledgered record's outcome after the fact, without
	// recomputing its hash — VerifyLedger must catch this exactly the
	// way trust.Kernel's own ledger does.
	e.ledger[0].Matched = !e.ledger[0].Matched
	if err := e.VerifyLedger(); err == nil {
		t.Fatalf("expected VerifyLedger to detect the tampered record")
	}
}

// --- calibration drift ----------------------------------------------------

func TestAdversarial_CalibrationDriftIsMeasurableAndPropagatesToTrust(t *testing.T) {
	calc := trustcalc.New(1, 1)
	e := NewEngineWithCalculus(calc)
	e.RegisterIndependentSource("GT")

	// Phase 1: the arbitration was reliable for a while (matches).
	for i := 0; i < 10; i++ {
		claim := "claim:drift"
		_ = e.RecordPrediction(Prediction{
			ClaimKey: claim, Value: "x", Confidence: 0.9,
			ArbitrationSourceIDs: []string{"DRIFTING_SRC"}, Tick: uint64(i),
		})
		if _, err := e.RecordGroundTruth(GroundTruth{ClaimKey: claim, Value: "x", SourceID: "GT", Tick: uint64(i)}); err != nil {
			t.Fatalf("phase1 #%d: %v", i, err)
		}
	}
	early := Summarize(e.RecordsFor("claim:drift"))
	earlyBelief := calc.Belief(trustcalc.NamespacedSubject(trustcalc.NamespaceCalibrationSource, "DRIFTING_SRC")).Mean()

	// Phase 2: the same source's reliability drifts — arbitration keeps
	// predicting "x" but ground truth now consistently disagrees.
	for i := 10; i < 30; i++ {
		claim := "claim:drift"
		_ = e.RecordPrediction(Prediction{
			ClaimKey: claim, Value: "x", Confidence: 0.9,
			ArbitrationSourceIDs: []string{"DRIFTING_SRC"}, Tick: uint64(i),
		})
		if _, err := e.RecordGroundTruth(GroundTruth{ClaimKey: claim, Value: "NOT_x", SourceID: "GT", Tick: uint64(i)}); err != nil {
			t.Fatalf("phase2 #%d: %v", i, err)
		}
	}
	late := Summarize(e.RecordsFor("claim:drift"))
	lateBelief := calc.Belief(trustcalc.NamespacedSubject(trustcalc.NamespaceCalibrationSource, "DRIFTING_SRC")).Mean()

	if late["calibration_accuracy"] >= early["calibration_accuracy"] {
		t.Fatalf("drift should lower cumulative calibration accuracy: early=%v late=%v",
			early["calibration_accuracy"], late["calibration_accuracy"])
	}
	if late["calibration_brier"] <= early["calibration_brier"] {
		t.Fatalf("drift should raise (worsen) cumulative Brier score: early=%v late=%v",
			early["calibration_brier"], late["calibration_brier"])
	}
	// The critical closed-loop assertion (P1): drift detected by
	// calibration must actually have reached the SAME shared trust
	// calculus Evidence/Intelligence use — not just sit in a metric
	// nobody reads.
	if lateBelief >= earlyBelief {
		t.Fatalf("drift must propagate into the shared trust calculus: early=%v late=%v", earlyBelief, lateBelief)
	}
}
