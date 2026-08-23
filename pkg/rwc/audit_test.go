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

// TestAuditTrustNowParticipatesInEveryRWCDecision is the RE-DERIVED
// form of the R23 audit's section-1 finding.
//
// WHAT R23 FOUND, verbatim from the test this replaces: "canonical.
// RunCanonical never reads Pipeline.Trust, and pkg/lifecycle never sets
// execution.Input.TrustSubject, so the DAG's TRUST_STATE stage records
// SKIPPED for every case in the corpus." That test asserted
// k.TrustLedger.Len() == 0 and passed, which is what made the audit's
// "trust state NOT PROVEN" answer true at the time.
//
// The canonical-truth-path round (WAVE A item 2) closed exactly that
// gap, so this test now pins the OPPOSITE fact, with the same
// discipline: every clause below is something that was demonstrably
// false in R23 and must not silently become false again.
func TestAuditTrustNowParticipatesInEveryRWCDecision(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown

	req, _, err := BuildRWC001Case(RWC001Candidates()[1], 1)
	if err != nil {
		t.Fatalf("BuildRWC001Case: %v", err)
	}
	res, err := Run(context.Background(), k, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 1. The shared trust Calculus is genuinely written by a canonical
	//    run (R23: exactly 0 observations).
	if n := k.TrustLedger.Len(); n == 0 {
		t.Error("the shared trust calculus recorded nothing for an RWC case; " +
			"canonical.RunCanonical is not reading/writing Pipeline.Trust again")
	}

	// 2. The trust evaluation is a real artifact of the case, with a
	//    named policy and a ledger head (R23: no such artifact existed).
	tr := res.Lifecycle.Canonical.Trust
	if !tr.Configured {
		t.Fatal("the canonical pipeline reports no trust engine attached")
	}
	if tr.RootHash == "" || tr.PolicyHash == "" || tr.LedgerHead == "" {
		t.Fatalf("trust evaluation is missing its commitments: root=%q policy=%q head=%q",
			tr.RootHash, tr.PolicyHash, tr.LedgerHead)
	}
	if len(tr.Sources) != len(req.Submissions) {
		t.Errorf("trust evaluated %d sources for a case with %d submissions",
			len(tr.Sources), len(req.Submissions))
	}

	// 3. The trust commitment is inside the certificate chain (R23: the
	//    certificate had no trust field at all).
	if res.Lifecycle.Canonical.Certificate.TrustRootHash != tr.RootHash {
		t.Error("the canonical certificate does not commit to the trust evaluation that gated the case")
	}

	// 4. TRUST_STATE is a real, OK DAG node placed BEFORE the decision
	//    (R23: SKIPPED, and declared downstream of DECISION).
	var trustIdx, decisionIdx = -1, -1
	for i, n := range res.Lifecycle.Execution.Trace.Nodes {
		switch n.StageID {
		case "TRUST_STATE":
			trustIdx = i
			if n.Status != "OK" {
				t.Errorf("TRUST_STATE recorded %s for a real RWC case", n.Status)
			}
		case "DECISION":
			decisionIdx = i
		}
	}
	if trustIdx < 0 || decisionIdx < 0 {
		t.Fatalf("TRUST_STATE (%d) or DECISION (%d) missing from the trace", trustIdx, decisionIdx)
	}
	if trustIdx > decisionIdx {
		t.Error("TRUST_STATE is still recorded after DECISION; trust cannot gate a decision it follows")
	}

	// 5. Every RWC provider is a never-assessed subject, so the honest
	//    outcome is RESTRICTED with mandatory review — not a silent pass.
	if !res.Lifecycle.HumanReviewRequired {
		t.Error("no RWC provider has ever been trust-assessed, yet the run did not require human review")
	}
	if len(res.Lifecycle.TrustReviewReasons) == 0 {
		t.Error("human review was required with no reason given")
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
