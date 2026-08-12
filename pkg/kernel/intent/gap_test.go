// Session update — closes roadmap item #2 from the audit follow-up
// report (§4): pkg/kernel/intent 78.9% -> higher. Real gaps closed:
//
//   - Ledger(), previously 0% covered despite being the package's
//     documented audit surface ("the same VerifyLedger() discipline
//     used everywhere else evidence is chained in this repo").
//   - currentScore's non-empty-history branch: the existing suite
//     always calls Verify on a subject with no prior Certify, so
//     `len(certs) == 0` was the only branch ever taken. This test
//     certifies a subject first, then verifies it again, to exercise
//     the "subject already has a certificate" path and confirm
//     TrustDelta is computed relative to that real prior score (not
//     relative to zero).
package intent

import (
	"testing"
	"veriqo/pkg/moat/decision"
)

func TestLedger_ExposesFullHashChainedHistory(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	subject := "vessel/IMO777"

	stmt1 := Statement{ID: "intent-x1", Subject: subject, ExpectedAction: decision.ActionFlag, ExpectedRisk: 0.5, Tick: 1}
	obs1 := Observation{ObservedAction: decision.ActionFlag, ObservedRisk: 0.5, Tick: 1}
	if _, cert1, err := v.Certify(stmt1, obs1); err != nil || cert1 == nil {
		t.Fatalf("first certify: cert=%v err=%v", cert1, err)
	}

	stmt2 := Statement{ID: "intent-x2", Subject: subject, ExpectedAction: decision.ActionFlag, ExpectedRisk: 0.5, Tick: 2}
	obs2 := Observation{ObservedAction: decision.ActionFlag, ObservedRisk: 0.51, Tick: 2}
	if _, cert2, err := v.Certify(stmt2, obs2); err != nil || cert2 == nil {
		t.Fatalf("second certify: cert=%v err=%v", cert2, err)
	}

	ledger := v.Ledger()
	if len(ledger) != 2 {
		t.Fatalf("want 2 certificates on the ledger, got %d", len(ledger))
	}
	if ledger[0].SubjectID != subject || ledger[1].SubjectID != subject {
		t.Fatalf("expected both certificates scoped to %q, got %+v", subject, ledger)
	}
	if err := v.VerifyLedger(); err != nil {
		t.Fatalf("ledger should verify: %v", err)
	}
}

func TestVerify_UsesRealPriorScoreAsBaseline(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	subject := "vessel/IMO888"

	// Certify once so currentScore(subject) has real history (non-empty
	// certs slice) the next time Verify is called for this subject.
	stmt1 := Statement{ID: "intent-y1", Subject: subject, ExpectedAction: decision.ActionFlag, ExpectedRisk: 0.5, Tick: 1}
	obs1 := Observation{ObservedAction: decision.ActionFlag, ObservedRisk: 0.5, Tick: 1}
	_, cert, err := v.Certify(stmt1, obs1)
	if err != nil || cert == nil {
		t.Fatalf("setup certify: cert=%v err=%v", cert, err)
	}
	baseline := cert.Score

	stmt2 := Statement{ID: "intent-y2", Subject: subject, ExpectedAction: decision.ActionFlag, ExpectedRisk: 0.5, Tick: 2}
	obs2 := Observation{ObservedAction: decision.ActionFlag, ObservedRisk: 0.5, Tick: 2}
	verdict, err := v.Verify(stmt2, obs2)
	if err != nil {
		t.Fatal(err)
	}
	wantDelta := verdict.TrustScore.Score - baseline
	if diff := verdict.TrustDelta - wantDelta; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("TrustDelta should be computed against the real prior certified score %v, got delta=%v (score=%v)",
			baseline, verdict.TrustDelta, verdict.TrustScore.Score)
	}
}
