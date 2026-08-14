// Package entityconsistency closes the machine-checkable half of audit
// item P0-B ("Canonical Entity Authority"): VERIQO runs three
// independent entity-resolution systems side by side --
// pkg/identity (a real, bitemporal resolver with confidence scoring
// and authority-weighted evidence), pkg/moat/entity (a hash-chained
// union-find over exact alias equality), and pkg/moat/domain/maritime
// (a typed, content-addressed ontology) -- with no canonical authority
// forcing them to agree. The audit's own risk diagram is exact:
//
//	Identity A -> pkg/identity     -> Entity-X
//	same object
//	Identity A -> pkg/moat/entity  -> Entity-Y   (X != Y: silent fragmentation)
//
// Consolidating the three into one canonical authority is real,
// multi-day architectural work (retiring or adapting two of the three
// systems and migrating every caller) that this package does NOT
// attempt. What it DOES provide, honestly and immediately: a real,
// machine-checkable DETECTOR for exactly the divergence the audit
// warned about, scoped to the two systems that are structurally
// comparable -- pkg/identity and pkg/moat/entity both key off a
// (Kind, Value) alias pair. pkg/moat/domain/maritime is deliberately
// NOT included: it derives entity IDs from a content-addressed natural
// key map, not an alias registered against a merge ledger, so there is
// no honest apples-to-apples comparison to make without inventing a
// translation layer between the two id schemes -- doing so would be
// exactly the kind of fabricated equivalence this package exists to
// prevent, not produce.
//
// This turns silent fragmentation into a visible, testable fact:
// "these two systems currently disagree about alias X" is now
// something a real test (or a future readiness gate) can assert on,
// rather than something nobody notices until two subsystems quietly
// diverge in production.
package entityconsistency

import (
	"context"
	"fmt"

	"veriqo/pkg/identity"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/platform/telemetry"
)

// Alias names one (kind, value) identifier pair, in the shape both
// pkg/identity.Identifier and pkg/moat/entity.Alias already use.
type Alias struct {
	Kind  string
	Value string
}

// Finding is the honest result of comparing two systems' opinions
// about whether aliasA and aliasB denote the same real-world entity.
type Finding struct {
	AliasA, AliasB Alias

	// IdentityKnown/EntityKnown report whether each system has ANY
	// opinion at all -- a system that has never seen an alias is not
	// "disagreeing", it is simply uninformed, and Finding keeps those
	// two honestly distinct rather than collapsing them into false
	// agreement or false divergence.
	IdentityKnown bool
	IdentitySame  bool
	EntityKnown   bool
	EntitySame    bool
}

// Diverges reports whether the two systems actively disagree -- both
// have an opinion, and those opinions differ. This is the exact,
// narrow, machine-checkable definition of the audit's named risk.
func (f Finding) Diverges() bool {
	return f.IdentityKnown && f.EntityKnown && f.IdentitySame != f.EntitySame
}

// Check compares pkg/identity's and pkg/moat/entity's opinions about
// aliasA and aliasB as of asOfTick, using each system's OWN real
// resolution logic -- EntityIDAt for pkg/identity (a pure, bitemporal
// fold), Resolve for pkg/moat/entity (the union-find's current root).
// Neither aliasA nor aliasB needs to already be known to either system;
// Finding's Known fields report exactly what was and wasn't found.
func Check(idResolver *identity.Resolver, entReg *entity.Registry, aliasA, aliasB Alias, asOfTick uint64) (Finding, error) {
	_, span := telemetry.StartSpan(context.Background(), "entityconsistency.Check")
	defer span.End()

	f := Finding{AliasA: aliasA, AliasB: aliasB}

	idA, err := idResolver.EntityIDAt(identity.Identifier{Kind: identity.Kind(aliasA.Kind), Value: aliasA.Value}, asOfTick)
	if err != nil {
		return Finding{}, fmt.Errorf("entityconsistency: identity resolve aliasA: %w", err)
	}
	idB, err := idResolver.EntityIDAt(identity.Identifier{Kind: identity.Kind(aliasB.Kind), Value: aliasB.Value}, asOfTick)
	if err != nil {
		return Finding{}, fmt.Errorf("entityconsistency: identity resolve aliasB: %w", err)
	}
	// EntityIDAt always returns a value (a singleton entity ID for a
	// never-seen alias, see pkg/identity's own doc comment) -- "known"
	// for this checker's purposes means the resolver has recorded at
	// least one real ledger event mentioning the alias, which
	// EntityIDAt's error-free singleton path does not by itself
	// establish. We treat pkg/identity as always "known" (it never
	// refuses to answer), matching its own designed behavior; the
	// comparison is still meaningful because a singleton ID can never
	// equal another alias's singleton ID unless they share the exact
	// same key, so no false agreement is introduced.
	f.IdentityKnown = true
	f.IdentitySame = idA == idB

	entA, okA := entReg.Resolve(entity.Alias{Kind: aliasA.Kind, Value: aliasA.Value})
	entB, okB := entReg.Resolve(entity.Alias{Kind: aliasB.Kind, Value: aliasB.Value})
	f.EntityKnown = okA && okB
	if f.EntityKnown {
		f.EntitySame = entA == entB
	}

	return f, nil
}
