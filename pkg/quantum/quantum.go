// Package quantum computes disputed amounts, and refuses to compute
// them when the inputs are not comparable.
//
// # The failure this package exists to prevent
//
// A cargo loaded at 15C in air and discharged at 20C in vacuum
// produces a difference of thousands of tonnes that is entirely an
// artefact of the measurement bases. Subtracting the two numbers is
// arithmetically correct and factually meaningless, and it is the
// single most common way a cargo claim is overstated.
//
// So a Quantum cannot be computed from two measurements until their
// BASES have been compared. Incompatible bases produce a refusal with
// the reason, not a number with a caveat -- a number with a caveat
// gets quoted without the caveat.
//
// # Every quantum carries four things
//
//	the figure          what the arithmetic gives
//	the assumptions     what had to be taken as true to get it
//	the missing inputs  what would change it
//	an alternative      the same question computed another way
//
// The alternative is the discipline. A single figure invites the
// reader to treat it as the answer; two figures computed on different
// defensible bases show the range the dispute is actually about.
package quantum

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrIncomparableBasis = errors.New("quantum: the measurements are not on comparable bases")
	ErrNoBasis           = errors.New("quantum: a measurement with no stated basis cannot be compared")
	ErrNoTolerance       = errors.New("quantum: no contractual tolerance was supplied")
	ErrUnitMismatch      = errors.New("quantum: the measurements are in different units")
	ErrNoPrice           = errors.New("quantum: a monetary quantum needs a price basis")
	ErrNoFX              = errors.New("quantum: a cross-currency quantum needs an FX basis")
	ErrWithinTolerance   = errors.New("quantum: the difference is within the contractual tolerance")
)

// Basis is how a quantity was measured. Two measurements are
// comparable only when their bases agree on every field that affects
// the number.
type Basis struct {
	// Method: shore tank, ship's figures, draft survey, flow meter,
	// weighbridge. Different methods have different systematic errors
	// and are not interchangeable.
	Method string `json:"method"`
	// TemperatureC is the reference temperature.
	TemperatureC *float64 `json:"temperature_c,omitempty"`
	// Density is the density used for a volume-to-mass conversion.
	Density *float64 `json:"density,omitempty"`
	// InVacuum distinguishes mass in vacuum from weight in air. The
	// difference is around 0.11% for a typical hydrocarbon -- small,
	// and larger than most contractual tolerances.
	InVacuum bool `json:"in_vacuum"`
	// Standard names the measurement standard applied.
	Standard string `json:"standard,omitempty"`
}

func (b Basis) Validate() error {
	if strings.TrimSpace(b.Method) == "" {
		return fmt.Errorf("%w: no method", ErrNoBasis)
	}
	return nil
}

// Comparable reports whether two bases may be differenced directly,
// and names every difference when they may not.
//
// It is deliberately strict. A missing temperature on one side is a
// difference, not a match: "not stated" is not "the same as the other
// one", and treating it as one is how an unstated basis becomes an
// assumed identical basis.
func (b Basis) Comparable(o Basis) (bool, []string) {
	var diffs []string
	if !strings.EqualFold(b.Method, o.Method) {
		diffs = append(diffs, fmt.Sprintf("method: %q vs %q", b.Method, o.Method))
	}
	if b.InVacuum != o.InVacuum {
		diffs = append(diffs, fmt.Sprintf("mass basis: %s vs %s",
			vacuumLabel(b.InVacuum), vacuumLabel(o.InVacuum)))
	}
	switch {
	case b.TemperatureC == nil && o.TemperatureC == nil:
		diffs = append(diffs, "temperature: not stated on either side")
	case b.TemperatureC == nil || o.TemperatureC == nil:
		diffs = append(diffs, "temperature: stated on one side only")
	case math.Abs(*b.TemperatureC-*o.TemperatureC) > 0.01:
		diffs = append(diffs, fmt.Sprintf("temperature: %.2fC vs %.2fC",
			*b.TemperatureC, *o.TemperatureC))
	}
	switch {
	case b.Density == nil && o.Density == nil:
		// Not always required: a direct mass measurement needs none.
	case b.Density == nil || o.Density == nil:
		diffs = append(diffs, "density: stated on one side only")
	case math.Abs(*b.Density-*o.Density) > 1e-6:
		diffs = append(diffs, fmt.Sprintf("density: %.5f vs %.5f", *b.Density, *o.Density))
	}
	if !strings.EqualFold(b.Standard, o.Standard) {
		diffs = append(diffs, fmt.Sprintf("standard: %q vs %q", b.Standard, o.Standard))
	}
	sort.Strings(diffs)
	return len(diffs) == 0, diffs
}

func vacuumLabel(v bool) string {
	if v {
		return "in vacuum"
	}
	return "in air"
}

// Measurement is one observed quantity.
type Measurement struct {
	ID    string    `json:"id"`
	Value float64   `json:"value"`
	Unit  string    `json:"unit"`
	Basis Basis     `json:"basis"`
	At    time.Time `json:"at"`

	EvidenceRefs []string `json:"evidence_refs"`
}

func (m Measurement) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("quantum: a measurement has no id")
	}
	if strings.TrimSpace(m.Unit) == "" {
		return errors.New("quantum: a measurement with no unit is a number")
	}
	if len(m.EvidenceRefs) == 0 {
		return fmt.Errorf("quantum: measurement %s cites no evidence", m.ID)
	}
	return m.Basis.Validate()
}

// Tolerance is the contractual allowance.
type Tolerance struct {
	// Percent is the allowance as a percentage of the contract
	// quantity.
	Percent float64 `json:"percent"`
	// Clause is where it comes from. A tolerance with no clause is
	// somebody's recollection.
	Clause string `json:"clause"`
	// AtWhoseOption records who elects within the tolerance, which
	// decides whether a shortfall inside it is a shortfall at all.
	AtWhoseOption string `json:"at_whose_option,omitempty"`
}

func (t Tolerance) Validate() error {
	if t.Percent < 0 {
		return errors.New("quantum: a negative tolerance")
	}
	if strings.TrimSpace(t.Clause) == "" {
		return fmt.Errorf("%w: no contractual clause is cited", ErrNoTolerance)
	}
	return nil
}

// Price is the basis for converting a quantity to money.
type Price struct {
	PerUnit  float64 `json:"per_unit"`
	Currency string  `json:"currency"`
	// Basis names the pricing mechanism: contract price, market price
	// on a date, replacement cost. The three give very different
	// answers and the dispute is often about which applies.
	Basis string `json:"basis"`
	// AsOf is the date the price is taken at.
	AsOf         time.Time `json:"as_of"`
	EvidenceRefs []string  `json:"evidence_refs"`
}

func (p Price) Validate() error {
	if p.PerUnit <= 0 {
		return fmt.Errorf("%w: no unit price", ErrNoPrice)
	}
	if strings.TrimSpace(p.Currency) == "" || strings.TrimSpace(p.Basis) == "" {
		return fmt.Errorf("%w: a price needs a currency and a stated basis", ErrNoPrice)
	}
	if len(p.EvidenceRefs) == 0 {
		return fmt.Errorf("%w: the price cites no evidence", ErrNoPrice)
	}
	return nil
}

// FX is a currency conversion basis.
type FX struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Rate   float64   `json:"rate"`
	AsOf   time.Time `json:"as_of"`
	Source string    `json:"source"`
}

func (f FX) Validate() error {
	if f.Rate <= 0 || f.From == "" || f.To == "" {
		return fmt.Errorf("%w: incomplete", ErrNoFX)
	}
	if strings.TrimSpace(f.Source) == "" || f.AsOf.IsZero() {
		return fmt.Errorf("%w: an FX basis needs a source and a date; the same pair on two "+
			"dates is two different numbers", ErrNoFX)
	}
	return nil
}

// Request is a quantum computation.
type Request struct {
	// Loaded and Discharged are the two measurements.
	Loaded, Discharged Measurement
	// ContractQuantity is what was contracted for, which is what the
	// tolerance is a percentage OF -- not of the loaded figure.
	ContractQuantity float64
	Tolerance        Tolerance

	// Price and FX are optional; without them the quantum is in
	// quantity terms only.
	Price *Price
	FX    *FX
	// TargetCurrency, when set, requires FX if it differs from the
	// price currency.
	TargetCurrency string
}

// Quantum is the computed amount with everything that qualifies it.
type Quantum struct {
	// QuantityDifference is discharged minus loaded, negative for a
	// shortfall.
	QuantityDifference float64 `json:"quantity_difference"`
	Unit               string  `json:"unit"`

	// ToleranceAllowance is the absolute allowance.
	ToleranceAllowance float64 `json:"tolerance_allowance"`
	// ExcessOverTolerance is the part of the shortfall that exceeds
	// the allowance -- which is the number a claim is actually for.
	ExcessOverTolerance float64 `json:"excess_over_tolerance"`
	WithinTolerance     bool    `json:"within_tolerance"`

	// Amount is the monetary quantum, when a price was supplied.
	Amount   *float64 `json:"amount,omitempty"`
	Currency string   `json:"currency,omitempty"`

	Assumptions   []string `json:"assumptions"`
	MissingInputs []string `json:"missing_inputs"`
	// Alternative is the same question computed another defensible
	// way, so the reader sees a range rather than a figure.
	Alternative *Alternative `json:"alternative,omitempty"`

	EvidenceRefs []string `json:"evidence_refs"`
}

// Alternative is a second, differently-based computation.
type Alternative struct {
	Basis    string   `json:"basis"`
	Amount   *float64 `json:"amount,omitempty"`
	Quantity float64  `json:"quantity"`
	Why      string   `json:"why"`
}

// Compute produces the quantum, or refuses.
//
// The refusal for incomparable bases comes FIRST, before any
// arithmetic, and it returns no number at all. A number with a caveat
// gets quoted without the caveat.
func Compute(r Request) (Quantum, error) {
	if err := r.Loaded.Validate(); err != nil {
		return Quantum{}, fmt.Errorf("quantum: loaded: %w", err)
	}
	if err := r.Discharged.Validate(); err != nil {
		return Quantum{}, fmt.Errorf("quantum: discharged: %w", err)
	}
	if !strings.EqualFold(r.Loaded.Unit, r.Discharged.Unit) {
		return Quantum{}, fmt.Errorf("%w: %s vs %s", ErrUnitMismatch, r.Loaded.Unit, r.Discharged.Unit)
	}
	if err := r.Tolerance.Validate(); err != nil {
		return Quantum{}, err
	}
	if r.ContractQuantity <= 0 {
		return Quantum{}, errors.New("quantum: no contract quantity; the tolerance is a " +
			"percentage of the contract quantity, not of the loaded figure")
	}

	// The check the whole package is about.
	comparable, diffs := r.Loaded.Basis.Comparable(r.Discharged.Basis)
	if !comparable {
		return Quantum{}, fmt.Errorf("%w: %s. The difference between these figures is not "+
			"a quantity difference until the bases are reconciled",
			ErrIncomparableBasis, strings.Join(diffs, "; "))
	}

	q := Quantum{
		QuantityDifference: r.Discharged.Value - r.Loaded.Value,
		Unit:               r.Loaded.Unit,
		ToleranceAllowance: r.ContractQuantity * r.Tolerance.Percent / 100,
		EvidenceRefs:       mergeRefs(r),
	}
	shortfall := -q.QuantityDifference
	if shortfall <= q.ToleranceAllowance {
		q.WithinTolerance = true
		q.ExcessOverTolerance = 0
	} else {
		q.ExcessOverTolerance = shortfall - q.ToleranceAllowance
	}

	q.Assumptions = append(q.Assumptions,
		fmt.Sprintf("both figures are on the %s basis %s at the same temperature and standard",
			r.Loaded.Basis.Method, vacuumLabel(r.Loaded.Basis.InVacuum)),
		fmt.Sprintf("the tolerance of %.3f%% is taken from %s and applied to the contract "+
			"quantity of %.3f %s", r.Tolerance.Percent, r.Tolerance.Clause,
			r.ContractQuantity, r.Loaded.Unit))
	if r.Tolerance.AtWhoseOption != "" {
		q.Assumptions = append(q.Assumptions, fmt.Sprintf(
			"the tolerance is at %s's option, so a shortfall inside it may be an election "+
				"rather than a loss", r.Tolerance.AtWhoseOption))
	} else {
		q.MissingInputs = append(q.MissingInputs,
			"the contract does not state at whose option the tolerance is exercised; a "+
				"shortfall within it may be an election rather than a loss")
	}
	if r.Loaded.Basis.Density == nil {
		q.MissingInputs = append(q.MissingInputs,
			"no density was recorded, so a volumetric reconciliation cannot be performed")
	}

	// Money.
	if r.Price != nil {
		if err := r.Price.Validate(); err != nil {
			return Quantum{}, err
		}
		amountCcy := r.Price.Currency
		amount := q.ExcessOverTolerance * r.Price.PerUnit
		if r.TargetCurrency != "" && !strings.EqualFold(r.TargetCurrency, r.Price.Currency) {
			if r.FX == nil {
				return Quantum{}, fmt.Errorf("%w: %s -> %s", ErrNoFX,
					r.Price.Currency, r.TargetCurrency)
			}
			if err := r.FX.Validate(); err != nil {
				return Quantum{}, err
			}
			if !strings.EqualFold(r.FX.From, r.Price.Currency) ||
				!strings.EqualFold(r.FX.To, r.TargetCurrency) {
				return Quantum{}, fmt.Errorf("%w: the supplied rate converts %s to %s",
					ErrNoFX, r.FX.From, r.FX.To)
			}
			amount *= r.FX.Rate
			amountCcy = r.TargetCurrency
			q.Assumptions = append(q.Assumptions, fmt.Sprintf(
				"converted at %.6f %s/%s from %s as of %s; a different conversion date "+
					"gives a different figure",
				r.FX.Rate, r.FX.To, r.FX.From, r.FX.Source, r.FX.AsOf.Format("2006-01-02")))
		}
		q.Amount = &amount
		q.Currency = amountCcy
		q.Assumptions = append(q.Assumptions, fmt.Sprintf(
			"priced at %.4f %s per %s on the %s basis as of %s",
			r.Price.PerUnit, r.Price.Currency, r.Loaded.Unit, r.Price.Basis,
			r.Price.AsOf.Format("2006-01-02")))

		// The alternative: the whole shortfall rather than only the
		// excess over tolerance. Both are defensible readings of a
		// contract and they differ by the allowance.
		altQty := shortfall
		altAmount := altQty * r.Price.PerUnit
		if r.TargetCurrency != "" && r.FX != nil &&
			!strings.EqualFold(r.TargetCurrency, r.Price.Currency) {
			altAmount *= r.FX.Rate
		}
		q.Alternative = &Alternative{
			Basis: "full shortfall, no tolerance deduction", Quantity: altQty,
			Amount: &altAmount,
			Why: "whether the tolerance is deducted from the claim or merely triggers it " +
				"is a question of construction; the two readings differ by " +
				fmt.Sprintf("%.3f %s", q.ToleranceAllowance, q.Unit)}
	} else {
		q.MissingInputs = append(q.MissingInputs,
			"no price basis was supplied, so the quantum is stated in quantity terms only")
	}

	sort.Strings(q.Assumptions)
	sort.Strings(q.MissingInputs)
	return q, nil
}

// Reconcile attempts to bring two incomparable measurements onto a
// common basis, and reports exactly what it could and could not do.
//
// It exists because the honest answer to an incomparable pair is often
// "these can be reconciled, here is how" rather than "no". What it
// never does is reconcile silently: the conversion becomes a stated
// assumption on the quantum.
func Reconcile(loaded, discharged Measurement) (Measurement, []string, error) {
	if comparable, _ := loaded.Basis.Comparable(discharged.Basis); comparable {
		return discharged, nil, nil
	}
	var applied []string
	out := discharged

	// The air/vacuum conversion, which is the one with a standard
	// factor. Everything else needs data this function does not have.
	if loaded.Basis.InVacuum != discharged.Basis.InVacuum {
		const airVacuumFactor = 0.0011 // ~0.11% for a typical hydrocarbon
		if loaded.Basis.InVacuum {
			out.Value = discharged.Value * (1 + airVacuumFactor)
		} else {
			out.Value = discharged.Value * (1 - airVacuumFactor)
		}
		out.Basis.InVacuum = loaded.Basis.InVacuum
		applied = append(applied, fmt.Sprintf(
			"converted %s to %s using a factor of %.4f; this is a STANDARD factor for a "+
				"typical hydrocarbon and is not the cargo's measured factor",
			vacuumLabel(discharged.Basis.InVacuum), vacuumLabel(loaded.Basis.InVacuum),
			airVacuumFactor))
	}

	if still, remaining := loaded.Basis.Comparable(out.Basis); !still {
		return Measurement{}, applied, fmt.Errorf(
			"%w: after applying %d conversion(s), these remain: %s. Reconciling them needs "+
				"data this computation does not have",
			ErrIncomparableBasis, len(applied), strings.Join(remaining, "; "))
	}
	return out, applied, nil
}

// Report renders the quantum.
func (q Quantum) Report() string {
	var b strings.Builder
	b.WriteString("QUANTUM\n")
	fmt.Fprintf(&b, "  quantity difference: %.3f %s\n", q.QuantityDifference, q.Unit)
	fmt.Fprintf(&b, "  tolerance allowance: %.3f %s\n", q.ToleranceAllowance, q.Unit)
	if q.WithinTolerance {
		b.WriteString("  the difference is WITHIN the contractual tolerance\n")
	} else {
		fmt.Fprintf(&b, "  excess over tolerance: %.3f %s\n", q.ExcessOverTolerance, q.Unit)
	}
	if q.Amount != nil {
		fmt.Fprintf(&b, "  amount: %.2f %s\n", *q.Amount, q.Currency)
	}
	if q.Alternative != nil {
		if q.Alternative.Amount != nil {
			fmt.Fprintf(&b, "  ALTERNATIVE (%s): %.3f %s = %.2f %s\n",
				q.Alternative.Basis, q.Alternative.Quantity, q.Unit,
				*q.Alternative.Amount, q.Currency)
		}
		fmt.Fprintf(&b, "    %s\n", q.Alternative.Why)
	}
	b.WriteString("  assumptions:\n")
	for _, a := range q.Assumptions {
		fmt.Fprintf(&b, "    - %s\n", a)
	}
	if len(q.MissingInputs) > 0 {
		b.WriteString("  missing inputs:\n")
		for _, m := range q.MissingInputs {
			fmt.Fprintf(&b, "    - %s\n", m)
		}
	}
	return b.String()
}

func mergeRefs(r Request) []string {
	seen := map[string]bool{}
	var out []string
	add := func(xs []string) {
		for _, x := range xs {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	add(r.Loaded.EvidenceRefs)
	add(r.Discharged.EvidenceRefs)
	if r.Price != nil {
		add(r.Price.EvidenceRefs)
	}
	sort.Strings(out)
	return out
}
