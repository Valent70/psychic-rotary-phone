package intent

import (
	"testing"

	"veriqo/pkg/moat/decision"
)

func TestVerify_AccurateIntentBuildsTrust(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	stmt := Statement{ID: "intent-1", ActorID: "operator-1", Subject: "vessel/IMO123", ExpectedAction: decision.ActionFlag, ExpectedRisk: 0.6, Tick: 1}
	obs := Observation{ObservedAction: decision.ActionFlag, ObservedRisk: 0.62, Tick: 2}

	verdict, err := v.Verify(stmt, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.ActionMatched {
		t.Fatal("expected action to match")
	}
	if verdict.AbsDeviation > 0.05 {
		t.Fatalf("expected small deviation, got %v", verdict.AbsDeviation)
	}
	if verdict.TrustScore.Score <= 0 {
		t.Fatalf("expected positive trust score for accurate intent, got %v", verdict.TrustScore.Score)
	}
}

func TestVerify_InaccurateIntentLowersTrust(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	subject := "vessel/IMO999"

	// First: a perfectly accurate prediction, establishing a high baseline.
	good := Statement{ID: "intent-a", Subject: subject, ExpectedAction: decision.ActionMonitor, ExpectedRisk: 0.1, Tick: 1}
	goodObs := Observation{ObservedAction: decision.ActionMonitor, ObservedRisk: 0.1, Tick: 1}
	firstVerdict, err := v.Verify(good, goodObs)
	if err != nil {
		t.Fatal(err)
	}

	// Then: a badly wrong prediction (predicted MONITOR/low-risk, actually ESCALATE/high-risk).
	bad := Statement{ID: "intent-b", Subject: subject, ExpectedAction: decision.ActionMonitor, ExpectedRisk: 0.1, Tick: 2}
	badObs := Observation{ObservedAction: decision.ActionEscalate, ObservedRisk: 0.95, Tick: 2}
	secondVerdict, err := v.Verify(bad, badObs)
	if err != nil {
		t.Fatal(err)
	}
	if secondVerdict.ActionMatched {
		t.Fatal("expected action mismatch to be detected")
	}
	if secondVerdict.AbsDeviation < 0.8 {
		t.Fatalf("expected large deviation, got %v", secondVerdict.AbsDeviation)
	}
	if secondVerdict.TrustScore.Score >= firstVerdict.TrustScore.Score {
		t.Fatalf("expected trust score to drop after a badly-wrong intent: before=%v after=%v",
			firstVerdict.TrustScore.Score, secondVerdict.TrustScore.Score)
	}
}

func TestCertify_IssuesHashChainedCertificate(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	stmt := Statement{ID: "intent-c", Subject: "vessel/IMO555", ExpectedAction: decision.ActionFlag, ExpectedRisk: 0.5, Tick: 1}
	obs := Observation{ObservedAction: decision.ActionFlag, ObservedRisk: 0.5, Tick: 1}
	_, cert, err := v.Certify(stmt, obs)
	if err != nil {
		t.Fatal(err)
	}
	if cert == nil {
		t.Fatal("expected a certificate for a perfectly accurate, high-confidence intent")
	}
	if err := v.VerifyLedger(); err != nil {
		t.Fatalf("ledger should verify: %v", err)
	}
}
