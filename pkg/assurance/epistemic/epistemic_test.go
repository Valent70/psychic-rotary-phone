package epistemic

import (
	"errors"
	"strings"
	"testing"
)

func coverage() Figure {
	return Figure{
		Name: "real-world weighted redaction coverage", Value: 88, Unit: "%",
		Status: Estimate,
		Basis: "structural coverage weighted by a judgement of how common each structure is " +
			"in real documents",
		Caveats: []string{
			"the prevalence weights are judgements, not counts",
			"three refused structures are COMMON in real documents; a real population " +
				"would land there in bulk",
		},
	}
}

// TestTheZeroStatusIsEstimate. An unlabelled number is a guess until
// somebody says otherwise, and the zero value must agree.
func TestTheZeroStatusIsEstimate(t *testing.T) {
	var s Status
	if s != Estimate {
		t.Fatalf("the zero status is %s", s)
	}
	var f Figure
	if f.Status != Estimate {
		t.Fatal("an unpopulated figure does not default to ESTIMATE")
	}
}

// TestAFigureCannotBeRenderedWithoutItsStatus. This is the property
// the package exists for: no formatting path produces a bare number.
func TestAFigureCannotBeRenderedWithoutItsStatus(t *testing.T) {
	f := coverage()
	for _, s := range []string{f.Render(), f.Describe()} {
		if !strings.Contains(s, "ESTIMATE") {
			t.Fatalf("a rendering omitted the status: %q", s)
		}
	}
	if !strings.Contains(f.Describe(), "must not be quoted as a measurement") {
		t.Fatalf("the description does not warn about the estimate:\n%s", f.Describe())
	}
	if !strings.Contains(f.Describe(), "COMMON in real documents") {
		t.Fatalf("the description drops the caveats:\n%s", f.Describe())
	}
}

// TestAnEstimateCannotBecomeAMeasurementByRepetition.
func TestAnEstimateCannotBecomeAMeasurementByRepetition(t *testing.T) {
	f := coverage()
	if _, err := f.Promote(Measured, "", "", ""); !errors.Is(err, ErrUpgrade) {
		t.Fatalf("an estimate became a measurement with no population: %v", err)
	}
	if _, err := f.Promote(Validated, "10,000 real documents", "Acme", ""); !errors.Is(err, ErrUpgrade) {
		t.Fatalf("a status jump was permitted: %v", err)
	}
	m, err := f.Promote(Measured, "10,000 documents from a customer corpus", "", "")
	if err != nil {
		t.Fatalf("a properly supported promotion was refused: %v", err)
	}
	if m.Status != Measured {
		t.Fatalf("status = %s", m.Status)
	}
	// And the caveats survive the promotion; a measurement of a
	// partial thing is still partial.
	if len(m.Caveats) != len(f.Caveats) {
		t.Fatal("promotion dropped the caveats")
	}
}

// TestEachStatusDemandsItsOwnSupport.
func TestEachStatusDemandsItsOwnSupport(t *testing.T) {
	cases := []struct {
		f    Figure
		want error
	}{
		{Figure{Name: "x", Status: Measured, Basis: "b"}, ErrNoPopulation},
		{Figure{Name: "x", Status: Validated, Basis: "b", Population: "p"}, ErrNoValidator},
		{Figure{Name: "x", Status: ProductionProven, Basis: "b", Population: "p",
			Validator: "v"}, ErrNoPeriod},
		{Figure{Name: "x", Status: Estimate}, ErrNoBasis},
	}
	for _, c := range cases {
		if err := c.f.Validate(); !errors.Is(err, c.want) {
			t.Fatalf("%s at %s gave %v, want %v", c.f.Name, c.f.Status, err, c.want)
		}
	}
	ok := Figure{Name: "x", Status: ProductionProven, Basis: "b", Population: "p",
		Validator: "v", Period: "90 days"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a fully supported figure was refused: %v", err)
	}
}

// TestOnlyProductionProvenMayBeStatedBare.
func TestOnlyProductionProvenMayBeStatedBare(t *testing.T) {
	for _, s := range Statuses() {
		want := s == ProductionProven
		if s.PublishableAsFact() != want {
			t.Fatalf("%s.PublishableAsFact() = %v", s, s.PublishableAsFact())
		}
	}
}

// TestADerivedFigureInheritsTheWeakestStatus. A conclusion drawn from
// an estimate and a measurement is an estimate; taking the stronger,
// or averaging, is the mistake Weakest exists to prevent.
func TestADerivedFigureInheritsTheWeakestStatus(t *testing.T) {
	est := coverage()
	meas := Figure{Name: "structural coverage", Value: 74, Unit: "%", Status: Measured,
		Basis: "17 of 23 variants accepted", Population: "the 23-variant structural corpus"}
	w, err := Weakest(est, meas)
	if err != nil {
		t.Fatal(err)
	}
	if w != Estimate {
		t.Fatalf("weakest of ESTIMATE and MEASURED is %s", w)
	}
	if _, err := Weakest(); err == nil {
		t.Fatal("Weakest of nothing returned a status")
	}
	// An invalid input must not be silently skipped.
	bad := Figure{Name: "x", Status: Measured, Basis: "b"}
	if _, err := Weakest(meas, bad); err == nil {
		t.Fatal("Weakest accepted an unsupported figure")
	}
}

// TestNonFiniteValuesAreRefused. A figure that diverged must not
// render as a number.
func TestNonFiniteValuesAreRefused(t *testing.T) {
	z := 0.0
	for _, v := range []float64{z / z, 1 / z, -1 / z} {
		f := Figure{Name: "x", Value: v, Status: Estimate, Basis: "b"}
		if err := f.Validate(); err == nil {
			t.Fatalf("a non-finite value validated: %v", v)
		}
	}
}

// TestEveryStatusRoundTrips.
func TestEveryStatusRoundTrips(t *testing.T) {
	for _, s := range Statuses() {
		got, err := Parse(s.String())
		if err != nil || got != s {
			t.Fatalf("Parse(%s) = %v, %v", s, got, err)
		}
	}
	if _, err := Parse("PROVEN"); !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("an invented status parsed: %v", err)
	}
	if len(Statuses()) != 4 {
		t.Fatalf("%d statuses", len(Statuses()))
	}
}
