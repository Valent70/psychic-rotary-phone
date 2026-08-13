package digitaltwin

import (
	"testing"

	"veriqo/pkg/identity"
	"veriqo/pkg/moat/fusion"
)

func mustResolver(t *testing.T) *identity.Resolver {
	t.Helper()
	r := identity.NewResolver()
	if err := r.RegisterAuthority(identity.Authority{
		SourceID: "port-monitor", Weight: 0.95, AuthoritativeFor: []identity.Kind{identity.KindIMO, identity.KindCallsign},
	}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	return r
}

func TestApplyArbitrationResolvedUsesRealEntityResolution(t *testing.T) {
	resolver := mustResolver(t)
	reg := NewRegistry()
	imo := identity.Identifier{Kind: identity.KindIMO, Value: "7778889"}

	res := fusion.ArbitrationResult{
		Claim: fusion.Claim{Subject: "unused", Predicate: "AIS_STATUS"}, Winner: "OFF", WinnerConfidence: 0.9,
	}
	entity, err := reg.ApplyArbitrationResolved("actor-1", imo, resolver, res, 5)
	if err != nil {
		t.Fatalf("ApplyArbitrationResolved: %v", err)
	}
	if entity == "" {
		t.Fatal("resolved entity must not be empty")
	}
	twin, ok := reg.Get(entity)
	if !ok {
		t.Fatalf("expected a twin to exist for the resolved entity %q", entity)
	}
	if twin.Attributes["AIS_STATUS"].Value != "OFF" {
		t.Fatalf("unexpected attribute state: %+v", twin.Attributes["AIS_STATUS"])
	}
}

// TestApplyArbitrationResolvedConsolidatesDifferentIdentifierKinds is
// the property that actually matters for P0-06: two updates that name
// the SAME vessel via two DIFFERENT identifier kinds (IMO from one
// source, callsign from another) must land on the SAME twin once the
// resolver has merged them -- exactly the consolidation a bare-string
// EntityID could never provide, since the caller would have had to
// already agree on one canonical string out of band.
func TestApplyArbitrationResolvedConsolidatesDifferentIdentifierKinds(t *testing.T) {
	resolver := mustResolver(t)
	reg := NewRegistry()
	imo := identity.Identifier{Kind: identity.KindIMO, Value: "7778889"}
	callsign := identity.Identifier{Kind: identity.KindCallsign, Value: "CS-7778889"}

	if _, err := resolver.Merge("resolution-agent", "port-monitor", imo, callsign, 5,
		"cross-referenced on visual boarding"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	entityByIMO, err := reg.ApplyArbitrationResolved("actor-1", imo, resolver,
		fusion.ArbitrationResult{Claim: fusion.Claim{Predicate: "AIS_STATUS"}, Winner: "OFF", WinnerConfidence: 0.9}, 10)
	if err != nil {
		t.Fatalf("ApplyArbitrationResolved (imo): %v", err)
	}
	entityByCallsign, err := reg.ApplyArbitrationResolved("actor-1", callsign, resolver,
		fusion.ArbitrationResult{Claim: fusion.Claim{Predicate: "SAR_POSITION_MATCH"}, Winner: "MISMATCH", WinnerConfidence: 0.8}, 10)
	if err != nil {
		t.Fatalf("ApplyArbitrationResolved (callsign): %v", err)
	}
	if entityByIMO != entityByCallsign {
		t.Fatalf("a merged IMO and callsign must resolve to the SAME entity, got %q vs %q", entityByIMO, entityByCallsign)
	}

	twin, ok := reg.Get(entityByIMO)
	if !ok {
		t.Fatal("expected a twin to exist")
	}
	if twin.Attributes["AIS_STATUS"].Value != "OFF" || twin.Attributes["SAR_POSITION_MATCH"].Value != "MISMATCH" {
		t.Fatalf("both updates must have landed on the ONE consolidated twin: %+v", twin.Attributes)
	}
}

// TestApplyArbitrationResolvedForksAcrossTickWhenIdentityChanges
// documents, with an explicit test rather than a surprise, the one
// real subtlety EntityIDAt introduces: the SAME identifier can
// legitimately resolve to a DIFFERENT entity id before vs. after a
// merge takes effect, because resolution is bitemporal (folded only
// over ledger events known as of that tick). This is deterministic and
// reproducible on replay, not a bug -- but a caller applying updates
// out of chronological order must be aware a twin's identity can fork
// across the merge boundary.
func TestApplyArbitrationResolvedForksAcrossTickWhenIdentityChanges(t *testing.T) {
	resolver := mustResolver(t)
	reg := NewRegistry()
	imo := identity.Identifier{Kind: identity.KindIMO, Value: "7778889"}
	callsign := identity.Identifier{Kind: identity.KindCallsign, Value: "CS-7778889"}

	beforeMerge, err := reg.ApplyArbitrationResolved("actor-1", imo, resolver,
		fusion.ArbitrationResult{Claim: fusion.Claim{Predicate: "AIS_STATUS"}, Winner: "ON", WinnerConfidence: 0.9}, 1)
	if err != nil {
		t.Fatalf("ApplyArbitrationResolved (before merge): %v", err)
	}

	if _, err := resolver.Merge("resolution-agent", "port-monitor", imo, callsign, 5, "cross-referenced"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	afterMerge, err := reg.ApplyArbitrationResolved("actor-1", imo, resolver,
		fusion.ArbitrationResult{Claim: fusion.Claim{Predicate: "AIS_STATUS"}, Winner: "OFF", WinnerConfidence: 0.9}, 10)
	if err != nil {
		t.Fatalf("ApplyArbitrationResolved (after merge): %v", err)
	}

	if beforeMerge == afterMerge {
		t.Fatal("resolving the same identifier before and after a merge must produce DIFFERENT entity ids " +
			"(pre-merge = singleton, post-merge = the merged entity) -- this test's premise is now stale " +
			"relative to identity.EntityIDAt's actual behaviour")
	}

	// Deterministic replay: re-resolving at the SAME tick against the
	// SAME ledger state must reproduce the SAME entity id every time.
	again, err := resolver.EntityIDAt(imo, 1)
	if err != nil {
		t.Fatalf("EntityIDAt: %v", err)
	}
	if EntityID(again) != beforeMerge {
		t.Fatalf("re-resolving at tick 1 must reproduce the pre-merge entity id deterministically: got %q, want %q",
			again, beforeMerge)
	}
}
