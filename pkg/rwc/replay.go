package rwc

import (
	"fmt"

	"veriqo/pkg/replay"
	"veriqo/veriqo/kernel"
)

// VerifyReplay is the brief's §12 P0 requirement, applied to one already
// -run CaseResult: it records the exact CaseInput/CanonicalResult that
// RunUnified actually used (execution.Result.Case, execution.Result.
// Canonical — NOT the caller's original request, which RunUnified may
// have mutated via entity resolution), wraps it in a real
// pkg/replay.ReplayPackage, and independently replays it through a
// brand-new pkg/canonical.Pipeline (pkg/replay.Engine.Replay never
// shares a pointer with the original run — see pkg/replay's own package
// doc). It calls no RWC-specific logic: this is the SAME replay engine
// and the SAME VerificationCertificate.Assert() every other VERIQO
// caller uses.
func VerifyReplay(k *kernel.Kernel, actorID string, cr *CaseResult) (replay.VerificationCertificate, error) {
	execRes := cr.Lifecycle.Execution
	rec, err := replay.Record(actorID, execRes.Case, cr.Lifecycle.Canonical, k.Canonical.Dependencies.ReplayAll(), k.Identity.Head())
	if err != nil {
		return replay.VerificationCertificate{}, fmt.Errorf("rwc: replay.Record(%s): %w", cr.CaseID, err)
	}
	pkg, err := replay.NewReplayPackage("rwc-v2-independent-auditor", "rwc-v2/"+cr.CaseID, rec)
	if err != nil {
		return replay.VerificationCertificate{}, fmt.Errorf("rwc: replay.NewReplayPackage(%s): %w", cr.CaseID, err)
	}
	cert, err := replay.NewEngine().Replay(pkg)
	if err != nil {
		return replay.VerificationCertificate{}, fmt.Errorf("rwc: replay.Engine.Replay(%s): %w", cr.CaseID, err)
	}
	return cert, nil
}
