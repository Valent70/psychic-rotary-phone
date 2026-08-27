package trust

import (
	"errors"
	"testing"
)

func sourceReliabilityPolicy() TrustPolicy {
	return TrustPolicy{
		Name: "source_reliability",
		Rules: []TrustRule{
			RuleFromEvidenceKey("win_rate", 0.6, "win_rate"),
			RuleInverse("low_contradiction", 0.4, "contradiction_rate"),
		},
		CertifyThreshold: 0.7,
	}
}

func TestRegisterPolicyRejectsInvalid(t *testing.T) {
	k := NewKernel()
	if err := k.RegisterPolicy(TrustPolicy{Name: "empty"}); err != ErrNoRulesInPolicy {
		t.Fatalf("expected ErrNoRulesInPolicy, got %v", err)
	}
}

func TestEvaluateComputesWeightedScore(t *testing.T) {
	k := NewKernel()
	must(t, k.RegisterPolicy(sourceReliabilityPolicy()))

	score, err := k.Evaluate("ais-vendor-a", "source_reliability", TrustEvidence{
		"win_rate": 0.9, "contradiction_rate": 0.1,
	}, 1)
	must(t, err)
	// 0.6*0.9 + 0.4*(1-0.1) = 0.54 + 0.36 = 0.90
	if diff := score.Score - 0.90; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected score ~0.90, got %f", score.Score)
	}
	if len(score.RuleScores) != 2 {
		t.Fatalf("expected 2 rule scores, got %d", len(score.RuleScores))
	}
}

func TestEvaluateUnknownPolicyErrors(t *testing.T) {
	k := NewKernel()
	if _, err := k.Evaluate("x", "nonexistent", TrustEvidence{}, 1); !errors.Is(err, ErrUnknownPolicy) {
		t.Fatalf("expected ErrUnknownPolicy, got %v", err)
	}
}

func TestCertifyIssuesOnlyAboveThreshold(t *testing.T) {
	k := NewKernel()
	must(t, k.RegisterPolicy(sourceReliabilityPolicy()))

	_, certGood, err := k.Certify("good-source", "source_reliability", TrustEvidence{
		"win_rate": 0.95, "contradiction_rate": 0.02,
	}, 1)
	must(t, err)
	if certGood == nil {
		t.Fatalf("expected certificate for high-trust source")
	}

	_, certBad, err := k.Certify("bad-source", "source_reliability", TrustEvidence{
		"win_rate": 0.3, "contradiction_rate": 0.6,
	}, 1)
	must(t, err)
	if certBad != nil {
		t.Fatalf("expected NO certificate for low-trust source, got %+v", certBad)
	}
}

func TestCertificateLedgerHashChainAndVerify(t *testing.T) {
	k := NewKernel()
	must(t, k.RegisterPolicy(sourceReliabilityPolicy()))
	for i := 0; i < 3; i++ {
		_, cert, err := k.Certify("source-x", "source_reliability", TrustEvidence{
			"win_rate": 0.9, "contradiction_rate": 0.05,
		}, uint64(i+1))
		must(t, err)
		if cert == nil {
			t.Fatalf("expected certificate at iteration %d", i)
		}
	}
	if err := k.VerifyLedger(); err != nil {
		t.Fatalf("expected clean ledger: %v", err)
	}
	if len(k.CertificatesFor("source-x")) != 3 {
		t.Fatalf("expected 3 certificates for source-x")
	}
}

func TestVerifyLedgerDetectsTamper(t *testing.T) {
	k := NewKernel()
	must(t, k.RegisterPolicy(sourceReliabilityPolicy()))
	_, cert, err := k.Certify("s", "source_reliability", TrustEvidence{"win_rate": 0.99, "contradiction_rate": 0.01}, 1)
	must(t, err)
	if cert == nil {
		t.Fatalf("expected certificate")
	}
	k.ledger[0].Score = 0.01 // tamper
	if err := k.VerifyLedger(); err == nil {
		t.Fatalf("expected tamper detection")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
