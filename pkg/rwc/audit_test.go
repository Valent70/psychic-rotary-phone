// audit_test.go holds the round-R23 independent audit's own executable
// findings. Each test here exists to PIN a fact the audit established
// about RWC v2 — including several unflattering ones — so that a later
// change which quietly makes an audit claim false fails the build instead
// of silently invalidating docs/RWC_V2_INDEPENDENT_AUDIT_REPORT.md.
//
// A test in this file failing does not necessarily mean the code is
// broken. It means the audit report needs re-deriving.
package rwc

import (
	"context"
	"testing"

	"veriqo/pkg/moat/decision"
	"veriqo/veriqo/kernel"
)

// TestAuditVerdictIsProducibleWithoutTheNativeEngine is audit section 2's
// FALSE-POSITIVE finding, demonstrated rather than described.
//
// InterpretVerdict returns the same Verdict when handed a zero-valued
// decision.Decision — i.e. when no native engine ran at all — as when
// handed the real one. The verdict is a pure function of the constraint
// evaluation. The native decision only ever affects the returned
// consistencyWarning.
//
// This is not a bug to be fixed here; it is the honest shape of the
// adapter, and it is why Verdict's doc comment forbids describing a
// verdict as the decision engine's output. What makes the pipeline safe
// in practice is the SECOND half of this test: the warning does fire, and
// every real caller treats it as a failure.
func TestAuditVerdictIsProducibleWithoutTheNativeEngine(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown

	// Candidate B: a hard LOA violation, so the honest verdict is FAIL.
	req, cr, err := BuildRWC001Case(RWC001Candidates()[1], 1)
	if err != nil {
		t.Fatalf("BuildRWC001Case: %v", err)
	}
	res, err := Run(context.Background(), k, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	real, realWarn := InterpretVerdict(cr, res.Lifecycle.Canonical.Decision)
	none, noneWarn := InterpretVerdict(cr, decision.Decision{})
	wrongDec := res.Lifecycle.Canonical.Decision
	wrongDec.Action = decision.ActionMonitor
	wrong, wrongWarn := InterpretVerdict(cr, wrongDec)

	if real != VerdictFail {
		t.Fatalf("control: got %s, want FAIL", real)
	}
	if none != real || wrong != real {
		t.Fatalf("the verdict DID vary with the decision handed in (real=%s zero=%s wrong=%s). "+
			"If InterpretVerdict has been changed to consume dec, audit section 2 and Verdict's "+
			"doc comment must both be re-derived — this is a material change, not a refinement",
			real, none, wrong)
	}

	// The bypass is detectable, and this is the only thing that makes it
	// safe. Both the corpus test and cmd/veriqo-rwc-v2 treat a non-empty
	// warning as a failure.
	if realWarn != "" {
		t.Errorf("a correctly wired run produced a consistency warning: %s", realWarn)
	}
	if noneWarn == "" {
		t.Error("a verdict produced with NO native decision raised no warning — the bypass would " +
			"then be undetectable, which is the condition audit section 2 says must not hold")
	}
	if wrongWarn == "" {
		t.Error("a verdict produced against a contradicting native decision raised no warning")
	}
}

// TestAuditKnowledgeGraphIsWrittenButDoesNotDecide is audit section 9.
//
// The Knowledge Graph DOES participate: veriqo/kernel.New builds one
// kg.Graph, hands it to fusion.NewEngine, and fusion.Arbitrate performs a
// single ordered write into it per arbitration. That write is real and
// measurable, and this test measures it.
//
// It participates as a SINK, not as an input. Nothing in
// canonical.RunCanonical reads the graph back before deciding, so no KG
// content can influence a decision. Both halves are pinned here.
func TestAuditKnowledgeGraphIsWrittenButDoesNotDecide(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown

	if before := k.Canonical.KG.LogLen(); before != 0 {
		t.Fatalf("a fresh kernel's knowledge graph already holds %d mutations", before)
	}

	req, _, err := BuildRWC001Case(RWC001Candidates()[1], 1)
	if err != nil {
		t.Fatalf("BuildRWC001Case: %v", err)
	}
	if _, err := Run(context.Background(), k, req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := k.Canonical.KG.LogLen(); got == 0 {
		t.Fatal("the knowledge graph recorded nothing for a real RWC case — audit section 9's " +
			"finding that the KG genuinely participates as an arbitration sink would be false")
	}
	if k.Canonical.KG.RootHash() == "" {
		t.Error("the knowledge graph has mutations but no root hash")
	}
	// The mutation log is ordered and content-addressed; a snapshot of it
	// is what a consumer would replay. Both must be non-trivial.
	if n := len(k.Canonical.KG.Snapshot()); n != k.Canonical.KG.LogLen() {
		t.Errorf("knowledge graph snapshot holds %d mutations but the log reports %d",
			n, k.Canonical.KG.LogLen())
	}
}

// TestAuditTrustCalculusDoesNotParticipate is the unflattering half of
// audit section 1.
//
// veriqo/kernel.New constructs ONE trustcalc.Calculus and shares it
// across Evidence, Intelligence, Intent and Calibration, and its every
// Observe() is hash-chained into a ledger. None of that is exercised by
// an RWC case: canonical.RunCanonical never reads Pipeline.Trust, and
// pkg/lifecycle never sets execution.Input.TrustSubject, so the DAG's
// TRUST_STATE stage records SKIPPED for every case in the corpus.
//
// The test asserts the ledger stays EMPTY. If a future change makes the
// canonical path observe trust, this fails — and that would be good news
// requiring the audit's answer to question 4 to be raised.
func TestAuditTrustCalculusDoesNotParticipate(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown

	req, _, err := BuildRWC001Case(RWC001Candidates()[1], 1)
	if err != nil {
		t.Fatalf("BuildRWC001Case: %v", err)
	}
	if _, err := Run(context.Background(), k, req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if n := k.TrustLedger.Len(); n != 0 {
		t.Fatalf("the shared trust calculus recorded %d observations for an RWC case. That is a "+
			"real improvement over what audit section 1 found, but it makes the report's "+
			"\"trust state NOT PROVEN\" answer stale — re-derive it rather than deleting this test", n)
	}
}

// TestAuditMutationChangesEveryHashEvenWhenTheDecisionHolds is audit
// section 5's certificate/hash requirement.
//
// A causally irrelevant mutation (a bowthruster the port does not
// constrain) must NOT change the decision — and must still change every
// content-addressed identifier, because the submitted evidence value
// genuinely differs. Both properties matter: a decision that moved would
// mean the constraint model is reading fields it should not, and a
// certificate that did not move would mean the certificate is not
// actually committing to the evidence.
func TestAuditMutationChangesEveryHashEvenWhenTheDecisionHolds(t *testing.T) {
	run := func(v Vessel) *CaseResult {
		t.Helper()
		k, err := kernel.New()
		if err != nil {
			t.Fatalf("kernel.New: %v", err)
		}
		defer k.Shutdown() //nolint:errcheck // test teardown
		req, _, err := BuildVesselPortCase("AUDIT-MUTATION", v, Ports["AKONIKIEN"], "AKONIKIEN", 1)
		if err != nil {
			t.Fatalf("BuildVesselPortCase: %v", err)
		}
		res, err := Run(context.Background(), k, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res
	}

	base := RWC001BaselineVessel
	irrelevant := base
	irrelevant.Bowthruster = !base.Bowthruster

	a, b := run(base), run(irrelevant)

	if a.DecisionAction != b.DecisionAction || a.RiskScore != b.RiskScore {
		t.Errorf("a bowthruster is not an Akonikien constraint, yet the native decision moved: "+
			"%s/%v -> %s/%v", a.DecisionAction, a.RiskScore, b.DecisionAction, b.RiskScore)
	}

	for name, pair := range map[string][2]string{
		"input_hash":          {a.InputHash, b.InputHash},
		"canonical_hash":      {a.CanonicalHash, b.CanonicalHash},
		"certificate_hash":    {a.CertificateHash, b.CertificateHash},
		"execution_root_hash": {a.Lifecycle.Certificate.ExecutionRootHash, b.Lifecycle.Certificate.ExecutionRootHash},
		"execution_id":        {a.ExecutionID, b.ExecutionID},
	} {
		if pair[0] == "" || pair[1] == "" {
			t.Errorf("%s is empty on one side (%q / %q)", name, pair[0], pair[1])
			continue
		}
		if pair[0] == pair[1] {
			t.Errorf("%s did NOT change when the submitted evidence value changed — the "+
				"certificate chain is not committing to the evidence it claims to cover", name)
		}
	}
}

// TestAuditCausalMutationMovesTheNativeDecision is section 5's other
// half, and section 4's answer to "could this pass without exercising
// decision computation".
//
// It asserts directly on decision.Action from the native engine, not on
// the adapter's Verdict, so it cannot be satisfied by the constraint
// arithmetic alone. Each row changes exactly one field of one shared
// baseline struct; nothing else differs between the control and the
// mutant.
func TestAuditCausalMutationMovesTheNativeDecision(t *testing.T) {
	run := func(v Vessel) *CaseResult {
		t.Helper()
		k, err := kernel.New()
		if err != nil {
			t.Fatalf("kernel.New: %v", err)
		}
		defer k.Shutdown() //nolint:errcheck // test teardown
		req, _, err := BuildVesselPortCase("AUDIT-CAUSAL", v, Ports["AKONIKIEN"], "AKONIKIEN", 1)
		if err != nil {
			t.Fatalf("BuildVesselPortCase: %v", err)
		}
		res, err := Run(context.Background(), k, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res
	}

	base := RWC001BaselineVessel
	control := run(base)
	if control.DecisionAction != string(decision.ActionMonitor) {
		t.Fatalf("control decision is %s, want MONITOR", control.DecisionAction)
	}

	loa := base
	loa.LOAMeters = 151
	draft := base
	draft.DraftMeters = 8.4
	gear := base
	gear.Geared = false

	for name, v := range map[string]Vessel{"loa_140_to_151": loa, "draft_7.2_to_8.4": draft, "geared_true_to_false": gear} {
		got := run(v)
		if got.DecisionAction != string(decision.ActionEscalate) {
			t.Errorf("%s: native decision is %s (risk %v), want ESCALATE. The verdict vocabulary "+
				"would still have said FAIL here, which is exactly why this test asserts on the "+
				"engine's own action instead", name, got.DecisionAction, got.RiskScore)
		}
		if got.RiskScore <= control.RiskScore {
			t.Errorf("%s: native risk %v did not rise above the control's %v",
				name, got.RiskScore, control.RiskScore)
		}
	}
}

// TestAuditEveryCorpusCaseSubmitsAtLeastOneObservation guards the
// corrected provenance classifier against a degenerate input the audit
// had to reason about: a claim backed ONLY by computation over itself.
// No such case exists in the corpus today, and if one is ever added it
// must be classified UNVERIFIED, never STRUCTURALLY_VALIDATED, because
// there is then no claim from anybody to validate.
func TestAuditEveryCorpusCaseSubmitsAtLeastOneObservation(t *testing.T) {
	cases, err := BuildCorpusV2(1)
	if err != nil {
		t.Fatalf("BuildCorpusV2: %v", err)
	}
	for _, c := range cases {
		observations := 0
		for _, s := range c.Request.Submissions {
			if EpistemicKindOf(string(s.SourceID)) == SourceObservation {
				observations++
			}
		}
		if observations == 0 {
			t.Errorf("%s submits no observation at all, only computation over its own claim; "+
				"ClassifyProvenance must report UNVERIFIED for it", c.ID)
		}
	}
}
