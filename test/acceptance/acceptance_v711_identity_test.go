package acceptance

import (
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/evidencegraph"
	tstate "veriqo/pkg/trust/state"
)

func idResolver(t *testing.T) *identity.Resolver {
	t.Helper()
	r := identity.NewResolver()
	if err := r.RegisterAuthority(identity.Authority{SourceID: "flag-registry", Weight: 1,
		AuthoritativeFor: []identity.Kind{identity.KindIMO, identity.KindRegistryID}}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	if err := r.RegisterAuthority(identity.Authority{SourceID: "aggregator", Weight: 0.4}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	return r
}

func idIMO(v string) identity.Identifier {
	return identity.Identifier{Kind: identity.KindIMO, Value: v}
}
func idCall(v string) identity.Identifier {
	return identity.Identifier{Kind: identity.KindCallsign, Value: v}
}

func TestAcceptance81_MergeIsOrderInvariant(t *testing.T) {
	a, b := idResolver(t), idResolver(t)
	if _, err := a.Merge("op", "flag-registry", idIMO("1"), idCall("X"), 1, "r"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, err := b.Merge("op", "flag-registry", idCall("X"), idIMO("1"), 1, "r"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	x, _ := a.EntityIDAt(idIMO("1"), 5)
	y, _ := b.EntityIDAt(idCall("X"), 5)
	if x != y {
		t.Fatalf("entity ID not merge-order invariant: %s vs %s", x, y)
	}
}

func TestAcceptance82_UnmergePreservesHistory(t *testing.T) {
	r := idResolver(t)
	if _, err := r.Merge("op", "flag-registry", idIMO("2"), idCall("Y"), 10, "r"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, err := r.Unmerge("op", "flag-registry", idIMO("2"), idCall("Y"), 100, "wrong"); err != nil {
		t.Fatalf("Unmerge: %v", err)
	}
	past, err := r.SameEntityAt(idIMO("2"), idCall("Y"), 50)
	if err != nil || !past {
		t.Fatal("unmerge rewrote history")
	}
	now, err := r.SameEntityAt(idIMO("2"), idCall("Y"), 200)
	if err != nil || now {
		t.Fatal("unmerge did not take effect")
	}
}

func TestAcceptance83_IdentityConflictSurfaced(t *testing.T) {
	r := idResolver(t)
	if _, err := r.Assert("op", "flag-registry", idCall("Z"), idIMO("3"), 1, "A"); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := r.Assert("op", "flag-registry", idCall("Z"), idIMO("4"), 1, "B"); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := r.Resolve(idCall("Z"), 5, 0); err == nil {
		t.Fatal("conflicting identity assertions resolved silently")
	}
}

func TestAcceptance84_LowAuthorityCannotReachHighConfidence(t *testing.T) {
	r := idResolver(t)
	if _, err := r.Assert("op", "aggregator",
		identity.Identifier{Kind: identity.KindName, Value: "SEA STAR"}, idIMO("5"), 1, "name match"); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := r.ResolveWithThreshold(identity.Identifier{Kind: identity.KindName, Value: "SEA STAR"}, 5, 0.9); err == nil {
		t.Fatal("weak identity evidence passed a high threshold")
	}
}

func TestAcceptance85_IdentityLedgerRebuildDeterministic(t *testing.T) {
	r := idResolver(t)
	if _, err := r.Merge("op", "flag-registry", idIMO("6"), idCall("W"), 1, "r"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	rebuilt, err := identity.Rebuild(r.Ledger(), []identity.Authority{{SourceID: "flag-registry", Weight: 1}})
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	a, _ := r.EntityIDAt(idIMO("6"), 10)
	b, _ := rebuilt.EntityIDAt(idIMO("6"), 10)
	if a != b {
		t.Fatal("rebuilt identity resolver diverged")
	}
}

func TestAcceptance86_IdentityCorrectionDoesNotLeakBackwards(t *testing.T) {
	r := idResolver(t)
	if _, err := r.Correct("op", "flag-registry", idIMO("70"), idIMO("7"), 10, "typo"); err != nil {
		t.Fatalf("Correct: %v", err)
	}
	before, _ := r.EntityIDAt(idIMO("70"), 5)
	after, _ := r.EntityIDAt(idIMO("70"), 20)
	if before == after {
		t.Fatal("correction leaked backwards in time")
	}
}

func TestAcceptance87_TemporalDependencyExpiry(t *testing.T) {
	g := evidencegraph.NewGraph()
	if _, err := g.Declare(evidencegraph.DependencyRecord{NodeID: "a", UpstreamID: "sat",
		Kind: evidencegraph.DependsOnSource, Confidence: 1, ValidTo: 100}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if _, err := g.Declare(evidencegraph.DependencyRecord{NodeID: "b", UpstreamID: "sat",
		Kind: evidencegraph.DependsOnSource, Confidence: 1}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	nodes := []evidencegraph.NodeID{"a", "b"}
	if g.SharedFractionAt(nodes, 50)["a"] <= 0 {
		t.Fatal("valid dependency did not discount")
	}
	if g.SharedFractionAt(nodes, 500)["a"] != 0 {
		t.Fatal("expired dependency still discounting")
	}
}

func TestAcceptance88_EvidenceCorrectionSupersedes(t *testing.T) {
	orig := evOntology("a", "OFF", "", 1)
	corr, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeCorrection, Subject: "VESSEL:IMO9000001", Predicate: "AIS_STATUS",
		Object: "ON", Source: "a", ObservedAt: 2, Confidence: 1, Supersedes: orig.EvidenceID})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, e := range ontology.ApplyCorrections([]ontology.Evidence{orig, corr}) {
		if e.EvidenceID == orig.EvidenceID {
			t.Fatal("superseded evidence survived")
		}
	}
}

func TestAcceptance89_TrustHistoricalStatePreserved(t *testing.T) {
	e := tstate.NewEngine(1000, 0.5)
	if _, err := e.Assess("acme", "op", "p", "clean", 0.9, []string{"ev"}, 10, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if _, err := e.Revoke("acme", "op", "sanctioned", []string{"ofac"}, 100); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if e.StateAt("acme", 20).Revoked {
		t.Fatal("later revocation rewrote historical trust state")
	}
}

func TestAcceptance90_TrustDecaysOverTime(t *testing.T) {
	e := tstate.NewEngine(50, 0.5)
	if _, err := e.Assess("acme", "op", "p", "clean", 0.95, []string{"ev"}, 0, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if e.StateAt("acme", 500).EffectiveScore >= e.StateAt("acme", 0).EffectiveScore {
		t.Fatal("trust did not decay")
	}
}
