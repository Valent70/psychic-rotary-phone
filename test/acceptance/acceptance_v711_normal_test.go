// v7.11.0 expansion of the permanent acceptance suite (PHASE 35).
// The suite may only ever GROW: internal/assurance.AcceptanceManifest
// enforces per-category minimums, and scripts/production-readiness.sh
// fails the release if any count decreases.
package acceptance

import (
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/evidence/api"
	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/moat/intelligence/risk"
	"veriqo/pkg/moat/simulation"
	"veriqo/pkg/replay"
	tstate "veriqo/pkg/trust/state"
)

func evOntology(source, object, producer string, tick uint64) ontology.Evidence {
	e, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeAISObservation, Subject: "VESSEL:IMO9000001", Predicate: "AIS_STATUS",
		Object: object, Source: source, ObservedAt: tick, Confidence: 0.8,
		Attributes: map[string]string{"mmsi": "1"},
		Provenance: ontology.Provenance{ProducerID: producer},
	})
	if err != nil {
		panic(err)
	}
	return e
}

func unsignedFacade() *api.Facade {
	return api.New(nil, nil, api.Config{RequireSignatures: false, RequirePrimaryForArbitration: true, MinIdentityConfidence: 0.5})
}

func TestAcceptance51_CanonicalRunEmitsDependencyCommitment(t *testing.T) {
	p := canonical.NewPipeline(nil)
	res, err := p.RunCanonical("a", baseCase("VESSEL:IMO9000001"))
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if res.Certificate.DependencyRootHash == "" || len(res.Dependency.Effective) != 3 {
		t.Fatalf("dependency evaluation not committed: %+v", res.Certificate)
	}
}

func TestAcceptance52_EvidenceFacadeHappyPath(t *testing.T) {
	f := unsignedFacade()
	for _, e := range []ontology.Evidence{evOntology("a", "OFF", "", 1), evOntology("b", "ON", "", 2)} {
		if _, err := f.Submit(e); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if _, err := f.Arbitrate("actor", "VESSEL:IMO9000001", "AIS_STATUS", risk.DefaultPolicy(), 5); err != nil {
		t.Fatalf("Arbitrate: %v", err)
	}
	if err := f.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestAcceptance53_FullLifecycleReplayMatches(t *testing.T) {
	p := canonical.NewPipeline(nil)
	in := baseCase("VESSEL:IMO9000001")
	res, err := p.RunCanonical("a", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	rec, err := replay.Record("a", in, res, p.Dependencies.ReplayAll(), "")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	pkg, err := replay.NewReplayPackage("auditor", "n1", rec)
	if err != nil {
		t.Fatalf("NewReplayPackage: %v", err)
	}
	cert, err := replay.NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("replay mismatch: %v", err)
	}
}

func TestAcceptance54_TemporalBayesianPosteriorMoves(t *testing.T) {
	tr := hbayes.Transition{States: []hbayes.State{"DARK", "NORMAL"}, P: map[hbayes.State]map[hbayes.State]float64{
		"DARK": {"DARK": 0.9, "NORMAL": 0.1}, "NORMAL": {"DARK": 0.05, "NORMAL": 0.95}}}
	m, err := hbayes.NewTemporalModel([]hbayes.State{"DARK", "NORMAL"}, tr,
		map[hbayes.State]float64{"DARK": 0.1, "NORMAL": 0.9}, nil, 0)
	if err != nil {
		t.Fatalf("NewTemporalModel: %v", err)
	}
	out, err := m.Infer([]hbayes.TickObservations{
		{Tick: 1, Observations: []hbayes.Observation{{SourceID: "sar", Likelihood: map[hbayes.State]float64{"DARK": 0.9, "NORMAL": 0.1}}}},
	}, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if out.Posterior("DARK") <= 0.1 {
		t.Fatalf("posterior did not update: %.4f", out.Posterior("DARK"))
	}
}

func TestAcceptance55_SimulationCarriesLineage(t *testing.T) {
	c := causal.NewGraph()
	tw := digitaltwin.NewRegistry()
	if _, err := c.Observe("a", "sanctions", "claim:dark", 0.7, 0, "", 1); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := tw.ApplyDecision("a", "E1", decision.Decision{Subject: "E1", PolicyName: "p", RiskScore: 0.5, Action: "FLAG"}, 1); err != nil {
		t.Fatalf("ApplyDecision: %v", err)
	}
	r, err := simulation.New(c, tw).SimulateDecision("E1", "p", "claim:dark", simulation.Params{Horizon: 3, BaseTarget: 10})
	if err != nil {
		t.Fatalf("SimulateDecision: %v", err)
	}
	if err := simulation.VerifyResult(r); err != nil || r.ReplayID == "" {
		t.Fatalf("simulation lineage incomplete: %v", err)
	}
}

func TestAcceptance56_TrustStateExplains(t *testing.T) {
	e := tstate.NewEngine(100, 0.5)
	if _, err := e.Assess("acme", "op", "p", "clean registry", 0.9, []string{"ev1"}, 1, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	lines, err := e.Explain("acme", 2)
	if err != nil || len(lines) < 2 {
		t.Fatalf("Explain: %v %v", err, lines)
	}
}

func TestAcceptance57_OntologyBitemporalKnowledgeFilter(t *testing.T) {
	early := evOntology("a", "OFF", "", 10)
	late, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeAISObservation, Subject: "VESSEL:IMO9000001", Predicate: "AIS_STATUS",
		Object: "ON", Source: "b", ObservedAt: 20, ReceivedAt: 500, Confidence: 0.8,
		Attributes: map[string]string{"mmsi": "2"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := ontology.AsOf([]ontology.Evidence{early, late}, 100, 100); len(got) != 1 {
		t.Fatalf("bitemporal filter wrong: %d", len(got))
	}
}

func TestAcceptance58_EconomicConsequenceStillComputed(t *testing.T) {
	in := baseCase("VESSEL:IMO9000001")
	in.EconomicChannels = map[string]float64{"commodity_flow_usd": 1_000_000}
	res, err := canonical.NewPipeline(nil).RunCanonical("a", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if res.EconomicImpact.TotalExpectedImpact == 0 {
		t.Fatal("economic consequence not computed")
	}
}

func TestAcceptance59_IndependentFamiliesReported(t *testing.T) {
	in := baseCase("VESSEL:IMO9000001", canonical.SourceSubmission{
		SourceID: fusion.SourceID("v1"), Value: "OFF", BaseReliability: 0.8, Provider: "sat-x"},
		canonical.SourceSubmission{SourceID: fusion.SourceID("v2"), Value: "OFF", BaseReliability: 0.8, Provider: "sat-x"})
	res, err := canonical.NewPipeline(nil).RunCanonical("a", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if res.Certificate.IndependentFamilies != 1 {
		t.Fatalf("expected 1 independent family, got %d", res.Certificate.IndependentFamilies)
	}
}

func TestAcceptance60_DependencyLedgerVerifiesAfterRun(t *testing.T) {
	p := canonical.NewPipeline(nil)
	if _, err := p.RunCanonical("a", baseCase("VESSEL:IMO9000001")); err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if err := p.Dependencies.VerifyChain(); err != nil {
		t.Fatalf("dependency ledger: %v", err)
	}
	if _, err := evidencegraph.Rebuild(p.Dependencies.ReplayAll()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
}
