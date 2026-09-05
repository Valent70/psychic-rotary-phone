package state

import (
	"errors"
	"testing"
	"time"

	"veriqo/pkg/identity"
)

var at = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func engineer() identity.Principal {
	return identity.Principal{ID: "human:engineer-1", Kind: identity.Human, TenantID: "t-veriqo",
		NotBefore: at.Add(-time.Hour), NotAfter: at.Add(time.Hour)}
}

func internalEvidence(class Class) Evidence {
	return Evidence{
		ID: "ae:internal-1", Class: class, Summary: "the package's own tests and attacks pass",
		Scope: "pkg/ledger, this commit", At: at, ArtefactHash: "abc123",
		Validator: Validator{ID: "veriqo-engineering", Name: "VERIQO engineering"},
	}
}

func externalEvidence(class Class) Evidence {
	return Evidence{
		ID: "ae:external-1", Class: class,
		Summary: "grey-box assessment found no way to modify a committed record undetected",
		Scope:   "pkg/ledger append and reopen paths only; the checkpoint anchor was out of scope",
		At:      at, ArtefactHash: "abc123",
		Exceptions: []string{"the anchor interface has no implementation and was not assessed"},
		Validator: Validator{ID: "assessor:acme-security", Name: "Acme Security",
			External: true, AttestedBy: "human:procurement-lead"},
	}
}

func climbTo(t *testing.T, m *Machine, target State) {
	t.Helper()
	for m.State() < target {
		next := m.State() + 1
		var ev Evidence
		switch next.RequiredEvidence() {
		case Internal:
			ev = internalEvidence(Internal)
		case ProductionRequired:
			ev = externalEvidence(ProductionRequired)
			ev.Period = "90 days"
		default:
			ev = externalEvidence(next.RequiredEvidence())
		}
		if err := m.Promote(next, engineer(), at, "climbing the fixture", ev); err != nil {
			t.Fatalf("promote to %s: %v", next, err)
		}
	}
}

// TestTheZeroValueIsUndefined. A struct nobody populated must not read
// as assured.
func TestTheZeroValueIsUndefined(t *testing.T) {
	var s State
	if s != Undefined {
		t.Fatalf("the zero state is %s", s)
	}
	m, err := New("pkg/ledger", "veriqo-engineering")
	if err != nil {
		t.Fatal(err)
	}
	if m.State() != Undefined {
		t.Fatalf("a fresh machine is at %s", m.State())
	}
}

// TestAMachineMustNameItsImplementer. Law 11 is a comparison against
// that party; it cannot be evaluated without one.
func TestAMachineMustNameItsImplementer(t *testing.T) {
	if _, err := New("pkg/ledger", ""); err == nil {
		t.Fatal("a machine with no implementer was created")
	}
	if _, err := New("", "veriqo-engineering"); err == nil {
		t.Fatal("a machine with no subject was created")
	}
}

// TestNoStateJump is the ladder's whole point.
func TestNoStateJump(t *testing.T) {
	m, _ := New("pkg/ledger", "veriqo-engineering")
	err := m.Promote(InternallyAssured, engineer(), at, "it is good",
		internalEvidence(Internal))
	if !errors.Is(err, ErrStateJump) {
		t.Fatalf("a jump from UNDEFINED to INTERNALLY_ASSURED gave %v", err)
	}
	// And the error names what was skipped, so the reader knows which
	// questions went unanswered.
	if err == nil || !contains(err.Error(), "SPECIFIED") || !contains(err.Error(), "IMPLEMENTED") {
		t.Fatalf("the refusal does not name the skipped rungs: %v", err)
	}
}

func contains(h, n string) bool {
	return len(h) >= len(n) && (func() bool {
		for i := 0; i+len(n) <= len(h); i++ {
			if h[i:i+len(n)] == n {
				return true
			}
		}
		return false
	})()
}

// TestTheLastSelfReachableRungIsInternallyAssured. This is the
// boundary Law 11 defends, and it must be stated as a property rather
// than left to the reader to infer from the constants.
func TestTheLastSelfReachableRungIsInternallyAssured(t *testing.T) {
	for _, s := range States() {
		want := s <= InternallyAssured
		if s.SelfReachable() != want {
			t.Fatalf("%s.SelfReachable() = %v", s, s.SelfReachable())
		}
	}
	if !InternallyAssured.SelfReachable() {
		t.Fatal("INTERNALLY_ASSURED is not self-reachable")
	}
	if ReadyForExternalTest.RequiredEvidence().NeedsIndependentParty() {
		t.Fatal("preparing for an external test was made to require an external party, " +
			"which would make the rung unreachable and the ladder unusable")
	}
	if !ExternallyTested.RequiredEvidence().NeedsIndependentParty() {
		t.Fatal("EXTERNALLY_TESTED does not require an independent party")
	}
}

// TestLaw11RefusesSelfCertification is the central test of this
// package. An implementer with impeccable internal evidence cannot
// reach EXTERNALLY_TESTED.
func TestLaw11RefusesSelfCertification(t *testing.T) {
	m, _ := New("pkg/ledger", "veriqo-engineering")
	climbTo(t, m, ReadyForExternalTest)

	// The attempt: internal evidence, however thorough, for a rung
	// defined by somebody else's work.
	err := m.Promote(ExternallyTested, engineer(), at,
		"our adversarial suite is extremely thorough", internalEvidence(Internal))
	if !errors.Is(err, ErrWrongClass) && !errors.Is(err, ErrSelfCertified) {
		t.Fatalf("internal evidence promoted a control to EXTERNALLY_TESTED: %v", err)
	}
	if m.State() != ReadyForExternalTest {
		t.Fatalf("the refused promotion moved the machine to %s", m.State())
	}

	// The subtler attempt: evidence LABELLED external, produced by the
	// implementer.
	self := externalEvidence(ExternalRequired)
	self.Validator = Validator{ID: "veriqo-engineering", Name: "VERIQO engineering",
		External: true, AttestedBy: "human:procurement-lead"}
	if err := m.Promote(ExternallyTested, engineer(), at, "we tested ourselves externally",
		self); !errors.Is(err, ErrSelfCertified) {
		t.Fatalf("the implementer validated its own control: %v", err)
	}

	// The correct promotion is accepted, so the test is not passing by
	// refusing everything.
	if err := m.Promote(ExternallyTested, engineer(), at,
		"Acme Security completed a grey-box assessment",
		externalEvidence(ExternalRequired)); err != nil {
		t.Fatalf("a genuine external promotion was refused: %v", err)
	}
}

// TestAValidatorCannotAttestToItsOwnIndependence.
func TestAValidatorCannotAttestToItsOwnIndependence(t *testing.T) {
	v := Validator{ID: "assessor:acme", Name: "Acme", External: true, AttestedBy: "assessor:acme"}
	if err := v.Validate(); !errors.Is(err, ErrNotIndependent) {
		t.Fatalf("self-attestation validated: %v", err)
	}
	v.AttestedBy = ""
	if err := v.Validate(); !errors.Is(err, ErrNotIndependent) {
		t.Fatalf("an unattested external claim validated: %v", err)
	}
	v.AttestedBy = "human:procurement-lead"
	if err := v.Validate(); err != nil {
		t.Fatalf("a properly attested validator was refused: %v", err)
	}
	if !v.IndependentOf("veriqo-engineering") {
		t.Fatal("an attested external validator is not independent of the implementer")
	}
	if v.IndependentOf("assessor:acme") {
		t.Fatal("a validator is independent of itself")
	}
}

// TestEvidenceMustStateItsScope. An unscoped external report is read
// as covering everything, which is how a narrow assessment becomes a
// broad claim.
func TestEvidenceMustStateItsScope(t *testing.T) {
	e := externalEvidence(ExternalRequired)
	e.Scope = ""
	if err := e.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("unscoped evidence validated: %v", err)
	}
	e = externalEvidence(ExternalRequired)
	e.ArtefactHash = ""
	if err := e.Validate(); err == nil {
		t.Fatal("evidence with no artefact hash validated; a report could be reused for a " +
			"later version")
	}
	e = externalEvidence(ExternalRequired)
	e.At = time.Time{}
	if err := e.Validate(); err == nil {
		t.Fatal("undated evidence validated")
	}
}

// TestOperationalEvidenceMustStateItsPeriod. "It ran" without "for how
// long" is not operational evidence.
func TestOperationalEvidenceMustStateItsPeriod(t *testing.T) {
	e := externalEvidence(ProductionRequired)
	if err := e.Validate(); err == nil {
		t.Fatal("operational evidence with no period validated")
	}
	e.Period = "90 days at production load"
	if err := e.Validate(); err != nil {
		t.Fatalf("operational evidence with a period was refused: %v", err)
	}
}

// TestDemotionIsCheapAndPromotionIsNot. Making the honest move
// expensive is how systems end up holding stale assurance.
func TestDemotionIsCheapAndPromotionIsNot(t *testing.T) {
	m, _ := New("pkg/ledger", "veriqo-engineering")
	climbTo(t, m, ExternallyValidated)

	// A demotion may skip any distance and cites no evidence.
	if err := m.Demote(Implemented, "human:engineer-1", at,
		"the assessed version was superseded; the report no longer covers this code"); err != nil {
		t.Fatalf("demotion refused: %v", err)
	}
	if m.State() != Implemented {
		t.Fatalf("after demotion the machine is at %s", m.State())
	}
	// But it still needs a reason.
	if err := m.Demote(Specified, "human:engineer-1", at, "  "); err == nil {
		t.Fatal("an unexplained demotion was accepted")
	}
	// And a demotion is not a promotion in disguise.
	if err := m.Demote(ExternallyValidated, "human:engineer-1", at, "putting it back"); err == nil {
		t.Fatal("Demote moved the machine upward")
	}
}

// TestPromoteRefusesToMoveBackwards: the two directions have different
// rules, so they must not be reachable through the same call.
func TestPromoteRefusesToMoveBackwards(t *testing.T) {
	m, _ := New("pkg/ledger", "veriqo-engineering")
	climbTo(t, m, InternallyTested)
	if err := m.Promote(Implemented, engineer(), at, "backwards",
		internalEvidence(Internal)); !errors.Is(err, ErrBackwards) {
		t.Fatalf("Promote moved backwards: %v", err)
	}
	if err := m.Promote(InternallyTested, engineer(), at, "sideways",
		internalEvidence(Internal)); !errors.Is(err, ErrBackwards) {
		t.Fatalf("Promote accepted a no-op: %v", err)
	}
}

// TestSelfCertifiedIsAlwaysFalseOnAWellFormedHistory. The property is
// asserted over the whole history rather than trusted per promotion.
func TestSelfCertifiedIsAlwaysFalseOnAWellFormedHistory(t *testing.T) {
	m, _ := New("pkg/ledger", "veriqo-engineering")
	climbTo(t, m, OperationallyProven)
	if m.SelfCertified() {
		t.Fatal("a machine built through Promote reports itself self-certified")
	}
	if len(m.History()) != int(OperationallyProven) {
		t.Fatalf("history has %d transitions for %d rungs", len(m.History()), int(OperationallyProven))
	}
}

// TestAnExpiredPromoterIsRefused. The promoter's window is checked at
// the instant of the promotion, not at construction.
func TestAnExpiredPromoterIsRefused(t *testing.T) {
	m, _ := New("pkg/ledger", "veriqo-engineering")
	if err := m.Promote(Specified, engineer(), at.Add(72*time.Hour), "later",
		internalEvidence(Internal)); err == nil {
		t.Fatal("an expired principal promoted a control")
	}
}

// TestEveryStateHasAName. A State printed as "State(7)" in an
// assurance report is a defect in the report.
func TestEveryStateHasAName(t *testing.T) {
	for _, s := range States() {
		if !s.Valid() {
			t.Fatalf("%d has no name", int(s))
		}
		got, err := Parse(s.String())
		if err != nil || got != s {
			t.Fatalf("Parse(%s) = %v, %v", s, got, err)
		}
		if !s.RequiredEvidence().Valid() {
			t.Fatalf("%s requires an unknown evidence class", s)
		}
	}
	if _, err := Parse("PRODUCTION_READY"); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("an invented state parsed: %v", err)
	}
	if len(States()) != 10 {
		t.Fatalf("the ladder has %d rungs", len(States()))
	}
}

// TestTheDescriptionSaysWhenNoIndependenceIsClaimed. A reader skimming
// a self-reachable state must not have to infer the absence.
func TestTheDescriptionSaysWhenNoIndependenceIsClaimed(t *testing.T) {
	m, _ := New("pkg/ledger", "veriqo-engineering")
	climbTo(t, m, InternallyAssured)
	d := m.Describe()
	if !contains(d, "none required at this state, and none claimed") {
		t.Fatalf("the description does not state the absence:\n%s", d)
	}
	climbTo(t, m, ExternallyValidated)
	d = m.Describe()
	if !contains(d, "Acme Security (external") {
		t.Fatalf("the description does not name the external validator:\n%s", d)
	}
	if !contains(d, "exception:") {
		t.Fatalf("the description drops the validator's exceptions:\n%s", d)
	}
}
