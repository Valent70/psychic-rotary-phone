package state

import (
	"errors"
	"strings"
	"testing"
)

func eng() *Engine { return NewEngine(100, 0.5) }

func TestTrustIncreaseRequiresEvidence(t *testing.T) {
	e := eng()
	if _, err := e.Assess("acme", "op", "p", "looks fine", 0.9, nil, 1, 0); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("trust raised with no evidence: %v", err)
	}
	if _, err := e.Assess("acme", "op", "p", "contradicted by port authority", 0.2, nil, 1, 0); err != nil {
		t.Fatalf("lowering trust without evidence refs should be allowed: %v", err)
	}
}

func TestTrustDecaysTowardNeutralPrior(t *testing.T) {
	e := eng()
	if _, err := e.Assess("acme", "op", "p", "clean history", 0.95, []string{"ev1"}, 0, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	now := e.StateAt("acme", 0)
	later := e.StateAt("acme", 300)
	if later.EffectiveScore >= now.EffectiveScore {
		t.Fatalf("trust did not decay: %.6f -> %.6f", now.EffectiveScore, later.EffectiveScore)
	}
	if later.EffectiveScore < e.NeutralPrior {
		t.Fatalf("decay overshot the neutral prior: %.6f", later.EffectiveScore)
	}
}

func TestRevocationIsTerminalUntilGovernanceReinstates(t *testing.T) {
	e := eng()
	if _, err := e.Assess("acme", "op", "p", "clean", 0.9, []string{"ev1"}, 1, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if _, err := e.Revoke("acme", "op", "sanctioned", []string{"ofac-listing"}, 2); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if st := e.StateAt("acme", 3); !st.Revoked || st.Level != LevelRevoked {
		t.Fatalf("revocation did not stick: %+v", st)
	}
	if _, err := e.Assess("acme", "op", "p", "seems ok again", 0.9, []string{"ev2"}, 4, 0); !errors.Is(err, ErrRevokedTerminal) {
		t.Fatal("an automated assessment un-revoked a revoked subject")
	}
	if _, err := e.Reinstate("acme", GovernanceDecision{ActorID: "op"}, 0.7); !errors.Is(err, ErrNoGovernanceActor) {
		t.Fatal("reinstatement accepted without an approver")
	}
	if _, err := e.Reinstate("acme", GovernanceDecision{ActorID: "op", ApprovedBy: "compliance-board",
		Reason: "delisted", Tick: 5, EvidenceRefs: []string{"delisting-notice"}}, 0.7); err != nil {
		t.Fatalf("governance reinstatement rejected: %v", err)
	}
	if st := e.StateAt("acme", 6); st.Revoked {
		t.Fatal("governance reinstatement did not take effect")
	}
}

func TestEscalationIsRoutingNotScoreChange(t *testing.T) {
	e := eng()
	if _, err := e.Assess("acme", "op", "p", "clean", 0.9, []string{"ev1"}, 1, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	before := e.StateAt("acme", 1)
	if _, err := e.Escalate("acme", "op", "conflicting evidence", []string{"contradiction-1"}, 2); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	st := e.StateAt("acme", 2)
	if !st.Escalated || st.Level != LevelSuspect {
		t.Fatalf("escalation not reflected: %+v", st)
	}
	if st.Score != before.Score {
		t.Fatalf("escalation silently changed the score: %.4f -> %.4f", before.Score, st.Score)
	}
}

func TestExplainAnswersBothAuditQuestions(t *testing.T) {
	e := eng()
	if _, err := e.Assess("acme", "op", "p", "clean registry record", 0.9, []string{"ev-registry"}, 1, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if _, err := e.Assess("acme", "op", "p", "port authority contradiction", 0.4, []string{"ev-port"}, 5, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	lines, err := e.Explain("acme", 6)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"ev-registry", "ev-port", "raised", "lowered"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("explanation missing %q:\n%s", want, joined)
		}
	}
	if _, err := e.Explain("nobody", 1); !errors.Is(err, ErrUnknownSubject) {
		t.Fatal("explained a subject with no history")
	}
}

func TestHistoricalStateIsPreserved(t *testing.T) {
	e := eng()
	if _, err := e.Assess("acme", "op", "p", "clean", 0.9, []string{"ev1"}, 10, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if _, err := e.Revoke("acme", "op", "sanctioned", []string{"ofac"}, 100); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	past := e.StateAt("acme", 20)
	if past.Revoked {
		t.Fatal("a later revocation rewrote history")
	}
	if past.Level == LevelRevoked {
		t.Fatal("historical level contaminated by a later transition")
	}
}

func TestValidityWindowExpiry(t *testing.T) {
	e := eng()
	if _, err := e.Assess("acme", "op", "p", "clean", 0.95, []string{"ev1"}, 1, 10); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if st := e.StateAt("acme", 5); strings.Contains(st.Reason, "expired") {
		t.Fatal("state expired inside its validity window")
	}
	if st := e.StateAt("acme", 50); !strings.Contains(st.Reason, "expired") {
		t.Fatalf("state did not expire past its window: %+v", st)
	}
}

func TestLedgerIsTamperEvident(t *testing.T) {
	e := eng()
	if _, err := e.Assess("acme", "op", "p", "clean", 0.9, []string{"ev1"}, 1, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if _, err := e.Escalate("acme", "op", "why not", nil, 2); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("clean chain rejected: %v", err)
	}
	e.ledger[0].ToScore = 0.99
	if err := e.VerifyChain(); !errors.Is(err, ErrChainBroken) {
		t.Fatal("tampered trust ledger accepted")
	}
}

func TestConfidenceSaturatesBelowOne(t *testing.T) {
	e := eng()
	refs := []string{}
	for i := 0; i < 30; i++ {
		refs = append(refs, "ev")
	}
	if _, err := e.Assess("acme", "op", "p", "many corroborations", 0.9, refs, 1, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if st := e.StateAt("acme", 1); st.Confidence >= 1 {
		t.Fatalf("confidence reached certainty: %.9f", st.Confidence)
	}
}

func TestEmptySubjectAndBadScoreRejected(t *testing.T) {
	e := eng()
	if _, err := e.Assess("", "op", "p", "r", 0.5, nil, 1, 0); !errors.Is(err, ErrEmptySubject) {
		t.Fatal("empty subject accepted")
	}
	if _, err := e.Assess("acme", "op", "p", "r", 1.5, []string{"e"}, 1, 0); !errors.Is(err, ErrBadScore) {
		t.Fatal("out-of-range score accepted")
	}
	if _, err := e.Assess("acme", "op", "p", "", 0.5, []string{"e"}, 1, 0); !errors.Is(err, ErrNoReason) {
		t.Fatal("reasonless transition accepted")
	}
}
