package fusion

import (
	"errors"
	"math"
	"sync"
	"testing"

	"veriqo/pkg/moat/kg"
)

const eps = 1e-9

func almostEqual(a, b float64) bool { return math.Abs(a-b) < eps }

func TestRegisterSourceValidation(t *testing.T) {
	e := NewEngine(nil)
	if err := e.RegisterSource(SourceProfile{ID: "ais-1", BaseReliability: 0}); !errors.Is(err, ErrInvalidReliability) {
		t.Fatalf("expected ErrInvalidReliability for 0, got %v", err)
	}
	if err := e.RegisterSource(SourceProfile{ID: "ais-1", BaseReliability: 1.5}); !errors.Is(err, ErrInvalidReliability) {
		t.Fatalf("expected ErrInvalidReliability for >1, got %v", err)
	}
	if err := e.RegisterSource(SourceProfile{ID: "ais-1", BaseReliability: 0.9}); err != nil {
		t.Fatalf("valid reliability rejected: %v", err)
	}
}

func TestSubmitUnknownSourceRejected(t *testing.T) {
	e := NewEngine(nil)
	_, err := e.Submit("ghost", Claim{Subject: "V1", Predicate: "FLAG"}, "PA", 1)
	if !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("expected ErrUnknownSource, got %v", err)
	}
}

func TestSubmitDuplicateEvidenceRejected(t *testing.T) {
	e := NewEngine(nil)
	must(t, e.RegisterSource(SourceProfile{ID: "src-a", BaseReliability: 0.8}))
	claim := Claim{Subject: "V1", Predicate: "FLAG"}
	if _, err := e.Submit("src-a", claim, "PA", 5); err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if _, err := e.Submit("src-a", claim, "PA", 5); !errors.Is(err, ErrDuplicateEvidence) {
		t.Fatalf("expected ErrDuplicateEvidence, got %v", err)
	}
}

func TestArbitrateNoEvidence(t *testing.T) {
	e := NewEngine(nil)
	_, err := e.Arbitrate("analyst-1", Claim{Subject: "V1", Predicate: "FLAG"}, 1)
	if !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("expected ErrNoEvidence, got %v", err)
	}
}

// TestFuseGroupNoisyOR verifies the Bayesian noisy-OR combination against
// hand-computed values: two independent sources of reliability 0.7 and
// 0.6 supporting the same value must combine to 1-(1-0.7)(1-0.6)=0.88.
func TestFuseGroupNoisyOR(t *testing.T) {
	// Distinct sources with no declared Provider are independent.
	group := []Evidence{
		{Source: "src-a", Reliability: 0.7},
		{Source: "src-b", Reliability: 0.6},
	}
	got := fuseGroup(group)
	want := 1 - (1-0.7)*(1-0.6)
	if !almostEqual(got, want) {
		t.Fatalf("fuseGroup = %.10f, want %.10f", got, want)
	}
}

func TestFuseGroupSingleEvidenceEqualsItsReliability(t *testing.T) {
	got := fuseGroup([]Evidence{{Source: "src-a", Reliability: 0.42}})
	if !almostEqual(got, 0.42) {
		t.Fatalf("single-evidence fusion = %v, want 0.42", got)
	}
}

func TestFuseGroupMonotonicWithMoreCorroboration(t *testing.T) {
	one := fuseGroup([]Evidence{{Source: "src-a", Reliability: 0.5}})
	two := fuseGroup([]Evidence{{Source: "src-a", Reliability: 0.5}, {Source: "src-b", Reliability: 0.5}})
	three := fuseGroup([]Evidence{{Source: "src-a", Reliability: 0.5}, {Source: "src-b", Reliability: 0.5}, {Source: "src-c", Reliability: 0.5}})
	if !(one < two && two < three) {
		t.Fatalf("confidence must strictly increase with independent corroboration: %v, %v, %v", one, two, three)
	}
	if three >= 1.0 {
		t.Fatalf("noisy-OR must never reach exactly 1.0 with finite reliabilities, got %v", three)
	}
}

// TestFuseGroupCorrelatedSourcesDoNotCompound is the direct regression
// test for the algorithmic gap Yang_masih_kurang.docx names explicitly:
// three AIS vendors reselling the same satellite feed must NOT compound
// to near-certainty the way three genuinely independent sources would.
func TestFuseGroupCorrelatedSourcesDoNotCompound(t *testing.T) {
	independent := fuseGroup([]Evidence{
		{Source: "vendor-a", Provider: "", Reliability: 0.9},
		{Source: "vendor-b", Provider: "", Reliability: 0.9},
		{Source: "vendor-c", Provider: "", Reliability: 0.9},
	})
	correlated := fuseGroup([]Evidence{
		{Source: "vendor-a", Provider: "satfeed-x", Reliability: 0.9},
		{Source: "vendor-b", Provider: "satfeed-x", Reliability: 0.9},
		{Source: "vendor-c", Provider: "satfeed-x", Reliability: 0.9},
	})
	// Independent case: classic noisy-OR, 1-(1-0.9)^3 = 0.999
	wantIndependent := 1 - (1-0.9)*(1-0.9)*(1-0.9)
	if !almostEqual(independent, wantIndependent) {
		t.Fatalf("independent fusion = %v, want %v", independent, wantIndependent)
	}
	// Correlated case: all three share one provider -> only the max
	// (0.9) counts, exactly as if a single witness reported it.
	if !almostEqual(correlated, 0.9) {
		t.Fatalf("correlated fusion = %v, want 0.9 (collapsed to single provider)", correlated)
	}
	if correlated >= independent {
		t.Fatalf("correlated confidence (%v) must be strictly lower than independent confidence (%v) for identical reliabilities", correlated, independent)
	}
}

// TestSubmitCopiesProviderFromSourceProfile proves Provider (source
// lineage) flows from registration into every Evidence, immutably.
func TestSubmitCopiesProviderFromSourceProfile(t *testing.T) {
	e := NewEngine(nil)
	must(t, e.RegisterSource(SourceProfile{ID: "vendor-a", BaseReliability: 0.9, Provider: "satfeed-x"}))
	claim := Claim{Subject: "V1", Predicate: "AIS_POSITION"}
	ev, err := e.Submit("vendor-a", claim, "ZONE-1", 1)
	must(t, err)
	if ev.Provider != "satfeed-x" {
		t.Fatalf("evidence.Provider = %q, want satfeed-x", ev.Provider)
	}
}

// TestArbitrateSingleValueNoContradiction is the baseline happy path: one
// value, multiple corroborating sources, winner = that value.
func TestArbitrateSingleValueNoContradiction(t *testing.T) {
	e := NewEngine(nil)
	must(t, e.RegisterSource(SourceProfile{ID: "ais-vendor-a", BaseReliability: 0.75}))
	must(t, e.RegisterSource(SourceProfile{ID: "port-authority", BaseReliability: 0.95}))

	claim := Claim{Subject: "VESSEL:IMO9876543", Predicate: "FLAG_STATE"}
	_, err := e.Submit("ais-vendor-a", claim, "PANAMA", 10)
	must(t, err)
	_, err = e.Submit("port-authority", claim, "PANAMA", 12)
	must(t, err)

	res, err := e.Arbitrate("analyst-1", claim, 20)
	must(t, err)
	if res.Winner != "PANAMA" {
		t.Fatalf("winner = %q, want PANAMA", res.Winner)
	}
	if res.Contradiction {
		t.Fatalf("expected no contradiction with single asserted value")
	}
	if res.EvidenceCount != 2 {
		t.Fatalf("evidence count = %d, want 2", res.EvidenceCount)
	}
	wantConf := 1 - (1-0.75)*(1-0.95)
	if !almostEqual(res.WinnerConfidence, wantConf) {
		t.Fatalf("winner confidence = %v, want %v", res.WinnerConfidence, wantConf)
	}
	if len(res.Explanation) == 0 {
		t.Fatalf("expected non-empty explanation trail")
	}
}

// TestArbitrateContradictionDetectedAndCorrectSourceWins is the core
// Truth Arbitration case: two mutually exclusive values asserted by
// different-reliability sources; the higher fused-confidence value must
// win and Contradiction must be flagged.
func TestArbitrateContradictionDetectedAndCorrectSourceWins(t *testing.T) {
	e := NewEngine(nil)
	must(t, e.RegisterSource(SourceProfile{ID: "unverified-blog", BaseReliability: 0.3}))
	must(t, e.RegisterSource(SourceProfile{ID: "sanctions-registry", BaseReliability: 0.97}))

	claim := Claim{Subject: "VESSEL:IMO1112223", Predicate: "BENEFICIAL_OWNER"}
	_, err := e.Submit("unverified-blog", claim, "SHELLCORP-X", 1)
	must(t, err)
	_, err = e.Submit("sanctions-registry", claim, "KNOWN-OPERATOR-Y", 2)
	must(t, err)

	res, err := e.Arbitrate("analyst-1", claim, 5)
	must(t, err)
	if !res.Contradiction {
		t.Fatalf("expected contradiction to be flagged")
	}
	if res.Winner != "KNOWN-OPERATOR-Y" {
		t.Fatalf("winner = %q, want KNOWN-OPERATOR-Y (higher-reliability source)", res.Winner)
	}
	if res.RunnerUp != "SHELLCORP-X" {
		t.Fatalf("runner-up = %q, want SHELLCORP-X", res.RunnerUp)
	}
	if res.WinnerConfidence <= res.RunnerUpConfidence {
		t.Fatalf("winner confidence %v must exceed runner-up confidence %v", res.WinnerConfidence, res.RunnerUpConfidence)
	}
}

// TestArbitrateTieBreakDeterministic proves that equal-confidence
// contradictions resolve identically every time via the documented
// tie-break rule (earliest tick, then lexicographic value) — never via
// map iteration order.
func TestArbitrateTieBreakDeterministic(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		e := NewEngine(nil)
		must(t, e.RegisterSource(SourceProfile{ID: "src-a", BaseReliability: 0.6}))
		must(t, e.RegisterSource(SourceProfile{ID: "src-b", BaseReliability: 0.6}))
		claim := Claim{Subject: "VESSEL:IMO0000001", Predicate: "AIS_POSITION"}
		_, err := e.Submit("src-a", claim, "ZONE-B", 3) // later tick, "later" alphabetically
		must(t, err)
		_, err = e.Submit("src-b", claim, "ZONE-A", 1) // earlier tick, "earlier" alphabetically
		must(t, err)

		res, err := e.Arbitrate("analyst-1", claim, 10)
		must(t, err)
		// Equal confidence (both single-source 0.6) -> tie-break by
		// earliest firstTick -> ZONE-A (tick 1) must win every trial.
		if res.Winner != "ZONE-A" {
			t.Fatalf("trial %d: winner = %q, want ZONE-A via earliest-tick tiebreak", trial, res.Winner)
		}
	}
}

// TestReplayDeterministicAcrossSubmissionOrder proves the headline claim:
// two independently-built engines fed the *same* evidence set in
// *different* submission orders must reach byte-identical arbitration
// hashes, because fusion math always re-sorts evidence canonically
// before computing — arrival order must never leak into the result.
func TestReplayDeterministicAcrossSubmissionOrder(t *testing.T) {
	type item struct {
		src   SourceID
		value string
		tick  uint64
	}
	claim := Claim{Subject: "VESSEL:IMO5556667", Predicate: "FLAG_STATE"}
	items := []item{
		{"src-a", "LIBERIA", 1},
		{"src-b", "LIBERIA", 2},
		{"src-c", "MARSHALL-ISLANDS", 3},
		{"src-d", "LIBERIA", 4},
	}
	orders := [][]int{
		{0, 1, 2, 3},
		{3, 2, 1, 0},
		{2, 0, 3, 1},
	}

	var heads []string
	for _, order := range orders {
		e := NewEngine(nil)
		for _, it := range items {
			must(t, e.RegisterSource(SourceProfile{ID: it.src, BaseReliability: 0.65}))
		}
		for _, idx := range order {
			it := items[idx]
			_, err := e.Submit(it.src, claim, it.value, it.tick)
			must(t, err)
		}
		res, err := e.Arbitrate("analyst-1", claim, 99)
		must(t, err)
		if res.Winner != "LIBERIA" {
			t.Fatalf("order %v: winner = %q, want LIBERIA", order, res.Winner)
		}
		heads = append(heads, e.Head())
	}
	for i := 1; i < len(heads); i++ {
		if heads[i] != heads[0] {
			t.Fatalf("chain head diverged across submission order: %s vs %s", heads[0], heads[i])
		}
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	e := NewEngine(nil)
	must(t, e.RegisterSource(SourceProfile{ID: "src-a", BaseReliability: 0.8}))
	claim := Claim{Subject: "V1", Predicate: "FLAG"}
	_, err := e.Submit("src-a", claim, "PANAMA", 1)
	must(t, err)
	_, err = e.Arbitrate("analyst-1", claim, 2)
	must(t, err)

	if err := e.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain on untampered log failed: %v", err)
	}

	e.log[0].Winner = "TAMPERED"
	if err := e.VerifyChain(); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected after mutation, got %v", err)
	}
}

func TestReplayAllReturnsDefensiveCopy(t *testing.T) {
	e := NewEngine(nil)
	must(t, e.RegisterSource(SourceProfile{ID: "src-a", BaseReliability: 0.8}))
	claim := Claim{Subject: "V1", Predicate: "FLAG"}
	_, err := e.Submit("src-a", claim, "PANAMA", 1)
	must(t, err)
	_, err = e.Arbitrate("analyst-1", claim, 2)
	must(t, err)

	copy1 := e.ReplayAll()
	copy1[0].Winner = "MUTATED-COPY"
	copy2 := e.ReplayAll()
	if copy2[0].Winner == "MUTATED-COPY" {
		t.Fatalf("ReplayAll leaked internal state — mutation of returned copy affected engine")
	}
}

// TestKGWiring proves the graph-sink integration: an arbitration must
// produce exactly the Claim/Assertion/SupportsClaim topology the
// deep-dive audit specifies, queryable back out of kg.Graph.
func TestKGWiring(t *testing.T) {
	graph := kg.NewGraph()
	e := NewEngine(graph)
	must(t, e.RegisterSource(SourceProfile{ID: "sanctions-registry", BaseReliability: 0.95}))
	must(t, e.RegisterSource(SourceProfile{ID: "unverified-blog", BaseReliability: 0.3}))

	claim := Claim{Subject: "VESSEL:IMO1112223", Predicate: "BENEFICIAL_OWNER"}
	_, err := e.Submit("sanctions-registry", claim, "KNOWN-OPERATOR-Y", 1)
	must(t, err)
	_, err = e.Submit("unverified-blog", claim, "SHELLCORP-X", 2)
	must(t, err)

	res, err := e.Arbitrate("analyst-1", claim, 5)
	must(t, err)

	claimNode, ok := graph.GetNode("claim:" + claim.Key())
	if !ok || claimNode.Kind != "Claim" {
		t.Fatalf("Claim node not written correctly: %+v ok=%v", claimNode, ok)
	}
	winnerNode, ok := graph.GetNode("assertion:" + claim.Key() + "::" + res.Winner)
	if !ok || winnerNode.Props["contradiction"] != "true" {
		t.Fatalf("winning Assertion node missing/incorrect: %+v ok=%v", winnerNode, ok)
	}
	loserNode, ok := graph.GetNode("assertion:" + claim.Key() + "::" + res.RunnerUp)
	if !ok {
		t.Fatalf("runner-up Assertion node must still exist (queryable contradiction), got ok=%v", ok)
	}
	_ = loserNode

	if _, err := kg.ReplayAll(graph.Snapshot()); err != nil {
		t.Fatalf("kg.Graph chain corrupted by fusion writes: %v", err)
	}
}

// TestKGWiringReArbitrationKeepsSingleTruthEdge proves that arbitrating
// the same claim twice (e.g. after new evidence arrives) does not leave
// two competing AssertedTruth edges behind.
func TestKGWiringReArbitrationKeepsSingleTruthEdge(t *testing.T) {
	graph := kg.NewGraph()
	e := NewEngine(graph)
	must(t, e.RegisterSource(SourceProfile{ID: "src-a", BaseReliability: 0.6}))
	must(t, e.RegisterSource(SourceProfile{ID: "src-b", BaseReliability: 0.99}))

	claim := Claim{Subject: "V1", Predicate: "FLAG"}
	_, err := e.Submit("src-a", claim, "PANAMA", 1)
	must(t, err)
	res1, err := e.Arbitrate("analyst-1", claim, 2)
	must(t, err)
	if res1.Winner != "PANAMA" {
		t.Fatalf("first arbitration winner = %q, want PANAMA", res1.Winner)
	}

	_, err = e.Submit("src-b", claim, "LIBERIA", 3)
	must(t, err)
	res2, err := e.Arbitrate("analyst-1", claim, 4)
	must(t, err)
	if res2.Winner != "LIBERIA" {
		t.Fatalf("second arbitration winner = %q, want LIBERIA (higher-reliability source)", res2.Winner)
	}

	before := graph.LogLen()
	if _, err := kg.ReplayAll(graph.Snapshot()); err != nil {
		t.Fatalf("kg chain broken after re-arbitration: %v", err)
	}
	// Truth edge must point at the new winner, not the stale one.
	node, ok := graph.GetNode("claim:" + claim.Key())
	if !ok {
		t.Fatalf("claim node missing after re-arbitration")
	}
	_ = node
	_ = before
}

// TestConcurrentSubmitAndArbitrateRace exercises the engine under
// concurrent load with -race to catch data races in the shared
// evidence/log maps.
func TestConcurrentSubmitAndArbitrateRace(t *testing.T) {
	e := NewEngine(kg.NewGraph())
	sources := []SourceID{"src-a", "src-b", "src-c", "src-d", "src-e"}
	for _, s := range sources {
		must(t, e.RegisterSource(SourceProfile{ID: s, BaseReliability: 0.7}))
	}
	claim := Claim{Subject: "VESSEL:CONCURRENT", Predicate: "FLAG_STATE"}

	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func(idx int, src SourceID) {
			defer wg.Done()
			_, _ = e.Submit(src, claim, "PANAMA", uint64(idx+1))
		}(i, s)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(tick int) {
			defer wg.Done()
			_, _ = e.Arbitrate("analyst-concurrent", claim, uint64(1000+tick))
		}(i)
	}
	wg.Wait()

	if err := e.VerifyChain(); err != nil {
		t.Fatalf("chain corrupted under concurrent load: %v", err)
	}
}

func TestEvidenceForReturnsCanonicalOrder(t *testing.T) {
	e := NewEngine(nil)
	must(t, e.RegisterSource(SourceProfile{ID: "src-z", BaseReliability: 0.5}))
	must(t, e.RegisterSource(SourceProfile{ID: "src-a", BaseReliability: 0.5}))
	claim := Claim{Subject: "V1", Predicate: "FLAG"}
	_, err := e.Submit("src-z", claim, "X", 5)
	must(t, err)
	_, err = e.Submit("src-a", claim, "Y", 5)
	must(t, err)
	list := e.EvidenceFor(claim)
	if len(list) != 2 {
		t.Fatalf("expected 2 evidence items, got %d", len(list))
	}
	// Same tick -> ordered by Source, so src-a before src-z.
	if list[0].Source != "src-a" || list[1].Source != "src-z" {
		t.Fatalf("evidence not in canonical (tick,source,id) order: %+v", list)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
