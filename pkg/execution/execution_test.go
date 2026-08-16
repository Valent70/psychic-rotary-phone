package execution

import (
	"context"
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/explanation"
	"veriqo/pkg/governance/knowledge"
	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/economic"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/moat/intelligence/risk"
)

func caseInput() canonical.CaseInput {
	return canonical.CaseInput{
		Entity: digitaltwin.EntityID("IMO9999999"), Subject: "IMO9999999", Predicate: "port_call",
		Submissions: []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("ais-vendor-a"), Value: "singapore",
				BaseReliability: 0.8, Provider: "sat-x", UpstreamID: "feed-sat-x"},
			{SourceID: fusion.SourceID("ais-vendor-b"), Value: "singapore",
				BaseReliability: 0.8, Provider: "sat-x", UpstreamID: "feed-sat-x"},
			{SourceID: fusion.SourceID("port-authority"), Value: "singapore",
				BaseReliability: 0.9, Provider: "port", UpstreamID: "feed-port"},
			{SourceID: fusion.SourceID("broker-report"), Value: "jakarta",
				BaseReliability: 0.5, Provider: "broker",
				Dependencies: []evidencegraph.DependencyRecord{
					{UpstreamID: evidencegraph.NodeID("model-detector-v3"),
						Kind: evidencegraph.DependsOnTransformation, Confidence: 0.6},
				}},
		},
		Policy:           risk.DefaultPolicy(),
		PatternScore:     0.7,
		PriceAnomaly:     0.9,
		EconomicChannels: map[string]float64{"freight": 1_000_000, "insurance": 250_000},
		Tick:             42,
	}
}

func ctx() Context {
	return Context{ExecutionID: "exec-1", CaseID: "case-1", Tenant: "acme", Actor: "analyst",
		PolicyVersion: "sanctions-screen@3", EvidencePackageID: "evp-1",
		IdentityResolutionVersion: "ident@2", LedgerPosition: 0, Tick: 42,
		ReplayMetadata: "unit-test"}
}

func scenarios() []economic.Scenario {
	return []economic.Scenario{
		{ID: "clean", Probability: 0.9, Outcome: 50_000, EvidenceRefs: []string{"e1"}},
		{ID: "detained", Probability: 0.08, Outcome: -400_000, EvidenceRefs: []string{"e2"}},
		{ID: "sanctioned", Probability: 0.02, Outcome: -5_000_000, EvidenceRefs: []string{"e3"}},
	}
}

func run(t *testing.T) (*Engine, *Result) {
	t.Helper()
	e := NewEngine(nil)
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	return e, res
}

func TestTopologicalOrderIsDeterministicAndComplete(t *testing.T) {
	first, err := topoOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(graph) {
		t.Fatalf("every stage must be ordered, got %d of %d", len(first), len(graph))
	}
	for i := 0; i < 50; i++ {
		again, _ := topoOrder()
		for j := range first {
			if first[j] != again[j] {
				t.Fatal("topological order must be deterministic across runs")
			}
		}
	}
	pos := map[StageID]int{}
	for i, id := range first {
		pos[id] = i
	}
	for _, g := range graph {
		for _, d := range g.deps {
			if pos[d] >= pos[g.id] {
				t.Fatalf("%s must be ordered before %s", d, g.id)
			}
		}
	}
}

func TestOneExecutionProducesEveryRequiredArtifact(t *testing.T) {
	_, res := run(t)
	if len(res.Trace.Nodes) != len(graph) {
		t.Fatalf("the trace must cover every stage, got %d", len(res.Trace.Nodes))
	}
	if res.ExecutionRootHash == "" || res.Trace.ContextHash == "" {
		t.Fatal("execution root and context hash are mandatory")
	}
	if res.Decision == "" {
		t.Fatal("a decision is mandatory")
	}
	if res.Explanation.Hash == "" {
		t.Fatal("a consolidated decision explanation is mandatory")
	}
	if res.ReplayPackage.ReplayPackageID == "" {
		t.Fatal("a replay package is mandatory")
	}
	if res.Certificate.VerificationCertificateID == "" {
		t.Fatal("a verification certificate is mandatory")
	}
	if !res.Certificate.Match {
		t.Fatalf("the in-run independent replay must match, diverged at %s",
			res.Certificate.DivergedStage)
	}
	if res.Economic.Hash == "" || res.Economic.CVaR < res.Economic.VaR {
		t.Fatalf("economic consequence must be computed coherently: %+v", res.Economic)
	}
}

func TestIncompleteContextIsRefused(t *testing.T) {
	e := NewEngine(nil)
	for _, mutate := range []func(*Context){
		func(c *Context) { c.ExecutionID = "" },
		func(c *Context) { c.Tenant = "" },
		func(c *Context) { c.Actor = "" },
		func(c *Context) { c.PolicyVersion = "" },
		func(c *Context) { c.IdentityResolutionVersion = "" },
	} {
		c := ctx()
		mutate(&c)
		if _, err := e.Run(context.Background(), Input{Context: c, Case: caseInput()}); !errors.Is(err, ErrContextIncomplete) {
			t.Fatalf("an incomplete execution context must be refused, got %v", err)
		}
	}
}

func TestEveryNodeCarriesTheRequiredShape(t *testing.T) {
	_, res := run(t)
	for _, n := range res.Trace.Nodes {
		if n.StageID == "" || n.Hash == "" || n.Version == "" {
			t.Fatalf("node missing identity: %+v", n)
		}
		if n.ExecutionTick != 42 {
			t.Fatalf("node %s carries the wrong tick: %d", n.StageID, n.ExecutionTick)
		}
		if n.Status != StatusOK && n.Status != StatusSkipped {
			t.Fatalf("node %s failed: %s / %s", n.StageID, n.Status, n.Error)
		}
	}
}

func TestSkippedStagesAreDeclaredNotFabricated(t *testing.T) {
	e := NewEngine(nil)
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput()}) // no scenarios
	if err != nil {
		t.Fatal(err)
	}
	var econ Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageEconomic {
			econ = n
		}
	}
	if econ.Status != StatusSkipped {
		t.Fatalf("with no scenarios the economic stage must be SKIPPED, got %s", econ.Status)
	}
	if res.Economic.Hash != "" {
		t.Fatal("a skipped economic stage must not fabricate a consequence")
	}
}

// identityFixture builds a Resolver with one registered authority --
// the minimum needed for EntityIDAt to have real ledger state to
// re-derive from, mirroring pkg/lifecycle's own identityAuthoritySourceID
// pattern.
func identityFixture(t *testing.T) *identity.Resolver {
	t.Helper()
	r := identity.NewResolver()
	if err := r.RegisterAuthority(identity.Authority{SourceID: "test-authority", Weight: 1}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	return r
}

// TestIdentityResolutionIndependentlyReResolvesAndConfirmsMatch is the
// P0-4 positive property: when a real Identifier is supplied, the
// IDENTITY_RESOLUTION stage does not merely trust Case.Entity, it calls
// Identity.EntityIDAt itself and only succeeds because the independent
// call agrees.
func TestIdentityResolutionIndependentlyReResolvesAndConfirmsMatch(t *testing.T) {
	r := identityFixture(t)
	q := identity.Identifier{Kind: identity.KindIMO, Value: "9998887"}
	resolved, err := r.EntityIDAt(q, 42)
	if err != nil {
		t.Fatalf("EntityIDAt: %v", err)
	}

	c := caseInput()
	c.Entity = digitaltwin.EntityID(resolved)
	c.Subject = resolved

	e := NewEngine(nil)
	e.Identity = r
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: c, Scenarios: scenarios(), Currency: "USD",
		IdentityAliases: []identity.Identifier{q}})
	if err != nil {
		t.Fatalf("expected a matching independent re-resolution to succeed, got: %v", err)
	}
	var idNode Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageIdentityResolution {
			idNode = n
		}
	}
	if idNode.Status != StatusOK {
		t.Fatalf("expected IDENTITY_RESOLUTION to succeed, got %s: %s", idNode.Status, idNode.Error)
	}
	if !strings.Contains(idNode.Detail, "independently re-resolved") {
		t.Fatalf("expected the node detail to record the independent re-resolution, got %q", idNode.Detail)
	}
}

// TestIdentityResolutionFailsWhenReResolutionDisagrees is the P0-4
// adversarial property: a caller-claimed Case.Entity that the identity
// ledger would NOT independently produce must fail the stage (and the
// whole execution), not silently pass through. This is what makes the
// re-resolution call real rather than decorative -- a decorative call
// whose result is ignored could never fail.
func TestIdentityResolutionFailsWhenReResolutionDisagrees(t *testing.T) {
	r := identityFixture(t)
	q := identity.Identifier{Kind: identity.KindIMO, Value: "9998887"}

	c := caseInput()
	c.Entity = digitaltwin.EntityID("ENT:this-does-not-match-what-the-ledger-would-derive")

	e := NewEngine(nil)
	e.Identity = r
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: c, Scenarios: scenarios(), Currency: "USD",
		IdentityAliases: []identity.Identifier{q}})
	// Run()'s top-level error wraps ErrStageFailed (Node.Error is a
	// plain string, for JSON serialisation, so the chain does not carry
	// ErrIdentityMismatch through errors.Is at this level) -- the
	// specific cause is asserted below via the node's own recorded
	// error text instead.
	if !errors.Is(err, ErrStageFailed) {
		t.Fatalf("expected ErrStageFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), string(StageIdentityResolution)) {
		t.Fatalf("expected the error to name IDENTITY_RESOLUTION, got %v", err)
	}
	var idNode Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageIdentityResolution {
			idNode = n
		}
	}
	if idNode.Status != StatusFailed {
		t.Fatalf("expected IDENTITY_RESOLUTION to FAIL on disagreement, got %s", idNode.Status)
	}
	if !strings.Contains(idNode.Error, ErrIdentityMismatch.Error()) {
		t.Fatalf("expected the node's own error to name the identity mismatch, got %q", idNode.Error)
	}
	// Every downstream stage must also fail closed (dependency not
	// satisfied), never fabricate a decision over a failed identity
	// check.
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageDecision && n.Status == StatusOK {
			t.Fatal("DECISION must not succeed when IDENTITY_RESOLUTION failed")
		}
	}
}

// TestIdentityResolutionWithoutAliasesPreservesPriorBindingOnlyBehavior
// proves nil-safety: omitting IdentityAliases (every existing
// production/test call site before this round) must not change
// behavior -- still OK, still bound to the ledger head, never attempts
// re-resolution.
func TestIdentityResolutionWithoutAliasesPreservesPriorBindingOnlyBehavior(t *testing.T) {
	r := identityFixture(t)
	e := NewEngine(nil)
	e.Identity = r
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	var idNode Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageIdentityResolution {
			idNode = n
		}
	}
	if idNode.Status != StatusOK {
		t.Fatalf("expected IDENTITY_RESOLUTION to stay OK without aliases, got %s", idNode.Status)
	}
	if strings.Contains(idNode.Detail, "independently re-resolved") {
		t.Fatal("must not claim independent re-resolution when no alias was supplied")
	}
}

// temporalFixture builds a minimal, valid two-state hbayes.Model --
// the same shape hbayes's own tests use -- for P0-5's execution-level
// wiring tests.
func temporalFixture(t *testing.T) *hbayes.Model {
	t.Helper()
	tr := hbayes.Transition{
		States: []hbayes.State{"DARK", "NORMAL"},
		P: map[hbayes.State]map[hbayes.State]float64{
			"DARK":   {"DARK": 0.85, "NORMAL": 0.15},
			"NORMAL": {"DARK": 0.05, "NORMAL": 0.95},
		},
	}
	m, err := hbayes.NewTemporalModel([]hbayes.State{"DARK", "NORMAL"}, tr,
		map[hbayes.State]float64{"DARK": 0.1, "NORMAL": 0.9}, nil, 0)
	if err != nil {
		t.Fatalf("NewTemporalModel: %v", err)
	}
	return m
}

// TestTemporalStageCallsRealInferenceWhenSupplied is P0-5's positive
// property: TEMPORAL_BAYESIAN is no longer unconditionally SKIPPED --
// when a real model and observation series are supplied it performs a
// genuine pkg/moat/hbayes.Model.Infer call and records the real result.
func TestTemporalStageCallsRealInferenceWhenSupplied(t *testing.T) {
	e := NewEngine(nil)
	seq := []hbayes.TickObservations{
		{Tick: 1, Observations: []hbayes.Observation{
			{SourceID: "sar", Likelihood: map[hbayes.State]float64{"DARK": 0.9, "NORMAL": 0.1}}}},
		{Tick: 2, Observations: []hbayes.Observation{
			{SourceID: "sar", Likelihood: map[hbayes.State]float64{"DARK": 0.9, "NORMAL": 0.1}}}},
	}
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD",
		TemporalModel: temporalFixture(t), TemporalObservations: seq})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	var temporal Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageTemporal {
			temporal = n
		}
	}
	if temporal.Status != StatusOK {
		t.Fatalf("expected TEMPORAL_BAYESIAN to succeed, got %s: %s", temporal.Status, temporal.Error)
	}
	if temporal.Hash == "" {
		t.Fatal("expected a real inference trace hash")
	}
	if !strings.Contains(temporal.Detail, "log-likelihood") {
		t.Fatalf("expected the detail to report the real inference outcome, got %q", temporal.Detail)
	}
}

// TestTemporalStageFailsClosedOnInvalidSeries proves the call is real,
// not decorative: a genuinely invalid observation sequence (ticks out
// of order) must fail the stage via hbayes's own validation, not be
// silently swallowed.
func TestTemporalStageFailsClosedOnInvalidSeries(t *testing.T) {
	e := NewEngine(nil)
	badSeq := []hbayes.TickObservations{
		{Tick: 2, Observations: []hbayes.Observation{
			{SourceID: "sar", Likelihood: map[hbayes.State]float64{"DARK": 0.9, "NORMAL": 0.1}}}},
		{Tick: 1, Observations: []hbayes.Observation{ // out of order
			{SourceID: "sar", Likelihood: map[hbayes.State]float64{"DARK": 0.9, "NORMAL": 0.1}}}},
	}
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD",
		TemporalModel: temporalFixture(t), TemporalObservations: badSeq})
	if !errors.Is(err, ErrStageFailed) {
		t.Fatalf("expected ErrStageFailed, got %v", err)
	}
	var temporal Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageTemporal {
			temporal = n
		}
	}
	if temporal.Status != StatusFailed {
		t.Fatalf("expected TEMPORAL_BAYESIAN to FAIL on an invalid series, got %s", temporal.Status)
	}
	if !strings.Contains(temporal.Error, hbayes.ErrNonMonotonicTicks.Error()) {
		t.Fatalf("expected the stage's own error to name the real hbayes validation failure, got %q", temporal.Error)
	}
}

// TestTemporalStageStillSkipsWithoutBothInputs proves nil-safety: every
// pre-existing call site (which never sets TemporalModel/
// TemporalObservations) keeps the exact prior SKIPPED behavior. Also
// checks the partial case (model without observations, and vice versa)
// to prove the stage requires BOTH rather than fabricating from one.
func TestTemporalStageStillSkipsWithoutBothInputs(t *testing.T) {
	cases := []struct {
		name  string
		model *hbayes.Model
		obs   []hbayes.TickObservations
	}{
		{"neither", nil, nil},
		{"model only", temporalFixture(t), nil},
		{"observations only", nil, []hbayes.TickObservations{
			{Tick: 1, Observations: []hbayes.Observation{
				{SourceID: "sar", Likelihood: map[hbayes.State]float64{"DARK": 0.9, "NORMAL": 0.1}}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEngine(nil)
			res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD",
				TemporalModel: c.model, TemporalObservations: c.obs})
			if err != nil {
				t.Fatalf("run failed: %v", err)
			}
			var temporal Node
			for _, n := range res.Trace.Nodes {
				if n.StageID == StageTemporal {
					temporal = n
				}
			}
			if temporal.Status != StatusSkipped ||
				temporal.Detail != "no temporal observation series supplied for this case" {
				t.Fatalf("expected TEMPORAL_BAYESIAN to stay SKIPPED for %s, got status=%s detail=%q",
					c.name, temporal.Status, temporal.Detail)
			}
		})
	}
}

func TestDependencyEvaluationPrecedesFusionInTheRecordedTrace(t *testing.T) {
	_, res := run(t)
	depIdx, fusionIdx := -1, -1
	for i, n := range res.Trace.Nodes {
		switch n.StageID {
		case StageDependencyEvaluation:
			depIdx = i
		case StageFusion:
			fusionIdx = i
		}
	}
	if depIdx < 0 || fusionIdx < 0 || depIdx > fusionIdx {
		t.Fatalf("dependency evaluation must be recorded before fusion: %d vs %d", depIdx, fusionIdx)
	}
}

func TestExplanationChainRunsFromSourceToDecision(t *testing.T) {
	_, res := run(t)
	// P0-01: the explanation the engine returns is the exact artifact
	// emitted by production execution, already bound to the execution
	// root via the two-phase commitment (explanation.Commit). It must
	// pass full verification, not just render.
	if err := explanation.VerifyFinal(res.Explanation); err != nil {
		t.Fatalf("the emitted explanation must verify against its own commitment: %v", err)
	}
	stages := map[explanation.Stage]bool{}
	for _, l := range res.Explanation.Chain {
		stages[l.Stage] = true
	}
	for _, want := range []explanation.Stage{
		explanation.StageSource, explanation.StageEvidence, explanation.StageDependency,
		explanation.StageWeight, explanation.StageTruth, explanation.StageFusion,
		explanation.StageRisk, explanation.StagePolicy, explanation.StageDecision,
	} {
		if !stages[want] {
			t.Fatalf("explanation chain missing %s", want)
		}
	}
	if len(res.Explanation.Trace()) == 0 {
		t.Fatal("the explanation must render a trace")
	}
	if res.Explanation.Why() == "" {
		t.Fatal("the explanation must answer why in one line")
	}
}

func TestSharedProviderIsVisibleInTheDependencyExplanation(t *testing.T) {
	_, res := run(t)
	found := false
	for _, l := range res.Explanation.DependencyExplanation.Lines {
		if contains(l, "ais-vendor-a") && contains(l, "shared fraction") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the shared-provider discount must appear in the explanation: %v",
			res.Explanation.DependencyExplanation.Lines)
	}
}

func TestIdenticalInputProducesIdenticalRootHash(t *testing.T) {
	a := NewEngine(nil)
	ra, err := a.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine(nil)
	rb, err := b.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if ra.ExecutionRootHash != rb.ExecutionRootHash {
		t.Fatalf("two identical executions must agree: %s vs %s",
			ra.ExecutionRootHash, rb.ExecutionRootHash)
	}
}

func TestContextChangeChangesTheRootHash(t *testing.T) {
	_, base := run(t)
	for name, mutate := range map[string]func(*Context){
		"tenant":     func(c *Context) { c.Tenant = "other" },
		"policy":     func(c *Context) { c.PolicyVersion = "sanctions-screen@4" },
		"identity":   func(c *Context) { c.IdentityResolutionVersion = "ident@3" },
		"ledger_pos": func(c *Context) { c.LedgerPosition = 7 },
		"models":     func(c *Context) { c.ModelVersions = []string{"risk@9"} },
		"sources":    func(c *Context) { c.SourceVersions = []string{"ais@9"} },
	} {
		c := ctx()
		mutate(&c)
		e := NewEngine(nil)
		got, err := e.Run(context.Background(), Input{Context: c, Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.ExecutionRootHash == base.ExecutionRootHash {
			t.Fatalf("changing %s must change the execution root hash", name)
		}
	}
}

func nodeFor(t *testing.T, res *Result, id StageID) Node {
	t.Helper()
	for _, n := range res.Trace.Nodes {
		if n.StageID == id {
			return n
		}
	}
	t.Fatalf("no node found for stage %s", id)
	return Node{}
}

// TestIdentityResolutionUnaffectedWhenIdentityNil proves the P0-D
// additive change's core promise: the default (Identity left nil, what
// every existing production/test construction site does today,
// including this file's own dozens of NewEngine(nil) calls) is
// deterministic and requires no changed expectations anywhere --
// TestColdReplay_CrossProcess_MatchesOriginal independently proves the
// SAME nil-Identity path reproduces the exact fixed evidence root it
// always has, at the whole-DAG, cross-process level.
func TestIdentityResolutionUnaffectedWhenIdentityNil(t *testing.T) {
	e := NewEngine(nil)
	if e.Identity != nil {
		t.Fatal("expected Identity to default to nil")
	}
	res1, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	res2, err := NewEngine(nil).Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if res1.ExecutionRootHash != res2.ExecutionRootHash {
		t.Fatal("two nil-Identity engines given identical input must produce identical execution root hashes")
	}
	n := nodeFor(t, res1, StageIdentityResolution)
	if n.Detail != "identity resolution version "+ctx().IdentityResolutionVersion {
		t.Fatalf("nil Identity must produce the exact pre-P0-D detail string with no ledger-head suffix, got %q", n.Detail)
	}
}

// TestIdentityResolutionBindsToIdentityLedgerHeadWhenSet is the property
// that makes this a REAL commitment, not cosmetic: setting Identity
// changes the stage's hash (proving the ledger head genuinely enters the
// computation), and a DIFFERENT ledger state (even an empty resolver
// with a still-fresh internal state vs one with real ledger entries)
// produces a DIFFERENT hash -- an execution replayed against a resolver
// whose ledger has since diverged is detectably not the same execution.
func TestIdentityResolutionBindsToIdentityLedgerHeadWhenSet(t *testing.T) {
	baseline := NewEngine(nil)
	baseRes, err := baseline.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	baseNode := nodeFor(t, baseRes, StageIdentityResolution)

	emptyResolver := identity.NewResolver()
	e1 := NewEngine(nil)
	e1.Identity = emptyResolver
	res1, err := e1.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	node1 := nodeFor(t, res1, StageIdentityResolution)
	if node1.Hash == baseNode.Hash {
		t.Fatal("setting Identity (even with an empty ledger) must change the IDENTITY_RESOLUTION hash versus nil Identity")
	}

	populatedResolver := identity.NewResolver()
	if err := populatedResolver.RegisterAuthority(identity.Authority{SourceID: "test-authority", Weight: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := populatedResolver.Merge("analyst-1", "test-authority",
		identity.Identifier{Kind: identity.KindIMO, Value: "9998887"},
		identity.Identifier{Kind: identity.KindCallsign, Value: "ABCD1"}, 1, "test merge"); err != nil {
		t.Fatal(err)
	}
	e2 := NewEngine(nil)
	e2.Identity = populatedResolver
	res2, err := e2.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	node2 := nodeFor(t, res2, StageIdentityResolution)
	if node2.Hash == node1.Hash {
		t.Fatal("a resolver with real ledger entries must produce a different IDENTITY_RESOLUTION hash than an empty one")
	}
	if res2.ExecutionRootHash == baseRes.ExecutionRootHash {
		t.Fatal("binding to a real identity ledger state must change the whole-execution root hash too")
	}
}

// TestIdentityResolutionBindingIsDeterministic proves two independently
// built engines sharing the SAME identity ledger state (not the same
// resolver instance) produce identical hashes -- the binding is a pure
// function of ledger content, not incidental object identity.
func TestIdentityResolutionBindingIsDeterministic(t *testing.T) {
	newResolverWithSameState := func(t *testing.T) *identity.Resolver {
		t.Helper()
		r := identity.NewResolver()
		if err := r.RegisterAuthority(identity.Authority{SourceID: "test-authority", Weight: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Merge("analyst-1", "test-authority",
			identity.Identifier{Kind: identity.KindIMO, Value: "9998887"},
			identity.Identifier{Kind: identity.KindCallsign, Value: "ABCD1"}, 1, "test merge"); err != nil {
			t.Fatal(err)
		}
		return r
	}

	e1 := NewEngine(nil)
	e1.Identity = newResolverWithSameState(t)
	res1, err := e1.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	e2 := NewEngine(nil)
	e2.Identity = newResolverWithSameState(t)
	res2, err := e2.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if res1.ExecutionRootHash != res2.ExecutionRootHash {
		t.Fatal("two independently built resolvers with identical ledger content must produce identical execution root hashes")
	}
}

func TestReplayRebuildsTheWholeDAGAndMatches(t *testing.T) {
	_, res := run(t)
	req := ReplayRequest{Context: ctx(), Case: caseInput(), Scenarios: scenarios(),
		Currency: "USD", Committed: res.Trace}
	data, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	v, err := ReplayDAG(context.Background(), data, NewEngine(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Assert(); err != nil {
		t.Fatalf("replay must match: %v", err)
	}
	if v.NodesCompared != len(graph) {
		t.Fatalf("replay must compare every node, got %d", v.NodesCompared)
	}
}

func TestReplayLocalisesTheFirstDivergentStage(t *testing.T) {
	_, res := run(t)
	tampered := res.Trace
	tampered.Nodes = append([]Node(nil), res.Trace.Nodes...)
	// Corrupt the risk node's hash: the divergence must be reported
	// there, not merely as "the root differs".
	idx := -1
	for i, n := range tampered.Nodes {
		if n.StageID == StageRisk {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("risk stage not found")
	}
	tampered.Nodes[idx].Hash = "deadbeef"

	req := ReplayRequest{Context: ctx(), Case: caseInput(), Scenarios: scenarios(),
		Currency: "USD", Committed: tampered}
	data, _ := req.Marshal()
	v, err := ReplayDAG(context.Background(), data, NewEngine(nil))
	if err != nil {
		t.Fatal(err)
	}
	if v.Matched {
		t.Fatal("a tampered trace must not match")
	}
	if v.DivergentStage != StageRisk {
		t.Fatalf("divergence must be localised to RISK, got %s", v.DivergentStage)
	}
	if v.Assert() == nil {
		t.Fatal("Assert must convert a divergence into an error")
	}
}

func TestChangedEvidenceIsDetectedByReplay(t *testing.T) {
	_, res := run(t)
	altered := caseInput()
	altered.Submissions[0].Value = "jakarta"
	req := ReplayRequest{Context: ctx(), Case: altered, Scenarios: scenarios(),
		Currency: "USD", Committed: res.Trace}
	data, _ := req.Marshal()
	v, err := ReplayDAG(context.Background(), data, NewEngine(nil))
	if err != nil {
		t.Fatal(err)
	}
	if v.Matched {
		t.Fatal("replaying different evidence must not match")
	}
	if v.DivergentStage == "" {
		t.Fatal("the divergence must be attributed to a stage")
	}
}

func TestGovernanceBindingIsCommittedAndEnforcedAtReplay(t *testing.T) {
	reg := lifecycle.NewRegistry()
	mustActivateModel(t, reg, "risk", "1", 1)
	e := NewEngine(nil)
	e.Lifecycle = reg
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trace.Context.BindingHash == "" {
		t.Fatal("a governance binding must be committed into the context")
	}
	if len(res.Explanation.ModelVersions) == 0 {
		t.Fatal("the explanation must carry the model versions the run used")
	}

	// A replayer whose registry runs a different model version must be
	// refused before anything is recomputed.
	other := lifecycle.NewRegistry()
	mustActivateModel(t, other, "risk", "2", 1)
	oe := NewEngine(nil)
	oe.Lifecycle = other
	req := ReplayRequest{Context: res.Trace.Context, Case: caseInput(), Scenarios: scenarios(),
		Currency: "USD", Committed: res.Trace}
	data, _ := req.Marshal()
	if _, err := ReplayDAG(context.Background(), data, oe); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("replay under a different model version must be refused, got %v", err)
	}
}

func TestKnowledgeRootIsCommitted(t *testing.T) {
	k := knowledge.New()
	p, err := k.Propose("analyst", "raise ais weight", []string{"e1"},
		[]knowledge.Mutation{{Kind: knowledge.ChangeWeight, Target: "ais_weight", Value: 0.7}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = k.Analyze(p.ID, 1)
	_, _ = k.Simulate(p.ID, knowledge.SimulationResult{SampleSize: 10, ChangedOutcomes: 1}, 1)
	_, _ = k.Approve(p.ID, "officer", 1)
	_, _ = k.Activate(p.ID, 2, 2)

	e := NewEngine(nil)
	e.Knowledge = k
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trace.Context.KnowledgeRoot == "" {
		t.Fatal("the knowledge root must be committed into the execution context")
	}
	if res.Explanation.KnowledgeRoot != res.Trace.Context.KnowledgeRoot {
		t.Fatal("the explanation must commit to the same knowledge root")
	}
}

func TestFailedCoreFailsEveryStageAndEmitsNoCertificate(t *testing.T) {
	e := NewEngine(nil)
	empty := caseInput()
	empty.Submissions = nil
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: empty})
	if err == nil {
		t.Fatal("an empty case must fail")
	}
	if !errors.Is(err, ErrStageFailed) {
		t.Fatalf("failure must name a stage, got %v", err)
	}
	if res.Certificate.VerificationCertificateID != "" {
		t.Fatal("no certificate may be emitted over a failed execution")
	}
	for _, n := range res.Trace.Nodes {
		if n.Status != StatusFailed {
			t.Fatalf("stage %s should be FAILED, got %s", n.StageID, n.Status)
		}
	}
}

func TestDecisionMatchesTheCanonicalDecision(t *testing.T) {
	_, res := run(t)
	if res.Decision != string(res.Canonical.Decision.Action) {
		t.Fatalf("the recorded decision must be the canonical one: %s vs %s",
			res.Decision, res.Canonical.Decision.Action)
	}
	if res.Decision != string(decision.ActionMonitor) &&
		res.Decision != string(decision.ActionFlag) &&
		res.Decision != string(decision.ActionEscalate) {
		t.Fatalf("unexpected decision action %q", res.Decision)
	}
}

// TestDecisionIDIsIndependentlyDerivedNotAliasedFromExecutionID is
// P0-7's foundational property, checked here at the execution layer
// directly (pkg/platform/correlation checks the same thing one layer
// up, over the packaged Key). DecisionID must be non-empty, must NOT
// equal ExecutionID, and must be a real function of the decision's own
// content: a case that produces a genuinely different decision gets a
// genuinely different DecisionID under an identical ExecutionID.
func TestDecisionIDIsIndependentlyDerivedNotAliasedFromExecutionID(t *testing.T) {
	_, res := run(t)
	if res.Explanation.DecisionID == "" {
		t.Fatal("expected a non-empty DecisionID")
	}
	if res.Explanation.DecisionID == res.Trace.Context.ExecutionID {
		t.Fatalf("expected DecisionID to be independently derived, got it equal to ExecutionID %q",
			res.Trace.Context.ExecutionID)
	}

	e := NewEngine(nil)
	c := caseInput()
	c.PatternScore = 0.99 // same ExecutionID, deliberately different risk-driving content
	res2, err := e.Run(context.Background(), Input{Context: ctx(), Case: c, Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res2.Explanation.DecisionID == res.Explanation.DecisionID {
		t.Fatal("expected a genuinely different case under the SAME ExecutionID to change DecisionID")
	}
}

func mustActivateModel(t *testing.T, r *lifecycle.Registry, id, ver string, tick uint64) {
	t.Helper()
	if _, err := r.RegisterModel(lifecycle.Model{ModelID: id, Version: ver, Type: "risk"}, "eng", tick); err != nil {
		t.Fatal(err)
	}
	key := id + "@" + ver
	if _, err := r.TransitionModel(key, lifecycle.ModelValidated, "eng", "", "", tick, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCalibration(key, "cal-"+ver); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionModel(key, lifecycle.ModelCalibrated, "eng", "", "", tick, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionModel(key, lifecycle.ModelApproved, "eng", "officer", "", tick, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionModel(key, lifecycle.ModelActive, "eng", "officer", "", tick, nil); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestPolicyStageIndependentlyVerifiesExpectedHash is P0-D's positive
// property for POLICY: when the caller declares what policy content
// they expect, the stage independently recomputes decision.Policy.Hash
// over the ACTUAL policy that ran and requires agreement -- proof this
// is a real check, not a decorative re-statement of Context.PolicyVersion
// (which, before this, was never cross-checked against Case.Policy at
// all).
func TestPolicyStageIndependentlyVerifiesExpectedHash(t *testing.T) {
	c := caseInput()
	e := NewEngine(nil)
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: c, Scenarios: scenarios(),
		Currency: "USD", ExpectedPolicyHash: c.Policy.Hash()})
	if err != nil {
		t.Fatalf("expected a matching expected policy hash to succeed, got: %v", err)
	}
	var polNode Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StagePolicy {
			polNode = n
		}
	}
	if polNode.Status != StatusOK {
		t.Fatalf("expected POLICY to succeed, got %s: %s", polNode.Status, polNode.Error)
	}
	if !strings.Contains(polNode.Detail, "independently verified policy hash") {
		t.Fatalf("expected the node detail to record the independent verification, got %q", polNode.Detail)
	}
}

// TestPolicyStageFailsClosedWhenExpectedHashDisagrees is P0-D's
// adversarial property for POLICY: a Case.Policy whose real content
// does not match the caller-declared ExpectedPolicyHash must fail the
// stage (and the whole execution), not silently proceed under
// Context.PolicyVersion's unverified label. This is exactly the
// "policy version" analogue of ErrIdentityMismatch, and is the concrete
// fix for the auditor finding that Policy was "registry entry, not a
// real DAG node": before ExpectedPolicyHash existed, nothing could ever
// make POLICY fail this way, because nothing compared the declared
// version to the policy's actual content.
func TestPolicyStageFailsClosedWhenExpectedHashDisagrees(t *testing.T) {
	c := caseInput()
	e := NewEngine(nil)
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: c, Scenarios: scenarios(),
		Currency: "USD", ExpectedPolicyHash: "sha256-of-a-totally-different-policy-that-was-never-run"})
	if !errors.Is(err, ErrStageFailed) {
		t.Fatalf("expected ErrStageFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), string(StagePolicy)) {
		t.Fatalf("expected the error to name POLICY, got %v", err)
	}
	var polNode Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StagePolicy {
			polNode = n
		}
	}
	if polNode.Status != StatusFailed {
		t.Fatalf("expected POLICY to FAIL on disagreement, got %s", polNode.Status)
	}
	if !strings.Contains(polNode.Error, ErrPolicyMismatch.Error()) {
		t.Fatalf("expected the node's own error to name the policy mismatch, got %q", polNode.Error)
	}
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageDecision && n.Status == StatusOK {
			t.Fatal("DECISION must not succeed when POLICY failed")
		}
	}
}

// TestPolicyStageWithoutExpectedHashPreservesPriorBehavior proves
// nil-safety: omitting ExpectedPolicyHash (every existing production/
// test call site before this round) must not change behavior at all.
func TestPolicyStageWithoutExpectedHashPreservesPriorBehavior(t *testing.T) {
	e := NewEngine(nil)
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: caseInput(),
		Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	var polNode Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StagePolicy {
			polNode = n
		}
	}
	if polNode.Status != StatusOK {
		t.Fatalf("expected POLICY to succeed without ExpectedPolicyHash, got %s", polNode.Status)
	}
	if strings.Contains(polNode.Detail, "independently verified") {
		t.Fatal("expected no independent-verification claim when ExpectedPolicyHash was not supplied")
	}
}

// TestPolicyHashIsDeterministicAndOrderIndependent proves Policy.Hash
// is a genuine content address: identical content in a different
// Factors order hashes the same, and any real content change (a
// threshold, a weight, a factor name) changes the hash.
func TestPolicyHashIsDeterministicAndOrderIndependent(t *testing.T) {
	p1 := decision.Policy{Name: "p", FlagThreshold: 0.5, EscalateThreshold: 0.8,
		Factors: []decision.FactorWeight{{Name: "a", Weight: 1}, {Name: "b", Weight: 2}}}
	p2 := decision.Policy{Name: "p", FlagThreshold: 0.5, EscalateThreshold: 0.8,
		Factors: []decision.FactorWeight{{Name: "b", Weight: 2}, {Name: "a", Weight: 1}}}
	if p1.Hash() != p2.Hash() {
		t.Fatal("expected reordered-but-identical factors to hash the same")
	}
	p3 := p1
	p3.FlagThreshold = 0.6
	if p3.Hash() == p1.Hash() {
		t.Fatal("expected a changed threshold to change the hash")
	}
}

// TestTemporalStageFailsClosedWhenPolicyRequiresCalibration is P0-D
// Test 2's exact acceptance criterion: a policy that declares
// RequiresTemporalCalibration must not get a silent SKIPPED pass when
// no temporal model/observations were supplied -- the stage fails
// closed, and the failure cascades to every downstream stage (CAUSAL
// depends on TEMPORAL_BAYESIAN in the declared graph) rather than
// fabricating a decision that never really reasoned temporally.
func TestTemporalStageFailsClosedWhenPolicyRequiresCalibration(t *testing.T) {
	c := caseInput()
	c.Policy.RequiresTemporalCalibration = true
	e := NewEngine(nil)
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: c, Scenarios: scenarios(), Currency: "USD"})
	if !errors.Is(err, ErrStageFailed) {
		t.Fatalf("expected ErrStageFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), string(StageTemporal)) {
		t.Fatalf("expected the error to name TEMPORAL_BAYESIAN, got %v", err)
	}
	var temporalNode, decisionNode Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageTemporal {
			temporalNode = n
		}
		if n.StageID == StageDecision {
			decisionNode = n
		}
	}
	if temporalNode.Status != StatusFailed {
		t.Fatalf("expected TEMPORAL_BAYESIAN to FAIL when the policy requires calibration, got %s", temporalNode.Status)
	}
	if !strings.Contains(temporalNode.Error, ErrTemporalCalibrationRequired.Error()) {
		t.Fatalf("expected the node's own error to name the missing calibration, got %q", temporalNode.Error)
	}
	if decisionNode.Status == StatusOK {
		t.Fatal("DECISION must not succeed when a required TEMPORAL_BAYESIAN stage failed")
	}
}

// TestTemporalStageStillSkipsWhenPolicyDoesNotRequireCalibration proves
// the opt-in nature of RequiresTemporalCalibration: policies that never
// set it (every existing policy before this round, including
// risk.DefaultPolicy) keep the exact prior honest-SKIPPED behavior.
func TestTemporalStageStillSkipsWhenPolicyDoesNotRequireCalibration(t *testing.T) {
	c := caseInput()
	if c.Policy.RequiresTemporalCalibration {
		t.Fatal("test fixture assumption violated: default policy must not require temporal calibration")
	}
	e := NewEngine(nil)
	res, err := e.Run(context.Background(), Input{Context: ctx(), Case: c, Scenarios: scenarios(), Currency: "USD"})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageTemporal && n.Status != StatusSkipped {
			t.Fatalf("expected TEMPORAL_BAYESIAN to remain SKIPPED, got %s", n.Status)
		}
	}
}
