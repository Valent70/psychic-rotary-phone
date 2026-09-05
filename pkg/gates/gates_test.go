package gates

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func register(t *testing.T) *Register {
	t.Helper()
	r, err := NewRegister(Veriqo())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func internalEvidence() Evidence {
	return Evidence{Description: "the test suite passes", Ref: "scripts/verify.sh",
		At: now, ProducedBy: "VERIQO engineering"}
}

func externalEvidence(by string) Evidence {
	return Evidence{Description: "a report", Ref: "reports/2026-09", At: now,
		ProducedBy: by, External: true}
}

// TestAnExternalGateCannotBeSatisfiedByVeriqosOwnEvidence.
//
// This is the register's central rule: a gate whose whole point is
// that somebody else looks cannot be closed by the party being looked
// at.
func TestAnExternalGateCannotBeSatisfiedByVeriqosOwnEvidence(t *testing.T) {
	r := register(t)
	err := r.Set("G4", Satisfied, "our security tests pass", internalEvidence())
	if !errors.Is(err, ErrSelfSatisfied) {
		t.Fatalf("VERIQO closed its own penetration-test gate: %v", err)
	}
	// And evidence marked external while naming VERIQO is refused at
	// the evidence, not only at the gate.
	fake := externalEvidence("VERIQO red team")
	if err := fake.Validate(); !errors.Is(err, ErrSelfSatisfied) {
		t.Fatalf("VERIQO-produced evidence was accepted as external: %v", err)
	}
	// A genuine outside party works.
	if err := r.Set("G4", Satisfied, "pentest complete",
		externalEvidence("an accredited testing firm")); err != nil {
		t.Fatalf("a genuine external report was refused: %v", err)
	}
}

// TestAGateCannotBeSatisfiedWithNoEvidence.
func TestAGateCannotBeSatisfiedWithNoEvidence(t *testing.T) {
	r := register(t)
	if err := r.Set("G3", Satisfied, "it is deployed"); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("a gate was satisfied by assertion: %v", err)
	}
}

// TestBlockedExternalStillBlocks. An honest blocker still blocks.
func TestBlockedExternalStillBlocks(t *testing.T) {
	if !BlockedExternal.Blocking() {
		t.Fatal("BLOCKED_EXTERNAL does not block a release")
	}
	if !NotStarted.Blocking() || !InProgress.Blocking() || !Regressed.Blocking() {
		t.Fatal("a non-satisfied state does not block")
	}
	if Satisfied.Blocking() {
		t.Fatal("SATISFIED blocks")
	}
}

// TestABlockMustSayWhatIsBlockingIt.
func TestABlockMustSayWhatIsBlockingIt(t *testing.T) {
	r := register(t)
	if err := r.Set("G4", BlockedExternal, ""); err == nil {
		t.Fatal("a gate was blocked with no stated reason")
	}
}

// TestASatisfiedGateThatStopsHoldingIsREGRESSED.
//
// A gate that quietly returned to NOT_STARTED would look like work
// that had never begun.
func TestASatisfiedGateThatStopsHoldingIsRegressed(t *testing.T) {
	r := register(t)
	if err := r.Set("G4", Satisfied, "pentest complete",
		externalEvidence("a testing firm")); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("G4", InProgress, "the report expired"); err != nil {
		t.Fatal(err)
	}
	s, _ := r.Status("G4")
	if s.State != Regressed {
		t.Fatalf("a previously satisfied gate moved to %s", s.State)
	}
	if len(r.Regressions()) != 1 {
		t.Fatalf("Regressions = %v", r.Regressions())
	}
	if !strings.Contains(r.Report(), "REGRESSED from a previously satisfied state") {
		t.Fatalf("the report does not surface the regression:\n%s", r.Report())
	}
}

// TestAnExternalGateNamesWhoCouldSatisfyIt. "Somebody else" is not a
// plan.
func TestAnExternalGateNamesWhoCouldSatisfyIt(t *testing.T) {
	for _, g := range Veriqo() {
		if g.RequiresExternalParty && strings.TrimSpace(g.WhoCouldSatisfy) == "" {
			t.Errorf("%s requires an outside party and does not say which kind", g.ID)
		}
	}
	bad := Gate{ID: "X", Name: "n", What: "w", Why: "y", RequiresExternalParty: true}
	if err := bad.Validate(); err == nil {
		t.Fatal("a gate requiring an unnamed outside party was accepted")
	}
}

// TestEveryGateSaysWhatAndWhy. A gate a reader cannot evaluate is a
// checkbox.
func TestEveryGateSaysWhatAndWhy(t *testing.T) {
	for _, g := range Veriqo() {
		if strings.TrimSpace(g.What) == "" {
			t.Errorf("%s does not say what it requires", g.ID)
		}
		if strings.TrimSpace(g.Why) == "" {
			t.Errorf("%s does not say what goes wrong without it", g.ID)
		}
	}
}

// TestTheOriginalEightSurviveAsGates. They are not deleted when
// closed.
func TestTheOriginalEightSurviveAsGates(t *testing.T) {
	r := register(t)
	for _, id := range []string{"G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8"} {
		if _, err := r.Gate(id); err != nil {
			t.Errorf("the original blocker %s is not in the register: %v", id, err)
		}
	}
	if len(Veriqo()) != 20 {
		t.Fatalf("%d gates; the specification names twenty", len(Veriqo()))
	}
}

// TestTheCurrentRegisterIsHonest. Every gate is in a non-satisfied
// state, because none of them is satisfied.
func TestTheCurrentRegisterIsHonest(t *testing.T) {
	r, err := VeriqoRegister()
	if err != nil {
		t.Fatal(err)
	}
	ready, reasons := r.ProductionReady()
	if ready {
		t.Fatal("THIS REPOSITORY REPORTS ITSELF PRODUCTION READY")
	}
	if len(reasons) != 20 {
		t.Fatalf("%d gates blocking, want all 20", len(reasons))
	}
	// Every gate carries a note explaining its state, so a reader can
	// disagree with a specific claim.
	for _, g := range r.Gates() {
		s, _ := r.Status(g.ID)
		if s.Note == "" {
			t.Errorf("%s is %s with no explanation", g.ID, s.State)
		}
	}
	// And thirteen need an outside party.
	if n := len(r.ExternallyBlocked()); n != 13 {
		t.Fatalf("%d gates need an outside party, want 13", n)
	}
}

// TestNoGateIsSatisfiedByDefault. The zero state is NOT_STARTED and
// it blocks.
func TestNoGateIsSatisfiedByDefault(t *testing.T) {
	r := register(t)
	for _, g := range r.Gates() {
		s, _ := r.Status(g.ID)
		if s.State != NotStarted {
			t.Errorf("%s starts at %s", g.ID, s.State)
		}
	}
	ready, _ := r.ProductionReady()
	if ready {
		t.Fatal("an untouched register reports production ready")
	}
}

// TestEvidenceMustNameWhoProducedIt.
func TestEvidenceMustNameWhoProducedIt(t *testing.T) {
	e := Evidence{Description: "d", Ref: "r", At: now}
	if err := e.Validate(); !errors.Is(err, ErrNoAttestor) {
		t.Fatalf("unattributed evidence was accepted: %v", err)
	}
}

// TestDuplicateGatesAreRefused.
func TestDuplicateGatesAreRefused(t *testing.T) {
	if _, err := NewRegister(append(Veriqo(), Veriqo()[0])); err == nil {
		t.Fatal("a duplicate gate was accepted")
	}
}

// TestAnUnknownGateIsRefused.
func TestAnUnknownGateIsRefused(t *testing.T) {
	r := register(t)
	if err := r.Set("G99", Satisfied, "n", externalEvidence("x")); !errors.Is(err, ErrUnknownGate) {
		t.Fatal("an unknown gate was set")
	}
}
