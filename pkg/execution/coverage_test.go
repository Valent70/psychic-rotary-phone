package execution

// PHASE E5 (P1-17) — architecture coverage, the two clauses this
// package owns.
//
// The program asks for six machine-readable coverage gates. Four of
// them are whole-tree source scans and live where the scanner lives:
//
//	canonical_evidence_write_coverage      -> internal/nobypass
//	canonical_identity_authority_coverage  -> pkg/governance/entityconsistency
//	canonical_execution_entrypoint_coverage-> internal/entrypoints
//	correlation_propagation_coverage       -> pkg/platform/correlation
//
// The remaining two — policy_registry_usage_coverage and
// temporal_calibration_usage_coverage — are NOT source-scannable
// properties. "Every governed decision commits to the policy that
// actually governed it" and "a policy that requires temporal
// calibration never gets a decision without it" are RUNTIME facts
// about the DAG. Grepping for them would prove only that certain text
// appears in certain files, which is exactly the kind of evidence this
// project refuses to accept elsewhere.
//
// So they are proved here, by running the real engine, in tests whose
// names cmd/veriqo-readiness invokes directly as its gate command.
// That reuses the established `go test -run <Name>` gate pattern the
// readiness pipeline already uses (see its dependency_integration
// gate) rather than inventing a third gate mechanism.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/hbayes"
)

// --- policy_registry_usage_coverage ----------------------------------

// TestPolicyRegistryUsageCoverageEveryDecisionCommitsToItsPolicy is the
// affirmative half: a real execution's POLICY node must attribute the
// exact policy that governed it, and the decision must be downstream of
// that node rather than beside it.
func TestPolicyRegistryUsageCoverageEveryDecisionCommitsToItsPolicy(t *testing.T) {
	in := coverageInput()
	policyHash := in.Case.Policy.Hash()
	if policyHash == "" {
		t.Fatal("the test policy has no hash; the coverage property would be vacuous")
	}

	res, err := NewEngine(nil).Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var policyNode, decisionNode Node
	var policySeen, decisionSeen bool
	policyIndex, decisionIndex := -1, -1
	for i, n := range res.Trace.Nodes {
		switch n.StageID {
		case StagePolicy:
			policyNode, policySeen, policyIndex = n, true, i
		case StageDecision:
			decisionNode, decisionSeen, decisionIndex = n, true, i
		}
	}
	if !policySeen {
		t.Fatal("the execution trace has no POLICY node -- a governed decision with no policy attribution")
	}
	if !decisionSeen {
		t.Fatal("the execution trace has no DECISION node")
	}
	if policyIndex >= decisionIndex {
		t.Fatalf("POLICY node is at %d and DECISION at %d -- the decision must be downstream of the policy that governed it",
			policyIndex, decisionIndex)
	}
	if policyNode.Hash == "" || decisionNode.Hash == "" {
		t.Fatal("policy or decision node carries no hash")
	}

	// The policy is genuinely load-bearing: a DIFFERENT policy must
	// move the POLICY node's hash. Without this, the node could be
	// attributing a constant.
	other := in
	otherPolicy := in.Case.Policy
	otherPolicy.Name = in.Case.Policy.Name + "-variant"
	otherPolicy.FlagThreshold = in.Case.Policy.FlagThreshold / 2
	other.Case.Policy = otherPolicy
	otherRes, err := NewEngine(nil).Run(context.Background(), other)
	if err != nil {
		t.Fatalf("Run (variant policy): %v", err)
	}
	for _, n := range otherRes.Trace.Nodes {
		if n.StageID == StagePolicy && n.Hash == policyNode.Hash {
			t.Fatal("a genuinely different policy produced an identical POLICY node hash -- the node is not committing to the policy")
		}
	}
}

// TestPolicyRegistryUsageCoverageRefusesAMismatchedPolicy is the
// fail-closed half: when a caller declares which policy it expects, the
// POLICY stage independently recomputes the hash of the policy that
// will ACTUALLY run and refuses a mismatch, rather than trusting the
// caller's label.
func TestPolicyRegistryUsageCoverageRefusesAMismatchedPolicy(t *testing.T) {
	in := coverageInput()
	in.ExpectedPolicyHash = "sha256:a-policy-that-is-not-the-one-in-this-case"

	_, err := NewEngine(nil).Run(context.Background(), in)
	if err == nil {
		t.Fatal("an execution ran under a policy whose hash did not match the one the caller declared")
	}
	if !errors.Is(err, ErrPolicyMismatch) && !errors.Is(err, ErrStageFailed) {
		t.Fatalf("err = %v, want a policy mismatch", err)
	}
	if !strings.Contains(err.Error(), "POLICY") && !errors.Is(err, ErrPolicyMismatch) {
		t.Errorf("the refusal does not localise to the POLICY stage: %v", err)
	}
}

// TestPolicyRegistryUsageCoverageAcceptsTheMatchingPolicy is the
// control: the check above must not simply reject everything.
func TestPolicyRegistryUsageCoverageAcceptsTheMatchingPolicy(t *testing.T) {
	in := coverageInput()
	in.ExpectedPolicyHash = in.Case.Policy.Hash()

	if _, err := NewEngine(nil).Run(context.Background(), in); err != nil {
		t.Fatalf("an execution declaring its own real policy hash was refused: %v", err)
	}
}

// --- temporal_calibration_usage_coverage -----------------------------

// TestTemporalCalibrationUsageCoverageFailsClosedWhenRequiredAndAbsent
// is the property PHASE F states and this gate measures: a policy that
// declares temporal reasoning load-bearing never yields a decision
// reached without it.
func TestTemporalCalibrationUsageCoverageFailsClosedWhenRequiredAndAbsent(t *testing.T) {
	in := coverageInput()
	p := in.Case.Policy
	p.RequiresTemporalCalibration = true
	in.Case.Policy = p
	// Deliberately supply neither a model nor an observation series.
	in.TemporalModel = nil
	in.TemporalObservations = nil

	_, err := NewEngine(nil).Run(context.Background(), in)
	if err == nil {
		t.Fatal("a policy requiring temporal calibration produced a decision with no calibration at all")
	}
	if !errors.Is(err, ErrTemporalCalibrationRequired) && !errors.Is(err, ErrStageFailed) {
		t.Fatalf("err = %v, want ErrTemporalCalibrationRequired", err)
	}
}

// TestTemporalCalibrationUsageCoverageSkipsHonestlyWhenNotRequired is
// the other side of the same rule: an optional skip is a legitimate,
// unremarkable outcome and must not be turned into a failure.
func TestTemporalCalibrationUsageCoverageSkipsHonestlyWhenNotRequired(t *testing.T) {
	in := coverageInput() // default policy: RequiresTemporalCalibration is false

	res, err := NewEngine(nil).Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var found bool
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageTemporal {
			found = true
			if n.Status == "" {
				t.Fatal("the TEMPORAL_BAYESIAN node records no status at all -- a silent skip")
			}
		}
	}
	if !found {
		t.Fatal("the trace has no TEMPORAL_BAYESIAN node; a skip must still be recorded as a node, not omitted")
	}
}

// TestTemporalCalibrationUsageCoverageExecutesWhenSupplied proves the
// required-and-present path really runs the model rather than reporting
// a skip under a different name.
func TestTemporalCalibrationUsageCoverageExecutesWhenSupplied(t *testing.T) {
	in := coverageInput()
	p := in.Case.Policy
	p.RequiresTemporalCalibration = true
	in.Case.Policy = p

	states := []hbayes.State{"DARK", "NORMAL"}
	tr := hbayes.Transition{States: states, P: map[hbayes.State]map[hbayes.State]float64{
		"DARK":   {"DARK": 0.7, "NORMAL": 0.3},
		"NORMAL": {"DARK": 0.1, "NORMAL": 0.9},
	}}
	model, err := hbayes.NewTemporalModel(states, tr,
		map[hbayes.State]float64{"DARK": 0.2, "NORMAL": 0.8}, nil, 0)
	if err != nil {
		t.Fatalf("NewTemporalModel: %v", err)
	}
	in.TemporalModel = model
	in.TemporalObservations = []hbayes.TickObservations{
		{Tick: 1, Observations: []hbayes.Observation{
			{SourceID: "ais-vendor-a", Tick: 1, Likelihood: map[hbayes.State]float64{"DARK": 0.9, "NORMAL": 0.1}},
		}},
	}

	res, err := NewEngine(nil).Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run with a real temporal model: %v", err)
	}
	var temporal Node
	for _, n := range res.Trace.Nodes {
		if n.StageID == StageTemporal {
			temporal = n
		}
	}
	if temporal.Hash == "" {
		t.Fatal("the TEMPORAL_BAYESIAN node produced no hash when a real model was supplied")
	}

	// A different observation series must move the node hash, proving
	// the stage consumed the observations rather than attributing a
	// constant.
	other := in
	other.TemporalObservations = []hbayes.TickObservations{
		{Tick: 1, Observations: []hbayes.Observation{
			{SourceID: "ais-vendor-a", Tick: 1, Likelihood: map[hbayes.State]float64{"DARK": 0.1, "NORMAL": 0.9}},
		}},
	}
	otherRes, err := NewEngine(nil).Run(context.Background(), other)
	if err != nil {
		t.Fatalf("Run (variant observations): %v", err)
	}
	for _, n := range otherRes.Trace.Nodes {
		if n.StageID == StageTemporal && n.Hash == temporal.Hash {
			t.Fatal("a genuinely different observation series produced an identical TEMPORAL_BAYESIAN hash")
		}
	}
}

// coverageInput is this file's single, obviously-real execution input,
// built from the SAME ctx()/caseInput()/scenarios() fixtures the rest
// of this package's tests use rather than a parallel set.
func coverageInput() Input {
	return Input{Context: ctx(), Case: caseInput(), Scenarios: scenarios(), Currency: "USD"}
}

// assertPolicyIsReal keeps the coverage tests honest about their own
// fixture: a policy with no factors would make "the decision commits to
// its policy" true but meaningless.
func assertPolicyIsReal(t *testing.T, p decision.Policy) {
	t.Helper()
	if p.Name == "" || len(p.Factors) == 0 {
		t.Fatalf("coverage fixture policy is not a real policy: %+v", p)
	}
}
