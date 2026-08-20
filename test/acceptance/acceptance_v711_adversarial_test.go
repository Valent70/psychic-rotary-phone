package acceptance

import (
	"testing"

	"veriqo/pkg/authz"
	"veriqo/pkg/canonical"
	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/moat/intelligence/risk"
	tstate "veriqo/pkg/trust/state"
)

func TestAcceptance61_CorrelatedSourcesCannotInflateConfidence(t *testing.T) {
	mk := func(provider string) canonical.CaseInput {
		return baseCase("VESSEL:IMO9000002",
			canonical.SourceSubmission{SourceID: fusion.SourceID("a"), Value: "OFF", BaseReliability: 0.8, Provider: provider},
			canonical.SourceSubmission{SourceID: fusion.SourceID("b"), Value: "OFF", BaseReliability: 0.8, Provider: provider})
	}
	shared, err := canonical.NewPipeline(nil).RunCanonical("a", mk("sat-x"))
	if err != nil {
		t.Fatalf("shared: %v", err)
	}
	indep, err := canonical.NewPipeline(nil).RunCanonical("a", mk(""))
	if err != nil {
		t.Fatalf("independent: %v", err)
	}
	if shared.Arbitration.WinnerConfidence >= indep.Arbitration.WinnerConfidence {
		t.Fatal("correlated evidence inflated confidence")
	}
}

func TestAcceptance62_DerivedEvidenceRejectedAsPrimary(t *testing.T) {
	base := evOntology("a", "OFF", "", 1)
	der, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeDerivedInference, Subject: "s", Predicate: "p", Object: "o",
		Source: "veriqo", ObservedAt: 2, Confidence: 0.9, DerivedFrom: []string{base.EvidenceID},
		Attributes: map[string]string{"method": "m"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := ontology.RequirePrimary([]ontology.Evidence{base, der}); err == nil {
		t.Fatal("derived evidence accepted as primary")
	}
}

func TestAcceptance63_MalformedDependencyRecordRejected(t *testing.T) {
	g := evidencegraph.NewGraph()
	if _, err := g.Declare(evidencegraph.DependencyRecord{NodeID: "n", UpstreamID: "n", Kind: evidencegraph.DependsOnSource}); err == nil {
		t.Fatal("self-dependency accepted")
	}
	if _, err := g.Declare(evidencegraph.DependencyRecord{NodeID: "n", UpstreamID: "u", Kind: "NOPE"}); err == nil {
		t.Fatal("unknown dependency kind accepted")
	}
}

func TestAcceptance64_UnsignedEvidenceRejectedAtBoundary(t *testing.T) {
	e := evOntology("a", "OFF", "", 1)
	if err := e.VerifySignature(); err == nil {
		t.Fatal("unsigned evidence verified")
	}
}

func TestAcceptance65_TrustCannotRiseWithoutEvidence(t *testing.T) {
	e := tstate.NewEngine(100, 0.5)
	if _, err := e.Assess("acme", "op", "p", "vibes", 0.95, nil, 1, 0); err == nil {
		t.Fatal("trust raised with no evidence")
	}
}

func TestAcceptance66_RevokedTrustCannotBeAutoRestored(t *testing.T) {
	e := tstate.NewEngine(100, 0.5)
	if _, err := e.Revoke("acme", "op", "sanctioned", []string{"ofac"}, 1); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := e.Assess("acme", "op", "p", "looks fine now", 0.9, []string{"ev"}, 2, 0); err == nil {
		t.Fatal("automated path un-revoked a revoked subject")
	}
}

func TestAcceptance67_UnregisteredSourceCannotAssertIdentity(t *testing.T) {
	r := identity.NewResolver()
	if _, err := r.Assert("a", "rogue-scraper",
		identity.Identifier{Kind: identity.KindIMO, Value: "1"},
		identity.Identifier{Kind: identity.KindCallsign, Value: "X"}, 1, "scraped"); err == nil {
		t.Fatal("unregistered source asserted identity")
	}
}

func TestAcceptance68_DenyAlwaysBeatsAllow(t *testing.T) {
	e := authz.NewEngine()
	if _, err := e.Publish(authz.Document{ID: "p", Rules: []authz.Rule{
		{ID: "a-allow-all", Effect: authz.Allow, Actions: []string{"*"}, Resources: []string{"*"}},
		{ID: "z-deny-secret", Effect: authz.Deny, Actions: []string{"read"}, Resources: []string{"case:secret"}},
	}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := e.Activate(1); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	ex, err := e.Can(authz.Request{Actor: "bob", Action: "read", Resource: "case:secret"})
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if ex.Allowed {
		t.Fatal("ALLOW overrode DENY")
	}
}

func TestAcceptance69_ImpossibleObservationSequenceRejected(t *testing.T) {
	tr := hbayes.Transition{States: []hbayes.State{"A", "B"}, P: map[hbayes.State]map[hbayes.State]float64{
		"A": {"A": 1, "B": 0}, "B": {"A": 0, "B": 1}}}
	m, err := hbayes.NewTemporalModel([]hbayes.State{"A", "B"}, tr, map[hbayes.State]float64{"A": 1, "B": 0}, nil, 0)
	if err != nil {
		t.Fatalf("NewTemporalModel: %v", err)
	}
	if _, err := m.Infer([]hbayes.TickObservations{{Tick: 2}, {Tick: 1}}, nil); err == nil {
		t.Fatal("out-of-order observation ticks accepted")
	}
}

func TestAcceptance70_ArbitrationRefusesUnevaluatedSource(t *testing.T) {
	in := baseCase("VESSEL:IMO9000003")
	in.Submissions = append(in.Submissions, canonical.SourceSubmission{
		SourceID: fusion.SourceID("late-arrival"), Value: "OFF", BaseReliability: 0.7})
	in.Policy = risk.DefaultPolicy()
	res, err := canonical.NewPipeline(nil).RunCanonical("a", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	if _, ok := res.Dependency.Effective["late-arrival"]; !ok {
		t.Fatal("a source reached fusion without dependency evaluation")
	}
}
