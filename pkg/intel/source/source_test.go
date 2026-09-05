package source

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/governance/classification"
)

var sat = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func basis() *LegalBasis {
	return &LegalBasis{Jurisdiction: "England and Wales", Purpose: "sanctions screening",
		Opinion: "memo 2026-07 on adverse-information processing", By: "external counsel",
		At: sat.Add(-30 * 24 * time.Hour)}
}

func material(c Class, producer string, l Lawfulness, b *LegalBasis) Material {
	return Material{ID: "mat:1", Class: c, ProducerID: producer, Lawfulness: l, Basis: b,
		ObservedAt: sat, ContentHash: "abc123"}
}

// TestBreachDerivedMaterialCannotFoundAFinding, and cannot be
// disclosed or trained on either.
func TestBreachDerivedMaterialCannotFoundAFinding(t *testing.T) {
	m := material(BreachDerived, "", Established, basis())
	if err := m.Validate(); err != nil {
		t.Fatalf("a lawfully held breach corpus was refused outright: %v", err)
	}
	for _, u := range []Use{Found, Corroborate, Disclose, Train, Lead} {
		if err := m.Permit(u, sat); !errors.Is(err, ErrPurposeRefused) {
			t.Fatalf("breach-derived material was permitted for %s: %v", u, err)
		}
	}
	// Screening is the one thing it may do.
	if err := m.Permit(Screen, sat); err != nil {
		t.Fatalf("breach-derived material was refused for screening: %v", err)
	}
}

// TestRestrictedClassesCannotBeHeldWithoutALegalBasis. The refusal is
// at Validate, not at use: holding is the act that needs the basis.
func TestRestrictedClassesCannotBeHeldWithoutALegalBasis(t *testing.T) {
	for _, c := range []Class{BreachDerived, HiddenServiceForum, AnonymousDisclosure, AdverseMedia} {
		m := material(c, "producer-1", Presumed, nil)
		if err := m.Validate(); !errors.Is(err, ErrUnlawful) {
			t.Fatalf("%s was held with no legal basis: %v", c, err)
		}
		// And "we assume it's fine" is not a basis.
		m.Lawfulness = Established
		if err := m.Validate(); !errors.Is(err, ErrNoBasis) {
			t.Fatalf("%s claimed ESTABLISHED with no basis record: %v", c, err)
		}
	}
}

// TestALegalBasisMustNameAJurisdictionAndAPurpose. An opinion for one
// of either says nothing about another.
func TestALegalBasisMustNameAJurisdictionAndAPurpose(t *testing.T) {
	for _, mut := range []func(*LegalBasis){
		func(b *LegalBasis) { b.Jurisdiction = "" },
		func(b *LegalBasis) { b.Purpose = "" },
		func(b *LegalBasis) { b.Opinion = "" },
		func(b *LegalBasis) { b.By = "" },
		func(b *LegalBasis) { b.At = time.Time{} },
	} {
		b := basis()
		mut(b)
		if err := b.Validate(); !errors.Is(err, ErrNoBasis) {
			t.Fatalf("an incomplete basis validated: %+v", b)
		}
	}
}

// TestALapsedBasisStopsUse.
func TestALapsedBasisStopsUse(t *testing.T) {
	b := basis()
	until := sat.Add(-24 * time.Hour)
	b.Until = &until
	m := material(HiddenServiceForum, "", Established, b)
	if err := m.Permit(Screen, sat); !errors.Is(err, ErrUnlawful) {
		t.Fatalf("material with a lapsed basis was used: %v", err)
	}
	if err := m.Permit(Screen, sat.Add(-48*time.Hour)); err != nil {
		t.Fatalf("material was refused while its basis was current: %v", err)
	}
}

// TestSixOutletsCarryingOneStoryAreNotSixSources. Adverse media does
// not count for corroboration at the class level at all, because the
// republication structure defeats naive counting.
func TestSixOutletsCarryingOneStoryAreNotSixSources(t *testing.T) {
	var s Support
	for i := 0; i < 6; i++ {
		m := material(AdverseMedia, "outlet-x", Established, basis())
		m.ID = contract.ID("mat:media-" + string(rune('a'+i)))
		s.Material = append(s.Material, m)
	}
	n, excluded := s.CorroborationCount()
	if n != 0 {
		t.Fatalf("six adverse-media items counted as %d sources", n)
	}
	if len(excluded) != 6 {
		t.Fatalf("%d items excluded from the count", len(excluded))
	}
	if _, err := s.FoundFinding(sat); !errors.Is(err, ErrSoleSupport) {
		t.Fatalf("six allegations founded a finding: %v", err)
	}
	if !strings.Contains(err(t, s).Error(), "Numerousness does not substitute") {
		t.Fatal("the refusal does not say why repetition is not enough")
	}
}

func err(t *testing.T, s Support) error {
	t.Helper()
	_, e := s.FoundFinding(sat)
	return e
}

// TestTenAnonymousDisclosuresAreStillZeroProducers.
func TestTenAnonymousDisclosuresAreStillZeroProducers(t *testing.T) {
	var s Support
	for i := 0; i < 10; i++ {
		m := material(AnonymousDisclosure, "", Established, basis())
		m.ID = contract.ID("mat:anon-" + string(rune('a'+i)))
		s.Material = append(s.Material, m)
	}
	n, _ := s.CorroborationCount()
	if n != 0 {
		t.Fatalf("ten anonymous disclosures counted as %d sources", n)
	}
	if _, e := s.FoundFinding(sat); !errors.Is(e, ErrSoleSupport) {
		t.Fatalf("ten anonymous disclosures founded a finding: %v", e)
	}
}

// TestOneAttributableSourceCarriesTheFindingAndTheCaveatsTravel.
//
// The whole point is not that weak material is banned. It is that
// something else carries the weight, and the weak material's caveats
// still reach the finding.
func TestOneAttributableSourceCarriesTheFindingAndTheCaveatsTravel(t *testing.T) {
	s := Support{Material: []Material{
		material(OfficialRegister, "companies-house", Presumed, nil),
		func() Material {
			m := material(HiddenServiceForum, "", Established, basis())
			m.ID = "mat:forum-1"
			m.VenueID = "forum-a"
			return m
		}(),
	}}
	caveats, e := s.FoundFinding(sat)
	if e != nil {
		t.Fatalf("a register-founded finding with forum context was refused: %v", e)
	}
	joined := strings.Join(caveats, " ")
	for _, want := range []string{
		"records what was filed",          // the register's caveat
		"designed to prevent attribution", // the forum's caveat
		"every incentive to mislead",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the caveat %q did not travel to the finding: %v", want, caveats)
		}
	}
	// And the forum item still does not count towards corroboration.
	n, excluded := s.CorroborationCount()
	if n != 1 {
		t.Fatalf("corroboration count is %d", n)
	}
	if len(excluded) != 1 || !strings.Contains(excluded[0], "HIDDEN_SERVICE_FORUM") {
		t.Fatalf("the excluded item is %v", excluded)
	}
}

// TestTheVenueIsNotTheProducer. Conflating them is how a forum becomes
// an author.
func TestTheVenueIsNotTheProducer(t *testing.T) {
	con, err := Of(HiddenServiceForum)
	if err != nil {
		t.Fatal(err)
	}
	if con.Attributable {
		t.Fatal("a venue designed to prevent attribution was marked attributable")
	}
	if con.RequiresProducer {
		t.Fatal("a class with no identifiable producer was made to require one, which " +
			"would force callers to invent a producer id")
	}
	// The two classes are genuinely distinct, which is the point.
	breach, _ := Of(BreachDerived)
	if len(breach.PermittedUses) >= len(con.PermittedUses) {
		t.Fatal("breach-derived material is no more constrained than a forum post; the " +
			"classes have collapsed into one")
	}
}

// TestRestrictedMaterialIsMarkedAtSecretAndCannotBeExportedOrTrainedOn.
func TestRestrictedMaterialIsMarkedAtSecretAndCannotBeExportedOrTrainedOn(t *testing.T) {
	for _, c := range []Class{BreachDerived, HiddenServiceForum, AnonymousDisclosure} {
		m := material(c, "", Established, basis())
		mk, e := m.Marking()
		if e != nil {
			t.Fatal(e)
		}
		if mk.Level != classification.Secret {
			t.Fatalf("%s is marked %s", c, mk.Level)
		}
		for _, h := range []classification.Handling{classification.NoExport,
			classification.NoTraining, classification.NoRedistribution} {
			if !mk.Has(h) {
				t.Fatalf("%s does not carry %s", c, h)
			}
		}
	}
}

// TestEveryClassIsCompleteAndCoherent. A table this consequential must
// not have a line nobody checked.
func TestEveryClassIsCompleteAndCoherent(t *testing.T) {
	for _, c := range Classes() {
		con, e := Of(c)
		if e != nil {
			t.Fatalf("%s: %v", c, e)
		}
		if strings.TrimSpace(con.Rationale) == "" {
			t.Fatalf("%s states no rationale; a refusal an analyst cannot understand is "+
				"one they will route around", c)
		}
		if len(con.PermittedUses) == 0 {
			t.Fatalf("%s permits nothing at all", c)
		}
		// A class that may found a finding must be countable for
		// corroboration and vice versa is not required -- but a class
		// that may found while being uncountable would be incoherent.
		if con.MayFound && !con.CountsForCorroboration {
			t.Fatalf("%s may found a finding and cannot be counted as a source", c)
		}
		// Anything unattributable must not be able to found.
		if !con.Attributable && con.MayFound {
			t.Fatalf("%s is unattributable and may found a finding; Law 2 cannot be "+
				"satisfied for it", c)
		}
		if !con.Attributable && len(con.MandatoryCaveats) == 0 {
			t.Fatalf("%s is unattributable and carries no mandatory caveat", c)
		}
		if _, e := Of(Class("INVENTED")); !errors.Is(e, ErrUnknownClass) {
			t.Fatal("an invented class resolved")
		}
	}
	if len(Classes()) != 11 {
		t.Fatalf("%d classes", len(Classes()))
	}
}

// TestInferenceCannotCorroborateItsOwnInputs.
func TestInferenceCannotCorroborateItsOwnInputs(t *testing.T) {
	con, _ := Of(Inference)
	if con.CountsForCorroboration {
		t.Fatal("a derived value counts as an independent observation")
	}
	if con.MayFound {
		t.Fatal("a derived value may found a finding")
	}
}

// TestDescribeCarriesTheCaveatsAndTheReason.
func TestDescribeCarriesTheCaveatsAndTheReason(t *testing.T) {
	d := Describe(BreachDerived)
	for _, want := range []string{"may found a finding: false", "legal basis needed:  true",
		"unauthorised access", "routinely salted"} {
		if !strings.Contains(d, want) {
			t.Fatalf("Describe omits %q:\n%s", want, d)
		}
	}
}
