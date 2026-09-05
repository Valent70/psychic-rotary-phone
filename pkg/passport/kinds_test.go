package passport

import (
	"errors"
	"strings"
	"testing"
)

func sections(p Profile) map[string]string {
	m := map[string]string{}
	for _, s := range p.RequiredSections {
		m[s] = "stated"
	}
	return m
}

// TestEachKindRefusesTheConclusionItsCustomerWillAskFor.
func TestEachKindRefusesTheConclusionItsCustomerWillAskFor(t *testing.T) {
	cases := map[Kind]string{
		ClaimQualification:        "the loss is covered under section 4",
		IncidentEvidence:          "the vessel was at fault for the collision",
		QuantityDiscrepancy:       "the cargo was stolen in transit",
		CollateralEvidence:        "the warehouse receipts are good security",
		DisputeEvidence:           "the claimant will succeed on liability",
		CounterpartyQualification: "the counterparty is sanctioned",
	}
	for k, statement := range cases {
		p, err := ProfileOf(k)
		if err != nil {
			t.Fatal(err)
		}
		err = CheckKind(k, statement, "", sections(p))
		if !errors.Is(err, ErrForbiddenStatement) {
			t.Fatalf("%s accepted %q: %v", k, statement, err)
		}
		// The refusal must say who the decision belongs to, or it
		// reads as an arbitrary restriction.
		if !strings.Contains(err.Error(), "The decision belongs to") {
			t.Fatalf("%s refusal does not name the decision-maker: %v", k, err)
		}
	}
}

// TestTheEvidentialFormOfTheSameStatementIsAccepted. The kinds
// constrain what may be CONCLUDED, not what may be discussed.
func TestTheEvidentialFormOfTheSameStatementIsAccepted(t *testing.T) {
	cases := map[Kind]string{
		ClaimQualification: "the evidence establishes a loss of 1,500 MT within the period " +
			"the policy names; whether the policy responds is not addressed here",
		IncidentEvidence: "two position reports place the vessels within 0.2 NM at 04:12; " +
			"what happened between them is not established",
		QuantityDiscrepancy: "the loading and discharge figures differ by 1,800 MT on a " +
			"comparable basis, of which 300 MT falls within the stated tolerance",
		CollateralEvidence: "the document set comprises three warehouse receipts; an " +
			"inspector attended on 4 March and recorded the quantities below",
		DisputeEvidence: "the parties' surveys disagree by 1,800 MT and rest on different " +
			"measurement bases",
		CounterpartyQualification: "one adverse media item names an entity with the same " +
			"registered name in a different jurisdiction; the identification is not established",
	}
	for k, statement := range cases {
		p, _ := ProfileOf(k)
		if err := CheckKind(k, statement, "", sections(p)); err != nil {
			t.Fatalf("%s refused an evidential statement: %v", k, err)
		}
	}
}

// TestARequiredSectionCannotBeOmitted. A reader supplies a favourable
// assumption for anything a document leaves out.
func TestARequiredSectionCannotBeOmitted(t *testing.T) {
	p, _ := ProfileOf(QuantityDiscrepancy)
	s := sections(p)
	delete(s, "alternative construction")
	err := CheckKind(QuantityDiscrepancy, "the figures differ by 1,800 MT", "", s)
	if err == nil {
		t.Fatal("a quantity passport omitted the alternative construction")
	}
	if !strings.Contains(err.Error(), "favourable assumption") {
		t.Fatalf("the refusal does not say why it matters: %v", err)
	}
}

// TestNoKindDecidesAnythingItself. Every profile names somebody else.
func TestNoKindDecidesAnythingItself(t *testing.T) {
	for _, k := range Kinds() {
		p, err := ProfileOf(k)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(p.Decides), "veriqo") {
			t.Fatalf("%s says VERIQO makes the decision", k)
		}
		if len(p.ForbiddenStatements) == 0 {
			t.Fatalf("%s forbids nothing; it is a template, not a kind", k)
		}
		if strings.TrimSpace(p.WhyForbidden) == "" {
			t.Fatalf("%s does not explain its refusals; a salesperson will promise around "+
				"a restriction they cannot explain", k)
		}
		if !strings.Contains(DescribeKind(k), "not by VERIQO") {
			t.Fatalf("%s description does not state who decides", k)
		}
	}
	if len(Kinds()) != 6 {
		t.Fatalf("%d kinds", len(Kinds()))
	}
	if _, err := ProfileOf(Kind("INVENTED")); err == nil {
		t.Fatal("an invented kind resolved")
	}
}

// TestTheScopeTextIsCheckedToo. A conclusion moved out of the
// statement and into the scope is the same conclusion.
func TestTheScopeTextIsCheckedToo(t *testing.T) {
	p, _ := ProfileOf(ClaimQualification)
	err := CheckKind(ClaimQualification, "the evidence establishes a loss",
		"prepared to confirm the claim is covered", sections(p))
	if !errors.Is(err, ErrForbiddenStatement) {
		t.Fatalf("a conclusion hidden in the scope was accepted: %v", err)
	}
}

// TestInflectionDoesNotEvadeTheScreen. "are good security" and "is
// good security" are the same statement to a reader.
func TestInflectionDoesNotEvadeTheScreen(t *testing.T) {
	p, _ := ProfileOf(CollateralEvidence)
	for _, s := range []string{
		"the warehouse receipts are good security",
		"the receipts were good security",
		"the receipts have been good security",
	} {
		if err := CheckKind(CollateralEvidence, s, "", sections(p)); !errors.Is(err, ErrForbiddenStatement) {
			t.Fatalf("an inflected forbidden statement passed: %q", s)
		}
	}
}

// TestADisclaimerIsNotAConclusion.
//
// Flagging "whether the policy responds is not addressed here" would
// teach authors to delete their disclaimers, which is the precise
// inversion of what the screen is for.
func TestADisclaimerIsNotAConclusion(t *testing.T) {
	p, _ := ProfileOf(ClaimQualification)
	ok := []string{
		"the evidence establishes a loss; whether the policy responds is not addressed here",
		"no view is expressed on whether the loss is covered",
		"whether the claim is fraudulent is a matter for the tribunal",
	}
	for _, s := range ok {
		if err := CheckKind(ClaimQualification, s, "", sections(p)); err != nil {
			t.Fatalf("a disclaimer was flagged as a conclusion: %q -> %v", s, err)
		}
	}
	// A disclaimer in one clause does not license a conclusion in the
	// next: the window is bounded by clause punctuation.
	bad := "whether the policy wording is unusual is not addressed. the loss is covered"
	if err := CheckKind(ClaimQualification, bad, "", sections(p)); !errors.Is(err, ErrForbiddenStatement) {
		t.Fatalf("a disclaimer in an earlier sentence licensed a later conclusion: %v", err)
	}
}

// TestTheDescriptionSaysTheScreenIsDefeatable. Overstating a control
// is the failure this whole system is about.
func TestTheDescriptionSaysTheScreenIsDefeatable(t *testing.T) {
	d := DescribeKind(ClaimQualification)
	if !strings.Contains(d, "not a proof") {
		t.Fatalf("the description presents the screen as complete:\n%s", d)
	}
	if !strings.Contains(d, "one person mints and another approves") {
		t.Fatalf("the description does not name the real control:\n%s", d)
	}
}
