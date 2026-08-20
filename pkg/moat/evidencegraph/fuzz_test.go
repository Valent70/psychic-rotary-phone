package evidencegraph

import "testing"

// FuzzDependencyLedger (PHASE 38) drives the dependency ledger with
// arbitrary records. Invariants: Declare never panics; any ledger that
// Declare produced must VerifyChain cleanly; and Rebuild from that
// ledger must reproduce the same root hash.
func FuzzDependencyLedger(f *testing.F) {
	f.Add("a", "b", "SOURCE", 1.0, uint64(0), uint64(0))
	f.Add("", "b", "PRODUCER", -1.0, uint64(5), uint64(1))
	f.Fuzz(func(t *testing.T, node, up, kind string, conf float64, from, to uint64) {
		g := NewGraph()
		rec := DependencyRecord{
			NodeID: NodeID(node), UpstreamID: NodeID(up), Kind: DependencyKind(kind),
			Confidence: conf, ValidFrom: from, ValidTo: to,
		}
		if _, err := g.Declare(rec); err != nil {
			return
		}
		if err := g.VerifyChain(); err != nil {
			t.Fatalf("a ledger this package produced fails its own verification: %v", err)
		}
		rebuilt, err := Rebuild(g.ReplayAll())
		if err != nil {
			t.Fatalf("Rebuild rejected a ledger this package produced: %v", err)
		}
		if rebuilt.RootHash() != g.RootHash() {
			t.Fatal("Rebuild produced a different root hash")
		}
	})
}

// FuzzEffectiveWeightBounds asserts the discount can never inflate a
// weight or produce a negative one, for any graph shape.
func FuzzEffectiveWeightBounds(f *testing.F) {
	f.Add("a", "b", "u", 0.5, 0.9)
	f.Fuzz(func(t *testing.T, n1, n2, up string, w1, w2 float64) {
		if w1 < 0 || w1 > 1 || w2 < 0 || w2 > 1 || n1 == "" || n2 == "" || up == "" {
			return
		}
		g := NewGraph()
		for _, n := range []string{n1, n2} {
			if n == up {
				return
			}
			_, _ = g.Declare(DependencyRecord{NodeID: NodeID(n), UpstreamID: NodeID(up),
				Kind: DependsOnSource, Confidence: 1})
		}
		nodes := []NodeID{NodeID(n1), NodeID(n2)}
		base := map[NodeID]float64{NodeID(n1): w1, NodeID(n2): w2}
		for n, eff := range g.EffectiveWeightAt(nodes, base, 0) {
			if eff < 0 || eff > base[n]+1e-12 {
				t.Fatalf("effective weight out of bounds for %s: %.9f (base %.9f)", n, eff, base[n])
			}
		}
	})
}
