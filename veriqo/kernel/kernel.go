// Package kernel is Veriqo's single composition root — the one place
// that boots Core Runtime, Execution, Evidence, Knowledge (via
// Intelligence), and Intelligence into one working system. This
// replaces the prior session's "veriqo/unified/system" package: same
// job, name changed to drop the "Unified" prefix per
// "Yang_masih_saya_kritisi.docx" §1.
//
// The one architectural change beyond renaming: Kernel now constructs
// exactly ONE pkg/core/trustcalc.Calculus and hands the SAME instance
// to both the Evidence engine and the Intelligence loop. That is the
// concrete mechanism behind "trust calculus... bekerja lintas seluruh
// sistem" — without this shared instance, Evidence and Intelligence
// would each learn an independent, disconnected notion of source
// trust, exactly the failure mode the critique warned about.
package kernel

import (
	"veriqo/pkg/canonical"
	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/kernel/intent"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/calibration"
	"veriqo/veriqo/core/evidence"
	"veriqo/veriqo/core/execution"
	"veriqo/veriqo/core/intelligence"
	"veriqo/veriqo/core/runtime"
	"veriqo/veriqo/registry"
)

// Kernel is the fully-wired Core Runtime + Execution + Evidence +
// Intelligence + Intent + Calibration system.
//
// Intent and Calibration were added to close the audit finding
// ("Hasil_audit_brutal...docx" P0/P1): before this, Intent verified
// outcomes through its own private trust.Kernel and Calibration's
// Summarize() had zero production callers into any trust system —
// two disconnected epistemic islands next to the shared Evidence/
// Intelligence trust calculus. Both now share the SAME TrustCalculus
// instance, so an Intent verdict or a Calibration ground-truth record
// changes the exact belief Evidence/Intelligence use next.
type Kernel struct {
	Registry      *registry.Registry
	Runtime       *runtime.Manager
	Execution     *execution.Orchestrator
	Evidence      *evidence.Engine
	Intelligence  *intelligence.Loop
	Intent        *intent.Verifier
	Calibration   *calibration.Engine
	TrustCalculus *trustcalc.Calculus
	// TrustLedger is the immutable, hash-chained record of every
	// Observe() ever performed against TrustCalculus, from any of
	// Evidence/Intelligence/Intent/Calibration — the P0 "Belief
	// Ledger" gap both audits named. TrustLedger.Replay(1, 1) rebuilds
	// a fresh Calculus from this history alone; its Belief(subject)
	// must equal TrustCalculus.Belief(subject) for every subject that
	// was ever observed (see kernel_test.go
	// TestTrustLedgerReplayMatchesLiveKernelState).
	TrustLedger *trustcalc.Ledger
	// Canonical is the v7.10.2 gap-closure P0: the single composed
	// MOAT path (Evidence -> Provenance -> Fusion -> Truth ->
	// Causal -> Risk -> Decision -> DigitalTwin -> Certificate) named
	// by the V7.10.1 whole-repo technical audit as missing — see
	// pkg/canonical's package comment. Shares the SAME TrustCalculus
	// as every other engine on this Kernel.
	Canonical *canonical.Pipeline
	// Lifecycle is the v7.10.3 gap-closure P0: the audit's required
	// Intent -> Evidence Planning -> Entity Resolution -> ... -> IVF
	// -> Outcome -> Calibration -> Replay contract, composed on top of
	// Canonical (see pkg/lifecycle's package comment for why this is
	// composition, not a second orchestration framework).
	Lifecycle *lifecycle.Orchestrator
}

// New boots a complete Kernel: builds a registry.Registry (the seven
// existing kernels), wraps them into Core Runtime via
// veriqo/core/runtime.RegisterAll, starts them all in dependency order,
// constructs ONE shared trustcalc.Calculus, and wires it into both a
// fresh Evidence Engine and a fresh Intelligence Loop. regOpts are
// forwarded to registry.New (e.g. registry.WithStatePath for
// persistence).
func New(regOpts ...registry.Option) (*Kernel, error) {
	reg, err := registry.New(regOpts...)
	if err != nil {
		return nil, err
	}

	mgr := runtime.NewManager()
	if err := runtime.RegisterAll(mgr, reg); err != nil {
		return nil, err
	}
	if err := mgr.StartAll(); err != nil {
		return nil, err
	}

	ledger := trustcalc.NewLedger()
	calc := trustcalc.NewWithLedger(1, 1, ledger) // Beta(1,1) — uninformative prior, shared everywhere, every Observe() hash-chained

	intentVerifier, err := intent.NewVerifierWithCalculus(calc)
	if err != nil {
		return nil, err
	}

	// Evidence reads trust through the SAME "evidence_source" namespace
	// Intelligence writes to (see trustcalc.NamespaceEvidenceSource),
	// so the two keep sharing one identity space with each other while
	// staying formally distinct from intent_actor/calibration_source —
	// the v7.10 audit's "core/intelligence.sourceTrust masih perlu
	// diaudit dan kemungkinan namespace" finding, closed OS-wide.
	nsEvidenceTrust := evidence.NewNamespacedTrust(calc, trustcalc.NamespaceEvidenceSource)

	canonPipeline := canonical.NewPipeline(calc)

	return &Kernel{
		Registry:      reg,
		Runtime:       mgr,
		Execution:     execution.New(mgr),
		Evidence:      evidence.New(nsEvidenceTrust, 0),
		Intelligence:  intelligence.New(calc),
		Intent:        intentVerifier,
		Calibration:   calibration.NewEngineWithCalculus(calc),
		TrustCalculus: calc,
		TrustLedger:   ledger,
		Canonical:     canonPipeline,
		Lifecycle:     lifecycle.NewOrchestrator(canonPipeline),
	}, nil
}

// Shutdown stops every engine and, if the underlying Registry has
// persistence enabled, saves final state.
func (k *Kernel) Shutdown() error {
	if err := k.Runtime.StopAll(); err != nil {
		return err
	}
	if k.Registry.HasPersistence() {
		return k.Registry.SaveState()
	}
	return nil
}

// HealthReport returns a fresh health snapshot of every Core Runtime
// member.
func (k *Kernel) HealthReport() map[string]runtime.Status { return k.Runtime.HealthReport() }
