// Real entity-resolution wiring (audit item P0-06): every existing
// digitaltwin.Registry method accepts EntityID as a bare, caller-
// supplied string with zero validation -- the audit's own words,
// "EntityID is a bare string, not resolved through real entity
// resolution." This file adds an additive path that derives the
// entity key from pkg/identity's real resolver instead, without
// touching the existing UpdateRecord schema, hash-chain format, or any
// of the pre-existing call sites, which all keep working exactly as
// before.
//
// Honest scope: this closes the specific gap named above -- a twin's
// key can now come from real, bitemporal entity resolution rather than
// an unverified string a caller happened to pass in. It does NOT
// attempt the larger P0-04 (unified Evidence system) refactor; nothing
// here changes EntityID's type, digitaltwin's Attributes/Risk/Causal
// schema, or replay semantics, which is exactly why this slice is
// separable from that larger one.
package digitaltwin

import (
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/fusion"
)

// ApplyArbitrationResolved is ApplyArbitration, except the entity key
// is derived from resolver.EntityIDAt(id, tick) -- real, bitemporal
// entity resolution -- rather than accepted as a bare caller-supplied
// EntityID. It returns the resolved EntityID so the caller can look
// the twin back up.
//
// EntityIDAt is a pure fold over resolver's ledger prefix at tick, so
// calling it twice with the same ledger state and tick is
// deterministic -- replay safety is preserved because UpdateRecord
// stores the ALREADY-RESOLVED entity at write time; Rebuild/ReplayAll
// never re-invoke resolution. The one real, non-bug subtlety: the SAME
// identifier can legitimately resolve to a DIFFERENT EntityID at a
// later tick (e.g. after an UNMERGE) -- see
// TestApplyArbitrationResolvedForksAcrossTickWhenIdentityChanges for
// what that means for a twin's history.
func (r *Registry) ApplyArbitrationResolved(actorID string, id identity.Identifier, resolver *identity.Resolver,
	res fusion.ArbitrationResult, tick uint64) (EntityID, error) {
	entity, err := resolver.EntityIDAt(id, tick)
	if err != nil {
		return "", err
	}
	if err := r.ApplyArbitration(actorID, EntityID(entity), res, tick); err != nil {
		return "", err
	}
	return EntityID(entity), nil
}

// ApplyDecisionResolved is ApplyDecision with the same real-entity-
// resolution derivation as ApplyArbitrationResolved.
func (r *Registry) ApplyDecisionResolved(actorID string, id identity.Identifier, resolver *identity.Resolver,
	d decision.Decision, tick uint64) (EntityID, error) {
	entity, err := resolver.EntityIDAt(id, tick)
	if err != nil {
		return "", err
	}
	if err := r.ApplyDecision(actorID, EntityID(entity), d, tick); err != nil {
		return "", err
	}
	return EntityID(entity), nil
}
