package ontology

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/contract"
	"veriqo/pkg/entity"
	"veriqo/pkg/governance/classification"
)

func mustVeriqo(t *testing.T) *Ontology {
	t.Helper()
	o, err := Veriqo(1)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// TestThereIsOneGraphNotSixIsTheWholePoint.
//
// If every object type belonged to exactly one domain and no edge
// crossed a boundary, this repository would contain five vertical
// products sharing a go.mod.
func TestThereIsOneGraphNotSixIsTheWholePoint(t *testing.T) {
	o := mustVeriqo(t)

	cross := o.CrossDomainRelationships()
	if len(cross) < 10 {
		t.Fatalf("only %d cross-domain relationships; the domains are not actually joined", len(cross))
	}

	// The specific chain the specification names must be walkable.
	chain := []struct{ rel, from, to string }{
		{"PERFORMED_VOYAGE", "Vessel", "Voyage"},
		{"CARRIED", "Voyage", "CargoLot"},
		{"EVIDENCED_BY_BL", "CargoLot", "BillOfLading"},
		{"UNDER_CONTRACT", "CargoLot", "Contract"},
		{"INVOICED_BY", "Contract", "Invoice"},
		{"SETTLED_BY", "Invoice", "Payment"},
		{"PAID_TO", "Payment", "Account"},
		{"CARGO_COVERED_BY", "CargoLot", "InsurancePolicy"},
		{"CLAIM_UNDER", "InsuranceClaim", "InsurancePolicy"},
	}
	for _, step := range chain {
		if err := o.CheckEdge(step.rel, step.from, step.to); err != nil {
			t.Errorf("the cross-domain chain is broken at %s: %v", step.rel, err)
		}
	}
}

// TestATypeAppearsInSeveralViews. A Vessel is one object seen through
// two projections, not two objects kept in sync.
func TestATypeAppearsInSeveralViews(t *testing.T) {
	o := mustVeriqo(t)
	v, err := o.ObjectType("Vessel")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Domains) < 2 {
		t.Fatalf("Vessel belongs to only %v", v.Domains)
	}
	marObjs, _ := o.View(Maritime)
	insObjs, _ := o.View(Insurance)
	inBoth := 0
	for _, a := range marObjs {
		for _, b := range insObjs {
			if a.Name == b.Name {
				inBoth++
			}
		}
	}
	if inBoth < 3 {
		t.Fatalf("only %d object types appear in both the maritime and insurance views", inBoth)
	}
}

// TestEveryDomainViewIsNonEmpty. A declared view with nothing in it is
// a customer-facing mode that shows an empty screen.
func TestEveryDomainViewIsNonEmpty(t *testing.T) {
	o := mustVeriqo(t)
	for _, d := range Domains() {
		objs, rels := o.View(d)
		if len(objs) == 0 {
			t.Errorf("the %s view has no object types", d)
		}
		if len(rels) == 0 {
			t.Errorf("the %s view has no relationships", d)
		}
	}
}

// TestPaymentInstructionAndSettlementAreSeparateProperties.
//
// The law is: never infer payment execution from a payment
// instruction. One timestamp field would force exactly that inference,
// because there would be nowhere to record that an instruction was
// issued and never settled.
func TestPaymentInstructionAndSettlementAreSeparateProperties(t *testing.T) {
	o := mustVeriqo(t)
	p, err := o.ObjectType("Payment")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Property("instructed_at"); !ok {
		t.Error("Payment has no instructed_at")
	}
	if _, ok := p.Property("settled_at"); !ok {
		t.Error("Payment has no settled_at")
	}
	// And neither may be required: an instruction with no settlement
	// is exactly the state the distinction exists to represent.
	for _, name := range []string{"instructed_at", "settled_at"} {
		pr, _ := p.Property(name)
		if pr.Required {
			t.Errorf("Payment.%s is required; an instruction that was never settled "+
				"could not be recorded", name)
		}
	}
}

// TestOwnershipAndControlAreTemporal. A system that stores them
// without a period asserts that today's owner always owned it.
func TestOwnershipAndControlAreTemporal(t *testing.T) {
	o := mustVeriqo(t)
	for _, name := range []string{"OWNED_BY", "OPERATED_BY", "BENEFICIALLY_OWNED_BY",
		"COVERED_BY", "ACCOUNT_HELD_BY"} {
		r, err := o.RelationshipType(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !r.Temporal {
			t.Errorf("%s is not temporal; the graph would assert that the current "+
				"holder always held it", name)
		}
	}
}

// TestEveryQuantityDeclaresAUnit. Two quantities with no units are
// comparable and should not be -- which is the cargo-discrepancy bug
// in one sentence.
func TestEveryQuantityDeclaresAUnit(t *testing.T) {
	o := mustVeriqo(t)
	checked := 0
	for _, ot := range o.ObjectTypes() {
		for _, p := range ot.Properties {
			if p.Type != Quantity {
				continue
			}
			checked++
			if strings.TrimSpace(p.Unit) == "" {
				t.Errorf("%s.%s is a quantity with no unit", ot.Name, p.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no quantity properties exist; this test governs nothing")
	}
}

// TestAnUnsetPropertyMarkingInheritsRatherThanDefaultingToPublic.
//
// This is the direction that matters: forgetting to mark a property
// must not disclose it.
func TestAnUnsetPropertyMarkingInheritsRatherThanDefaultingToPublic(t *testing.T) {
	o := mustVeriqo(t)
	cargo, err := o.ObjectType("CargoLot")
	if err != nil {
		t.Fatal(err)
	}
	if cargo.Classification.Level != classification.Confidential {
		t.Fatalf("premise changed: CargoLot is %s", cargo.Classification)
	}
	p, ok := cargo.Property("commodity")
	if !ok {
		t.Fatal("CargoLot has no commodity property")
	}
	if p.Classification.Valid() {
		t.Fatal("premise changed: this property was expected to be unmarked")
	}
	eff, err := p.EffectiveClassification(cargo.Classification)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Level != classification.Confidential {
		t.Fatalf("an unmarked property resolved to %s rather than inheriting CONFIDENTIAL", eff)
	}
}

// TestAPropertyMayBeMoreRestrictiveThanItsObject. Law 10 reaching
// property level is only real if this works.
func TestAPropertyMayBeMoreRestrictiveThanItsObject(t *testing.T) {
	o := mustVeriqo(t)
	org, err := o.ObjectType("Organisation")
	if err != nil {
		t.Fatal(err)
	}
	if org.Classification.Level != classification.Internal {
		t.Fatalf("premise changed: Organisation is %s", org.Classification)
	}
	bo, ok := org.Property("beneficial_owner")
	if !ok {
		t.Fatal("Organisation has no beneficial_owner property")
	}
	eff, err := bo.EffectiveClassification(org.Classification)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Level != classification.Restricted || !eff.Has(classification.PersonalData) {
		t.Fatalf("beneficial_owner resolved to %s; property-level security is not in force", eff)
	}
}

// TestAPropertyMayNotBeClassifiedBelowItsObject.
func TestAPropertyMayNotBeClassifiedBelowItsObject(t *testing.T) {
	bad := ObjectType{Name: "X", Kind: entity.Document, Domains: []string{"d"},
		Classification: classification.MustNew(classification.Restricted),
		Properties: []Property{{Name: "p", Type: Text,
			Classification: classification.MustNew(classification.Public)}}}
	if err := bad.Validate(); !errors.Is(err, classification.ErrDowngrade) {
		t.Fatalf("a PUBLIC property on a RESTRICTED object was accepted: %v", err)
	}
}

// TestADanglingRelationshipIsRefusedAtConstruction. Schema drift
// degrades silently: queries just return less.
func TestADanglingRelationshipIsRefusedAtConstruction(t *testing.T) {
	objs := []ObjectType{{Name: "A", Kind: entity.Document, Domains: []string{"d"},
		Classification: classification.MustNew(classification.Internal)}}
	rels := []RelationshipType{{Name: "R", From: "A", To: "GhostType",
		Cardinality: OneToMany, Domains: []string{"d"},
		Classification: classification.MustNew(classification.Internal)}}
	_, err := New(contract.Version{Component: "t", Revision: 1}, objs, rels)
	if !errors.Is(err, ErrDanglingType) {
		t.Fatalf("a relationship to an undeclared type was accepted: %v", err)
	}
}

// TestEdgeDirectionIsChecked. An inverted edge is a different claim.
func TestEdgeDirectionIsChecked(t *testing.T) {
	o := mustVeriqo(t)
	if err := o.CheckEdge("SETTLED_BY", "Invoice", "Payment"); err != nil {
		t.Fatalf("the declared direction was refused: %v", err)
	}
	if err := o.CheckEdge("SETTLED_BY", "Payment", "Invoice"); !errors.Is(err, ErrEndpointMismatch) {
		t.Fatalf("an inverted edge was accepted: %v", err)
	}
	if err := o.CheckEdge("NOT_A_RELATIONSHIP", "Invoice", "Payment"); !errors.Is(err, ErrUnknownRelationship) {
		t.Fatalf("an undeclared relationship was accepted: %v", err)
	}
}

// TestAnUnversionedOntologyIsRefused.
func TestAnUnversionedOntologyIsRefused(t *testing.T) {
	if _, err := New(contract.Version{}, VeriqoObjects(), VeriqoRelationships()); !errors.Is(err, contract.ErrUnversioned) {
		t.Fatal("an unversioned ontology was built; its graphs could not be replayed")
	}
}

// TestDuplicatesAreRefused.
func TestDuplicatesAreRefused(t *testing.T) {
	objs := append(VeriqoObjects(), VeriqoObjects()[0])
	if _, err := New(contract.Version{Component: "t", Revision: 1}, objs, nil); !errors.Is(err, ErrDuplicate) {
		t.Fatal("a duplicate object type was accepted")
	}
}

// TestASymmetricRelationshipBetweenDifferentTypesIsRefused: it cannot
// hold in both directions.
func TestASymmetricRelationshipBetweenDifferentTypesIsRefused(t *testing.T) {
	r := RelationshipType{Name: "R", From: "A", To: "B", Cardinality: ManyToMany,
		Symmetric: true, Domains: []string{"d"},
		Classification: classification.MustNew(classification.Internal)}
	if err := r.Validate(); err == nil {
		t.Fatal("a symmetric relationship between different types was accepted")
	}
}

// TestEveryObjectTypeBelongsToAView. A type in no view is unreachable
// from every customer-facing mode.
func TestEveryObjectTypeBelongsToAView(t *testing.T) {
	o := mustVeriqo(t)
	known := map[string]bool{}
	for _, d := range Domains() {
		known[d] = true
	}
	for _, ot := range o.ObjectTypes() {
		if len(ot.Domains) == 0 {
			t.Errorf("%s belongs to no view", ot.Name)
		}
		for _, d := range ot.Domains {
			if !known[d] {
				t.Errorf("%s belongs to undeclared view %q", ot.Name, d)
			}
		}
	}
}
