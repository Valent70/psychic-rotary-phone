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

// ErrHypothesisStatusNotEvidenceDerived is VerifyFindingAgainstHypothesis's
// refusal when the cited hypothesis's own stored Status is stronger than
// its SupportingEvidence/ContradictingEvidence/MissingEvidence could
// ever legitimately produce -- see this function's own doc comment for
// why this check exists and why it cannot false-positive.
var ErrHypothesisStatusNotEvidenceDerived = errors.New("cre: hypothesis Status is not supported by its own evidence lists")

// VerifyFindingAgainstHypothesis closes the gap BuildFinding's own
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
//
// A second, deeper check closes a gap one level further back:
// causation.HypothesisSet.Add itself does not force Status to be
// evidence-derived -- a caller can Add a Hypothesis with SupportedBy
// fabricated data already sitting in its own Status field, no evidence
// attached at all, and Add accepts it as long as it is a KNOWN status
// value. If VerifyFindingAgainstHypothesis only compared f.ConfidenceBasis
// against h.Status, a Finding citing such a hypothesis would pass even
// though h.Status was never actually earned. So this also recomputes the
// status h's own evidence lists would produce, via causation.DeriveStatus
// (the exported form of the causation package's own internal
// computeStatus), and refuses if h.Status is STRONGER than that
// recomputation. The comparison is deliberately one-directional, not
// exact equality: DeriveStatus(h, nil) counts evidence by literal
// deduplication with no independence discounting, which is the MOST
// GENEROUS status any legitimate computation (with or without a real
// evidence.DependencyGraph discounting correlated evidence) could ever
// produce for h. A real dg-aware computation can only make the status
// weaker or equal, never stronger -- so h.Status exceeding this
// most-generous bound is only possible if it was never genuinely
// evidence-derived at all, and checking it this way can never
// false-positive against a legitimate dg-discounted status.
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
	mostGenerous := causation.DeriveStatus(h, nil)
	if statusStrength(h.Status) > statusStrength(mostGenerous) {
		return fmt.Errorf("%w: hypothesis %s claims Status %q but its own evidence lists derive, at most, %q",
			ErrHypothesisStatusNotEvidenceDerived, hypothesisID, h.Status, mostGenerous)
	}
	return nil
}

// statusStrength orders causation.Status by how much support it claims,
// for the one-directional comparison VerifyFindingAgainstHypothesis
// needs. CONTRADICTED is deliberately grouped with the weakest tier: it
// is a claim of NET evidence AGAINST, not a support claim, so it can
// never license a stronger downstream ConfidenceBasis than genuinely
// having no evidence at all would.
func statusStrength(s causation.Status) int {
	switch s {
	case causation.StatusSupported:
		return 2
	case causation.StatusPartiallySupported:
		return 1
	default: // StatusUnproven, StatusInsufficientEvidence, StatusContradicted, or unknown
		return 0
	}
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
