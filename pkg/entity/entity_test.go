package entity

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
)

var (
	y15 = time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	y18 = time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	y20 = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	y23 = time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	y26 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

func at(t time.Time) *time.Time { return &t }

func ident(s Scheme, v string, from time.Time, to *time.Time) Identifier {
	return Identifier{Scheme: s, Value: v,
		Scope:        contract.Interval{From: from, To: to},
		Authority:    "a registry",
		EvidenceRefs: []string{"evidenceversion:e1v1"}}
}

// TestMMSIIsWeakAndReassignable.
//
// It is the identifier most present in maritime data and the one most
// often treated as a primary key, and the reassignment behaviour makes
// that wrong in exactly the cases that matter.
func TestMMSIIsWeakAndReassignable(t *testing.T) {
	if MMSI.Strength() != Weak {
		t.Fatalf("MMSI is %v", MMSI.Strength())
	}
	if !MMSI.Reassignable() {
		t.Fatal("MMSI is not marked reassignable")
	}
	// IMO is the contrast: permanent, never reassigned.
	if IMO.Strength() != Definitive {
		t.Fatalf("IMO is %v", IMO.Strength())
	}
	if IMO.Reassignable() {
		t.Fatal("IMO is marked reassignable")
	}
}

// TestTwoVesselsEightYearsApartWithOneMMSIDoNotMatch.
//
// This is the silent merge the whole scheme exists to prevent: equal
// values, disjoint scopes.
func TestTwoVesselsEightYearsApartWithOneMMSIDoNotMatch(t *testing.T) {
	older := ident(MMSI, "123456789", y15, at(y18))
	newer := ident(MMSI, "123456789", y23, at(y26))
	if older.Matches(newer) {
		t.Fatal("two vessels carrying the same MMSI in disjoint periods matched")
	}
	if newer.Matches(older) {
		t.Fatal("the match is not symmetric")
	}
	// Overlapping periods DO match, so the test is not passing by
	// refusing everything. The intervals are half-open, so [y15,y18)
	// and [y18,y26) do NOT overlap -- a genuine overlap has to start
	// before the other ends.
	overlapping := ident(MMSI, "123456789", y15.AddDate(1, 0, 0), at(y26))
	if !older.Matches(overlapping) {
		t.Fatal("two overlapping MMSI periods did not match")
	}
}

// TestANonReassignableSchemeMatchesRegardlessOfPeriod.
//
// An IMO number is permanent, so two records carrying it are the same
// vessel whatever windows they were observed in. Requiring an overlap
// there would refuse legitimate matches for no reason.
func TestANonReassignableSchemeMatchesRegardlessOfPeriod(t *testing.T) {
	a := ident(IMO, "9074729", y15, at(y18))
	b := ident(IMO, "9074729", y23, at(y26))
	if !a.Matches(b) {
		t.Fatal("two disjoint observations of one IMO number did not match")
	}
}

// TestAMatchIsCaseInsensitiveOnValueButNotOnScheme.
func TestAMatchIsCaseInsensitiveOnValueButNotOnScheme(t *testing.T) {
	a := ident(CallSign, "9hab7", y20, at(y26))
	b := ident(CallSign, "9HAB7", y20, at(y26))
	if !a.Matches(b) {
		t.Fatal("call signs differing only in case did not match")
	}
	c := ident(LEI, "9HAB7", y20, at(y26))
	if b.Matches(c) {
		t.Fatal("the same value under two schemes matched")
	}
}

// TestAnIdentifierWithNoEvidenceIsSomebodysAssertion.
func TestAnIdentifierWithNoEvidenceIsSomebodysAssertion(t *testing.T) {
	i := ident(IMO, "9074729", y20, at(y26))
	i.EvidenceRefs = nil
	if err := i.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("an identifier with no evidence validated: %v", err)
	}
	i = ident(IMO, "", y20, at(y26))
	if err := i.Validate(); err == nil {
		t.Fatal("an identifier with no value validated")
	}
	i = Identifier{Scheme: "INVENTED", Value: "x",
		Scope: contract.Interval{From: y20}, EvidenceRefs: []string{"e"}}
	if err := i.Validate(); !errors.Is(err, ErrUnknownScheme) {
		t.Fatalf("an invented scheme validated: %v", err)
	}
}

// TestAnInvertedIntervalIsRefused. A scope ending before it starts
// would make every overlap test meaningless.
func TestAnInvertedIntervalIsRefused(t *testing.T) {
	i := ident(MMSI, "123456789", y23, at(y15))
	if err := i.Validate(); !errors.Is(err, ErrBadInterval) {
		t.Fatalf("an inverted interval validated: %v", err)
	}
}

// TestEverySchemeIsGradedAndTheGradingIsCoherent.
//
// A scheme with no strength defaults to Weak, which is the safe
// direction; but a scheme that is reassignable and DEFINITIVE would be
// incoherent, because permanence is what definitive means.
func TestEverySchemeIsGradedAndTheGradingIsCoherent(t *testing.T) {
	for _, s := range Schemes() {
		if !s.Valid() {
			t.Fatalf("%s is not valid", s)
		}
		if s.Reassignable() && s.Strength() == Definitive {
			t.Fatalf("%s is reassignable and DEFINITIVE; permanence is what definitive "+
				"means", s)
		}
	}
	if len(Schemes()) != 11 {
		t.Fatalf("%d schemes", len(Schemes()))
	}
	if Scheme("INVENTED").Strength() != Weak {
		t.Fatal("an unknown scheme does not default to WEAK, which is the safe direction")
	}
}

// TestAnAttributeIsAFactAboutAPeriodReadFromADocument.
func TestAnAttributeIsAFactAboutAPeriodReadFromADocument(t *testing.T) {
	e := Entity{ID: "entity:v1", Kind: Vessel, TenantID: "t-acme",
		Identifiers: []Identifier{ident(IMO, "9074729", y15, at(y26))},
		Attributes: []Attribute{
			{Name: "name", Value: "MV ALPHA",
				Scope:        contract.Interval{From: y15, To: at(y20)},
				EvidenceRefs: []string{"evidenceversion:e1v1"}},
			{Name: "name", Value: "MV BETA",
				Scope:        contract.Interval{From: y20, To: at(y26)},
				EvidenceRefs: []string{"evidenceversion:e2v1"}},
		}}
	if err := e.Validate(); err != nil {
		t.Fatalf("a well-formed entity was refused: %v", err)
	}
	// The name depends on when you ask, which is the point.
	if v, ok := e.AttributeAt("name", y18); !ok || v != "MV ALPHA" {
		t.Fatalf("in 2018 the name was %q (%v)", v, ok)
	}
	if v, ok := e.AttributeAt("name", y23); !ok || v != "MV BETA" {
		t.Fatalf("in 2023 the name was %q (%v)", v, ok)
	}
	if _, ok := e.AttributeAt("name", time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatal("a name was returned for a period no evidence covers")
	}
	// Sequential names are not a conflict.
	if c := e.ConflictingAttributes(); len(c) != 0 {
		t.Fatalf("sequential renaming reported as conflicting: %v", c)
	}
}

// TestTwoDifferentValuesInTheSameWindowAreAConflict.
//
// A vessel cannot have had two names at once. Detecting that is the
// difference between a record and a merged mess.
func TestTwoDifferentValuesInTheSameWindowAreAConflict(t *testing.T) {
	e := Entity{ID: "entity:v1", Kind: Vessel, TenantID: "t-acme",
		Identifiers: []Identifier{ident(IMO, "9074729", y15, at(y26))},
		Attributes: []Attribute{
			{Name: "flag", Value: "PANAMA",
				Scope:        contract.Interval{From: y20, To: at(y26)},
				EvidenceRefs: []string{"evidenceversion:e1v1"}},
			{Name: "flag", Value: "LIBERIA",
				Scope:        contract.Interval{From: y23, To: at(y26)},
				EvidenceRefs: []string{"evidenceversion:e2v1"}},
		}}
	c := e.ConflictingAttributes()
	if len(c) != 1 || c[0] != "flag" {
		t.Fatalf("conflicting attributes = %v", c)
	}
	// Identical values in an overlapping window are agreement, not
	// conflict.
	e.Attributes[1].Value = "PANAMA"
	if c := e.ConflictingAttributes(); len(c) != 0 {
		t.Fatalf("two sources agreeing reported as conflicting: %v", c)
	}
}

// TestAnEntityMustCarryAtLeastOneIdentifier.
func TestAnEntityMustCarryAtLeastOneIdentifier(t *testing.T) {
	e := Entity{ID: "entity:v1", Kind: Vessel, TenantID: "t-acme"}
	if err := e.Validate(); err == nil {
		t.Fatal("an entity with no identifiers validated")
	}
	e.Identifiers = []Identifier{ident(IMO, "9074729", y15, at(y26))}
	e.Kind = "INVENTED"
	if err := e.Validate(); err == nil {
		t.Fatal("an entity of an unknown kind validated")
	}
	e.Kind = Vessel
	e.TenantID = ""
	if err := e.Validate(); err == nil {
		t.Fatal("an entity with no tenant validated")
	}
}

// TestIdentifiersOfAndEvidenceRefsAreUsableForCitation.
func TestIdentifiersOfAndEvidenceRefsAreUsableForCitation(t *testing.T) {
	e := Entity{ID: "entity:v1", Kind: Vessel, TenantID: "t-acme",
		Identifiers: []Identifier{
			ident(IMO, "9074729", y15, at(y26)),
			ident(MMSI, "123456789", y15, at(y18)),
			ident(MMSI, "987654321", y20, at(y26)),
		}}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	if n := len(e.IdentifiersOf(MMSI)); n != 2 {
		t.Fatalf("%d MMSI identifiers", n)
	}
	if n := len(e.IdentifiersOf(LEI)); n != 0 {
		t.Fatalf("%d LEI identifiers on a vessel", n)
	}
	refs := e.EvidenceRefs()
	if len(refs) != 1 || refs[0] != "evidenceversion:e1v1" {
		t.Fatalf("evidence refs = %v", refs)
	}
	if s := e.Identifiers[0].String(); !strings.HasPrefix(s, "IMO:") {
		t.Fatalf("identifier renders as %q", s)
	}
}
