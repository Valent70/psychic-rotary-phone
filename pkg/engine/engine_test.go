package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
)

var at = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func human(id string) identity.Principal {
	return identity.Principal{
		ID: contract.ID("human:" + id), Kind: identity.Human, TenantID: "t1",
		Display: id, NotBefore: at.Add(-time.Hour), NotAfter: at.Add(time.Hour),
	}
}

func agent(id string) identity.Principal {
	return identity.Principal{
		ID: contract.ID("agent:" + id), Kind: identity.Agent, TenantID: "t1",
		Display: id, SPIFFE: "spiffe://veriqo/" + id,
		NotBefore: at.Add(-time.Hour), NotAfter: at.Add(time.Hour),
	}
}

func prod(s Stage, by identity.Principal, refs ...contract.ID) Product {
	return Product{Stage: s, By: by, Refs: refs, Summary: "the " + string(s) + " engine ran", At: at}
}

func passage(t *testing.T) *Passage {
	t.Helper()
	p, err := NewPassage("passage:1", "t1", "did this vessel load cargo during the gap?")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func observe(t *testing.T, p *Passage) {
	t.Helper()
	if err := p.Record(prod(Observe, agent("collector"), "evidenceversion:1")); err != nil {
		t.Fatal(err)
	}
}

// TestADecisionWithoutArbitrationIsRefused.
//
// This is the shortcut every system takes under deadline: the
// observations are in, the answer is obvious, and the arbitration gets
// written up afterwards to match. The point of the refusal is that
// afterwards is too late -- the losing hypotheses were never held
// against the winner, they were reconstructed to lose.
func TestADecisionWithoutArbitrationIsRefused(t *testing.T) {
	p := passage(t)
	observe(t, p)
	if err := p.Record(prod(Qualify, agent("grader"), "qualification:1")); err != nil {
		t.Fatal(err)
	}
	err := p.Record(prod(Decide, human("owner"), "decision:1"))
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("a decision was taken with no arbitration: %v", err)
	}
	if !strings.Contains(err.Error(), "whichever reading happened to be looked at first") {
		t.Errorf("the refusal restates the rule instead of naming what it prevents: %v", err)
	}
}

// TestADecisionWithoutQualificationIsRefused.
func TestADecisionWithoutQualificationIsRefused(t *testing.T) {
	p := passage(t)
	observe(t, p)
	if err := p.Record(prod(Arbitrate, agent("weigher"), "hypothesis:1")); err != nil {
		t.Fatal(err)
	}
	err := p.Record(prod(Decide, human("owner"), "decision:1"))
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("a decision was taken with no source grading: %v", err)
	}
	if !strings.Contains(err.Error(), "anonymous forum post") {
		t.Errorf("the refusal does not say what ungraded sources cost: %v", err)
	}
}

// TestArbitrationAndQualificationAreSiblingsNotASequence.
//
// Either order must work. Forcing one before the other would mean
// either that arbitration sees the source grades before weighing the
// readings, or that qualification sees which reading is winning before
// grading its source. Both are grading the answer you want.
func TestArbitrationAndQualificationAreSiblingsNotASequence(t *testing.T) {
	for _, order := range [][]Stage{{Arbitrate, Qualify}, {Qualify, Arbitrate}} {
		p := passage(t)
		observe(t, p)
		for _, s := range order {
			if err := p.Record(prod(s, agent("worker"), contract.ID("artefact:"+string(s)))); err != nil {
				t.Fatalf("%v: %v", order, err)
			}
		}
		if len(p.Missing()) != 0 {
			t.Errorf("%v left %v missing", order, p.Missing())
		}
	}
}

// TestVeriqoMayNotCloseTheDecision.
//
// The fourth engine is drawn inside the VERIQO box and does not belong
// to VERIQO. An automated principal may run the first three end to
// end; letting it close the fourth would leave no one to hold to the
// decision.
func TestVeriqoMayNotCloseTheDecision(t *testing.T) {
	p := passage(t)
	observe(t, p)
	for _, s := range []Stage{Arbitrate, Qualify} {
		if err := p.Record(prod(s, agent("worker"), "artefact:1")); err != nil {
			t.Fatal(err)
		}
	}
	err := p.Record(prod(Decide, agent("worker"), "decision:1"))
	if !errors.Is(err, ErrNotOurs) {
		t.Fatalf("an agent closed DECIDE: %v", err)
	}
	// And the same passage accepts a human.
	if err := p.Record(prod(Decide, human("owner"), "decision:1")); err != nil {
		t.Fatalf("a human was refused the decision: %v", err)
	}
}

// TestAServiceAccountIsNotAHumanEither. The check is on the KIND, not
// on a name that happens to look like a person's.
func TestAServiceAccountIsNotAHumanEither(t *testing.T) {
	p := passage(t)
	observe(t, p)
	for _, s := range []Stage{Arbitrate, Qualify} {
		if err := p.Record(prod(s, agent("w"), "artefact:1")); err != nil {
			t.Fatal(err)
		}
	}
	svc := human("looks-like-a-person")
	svc.Kind = identity.Service
	svc.SPIFFE = "spiffe://veriqo/svc"
	if err := p.Record(prod(Decide, svc, "decision:1")); !errors.Is(err, ErrNotOurs) {
		t.Fatalf("a service account closed DECIDE: %v", err)
	}
}

// TestAnEngineMayNotBeReRunUntilItAgrees.
//
// A second ARBITRATE that silently replaced the first would let an
// unwelcome result be re-run until it came out differently, leaving no
// trace that it ever came out the other way.
func TestAnEngineMayNotBeReRunUntilItAgrees(t *testing.T) {
	p := passage(t)
	observe(t, p)
	if err := p.Record(prod(Arbitrate, agent("w"), "hypothesis:1")); err != nil {
		t.Fatal(err)
	}
	err := p.Record(prod(Arbitrate, agent("w"), "hypothesis:2"))
	if !errors.Is(err, ErrAlreadyRecorded) {
		t.Fatalf("arbitration was re-run in place: %v", err)
	}
	if !strings.Contains(err.Error(), "laundered") {
		t.Errorf("the refusal does not say what re-running buys: %v", err)
	}
	// The first result stands.
	got, _ := p.Product(Arbitrate)
	if got.Refs[0] != "hypothesis:1" {
		t.Fatalf("the second run overwrote the first: %v", got.Refs)
	}
}

// TestAnEngineThatRanAndProducedNothingIsNotAnEngineThatDidNotRun.
//
// The two states must stay distinguishable: "arbitration found no
// surviving hypothesis" is a finding about the evidence, and
// "arbitration did not run" is a hole in the process. A struct that
// rendered them the same would present the hole as a finding.
func TestAnEngineThatRanAndProducedNothingIsNotAnEngineThatDidNotRun(t *testing.T) {
	p := passage(t)
	observe(t, p)
	empty := prod(Arbitrate, agent("w"))
	if err := p.Record(empty); !errors.Is(err, ErrEmpty) {
		t.Fatalf("a stage with no artefacts was recorded as having run: %v", err)
	}
	if p.Ran(Arbitrate) {
		t.Fatal("the refused stage was recorded anyway")
	}
}

// TestObserveRestsOnNothing. It is where evidence enters; requiring an
// input would mean evidence could only enter where evidence already was.
func TestObserveRestsOnNothing(t *testing.T) {
	if got := Observe.RestsOn(); len(got) != 0 {
		t.Fatalf("OBSERVE rests on %v", got)
	}
	p := passage(t)
	if err := p.Record(prod(Observe, agent("c"), "evidenceversion:1")); err != nil {
		t.Fatal(err)
	}
}

// TestArbitrationBeforeAnyObservationIsRefused.
func TestArbitrationBeforeAnyObservationIsRefused(t *testing.T) {
	p := passage(t)
	err := p.Record(prod(Arbitrate, agent("w"), "hypothesis:1"))
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("arbitration ran with nothing to weigh: %v", err)
	}
	if !strings.Contains(err.Error(), "the answer somebody already had") {
		t.Errorf("the refusal does not name what it prevents: %v", err)
	}
}

// TestSealRefusesAPassportThatCannotBeReRun.
//
// The last row of the diagram -- REPLAY and CHALLENGE -- is a
// condition on the passport existing, not a feature to be added later.
func TestSealRefusesAPassportThatCannotBeReRun(t *testing.T) {
	p := complete(t)
	if _, err := p.Seal("", "route:1"); !errors.Is(err, ErrNoReplay) {
		t.Fatalf("sealed without a replay manifest: %v", err)
	}
	if _, err := p.Seal("replay:1", ""); !errors.Is(err, ErrNoChallenge) {
		t.Fatalf("sealed without a disproof route: %v", err)
	}
	s, err := p.Seal("replay:1", "route:1")
	if err != nil {
		t.Fatal(err)
	}
	if s.ReplayManifest != "replay:1" || s.DisproofRoute != "route:1" {
		t.Fatalf("%+v", s)
	}
}

// TestAnAssembledPassageIsNotADecision.
//
// Every input present and nobody has signed. The report has to say so
// in those words, because "complete" would read as "decided".
func TestAnAssembledPassageIsNotADecision(t *testing.T) {
	p := passage(t)
	observe(t, p)
	for _, s := range []Stage{Arbitrate, Qualify} {
		if err := p.Record(prod(s, agent("w"), "artefact:1")); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.Missing()) != 0 {
		t.Fatalf("still missing %v", p.Missing())
	}
	_, err := p.Seal("replay:1", "route:1")
	if !errors.Is(err, ErrNotOurs) {
		t.Fatalf("an unsigned passage sealed into a passport: %v", err)
	}
	r := p.Report()
	if !strings.Contains(r, "ASSEMBLED, NOT DECIDED") {
		t.Errorf("the report does not distinguish assembled from decided:\n%s", r)
	}
}

// TestAPrincipalFromAnotherTenantMayNotCloseAStage.
func TestAPrincipalFromAnotherTenantMayNotCloseAStage(t *testing.T) {
	p := passage(t)
	other := agent("c")
	other.TenantID = "t2"
	other.ID = "agent:t2-collector"
	if err := p.Record(prod(Observe, other, "evidenceversion:1")); err == nil {
		t.Fatal("a principal from another tenant closed a stage")
	}
}

// TestEveryStageStatesWhatItMayNotDo.
//
// The MayNot() text is the whole reason the separation exists. A stage
// that could not say what it is forbidden would be a box on a diagram.
func TestEveryStageStatesWhatItMayNotDo(t *testing.T) {
	for _, s := range Stages() {
		if strings.TrimSpace(s.Produces()) == "" {
			t.Errorf("%s says nothing about what it produces", s)
		}
		if strings.TrimSpace(s.MayNot()) == "" {
			t.Errorf("%s states no forbidden act, so the separation is decorative", s)
		}
	}
}

// TestOnlyDecideIsBeyondAutomation.
func TestOnlyDecideIsBeyondAutomation(t *testing.T) {
	for _, s := range Stages() {
		want := s != Decide
		if s.MayBeAutomated() != want {
			t.Errorf("%s.MayBeAutomated()=%v, want %v", s, s.MayBeAutomated(), want)
		}
	}
}

// TestThereIsNoCycleAmongTheEngines.
func TestThereIsNoCycleAmongTheEngines(t *testing.T) {
	var walk func(s Stage, seen map[Stage]bool)
	walk = func(s Stage, seen map[Stage]bool) {
		if seen[s] {
			t.Fatalf("cycle reaching %s", s)
		}
		seen[s] = true
		for _, r := range s.RestsOn() {
			next := map[Stage]bool{}
			for k := range seen {
				next[k] = true
			}
			walk(r, next)
		}
	}
	for _, s := range Stages() {
		walk(s, map[Stage]bool{})
	}
}

// TestDescribeNamesEveryEngineAndItsRefusal.
func TestDescribeNamesEveryEngineAndItsRefusal(t *testing.T) {
	d := Describe()
	// The report wraps to a terminal width, so compare on the words
	// rather than on the line breaks.
	flat := strings.Join(strings.Fields(d), " ")
	for _, s := range Stages() {
		if !strings.Contains(d, string(s)) {
			t.Errorf("Describe() omits %s", s)
		}
		if !strings.Contains(flat, strings.Join(strings.Fields(s.MayNot()), " ")) {
			t.Errorf("Describe() omits what %s may not do", s)
		}
		if !strings.Contains(flat, strings.Join(strings.Fields(s.Produces()), " ")) {
			t.Errorf("Describe() omits what %s produces", s)
		}
	}
	for _, line := range strings.Split(d, "\n") {
		if len(line) > 78 {
			t.Errorf("a %d-column line will wrap in the reader's window: %q", len(line), line)
		}
	}
	if !strings.Contains(d, "DECIDE is not") {
		t.Error("Describe() does not say the fourth engine is not VERIQO's")
	}
	if Describe() != Describe() {
		t.Error("Describe() is not deterministic")
	}
}

// TestAPassageWithNoQuestionIsRefused.
func TestAPassageWithNoQuestionIsRefused(t *testing.T) {
	if _, err := NewPassage("passage:1", "t1", "  "); err == nil {
		t.Fatal("a passage with no question was opened")
	}
	if _, err := NewPassage("passage:1", "", "q"); err == nil {
		t.Fatal("a passage with no tenant was opened")
	}
}

// complete builds a passage that has run all four engines.
func complete(t *testing.T) *Passage {
	t.Helper()
	p := passage(t)
	observe(t, p)
	for _, s := range []Stage{Arbitrate, Qualify} {
		if err := p.Record(prod(s, agent("w"), contract.ID("artefact:"+string(s)))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Record(prod(Decide, human("owner"), "decision:1")); err != nil {
		t.Fatal(err)
	}
	return p
}
