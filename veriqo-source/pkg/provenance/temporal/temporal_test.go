package temporal

import (
	"errors"
	"strings"
	"testing"
)

// TestTheZeroValueIsUnverified. Throughout VERIQO the zero value is the
// honest default. A reference nobody classified must not acquire
// standing by omission, which is exactly how a stale citation survives
// review.
func TestTheZeroValueIsUnverified(t *testing.T) {
	var r Reference
	if r.State != Unverified {
		t.Fatalf("the zero state is %q, want the unverified zero value", r.State)
	}
	if r.State.PresentableAsCurrent() {
		t.Fatal("an unclassified reference may not be presented as current")
	}
	if r.State.String() != "UNVERIFIED" {
		t.Fatalf("the zero state renders as %q; an empty string in a report reads as an oversight", r.State.String())
	}
}

// TestOnlyCurrentMayBePresentedAsCurrent. Derived is the interesting
// exclusion: a derived value is only as current as its source, and that
// is a property of the source.
func TestOnlyCurrentMayBePresentedAsCurrent(t *testing.T) {
	for _, s := range States() {
		got := s.PresentableAsCurrent()
		want := s == Current
		if got != want {
			t.Errorf("%s: PresentableAsCurrent = %v, want %v", s, got, want)
		}
	}
}

// TestSupersededMustNameItsSuccessor is what makes SUPERSEDED stronger
// than HISTORICAL. Without a successor the two say the same thing.
func TestSupersededMustNameItsSuccessor(t *testing.T) {
	r := Reference{Subject: "AUDIT-009", State: Superseded}
	if err := r.Validate(); !errors.Is(err, ErrSupersededWithoutSuccessor) {
		t.Fatalf("want ErrSupersededWithoutSuccessor, got %v", err)
	}
	r.SupersededBy = "AUDIT-013"
	if err := r.Validate(); err != nil {
		t.Fatalf("a superseded reference naming its successor must validate: %v", err)
	}
}

func TestDerivedMustNameItsSource(t *testing.T) {
	r := Reference{Subject: "effective-source-count", State: Derived}
	if err := r.Validate(); !errors.Is(err, ErrDerivedWithoutSource) {
		t.Fatalf("want ErrDerivedWithoutSource, got %v", err)
	}
}

func TestExternalMustNameItsAttestor(t *testing.T) {
	r := Reference{Subject: "RFC 3161 token", State: External}
	if err := r.Validate(); !errors.Is(err, ErrExternalWithoutAttestor) {
		t.Fatalf("want ErrExternalWithoutAttestor, got %v", err)
	}
}

// TestAContradictoryReferenceIsRefused. A reference marked CURRENT that
// also names a successor is two claims. Silently preferring one is how
// a stale citation survives.
func TestAContradictoryReferenceIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  Reference
		want string
	}{
		{"current with a successor",
			Reference{Subject: "x", State: Current, SupersededBy: "y"}, "only SUPERSEDED may"},
		{"historical with a source",
			Reference{Subject: "x", State: Historical, DerivedFrom: "y"}, "only DERIVED may"},
		{"current with an attestor",
			Reference{Subject: "x", State: Current, Attestor: "somebody"}, "only EXTERNAL may"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ref.Validate()
			if err == nil {
				t.Fatal("a contradictory reference validated")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error saying %q, got %v", tc.want, err)
			}
		})
	}
}

// TestPromotionNeedsAReasonAndDemotionDoesNot. Demotion is ordinary --
// discovering something is stale should be easy to record. Promotion is
// how a stale claim becomes a current one, so it needs evidence.
func TestPromotionNeedsAReasonAndDemotionDoesNot(t *testing.T) {
	current := Reference{Subject: "AUDIT-013", State: Current}
	if _, err := current.Transition(Historical, ""); err != nil {
		t.Fatalf("demotion must not require ceremony: %v", err)
	}
	hist := Reference{Subject: "AUDIT-009", State: Historical}
	if _, err := hist.Transition(Current, ""); !errors.Is(err, ErrPromotion) {
		t.Fatalf("want ErrPromotion for an unexplained promotion, got %v", err)
	}
	promoted, err := hist.Transition(Current, "regenerated and confirmed against the committed artefact")
	if err != nil {
		t.Fatalf("an explained promotion must be allowed: %v", err)
	}
	if promoted.State != Current {
		t.Fatalf("state is %s after promotion", promoted.State)
	}
}

// TestTransitionClearsContradictoryFields. A transition that left a
// successor behind would produce exactly the contradictory reference
// Validate refuses.
func TestTransitionClearsContradictoryFields(t *testing.T) {
	r := Reference{Subject: "AUDIT-009", State: Superseded, SupersededBy: "AUDIT-013"}
	back, err := r.Transition(Historical, "the successor was itself withdrawn")
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if back.SupersededBy != "" {
		t.Fatal("the successor survived a transition away from SUPERSEDED")
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("the transitioned reference must validate: %v", err)
	}
}

// TestExternalIsNotRankedAboveCurrent. An outside attestation of a
// stale fact is still stale, so EXTERNAL must not let a reference climb
// past CURRENT without a reason.
func TestExternalIsNotRankedAboveCurrent(t *testing.T) {
	if External.rank() >= Current.rank() {
		t.Fatal("EXTERNAL outranks CURRENT: an attested stale fact could be promoted silently")
	}
}

// TestSupersedeIsAOneCallDemotion, because the common case must be the
// easy one.
func TestSupersedeIsAOneCallDemotion(t *testing.T) {
	r := Reference{Subject: "AUDIT-009-case.resolved", State: Current}
	got, err := r.Supersede("AUDIT-013-case.resolved", "the sequencing audit moved resolution last")
	if err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if got.State != Superseded || got.SupersededBy != "AUDIT-013-case.resolved" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.PresentableAsCurrent() {
		t.Fatal("a superseded reference may not be presented as current")
	}
}

// TestAnUnclassifiedSetReportsWhatItHasNotLookedAt. A non-empty result
// is a finding, not an error.
func TestAnUnclassifiedSetReportsWhatItHasNotLookedAt(t *testing.T) {
	s, err := NewSet(
		Reference{Subject: "AUDIT-013", State: Current},
		Reference{Subject: "AUDIT-009", State: Superseded, SupersededBy: "AUDIT-013"},
		Reference{Subject: "some-uninspected-citation"},
	)
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	un := s.Unverified()
	if len(un) != 1 || un[0] != "some-uninspected-citation" {
		t.Fatalf("Unverified = %v, want the one unclassified subject", un)
	}
}

// TestEveryStateHasMeaning. A state with no stated semantics is a label,
// and a label is what this package exists to replace.
func TestEveryStateHasMeaning(t *testing.T) {
	for _, s := range States() {
		if strings.TrimSpace(s.Meaning()) == "" {
			t.Errorf("%s states no meaning", s)
		}
	}
}

// TestTheReportNamesEveryStatePresent.
func TestTheReportNamesEveryStatePresent(t *testing.T) {
	s, err := NewSet(
		Reference{Subject: "a", State: Current},
		Reference{Subject: "b", State: Historical},
		Reference{Subject: "c", State: Superseded, SupersededBy: "a"},
		Reference{Subject: "d", State: Derived, DerivedFrom: "a"},
		Reference{Subject: "e", State: External, Attestor: "a named TSA"},
		Reference{Subject: "f"},
	)
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	rep := s.Report()
	for _, st := range States() {
		if !strings.Contains(rep, st.String()) {
			t.Errorf("the report omits %s", st)
		}
	}
}
