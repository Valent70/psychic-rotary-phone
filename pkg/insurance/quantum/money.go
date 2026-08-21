package quantum

import (
	"fmt"
	"sort"
)

// ---- Amount: the monetary representation and why it is not float64 ----
//
// Amount holds a monetary value in MINOR currency units (e.g. cents
// for USD), as an int64.
//
// Why not float64: the blueprint's own §20 requires every Calculation
// to carry a "calculation_version" and (§36/§37) requires "quantum
// calculation reproducible" — the SAME inputs must produce the
// EXACT SAME output, forever, including across process restarts,
// architectures and Go versions. float64 arithmetic is binary
// floating point: 0.1 + 0.2 != 0.3 in IEEE-754, and repeated
// add/subtract chains (exactly what the §20 formula is) accumulate
// representation error that is not guaranteed bit-identical across
// compilers/optimisation levels. That is unacceptable for a figure
// that ultimately informs a claim payment. int64 minor-unit
// arithmetic is exact, associative, and trivially reproducible: the
// same six integers always add up to the same integer, everywhere.
//
// Simplification acknowledged: we use a fixed minor-unit exponent of
// 2 (cents) for every currency, rather than a full ISO 4217
// currency-exponent table (JPY has exponent 0, some currencies have
// 3). That table is straightforward to add later (a map[string]int
// keyed by currency code) but is out of scope for what the blueprint
// asks this package to do; documented here so it is not a silent
// assumption.
type Amount int64

// MajorUnits constructs an Amount from a whole-number major-currency-
// unit value, e.g. MajorUnits(2_500_000) for "USD 2,500,000" ==
// Amount(250_000_000_00 minor units)... concretely 2_500_000 * 100
// cents. Exists so callers (and tests transcribing the blueprint's
// own examples) never need to hand-multiply by 100 themselves.
func MajorUnits(whole int64) Amount { return Amount(whole * 100) }

// MajorUnitsFraction constructs an Amount from a major-unit value plus
// a minor-unit remainder, e.g. MajorUnitsFraction(1_900_000, 50) for
// "USD 1,900,000.50".
func MajorUnitsFraction(whole int64, minor int64) Amount {
	sign := int64(1)
	if whole < 0 {
		sign = -1
	}
	return Amount(whole*100 + sign*minor)
}

// Add returns a + b.
func (a Amount) Add(b Amount) Amount { return a + b }

// Sub returns a - b.
func (a Amount) Sub(b Amount) Amount { return a - b }

// String renders the amount as a decimal major-unit string with
// exactly two minor-unit digits, e.g. Amount(250000000).String() ==
// "2500000.00". This is a display convenience only — it is never
// parsed back and never used inside a calculation.
func (a Amount) String() string {
	neg := a < 0
	v := int64(a)
	if neg {
		v = -v
	}
	whole := v / 100
	frac := v % 100
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%02d", sign, whole, frac)
}

// ---- ExchangeRate: also fixed-point, for the same reason as Amount ----

// RateScale is the fixed-point scale applied to every ExchangeRate:
// a Scaled value of RateScale means a rate of exactly 1.0.
const RateScale int64 = 1_000_000

// ExchangeRate is a fixed-point exchange rate (Scaled / RateScale),
// deliberately not a float64 for the same reproducibility reason
// documented on Amount above: an exchange_rate is itself an input to
// a calculation the blueprint requires to be exactly reproducible, so
// it must not carry binary-floating-point rounding either.
type ExchangeRate struct {
	Scaled int64 `json:"scaled"`
}

// NewExchangeRate builds a fixed-point ExchangeRate from a scaled
// integer (rate * RateScale). Callers converting from a decimal rate
// (e.g. 1 USD = 15,450.25 IDR) compute the scaled integer themselves
// so no float64 ever enters this package.
func NewExchangeRate(scaled int64) ExchangeRate { return ExchangeRate{Scaled: scaled} }

// UnitExchangeRate is the identity rate (1:1) — the correct value for
// a Calculation whose Currency needs no conversion.
func UnitExchangeRate() ExchangeRate { return ExchangeRate{Scaled: RateScale} }

// IsPositive reports whether r is a well-formed, positive rate. A
// zero or negative rate is never valid input to a calculation.
func (r ExchangeRate) IsPositive() bool { return r.Scaled > 0 }

// ---- EvidenceStatus: is this figure actually backed by evidence? ----

// EvidenceStatus reports whether an EvidenceBackedAmount is backed by
// at least one real evidence.Record EvidenceID. This is the mechanism
// behind the golden rule (blueprint §36): "When evidence is
// insufficient, the correct output is INSUFFICIENT_EVIDENCE — not a
// guessed conclusion." An amount of zero with zero evidence IDs is
// NOT the same fact as "evidence proves the loss was zero" — the
// former means nobody has evidenced this component at all, and this
// package refuses to let the two collapse into the same silent zero.
type EvidenceStatus string

const (
	// EvidenceStatusSupported means at least one EvidenceID backs this
	// amount, whatever the amount's numeric value (including zero).
	EvidenceStatusSupported EvidenceStatus = "SUPPORTED"
	// EvidenceStatusUnsupported means this amount carries NO evidence
	// at all. Downstream consumers must treat the amount as unproven,
	// never as a confirmed figure -- see Calculation.ReviewStatus and
	// Calculation.IsFullySupported.
	EvidenceStatusUnsupported EvidenceStatus = "UNSUPPORTED_NO_EVIDENCE"
)

// EvidenceBackedAmount pairs a monetary figure with the EvidenceIDs
// (from pkg/insurance/evidence.Record.EvidenceID) that support it.
// Every monetary figure this package produces traces back to specific
// evidence through structures built from this type.
type EvidenceBackedAmount struct {
	Amount      Amount   `json:"amount"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// NewEvidenceBackedAmount builds an EvidenceBackedAmount. Passing no
// evidenceIDs is legal (it is exactly the "no evidence at all" case
// this package is required to surface, not hide) but the resulting
// value's Status() will be EvidenceStatusUnsupported.
func NewEvidenceBackedAmount(amount Amount, evidenceIDs ...string) EvidenceBackedAmount {
	var ids []string
	if len(evidenceIDs) > 0 {
		ids = append([]string(nil), evidenceIDs...)
	}
	return EvidenceBackedAmount{Amount: amount, EvidenceIDs: ids}
}

// Status reports whether e carries any supporting evidence.
func (e EvidenceBackedAmount) Status() EvidenceStatus {
	if len(e.EvidenceIDs) == 0 {
		return EvidenceStatusUnsupported
	}
	return EvidenceStatusSupported
}

// mergeEvidenceIDs returns the deduplicated, sorted union of every
// EvidenceID across the given EvidenceBackedAmounts. Sorted output is
// what makes the resulting input_evidence slice deterministic
// regardless of Go's unordered map iteration or caller argument order
// -- required for the determinism the blueprint demands (§36/§37).
func mergeEvidenceIDs(amounts ...EvidenceBackedAmount) []string {
	seen := make(map[string]bool)
	for _, a := range amounts {
		for _, id := range a.EvidenceIDs {
			seen[id] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
