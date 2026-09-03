package fref

import (
	"errors"
	"strings"
	"testing"
)

func runForward(t *testing.T, subject string, stop Stage) *Execution {
	t.Helper()
	e, err := NewExecution(Forward, subject)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	for i, s := range Order(Forward) {
		b, _ := BindingFor(s)
		if err := e.Complete(s, b.Package, uint64(i+1), "h-"+string(s), ""); err != nil {
			t.Fatalf("Complete(%s): %v", s, err)
		}
		if s == stop {
			break
		}
	}
	return e
}

func runReverse(t *testing.T, subject string, stop Stage) *Execution {
	t.Helper()
	e, err := NewExecution(Reverse, subject)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	for i, s := range Order(Reverse) {
		b, _ := BindingFor(s)
		if err := e.Complete(s, b.Package, uint64(i+1), "h-"+string(s), ""); err != nil {
			t.Fatalf("Complete(%s): %v", s, err)
		}
		if s == stop {
			break
		}
	}
	return e
}

// --- The contract itself ---------------------------------------------

// TestEveryStageHasABinding proves the contract describes work something
// actually does. A stage with no binding is architecture fiction.
func TestEveryStageHasABinding(t *testing.T) {
	for _, d := range []Direction{Forward, Reverse} {
		for _, s := range Order(d) {
			b, ok := BindingFor(s)
			if !ok {
				t.Fatalf("%s stage %s has no contract binding", d, s)
			}
			if b.Package == "" || b.Authoritative == "" || b.FailClosed == "" {
				t.Fatalf("stage %s has an incomplete binding: %+v", s, b)
			}
		}
	}
}

// TestEveryStageStatesItsFailClosedBehaviour is the property the
// backlog's capability audit asks for at every stage.
func TestEveryStageStatesItsFailClosedBehaviour(t *testing.T) {
	// The property is positive: every stage must say what it REFUSES to
	// do when it cannot complete. A blacklist of weasel words would
	// misfire on honest negations ("never assumed independent"), so the
	// assertion is that a refusal verb is present, not that a suspicious
	// one is absent.
	refusals := []string{"rejected", "refused", "never", "cannot", "no ", "excluded", "defeats", "stays", "is not", "fails"}
	for _, b := range Bindings() {
		lower := strings.ToLower(b.FailClosed)
		found := false
		for _, r := range refusals {
			if strings.Contains(lower, r) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("stage %s does not state a refusal, so its failure behaviour is not fail-closed: %q",
				b.Stage, b.FailClosed)
		}
	}
}

func TestBothDirectionsHaveSevenStages(t *testing.T) {
	if len(Order(Forward)) != 7 || len(Order(Reverse)) != 7 {
		t.Fatalf("expected seven stages each, got %d and %d", len(Order(Forward)), len(Order(Reverse)))
	}
	if Order("SIDEWAYS") != nil {
		t.Fatal("an unknown direction has no stage order")
	}
}

// TestTrustPrecedesFinding is the forward invariant that matters most:
// a finding that has not passed through trust assessment is an opinion.
func TestTrustPrecedesFinding(t *testing.T) {
	order := Order(Forward)
	trust, finding := -1, -1
	for i, s := range order {
		switch s {
		case StageTrust:
			trust = i
		case StageFinding:
			finding = i
		}
	}
	if trust < 0 || finding < 0 || trust >= finding {
		t.Fatalf("TRUST must precede FINDING, got positions %d and %d", trust, finding)
	}
}

// TestQualificationPrecedesNextBest is the reverse invariant: you
// cannot say what to get next before you know what you have.
func TestQualificationPrecedesNextBest(t *testing.T) {
	order := Order(Reverse)
	qual, next := -1, -1
	for i, s := range order {
		switch s {
		case StageQualification:
			qual = i
		case StageNextBestEvidence:
			next = i
		}
	}
	if qual >= next {
		t.Fatalf("QUALIFICATION must precede NEXT_BEST_EVIDENCE, got %d and %d", qual, next)
	}
}

// --- Ordering enforcement --------------------------------------------

// TestStageCannotSkipAnEarlierStage is the contract doing its job.
func TestStageCannotSkipAnEarlierStage(t *testing.T) {
	e, _ := NewExecution(Forward, "P-1")
	if err := e.Complete(StageFinding, "veriqo/pkg/proof", 1, "h", ""); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("expected ErrOutOfOrder, got %v", err)
	}
	// And the refused stage did not land.
	if e.Reached(StageFinding) {
		t.Fatal("a refused stage must not be recorded")
	}
}

// TestReasoningCannotJumpStraightToDecision is the same rule at the far
// end: reasoning proposes, it does not decide.
func TestReasoningCannotJumpStraightToDecision(t *testing.T) {
	e, _ := NewExecution(Forward, "P-1")
	mustComplete(t, e, StageObservation, StageEvidence, StageKnowledge, StageReasoning)
	if err := e.Complete(StageDecision, "veriqo/pkg/proof", 5, "h", ""); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("reasoning must not reach decision directly, got %v", err)
	}
}

func TestStageCannotCompleteTwice(t *testing.T) {
	e, _ := NewExecution(Forward, "P-1")
	mustComplete(t, e, StageObservation)
	if err := e.Complete(StageObservation, "p", 2, "h", ""); !errors.Is(err, ErrStageRepeated) {
		t.Fatalf("expected ErrStageRepeated, got %v", err)
	}
}

func TestStageMustBelongToItsDirection(t *testing.T) {
	e, _ := NewExecution(Forward, "P-1")
	if err := e.Complete(StageProofObligations, "p", 1, "h", ""); !errors.Is(err, ErrUnknownStage) {
		t.Fatalf("a reverse stage in a forward run must be refused, got %v", err)
	}
	r, _ := NewExecution(Reverse, "P-1")
	if err := r.Complete(StageObservation, "p", 1, "h", ""); !errors.Is(err, ErrUnknownStage) {
		t.Fatalf("a forward stage in a reverse run must be refused, got %v", err)
	}
}

func TestExecutionRequiresASubject(t *testing.T) {
	if _, err := NewExecution(Forward, "  "); !errors.Is(err, ErrNoSubject) {
		t.Fatalf("expected ErrNoSubject, got %v", err)
	}
	if _, err := NewExecution("DIAGONAL", "P-1"); err == nil {
		t.Fatal("an unknown direction must be refused")
	}
}

// --- Completion ------------------------------------------------------

func TestCompleteForwardRunReachesDecision(t *testing.T) {
	e := runForward(t, "P-1", StageDecision)
	if err := e.RequireComplete(); err != nil {
		t.Fatalf("RequireComplete: %v", err)
	}
	if s, ok := e.FurthestStage(); !ok || s != StageDecision {
		t.Fatalf("expected DECISION, got %s", s)
	}
}

// TestReverseRunStoppingAtQualificationIsIncomplete is the diagnosis-
// without-direction failure the reverse path exists to prevent.
func TestReverseRunStoppingAtQualificationIsIncomplete(t *testing.T) {
	e := runReverse(t, "P-1", StageQualification)
	err := e.RequireComplete()
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("expected ErrIncomplete, got %v", err)
	}
	if !strings.Contains(err.Error(), string(StageNextBestEvidence)) {
		t.Fatalf("the error should name the terminal stage, got %q", err)
	}
}

func TestCompleteReverseRunReachesNextBestEvidence(t *testing.T) {
	e := runReverse(t, "P-1", StageNextBestEvidence)
	if err := e.RequireComplete(); err != nil {
		t.Fatalf("RequireComplete: %v", err)
	}
}

func TestEmptyExecutionHasNoFurthestStage(t *testing.T) {
	e, _ := NewExecution(Forward, "P-1")
	if _, ok := e.FurthestStage(); ok {
		t.Fatal("an execution with no completed stage has no furthest stage")
	}
	if err := e.RequireComplete(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("expected ErrIncomplete, got %v", err)
	}
}

// --- Contract drift --------------------------------------------------

// TestExecutionInTheWrongPackageIsDrift is how a second implementation
// of an owned stage surfaces.
func TestExecutionInTheWrongPackageIsDrift(t *testing.T) {
	e, _ := NewExecution(Forward, "P-1")
	mustComplete(t, e, StageObservation, StageEvidence, StageKnowledge, StageReasoning)
	if err := e.Complete(StageTrust, "veriqo/pkg/some/other/trustengine", 5, "h", ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	err := e.VerifyAgainstContract()
	if err == nil {
		t.Fatal("a stage run outside its bound package must be reported as drift")
	}
	if !strings.Contains(err.Error(), "trustcalc") {
		t.Fatalf("the drift report should name the contract's package, got %q", err)
	}
}

func TestContractCompliantExecutionHasNoDrift(t *testing.T) {
	if err := runForward(t, "P-1", StageDecision).VerifyAgainstContract(); err != nil {
		t.Fatalf("a contract-compliant run must not report drift: %v", err)
	}
}

// TestUnrecordedPackageIsNotDrift keeps the check useful rather than
// noisy: a record that names no package is not asserting anything.
func TestUnrecordedPackageIsNotDrift(t *testing.T) {
	e, _ := NewExecution(Forward, "P-1")
	mustComplete(t, e, StageObservation)
	if err := e.VerifyAgainstContract(); err != nil {
		t.Fatalf("an unrecorded package must not count as drift: %v", err)
	}
}

// --- Closure ---------------------------------------------------------

func TestClosureHoldsWhenBothDirectionsAgree(t *testing.T) {
	fwd := runForward(t, "P-1", StageDecision)
	rev := runReverse(t, "P-1", StageNextBestEvidence)

	c, err := Close(fwd, rev, []string{"EV-1", "EV-2"}, []string{"EV-2", "EV-1"})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !c.Holds {
		t.Fatalf("closure should hold: %s", c.Explain())
	}
	if !strings.Contains(c.Explain(), "Closure holds") {
		t.Fatalf("unexpected explanation: %q", c.Explain())
	}
}

// TestUnrequiredEvidenceBreaksClosure is the failure the closure check
// exists to catch: a finding resting on evidence no obligation asked for.
func TestUnrequiredEvidenceBreaksClosure(t *testing.T) {
	fwd := runForward(t, "P-1", StageDecision)
	rev := runReverse(t, "P-1", StageNextBestEvidence)

	c, err := Close(fwd, rev, []string{"EV-1", "EV-ROGUE"}, []string{"EV-1"})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.Holds {
		t.Fatal("closure must not hold when the finding rests on unrequired evidence")
	}
	if len(c.Unrequired) != 1 || c.Unrequired[0] != "EV-ROGUE" {
		t.Fatalf("the unrequired evidence must be named, got %v", c.Unrequired)
	}
	if !strings.Contains(c.Explain(), "no proof obligation required") {
		t.Fatalf("the explanation must say why, got %q", c.Explain())
	}
}

// TestUnmetObligationBreaksClosure is the other half: an obligation the
// forward run never satisfied.
func TestUnmetObligationBreaksClosure(t *testing.T) {
	fwd := runForward(t, "P-1", StageDecision)
	rev := runReverse(t, "P-1", StageNextBestEvidence)

	c, _ := Close(fwd, rev, []string{"EV-1"}, []string{"EV-1", "EV-MISSING"})
	if c.Holds || len(c.Unmet) != 1 {
		t.Fatalf("an unmet obligation must break closure, got %+v", c)
	}
	if !strings.Contains(c.Explain(), "never met") {
		t.Fatalf("the explanation must say so, got %q", c.Explain())
	}
}

func TestClosureRequiresTheSameSubject(t *testing.T) {
	fwd := runForward(t, "P-1", StageDecision)
	rev := runReverse(t, "P-2", StageNextBestEvidence)
	if _, err := Close(fwd, rev, nil, nil); !errors.Is(err, ErrNoClosure) {
		t.Fatalf("expected ErrNoClosure, got %v", err)
	}
}

// TestClosureRequiresTheForwardEvidenceSetToBeSettled is the corrected
// precondition.
//
// Closure needs the evidence the forward run relies on, and that is
// settled at TRUST. It must NOT require a completed forward run: doing
// so was what forced the reverse direction to run after the decision.
func TestClosureRequiresTheForwardEvidenceSetToBeSettled(t *testing.T) {
	// Reached only REASONING: the evidence set is not yet settled.
	tooEarly := runForward(t, "P-1", StageReasoning)
	rev := runReverse(t, "P-1", StageNextBestEvidence)
	if _, err := Close(tooEarly, rev, nil, nil); !errors.Is(err, ErrStageNotReached) {
		t.Fatalf("expected ErrStageNotReached, got %v", err)
	}

	// Reached TRUST but not DECISION: closure is now computable, which
	// is what makes the reverse direction a gate rather than an audit.
	settled := runForward(t, "P-1", StageTrust)
	c, err := Close(settled, rev, []string{"EV-1"}, []string{"EV-1"})
	if err != nil {
		t.Fatalf("closure must be computable before the decision exists: %v", err)
	}
	if !c.Holds {
		t.Fatalf("closure should hold: %s", c.Explain())
	}
}

// TestClosureRequiresTheReverseRunToHaveNamedItsEvidence: a reverse run
// that never reached REQUIRED_EVIDENCE has nothing to compare against.
func TestClosureRequiresRequiredEvidenceStage(t *testing.T) {
	fwd := runForward(t, "P-1", StageDecision)
	rev := runReverse(t, "P-1", StageProofObligations)
	if _, err := Close(fwd, rev, nil, nil); !errors.Is(err, ErrStageNotReached) {
		t.Fatalf("expected ErrStageNotReached, got %v", err)
	}
}

func TestClosureNeedsOneOfEachDirection(t *testing.T) {
	a := runForward(t, "P-1", StageDecision)
	b := runForward(t, "P-1", StageDecision)
	if _, err := Close(a, b, nil, nil); err == nil {
		t.Fatal("two forward runs cannot close")
	}
}

// --- Stage outputs ---------------------------------------------------

func TestOutputOfReachedAndUnreachedStages(t *testing.T) {
	e := runForward(t, "P-1", StageTrust)
	h, err := e.OutputOf(StageTrust)
	if err != nil || h != "h-TRUST" {
		t.Fatalf("expected the pinned trust output, got %q/%v", h, err)
	}
	if _, err := e.OutputOf(StageDecision); !errors.Is(err, ErrStageNotReached) {
		t.Fatalf("expected ErrStageNotReached, got %v", err)
	}
}

func TestRecordsReturnsACopy(t *testing.T) {
	e := runForward(t, "P-1", StageEvidence)
	e.Records()[0].Note = "rewritten"
	if e.Records()[0].Note == "rewritten" {
		t.Fatal("Records must return a copy")
	}
}

func mustComplete(t *testing.T, e *Execution, stages ...Stage) {
	t.Helper()
	for i, s := range stages {
		b, _ := BindingFor(s)
		if err := e.Complete(s, b.Package, uint64(i+1), "h-"+string(s), ""); err != nil {
			t.Fatalf("Complete(%s): %v", s, err)
		}
	}
}
