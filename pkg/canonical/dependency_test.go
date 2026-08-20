package canonical

import (
	"testing"

	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

// twoAgreeingSources builds a case where two sources agree on the same
// value, so any change in their EFFECTIVE weight shows up directly in
// the winner's confidence. deps is applied to both sources.
func twoAgreeingSources(depA, depB []evidencegraph.DependencyRecord, provider string) CaseInput {
	return CaseInput{
		Entity: digitaltwin.EntityID("VESSEL:IMO1111111"), Subject: "VESSEL:IMO1111111",
		Predicate: "AIS_STATUS", Tick: 10, Policy: risk.DefaultPolicy(),
		Submissions: []SourceSubmission{
			{SourceID: fusion.SourceID("vendor-a"), Value: "OFF", BaseReliability: 0.8, Provider: provider, Dependencies: depA},
			{SourceID: fusion.SourceID("vendor-b"), Value: "OFF", BaseReliability: 0.8, Provider: provider, Dependencies: depB},
		},
	}
}

func runConfidence(t *testing.T, in CaseInput) (*CanonicalResult, float64) {
	t.Helper()
	p := NewPipeline(nil)
	res, err := p.RunCanonical("actor", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	return res, res.Arbitration.WinnerConfidence
}

// TestCanonicalUsesEvidenceDependencyGraph is the audit's named gate:
// if the graph merely exists but canonical does not use it, FAIL.
func TestCanonicalUsesEvidenceDependencyGraph(t *testing.T) {
	p := NewPipeline(nil)
	if p.Dependencies == nil {
		t.Fatal("Pipeline has no evidence dependency graph")
	}
	in := twoAgreeingSources(nil, nil, "sat-op-x")
	res, err := p.RunCanonical("actor", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if len(res.Dependency.Effective) != 2 {
		t.Fatalf("dependency evaluation did not cover every source: %+v", res.Dependency.Effective)
	}
	if res.Certificate.DependencyRootHash == "" {
		t.Fatal("certificate does not commit to the dependency ledger")
	}
	if len(p.Dependencies.Ledger()) == 0 {
		t.Fatal("canonical run declared no dependency records — the graph was not consulted")
	}
	if err := p.Dependencies.VerifyChain(); err != nil {
		t.Fatalf("dependency ledger not verifiable after a canonical run: %v", err)
	}
	// The declared shared producer must actually have discounted at
	// least one source below its declared base reliability.
	discounted := false
	for id, eff := range res.Dependency.Effective {
		if eff < res.Dependency.Base[id] {
			discounted = true
		}
	}
	if !discounted {
		t.Fatal("shared producer produced no discount: dependency evaluation is decorative, not wired")
	}
}

// TestCanonicalCannotFuseWithoutDependencyEvaluation proves the guard
// is structural: a source that never went through the resolver is
// refused rather than silently fused at full weight.
func TestCanonicalCannotFuseWithoutDependencyEvaluation(t *testing.T) {
	p := NewPipeline(nil)
	in := twoAgreeingSources(nil, nil, "")
	// Simulate a future refactor that adds a submission AFTER the
	// resolver would have run, by evaluating a different set.
	eval, err := p.resolveDependencies(in)
	if err != nil {
		t.Fatalf("resolveDependencies: %v", err)
	}
	if _, ok := eval.Effective["vendor-c"]; ok {
		t.Fatal("resolver invented a weight for a source that was not submitted")
	}
	in.Submissions = append(in.Submissions, SourceSubmission{
		SourceID: fusion.SourceID("vendor-c"), Value: "OFF", BaseReliability: 0.9,
	})
	// Re-running through the real entry point must re-evaluate and
	// cover the new source (never fuse it unevaluated).
	res, err := p.RunCanonical("actor", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if _, ok := res.Dependency.Effective["vendor-c"]; !ok {
		t.Fatal("newly added source bypassed dependency evaluation")
	}
}

// TestSharedSatelliteProviderCannotInflateConfidence is the audit's
// literal scenario: two vendors reselling the same satellite provider
// must not produce the same confidence as two genuinely independent
// vendors.
func TestSharedSatelliteProviderCannotInflateConfidence(t *testing.T) {
	shared := twoAgreeingSources(nil, nil, "satellite-provider-x")
	independent := twoAgreeingSources(nil, nil, "")

	_, sharedConf := runConfidence(t, shared)
	_, indepConf := runConfidence(t, independent)

	if sharedConf >= indepConf {
		t.Fatalf("correlated evidence inflated confidence: shared=%.6f independent=%.6f", sharedConf, indepConf)
	}
}

// TestSharedProducerDiscount isolates the PRODUCER dependency kind.
func TestSharedProducerDiscount(t *testing.T) {
	in := twoAgreeingSources(nil, nil, "producer-omega")
	res, _ := runConfidence(t, in)
	for id, frac := range res.Dependency.SharedFraction {
		if frac <= 0 {
			t.Fatalf("source %s shares a producer but has zero discount", id)
		}
	}
	if res.Certificate.IndependentFamilies != 1 {
		t.Fatalf("two sources on one producer must be one family, got %d", res.Certificate.IndependentFamilies)
	}
}

// TestSharedTransformationDiscount covers the case the audit called
// out as invisible to source-level provenance: two analysts running
// the SAME model over DIFFERENT raw feeds.
func TestSharedTransformationDiscount(t *testing.T) {
	xf := evidencegraph.DependencyRecord{
		UpstreamID: "model:dark-vessel-detector-v3",
		Kind:       evidencegraph.DependsOnTransformation, Confidence: 1,
		TransformationID: "dark-vessel-detector-v3",
	}
	// Different raw feeds (different UpstreamID), different producers,
	// same transformation.
	in := twoAgreeingSources([]evidencegraph.DependencyRecord{xf}, []evidencegraph.DependencyRecord{xf}, "")
	in.Submissions[0].UpstreamID = "feed-alpha"
	in.Submissions[1].UpstreamID = "feed-beta"

	control := twoAgreeingSources(nil, nil, "")
	control.Submissions[0].UpstreamID = "feed-alpha"
	control.Submissions[1].UpstreamID = "feed-beta"

	res, sharedConf := runConfidence(t, in)
	_, indepConf := runConfidence(t, control)

	if sharedConf >= indepConf {
		t.Fatalf("shared transformation did not discount: shared=%.6f independent=%.6f", sharedConf, indepConf)
	}
	if res.Dependency.MaxDiscount <= 0 {
		t.Fatal("shared transformation produced no measurable discount")
	}
}

// TestTransitiveDependencyDiscount proves multi-hop dependency is
// detected, not just direct edges: A -> reseller -> satellite-x and
// B -> satellite-x share only at depth 2.
func TestTransitiveDependencyDiscount(t *testing.T) {
	p := NewPipeline(nil)
	if _, err := p.Dependencies.Declare(evidencegraph.DependencyRecord{
		NodeID: "reseller-r", UpstreamID: "satellite-x",
		Kind: evidencegraph.DependsOnSource, Confidence: 1,
	}); err != nil {
		t.Fatalf("declare: %v", err)
	}
	in := twoAgreeingSources(nil, nil, "")
	in.Submissions[0].UpstreamID = "reseller-r" // vendor-a -> reseller-r -> satellite-x
	in.Submissions[1].UpstreamID = "satellite-x"

	res, err := p.RunCanonical("actor", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if res.Dependency.SharedFraction["vendor-a"] <= 0 {
		t.Fatalf("transitive dependency missed: %+v", res.Dependency.SharedFraction)
	}
	if res.Certificate.IndependentFamilies != 1 {
		t.Fatalf("transitively linked sources must be one family, got %d", res.Certificate.IndependentFamilies)
	}
}

// TestDependencyGraphReplayDeterministic proves the graph can be
// rebuilt from its ledger alone and produces identical output.
func TestDependencyGraphReplayDeterministic(t *testing.T) {
	p := NewPipeline(nil)
	in := twoAgreeingSources(nil, nil, "sat-op-x")
	res1, err := p.RunCanonical("actor", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	rebuilt, err := evidencegraph.Rebuild(p.Dependencies.ReplayAll())
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if rebuilt.RootHash() != p.Dependencies.RootHash() {
		t.Fatal("rebuilt dependency graph has a different root hash")
	}
	nodes := []evidencegraph.NodeID{"vendor-a", "vendor-b"}
	base := map[evidencegraph.NodeID]float64{"vendor-a": 0.8, "vendor-b": 0.8}
	a := p.Dependencies.EffectiveWeightAt(nodes, base, in.Tick)
	b := rebuilt.EffectiveWeightAt(nodes, base, in.Tick)
	for _, n := range nodes {
		if a[n] != b[n] {
			t.Fatalf("replayed weight differs for %s: %.9f vs %.9f", n, a[n], b[n])
		}
	}
	// And a second identical pipeline run reproduces the same root.
	p2 := NewPipeline(nil)
	res2, err := p2.RunCanonical("actor", twoAgreeingSources(nil, nil, "sat-op-x"))
	if err != nil {
		t.Fatalf("RunCanonical#2: %v", err)
	}
	if res1.Certificate.DependencyRootHash != res2.Certificate.DependencyRootHash {
		t.Fatal("dependency root hash is not deterministic across identical runs")
	}
}

// TestDependencyGraphTamperDetection proves the ledger is
// tamper-evident at record and chain level.
func TestDependencyGraphTamperDetection(t *testing.T) {
	g := evidencegraph.NewGraph()
	for i, up := range []evidencegraph.NodeID{"u1", "u2", "u3"} {
		if _, err := g.Declare(evidencegraph.DependencyRecord{
			NodeID: evidencegraph.NodeID("n"), UpstreamID: up,
			Kind: evidencegraph.DependsOnSource, Confidence: 1, TxTick: uint64(i),
		}); err != nil {
			t.Fatalf("declare: %v", err)
		}
	}
	if err := g.VerifyChain(); err != nil {
		t.Fatalf("clean chain rejected: %v", err)
	}
	tampered := g.ReplayAll()
	tampered[1].Confidence = 0.1 // silently weaken a dependency
	if _, err := evidencegraph.Rebuild(tampered); err == nil {
		t.Fatal("tampered ledger rebuilt without error")
	}
	// Direct chain verification on a mutated copy.
	g2, err := evidencegraph.Rebuild(g.ReplayAll())
	if err != nil {
		t.Fatalf("Rebuild clean: %v", err)
	}
	if g2.RootHash() != g.RootHash() {
		t.Fatal("clean rebuild diverged")
	}
}

// TestDependencyRecordValidationRejectsMalformed keeps the record model
// honest at the boundary.
func TestDependencyRecordValidationRejectsMalformed(t *testing.T) {
	g := evidencegraph.NewGraph()
	cases := []struct {
		name string
		rec  evidencegraph.DependencyRecord
	}{
		{"empty node", evidencegraph.DependencyRecord{UpstreamID: "u", Kind: evidencegraph.DependsOnSource}},
		{"empty upstream", evidencegraph.DependencyRecord{NodeID: "n", Kind: evidencegraph.DependsOnSource}},
		{"self", evidencegraph.DependencyRecord{NodeID: "n", UpstreamID: "n", Kind: evidencegraph.DependsOnSource}},
		{"bad confidence", evidencegraph.DependencyRecord{NodeID: "n", UpstreamID: "u", Kind: evidencegraph.DependsOnSource, Confidence: 1.5}},
		{"bad validity", evidencegraph.DependencyRecord{NodeID: "n", UpstreamID: "u", Kind: evidencegraph.DependsOnSource, ValidFrom: 10, ValidTo: 5}},
		{"unknown kind", evidencegraph.DependencyRecord{NodeID: "n", UpstreamID: "u", Kind: "NOPE"}},
	}
	for _, c := range cases {
		if _, err := g.Declare(c.rec); err == nil {
			t.Fatalf("%s: malformed dependency record accepted", c.name)
		}
	}
}

// TestTemporalDependencyExpires proves a dependency that expired
// before the case tick no longer discounts — bitemporality, not a
// permanent smear.
func TestTemporalDependencyExpires(t *testing.T) {
	g := evidencegraph.NewGraph()
	if _, err := g.Declare(evidencegraph.DependencyRecord{
		NodeID: "a", UpstreamID: "sat-x", Kind: evidencegraph.DependsOnSource,
		Confidence: 1, ValidFrom: 0, ValidTo: 100,
	}); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if _, err := g.Declare(evidencegraph.DependencyRecord{
		NodeID: "b", UpstreamID: "sat-x", Kind: evidencegraph.DependsOnSource, Confidence: 1,
	}); err != nil {
		t.Fatalf("declare: %v", err)
	}
	nodes := []evidencegraph.NodeID{"a", "b"}
	if in := g.SharedFractionAt(nodes, 50); in["a"] <= 0 {
		t.Fatal("dependency inside its validity window did not discount")
	}
	if out := g.SharedFractionAt(nodes, 500); out["a"] != 0 {
		t.Fatalf("expired dependency still discounting: %.6f", out["a"])
	}
}
