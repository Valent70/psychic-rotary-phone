package entityconsistency

import (
	"testing"

	"veriqo/pkg/identity"
	"veriqo/pkg/moat/entity"
)

func mustIdentityResolver(t *testing.T) *identity.Resolver {
	t.Helper()
	r := identity.NewResolver()
	if err := r.RegisterAuthority(identity.Authority{
		SourceID: "port-monitor", Weight: 0.95,
		AuthoritativeFor: []identity.Kind{identity.KindIMO, identity.KindCallsign},
	}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	return r
}

func TestCheckReportsAgreementWhenBothSystemsMergeTheSamePair(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry()

	imo := Alias{Kind: "IMO", Value: "7778889"}
	cs := Alias{Kind: "CALLSIGN", Value: "CS-7778889"}

	if _, err := idR.Merge("resolution-agent", "port-monitor",
		identity.Identifier{Kind: identity.KindIMO, Value: imo.Value},
		identity.Identifier{Kind: identity.KindCallsign, Value: cs.Value}, 5, "cross-referenced"); err != nil {
		t.Fatalf("identity Merge: %v", err)
	}
	if _, err := entR.Merge("resolution-agent", entity.Alias{Kind: imo.Kind, Value: imo.Value},
		entity.Alias{Kind: cs.Kind, Value: cs.Value}, 5); err != nil {
		t.Fatalf("entity Merge: %v", err)
	}

	f, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if f.Diverges() {
		t.Fatalf("both systems agreeing must not be reported as a divergence: %+v", f)
	}
	if !f.IdentitySame || !f.EntitySame {
		t.Fatalf("expected both systems to consider the pair the same entity: %+v", f)
	}
}

// TestCheckDetectsDivergenceWhenOnlyOneSystemMerges is the property
// this package exists to guarantee: the exact "same object -> Entity-X
// / same object -> Entity-Y" risk the audit named must be caught, not
// silently missed.
func TestCheckDetectsDivergenceWhenOnlyOneSystemMerges(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry()

	imo := Alias{Kind: "IMO", Value: "7778889"}
	cs := Alias{Kind: "CALLSIGN", Value: "CS-7778889"}

	// Only pkg/moat/entity is told these denote the same vessel;
	// pkg/identity is never told.
	if _, err := entR.Merge("resolution-agent", entity.Alias{Kind: imo.Kind, Value: imo.Value},
		entity.Alias{Kind: cs.Kind, Value: cs.Value}, 5); err != nil {
		t.Fatalf("entity Merge: %v", err)
	}

	f, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !f.Diverges() {
		t.Fatalf("expected a detected divergence when only one system has merged the pair: %+v", f)
	}
	if f.IdentitySame {
		t.Fatal("pkg/identity was never told these are the same entity, IdentitySame must be false")
	}
	if !f.EntitySame {
		t.Fatal("pkg/moat/entity WAS told these are the same entity, EntitySame must be true")
	}
}

func TestCheckReportsEntityUnknownWhenNeitherAliasWasEverRegistered(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry()

	f, err := Check(idR, entR, Alias{Kind: "IMO", Value: "9999999"}, Alias{Kind: "CALLSIGN", Value: "ZZZZ"}, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if f.EntityKnown {
		t.Fatal("pkg/moat/entity has never seen either alias; EntityKnown must be false")
	}
	if f.Diverges() {
		t.Fatal("a system with no opinion (unknown) must never be reported as diverging")
	}
}

// TestCheckReportsEntityUnknownForTheLifecycleProductionPath is the
// documented consequence of a later round's P0-B fix, made a real
// assertion rather than only a package-comment claim: after
// pkg/lifecycle.Orchestrator.RunUnified (the one production entity-
// resolution choke point) resolves a common-vocabulary alias pair
// through pkg/identity ONLY (never writing pkg/moat/entity for a Kind
// identity.Kind models), Check correctly reports EntityKnown=false --
// not a false "these agree" -- because pkg/moat/entity genuinely has
// nothing to compare. This models exactly what lifecycle.go's
// resolveCanonicalEntity does for the identity-resolved case, without
// pulling in the full lifecycle/canonical/policy machinery just to
// prove this one property.
func TestCheckReportsEntityUnknownForTheLifecycleProductionPath(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry() // never written -- models the post-fix production path exactly

	imo := Alias{Kind: "IMO", Value: "5551234"}
	cs := Alias{Kind: "CALLSIGN", Value: "ZZ-5551234"}
	if _, err := idR.Merge("lifecycle", "port-monitor",
		identity.Identifier{Kind: identity.KindIMO, Value: imo.Value},
		identity.Identifier{Kind: identity.KindCallsign, Value: cs.Value}, 1,
		"lifecycle.RunUnified: aliases co-occur on one Intent"); err != nil {
		t.Fatalf("identity Merge: %v", err)
	}

	f, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !f.IdentitySame {
		t.Fatal("expected identity (the canonical authority) to consider the pair the same entity")
	}
	if f.EntityKnown {
		t.Fatal("expected EntityKnown=false: pkg/moat/entity was never written for this alias pair, " +
			"per the P0-B fix's own documented consequence -- reporting EntityKnown=true here would be fabricated agreement")
	}
	if f.Diverges() {
		t.Fatal("a system with no opinion (unknown) must never be reported as diverging, even post-P0-B")
	}
}

// TestCheckIsDeterministicAcrossRepeatedCalls proves Check has no
// hidden mutable state that would make a governance report
// non-reproducible.
func TestCheckIsDeterministicAcrossRepeatedCalls(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry()
	imo := Alias{Kind: "IMO", Value: "1234567"}
	cs := Alias{Kind: "CALLSIGN", Value: "ABCD"}
	if _, err := entR.Merge("a", entity.Alias{Kind: imo.Kind, Value: imo.Value},
		entity.Alias{Kind: cs.Kind, Value: cs.Value}, 1); err != nil {
		t.Fatalf("entity Merge: %v", err)
	}

	first, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	second, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if first != second {
		t.Fatalf("Check must be deterministic given the same state and tick: %+v vs %+v", first, second)
	}
}
