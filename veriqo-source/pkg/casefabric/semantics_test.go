package casefabric

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veriqo/pkg/ontology"
)

// TestEveryRegisteredDomainDeclaresCompleteSemantics is the requirement:
// six domains projecting onto one spine is only worth having if each
// says what it is for.
func TestEveryRegisteredDomainDeclaresCompleteSemantics(t *testing.T) {
	if err := ValidateAllSemantics(); err != nil {
		t.Fatalf("ValidateAllSemantics: %v", err)
	}
	if len(DomainSemantics()) != len(RegisteredDomains()) {
		t.Fatalf("%d declarations for %d registered domains",
			len(DomainSemantics()), len(RegisteredDomains()))
	}
}

// TestDomainSemanticsAreDataNotEngines is the load-bearing constraint.
//
// A domain that needed its own engine to express its semantics would
// have forked the fabric. Declared as data, the semantics project onto
// the shared machinery; declared as code, they become a rival to it.
func TestDomainSemanticsAreDataNotEngines(t *testing.T) {
	root := moduleRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "pkg", "casefabric", "semantics.go"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	src := string(body)

	// The declarations must not import or reach into any domain package:
	// that would invert the relationship the fabric exists to hold.
	for _, forbidden := range []string{
		`"veriqo/pkg/insurance/`, `"veriqo/pkg/domain/`, `"veriqo/pkg/moat/`,
	} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("semantics.go imports %s: semantics are data over the shared fabric, not domain code", forbidden)
		}
	}
	// And there must be no per-domain BEHAVIOUR. The precise property is
	// that nothing branches on a specific domain: a lookup comparing
	// against a caller's parameter is fine, but a comparison or switch
	// against DomainMaritime, DomainInsurance and so on is an engine
	// growing inside a data table.
	//
	// The declaration table assigns those constants (`Domain: DomainMaritime,`)
	// and that is exactly what it should do; only comparisons are the
	// problem.
	for _, forbidden := range []string{
		"== Domain", "!= Domain", "case Domain", "switch s.Domain", "switch domain",
	} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("semantics.go branches on a specific domain (%q): that is domain-specific behaviour, not declared semantics", forbidden)
		}
	}
}

// TestEveryDomainWorksInRegisteredObjectTypes: a domain working outside
// the ontology is working outside VERIQO.
func TestEveryDomainWorksInRegisteredObjectTypes(t *testing.T) {
	for _, s := range DomainSemantics() {
		for _, ty := range s.ObjectTypes {
			if !ontology.IsKnownObjectType(ty) {
				t.Fatalf("domain %q works in unregistered type %q", s.Domain, ty)
			}
			if _, ok := ontology.ContractForType(ty); !ok {
				t.Fatalf("domain %q works in type %q, which has no object contract", s.Domain, ty)
			}
		}
	}
}

// TestEveryObligationHasAFalsifier: an obligation with no falsifier is
// not a test, and a domain built on untestable obligations proves
// nothing.
func TestEveryObligationHasAFalsifier(t *testing.T) {
	for _, s := range DomainSemantics() {
		for _, o := range s.Obligations {
			if strings.TrimSpace(o.FalsifiedBy) == "" {
				t.Fatalf("domain %q obligation %q states no falsifier", s.Domain, o.Claim)
			}
			if len(o.Requires) == 0 {
				t.Fatalf("domain %q obligation %q requires nothing", s.Domain, o.Claim)
			}
		}
	}
}

// TestNoDomainOutcomeAdjudicates holds the boundary at the vocabulary
// level, per domain.
func TestNoDomainOutcomeAdjudicates(t *testing.T) {
	for _, s := range DomainSemantics() {
		for _, o := range s.OutcomeVocabulary {
			if err := (Outcome{Disposition: o, Summary: "check"}).Validate(); err != nil {
				t.Fatalf("domain %q outcome %q adjudicates: %v", s.Domain, o, err)
			}
		}
	}
}

// TestEveryEvidenceClassNamesARealWorldSource is what makes the
// LIVE_DATA blocker concrete rather than abstract: each class names
// where the data would actually come from, and every one of them is
// currently a fixture.
func TestEveryEvidenceClassNamesARealWorldSource(t *testing.T) {
	for _, s := range DomainSemantics() {
		if len(s.EvidenceClasses) < 4 {
			t.Fatalf("domain %q declares only %d evidence classes", s.Domain, len(s.EvidenceClasses))
		}
		for _, e := range s.EvidenceClasses {
			if len(strings.Fields(e.Source)) < 2 {
				t.Fatalf("domain %q evidence class %q names its source too thinly: %q", s.Domain, e.Name, e.Source)
			}
		}
	}
}

// TestEveryDomainHasPartyMediatedEvidence is an honesty check, not a
// completeness one. A domain claiming every class is independently
// acquired has not looked at where its evidence comes from.
func TestEveryDomainHasPartyMediatedEvidence(t *testing.T) {
	for _, s := range DomainSemantics() {
		if len(s.PartyMediatedClasses()) == 0 {
			t.Fatalf("domain %q claims no party-mediated evidence at all, which is not credible for any real matter", s.Domain)
		}
	}
}

// TestTheDisputeDomainDefersDecision is the positioning boundary,
// checked in the one domain where it matters most.
func TestTheDisputeDomainDefersDecision(t *testing.T) {
	s, ok := SemanticsFor(DomainDispute)
	if !ok {
		t.Fatal("the dispute domain declares no semantics")
	}
	joined := strings.ToLower(strings.Join(s.Rules, " "))
	if !strings.Contains(joined, "decision-maker decides") && !strings.Contains(joined, "the arbitrator, court") {
		t.Fatalf("the dispute domain must state who decides: %v", s.Rules)
	}
	for _, o := range s.OutcomeVocabulary {
		if strings.Contains(strings.ToLower(o), "determin") {
			t.Fatalf("the dispute domain must not end by determining anything: %q", o)
		}
	}
}

// TestMaritimeAbsenceRuleMatchesTheObservabilityGate is a spot check
// that a domain rule states the shared fabric's actual behaviour rather
// than a domain-local variation of it.
func TestMaritimeAbsenceRuleMatchesTheObservabilityGate(t *testing.T) {
	s, _ := SemanticsFor(DomainMaritime)
	joined := strings.ToLower(strings.Join(s.Rules, " "))
	if !strings.Contains(joined, "observability gate") {
		t.Fatalf("the maritime absence rule must defer to the shared observability gate: %v", s.Rules)
	}
	if !strings.Contains(joined, "single source") {
		t.Fatalf("the maritime rules must address aggregated feeds as one source: %v", s.Rules)
	}
}

// TestSemanticsForUnknownDomain returns false rather than an empty
// declaration that would read as "this domain has no semantics".
func TestSemanticsForUnknownDomain(t *testing.T) {
	if _, ok := SemanticsFor("astrology"); ok {
		t.Fatal("an unregistered domain must not resolve to a declaration")
	}
}

// TestAnUnregisteredDomainCannotDeclareSemantics closes the other
// direction: semantics for a domain the fabric does not know about.
func TestAnUnregisteredDomainCannotDeclareSemantics(t *testing.T) {
	s := Semantics{
		Domain: "astrology", ObjectTypes: []ontology.ObjectType{ontology.ObjectCase},
		EvidenceClasses: []EvidenceClass{{Name: "chart", Source: "an ephemeris service"}},
		Obligations:     []ProofObligation{{Claim: "c", Requires: []string{"r"}, FalsifiedBy: "f"}},
		Rules:           []string{"r"}, OutcomeVocabulary: []string{"closed"},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("a domain not registered with the fabric must not declare semantics")
	}
}

func TestRenderSemanticsCoversEveryDomainAndSection(t *testing.T) {
	out := RenderSemantics()
	for _, s := range DomainSemantics() {
		if !strings.Contains(out, strings.ToUpper(s.Domain)) {
			t.Fatalf("domain %q missing from the render", s.Domain)
		}
	}
	for _, section := range []string{"ONTOLOGY", "EVIDENCE", "OBLIGATIONS", "falsified by", "RULES", "OUTCOMES"} {
		if !strings.Contains(out, section) {
			t.Fatalf("section %q missing from the render", section)
		}
	}
	if !strings.Contains(out, "[party-mediated]") {
		t.Fatal("the render must mark party-mediated evidence")
	}
}
