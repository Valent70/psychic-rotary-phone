package fref

import (
	"errors"
	"strings"
	"testing"
)

// TestReverseProofGatesResolution is the defect this file was written
// for, stated as a test.
//
// A case that reaches resolution without the reverse direction having
// closed must be refused. Before this law existed, that ordering was
// not merely permitted — it was what the integration test actually did.
func TestReverseProofGatesResolution(t *testing.T) {
	s, err := NewSequence("CASE-1")
	if err != nil {
		t.Fatalf("NewSequence: %v", err)
	}
	// A forward-only run: case, scope, evidence, hypothesis, then
	// straight at resolution.
	for _, step := range []Step{StepCase, StepScope, StepEvidence, StepHypothesis} {
		if err := s.CompleteGated(step); err != nil {
			t.Fatalf("CompleteGated(%s): %v", step, err)
		}
	}
	err = s.CompleteGated(StepCaseResolution)
	if !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("expected ErrSequenceGap, got %v", err)
	}
	for _, required := range []string{"REVERSE_PROOF", "PROOF_SEAL", "FINDING", "AUTHORIZED_DECISION"} {
		if !strings.Contains(err.Error(), required) {
			t.Fatalf("the refusal must name %s as a missing gate, got %q", required, err)
		}
	}
}

// TestTheLawfulOrderCompletes proves the law is satisfiable — a rule
// nothing can pass is not a rule.
func TestTheLawfulOrderCompletes(t *testing.T) {
	s, _ := NewSequence("CASE-1")
	for _, step := range CanonicalSequence() {
		if err := s.CompleteGated(step); err != nil {
			t.Fatalf("the canonical order must complete: %s: %v", step, err)
		}
	}
	if len(s.Taken()) != len(CanonicalSequence()) {
		t.Fatalf("expected %d steps, got %d", len(CanonicalSequence()), len(s.Taken()))
	}
}

// TestReverseProofPrecedesQualificationAndSeal is the epistemic order:
// obligations are fixed before the verdict, and the verdict before the
// seal. Fixing them afterwards would let the obligations be chosen to
// fit the answer.
func TestReverseProofPrecedesQualificationAndSeal(t *testing.T) {
	if !MustPrecede(StepReverseProof, StepQualification) {
		t.Fatal("REVERSE_PROOF must precede QUALIFICATION")
	}
	if !MustPrecede(StepQualification, StepProofSeal) {
		t.Fatal("QUALIFICATION must precede PROOF_SEAL")
	}
	if !MustPrecede(StepProofSeal, StepFinding) {
		t.Fatal("PROOF_SEAL must precede FINDING")
	}
	if !MustPrecede(StepFinding, StepAuthorizedDecision) {
		t.Fatal("FINDING must precede AUTHORIZED_DECISION")
	}
	if !MustPrecede(StepAuthorizedDecision, StepCaseResolution) {
		t.Fatal("AUTHORIZED_DECISION must precede CASE_RESOLUTION")
	}
}

// TestTheForbiddenOrderIsForbidden names the inverted sequence the
// reviewer found, and asserts it cannot be recorded.
func TestTheForbiddenOrderIsForbidden(t *testing.T) {
	// FORWARD -> DECISION -> RESOLUTION -> REVERSE
	s, _ := NewSequence("CASE-1")
	for _, step := range []Step{
		StepCase, StepScope, StepEvidence, StepHypothesis, StepReverseProof,
		StepQualification, StepProofSeal, StepFinding, StepAuthorizedDecision, StepCaseResolution,
	} {
		if err := s.CompleteGated(step); err != nil {
			t.Fatalf("CompleteGated(%s): %v", step, err)
		}
	}
	// Now try to run the reverse direction again, after resolution.
	if err := s.Complete(StepReverseProof); !errors.Is(err, ErrStepRepeated) {
		t.Fatalf("expected ErrStepRepeated, got %v", err)
	}

	// And a fresh sequence that resolves then reverses is refused on the
	// gate, not merely on repetition.
	fresh, _ := NewSequence("CASE-2")
	for _, step := range []Step{StepCase, StepScope, StepEvidence, StepHypothesis} {
		mustStep(t, fresh, step)
	}
	if err := fresh.CompleteGated(StepCaseResolution); err == nil {
		t.Fatal("resolution before the reverse direction must be refused")
	}
}

// TestAStepCannotFollowALaterStep is the general ordering rule.
func TestAStepCannotFollowALaterStep(t *testing.T) {
	s, _ := NewSequence("CASE-1")
	mustStep(t, s, StepCase)
	mustStep(t, s, StepScope)
	mustStep(t, s, StepEvidence)
	mustStep(t, s, StepHypothesis)
	mustStep(t, s, StepReverseProof)
	mustStep(t, s, StepQualification)
	mustStep(t, s, StepProofSeal)

	// EVIDENCE is earlier in the law than PROOF_SEAL, which has already
	// been taken. Adding more evidence after sealing is a different act
	// and must not be recorded as this step.
	if err := s.Complete(StepEvidence); err == nil {
		t.Fatal("an earlier step must not be recorded after a later one")
	}
}

func TestUnknownStepIsRefused(t *testing.T) {
	s, _ := NewSequence("CASE-1")
	if err := s.Complete(Step("INVENTED")); !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("expected ErrUnknownStep, got %v", err)
	}
}

func TestSequenceRequiresASubject(t *testing.T) {
	if _, err := NewSequence("  "); !errors.Is(err, ErrNoSequenceSubject) {
		t.Fatalf("expected ErrNoSequenceSubject, got %v", err)
	}
}

// TestEveryStepAfterTheFirstIsGated: a step with no gates can be reached
// from anywhere, and only CASE should be reachable from nothing.
func TestEveryStepAfterTheFirstIsGated(t *testing.T) {
	for _, step := range CanonicalSequence() {
		gates := RequiredGates(step)
		if step == StepCase {
			if len(gates) != 0 {
				t.Fatalf("CASE is where a case begins and must have no gates, got %v", gates)
			}
			continue
		}
		if len(gates) == 0 {
			t.Fatalf("step %s has no gates and is therefore reachable from anywhere", step)
		}
	}
}

// TestResolutionHasFourGates states the specific requirement in the
// mandate, so it cannot be quietly weakened to one.
func TestResolutionHasFourGates(t *testing.T) {
	gates := RequiredGates(StepCaseResolution)
	if len(gates) != 4 {
		t.Fatalf("CASE_RESOLUTION must be gated on four steps, got %v", gates)
	}
	want := map[Step]bool{
		StepReverseProof: true, StepProofSeal: true,
		StepFinding: true, StepAuthorizedDecision: true,
	}
	for _, g := range gates {
		if !want[g] {
			t.Fatalf("unexpected gate on CASE_RESOLUTION: %s", g)
		}
	}
}

// --- Verifying a recorded stream --------------------------------------

// TestVerifyEventOrderCatchesTheArtefactDefect is the after-the-fact
// half, run against the exact ordering the reviewer found.
func TestVerifyEventOrderCatchesTheArtefactDefect(t *testing.T) {
	broken := []string{
		"case.opened", "case.scoped", "case.evidence_pinned",
		"case.hypothesis_recorded", "case.claim_registered",
		"case.hypothesis_tested", "case.qualification_begun",
		"case.proof_attached", "case.resolved", "proof.sealed",
	}
	violations := VerifyEventOrder(broken)
	if len(violations) == 0 {
		t.Fatal("case.resolved before proof.sealed must be reported as a violation")
	}
	found := false
	for _, v := range violations {
		if v.Recorded == StepCaseResolution && v.Expected == StepProofSeal {
			found = true
			if !strings.Contains(v.String(), "requires PROOF_SEAL before CASE_RESOLUTION") {
				t.Fatalf("the violation must explain itself, got %q", v.String())
			}
		}
	}
	if !found {
		t.Fatalf("the specific defect was not reported, got %v", violations)
	}
}

// TestVerifyEventOrderAcceptsALawfulStream proves the verifier is not
// simply always unhappy.
func TestVerifyEventOrderAcceptsALawfulStream(t *testing.T) {
	lawful := []string{
		"case.opened", "case.scoped", "case.evidence_pinned",
		"case.hypothesis_recorded", "case.hypothesis_tested",
		"qualification.reverse_closed", "case.qualification_begun",
		"proof.sealed", "case.proof_attached",
		"claim.finding_founded", "case.decision_authorized", "case.resolved",
	}
	if v := VerifyEventOrder(lawful); len(v) != 0 {
		t.Fatalf("a lawful stream must produce no violations, got %v", v)
	}
}

// TestNonSequenceEventsAreSkipped keeps the verifier useful rather than
// noisy: an actor being recorded is not a sequence step.
func TestNonSequenceEventsAreSkipped(t *testing.T) {
	stream := []string{
		"case.opened", "case.domain_state_synced", "case.scoped",
		"case.claim_registered", "case.evidence_pinned",
	}
	if v := VerifyEventOrder(stream); len(v) != 0 {
		t.Fatalf("unmapped events must be skipped, got %v", v)
	}
	if _, ok := EventStepFor("case.domain_state_synced"); ok {
		t.Fatal("a domain state sync is not a sequence step")
	}
	if st, ok := EventStepFor("case.resolved"); !ok || st != StepCaseResolution {
		t.Fatal("case.resolved must map to CASE_RESOLUTION")
	}
}

func TestExplainSequenceStatesTheRule(t *testing.T) {
	out := ExplainSequence()
	for _, s := range CanonicalSequence() {
		if !strings.Contains(out, string(s)) {
			t.Fatalf("step %s missing from the explanation", s)
		}
	}
	if !strings.Contains(out, "constitutional gate, not a retrospective audit") {
		t.Fatalf("the explanation must state the rule it exists for, got %q", out)
	}
}

func mustStep(t *testing.T, s *Sequence, step Step) {
	t.Helper()
	if err := s.Complete(step); err != nil {
		t.Fatalf("Complete(%s): %v", step, err)
	}
}
