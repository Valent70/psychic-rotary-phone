// This file closes the single largest concrete gap found while
// consolidating the Palantir/Bloomberg-informed audit
// ("Hasil_analisa_Palantir_dll.docx", "Rekomendasi_prioritas_next_step.docx",
// "Hal_yang_sangat_krusial.docx"): pkg/verification (the Independent
// Verification Framework, self-identified by the audit as VERIQO's
// single biggest potential MOAT) had ZERO callers anywhere else in this
// repository. It was a correct, well-tested, isolated pipeline that no
// real engine run ever actually exercised — evidence built but never
// wired, exactly the "ilusi komputer" failure mode being closed here.
//
// This file makes IVF a live, mandatory part of the Unified Engine
// Orchestrator's own run loop (RunAndVerify below), not a demo prop
// bolted on afterward: any adapter that implements IVFCapable gets a
// REAL independent-replay verification, from RAW evidence (observations
// actually fed to the engine), on EVERY run, with the verifier built
// fresh each time so no state leaks from the run being checked.
package engine

import (
	"fmt"

	"veriqo/pkg/platform/observability"
	"veriqo/pkg/verification"
)

// IVFCapable is implemented by engine adapters that can export the
// RAW input evidence behind their last Evaluate() (not a description of
// the output — the actual observations/factors an independent party
// would need to recompute the answer from scratch) plus a pure replay
// function that recomputes a result on a brand-new inner engine
// instance. An adapter that does not implement this interface simply
// skips IVF (see RunAndVerify) — added mechanically per-adapter, same
// pattern each time, consistent with this repo's existing "explicit
// open item, not silently skipped" discipline.
type IVFCapable interface {
	// RawEvidence returns the actual raw inputs behind the last
	// Evaluate() call, ready to package into a verification.EvidencePackage.
	RawEvidence() ([]verification.EvidenceRecord, error)
	// ClaimedResult is the canonical string form of the last
	// Evaluate() outcome — what an independent verifier must reproduce
	// byte-for-byte to pass.
	ClaimedResult() string
	// IVFDomain is the ReplayFunc registration key for this adapter's
	// evidence domain.
	IVFDomain() string
	// IVFReplayFunc returns a function that independently recomputes
	// ClaimedResult from a RawEvidence-shaped EvidencePackage using a
	// FRESH inner engine constructed inside the closure — never the
	// adapter's own `inner` — so a passing verification proves the
	// algorithm, not merely that the same object was asked twice.
	IVFReplayFunc() verification.ReplayFunc
}

// BuildVerificationBundle runs IVF Stage 2-4: package one IVFCapable
// adapter's last-run raw evidence plus its claimed result into a
// content-hashed, shippable VerificationBundle.
func BuildVerificationBundle(subject string, e IVFCapable) (verification.VerificationBundle, error) {
	records, err := e.RawEvidence()
	if err != nil {
		return verification.VerificationBundle{}, fmt.Errorf("engine: collecting raw evidence: %w", err)
	}
	records = verification.SortRecordsByIndex(records)
	rp := verification.ReplayPackage{
		Evidence:      verification.EvidencePackage{Subject: subject, Records: records},
		ClaimedResult: e.ClaimedResult(),
	}
	return verification.NewBundle(rp)
}

// VerifiedRunRecord extends RunRecord with the IVF artifacts produced
// automatically for IVFCapable engines — nil for engines that don't
// implement the interface yet (an explicit, visible "not yet wired"
// signal rather than a silent skip).
type VerifiedRunRecord struct {
	RunRecord
	Bundle             *verification.VerificationBundle
	VerificationResult *verification.VerificationResult
}

// RunAndVerify executes the standard Stage 2-6 lifecycle (identical to
// Run) and then, only if the named engine implements IVFCapable, runs
// the FULL IVF Stage 2-7 pipeline in one call: build the bundle from
// real raw evidence, then independently verify it with a brand-new
// verification.Verifier (see verification.NewVerifier) and the
// adapter's own IVFReplayFunc (which itself must construct a fresh
// inner engine — see adapters.go for the concrete per-adapter
// implementations). This is the concrete mechanism that turns "IVF
// exists" into "IVF actually ran on this real decision" for every
// engine that opts in.
func (k *Kernel) RunAndVerify(engineName string, ctx Context) (VerifiedRunRecord, error) {
	rec, err := k.Run(engineName, ctx)
	if err != nil {
		return VerifiedRunRecord{}, err
	}
	out := VerifiedRunRecord{RunRecord: rec}

	k.mu.Lock()
	e := k.engines[engineName]
	metrics := k.metrics
	k.mu.Unlock()
	ivf, ok := e.(IVFCapable)
	if !ok {
		return out, nil
	}

	var span *observability.Span
	if metrics != nil {
		span = metrics.StartStage(observability.StageVerification)
	}

	bundle, err := BuildVerificationBundle(ctx.Subject, ivf)
	if err != nil {
		if span != nil {
			span.Finish()
		}
		return out, fmt.Errorf("engine: building IVF bundle for %q: %w", engineName, err)
	}
	out.Bundle = &bundle

	v := verification.NewVerifier()
	v.RegisterReplayFunc(ivf.IVFDomain(), ivf.IVFReplayFunc())
	result, err := v.Verify(ivf.IVFDomain(), bundle)
	if span != nil {
		span.Finish()
	}
	if err != nil {
		if metrics != nil {
			metrics.SetVerificationScore(0)
			metrics.RecordReplay(false)
		}
		return out, fmt.Errorf("engine: independent verification for %q: %w", engineName, err)
	}
	out.VerificationResult = &result

	// VerificationScore per Kritik 8: 1.0 only when BOTH the manifest
	// integrity check and the independent replay agreed — the same
	// bar AuditCertificate issuance uses (see verification.go), so
	// this metric never reports "verified" more optimistically than
	// the certificate itself would.
	if metrics != nil {
		passed := result.ManifestValid && result.ReplayValid
		if passed {
			metrics.SetVerificationScore(1)
		} else {
			metrics.SetVerificationScore(0)
		}
		metrics.RecordReplay(passed)
	}
	return out, nil
}
