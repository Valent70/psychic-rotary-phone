package reverseproof

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/claim"
	"veriqo/pkg/contract"
)

func d(y int) time.Time         { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
func to(t time.Time) *time.Time { return &t }

func testClaim() claim.Claim {
	return claim.Claim{
		ID: "claim:c1", TenantID: "t-acme", CaseID: "case-1",
		Statement: "cargo loss = 1,800 MT",
		Scope: claim.Scope{Subject: "CargoLot L-77", Aspect: "quantity",
			Period: contract.Interval{From: d(2024), To: to(d(2025))}},
		SupportingEvidence:    []string{"ev:discharge-survey"},
		AlternativeHypotheses: []string{"measurement conversion error"},
		DisproofPath:          "a certified density measurement showing incomparable bases",
		Status:                claim.Supported,
		Versions: contract.VersionSet{
			Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
			Policy:    contract.Version{Component: "baseline", Revision: 1},
			Algorithm: contract.Version{Component: "reverseproof", Revision: 1},
		},
	}
}

// The eight conditions the specification's own example names.
func cargoConditions() []Condition {
	return []Condition{
		{ID: "C1", Must: "the cargo existed", Expected: "a loading survey and a bill of lading",
			Sources: []string{"terminal", "shipper"}, Diagnosticity: 0.4,
			AcquisitionCost: 0.2, LegallyAccessible: true},
		{ID: "C2", Must: "the loading measurement is valid",
			Expected: "a calibrated ullage report from the load port",
			Sources:  []string{"load terminal"}, Diagnosticity: 0.8,
			AcquisitionCost: 0.3, LegallyAccessible: true},
		{ID: "C3", Must: "the discharge measurement is valid",
			Expected: "a calibrated ullage report from the discharge port",
			Sources:  []string{"discharge terminal"}, Diagnosticity: 0.8,
			AcquisitionCost: 0.3, LegallyAccessible: true},
		{ID: "C4", Must: "the measurement bases are comparable",
			Expected: "density and temperature at both ends on the same standard",
			Sources:  []string{"both terminals", "independent inspector"}, Diagnosticity: 0.95,
			AcquisitionCost: 0.4, LegallyAccessible: true},
		{ID: "C5", Must: "the difference exceeds contractual tolerance",
			Expected: "the contract's tolerance clause and the arithmetic",
			Sources:  []string{"contract"}, Diagnosticity: 0.6,
			AcquisitionCost: 0.1, LegallyAccessible: true},
		{ID: "C6", Must: "the event occurred in the defined interval",
			Expected: "voyage timestamps bracketing the loss",
			Sources:  []string{"AIS", "port records"}, Diagnosticity: 0.5,
			AcquisitionCost: 0.2, LegallyAccessible: true},
		{ID: "C7", Must: "a physical mechanism is plausible",
			Expected: "a leak, a transfer, or an accounted-for retention",
			Sources:  []string{"vessel log", "survey"}, Diagnosticity: 0.7,
			AcquisitionCost: 0.6, LegallyAccessible: true},
		{ID: "C8", Must: "alternatives have been tested",
			Expected: "each alternative hypothesis assessed against the evidence",
			Sources:  []string{"internal analysis"}, Diagnosticity: 0.9,
			AcquisitionCost: 0.1, LegallyAccessible: true},
	}
}

func proof(t *testing.T) *Proof {
	t.Helper()
	p, err := New(testClaim(), "reverseproof:rp1", cargoConditions(),
		[]string{"measurement conversion error", "loading quantity overstated"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestOneRefutedNecessaryConditionRefutesTheClaim.
//
// This is the asymmetry the whole package rests on: a necessary
// condition refuted refutes; any number satisfied does not prove.
func TestOneRefutedNecessaryConditionRefutesTheClaim(t *testing.T) {
	p := proof(t)
	// Satisfy everything except one.
	for _, c := range p.Conditions {
		if c.ID == "C4" {
			continue
		}
		if err := p.Set(c.ID, Satisfied, []string{"ev:" + c.ID}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Set("C4", Refuted, []string{"ev:density-report"},
		"loading was measured at 15C in air, discharge at 20C in vacuum"); err != nil {
		t.Fatal(err)
	}
	if p.Verdict() != VerdictRefuted {
		t.Fatalf("verdict = %s with seven of eight conditions satisfied and one refuted",
			p.Verdict())
	}
}

// TestThereIsNoConfirmedVerdict. Satisfying necessary conditions does
// not establish a claim, and no verdict may suggest otherwise.
func TestThereIsNoConfirmedVerdict(t *testing.T) {
	p := proof(t)
	for _, c := range p.Conditions {
		if err := p.Set(c.ID, Satisfied, []string{"ev:" + c.ID}, ""); err != nil {
			t.Fatal(err)
		}
	}
	v := p.Verdict()
	if v != VerdictFullyChecked {
		t.Fatalf("verdict = %s", v)
	}
	for _, forbidden := range []string{"PROVED", "CONFIRMED", "ESTABLISHED", "TRUE"} {
		if strings.Contains(string(v), forbidden) {
			t.Fatalf("the verdict %q reads as a proof", v)
		}
	}
	// And the report says so in words.
	if !strings.Contains(p.Report(), "does not establish the claim") {
		t.Fatalf("the report does not state the asymmetry:\n%s", p.Report())
	}
}

// TestNotObservedIsNotRefuted is Law 5 inside the proof engine.
func TestNotObservedIsNotRefuted(t *testing.T) {
	p := proof(t)
	for _, c := range p.Conditions {
		p.Set(c.ID, Satisfied, []string{"ev:" + c.ID}, "")
	}
	if err := p.Set("C7", NotObserved, nil, "the vessel log was not produced"); err != nil {
		t.Fatal(err)
	}
	if p.Verdict() == VerdictRefuted {
		t.Fatal("A CONDITION THAT WAS LOOKED FOR AND NOT FOUND REFUTED THE CLAIM")
	}
	notObserved, unexamined := p.Missing()
	if len(notObserved) != 1 || notObserved[0].ID != "C7" {
		t.Fatalf("Missing() notObserved = %v", notObserved)
	}
	if len(unexamined) != 0 {
		t.Fatalf("a NOT_OBSERVED condition was counted as unexamined: %v", unexamined)
	}
}

// TestUnexaminedIsNotNotObserved: one is an absence of the
// observation, the other an absence of work.
func TestUnexaminedIsNotNotObserved(t *testing.T) {
	p := proof(t)
	p.Set("C1", Satisfied, []string{"ev:C1"}, "")
	p.Set("C2", NotObserved, nil, "requested and not supplied")
	// The rest stay unexamined.

	notObserved, unexamined := p.Missing()
	if len(notObserved) != 1 {
		t.Fatalf("notObserved = %v", notObserved)
	}
	if len(unexamined) != 6 {
		t.Fatalf("unexamined = %d, want 6", len(unexamined))
	}
	if p.Verdict() != VerdictIncomplete {
		t.Fatalf("verdict = %s with six conditions unexamined", p.Verdict())
	}
	if !NotObserved.Examined() {
		t.Fatal("NOT_OBSERVED is classified as unexamined")
	}
	if Unexamined.Examined() {
		t.Fatal("UNEXAMINED is classified as examined")
	}
}

// TestCompletenessIsReportedAlongsideEveryVerdict.
//
// ALL_CONDITIONS_SATISFIED over a decomposition that only listed the
// easy conditions is a strong-looking result about a weak question.
func TestCompletenessIsReportedAlongsideEveryVerdict(t *testing.T) {
	p := proof(t)
	if p.Completeness() != 0 {
		t.Fatalf("an untouched decomposition reports completeness %v", p.Completeness())
	}
	for i, c := range p.Conditions {
		if i >= 4 {
			break
		}
		p.Set(c.ID, Satisfied, []string{"ev:" + c.ID}, "")
	}
	if got := p.Completeness(); got != 0.5 {
		t.Fatalf("completeness = %v, want 0.5", got)
	}
	if !strings.Contains(p.Report(), "completeness 50%") {
		t.Fatalf("the report omits completeness:\n%s", p.Report())
	}
}

// TestThePlanRanksByDiagnosticityOverCost. This is the output that
// turns "we do not know" into "this is what would settle it".
func TestThePlanRanksByDiagnosticityOverCost(t *testing.T) {
	p := proof(t)
	actionable, blocked := p.Plan()
	if len(actionable) == 0 {
		t.Fatal("the plan proposes nothing")
	}
	if len(blocked) != 0 {
		t.Fatalf("nothing should be blocked here: %v", blocked)
	}
	// C8 has diagnosticity 0.9 at cost 0.1 = 9.0; C4 has 0.95/0.4 =
	// 2.375. The cheap, highly diagnostic condition comes first.
	if actionable[0].Condition.ID != "C8" {
		t.Fatalf("the plan's first item is %s (value %.2f), want C8",
			actionable[0].Condition.ID, actionable[0].Value)
	}
	for i := 1; i < len(actionable); i++ {
		if actionable[i-1].Value < actionable[i].Value {
			t.Fatalf("the plan is not ranked: %v before %v",
				actionable[i-1].Value, actionable[i].Value)
		}
	}
}

// TestUnobtainableAndUnlawfulConditionsAreBlockedNotDropped.
//
// A plan that omits them looks achievable and is not.
func TestUnobtainableAndUnlawfulConditionsAreBlockedNotDropped(t *testing.T) {
	conds := cargoConditions()
	conds[6].Sources = nil             // C7: nothing can supply it
	conds[5].LegallyAccessible = false // C6: exists, may not be obtained
	p, err := New(testClaim(), "reverseproof:rp1", conds, []string{"alt"})
	if err != nil {
		t.Fatal(err)
	}
	actionable, blocked := p.Plan()
	if len(blocked) != 2 {
		t.Fatalf("blocked = %v, want C6 and C7", blocked)
	}
	reasons := blocked[0].Reason + " " + blocked[1].Reason
	if !strings.Contains(reasons, "lawfully") {
		t.Fatalf("the legal block is not stated: %v", reasons)
	}
	if !strings.Contains(reasons, "no source") {
		t.Fatalf("the unobtainability is not stated: %v", reasons)
	}
	for _, a := range actionable {
		if a.Condition.ID == "C6" || a.Condition.ID == "C7" {
			t.Fatalf("%s appears as actionable", a.Condition.ID)
		}
	}
	if !strings.Contains(p.Report(), "cannot be settled by acquiring evidence") {
		t.Fatalf("the report does not separate the blocked items:\n%s", p.Report())
	}
}

// TestUndecidableWhenTheRemainderIsUnobtainable. More work will not
// settle it, and saying "incomplete" would imply otherwise.
func TestUndecidableWhenTheRemainderIsUnobtainable(t *testing.T) {
	p := proof(t)
	for _, c := range p.Conditions {
		if c.ID == "C7" {
			p.Set(c.ID, Unobtainable, nil, "the vessel log was destroyed")
			continue
		}
		p.Set(c.ID, Satisfied, []string{"ev:" + c.ID}, "")
	}
	if p.Verdict() != VerdictUndecidable {
		t.Fatalf("verdict = %s, want UNDECIDABLE_ON_AVAILABLE_EVIDENCE", p.Verdict())
	}
}

// TestARefutationPropagatesToTheClaimAutomatically.
//
// The failure mode is that somebody records the refutation and the
// conclusion stays standing.
func TestARefutationPropagatesToTheClaimAutomatically(t *testing.T) {
	c := testClaim()
	p := proof(t)
	p.Set("C4", Refuted, []string{"ev:density-report"},
		"loading at 15C in air, discharge at 20C in vacuum")

	out, err := p.ApplyTo(c)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != claim.Contradicted {
		t.Fatalf("the claim is %s after a necessary condition was refuted", out.Status)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("the updated claim is invalid: %v", err)
	}
	if out.ReverseProofRef != "reverseproof:rp1" {
		t.Fatalf("the claim does not cite its decomposition: %q", out.ReverseProofRef)
	}
}

// TestTheClaimLearnsWhatWasExpectedAndMissing. The gap has to be
// visible on the claim itself, not only inside the proof.
func TestTheClaimLearnsWhatWasExpectedAndMissing(t *testing.T) {
	c := testClaim()
	p := proof(t)
	p.Set("C2", NotObserved, nil, "the load-port ullage report was not supplied")
	for _, id := range []string{"C1", "C3", "C4", "C5", "C6", "C7", "C8"} {
		p.Set(id, Satisfied, []string{"ev:" + id}, "")
	}
	out, err := p.ApplyTo(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ExpectedEvidence) != 8 {
		t.Fatalf("the claim records %d expected items, want 8", len(out.ExpectedEvidence))
	}
	joined := strings.Join(out.MissingEvidence, " ")
	if !strings.Contains(joined, "C2") {
		t.Fatalf("the claim does not record the missing observation: %v", out.MissingEvidence)
	}
	// And it is still SUPPORTED: not finding something is not finding
	// the opposite.
	if out.Status != claim.Supported {
		t.Fatalf("a not-observed condition demoted the claim to %s", out.Status)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("the updated claim is invalid: %v", err)
	}
}

// TestAConditionWithNoExpectedObservationIsRefused. It cannot be
// looked for, so it cannot be assessed, so it cannot be part of a
// proof.
func TestAConditionWithNoExpectedObservationIsRefused(t *testing.T) {
	c := Condition{ID: "C1", Must: "the cargo existed"}
	if err := c.Validate(); !errors.Is(err, ErrNoExpectation) {
		t.Fatalf("a condition with no expected observation was accepted: %v", err)
	}
}

// TestASatisfiedOrRefutedConditionMustCiteEvidence. A refutation with
// no evidence is an opinion.
func TestASatisfiedOrRefutedConditionMustCiteEvidence(t *testing.T) {
	for _, st := range []State{Satisfied, Refuted} {
		c := Condition{ID: "C1", Must: "x", Expected: "y", State: st}
		if err := c.Validate(); err == nil {
			t.Errorf("a %s condition with no evidence was accepted", st)
		}
	}
}

// TestAnAssumptionMustBeStated. A decomposition with hidden
// assumptions is worse than one with declared ones.
func TestAnAssumptionMustBeStated(t *testing.T) {
	c := Condition{ID: "C1", Must: "x", Expected: "y", State: Assumed}
	if err := c.Validate(); err == nil {
		t.Fatal("an unstated assumption was accepted")
	}
	c.Note = "the parties agree the cargo existed; it is not in dispute"
	if err := c.Validate(); err != nil {
		t.Fatalf("a stated assumption was refused: %v", err)
	}

	p := proof(t)
	p.Set("C1", Assumed, nil, "not in dispute between the parties")
	if len(p.Assumptions()) != 1 {
		t.Fatalf("Assumptions() = %v", p.Assumptions())
	}
}

// TestADecompositionWithNoConditionsIsRefused: an undecomposed claim
// has not been reverse-proved, it has been restated.
func TestADecompositionWithNoConditionsIsRefused(t *testing.T) {
	if _, err := New(testClaim(), "reverseproof:rp1", nil, nil); !errors.Is(err, ErrNoConditions) {
		t.Fatalf("a proof with no conditions was built: %v", err)
	}
}

func TestDuplicateConditionIDsAreRefused(t *testing.T) {
	conds := append(cargoConditions(), cargoConditions()[0])
	if _, err := New(testClaim(), "reverseproof:rp1", conds, nil); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate condition ids were accepted: %v", err)
	}
}

func TestSettingAnUnknownConditionIsRefused(t *testing.T) {
	p := proof(t)
	if err := p.Set("C99", Satisfied, []string{"ev:x"}, ""); !errors.Is(err, ErrUnknownCondition) {
		t.Fatalf("an unknown condition was set: %v", err)
	}
}

// TestApplyToRefusesTheWrongClaim.
func TestApplyToRefusesTheWrongClaim(t *testing.T) {
	p := proof(t)
	other := testClaim()
	other.ID = "claim:c2"
	if _, err := p.ApplyTo(other); err == nil {
		t.Fatal("a decomposition was applied to a different claim")
	}
}
