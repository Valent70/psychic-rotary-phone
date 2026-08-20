package acceptance

import (
	"fmt"
	"sync"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/evidence/api"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
	"veriqo/pkg/replay"
	tstate "veriqo/pkg/trust/state"
)

func TestAcceptance91_ConcurrentDependencyDeclarationsAreSafe(t *testing.T) {
	g := evidencegraph.NewGraph()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = g.Declare(evidencegraph.DependencyRecord{
				NodeID: evidencegraph.NodeID(fmt.Sprintf("n%d", i)), UpstreamID: "sat",
				Kind: evidencegraph.DependsOnSource, Confidence: 1})
		}(i)
	}
	wg.Wait()
	if err := g.VerifyChain(); err != nil {
		t.Fatalf("concurrent declarations broke the ledger: %v", err)
	}
	if len(g.Ledger()) != 50 {
		t.Fatalf("expected 50 records, got %d", len(g.Ledger()))
	}
}

func TestAcceptance92_ConcurrentCanonicalRunsOnSharedPipeline(t *testing.T) {
	p := canonical.NewPipeline(nil)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := baseCase(fmt.Sprintf("VESSEL:CONC%d", i))
			if _, err := p.RunCanonical("actor", in); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent canonical run failed: %v", err)
	}
	if err := p.Dependencies.VerifyChain(); err != nil {
		t.Fatalf("dependency ledger corrupted under concurrency: %v", err)
	}
}

func TestAcceptance93_ConcurrentTrustAssessments(t *testing.T) {
	e := tstate.NewEngine(100, 0.5)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = e.Assess(fmt.Sprintf("subj%d", i), "op", "p", "clean", 0.8, []string{"ev"}, uint64(i), 0)
		}(i)
	}
	wg.Wait()
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("trust ledger corrupted: %v", err)
	}
}

func TestAcceptance94_ConcurrentIdentityAssertions(t *testing.T) {
	r := idResolver(t)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = r.Assert("op", "aggregator", idCall(fmt.Sprintf("C%d", i)), idIMO(fmt.Sprintf("%d", i)), uint64(i), "r")
		}(i)
	}
	wg.Wait()
	if err := r.VerifyChain(); err != nil {
		t.Fatalf("identity ledger corrupted: %v", err)
	}
}

func TestAcceptance95_ConcurrentFacadeSubmissions(t *testing.T) {
	f := unsignedFacade()
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = f.Submit(evOntology(fmt.Sprintf("src%d", i), "OFF", "sat-x", uint64(i+1)))
		}(i)
	}
	wg.Wait()
	if err := f.Verify(); err != nil {
		t.Fatalf("facade state inconsistent after concurrent submits: %v", err)
	}
}

func TestAcceptance96_ConcurrentReplaysAreIndependent(t *testing.T) {
	rec, _ := recordRun(t, "VESSEL:IMO9000020")
	var wg sync.WaitGroup
	hashes := make(chan string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pkg, err := replay.NewReplayPackage(fmt.Sprintf("auditor%d", i), "n", rec)
			if err != nil {
				return
			}
			cert, err := replay.NewEngine().Replay(pkg)
			if err == nil {
				hashes <- cert.ReplayResultHash
			}
		}(i)
	}
	wg.Wait()
	close(hashes)
	var first string
	for h := range hashes {
		if first == "" {
			first = h
		} else if h != first {
			t.Fatal("concurrent independent replays disagreed")
		}
	}
	if first == "" {
		t.Fatal("no replay completed")
	}
}

func TestAcceptance97_ConcurrentReadsDuringWrites(t *testing.T) {
	g := evidencegraph.NewGraph()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				_, _ = g.Declare(evidencegraph.DependencyRecord{
					NodeID: evidencegraph.NodeID(fmt.Sprintf("w%d", i)), UpstreamID: "u",
					Kind: evidencegraph.DependsOnSource, Confidence: 1})
			}
		}
	}()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.RootHash()
			_ = g.SharedFractionAt([]evidencegraph.NodeID{"w1", "w2"}, 1)
		}()
	}
	close(stop)
	wg.Wait()
	if err := g.VerifyChain(); err != nil {
		t.Fatalf("concurrent read/write corrupted the ledger: %v", err)
	}
}

func TestAcceptance98_StressManySourcesSingleCase(t *testing.T) {
	subs := make([]canonical.SourceSubmission, 0, 60)
	for i := 0; i < 60; i++ {
		subs = append(subs, canonical.SourceSubmission{
			SourceID: fusion.SourceID(fmt.Sprintf("s%d", i)), Value: "OFF",
			BaseReliability: 0.7, Provider: fmt.Sprintf("prov%d", i%5)})
	}
	in := baseCase("VESSEL:STRESS", subs...)
	in.Policy = risk.DefaultPolicy()
	res, err := canonical.NewPipeline(nil).RunCanonical("a", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if res.Certificate.IndependentFamilies != 5 {
		t.Fatalf("expected 5 independent families across 60 sources, got %d", res.Certificate.IndependentFamilies)
	}
}

func TestAcceptance99_ConcurrentFacadeArbitrationsDistinctClaims(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f := unsignedFacade()
			if _, err := f.Submit(evOntology("a", "OFF", "", 1)); err != nil {
				return
			}
			_, _ = f.Arbitrate("actor", "VESSEL:IMO9000001", "AIS_STATUS", risk.DefaultPolicy(), uint64(i+1))
		}(i)
	}
	wg.Wait()
}

func TestAcceptance100_ConcurrentIdentityResolutionIsStable(t *testing.T) {
	r := idResolver(t)
	if _, err := r.Merge("op", "flag-registry", idIMO("999"), idCall("QQ"), 1, "r"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var wg sync.WaitGroup
	out := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := r.EntityIDAt(idIMO("999"), 10)
			if err == nil {
				out <- id
			}
		}()
	}
	wg.Wait()
	close(out)
	var first string
	for id := range out {
		if first == "" {
			first = id
		} else if id != first {
			t.Fatal("concurrent identity resolution is unstable")
		}
	}
	_ = identity.Kind("")
	_ = api.DefaultConfig()
}
