package knowledge

import (
	"errors"
	"testing"
)

func fullCycle(t *testing.T, e *Engine, proposer string, muts []Mutation, from, tick uint64) Proposal {
	t.Helper()
	p, err := e.Propose(proposer, "reason", []string{"e1"}, muts, tick)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Analyze(p.ID, tick); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Simulate(p.ID, SimulationResult{SampleSize: 100, ChangedOutcomes: 3}, tick); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(p.ID, "governance-officer", tick); err != nil {
		t.Fatal(err)
	}
	out, err := e.Activate(p.ID, from, tick)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestKnowledgeStateAtT1IsUnchangedByAChangeAtT2(t *testing.T) {
	e := New()
	fullCycle(t, e, "analyst", []Mutation{{Kind: ChangeWeight, Target: "ais_weight", Value: 0.6}}, 10, 10)
	at15 := e.StateAt(15)
	if at15.Weights["ais_weight"] != 0.6 {
		t.Fatalf("precondition failed: %v", at15.Weights)
	}
	rootAt15 := at15.RootHash

	fullCycle(t, e, "analyst", []Mutation{{Kind: ChangeWeight, Target: "ais_weight", Value: 0.2}}, 30, 20)

	again := e.StateAt(15)
	if again.RootHash != rootAt15 {
		t.Fatalf("THE critical invariant broke: state at tick 15 changed because of a tick-30 change\n%v\n%v",
			at15, again)
	}
	if again.Weights["ais_weight"] != 0.6 {
		t.Fatalf("historical weight was rewritten: %v", again.Weights)
	}
	now := e.StateAt(35)
	if now.Weights["ais_weight"] != 0.2 {
		t.Fatalf("the new value must apply after its effective tick, got %v", now.Weights)
	}
}

func TestActivationCannotBeBackdated(t *testing.T) {
	e := New()
	p, _ := e.Propose("a", "r", []string{"e"}, []Mutation{{Kind: ChangeWeight, Target: "w", Value: 0.5}}, 100)
	_, _ = e.Analyze(p.ID, 100)
	_, _ = e.Simulate(p.ID, SimulationResult{SampleSize: 10}, 100)
	_, _ = e.Approve(p.ID, "officer", 100)
	if _, err := e.Activate(p.ID, 50, 100); !errors.Is(err, ErrRetroactive) {
		t.Fatalf("retroactive activation must be refused, got %v", err)
	}
}

func TestApprovalRequiresASimulation(t *testing.T) {
	e := New()
	p, _ := e.Propose("a", "r", []string{"e"}, []Mutation{{Kind: AddRule, Target: "r1", Body: "x"}}, 1)
	_, _ = e.Analyze(p.ID, 1)
	if _, err := e.Approve(p.ID, "officer", 1); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("ANALYZED -> APPROVED must be illegal without SIMULATED, got %v", err)
	}
}

func TestEmptySimulationSampleIsRefused(t *testing.T) {
	e := New()
	p, _ := e.Propose("a", "r", []string{"e"}, []Mutation{{Kind: AddRule, Target: "r1", Body: "x"}}, 1)
	_, _ = e.Analyze(p.ID, 1)
	if _, err := e.Simulate(p.ID, SimulationResult{SampleSize: 0}, 1); !errors.Is(err, ErrSimulationRequired) {
		t.Fatalf("a zero-sample simulation proves nothing and must be refused, got %v", err)
	}
}

func TestSelfApprovalRefused(t *testing.T) {
	e := New()
	p, _ := e.Propose("analyst", "r", []string{"e"}, []Mutation{{Kind: AddRule, Target: "r1", Body: "x"}}, 1)
	_, _ = e.Analyze(p.ID, 1)
	_, _ = e.Simulate(p.ID, SimulationResult{SampleSize: 5}, 1)
	if _, err := e.Approve(p.ID, "analyst", 1); !errors.Is(err, ErrApproverIsProposer) {
		t.Fatalf("self-approval must be refused, got %v", err)
	}
}

func TestProposalWithoutJustificationRefused(t *testing.T) {
	e := New()
	if _, err := e.Propose("a", "", []string{"e"}, []Mutation{{Kind: AddRule, Target: "r", Body: "b"}}, 1); !errors.Is(err, ErrMissingJustification) {
		t.Fatalf("expected justification error, got %v", err)
	}
	if _, err := e.Propose("a", "r", nil, []Mutation{{Kind: AddRule, Target: "r", Body: "b"}}, 1); !errors.Is(err, ErrMissingJustification) {
		t.Fatalf("expected justification error for missing evidence, got %v", err)
	}
}

func TestMalformedMutationsRejected(t *testing.T) {
	e := New()
	cases := []Mutation{
		{Kind: "TELEPORT_RULE", Target: "x"},
		{Kind: AddRule, Target: ""},
		{Kind: AddRule, Target: "r"},                            // no body
		{Kind: ChangeWeight, Target: "w", Value: 3},             // out of range
		{Kind: RemoveRule, Target: "r", Value: 0.5},             // value on a structural mutation
		{Kind: ChangeSourceAuthority, Target: "s", Value: -0.1}, // out of range
	}
	for i, m := range cases {
		if _, err := e.Propose("a", "r", []string{"e"}, []Mutation{m}, uint64(i)); !errors.Is(err, ErrInvalidMutation) {
			t.Fatalf("case %d (%+v) must be rejected, got %v", i, m, err)
		}
	}
}

func TestTwoConcurrentInFlightProposalsConflict(t *testing.T) {
	e := New()
	a, err := e.Propose("analyst-a", "raise ais weight", []string{"e1"},
		[]Mutation{{Kind: ChangeWeight, Target: "shared", Value: 0.7}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Analyze(a.ID, 10); err != nil {
		t.Fatal(err)
	}
	b, err := e.Propose("analyst-b", "lower ais weight", []string{"e2"},
		[]Mutation{{Kind: ChangeWeight, Target: "shared", Value: 0.1}}, 11)
	if err != nil {
		t.Fatal(err)
	}
	an, err := e.Analyze(b.ID, 11)
	if err != nil {
		t.Fatal(err)
	}
	if !an.Conflict.Blocking || len(an.Conflict.ConflictsWith) == 0 {
		t.Fatalf("two in-flight proposals over one target must conflict, got %+v", an.Conflict)
	}
	// And the conflict is enforced at activation, not merely reported.
	_, _ = e.Simulate(b.ID, SimulationResult{SampleSize: 10}, 11)
	_, _ = e.Approve(b.ID, "officer", 11)
	if _, err := e.Activate(b.ID, 12, 12); !errors.Is(err, ErrConflict) {
		t.Fatalf("activation must be blocked by the conflict, got %v", err)
	}
}

func TestSupersedingAnActiveProposalIsTheNormalPath(t *testing.T) {
	e := New()
	first := fullCycle(t, e, "a", []Mutation{{Kind: ChangeWeight, Target: "shared", Value: 0.7}}, 10, 10)
	p, err := e.Propose("b", "revised weight", []string{"e2"},
		[]Mutation{{Kind: ChangeWeight, Target: "shared", Value: 0.1}}, 11)
	if err != nil {
		t.Fatal(err)
	}
	an, err := e.Analyze(p.ID, 11)
	if err != nil {
		t.Fatal(err)
	}
	if an.Conflict.Blocking {
		t.Fatalf("superseding an active proposal must not be blocking: %+v", an.Conflict)
	}
	if len(an.Conflict.Supersedes) != 1 || an.Conflict.Supersedes[0] != first.ID {
		t.Fatalf("the superseded proposal must be named: %+v", an.Conflict)
	}
	_, _ = e.Simulate(p.ID, SimulationResult{SampleSize: 10}, 11)
	_, _ = e.Approve(p.ID, "officer", 11)
	if _, err := e.Activate(p.ID, 20, 12); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Proposal(first.ID); got.State != StateSuperseded {
		t.Fatalf("the first proposal must be SUPERSEDED, got %s", got.State)
	}
	if v := e.StateAt(25).Weights["shared"]; v != 0.1 {
		t.Fatalf("the newer value must be in force, got %v", v)
	}
	if v := e.StateAt(15).Weights["shared"]; v != 0.7 {
		t.Fatalf("the older value must still hold in its own window, got %v", v)
	}
}

func TestIdenticalMutationIsANoOpNotAConflict(t *testing.T) {
	e := New()
	fullCycle(t, e, "a", []Mutation{{Kind: ChangeWeight, Target: "shared", Value: 0.7}}, 10, 10)
	p, _ := e.Propose("b", "reassert", []string{"e2"},
		[]Mutation{{Kind: ChangeWeight, Target: "shared", Value: 0.7}}, 11)
	an, err := e.Analyze(p.ID, 11)
	if err != nil {
		t.Fatal(err)
	}
	if an.Conflict.Blocking {
		t.Fatalf("re-asserting the same value is idempotent, not a conflict: %+v", an.Conflict)
	}
}

func TestNonConflictingRulesCoexist(t *testing.T) {
	e := New()
	first := fullCycle(t, e, "a", []Mutation{{Kind: AddRule, Target: "r1", Body: "old"}}, 10, 10)

	// A different proposer avoids the conflict path by removing then
	// re-adding, which is a genuine collision; instead assert the
	// supersession path directly with a non-blocking distinct target.
	second := fullCycle(t, e, "a", []Mutation{{Kind: AddRule, Target: "r2", Body: "new"}}, 20, 20)

	if first.ID == second.ID {
		t.Fatal("distinct proposals must have distinct ids")
	}
	s := e.StateAt(25)
	if s.Rules["r1"] != "old" || s.Rules["r2"] != "new" {
		t.Fatalf("both non-conflicting rules should be in force: %v", s.Rules)
	}
}

func TestRevertIsAnAppendNotADeletion(t *testing.T) {
	e := New()
	p := fullCycle(t, e, "a", []Mutation{{Kind: AddRule, Target: "r1", Body: "v1"}}, 10, 10)
	at15 := e.StateAt(15)
	if at15.Rules["r1"] != "v1" {
		t.Fatal("precondition")
	}
	if _, err := e.Revert(p.ID, "sre", "caused false positives", 30, 25); err != nil {
		t.Fatal(err)
	}
	after := e.StateAt(35)
	if _, still := after.Rules["r1"]; still {
		t.Fatal("a reverted rule must not apply after its window closes")
	}
	history := e.StateAt(15)
	if history.RootHash != at15.RootHash {
		t.Fatal("reverting must not rewrite the historical state")
	}
}

func TestRevertBeforeEffectiveIsRefused(t *testing.T) {
	e := New()
	p := fullCycle(t, e, "a", []Mutation{{Kind: AddRule, Target: "r", Body: "v"}}, 50, 10)
	if _, err := e.Revert(p.ID, "sre", "", 40, 60); !errors.Is(err, ErrRetroactive) {
		t.Fatalf("expected retroactive rejection, got %v", err)
	}
}

func TestStateRootChangesWithTheKnowledgeAndNotOtherwise(t *testing.T) {
	e := New()
	fullCycle(t, e, "a", []Mutation{{Kind: ChangeModelParameter, Target: "hbayes@decay", Value: 0.9}}, 10, 10)
	a := e.StateAt(12)
	b := e.StateAt(13)
	if a.RootHash != b.RootHash {
		t.Fatal("two ticks with identical knowledge must have the same root")
	}
	fullCycle(t, e, "a", []Mutation{{Kind: ChangeModelParameter, Target: "hbayes@floor", Value: 0.01}}, 20, 20)
	c := e.StateAt(25)
	if c.RootHash == a.RootHash {
		t.Fatal("a new active parameter must change the root")
	}
}

func TestImpactSeverityIsDerivedNotAsserted(t *testing.T) {
	e := New()
	p1, _ := e.Propose("a", "small", []string{"e"},
		[]Mutation{{Kind: ChangeWeight, Target: "w", Value: 0.02}}, 1)
	an1, _ := e.Analyze(p1.ID, 1)
	if an1.Impact.Severity != "LOW" {
		t.Fatalf("a tiny weight change should be LOW, got %s", an1.Impact.Severity)
	}
	p2, _ := e.Propose("a", "authority change", []string{"e"},
		[]Mutation{{Kind: ChangeSourceAuthority, Target: "flag-registry", Value: 0.4}}, 2)
	an2, _ := e.Analyze(p2.ID, 2)
	if an2.Impact.Severity != "HIGH" {
		t.Fatalf("a source authority change should be HIGH, got %s", an2.Impact.Severity)
	}
}

func TestProposalIDIsContentDerivedAndStable(t *testing.T) {
	e1 := New()
	e2 := New()
	m := []Mutation{{Kind: AddRule, Target: "r", Body: "b"}}
	a, _ := e1.Propose("a", "r", []string{"e"}, m, 5)
	b, _ := e2.Propose("a", "r", []string{"e"}, m, 5)
	if a.ID != b.ID {
		t.Fatalf("identical proposals must derive identical ids: %s vs %s", a.ID, b.ID)
	}
	c, _ := e2.Propose("a", "r", []string{"e"}, []Mutation{{Kind: AddRule, Target: "r", Body: "c"}}, 6)
	if c.ID == a.ID {
		t.Fatal("different content must derive a different id")
	}
}

func TestMutationOrderingInsideAProposalIsCanonical(t *testing.T) {
	e1, e2 := New(), New()
	m1 := Mutation{Kind: AddRule, Target: "a", Body: "1"}
	m2 := Mutation{Kind: AddRule, Target: "b", Body: "2"}
	p1, _ := e1.Propose("x", "r", []string{"e"}, []Mutation{m1, m2}, 1)
	p2, _ := e2.Propose("x", "r", []string{"e"}, []Mutation{m2, m1}, 1)
	if p1.ID != p2.ID {
		t.Fatal("mutation ordering must not change the proposal identity")
	}
}

func TestBackdatedGovernanceRejected(t *testing.T) {
	e := New()
	_, _ = e.Propose("a", "r", []string{"e"}, []Mutation{{Kind: AddRule, Target: "r", Body: "b"}}, 100)
	if _, err := e.Propose("a", "r2", []string{"e"}, []Mutation{{Kind: AddRule, Target: "s", Body: "b"}}, 50); !errors.Is(err, ErrNonMonotonicTick) {
		t.Fatalf("expected monotonic tick rejection, got %v", err)
	}
}

func TestLedgerIsHashChainedAndTamperEvident(t *testing.T) {
	e := New()
	fullCycle(t, e, "a", []Mutation{{Kind: AddRule, Target: "r", Body: "b"}}, 10, 10)
	if err := e.VerifyChain(); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.ledger[2].Actor = "impostor"
	e.mu.Unlock()
	if err := e.VerifyChain(); !errors.Is(err, ErrLedgerTampered) {
		t.Fatalf("expected tamper detection, got %v", err)
	}
}

func TestExplainNamesEveryTransition(t *testing.T) {
	e := New()
	p := fullCycle(t, e, "a", []Mutation{{Kind: AddRule, Target: "r", Body: "b"}}, 10, 10)
	lines, err := e.Explain(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 6 {
		t.Fatalf("expected the full pipeline in the explanation, got %d lines: %v", len(lines), lines)
	}
}

func TestRejectedProposalIsTerminalAndNeverApplies(t *testing.T) {
	e := New()
	p, _ := e.Propose("a", "r", []string{"e"}, []Mutation{{Kind: AddRule, Target: "r", Body: "b"}}, 1)
	if _, err := e.Reject(p.ID, "officer", "bad idea", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Analyze(p.ID, 3); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("a rejected proposal must be terminal, got %v", err)
	}
	if s := e.StateAt(100); len(s.Rules) != 0 {
		t.Fatalf("a rejected proposal must never appear in state: %v", s.Rules)
	}
}

func TestUnknownProposalIsAnError(t *testing.T) {
	e := New()
	if _, err := e.Analyze("kp-nope", 1); !errors.Is(err, ErrUnknownProposal) {
		t.Fatalf("expected unknown proposal, got %v", err)
	}
}

func TestRemoveRuleSupersedesTheAddThatCreatedIt(t *testing.T) {
	e := New()
	added := fullCycle(t, e, "a", []Mutation{{Kind: AddRule, Target: "r1", Body: "v"}}, 10, 10)
	if _, ok := e.StateAt(15).Rules["r1"]; !ok {
		t.Fatal("precondition")
	}
	removed := fullCycle(t, e, "b", []Mutation{{Kind: RemoveRule, Target: "r1"}}, 30, 20)
	if removed.Conflict.Blocking {
		t.Fatalf("removal of an active rule is supersession, not conflict: %+v", removed.Conflict)
	}
	if _, still := e.StateAt(35).Rules["r1"]; still {
		t.Fatal("the rule must be gone after the removal takes effect")
	}
	if _, ok := e.StateAt(15).Rules["r1"]; !ok {
		t.Fatal("the rule must still exist in the window before removal")
	}
	if got, _ := e.Proposal(added.ID); got.State != StateSuperseded {
		t.Fatalf("the original ADD must be superseded, got %s", got.State)
	}
}
