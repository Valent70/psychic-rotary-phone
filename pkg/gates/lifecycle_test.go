package gates

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/assurance/state"
)

var lat = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func extEvidence() []state.Evidence {
	return []state.Evidence{{
		ID: "ae:redteam-1", Class: state.ExternalRequired,
		Summary: "grey-box assessment of the tenant isolation boundary",
		Scope:   "key derivation and the request guard; no deployed datastore was in scope",
		At:      lat, ArtefactHash: "h1",
		Validator: state.Validator{ID: "redteam:acme", Name: "Acme Red Team",
			External: true, AttestedBy: "human:procurement-lead"},
	}}
}

func internalOnly() []state.Evidence {
	return []state.Evidence{{
		ID: "ae:self-1", Class: state.Internal, Summary: "our own tests pass",
		Scope: "everything", At: lat, ArtefactHash: "h1",
		Validator: state.Validator{ID: "veriqo-engineering", Name: "VERIQO engineering"},
	}}
}

func fresh(t *testing.T) *Lifecycle {
	t.Helper()
	l, err := NewLifecycle("G15", "veriqo-engineering")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestAGateCannotBeClosedByEditingAField. This is the whole point:
// OPEN does not reach VALIDATED, however the caller spells it.
func TestAGateCannotBeClosedByEditingAField(t *testing.T) {
	l := fresh(t)
	err := l.Advance(Validated, "human:engineer-1", lat, "status: CLOSED",
		extEvidence(), nil)
	if !errors.Is(err, ErrPhaseJump) {
		t.Fatalf("OPEN reached VALIDATED: %v", err)
	}
	if !strings.Contains(err.Error(), "READY -> TESTING -> FINDINGS") {
		t.Fatalf("the refusal does not name the path it skipped: %v", err)
	}
	if l.Phase() != Open {
		t.Fatalf("the refused move left the gate at %s", l.Phase())
	}
	// Every intermediate phase is refused too, not just the first.
	for _, p := range []Phase{Testing, Findings, Remediated, Retested} {
		if err := l.Advance(p, "human:engineer-1", lat, "shortcut", extEvidence(), nil); !errors.Is(err, ErrPhaseJump) {
			t.Fatalf("OPEN reached %s: %v", p, err)
		}
	}
}

// TestEnteringTestingRequiresSomebodyElse. READY is the last phase
// VERIQO can reach alone.
func TestEnteringTestingRequiresSomebodyElse(t *testing.T) {
	l := fresh(t)
	if err := l.Advance(Ready, "human:engineer-1", lat, "the auditor capsule builds",
		internalOnly(), nil); err != nil {
		t.Fatalf("READY was refused: %v", err)
	}
	err := l.Advance(Testing, "human:engineer-1", lat, "we are testing it ourselves",
		internalOnly(), nil)
	if !errors.Is(err, ErrPhaseExternal) {
		t.Fatalf("VERIQO entered TESTING alone: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot be performed by editing a field") {
		t.Fatalf("the refusal does not say what it is defending: %v", err)
	}
	// Evidence labelled external but produced by the implementer is
	// the same attack with a different label.
	self := extEvidence()
	self[0].Validator = state.Validator{ID: "veriqo-engineering", Name: "VERIQO engineering",
		External: true, AttestedBy: "human:procurement-lead"}
	if err := l.Advance(Testing, "human:engineer-1", lat, "external", self, nil); !errors.Is(err, ErrPhaseExternal) {
		t.Fatalf("the implementer entered TESTING as its own assessor: %v", err)
	}
	if err := l.Advance(Testing, "human:engineer-1", lat, "Acme began the assessment",
		extEvidence(), nil); err != nil {
		t.Fatalf("a genuine external assessor was refused: %v", err)
	}
}

// TestNothingFoundMustBeRecordedNotOmitted. "Nothing was found" and
// "nobody looked" are the two situations a green row conflates.
func TestNothingFoundMustBeRecordedNotOmitted(t *testing.T) {
	l := fresh(t)
	walk(t, l, Ready, Testing)
	if err := l.Advance(Findings, "human:engineer-1", lat, "assessment complete",
		extEvidence(), nil); err != nil {
		t.Fatalf("FINDINGS with no findings was refused: %v", err)
	}
	err := l.Advance(Remediated, "human:engineer-1", lat, "nothing to do",
		internalOnly(), nil)
	if !errors.Is(err, ErrNoRemediation) {
		t.Fatalf("a gate advanced past FINDINGS with no record at all: %v", err)
	}
	// The explicit finding-free record is accepted.
	if err := l.Advance(Remediated, "human:engineer-1", lat, "nothing to remediate",
		internalOnly(), []Finding{{ID: "F-NONE", Severity: "NONE",
			Summary: "the assessment produced no findings", Accepted: true,
			Rationale: "there is nothing to fix; recorded so that the absence is explicit"}}); err != nil {
		t.Fatalf("an explicit finding-free result was refused: %v", err)
	}
}

// TestAnOpenFindingBlocksRemediationAndValidation.
func TestAnOpenFindingBlocksRemediationAndValidation(t *testing.T) {
	l := fresh(t)
	walk(t, l, Ready, Testing)
	open := []Finding{
		{ID: "F-1", Severity: "HIGH", Summary: "a cross-tenant read was possible via the cache key"},
		{ID: "F-2", Severity: "LOW", Summary: "an error message leaked a tenant identifier"},
	}
	if err := l.Advance(Findings, "human:engineer-1", lat, "two findings", extEvidence(), open); err != nil {
		t.Fatal(err)
	}
	if err := l.Advance(Remediated, "human:engineer-1", lat, "fixed", internalOnly(), nil); !errors.Is(err, ErrFindingsUnmet) {
		t.Fatalf("remediation with open findings was accepted: %v", err)
	}
	// Addressing one is not enough.
	one := []Finding{{ID: "F-1", Severity: "HIGH", Summary: open[0].Summary,
		Addressed: "commit abc123"}}
	if err := l.Advance(Remediated, "human:engineer-1", lat, "fixed one", internalOnly(), one); !errors.Is(err, ErrFindingsUnmet) {
		t.Fatalf("remediation with one finding still open was accepted: %v", err)
	}
	// An accepted risk needs a rationale and is a decision, not a fix.
	bad := []Finding{{ID: "F-2", Severity: "LOW", Summary: open[1].Summary, Accepted: true}}
	if err := l.Advance(Remediated, "human:engineer-1", lat, "accepted", internalOnly(), bad); err == nil {
		t.Fatal("a risk was accepted with no rationale")
	}
	good := []Finding{{ID: "F-2", Severity: "LOW", Summary: open[1].Summary, Accepted: true,
		Rationale: "the identifier is not secret and the message is internal-only"}}
	if err := l.Advance(Remediated, "human:engineer-1", lat, "one fixed, one accepted",
		internalOnly(), good); err != nil {
		t.Fatalf("a properly closed finding set was refused: %v", err)
	}
}

// TestTheOnlyPathToValidatedRunsThroughRetest.
func TestTheOnlyPathToValidatedRunsThroughRetest(t *testing.T) {
	l := fresh(t)
	walk(t, l, Ready, Testing)
	if err := l.Advance(Findings, "human:engineer-1", lat, "one finding", extEvidence(),
		[]Finding{{ID: "F-1", Severity: "HIGH", Summary: "a real problem",
			Addressed: "commit abc123"}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Advance(Remediated, "human:engineer-1", lat, "fixed", internalOnly(), nil); err != nil {
		t.Fatal(err)
	}
	// The tempting jump: remediated, therefore validated.
	if err := l.Advance(Validated, "human:engineer-1", lat, "we fixed it", extEvidence(), nil); !errors.Is(err, ErrPhaseJump) {
		t.Fatalf("REMEDIATED reached VALIDATED: %v", err)
	}
	// Retest needs the outside party again.
	if err := l.Advance(Retested, "human:engineer-1", lat, "we checked our own fix",
		internalOnly(), nil); !errors.Is(err, ErrPhaseExternal) {
		t.Fatalf("VERIQO retested its own remediation: %v", err)
	}
	if err := l.Advance(Retested, "human:engineer-1", lat, "Acme confirmed the fix",
		extEvidence(), nil); err != nil {
		t.Fatal(err)
	}
	if err := l.Advance(Validated, "human:engineer-1", lat, "Acme issued a signed report",
		extEvidence(), nil); err != nil {
		t.Fatalf("a fully walked gate was refused validation: %v", err)
	}
	if !l.Closed() {
		t.Fatal("a validated gate does not report closed")
	}
	if !l.ExternallyTouched() {
		t.Fatal("a validated gate reports no external contribution")
	}
}

// TestLapsingIsEasyAndReopeningStartsAtOpen. Making withdrawal
// expensive is how systems keep stale assurance.
func TestLapsingIsEasyAndReopeningStartsAtOpen(t *testing.T) {
	l := fresh(t)
	walk(t, l, Ready, Testing)
	if err := l.Advance(Findings, "e", lat, "done", extEvidence(),
		[]Finding{{ID: "F-1", Severity: "LOW", Summary: "x", Addressed: "y"}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Lapse("human:engineer-1", lat, "the assessed version was superseded"); err != nil {
		t.Fatal(err)
	}
	if l.Phase() != Lapsed {
		t.Fatalf("phase = %s", l.Phase())
	}
	if err := l.Advance(Remediated, "e", lat, "carrying on", internalOnly(), nil); err == nil {
		t.Fatal("a lapsed gate advanced")
	}
	if err := l.Lapse("e", lat, ""); err == nil {
		t.Fatal("an unexplained lapse was accepted")
	}
	if err := l.Reopen("human:engineer-1", lat, "reassessing against the new version"); err != nil {
		t.Fatal(err)
	}
	if l.Phase() != Open {
		t.Fatalf("a reopened gate is at %s", l.Phase())
	}
	if len(l.Findings()) != 0 {
		t.Fatal("a reopened gate kept the previous assessment's findings")
	}
}

// TestOnlyReadyAndRemediatedAreSelfReachable.
func TestOnlyReadyAndRemediatedAreSelfReachable(t *testing.T) {
	want := map[Phase]bool{Open: true, Ready: true, Testing: false, Findings: false,
		Remediated: true, Retested: false, Validated: false}
	for p, w := range want {
		if p.SelfReachable() != w {
			t.Fatalf("%s.SelfReachable() = %v, want %v", p, p.SelfReachable(), w)
		}
	}
	if len(Phases()) != 7 {
		t.Fatalf("%d phases", len(Phases()))
	}
}

// TestALifecycleMustNameTheImplementer. Independence cannot be
// evaluated without knowing whose work is being assessed.
func TestALifecycleMustNameTheImplementer(t *testing.T) {
	if _, err := NewLifecycle("G15", ""); err == nil {
		t.Fatal("a lifecycle with no implementer was created")
	}
	if _, err := NewLifecycle("", "veriqo-engineering"); err == nil {
		t.Fatal("a lifecycle with no gate was created")
	}
}

// TestTheDescriptionStatesTheAbsenceOfOutsideWork.
func TestTheDescriptionStatesTheAbsenceOfOutsideWork(t *testing.T) {
	l := fresh(t)
	walk(t, l, Ready)
	if !strings.Contains(l.Describe(), "no party other than the implementer has contributed") {
		t.Fatalf("the description does not state the absence:\n%s", l.Describe())
	}
}

func walk(t *testing.T, l *Lifecycle, to ...Phase) {
	t.Helper()
	for _, p := range to {
		ev := internalOnly()
		if !p.SelfReachable() {
			ev = extEvidence()
		}
		if err := l.Advance(p, "human:engineer-1", lat, "walking the fixture", ev, nil); err != nil {
			t.Fatalf("advance to %s: %v", p, err)
		}
	}
}
