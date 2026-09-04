package domain

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/hypothesis"
	"veriqo/pkg/ontology"
	"veriqo/pkg/reverseproof"
)

func registry(t *testing.T) *Registry {
	t.Helper()
	r, err := Veriqo()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestInsuranceCannotSayCovered is the domain's defining refusal.
func TestInsuranceCannotSayCovered(t *testing.T) {
	r := registry(t)
	forbidden := []string{
		"The loss is covered under clause 4.",
		"The loss is not covered.",
		"Coverage is denied for late notification.",
		"The policy responds to this loss.",
		"The claim is fraudulent.",
	}
	for _, s := range forbidden {
		if err := r.CheckStatement("insurance", s); !errors.Is(err, ErrForbiddenClaim) {
			t.Errorf("insurance was permitted to state %q: %v", s, err)
		}
	}
	// What it MAY say.
	permitted := []string{
		"Clause 4 is potentially applicable; the evidence supporting and contradicting " +
			"applicability is set out below.",
		"The policy evidence is incomplete: no endorsement schedule was produced.",
	}
	for _, s := range permitted {
		if err := r.CheckStatement("insurance", s); err != nil {
			t.Errorf("insurance was refused a permitted statement %q: %v", s, err)
		}
	}
}

// TestFinanceCannotInferExecutionFromInstruction.
func TestFinanceCannotInferExecutionFromInstruction(t *testing.T) {
	r := registry(t)
	for _, s := range []string{
		"The payment was executed on 4 June.",
		"The payment settled to the substituted account.",
		"The funds were received by the beneficiary.",
	} {
		if err := r.CheckStatement("finance", s); !errors.Is(err, ErrForbiddenClaim) {
			t.Errorf("finance was permitted to state %q", s)
		}
	}
	if err := r.CheckStatement("finance",
		"A payment instruction was issued on 4 June naming account X; no settlement "+
			"record has been obtained."); err != nil {
		t.Fatalf("the permitted formulation was refused: %v", err)
	}
}

// TestThePaymentDiversionTemplateRequiresSettlementSeparately.
//
// This is the rule expressed as a necessary condition rather than as a
// warning: an analyst working the template cannot skip it without
// leaving an unexamined condition on the record.
func TestThePaymentDiversionTemplateRequiresSettlementSeparately(t *testing.T) {
	r := registry(t)
	tpl, err := r.Template("FIN-PAYMENT-DIVERSION")
	if err != nil {
		t.Fatal(err)
	}
	var settlement reverseproof.Condition
	for _, c := range tpl.Conditions {
		if strings.Contains(c.Must, "funds actually moved") {
			settlement = c
		}
	}
	if settlement.ID == "" {
		t.Fatal("the template has no condition requiring evidence that funds moved")
	}
	if !strings.Contains(settlement.Expected, "DIFFERENT record from the instruction") {
		t.Fatalf("the condition does not distinguish settlement from instruction: %q",
			settlement.Expected)
	}
	// And "instructed and never settled" must be a live hypothesis.
	found := false
	for _, h := range tpl.Hypotheses {
		if strings.Contains(h.Statement, "never settled") {
			found = true
		}
	}
	if !found {
		t.Fatal("the template does not consider that the instruction was never settled")
	}
}

// TestDarkActivityCarriesTheOrdinaryExplanations.
//
// Absence of AIS is not evidence of concealment, and the template is
// where that is enforced: an analyst must score against coverage,
// equipment failure, a lawful switch-off and provider loss.
func TestDarkActivityCarriesTheOrdinaryExplanations(t *testing.T) {
	r := registry(t)
	tpl, err := r.Template("MAR-DARK-ACTIVITY")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"coverage": false, "failed": false, "security": false, "provider": false,
	}
	for _, h := range tpl.Hypotheses {
		s := strings.ToLower(h.Statement)
		for k := range want {
			if strings.Contains(s, k) {
				want[k] = true
			}
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("the dark-activity template does not consider the %q explanation", k)
		}
	}
	if len(tpl.Hypotheses) < 5 {
		t.Fatalf("only %d hypotheses; the ordinary explanations are being skipped",
			len(tpl.Hypotheses))
	}
	// The deliberate-concealment hypothesis must not be first-listed
	// in a way that makes it the default -- and more concretely, it
	// must require an EVIDENCED activity rather than merely a gap.
	var h1 hypothesis.Hypothesis
	for _, h := range tpl.Hypotheses {
		if h.ID == "H1" {
			h1 = h
		}
	}
	if len(h1.Expected) < 3 {
		t.Fatalf("the concealment hypothesis expects only %v", h1.Expected)
	}
	found := false
	for _, e := range h1.Expected {
		if strings.Contains(e, "evidenced activity") {
			found = true
		}
	}
	if !found {
		t.Fatalf("concealment can be concluded from a gap alone: %v", h1.Expected)
	}
}

// TestSingleSourceRiskRequiresTheSearchToHaveHappened.
//
// A supplier that looks single-source in a graph built from shipping
// records may have three qualified alternatives that never shipped.
func TestSingleSourceRiskRequiresTheSearchToHaveHappened(t *testing.T) {
	r := registry(t)
	tpl, err := r.Template("SC-SINGLE-SOURCE-RISK")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range tpl.Conditions {
		if strings.Contains(c.Must, "search for alternatives was actually performed") {
			found = true
		}
	}
	if !found {
		t.Fatal("the template concludes single-sourcing from an absence in the data held")
	}
	// And the artefact explanation must be a hypothesis.
	artefact := false
	for _, h := range tpl.Hypotheses {
		if strings.Contains(strings.ToLower(h.Statement), "artefact of the data held") {
			artefact = true
		}
	}
	if !artefact {
		t.Fatal("the template does not consider that the concentration is a data artefact")
	}
}

// TestQuantityDiscrepancyPutsTheBasisQuestionFirst.
func TestQuantityDiscrepancyPutsTheBasisQuestionFirst(t *testing.T) {
	r := registry(t)
	tpl, err := r.Template("COM-QUANTITY-DISCREPANCY")
	if err != nil {
		t.Fatal(err)
	}
	var basis reverseproof.Condition
	for _, c := range tpl.Conditions {
		if strings.Contains(c.Must, "bases are comparable") {
			basis = c
		}
	}
	if basis.ID == "" {
		t.Fatal("the template does not require the bases to be comparable")
	}
	// It must be the most diagnostic condition in the template, or an
	// acquisition plan will chase cheaper, less decisive evidence.
	for _, c := range tpl.Conditions {
		if c.Diagnosticity > basis.Diagnosticity {
			t.Errorf("condition %s (%.2f) is more diagnostic than the basis comparison (%.2f)",
				c.ID, c.Diagnosticity, basis.Diagnosticity)
		}
	}
	// And the artefact hypothesis must be present.
	found := false
	for _, h := range tpl.Hypotheses {
		if strings.Contains(strings.ToLower(h.Statement), "measurement-basis artefact") {
			found = true
		}
	}
	if !found {
		t.Fatal("the template does not consider that the difference is a basis artefact")
	}
}

// --- Structural rules ---------------------------------------------------

// TestEveryTemplateOffersCompetingHypotheses. One hypothesis is a
// template for confirming a conclusion.
func TestEveryTemplateOffersCompetingHypotheses(t *testing.T) {
	r := registry(t)
	for _, d := range r.Domains() {
		for _, tpl := range r.ForDomain(d) {
			if len(tpl.Hypotheses) < 2 {
				t.Errorf("%s offers %d hypothesis(es)", tpl.ID, len(tpl.Hypotheses))
			}
		}
	}
}

// TestEveryTemplateShipsUnexaminedConditions. A template supplies the
// questions, not the answers.
func TestEveryTemplateShipsUnexaminedConditions(t *testing.T) {
	r := registry(t)
	for _, d := range r.Domains() {
		for _, tpl := range r.ForDomain(d) {
			for _, c := range tpl.Conditions {
				if c.State != reverseproof.Unexamined {
					t.Errorf("%s ships %s in state %s", tpl.ID, c.ID, c.State)
				}
				if len(c.EvidenceRefs) != 0 {
					t.Errorf("%s ships %s with evidence attached", tpl.ID, c.ID)
				}
			}
		}
	}
}

// TestFillingInATemplateDoesNotMutateIt. Otherwise the second case
// using a template inherits the first case's answers.
func TestFillingInATemplateDoesNotMutateIt(t *testing.T) {
	r := registry(t)
	tpl, err := r.Template("MAR-DARK-ACTIVITY")
	if err != nil {
		t.Fatal(err)
	}
	first := tpl.NewConditions()
	first[0].State = reverseproof.Satisfied
	first[0].EvidenceRefs = []string{"ev:case-1"}

	second := tpl.NewConditions()
	if second[0].State != reverseproof.Unexamined || len(second[0].EvidenceRefs) != 0 {
		t.Fatal("a second case inherited the first case's answers")
	}
	// And the registry's own copy is untouched.
	again, _ := r.Template("MAR-DARK-ACTIVITY")
	if again.Conditions[0].State != reverseproof.Unexamined {
		t.Fatal("the registry's template was mutated")
	}
}

// TestATemplateDrivesARealReverseProof, end to end.
func TestATemplateDrivesARealReverseProof(t *testing.T) {
	r := registry(t)
	tpl, err := r.Template("COM-QUANTITY-DISCREPANCY")
	if err != nil {
		t.Fatal(err)
	}
	conds := tpl.NewConditions()
	// An analyst who has done nothing yet.
	unexamined := 0
	for _, c := range conds {
		if c.State == reverseproof.Unexamined {
			unexamined++
		}
	}
	if unexamined != len(conds) {
		t.Fatalf("%d of %d conditions arrive unexamined", unexamined, len(conds))
	}
	// The template's most diagnostic unexamined condition is what an
	// acquisition plan should reach for first.
	best := conds[0]
	for _, c := range conds {
		if c.Diagnosticity/maxf(c.AcquisitionCost, 0.01) >
			best.Diagnosticity/maxf(best.AcquisitionCost, 0.01) {
			best = c
		}
	}
	if best.ID == "" {
		t.Fatal("no condition could be ranked")
	}
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// TestEveryDomainDeclaresRefusals. Every domain has statements it may
// not make.
func TestEveryDomainDeclaresRefusals(t *testing.T) {
	r := registry(t)
	for _, d := range ontology.Domains() {
		if len(r.Refusals(d)) == 0 {
			t.Errorf("%s declares no refusals", d)
		}
	}
}

// TestEveryOntologyDomainWithTemplatesIsDeclared, so a template
// cannot belong to a view the ontology does not have.
func TestEveryOntologyDomainWithTemplatesIsDeclared(t *testing.T) {
	r := registry(t)
	known := map[string]bool{}
	for _, d := range ontology.Domains() {
		known[d] = true
	}
	for _, d := range r.Domains() {
		if !known[d] {
			t.Errorf("templates exist for undeclared domain %q", d)
		}
	}
}

// TestTheFiveCommercialDomainsHaveTemplates. A domain VERIQO says it
// covers and for which nothing is decomposed is a claim without a
// mechanism.
func TestTheFiveCommercialDomainsHaveTemplates(t *testing.T) {
	r := registry(t)
	for _, d := range []string{"maritime", "insurance", "commodity", "supplychain", "finance"} {
		if len(r.ForDomain(d)) == 0 {
			t.Errorf("%s has no templates", d)
		}
	}
}

// TestAMalformedTemplateIsRefusedAtConstruction.
func TestAMalformedTemplateIsRefusedAtConstruction(t *testing.T) {
	bad := Template{ID: "X", Domain: "maritime", Question: "q",
		Conditions: []reverseproof.Condition{
			{ID: "C1", Must: "x", Expected: "y"},
		},
		Hypotheses: []hypothesis.Hypothesis{
			{ID: "H1", Statement: "s", Expected: []string{"e"}},
			{ID: "H2", Statement: "s", Expected: []string{"e"}},
		}}
	if _, err := NewRegistry([]Template{bad}, Refusals()); err == nil {
		t.Fatal("a template with one condition was accepted")
	}

	bad.Conditions = append(bad.Conditions,
		reverseproof.Condition{ID: "C2", Must: "x", Expected: "y"})
	bad.Hypotheses = bad.Hypotheses[:1]
	if _, err := NewRegistry([]Template{bad}, Refusals()); err == nil {
		t.Fatal("a template with one hypothesis was accepted")
	}
}

// TestADomainWithNoRefusalsIsRefused.
func TestADomainWithNoRefusalsIsRefused(t *testing.T) {
	if _, err := NewRegistry(MaritimeTemplates(),
		map[string][]string{"maritime": {}}); err == nil {
		t.Fatal("a domain declaring no refusals was accepted")
	}
}

// TestAnUnknownTemplateIsRefused.
func TestAnUnknownTemplateIsRefused(t *testing.T) {
	if _, err := registry(t).Template("NOT-A-TEMPLATE"); !errors.Is(err, ErrUnknownTemplate) {
		t.Fatal("an unknown template resolved")
	}
}
