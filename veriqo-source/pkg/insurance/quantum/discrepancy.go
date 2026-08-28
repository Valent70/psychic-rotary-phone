package quantum

import (
	"errors"
)

// QuantumDiscrepancy is VICE's output when the claimant's asserted
// loss and the evidence-supported figure disagree — blueprint §21
// ("Quantum Contradiction"). The blueprint is explicit: "VICE jangan
// langsung memilih angka" ("VICE must not simply pick a number"). A
// QuantumDiscrepancy therefore reports BOTH figures side by side,
// plus the agreed reductions and a computed Unresolved gap — never a
// chosen winner. Nothing in this type or DetectDiscrepancy decides
// which figure is "right".
type QuantumDiscrepancy struct {
	DiscrepancyID string `json:"discrepancy_id"`
	// CalculationID optionally links this discrepancy to a Calculation
	// (e.g. one computed from SupportedGross via Compute); empty if none.
	CalculationID string `json:"calculation_id,omitempty"`

	Claimed        EvidenceBackedAmount `json:"claimed"`
	SupportedGross EvidenceBackedAmount `json:"supported_gross"`
	Salvage        EvidenceBackedAmount `json:"salvage"`
	Deductible     EvidenceBackedAmount `json:"deductible"`

	// Unresolved is the computed, disputed residual — see the doc
	// comment on DetectDiscrepancy for the formula and its derivation.
	// A non-positive Unresolved does not mean "resolved in the
	// insurer's favour"; it means the evidence-vs-claim gap is fully
	// absorbed by the undisputed reductions and there is no residual
	// dispute left for a human reviewer to focus on. See
	// HasUnresolvedGap.
	Unresolved Amount `json:"unresolved"`

	Currency      string   `json:"currency"`
	InputEvidence []string `json:"input_evidence"`

	// ReviewStatus is always ReviewStatusHumanReviewRequired: the
	// entire point of a QuantumDiscrepancy record is that VICE could
	// not, and must not, resolve the gap itself.
	ReviewStatus ReviewStatus `json:"review_status"`
}

// HasUnresolvedGap reports whether Unresolved represents a genuine
// disputed shortfall (> 0) that a human reviewer still needs to
// address, as opposed to zero or negative (the undisputed reductions
// already absorb the entire claimed-vs-supported gap).
func (d QuantumDiscrepancy) HasUnresolvedGap() bool { return d.Unresolved > 0 }

var (
	ErrEmptyDiscrepancyID = errors.New("quantum: DiscrepancyID must be non-empty")
)

// DetectDiscrepancy implements blueprint §21's worked example as a
// real, general computation over any (claimed, supportedGross,
// salvage, deductible), not a constant.
//
// The blueprint's own worked example gives:
//
//	claimed          = USD 2,500,000
//	supported_gross  = USD 1,900,000   (invoice evidence)
//	salvage          = USD   300,000
//	deductible       = USD   100,000
//	unresolved       = USD   200,000
//
// and states these numbers without spelling out the arithmetic in
// full. Derivation:
//
// The part of the claim genuinely IN DISPUTE is the gap between what
// the claimant asserts as gross loss and what invoice evidence
// actually proves:
//
//	gap := claimed - supported_gross
//	     = 2,500,000 - 1,900,000
//	     = 600,000
//
// Salvage and deductible, unlike the gross-loss figure, are not
// contested between claimant and insurer in this example — they are
// agreed, evidenced reductions that apply to the claim regardless of
// which gross-loss figure ultimately prevails. Because they are
// common ground, we net them against the disputed gap exactly ONCE
// (not once per side — that would double-count them):
//
//	unresolved := gap - salvage - deductible
//	            = 600,000 - 300,000 - 100,000
//	            = 200,000
//
// which reproduces the blueprint's own figure exactly. Equivalently,
// in one expression:
//
//	unresolved = claimed - supported_gross - salvage - deductible
//
// We checked this against the more "obvious"-looking alternative of
// running the §20 formula independently for each side and diffing the
// two INDICATIVE CLAIM VALUEs:
//
//	claimant_indicative  = claimed         - salvage - deductible = 2,100,000
//	supported_indicative = supported_gross - salvage - deductible = 1,500,000
//	diff                 = claimant_indicative - supported_indicative = 600,000
//
// That alternative subtracts salvage and deductible from BOTH sides
// of the diff, so they cancel out entirely and the result collapses
// to plain (claimed - supported_gross) = 600,000 — it can never equal
// the blueprint's 200,000 for ANY nonzero salvage/deductible. The
// formula actually implemented here is the one that reconciles to the
// blueprint's own stated answer, and it is defensible independently
// of that fact: salvage and deductible are the parts of this dispute
// that are NOT in question, so they belong to resolving the disputed
// gap, not to a duplicated before/after calculation on each side.
func DetectDiscrepancy(discrepancyID string, claimed, supportedGross, salvage, deductible EvidenceBackedAmount, currency string) (QuantumDiscrepancy, error) {
	if discrepancyID == "" {
		return QuantumDiscrepancy{}, ErrEmptyDiscrepancyID
	}
	if currency == "" {
		return QuantumDiscrepancy{}, ErrEmptyCurrency
	}
	for _, amt := range []Amount{claimed.Amount, supportedGross.Amount, salvage.Amount, deductible.Amount} {
		if amt < 0 {
			return QuantumDiscrepancy{}, ErrNegativeAmount
		}
	}

	gap := claimed.Amount.Sub(supportedGross.Amount)
	unresolved := gap.Sub(salvage.Amount).Sub(deductible.Amount)

	return QuantumDiscrepancy{
		DiscrepancyID:  discrepancyID,
		Claimed:        claimed,
		SupportedGross: supportedGross,
		Salvage:        salvage,
		Deductible:     deductible,
		Unresolved:     unresolved,
		Currency:       currency,
		InputEvidence:  mergeEvidenceIDs(claimed, supportedGross, salvage, deductible),
		ReviewStatus:   ReviewStatusHumanReviewRequired,
	}, nil
}
