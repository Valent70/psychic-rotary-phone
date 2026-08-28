package cre

import (
	"errors"
	"fmt"
	"sort"

	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/finding"
)

// ErrInferenceTraceNotFound is VerifyFindingProvenance's refusal when a
// Finding cites a SourceInferenceTraceID that is not present in the
// given traces.
var ErrInferenceTraceNotFound = errors.New("cre: Finding cites a SourceInferenceTraceID that does not exist in the given traces")

// VerifyFindingProvenance closes the loop CRE's AI-governance discipline
// opens: a Finding.SourceInferenceTraceID is a CITATION, and a citation
// nobody ever checks is not meaningfully different from an unverifiable
// assertion. This confirms f's own Hash verifies, and -- when
// SourceInferenceTraceID is set -- that it names a REAL trace within
// traces (typically Recorder.Traces()) whose own Hash also verifies. A
// Finding citing a trace ID that does not exist, or whose hash no
// longer verifies, fails here even though f.Hash alone might still be
// perfectly valid: f's own internal consistency says nothing about
// whether what it cites is real.
func VerifyFindingProvenance(f finding.Finding, traces []inference.InferenceTrace) error {
	if err := finding.VerifyFindingHash(f); err != nil {
		return err
	}
	if f.SourceInferenceTraceID == "" {
		return nil
	}
	for _, t := range traces {
		if t.TraceID == f.SourceInferenceTraceID {
			return inference.VerifyTraceHash(t)
		}
	}
	return fmt.Errorf("%w: %s", ErrInferenceTraceNotFound, f.SourceInferenceTraceID)
}

// ErrFindingDoesNotMatchHypothesis is VerifyFindingAgainstHypothesis's
// refusal when a Finding's own SupportedBy/ContradictedBy/
// ConfidenceBasis fields do not match what the cited hypothesis's real
// data says.
var ErrFindingDoesNotMatchHypothesis = errors.New("cre: Finding's SupportedBy/ContradictedBy/ConfidenceBasis does not match the cited hypothesis's real data")

// VerifyFindingAgainstHypothesis closes the one gap BuildFinding's own
// mechanical construction does not structurally prevent: nothing stops
// a caller from hand-constructing a finding.Finding directly (bypassing
// BuildFinding) with a ConfidenceBasis, SupportedBy, or ContradictedBy
// that was never actually computed by a real causation.HypothesisSet at
// all -- finding.Finding itself has no way to know its own fields were
// honestly derived, only that they are internally hash-consistent. This
// re-derives all three from the REAL hypothesis h within hs (never
// trusting f's own claim) and confirms they match exactly, the same
// re-verify-by-recomputing discipline pkg/insurance/verification's four
// gates already use for coverage/quantum/preservation. SupportedBy and
// ContradictedBy are compared as SETS (order-independent): BuildFinding
// itself always preserves order, but a hand-built Finding that lists the
// identical evidence in a different order is not thereby fabricating
// anything.
func VerifyFindingAgainstHypothesis(f finding.Finding, hs *causation.HypothesisSet, hypothesisID causation.HypothesisID) error {
	if err := finding.VerifyFindingHash(f); err != nil {
		return err
	}
	h, ok := hs.Get(hypothesisID)
	if !ok {
		return fmt.Errorf("%w: hypothesis %s not found in the given HypothesisSet", ErrFindingDoesNotMatchHypothesis, hypothesisID)
	}
	if !sameStringSet(f.SupportedBy, h.SupportingEvidence) {
		return fmt.Errorf("%w: SupportedBy does not match the hypothesis's real SupportingEvidence", ErrFindingDoesNotMatchHypothesis)
	}
	if !sameStringSet(f.ContradictedBy, h.ContradictingEvidence) {
		return fmt.Errorf("%w: ContradictedBy does not match the hypothesis's real ContradictingEvidence", ErrFindingDoesNotMatchHypothesis)
	}
	if f.ConfidenceBasis != h.Status {
		return fmt.Errorf("%w: ConfidenceBasis %q does not match the hypothesis's real Status %q", ErrFindingDoesNotMatchHypothesis, f.ConfidenceBasis, h.Status)
	}
	return nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac, bc := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
