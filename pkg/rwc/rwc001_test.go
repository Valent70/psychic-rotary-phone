package rwc

import (
	"context"
	"testing"

	"veriqo/veriqo/kernel"
)

// TestRWC001AdversarialCandidates runs the brief's §4 five candidates
// through the real native path (veriqo/kernel.Kernel ->
// lifecycle.Orchestrator.RunUnified -> pkg/execution -> pkg/canonical)
// and asserts the PASS/FAIL/CONDITIONAL verdict the brief specifies for
// each. The verdict is never asserted directly by test code — it is
// produced by EvaluateVesselAtPort + InterpretVerdict from the REAL
// decision.Engine output each RunUnified call returns; this test only
// checks that the emergent result matches the brief's stated
// expectation, which is exactly what the brief's §4 final line requires
// ("The system must NOT hard-code these expected outputs. They must
// emerge from the native reasoning path.").
func TestRWC001AdversarialCandidates(t *testing.T) {
	want := map[string]Verdict{
		"A": VerdictPass,
		"B": VerdictFail,
		"C": VerdictFail,
		"D": VerdictFail,
		"E": VerdictConditional,
	}

	for _, c := range RWC001Candidates() {
		c := c
		t.Run(c.ID, func(t *testing.T) {
			k, err := kernel.New()
			if err != nil {
				t.Fatalf("kernel.New: %v", err)
			}
			defer k.Shutdown()

			req, cr, err := BuildRWC001Case(c, 1)
			if err != nil {
				t.Fatalf("BuildRWC001Case: %v", err)
			}

			res, err := Run(context.Background(), k, req)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			verdict, warn := InterpretVerdict(cr, res.Lifecycle.Canonical.Decision)
			if warn != "" {
				t.Errorf("consistency warning: %s", warn)
			}
			if verdict != want[c.ID] {
				t.Errorf("candidate %s: got verdict %s, want %s (violations=%v unresolved=%v decision_action=%s risk_score=%.4f)",
					c.ID, verdict, want[c.ID], cr.HardViolations, cr.Unresolved, res.DecisionAction, res.RiskScore)
			}

			if res.InputHash == "" || res.ExecutionID == "" || res.CanonicalHash == "" || res.CertificateHash == "" {
				t.Errorf("candidate %s: missing required hash/id: input=%q exec=%q canonical=%q cert=%q",
					c.ID, res.InputHash, res.ExecutionID, res.CanonicalHash, res.CertificateHash)
			}
		})
	}
}
