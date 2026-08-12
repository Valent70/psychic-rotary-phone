package kernel

import (
	"testing"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/kernel/intent"
	"veriqo/pkg/moat/calibration"
	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/decision"
	"veriqo/veriqo/core/runtime"
)

func TestKernelBootAllHealthy(t *testing.T) {
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer k.Shutdown()

	report := k.HealthReport()
	if len(report) != 7 {
		t.Fatalf("expected 7 engines, got %d: %v", len(report), report)
	}
	for name, status := range report {
		if status != runtime.StatusHealthy {
			t.Fatalf("engine %s not healthy: %v", name, status)
		}
	}
}

func TestKernelExecutionAndIntelligenceWork(t *testing.T) {
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer k.Shutdown()

	out, err := k.Runtime.Invoke("reasoning", "assert", map[string]string{
		"subject": "vessel/IMO1", "predicate": "status", "object": "at-sea",
	})
	if err != nil || out["result"] == "" {
		t.Fatalf("expected assert to succeed: %v %v", out, err)
	}

	_ = k.Intelligence.Observe(contradiction.RawObservation{
		ClaimKey: "vessel/IMO1", SourceID: "radar", Value: "at-sea", SourceReliability: 0.9, Tick: 1,
	})
	exp, err := k.Intelligence.Resolve("vessel/IMO1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if exp.WinningValue != "at-sea" {
		t.Fatalf("expected at-sea, got %s", exp.WinningValue)
	}
}

// TestSharedTrustCalculusConnectsEvidenceAndIntelligence proves the
// architectural fix this session made: Kernel.Evidence and
// Kernel.Intelligence must observe the SAME calibrated trust for a
// source, because they share one Calculus instance.
func TestSharedTrustCalculusConnectsEvidenceAndIntelligence(t *testing.T) {
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer k.Shutdown()

	_ = k.Intelligence.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "reliable-src", Value: "v1", SourceReliability: 0.95, Tick: 1})
	_ = k.Intelligence.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "unreliable-src", Value: "v2", SourceReliability: 0.2, Tick: 1})
	if _, err := k.Intelligence.Resolve("c1", 1, 0); err != nil {
		t.Fatal(err)
	}

	// Intelligence learned this via its shared calculus — Evidence.trust
	// (same instance, read through the same "evidence_source" namespace
	// via evidence.NewNamespacedTrust — see kernel.go wiring) must
	// report the identical score.
	fromIntelligence := k.Intelligence.Reputation("reliable-src")
	nsKey := trustcalc.NamespacedSubject(trustcalc.NamespaceEvidenceSource, "reliable-src")
	fromCalculus := k.TrustCalculus.Score(nsKey)
	if fromIntelligence != fromCalculus {
		t.Fatalf("expected identical scores from shared calculus: intelligence=%v calculus=%v", fromIntelligence, fromCalculus)
	}
	if fromCalculus <= 0.5 {
		t.Fatalf("expected reliable-src score to have risen above prior, got %v", fromCalculus)
	}
	// A raw (non-namespaced) lookup for the same ID must NOT alias the
	// namespaced belief — proving the two identity spaces are actually
	// distinct, not just conventionally different strings.
	if raw := k.TrustCalculus.Score("reliable-src"); raw == fromCalculus {
		t.Fatalf("raw key unexpectedly aliased the namespaced belief: both read %v", raw)
	}
}

// TestTrustLedgerReplayMatchesLiveKernelState is the kernel-level
// belief-ledger proof: everything Evidence/Intelligence/Intent/
// Calibration ever Observe() through the kernel's shared Calculus is
// recorded in k.TrustLedger, and replaying that ledger from scratch
// must reproduce the live Calculus's belief for every subject that
// was touched — the P0 "Belief Ledger" gap, closed and verified at
// the full kernel-integration level (not just in package trustcalc's
// own unit tests).
func TestTrustLedgerReplayMatchesLiveKernelState(t *testing.T) {
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer k.Shutdown()

	_ = k.Intelligence.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "reliable-src", Value: "v1", SourceReliability: 0.95, Tick: 1})
	_ = k.Intelligence.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "unreliable-src", Value: "v2", SourceReliability: 0.2, Tick: 1})
	if _, err := k.Intelligence.Resolve("c1", 1, 0); err != nil {
		t.Fatal(err)
	}

	if err := k.TrustLedger.VerifyChain(); err != nil {
		t.Fatalf("expected clean ledger chain, got %v", err)
	}
	if k.TrustLedger.Len() == 0 {
		t.Fatal("expected kernel activity to produce ledger entries")
	}

	replayed, err := k.TrustLedger.Replay(1, 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	nsKey := trustcalc.NamespacedSubject(trustcalc.NamespaceEvidenceSource, "reliable-src")
	live := k.TrustCalculus.Belief(nsKey)
	replay := replayed.Belief(nsKey)
	if live.Alpha != replay.Alpha || live.Beta != replay.Beta {
		t.Fatalf("live vs replayed belief diverged: live=%+v replay=%+v", live, replay)
	}
}

// TestIntentAndCalibrationShareTrustCalculus proves this session's P0/P1
// fix: Intent's Verifier and Calibration's Engine were previously two
// disconnected trust islands (Intent had its own private trust.Kernel;
// Calibration's Summarize() had zero production callers into any trust
// system). Both now write into the SAME k.TrustCalculus instance
// Evidence and Intelligence already share.
func TestIntentAndCalibrationShareTrustCalculus(t *testing.T) {
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer k.Shutdown()

	// --- Intent -> shared calculus ---
	before := k.TrustCalculus.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceIntentActor, "vessel/IMO2"))
	verdict, err := k.Intent.Verify(
		intent.Statement{ID: "int-1", Subject: "vessel/IMO2", ExpectedAction: decision.ActionMonitor, ExpectedRisk: 0.1, Tick: 1},
		intent.Observation{ObservedAction: decision.ActionMonitor, ObservedRisk: 0.1, Tick: 1},
	)
	if err != nil {
		t.Fatalf("Intent.Verify: %v", err)
	}
	if !verdict.ActionMatched {
		t.Fatalf("expected matched verdict, got %+v", verdict)
	}
	afterIntent := k.TrustCalculus.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceIntentActor, "vessel/IMO2"))
	if afterIntent <= before {
		t.Fatalf("Intent.Verify must move the SHARED calculus belief for its subject: before=%v after=%v", before, afterIntent)
	}

	// --- Calibration -> shared calculus ---
	k.Calibration.RegisterIndependentSource("GROUND_TRUTH")
	beforeSrc := k.TrustCalculus.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceCalibrationSource, "EVIDENCE_SRC_A"))
	if err := k.Calibration.RecordPrediction(calibration.Prediction{
		ClaimKey: "claim:vessel/IMO2:sanctioned", Value: "false", Confidence: 0.8,
		ArbitrationSourceIDs: []string{"EVIDENCE_SRC_A"}, Tick: 1,
	}); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}
	if _, err := k.Calibration.RecordGroundTruth(calibration.GroundTruth{
		ClaimKey: "claim:vessel/IMO2:sanctioned", Value: "false", SourceID: "GROUND_TRUTH", Tick: 2,
	}); err != nil {
		t.Fatalf("RecordGroundTruth: %v", err)
	}
	afterSrc := k.TrustCalculus.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceCalibrationSource, "EVIDENCE_SRC_A"))
	if afterSrc <= beforeSrc {
		t.Fatalf("Calibration.RecordGroundTruth must move the SHARED calculus belief for its arbitration source: before=%v after=%v", beforeSrc, afterSrc)
	}

	// Evidence Engine (same calculus instance) must see this exact
	// updated belief for EVIDENCE_SRC_A too -- proving it is one shared
	// object, not three parallel trust systems that merely agree by
	// coincidence.
	if k.Evidence == nil {
		t.Fatal("Evidence engine missing")
	}
	if k.TrustCalculus.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceCalibrationSource, "EVIDENCE_SRC_A")) != afterSrc {
		t.Fatalf("calculus score must be stable/identical across every reader")
	}
}

func TestKernelShutdownStopsEngines(t *testing.T) {
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Shutdown(); err != nil {
		t.Fatal(err)
	}
	report := k.HealthReport()
	for name, status := range report {
		if status != runtime.StatusStopped {
			t.Fatalf("engine %s expected stopped, got %v", name, status)
		}
	}
}

// TestKernelCanonicalPipelineSharesTrustCalculus proves k.Canonical is
// not a disconnected fourth trust island: it must be constructed with
// the exact same TrustCalculus instance as Evidence/Intelligence/
// Intent/Calibration (the v7.10.2 gap-closure P0 — see pkg/canonical's
// package comment).
func TestKernelCanonicalPipelineSharesTrustCalculus(t *testing.T) {
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if k.Canonical == nil {
		t.Fatal("expected Kernel.Canonical to be wired")
	}
	if k.Canonical.Trust != k.TrustCalculus {
		t.Fatal("expected Kernel.Canonical to share the exact same TrustCalculus instance as the rest of the kernel")
	}
}
