// verdict.go closes WAVE A item 1 (mandate section II) — "NATIVE
// DECISION HARUS MENJADI SINGLE SOURCE OF TRUTH".
//
// WHAT R23'S AUDIT FOUND, and this file inverts. The chain was:
//
//	constraint arithmetic -> RWC verdict
//	         (native decision = cross-check only)
//
// InterpretVerdict read `cr` and nothing else; `dec` affected only a
// warning string. The audit demonstrated this with a live test that
// handed InterpretVerdict a ZERO-VALUED decision.Decision and got the
// identical verdict back. The mandate's required chain is:
//
//	Evidence -> Trust -> Truth Arbitration -> Native Decision Engine
//	-> Policy Mapping -> Final Verdict
//
// So the verdict is now a pure function of the native engine's own
// decision.Action and an explicit, hashable VerdictPolicy. The
// constraint evaluation keeps exactly one job — the one it always
// genuinely had — which is to COMPUTE THE INPUT the native engine
// scores (PatternScore/PriceAnomaly from ViolationRatio, see
// buildVesselPortCase). It no longer selects an outcome.
//
// THE THREE PROOF OBLIGATIONS the mandate names, and where each is met:
//
//	"Jika Native Decision.Action berubah, Final Verdict berubah"
//	    -> InterpretNativeDecision reads dec.Action and nothing else;
//	       TestAcceptance.../audit_test.go assert it varies.
//	"Jika native decision engine dihapus: No final verdict"
//	    -> ErrNoNativeDecision. A zero-valued Decision produces an
//	       ERROR, not a fallback verdict. There is no arithmetic path
//	       left that could answer instead.
//	"Jika policy mapping berubah: Decision root berubah"
//	    -> DecisionRoot folds VerdictPolicy.Hash() together with the
//	       execution root and the action.
package rwc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"veriqo/pkg/moat/decision"
)

var (
	// ErrNoNativeDecision is returned when there is no native decision to
	// interpret. This is the mandate's "if the native decision engine is
	// removed, there is no final verdict" made structural: the adapter
	// cannot answer on its own, so it does not answer at all.
	//
	// It is deliberately an ERROR and not a VerdictInsufficientEvidence.
	// INSUFFICIENT_EVIDENCE is a real judgement about the CASE ("we
	// looked and there was not enough to go on"); a missing decision
	// engine is a statement about VERIQO ("we did not run"). Collapsing
	// the second into the first is precisely the silent fallback section
	// II forbids.
	ErrNoNativeDecision = errors.New("rwc: no native decision to interpret; refusing to produce a verdict")
	// ErrUnmappedAction is returned when the native engine produced an
	// action this verdict policy does not map. Fail-closed: an unmapped
	// action is never quietly treated as PASS.
	ErrUnmappedAction = errors.New("rwc: verdict policy does not map this native decision action")
)

// VerdictPolicy is the explicit, inspectable, hashable mapping from the
// native engine's decision.Action onto the corpus's PASS/FAIL/
// CONDITIONAL vocabulary.
//
// It is DATA, not a switch statement, for the same reason
// pkg/execution's DAG shape and pkg/canonical's TrustPolicy are data:
// the mandate requires that changing the policy mapping provably changes
// the decision root, and a mapping buried in control flow has no hash.
type VerdictPolicy struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Mapping is total over the actions the engine can emit. A missing
	// entry is an error at interpretation time (ErrUnmappedAction), never
	// a default.
	Mapping map[decision.Action]Verdict `json:"mapping"`
	// InsufficientEvidenceBelowSources is the ONE case in which the
	// verdict is not read from the native action: a case that submitted
	// fewer than this many evidence items had nothing for the engine to
	// reason over, so the honest answer is INSUFFICIENT_EVIDENCE
	// regardless of what the engine's thresholds produced from an empty
	// input. Set to 0 to disable.
	//
	// This is not a back door to the old arithmetic. It cannot select
	// between PASS/FAIL/CONDITIONAL, it reads no constraint finding, and
	// it can only ever REFUSE to answer — it is a floor on evidence, not
	// an alternative decision path.
	InsufficientEvidenceBelowSources int `json:"insufficient_evidence_below_sources"`
}

// DefaultVerdictPolicy is the corpus's mapping. Its shape is the whole
// argument: MONITOR/FLAG/ESCALATE are the three actions
// pkg/moat/decision.Engine emits under a two-threshold policy, and they
// map onto the corpus vocabulary one-for-one.
func DefaultVerdictPolicy() VerdictPolicy {
	return VerdictPolicy{
		Name: "rwc.verdict_mapping", Version: "v1",
		Mapping: map[decision.Action]Verdict{
			decision.ActionMonitor:  VerdictPass,
			decision.ActionFlag:     VerdictConditional,
			decision.ActionEscalate: VerdictFail,
		},
		InsufficientEvidenceBelowSources: 1,
	}
}

// Hash is the policy's content commitment.
func (p VerdictPolicy) Hash() string {
	h := sha256.New()
	fmt.Fprintf(h, "rwc.verdict_policy/v1|name=%s|version=%s|floor=%d|",
		p.Name, p.Version, p.InsufficientEvidenceBelowSources)
	actions := make([]string, 0, len(p.Mapping))
	for a := range p.Mapping {
		actions = append(actions, string(a))
	}
	sort.Strings(actions)
	for _, a := range actions {
		fmt.Fprintf(h, "%s=>%s|", a, p.Mapping[decision.Action(a)])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// InterpretNativeDecision is the mandate's required
//
//	verdict := InterpretNativeDecision(decision, policy)
//
// It reads the native engine's own Action and the policy mapping. It
// does NOT accept a ConstraintResult, and cannot: the type is not in
// its signature, which is what makes "the adapter cannot produce a
// final verdict independent of the native engine" a compile-time
// property rather than a convention.
//
// evidenceCount is the number of evidence items the native arbitration
// actually saw (canonical.CanonicalResult.Arbitration.EvidenceCount) —
// a real output of the engine, not an adapter-side recount of the
// inputs. It drives only the INSUFFICIENT_EVIDENCE floor.
func InterpretNativeDecision(dec decision.Decision, evidenceCount int, p VerdictPolicy) (Verdict, error) {
	if dec.Action == "" {
		return "", fmt.Errorf("%w (policy %s)", ErrNoNativeDecision, p.Name)
	}
	if p.InsufficientEvidenceBelowSources > 0 && evidenceCount < p.InsufficientEvidenceBelowSources {
		return VerdictInsufficientEvidence, nil
	}
	v, ok := p.Mapping[dec.Action]
	if !ok {
		return "", fmt.Errorf("%w: action=%s policy=%s", ErrUnmappedAction, dec.Action, p.Name)
	}
	return v, nil
}

// DecisionRoot is the commitment that ties a verdict to everything that
// produced it: the execution root the native engine ran under, the
// action it emitted, the verdict-policy mapping applied, and the
// resulting verdict.
//
// It exists to satisfy the mandate's third proof obligation directly —
// "Jika policy mapping berubah: Decision root berubah" — with no
// inference required: policyHash is one of the hashed terms.
func DecisionRoot(executionRootHash string, action decision.Action, p VerdictPolicy, v Verdict) string {
	h := sha256.New()
	fmt.Fprintf(h, "rwc.decision_root/v1|execroot=%s|action=%s|policy=%s|verdict=%s|",
		executionRootHash, action, p.Hash(), v)
	return hex.EncodeToString(h.Sum(nil))
}

// ConstraintCrossCheck reports whether the native engine's action agrees
// with what the constraint evaluation would have implied.
//
// THIS IS THE INVERSION of what R23 audited. The constraint arithmetic
// used to select the verdict with the native decision as a cross-check;
// now the native decision selects the verdict and the arithmetic is the
// cross-check. That is not a relabelling: the returned value is a
// warning STRING, it is not an input to any verdict, and no caller can
// obtain a Verdict from this function because it does not return one.
//
// It remains worth computing. The constraint findings are what
// ViolationRatio turns into the risk score the engine consumes, so a
// disagreement between "what the findings imply" and "what the engine
// decided" is a genuine wiring defect (e.g. PatternScore not actually
// reaching the engine) — exactly the defect the R23 audit was able to
// detect only because this comparison existed.
func ConstraintCrossCheck(cr ConstraintResult, dec decision.Decision) string {
	if cr.Evaluated == 0 {
		return ""
	}
	want := decision.ActionMonitor
	switch {
	case len(cr.HardViolations) > 0:
		want = decision.ActionEscalate
	case len(cr.Unresolved) > 0:
		want = decision.ActionFlag
	}
	if dec.Action == want {
		return ""
	}
	return "native decision.Action=" + string(dec.Action) +
		" does not match the action expected from ConstraintResult (" + string(want) +
		") — PatternScore/PriceAnomaly wiring should be checked"
}
