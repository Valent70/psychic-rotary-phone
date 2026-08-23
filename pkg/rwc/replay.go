package rwc

import (
	"fmt"

	"veriqo/pkg/replay"
	"veriqo/veriqo/kernel"
)

// VerifyReplay independently replays one already-run CaseResult.
//
// It records the exact CaseInput/CanonicalResult that the execution
// engine actually used — execution.Result.Case and lifecycle.Result.
// Canonical, NOT the caller's original request, because RunUnified
// resolves entity aliases and mutates its own local copy of CaseInput
// before running it (see pkg/execution.Result.Case's own doc comment on
// why this is the only place the committed bytes are visible). It wraps
// that in a real pkg/replay.ReplayPackage and replays it through a
// brand-new pkg/canonical.Pipeline: pkg/replay.Engine.Replay never
// shares a pointer with the original run.
//
// It calls no RWC-specific replay logic: this is the SAME replay engine
// and the SAME VerificationCertificate.Assert() every other VERIQO
// caller uses.
//
// SCOPE. This is an in-process replay through a fresh pipeline, not a
// cold cross-process replay. The cross-process capability exists
// separately on this branch (pkg/execution.Result.ExportReplay ->
// cmd/veriqo-cold-replay); ReplayDAG is what verifies the whole
// execution DAG from bytes alone. VerifyReplay verifies the canonical
// stage chain. Both are real; they are not the same claim, and
// cmd/veriqo-rwc-v2 records which one it ran.
func VerifyReplay(k *kernel.Kernel, actorID string, cr *CaseResult) (replay.VerificationCertificate, error) {
	execRes := cr.Lifecycle.Execution
	rec, err := replay.Record(actorID, execRes.Case, cr.Lifecycle.Canonical,
		k.Canonical.Dependencies.ReplayAll(), k.Identity.Head())
	if err != nil {
		return noCertificate, fmt.Errorf("rwc: replay.Record(%s): %w", cr.CaseID, err)
	}
	pkg, err := replay.NewReplayPackage("rwc-v2-independent-auditor", "rwc-v2/"+cr.CaseID, rec)
	if err != nil {
		return noCertificate, fmt.Errorf("rwc: replay.NewReplayPackage(%s): %w", cr.CaseID, err)
	}
	cert, err := replay.NewEngine().Replay(pkg)
	if err != nil {
		return noCertificate, fmt.Errorf("rwc: replay.Engine.Replay(%s): %w", cr.CaseID, err)
	}
	return cert, nil
}

// noCertificate is the value every error path above returns. It exists
// as a named zero value rather than an inline composite literal for a
// governance reason, not a stylistic one: internal/entrypoints guards
// the literal construction of a replay.VerificationCertificate, because
// "a verification certificate is the output of an independent replay;
// constructing one directly is asserting a verification that never ran".
// This package never asserts one. Returning a named, obviously-empty
// value says that more plainly than an inline literal would, and keeps
// this file off that gate's allowlist entirely.
var noCertificate replay.VerificationCertificate
