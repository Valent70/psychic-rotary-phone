package precedence

import (
	"errors"
	"testing"
)

func TestDenyBeatsEverything(t *testing.T) {
	v, err := Combine([]Signal{
		{Source: "risk_policy", Action: ActionAllow},
		{Source: "hitl", Action: ActionReview},
		{Source: "authz", Action: ActionDeny, Reason: "policy sanctions-screen@3 denied"},
		{Source: "sandbox", Action: ActionEscalate},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != ActionDeny {
		t.Fatalf("DENY must win over every other signal, got %s", v.Action)
	}
	if v.Winner.Source != "authz" {
		t.Fatalf("winner must be traceable to its source, got %s", v.Winner.Source)
	}
}

func TestFullOrdering(t *testing.T) {
	order := []Action{ActionAllow, ActionReview, ActionEscalate, ActionBlock, ActionDeny}
	for i, lower := range order {
		for j, higher := range order {
			if j <= i {
				continue
			}
			v, err := Combine([]Signal{{Source: "a", Action: lower}, {Source: "b", Action: higher}})
			if err != nil {
				t.Fatal(err)
			}
			if v.Action != higher {
				t.Fatalf("%s vs %s: expected %s to win, got %s", lower, higher, higher, v.Action)
			}
		}
	}
}

func TestCombineIsOrderIndependent(t *testing.T) {
	a, err := Combine([]Signal{
		{Source: "z", Action: ActionEscalate}, {Source: "a", Action: ActionBlock}, {Source: "m", Action: ActionAllow},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Combine([]Signal{
		{Source: "m", Action: ActionAllow}, {Source: "z", Action: ActionEscalate}, {Source: "a", Action: ActionBlock},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash != b.Hash || a.Action != b.Action || a.Winner != b.Winner {
		t.Fatal("Combine must be independent of input signal order")
	}
}

func TestTieAtHighestRankBrokenBySourceDeterministically(t *testing.T) {
	v, err := Combine([]Signal{
		{Source: "zeta", Action: ActionDeny}, {Source: "alpha", Action: ActionDeny},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Winner.Source != "alpha" {
		t.Fatalf("a tie at the same rank must break by lexicographic source, got %s", v.Winner.Source)
	}
}

func TestUnknownActionRefused(t *testing.T) {
	_, err := Combine([]Signal{{Source: "x", Action: "MAYBE"}})
	if !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("expected ErrUnknownAction, got %v", err)
	}
}

func TestEmptySignalsRefused(t *testing.T) {
	_, err := Combine(nil)
	if !errors.Is(err, ErrNoSignals) {
		t.Fatalf("expected ErrNoSignals, got %v", err)
	}
}

func TestAnonymousSignalRefused(t *testing.T) {
	_, err := Combine([]Signal{{Action: ActionAllow}})
	if !errors.Is(err, ErrEmptySource) {
		t.Fatalf("expected ErrEmptySource, got %v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	v, err := Combine([]Signal{{Source: "a", Action: ActionAllow}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(v); err != nil {
		t.Fatalf("a freshly combined verdict must verify: %v", err)
	}
	v.Action = ActionDeny
	if err := Verify(v); err == nil {
		t.Fatal("editing the resolved action after the fact must fail verification")
	}
}

func TestSingleSignalIsItsOwnWinner(t *testing.T) {
	v, err := Combine([]Signal{{Source: "solo", Action: ActionReview, Reason: "manual hold"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != ActionReview || v.Winner.Source != "solo" {
		t.Fatalf("a single signal must pass through unchanged, got %+v", v)
	}
}
