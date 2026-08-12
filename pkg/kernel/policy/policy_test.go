package policy

import "testing"

func TestGate_FirstMatchWins_DenyBeforeAllow(t *testing.T) {
	g := NewGate(Deny)
	g.RegisterRule(RuleMinTrustScore("min-trust", 0.5))
	g.RegisterRule(RuleAllowKnownActions("known-actions", "PLACE_ENGINE"))

	low := 0.2
	v, err := g.Evaluate(Input{Action: "PLACE_ENGINE", Subject: "engine-1", TrustScore: &low})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v != Deny {
		t.Fatalf("want Deny for low trust score, got %s", v)
	}
}

func TestGate_AllowWhenTrustOKAndActionKnown(t *testing.T) {
	g := NewGate(Deny)
	g.RegisterRule(RuleMinTrustScore("min-trust", 0.5))
	g.RegisterRule(RuleAllowKnownActions("known-actions", "PLACE_ENGINE"))

	high := 0.9
	v, err := g.Evaluate(Input{Action: "PLACE_ENGINE", Subject: "engine-1", TrustScore: &high})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v != Allow {
		t.Fatalf("want Allow, got %s", v)
	}
}

func TestGate_EscalateRoutesToApproval(t *testing.T) {
	g := NewGate(Deny)
	g.RegisterRule(RuleEscalatedRequiresApproval("escalate-gate"))
	g.RegisterRule(RuleAllowKnownActions("known-actions", "DARK_VESSEL_ACTION"))

	v, err := g.Evaluate(Input{Action: "DARK_VESSEL_ACTION", Subject: "vessel-1", RiskAction: "ESCALATE"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v != RequireApproval {
		t.Fatalf("want RequireApproval, got %s", v)
	}
}

func TestGate_OntologyInvalidDenies(t *testing.T) {
	g := NewGate(Allow)
	g.RegisterRule(RuleOntologyMustBeValid("ontology-gate"))

	bad := false
	v, err := g.Evaluate(Input{Action: "REGISTER_INSTANCE", Subject: "x", OntologyOK: &bad})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v != Deny {
		t.Fatalf("want Deny for invalid ontology instance, got %s", v)
	}
}

func TestGate_DefaultVerdictAppliesWhenNoRuleMatches(t *testing.T) {
	g := NewGate(Deny)
	g.RegisterRule(RuleMinTrustScore("min-trust", 0.5)) // TrustScore nil -> defers
	v, err := g.Evaluate(Input{Action: "UNKNOWN_ACTION", Subject: "x"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v != Deny {
		t.Fatalf("want fail-closed default Deny, got %s", v)
	}
}

func TestGate_LedgerIsHashChainedAndVerifiable(t *testing.T) {
	g := NewGate(Deny)
	g.RegisterRule(RuleAllowKnownActions("known", "A", "B"))

	for _, action := range []string{"A", "B", "C"} {
		if _, err := g.Evaluate(Input{Action: action, Subject: "s"}); err != nil {
			t.Fatalf("Evaluate(%s): %v", action, err)
		}
	}
	if err := g.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	ledger := g.Ledger()
	if len(ledger) != 3 {
		t.Fatalf("want 3 records, got %d", len(ledger))
	}
	if ledger[0].Verdict != Allow || ledger[1].Verdict != Allow || ledger[2].Verdict != Deny {
		t.Fatalf("unexpected verdicts: %+v", ledger)
	}

	// Tamper detection: mutate a record in place (simulating an
	// unauthorized log edit) and confirm VerifyChain catches it.
	tampered := &Gate{deflt: Deny, log: ledger}
	tampered.log[1].Verdict = Deny
	if err := tampered.VerifyChain(); err == nil {
		t.Fatal("expected VerifyChain to detect tampering, got nil error")
	}
}
