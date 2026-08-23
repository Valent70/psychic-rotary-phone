// canonical_truth_support_test.go holds the shared fixtures for the
// canonical-truth-path acceptance suite (WAVE A of the final gap
// closure mandate). It builds cases through the REAL entrypoints only —
// veriqo/kernel.New, lifecycle.Intent, lifecycle.PlanEvidence,
// Orchestrator.RunUnified — so nothing in this suite can pass by
// reaching into an engine the production path does not itself use.
package acceptance

import (
	"context"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/ledger"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/trust/state"
	"veriqo/veriqo/kernel"
)

// truthPolicy is the decision policy every case in this suite runs
// under. It declares the ONE factor pkg/canonical actually populates
// ("tbml_composite_risk_score" — see risk.ToDecisionValues), so the
// native decision engine genuinely consumes the computed risk rather
// than scoring against a factor nothing produces.
var truthPolicy = decision.Policy{
	Name: "canonical_truth_path_acceptance_v1",
	Factors: []decision.FactorWeight{
		{Name: "tbml_composite_risk_score", Weight: 1.0},
	},
	FlagThreshold:     0.3,
	EscalateThreshold: 0.7,
}

// truthCase is one acceptance scenario expressed in the same terms a
// real caller uses.
type truthCase struct {
	objective   string
	tenant      string
	aliases     []entity.Alias
	predicate   string
	submissions []canonical.SourceSubmission
	pattern     float64
	price       float64
	tick        uint64
}

func (c truthCase) run(t *testing.T, k *kernel.Kernel) *lifecycle.Result {
	t.Helper()
	res, err := c.runErr(k)
	if err != nil {
		t.Fatalf("RunUnified(%s): %v", c.objective, err)
	}
	return res
}

func (c truthCase) runErr(k *kernel.Kernel) (*lifecycle.Result, error) {
	in := lifecycle.Intent{
		ActorID: "canonical-truth-acceptance", Objective: c.objective,
		Tenant: c.tenant, EntityAliases: c.aliases,
		TemporalScope: "acceptance", Tick: c.tick,
	}
	plan := lifecycle.PlanEvidence(in, nil)
	caseIn := canonical.CaseInput{
		Predicate: c.predicate, Submissions: c.submissions, Policy: truthPolicy,
		PatternScore: c.pattern, PriceAnomaly: c.price, Tick: c.tick,
	}
	return k.Lifecycle.RunUnified(context.Background(), in, plan, caseIn)
}

// newTruthKernel boots a real Kernel for one test and registers its
// shutdown.
func newTruthKernel(t *testing.T) *kernel.Kernel {
	t.Helper()
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	t.Cleanup(func() { _ = k.Shutdown() })
	return k
}

// withDurableLedger attaches a real durable ledger, backed by a real
// write-ahead log in a real directory on disk, to a Kernel's lifecycle
// Orchestrator. It returns the ledger and the directory, so a test can
// reopen the SAME bytes from a second process (see the crash-recovery
// acceptance test).
func withDurableLedger(t *testing.T, k *kernel.Kernel, dir string) *ledger.Ledger {
	t.Helper()
	l, rep, err := ledger.Open(ledger.Config{Dir: dir})
	if err != nil {
		t.Fatalf("ledger.Open(%s): %v", dir, err)
	}
	if rep != nil && rep.FailedClosed {
		t.Fatalf("ledger.Open(%s) failed closed on recovery: %+v", dir, rep.Findings)
	}
	k.Lifecycle.Ledger = l
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// assessTrust records a real, positive trust assessment for a subject
// against the SAME trust engine the canonical pipeline weighs evidence
// with. It goes through pkg/trust/state.Engine.Assess — the governed
// transition path — never by writing a level directly.
func assessTrust(t *testing.T, k *kernel.Kernel, subject string, score float64, tick uint64) {
	t.Helper()
	if _, err := k.Canonical.TrustState.Assess(subject, "canonical-truth-acceptance",
		truthPolicy.Name, "acceptance fixture: provider vetted against its delivery history",
		score, []string{"acceptance-evidence-ref"}, tick, 0); err != nil {
		t.Fatalf("trust Assess(%s): %v", subject, err)
	}
}

// revokeTrust terminally revokes a subject through the governed path.
func revokeTrust(t *testing.T, k *kernel.Kernel, subject string, tick uint64) {
	t.Helper()
	if _, err := k.Canonical.TrustState.Revoke(subject, "canonical-truth-acceptance",
		"acceptance fixture: provider revoked after a confirmed fabrication",
		[]string{"acceptance-revocation-ref"}, tick); err != nil {
		t.Fatalf("trust Revoke(%s): %v", subject, err)
	}
}

// trustLevelOf reads a source's trust posture out of a completed run,
// so a test asserts on what the DECISION actually saw rather than on
// what the ledger says now.
func trustLevelOf(res *lifecycle.Result, sourceID string) (state.Level, canonical.TrustPosture, bool) {
	for _, s := range res.Canonical.Trust.Sources {
		if s.SourceID == sourceID {
			return s.Level, s.Posture, true
		}
	}
	return "", "", false
}

// sub is a terse SourceSubmission constructor for this suite.
func sub(id, provider, value string, reliability float64) canonical.SourceSubmission {
	return canonical.SourceSubmission{
		SourceID: fusion.SourceID(id), Value: value, BaseReliability: reliability, Provider: provider,
	}
}
