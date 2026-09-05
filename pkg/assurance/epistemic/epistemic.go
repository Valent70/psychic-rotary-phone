// Package epistemic separates four things that a percentage sign hides.
//
// A figure in an assurance report can rest on four completely
// different foundations, and printing all four the same way is how a
// guess becomes a guarantee by being repeated:
//
//	ESTIMATE           somebody's judgement. No measurement was taken.
//	MEASURED           counted against a real population, by us.
//	VALIDATED          counted, and an independent party confirmed the
//	                   method and the count.
//	PRODUCTION_PROVEN  held up in production, over a stated period,
//	                   with its exceptions recorded.
//
// These are not confidence levels. They name WHERE THE NUMBER CAME
// FROM, which is why they cannot be interconverted: no amount of
// re-running an estimate makes it a measurement, and no amount of
// internal measurement makes it validated.
//
// VERIQO's own coverage figure is the worked example. 88% weighted
// redaction coverage is an ESTIMATE, because the prevalence weights
// are judgements and the corpus is VERIQO's own. Publishing it as
// "88%" alone would be false in exactly the way this package exists to
// prevent -- and the type system here makes the bare form unavailable:
// a Figure renders with its status or not at all.
package epistemic

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrUnknownStatus = errors.New("epistemic: unknown status")
	ErrUpgrade       = errors.New("epistemic: a figure cannot be upgraded without new evidence of the required kind")
	ErrNoBasis       = errors.New("epistemic: a figure states no basis")
	ErrNoPopulation  = errors.New("epistemic: a measured figure names no population")
	ErrNoValidator   = errors.New("epistemic: a validated figure names no independent validator")
	ErrNoPeriod      = errors.New("epistemic: a production-proven figure states no period")
)

// Status is where a number came from.
type Status int

const (
	// Estimate: a judgement. The zero value, deliberately -- an
	// unlabelled number is a guess until somebody says otherwise.
	Estimate Status = iota
	// Measured: counted against a real population by VERIQO.
	Measured
	// Validated: counted, with an independent party confirming the
	// method and the result.
	Validated
	// ProductionProven: observed to hold in production over a stated
	// period, with exceptions recorded.
	ProductionProven
)

var names = map[Status]string{
	Estimate: "ESTIMATE", Measured: "MEASURED", Validated: "VALIDATED",
	ProductionProven: "PRODUCTION_PROVEN",
}

func (s Status) String() string {
	if n, ok := names[s]; ok {
		return n
	}
	return fmt.Sprintf("Status(%d)", int(s))
}

func (s Status) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }

func (s Status) Valid() bool { _, ok := names[s]; return ok }

// Statuses returns every status, weakest first.
func Statuses() []Status {
	out := make([]Status, 0, len(names))
	for s := range names {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func Parse(s string) (Status, error) {
	for k, v := range names {
		if strings.EqualFold(v, strings.TrimSpace(s)) {
			return k, nil
		}
	}
	return Estimate, fmt.Errorf("%w: %q", ErrUnknownStatus, s)
}

// PublishableAsFact reports whether a figure at this status may be
// stated without its qualifier in customer-facing material.
//
// Only PRODUCTION_PROVEN may, and even then the period travels with
// it. Everything else must carry its status, which is why Figure has
// no method that returns the number alone.
func (s Status) PublishableAsFact() bool { return s == ProductionProven }

// Figure is a number with its provenance.
//
// There is deliberately no accessor that returns the value without the
// status. A caller that wants the raw float must reach for Value() and
// will find its name says what it is giving up.
type Figure struct {
	// Name is what is being measured.
	Name string `json:"name"`
	// Value is the number.
	Value float64 `json:"value"`
	// Unit is "%" or "documents" or whatever makes the value readable.
	Unit string `json:"unit,omitempty"`
	// Status is where it came from.
	Status Status `json:"status"`
	// Basis says how it was arrived at, in a sentence somebody can
	// disagree with. Required at every status, including ESTIMATE --
	// especially at ESTIMATE.
	Basis string `json:"basis"`
	// Population names what was counted, for MEASURED and above. An
	// unpopulated measurement is an estimate wearing a lab coat.
	Population string `json:"population,omitempty"`
	// SampleSize, where one applies. Zero means not applicable or not
	// stated, and Describe says which.
	SampleSize int `json:"sample_size,omitempty"`
	// Validator names the independent party, for VALIDATED and above.
	Validator string `json:"validator,omitempty"`
	// Period, for PRODUCTION_PROVEN.
	Period string `json:"period,omitempty"`
	// At is when the figure was produced.
	At time.Time `json:"at,omitempty"`
	// Caveats are what the figure does not cover. A figure with no
	// caveats is read as universal.
	Caveats []string `json:"caveats,omitempty"`
}

// Validate refuses a figure whose status outruns its support.
func (f Figure) Validate() error {
	if strings.TrimSpace(f.Name) == "" {
		return errors.New("epistemic: a figure has no name")
	}
	if !f.Status.Valid() {
		return fmt.Errorf("%w: %v", ErrUnknownStatus, f.Status)
	}
	if math.IsNaN(f.Value) || math.IsInf(f.Value, 0) {
		return fmt.Errorf("epistemic: %s has a non-finite value", f.Name)
	}
	if strings.TrimSpace(f.Basis) == "" {
		return fmt.Errorf("%w: %s", ErrNoBasis, f.Name)
	}
	if f.Status >= Measured && strings.TrimSpace(f.Population) == "" {
		return fmt.Errorf("%w: %s is %s", ErrNoPopulation, f.Name, f.Status)
	}
	if f.Status >= Validated && strings.TrimSpace(f.Validator) == "" {
		return fmt.Errorf("%w: %s is %s", ErrNoValidator, f.Name, f.Status)
	}
	if f.Status == ProductionProven && strings.TrimSpace(f.Period) == "" {
		return fmt.Errorf("%w: %s", ErrNoPeriod, f.Name)
	}
	return nil
}

// Promote raises a figure's status, and refuses to do so on the
// strength of anything but evidence of the kind that status names.
//
// The signature carries the required evidence as parameters rather
// than as a struct precisely so that a caller cannot promote by
// setting a field.
func (f Figure) Promote(to Status, population, validator, period string) (Figure, error) {
	if !to.Valid() {
		return Figure{}, fmt.Errorf("%w: %v", ErrUnknownStatus, to)
	}
	if to <= f.Status {
		return Figure{}, fmt.Errorf("%w: %s is already %s", ErrUpgrade, f.Name, f.Status)
	}
	if to != f.Status+1 {
		return Figure{}, fmt.Errorf("%w: %s is %s and the promotion targets %s; each step "+
			"needs its own evidence", ErrUpgrade, f.Name, f.Status, to)
	}
	out := f
	out.Status = to
	if strings.TrimSpace(population) != "" {
		out.Population = population
	}
	if strings.TrimSpace(validator) != "" {
		out.Validator = validator
	}
	if strings.TrimSpace(period) != "" {
		out.Period = period
	}
	if err := out.Validate(); err != nil {
		return Figure{}, fmt.Errorf("%w: %v", ErrUpgrade, err)
	}
	return out, nil
}

// Render is the only way a figure reaches a reader, and it always
// carries the status.
//
// A caller that wants the bare number can still read the field -- Go
// has no way to prevent that -- but nothing in this package will
// format one for them, so a bare figure cannot arrive in a report by
// accident.
func (f Figure) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s%s (%s)", f.Name, trim(f.Value), f.Unit, f.Status)
	if f.Status == Estimate {
		b.WriteString(" -- not measured")
	}
	if f.Population != "" {
		fmt.Fprintf(&b, " over %s", f.Population)
	}
	if f.SampleSize > 0 {
		fmt.Fprintf(&b, ", n=%d", f.SampleSize)
	}
	if f.Validator != "" {
		fmt.Fprintf(&b, ", validated by %s", f.Validator)
	}
	if f.Period != "" {
		fmt.Fprintf(&b, ", over %s", f.Period)
	}
	return b.String()
}

// Describe renders the figure with everything qualifying it.
func (f Figure) Describe() string {
	var b strings.Builder
	b.WriteString(f.Render())
	b.WriteString("\n    basis: " + f.Basis + "\n")
	for _, c := range f.Caveats {
		b.WriteString("    caveat: " + c + "\n")
	}
	if f.Status == Estimate {
		b.WriteString("    this figure is a judgement. It has never been counted against a " +
			"real population, and it must not be quoted as a measurement.\n")
	}
	if !f.Status.PublishableAsFact() {
		b.WriteString("    it may not be stated without this qualifier in customer-facing " +
			"material.\n")
	}
	return b.String()
}

func trim(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// Weakest returns the weakest status among figures, which is what a
// derived number inherits.
//
// A conclusion drawn from an estimate and a measurement is an
// estimate. Averaging the statuses, or taking the stronger, is the
// mistake this function exists to make unavailable.
func Weakest(fs ...Figure) (Status, error) {
	if len(fs) == 0 {
		return Estimate, errors.New("epistemic: no figures")
	}
	w := ProductionProven
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			return Estimate, err
		}
		if f.Status < w {
			w = f.Status
		}
	}
	return w, nil
}
