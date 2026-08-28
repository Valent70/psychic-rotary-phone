// Session update — closes roadmap item #2 from the audit follow-up
// report (§4): pkg/kernel/intentgraph 79.4% -> higher. Real gaps
// closed, all exercising genuinely reachable public API paths (not
// engineered/unreachable branches):
//
//   - AddRule, previously 0% covered despite being the package's own
//     documented extension point for "domain-specific prediction
//     rules (e.g. maritime-specific patterns)".
//   - PredictFor, previously 0% covered — both its "matches the
//     caller's specific proposal" and "does not match" branches.
//   - Facts, previously 0% covered — the package's documented audit
//     surface ("for audit and for tests that want to assert on the
//     derivation trail directly").
package intentgraph

import (
	"testing"

	"veriqo/pkg/kernel/reasoning"
)

func TestAddRule_DomainSpecificRuleFires(t *testing.T) {
	g := NewGraph(nil)
	// A maritime-specific rule layered on top of the one shipped base
	// rule, exactly as the package doc comment describes callers doing.
	// Note: Predict() only surfaces facts where predicted_outcome ==
	// "fulfilled" (see intentgraph.go's Predict), so a domain rule that
	// wants to participate in Predict's output must target that same
	// outcome value — a "held"/"denied" outcome value would fire
	// correctly in the KB but never surface via Predict, which is a
	// real constraint of this package worth documenting via this test,
	// not a bug to work around.
	g.AddRule(reasoning.Rule{
		Name: "flagged_port_predicts_fulfillment_by_declaration",
		If: []reasoning.Triple{
			{Subject: reasoning.Var("actor"), Predicate: "declared_port", Object: "sanctioned"},
			{Subject: reasoning.Var("actor"), Predicate: "proposes", Object: reasoning.Var("proposal")},
		},
		Then: reasoning.Triple{Subject: reasoning.Var("proposal"), Predicate: "predicted_outcome", Object: "fulfilled"},
	})

	if err := g.Register(PendingIntent{
		IntentID: "hold-1", ActorID: "operator-Z", Subject: "vessel-4242",
		ProposedAction: "ENTER_PORT", ActorReliability: 0.3, Tick: 1,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Assert the extra fact the domain rule needs, directly against the
	// underlying KB the same way registerBaseRules asserts its own facts.
	g.kb.Assert(reasoning.Triple{Subject: "operator-Z", Predicate: "declared_port", Object: "sanctioned"})

	res, err := g.Predict("vessel-4242")
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !res.HasDerivation {
		t.Fatalf("expected the newly-added domain rule to fire, got %+v", res)
	}
	if res.Derivation.RuleName != "flagged_port_predicts_fulfillment_by_declaration" {
		t.Fatalf("want the domain-specific rule in the derivation trail, got %q", res.Derivation.RuleName)
	}
}

func TestPredictFor_MatchesAndMismatches(t *testing.T) {
	g := NewGraph(nil)
	pi := PendingIntent{
		IntentID: "pf-1", ActorID: "operator-B", Subject: "vessel-7000",
		ProposedAction: "TRANSIT_NORMAL", ActorReliability: 0.95, Tick: 1,
	}
	if err := g.Register(pi); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Matching case: PredictFor(pi) should report a fulfilled derivation
	// for exactly this actor's exact proposal.
	res, err := g.PredictFor(pi)
	if err != nil {
		t.Fatalf("PredictFor: %v", err)
	}
	if !res.HasDerivation || res.ProposedAction != "TRANSIT_NORMAL" {
		t.Fatalf("expected a matching derivation for the registered proposal, got %+v", res)
	}

	// Mismatching case: asking about a DIFFERENT proposed action for the
	// same subject must not borrow another proposal's derivation.
	other := pi
	other.ProposedAction = "DIVERT_ROUTE"
	res2, err := g.PredictFor(other)
	if err != nil {
		t.Fatalf("PredictFor (mismatch): %v", err)
	}
	if res2.HasDerivation {
		t.Fatalf("expected no derivation for an unregistered proposal, got %+v", res2)
	}
	if res2.ProposedAction != "DIVERT_ROUTE" {
		t.Fatalf("expected the queried proposal to be echoed back, got %q", res2.ProposedAction)
	}
}

func TestFacts_ExposesUnderlyingKBFactSet(t *testing.T) {
	g := NewGraph(nil)
	if err := g.Register(PendingIntent{
		IntentID: "facts-1", ActorID: "operator-C", Subject: "vessel-1111",
		ProposedAction: "ANCHOR", ActorReliability: 0.8, Tick: 1,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	facts := g.Facts()
	if len(facts) == 0 {
		t.Fatal("expected Facts() to expose at least the asserted reliability_tier and proposes facts")
	}
	var sawReliability, sawProposes bool
	for _, f := range facts {
		if f.Subject == "operator-C" && f.Predicate == "reliability_tier" && f.Object == "high" {
			sawReliability = true
		}
		if f.Subject == "operator-C" && f.Predicate == "proposes" {
			sawProposes = true
		}
	}
	if !sawReliability || !sawProposes {
		t.Fatalf("Facts() did not expose the expected asserted facts: %+v", facts)
	}
}
