package reasoning

import "testing"

func TestKB_SimpleForwardChain(t *testing.T) {
	kb := NewKB()
	kb.Assert(Triple{"vessel-A", "ais_gap_detected", "true"})
	kb.Assert(Triple{"vessel-A", "sts_transfer_detected", "true"})
	kb.AddRule(Rule{
		Name: "dark-vessel-suspicion",
		If: []Triple{
			{Subject: Var("v"), Predicate: "ais_gap_detected", Object: "true"},
			{Subject: Var("v"), Predicate: "sts_transfer_detected", Object: "true"},
		},
		Then: Triple{Subject: Var("v"), Predicate: "suspected_dark_activity", Object: "true"},
	})

	n, err := kb.Infer()
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 new fact, got %d", n)
	}
	got := kb.Query(Triple{Subject: "vessel-A", Predicate: "suspected_dark_activity", Object: Var("x")})
	if len(got) != 1 {
		t.Fatalf("expected derived fact for vessel-A, got %v", got)
	}
	deriv, ok := kb.Explain(got[0])
	if !ok {
		t.Fatal("expected a derivation trace for the derived fact")
	}
	if deriv.RuleName != "dark-vessel-suspicion" || len(deriv.Premises) != 2 {
		t.Fatalf("unexpected derivation: %+v", deriv)
	}
}

func TestKB_MultiHopTransitiveChain(t *testing.T) {
	kb := NewKB()
	kb.Assert(Triple{"vessel-A", "docked_at", "port-1"})
	kb.Assert(Triple{"port-1", "located_in", "sanctioned-zone"})
	kb.AddRule(Rule{
		Name: "vessel-in-sanctioned-zone",
		If: []Triple{
			{Subject: Var("v"), Predicate: "docked_at", Object: Var("p")},
			{Subject: Var("p"), Predicate: "located_in", Object: Var("z")},
		},
		Then: Triple{Subject: Var("v"), Predicate: "present_in", Object: Var("z")},
	})
	kb.AddRule(Rule{
		Name: "sanctioned-zone-risk-flag",
		If: []Triple{
			{Subject: Var("v"), Predicate: "present_in", Object: "sanctioned-zone"},
		},
		Then: Triple{Subject: Var("v"), Predicate: "risk_flag", Object: "sanctions_exposure"},
	})

	n, err := kb.Infer()
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 new facts across the two-hop chain, got %d", n)
	}
	got := kb.Query(Triple{Subject: "vessel-A", Predicate: "risk_flag", Object: "sanctions_exposure"})
	if len(got) != 1 {
		t.Fatalf("expected the two-hop-derived risk flag, got %v", got)
	}
}

func TestKB_FixpointTerminatesWithNoNewFacts(t *testing.T) {
	kb := NewKB()
	kb.Assert(Triple{"a", "related_to", "b"})
	kb.AddRule(Rule{
		Name: "symmetric",
		If:   []Triple{{Subject: Var("x"), Predicate: "related_to", Object: Var("y")}},
		Then: Triple{Subject: Var("y"), Predicate: "related_to", Object: Var("x")},
	})
	n1, err := kb.Infer()
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	n2, err := kb.Infer()
	if err != nil {
		t.Fatalf("second Infer: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("second Infer call should find nothing new (already at fixpoint), got %d new (first pass found %d)", n2, n1)
	}
}
