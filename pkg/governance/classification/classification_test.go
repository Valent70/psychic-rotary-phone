package classification

import (
	"errors"
	"testing"
)

// TestTheZeroValueIsNotPublic. If Unset compared as Public, an
// artefact nobody classified would be exportable.
func TestTheZeroValueIsNotPublic(t *testing.T) {
	var m Marking
	if m.Valid() {
		t.Fatal("the zero marking is valid; unclassified data would pass a classification check")
	}
	if Unset.Valid() {
		t.Fatal("Unset is a valid level")
	}
	if Unset >= Public {
		t.Fatal("Unset orders at or above PUBLIC")
	}
	if err := Readable(MustNew(Secret), m); !errors.Is(err, ErrNotClassified) {
		t.Fatalf("an unmarked artefact was readable: %v", err)
	}
}

// TestJoinTakesTheLeastUpperBoundOnBothAxes is the rule that stops an
// export bundle from carrying the label of whichever object was
// constructed first.
func TestJoinTakesTheLeastUpperBoundOnBothAxes(t *testing.T) {
	j, err := Join(
		MustNew(Internal, NoExport),
		MustNew(Restricted),
		MustNew(Public, PersonalData),
	)
	if err != nil {
		t.Fatal(err)
	}
	if j.Level != Restricted {
		t.Fatalf("join level = %s, want RESTRICTED", j.Level)
	}
	if !j.Has(NoExport) || !j.Has(PersonalData) {
		t.Fatalf("join dropped a caveat: %s", j)
	}
}

// TestDominationIsAPartialOrder is the trap. A total-order reading
// concludes that SECRET may be exported because its level exceeds
// PUBLIC's.
func TestDominationIsAPartialOrder(t *testing.T) {
	secret := MustNew(Secret)
	publicNoExport := MustNew(Public, NoExport)

	if secret.Dominates(publicNoExport) {
		t.Fatal("SECRET dominates PUBLIC//NO_EXPORT: the caveat was treated as implied by the level")
	}
	if publicNoExport.Dominates(secret) {
		t.Fatal("PUBLIC//NO_EXPORT dominates SECRET on level")
	}
	// Neither dominates the other; that is what partial means.
	if !MustNew(Secret, NoExport).Dominates(publicNoExport) {
		t.Fatal("SECRET//NO_EXPORT fails to dominate PUBLIC//NO_EXPORT")
	}
}

// TestADerivativeCannotBeDowngraded.
func TestADerivativeCannotBeDowngraded(t *testing.T) {
	_, err := Derive(MustNew(Internal), MustNew(Restricted), MustNew(Public))
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade accepted: %v", err)
	}
	// Dropping a caveat is a downgrade too, even at a higher level.
	_, err = Derive(MustNew(Secret), MustNew(Internal, PersonalData))
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("a caveat was dropped by raising the level: %v", err)
	}
}

// TestADerivativeMayBeMoreRestrictive: a redaction manifest over
// public inputs is legitimately RESTRICTED.
func TestADerivativeMayBeMoreRestrictive(t *testing.T) {
	got, err := Derive(MustNew(Restricted, NoExport), MustNew(Public))
	if err != nil {
		t.Fatalf("an upgrade was refused: %v", err)
	}
	if got.Level != Restricted || !got.Has(NoExport) {
		t.Fatalf("upgrade not preserved: %s", got)
	}
}

// TestAnUnstatedProposalDefaultsToTheFloor rather than to nothing.
func TestAnUnstatedProposalDefaultsToTheFloor(t *testing.T) {
	got, err := Derive(Marking{}, MustNew(Confidential, PersonalData))
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != Confidential || !got.Has(PersonalData) {
		t.Fatalf("the default was not the source join: %s", got)
	}
}

// TestDeriveWithNoSourcesIsRefused. A finding with no evidence has no
// classification floor, and Law 1 says it should not exist at all.
func TestDeriveWithNoSourcesIsRefused(t *testing.T) {
	if _, err := Derive(MustNew(Public)); !errors.Is(err, ErrNotClassified) {
		t.Fatalf("a derivative with no sources was classified: %v", err)
	}
}

// TestClearanceLevelIsNotAKeyToEveryCaveat.
func TestClearanceLevelIsNotAKeyToEveryCaveat(t *testing.T) {
	reader := MustNew(Secret) // top level, no caveat authorisations
	personal := MustNew(Internal, PersonalData)
	if err := Readable(reader, personal); err == nil {
		t.Fatal("a SECRET clearance read PERSONAL_DATA without being authorised for it")
	}
	authorised := MustNew(Secret, PersonalData)
	if err := Readable(authorised, personal); err != nil {
		t.Fatalf("an authorised reader was refused: %v", err)
	}
}

// TestUseProhibitionsDoNotRestrictReading. NO_EXPORT limits what may
// be done with data, not who may look at it. Conflating them would
// make an analyst unable to read the evidence in their own case.
func TestUseProhibitionsDoNotRestrictReading(t *testing.T) {
	reader := MustNew(Internal)
	for _, h := range []Handling{NoExport, NoTraining, NoRedistribution, NoDerivative} {
		if err := Readable(reader, MustNew(Internal, h)); err != nil {
			t.Errorf("%s blocked reading: %v", h, err)
		}
	}
}

func TestClearanceBelowLevelIsRefused(t *testing.T) {
	if err := Readable(MustNew(Internal), MustNew(Restricted)); err == nil {
		t.Fatal("a reader below the artefact's level was admitted")
	}
}

// TestMarkingsNormaliseSoTheyHashAlike. Two markings with the same
// caveats in different orders must be the same value, or the same
// artefact gets two different digests.
func TestMarkingsNormaliseSoTheyHashAlike(t *testing.T) {
	a := MustNew(Internal, NoExport, PersonalData, NoExport)
	b := MustNew(Internal, PersonalData, NoExport)
	if a.String() != b.String() {
		t.Fatalf("orderings differ: %s vs %s", a, b)
	}
	if len(a.Handling) != 2 {
		t.Fatalf("duplicate caveat retained: %v", a.Handling)
	}
}

func TestParseLevelRejectsUnknownAndUnset(t *testing.T) {
	if _, err := ParseLevel("UNSET"); err == nil {
		t.Fatal("UNSET parsed as a level")
	}
	if _, err := ParseLevel("TOP_SECRET"); err == nil {
		t.Fatal("an unknown level parsed")
	}
	l, err := ParseLevel("restricted")
	if err != nil || l != Restricted {
		t.Fatalf("ParseLevel(restricted) = %v, %v", l, err)
	}
}

func TestUnknownCaveatIsRefused(t *testing.T) {
	if _, err := New(Internal, Handling("PROBABLY_FINE")); err == nil {
		t.Fatal("an unknown caveat was accepted")
	}
}
