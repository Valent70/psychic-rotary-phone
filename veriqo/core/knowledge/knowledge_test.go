package knowledge

import (
	"testing"

	"veriqo/pkg/kernel/reasoning"
)

func TestAssertAndQuery(t *testing.T) {
	s := NewStore()
	fact := reasoning.Triple{Subject: "vessel/IMO1", Predicate: "flag", Object: "PA"}
	s.Assert(fact, 0.9, "")
	got := s.Query(reasoning.Triple{Subject: "vessel/IMO1", Predicate: "flag", Object: reasoning.Var("x")})
	if len(got) != 1 || got[0].Object != "PA" {
		t.Fatalf("expected 1 match, got %+v", got)
	}
	c, ok := s.Confidence(fact)
	if !ok || c != 0.9 {
		t.Fatalf("expected confidence 0.9, got %v ok=%v", c, ok)
	}
}

func TestConflictDetection(t *testing.T) {
	s := NewStore()
	s.Assert(reasoning.Triple{Subject: "vessel/IMO1", Predicate: "flag", Object: "PA"}, 0.9, "")
	conflicts := s.Conflicts(reasoning.Triple{Subject: "vessel/IMO1", Predicate: "flag", Object: "LR"})
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
}

func TestVersionChainAndTamperDetection(t *testing.T) {
	s := NewStore()
	s.Assert(reasoning.Triple{Subject: "a", Predicate: "p", Object: "1"}, 0.8, "")
	s.Assert(reasoning.Triple{Subject: "a", Predicate: "p", Object: "2"}, 0.6, "")
	if err := s.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain, got %v", err)
	}
	s.mu.Lock()
	s.history[0].Confidence = 0.99
	s.mu.Unlock()
	if err := s.VerifyChain(); err == nil {
		t.Fatal("expected tamper detection to fail VerifyChain")
	}
}

func TestInferPassthrough(t *testing.T) {
	s := NewStore()
	s.Assert(reasoning.Triple{Subject: "a", Predicate: "knows", Object: "b"}, 1.0, "")
	s.AddRule(reasoning.Rule{
		If:   []reasoning.Triple{{Subject: reasoning.Var("x"), Predicate: "knows", Object: reasoning.Var("y")}},
		Then: reasoning.Triple{Subject: reasoning.Var("y"), Predicate: "knownBy", Object: reasoning.Var("x")},
	})
	n, err := s.Infer()
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected at least one derived fact")
	}
	got := s.Query(reasoning.Triple{Subject: "b", Predicate: "knownBy", Object: "a"})
	if len(got) != 1 {
		t.Fatalf("expected derived fact to be queryable, got %+v", got)
	}
}

func TestReviseIsBeliefRevisionNotOverwrite(t *testing.T) {
	s := NewStore()
	fact := reasoning.Triple{Subject: "vessel/IMO1", Predicate: "sanctioned", Object: "true"}
	// First sighting: neutral prior via Assert.
	s.Assert(fact, 0.5, "")
	// Strong confirming evidence should push confidence up, not just replace it with a caller-picked number.
	v := s.Revise(fact, 9.0, "")
	if v.Confidence <= 0.5 {
		t.Fatalf("expected revised confidence to rise with confirming evidence, got %v", v.Confidence)
	}
	// Strong disconfirming evidence afterwards should pull it back down.
	v2 := s.Revise(fact, 1.0/9.0, "")
	if v2.Confidence >= v.Confidence {
		t.Fatalf("expected revised confidence to fall with disconfirming evidence: %v -> %v", v.Confidence, v2.Confidence)
	}
}

func TestReviseOnNeverAssertedFact(t *testing.T) {
	s := NewStore()
	fact := reasoning.Triple{Subject: "x", Predicate: "y", Object: "z"}
	v := s.Revise(fact, 4.0, "")
	if v.Confidence <= 0.5 {
		t.Fatalf("expected revision from uninformative 0.5 prior to rise with confirming evidence, got %v", v.Confidence)
	}
}

func TestPropagateConfidenceNoisyAnd(t *testing.T) {
	got := PropagateConfidence([]float64{0.9, 0.8})
	want := 0.72
	if got < want-0.0001 || got > want+0.0001 {
		t.Fatalf("expected noisy-AND product 0.72, got %v", got)
	}
	if PropagateConfidence(nil) != 0 {
		t.Fatal("expected 0 for no premises")
	}
}
