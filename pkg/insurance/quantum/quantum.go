// Package quantum is VICE's Quantum Engine — blueprint §20 (Quantum
// Engine) and §21 (Quantum Contradiction).
//
// The blueprint states the core formula explicitly:
//
//	GROSS LOSS + MITIGATION - SALVAGE - DEDUCTIBLE = INDICATIVE CLAIM VALUE
//
// and is equally explicit about what the result is NOT: "Tetapi
// hasilnya: INDICATIVE bukan settlement decision" ("but the result is:
// INDICATIVE, not a settlement decision"). Every exported name in this
// package's public API is chosen so that fact cannot be missed: the
// formula's output field is Calculation.IndicativeClaimValue, never
// "Total" or "SettlementAmount" or "Payable" — there is no field or
// function anywhere in this package that could be read as the final
// payable amount.
//
// This package also does not pick a number when the claimant and the
// evidence disagree (§21, and the golden rule at §36: "Never convert
// uncertainty into certainty"). DetectDiscrepancy (discrepancy.go)
// reports the claimant's figure, the evidence-supported figure, and a
// computed "unresolved" gap, side by side — never a chosen winner.
//
// Amount representation: see the doc comment on Amount in money.go for
// why monetary values here are int64 minor units rather than float64.
package quantum

import (
	"errors"
)

// Formula names which formula a Calculation applied. Kept as an
// inspectable string type (not an opaque enum) so a Calculation's
// formula is self-describing wherever it is logged, stored or
// rendered into a dossier.
type Formula string

// FormulaIndicativeClaimValue is the blueprint's own §20 formula,
// transcribed verbatim (its English gloss):
// "GROSS LOSS + MITIGATION - SALVAGE - DEDUCTIBLE = INDICATIVE CLAIM VALUE".
const FormulaIndicativeClaimValue Formula = "GROSS_LOSS + MITIGATION - SALVAGE - DEDUCTIBLE = INDICATIVE_CLAIM_VALUE"

// CurrentCalculationVersion is the default calculation_version stamped
// on a Calculation when the caller does not supply one. Bumping this
// constant is how a future change to the §20 formula (or its
// implementation) would be distinguished from calculations produced
// under the current logic -- part of what makes a calculation
// "reproducible": replaying against the SAME calculation_version must
// yield the SAME result, forever.
const CurrentCalculationVersion = "quantum-v1"

// ReviewStatus tracks whether a Calculation has been looked at by a
// human, and whether it is even fit to be looked at as "computed" in
// the first place. Compute NEVER sets ReviewStatusReviewed itself —
// that transition happens only when a human actually reviews the
// calculation (MarkReviewed), matching the blueprint's absolute
// principle (§0) that VICE must never "automatically approve" a
// claim-adjacent figure.
type ReviewStatus string

const (
	// ReviewStatusPending is the default: freshly computed, not yet
	// reviewed by a human, but every input was at least evidence-backed.
	ReviewStatusPending ReviewStatus = "PENDING_REVIEW"
	// ReviewStatusHumanReviewRequired is set automatically by Compute
	// whenever any component (gross loss, mitigation, salvage or
	// deductible) carries NO supporting evidence at all -- blueprint §0's
	// own required output category, HUMAN_REVIEW_REQUIRED, applied here
	// mechanically rather than left to a caller to remember.
	ReviewStatusHumanReviewRequired ReviewStatus = "HUMAN_REVIEW_REQUIRED"
	// ReviewStatusReviewed is set only by MarkReviewed, never by Compute.
	ReviewStatusReviewed ReviewStatus = "REVIEWED"
)

var knownReviewStatuses = map[ReviewStatus]bool{
	ReviewStatusPending: true, ReviewStatusHumanReviewRequired: true, ReviewStatusReviewed: true,
}

// IsKnownReviewStatus reports whether s is a modelled review status.
func IsKnownReviewStatus(s ReviewStatus) bool { return knownReviewStatuses[s] }

// Calculation is one application of the §20 formula, carrying exactly
// the fields the blueprint requires every quantum figure to have:
// calculation_id, formula, input_evidence, currency, exchange_rate,
// rate_source, calculation_version, review_status -- plus the
// evidence-backed operands and the resulting IndicativeClaimValue.
type Calculation struct {
	CalculationID      string       `json:"calculation_id"`
	Formula            Formula      `json:"formula"`
	InputEvidence      []string     `json:"input_evidence"`
	Currency           string       `json:"currency"`
	ExchangeRate       ExchangeRate `json:"exchange_rate"`
	RateSource         string       `json:"rate_source"`
	CalculationVersion string       `json:"calculation_version"`
	ReviewStatus       ReviewStatus `json:"review_status"`

	GrossLoss  EvidenceBackedAmount `json:"gross_loss"`
	Mitigation EvidenceBackedAmount `json:"mitigation"`
	Salvage    EvidenceBackedAmount `json:"salvage"`
	Deductible EvidenceBackedAmount `json:"deductible"`

	// IndicativeClaimValue is the §20 formula's result. Deliberately
	// NOT named Total, SettlementAmount or PayableAmount: it is
	// INDICATIVE, never a settlement decision (§20's own words).
	IndicativeClaimValue Amount `json:"indicative_claim_value"`
}

// IsFullySupported reports whether every operand that fed this
// Calculation carries at least one piece of supporting evidence. A
// Calculation can be arithmetically valid (Compute never refuses to
// compute) while still being IsFullySupported() == false; callers
// must check this — not just read IndicativeClaimValue — before
// treating the result as anything more than a provisional figure.
func (c Calculation) IsFullySupported() bool {
	return c.GrossLoss.Status() == EvidenceStatusSupported &&
		c.Mitigation.Status() == EvidenceStatusSupported &&
		c.Salvage.Status() == EvidenceStatusSupported &&
		c.Deductible.Status() == EvidenceStatusSupported
}

// MarkReviewed returns a copy of c with ReviewStatus set to
// ReviewStatusReviewed. This is the ONLY way a Calculation's review
// status becomes REVIEWED — Compute never sets it, matching the
// blueprint's absolute principle that VICE itself must never finalise
// a figure; only a human reviewing it can.
func (c Calculation) MarkReviewed() Calculation {
	c.ReviewStatus = ReviewStatusReviewed
	return c
}

var (
	ErrEmptyCalculationID  = errors.New("quantum: CalculationID must be non-empty")
	ErrEmptyCurrency       = errors.New("quantum: Currency must be non-empty")
	ErrEmptyRateSource     = errors.New("quantum: RateSource must be non-empty")
	ErrInvalidExchangeRate = errors.New("quantum: ExchangeRate must be positive (use UnitExchangeRate() for same-currency calculations)")
	ErrNegativeAmount      = errors.New("quantum: amounts must not be negative")
)

// ComputeInput is the set of evidence-backed operands and metadata
// Compute needs to apply the §20 formula.
type ComputeInput struct {
	CalculationID string

	GrossLoss  EvidenceBackedAmount
	Mitigation EvidenceBackedAmount
	Salvage    EvidenceBackedAmount
	Deductible EvidenceBackedAmount

	Currency     string
	ExchangeRate ExchangeRate
	RateSource   string

	// CalculationVersion is optional; if empty, CurrentCalculationVersion
	// is used.
	CalculationVersion string
}

// Compute applies the blueprint's §20 formula:
//
//	GROSS LOSS + MITIGATION - SALVAGE - DEDUCTIBLE = INDICATIVE CLAIM VALUE
//
// to in, and returns a Calculation. Compute NEVER refuses to compute
// merely because an operand lacks supporting evidence — silently
// erroring out would be just as unhelpful as silently trusting an
// unevidenced number. Instead, per the golden rule (§36: "When
// evidence is insufficient, the correct output is
// INSUFFICIENT_EVIDENCE — not a guessed conclusion"), Compute makes
// the gap impossible to miss: any operand with zero EvidenceIDs
// (EvidenceBackedAmount.Status() == EvidenceStatusUnsupported) forces
// the resulting Calculation.ReviewStatus to
// ReviewStatusHumanReviewRequired, and Calculation.IsFullySupported()
// reports false. A caller that reads only IndicativeClaimValue and
// ignores ReviewStatus/IsFullySupported is misusing this API — the
// number is always present, but it is only ever "PENDING_REVIEW" at
// best, never a confirmed settlement figure (see the package doc).
//
// Compute DOES refuse malformed input outright: an empty
// CalculationID/Currency/RateSource, a non-positive ExchangeRate, or a
// negative operand amount are all programmer/caller errors, not
// evidentiary gaps, and are reported as such.
func Compute(in ComputeInput) (Calculation, error) {
	if in.CalculationID == "" {
		return Calculation{}, ErrEmptyCalculationID
	}
	if in.Currency == "" {
		return Calculation{}, ErrEmptyCurrency
	}
	if in.RateSource == "" {
		return Calculation{}, ErrEmptyRateSource
	}
	if !in.ExchangeRate.IsPositive() {
		return Calculation{}, ErrInvalidExchangeRate
	}
	for _, amt := range []Amount{in.GrossLoss.Amount, in.Mitigation.Amount, in.Salvage.Amount, in.Deductible.Amount} {
		if amt < 0 {
			return Calculation{}, ErrNegativeAmount
		}
	}

	version := in.CalculationVersion
	if version == "" {
		version = CurrentCalculationVersion
	}

	indicative := in.GrossLoss.Amount.Add(in.Mitigation.Amount).Sub(in.Salvage.Amount).Sub(in.Deductible.Amount)

	review := ReviewStatusPending
	if in.GrossLoss.Status() == EvidenceStatusUnsupported ||
		in.Mitigation.Status() == EvidenceStatusUnsupported ||
		in.Salvage.Status() == EvidenceStatusUnsupported ||
		in.Deductible.Status() == EvidenceStatusUnsupported {
		review = ReviewStatusHumanReviewRequired
	}

	return Calculation{
		CalculationID:      in.CalculationID,
		Formula:            FormulaIndicativeClaimValue,
		InputEvidence:      mergeEvidenceIDs(in.GrossLoss, in.Mitigation, in.Salvage, in.Deductible),
		Currency:           in.Currency,
		ExchangeRate:       in.ExchangeRate,
		RateSource:         in.RateSource,
		CalculationVersion: version,
		ReviewStatus:       review,

		GrossLoss:  in.GrossLoss,
		Mitigation: in.Mitigation,
		Salvage:    in.Salvage,
		Deductible: in.Deductible,

		IndicativeClaimValue: indicative,
	}, nil
}

// A note for reviewers and future maintainers of this file: do not
// add a field or exported function whose name could be read as "the
// final payable amount" or "the settlement". If a real need arises
// for such a concept, it belongs in a different, clearly
// human-decision-gated package, not here. See the package doc and
// blueprint §0 ("VICE MUST NOT ... determine legal liability
// finally").
