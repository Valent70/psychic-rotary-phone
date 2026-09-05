package register

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/assurance/state"
	"veriqo/pkg/contract"
)

func at() time.Time { return AssessedAt() }

func graph(t *testing.T) *Graph {
	t.Helper()
	g, err := VeriqoGraph()
	if err != nil {
		t.Fatalf("the register does not build: %v", err)
	}
	return g
}

// TestTheRegisterBuildsAndEveryReferenceResolves. A register whose
// references dangle is a register nobody has walked.
func TestTheRegisterBuildsAndEveryReferenceResolves(t *testing.T) {
	g := graph(t)
	if len(g.Claims()) < 15 {
		t.Fatalf("the register has shrunk to %d claims", len(g.Claims()))
	}
	if len(g.Gates()) != 20 {
		t.Fatalf("%d gates; the production gate set is twenty", len(g.Gates()))
	}
	if len(g.Debts()) < 10 {
		t.Fatalf("%d debts", len(g.Debts()))
	}
}

// TestNothingInTheRegisterIsAboveInternallyAssured.
//
// This is the honesty check. INTERNALLY_ASSURED is the highest rung
// reachable without an outside party, and no outside party has
// examined any of this. The moment one does, this test must be
// updated deliberately -- which is the point.
func TestNothingInTheRegisterIsAboveInternallyAssured(t *testing.T) {
	for _, c := range graph(t).Claims() {
		if c.CurrentLevel > state.InternallyAssured {
			t.Fatalf("%s holds %s, which requires an outside party. If that is now true, "+
				"this test must be changed deliberately and the evidence recorded",
				c.ID, c.CurrentLevel)
		}
		if !c.CurrentLevel.SelfReachable() {
			t.Fatalf("%s holds a level VERIQO cannot reach alone", c.ID)
		}
	}
}

// TestNoGateIsClosable. Twenty gates, none satisfied.
func TestNoGateIsClosable(t *testing.T) {
	g := graph(t)
	for _, gt := range g.Gates() {
		s, err := g.Support(gt.ID, at())
		if err != nil {
			t.Fatalf("%s: %v", gt.ID, err)
		}
		if s.Closable() {
			t.Fatalf("%s reports closable; no gate is satisfied", gt.ID)
		}
	}
	d := g.Release(at())
	if d.Permitted {
		t.Fatal("release is permitted")
	}
	if len(d.Reasons) < 20 {
		t.Fatalf("release was refused with only %d reasons", len(d.Reasons))
	}
}

// TestEveryMandatoryGateRestsOnVeriqosOwnEvidence. Stating this as a
// test rather than a sentence means it stops being true the moment it
// stops being true.
func TestEveryMandatoryGateRestsOnVeriqosOwnEvidence(t *testing.T) {
	g := graph(t)
	d := g.Release(at())
	if len(d.SelfProducedGates) != 20 {
		t.Fatalf("%d of 20 mandatory gates rest entirely on VERIQO's own evidence; if that "+
			"number has fallen, an outside party has contributed and the register must say who",
			len(d.SelfProducedGates))
	}
}

// TestEveryControlIsClaimedAbout. A control nothing asserts anything
// about is work that is presumed fine and has no testable property.
func TestEveryControlIsClaimedAbout(t *testing.T) {
	if u := graph(t).UnclaimedControls(); len(u) != 0 {
		t.Fatalf("controls with no assurance claim: %v", u)
	}
}

// TestEveryClaimNamesADebtOrIsFullySupported. A claim short of its
// required level with no debt behind it is a gap nobody owns.
func TestEveryClaimNamesADebtOrIsFullySupported(t *testing.T) {
	g := graph(t)
	byID := map[contract.ID]Debt{}
	for _, d := range g.Debts() {
		byID[d.ID] = d
	}
	for _, c := range g.Claims() {
		if c.Met(at()) {
			continue
		}
		if len(c.Debts) == 0 {
			t.Fatalf("%s is at %s, needs %s, and names no evidence debt; the gap has no owner",
				c.ID, c.CurrentLevel, c.RequiredLevel)
		}
		for _, id := range c.Debts {
			d, ok := byID[id]
			if !ok {
				t.Fatalf("%s cites debt %s which does not exist", c.ID, id)
			}
			if strings.TrimSpace(d.Owner) == "" {
				t.Fatalf("debt %s has no owner", id)
			}
		}
	}
}

// TestEveryOpenDebtStatesItsRiskAndItsDependency.
func TestEveryOpenDebtStatesItsRiskAndItsDependency(t *testing.T) {
	for _, d := range graph(t).Debts() {
		if !d.Open() {
			continue
		}
		if strings.TrimSpace(d.Risk) == "" {
			t.Fatalf("%s states no risk", d.ID)
		}
		if !d.SelfPayable() && strings.TrimSpace(d.ExternalDependency) == "" {
			t.Fatalf("%s needs an outside party and names none", d.ID)
		}
		if d.SelfPayable() && d.Class != state.Internal {
			t.Fatalf("%s is marked self-payable at class %s", d.ID, d.Class)
		}
	}
}

// TestAClaimCannotOutrunItsEvidence is the Law 11 check at the
// register layer: a claim cannot simply be written at a level.
func TestAClaimCannotOutrunItsEvidence(t *testing.T) {
	base := Claims()[0]
	c := base
	c.CurrentLevel = state.ExternallyValidated
	if err := c.Validate(); !errors.Is(err, ErrOverclaim) && !errors.Is(err, state.ErrSelfCertified) {
		t.Fatalf("a claim was written at EXTERNALLY_VALIDATED on internal evidence: %v", err)
	}

	// Even with evidence of the right CLASS, an internal validator
	// does not satisfy it.
	c.Evidence = append(c.Evidence, state.Evidence{
		ID: "ae:fake", Class: state.ExternalRequired, Summary: "we checked", Scope: "everything",
		At: at(), ArtefactHash: "h",
		Validator: state.Validator{ID: implementer, Name: "VERIQO engineering"},
	})
	if err := c.Validate(); err == nil {
		t.Fatal("internal evidence labelled EXTERNAL_REQUIRED was accepted")
	}
}

// TestAnOpenCounterexampleCapsTheLevel.
func TestAnOpenCounterexampleCapsTheLevel(t *testing.T) {
	c := Claims()[0]
	c.Counterexamples = []string{"the attack still works"}
	if err := c.Validate(); !errors.Is(err, ErrOpenCounter) {
		t.Fatalf("a claim held %s with an open counterexample: %v", c.CurrentLevel, err)
	}
	c.CurrentLevel = state.Implemented
	if err := c.Validate(); err != nil {
		t.Fatalf("a demoted claim with an open counterexample was refused: %v", err)
	}
}

// TestEveryClaimHasADisproofPathThatIsNotItsAssertion.
func TestEveryClaimHasADisproofPathThatIsNotItsAssertion(t *testing.T) {
	for _, c := range graph(t).Claims() {
		if strings.TrimSpace(c.DisproofPath) == "" {
			t.Fatalf("%s has no disproof path", c.ID)
		}
		if normalise(c.Assertion) == normalise(c.DisproofPath) {
			t.Fatalf("%s restates its assertion as its disproof path", c.ID)
		}
		if strings.TrimSpace(c.Scope) == "" || strings.TrimSpace(c.Environment) == "" {
			t.Fatalf("%s omits its scope or environment", c.ID)
		}
	}
}

// TestTheDescriptionSaysWhenNobodyIndependentHasLooked. A reader must
// not have to notice an absence.
func TestTheDescriptionSaysWhenNobodyIndependentHasLooked(t *testing.T) {
	for _, c := range graph(t).Claims() {
		d := c.Describe()
		if strings.Contains(d, "(independent,") {
			t.Fatalf("%s claims an independent validator", c.ID)
		}
		if !strings.Contains(d, "no independent party has examined this") &&
			!strings.Contains(d, "no evidence record names a party") {
			t.Fatalf("%s does not state the absence of independent review:\n%s", c.ID, d)
		}
	}
}

// TestADanglingReferenceIsRefusedAtConstruction.
func TestADanglingReferenceIsRefusedAtConstruction(t *testing.T) {
	cs := Claims()
	cs[0].Controls = append(cs[0].Controls, "CTL-DOES-NOT-EXIST")
	if _, err := New(Controls(), cs, Debts(), GateRefs()); !errors.Is(err, ErrDanglingRef) {
		t.Fatalf("a dangling control reference was accepted: %v", err)
	}
	gs := GateRefs()
	gs[0].Controls = nil
	if _, err := New(Controls(), Claims(), Debts(), gs); err == nil {
		t.Fatal("a gate with no controls was accepted; it could be closed by assertion")
	}
}

// TestTheGateShortfallIsStatedAgainstTheGate. A claim at the level its
// own risk demands is not "short" of that level just because a gate
// resting on it needs more.
func TestTheGateShortfallIsStatedAgainstTheGate(t *testing.T) {
	g := graph(t)
	s, err := g.Support("G14", at()) // requires OPERATIONALLY_PROVEN
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Unmet) == 0 {
		t.Fatal("G14 reports nothing unmet")
	}
	joined := strings.Join(s.Unmet, " ")
	if !strings.Contains(joined, "this gate requires OPERATIONALLY_PROVEN") {
		t.Fatalf("the shortfall is not stated against the gate's requirement: %v", s.Unmet)
	}
}

// TestAnUncoveredControlIsReported. The quiet failure a graph exists
// to catch: a gate that looks supported because its OTHER controls are.
func TestAnUncoveredControlIsReported(t *testing.T) {
	cs := Claims()
	var kept []Claim
	for _, c := range cs {
		if c.ID != "AC-ANCHOR-DELIBERATELY-ABSENT" {
			kept = append(kept, c)
		}
	}
	g, err := New(Controls(), kept, Debts(), GateRefs())
	if err != nil {
		t.Fatal(err)
	}
	s, err := g.Support("G10", at())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Uncovered) == 0 {
		t.Fatal("a gate naming a control with no claim reported no uncovered control")
	}
	if s.Closable() {
		t.Fatal("a gate with an uncovered control reported closable")
	}
	d := g.Release(at())
	if !strings.Contains(strings.Join(d.Reasons, " "), "no assurance claim covers it") {
		t.Fatalf("the release decision does not name the uncovered control: %v", d.Reasons)
	}
}

// TestTheReportShowsTheWholeChain.
func TestTheReportShowsTheWholeChain(t *testing.T) {
	r := graph(t).Report(at())
	for _, want := range []string{
		"gate -> control -> claim -> evidence -> validator -> level -> release",
		"EVIDENCE DEBT", "RELEASE DECISION", "NOT PERMITTED",
		"rest entirely on VERIQO's own evidence",
	} {
		if !strings.Contains(r, want) {
			t.Fatalf("the report omits %q", want)
		}
	}
}

// TestAnOrphanIsATypedFindingWithAConsequence.
//
// A control nothing claims anything about does not appear in any
// report of what is unproven, because nothing was ever claimed. It is
// invisible in exactly the way that matters, so it must be a finding
// the system produces rather than a row somebody notices missing.
func TestAnOrphanIsATypedFindingWithAConsequence(t *testing.T) {
	cs := Claims()
	var kept []Claim
	for _, c := range cs {
		if c.ID != "AC-ANCHOR-DELIBERATELY-ABSENT" {
			kept = append(kept, c)
		}
	}
	g, err := New(Controls(), kept, Debts(), GateRefs())
	if err != nil {
		t.Fatal(err)
	}
	orphans := g.Orphans()
	if len(orphans) != 1 {
		t.Fatalf("%d orphans", len(orphans))
	}
	o := orphans[0]
	if o.Control != "CTL-ANCHOR" {
		t.Fatalf("orphan = %s", o.Control)
	}
	// The finding must name the gates resting on nothing, so it can be
	// prioritised rather than merely listed.
	if len(o.Gates) == 0 {
		t.Fatal("the orphan does not name the gates resting on it")
	}
	if len(o.Packages) == 0 {
		t.Fatal("the orphan does not say where to look")
	}
	if !strings.Contains(o.Consequence, "assumption rather than by a claim") {
		t.Fatalf("the orphan states no consequence: %q", o.Consequence)
	}
	if !strings.Contains(o.String(), "ASSURANCE_ORPHAN") {
		t.Fatalf("the rendering does not name the finding type: %s", o)
	}
	rep := g.Report(at())
	if !strings.Contains(rep, "ASSURANCE ORPHAN") {
		t.Fatalf("the graph report does not surface orphans:\n%s", rep)
	}
	if !strings.Contains(rep, "because nothing was ever claimed") {
		t.Fatalf("the report does not explain why an orphan is invisible:\n%s", rep)
	}
}
