package quantum

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

func shoreTank(temp float64, inVacuum bool) Basis {
	return Basis{Method: "shore tank", TemperatureC: f(temp), Density: f(0.8654),
		InVacuum: inVacuum, Standard: "API MPMS Ch.12"}
}

func meas(id string, v float64, b Basis) Measurement {
	return Measurement{ID: id, Value: v, Unit: "MT", Basis: b, At: t0,
		EvidenceRefs: []string{"ev:" + id}}
}

func request() Request {
	return Request{
		Loaded:           meas("loading-survey", 60000, shoreTank(15, false)),
		Discharged:       meas("discharge-survey", 58200, shoreTank(15, false)),
		ContractQuantity: 60000,
		Tolerance: Tolerance{Percent: 0.5, Clause: "clause 4(b)",
			AtWhoseOption: "the seller"},
	}
}

// TestIncomparableBasesProduceARefusalNotANumber.
//
// This is the whole package. A number with a caveat gets quoted
// without the caveat.
func TestIncomparableBasesProduceARefusalNotANumber(t *testing.T) {
	r := request()
	r.Discharged.Basis = shoreTank(20, true) // different temperature AND mass basis

	q, err := Compute(r)
	if !errors.Is(err, ErrIncomparableBasis) {
		t.Fatalf("incomparable bases produced %v", err)
	}
	if q.QuantityDifference != 0 || q.Amount != nil {
		t.Fatal("A NUMBER WAS RETURNED ALONGSIDE THE REFUSAL")
	}
	// The refusal must name every difference, or the analyst cannot
	// act on it.
	for _, want := range []string{"temperature", "mass basis"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name the %s difference: %v", want, err)
		}
	}
}

// TestAnUnstatedTemperatureIsADifferenceNotAMatch.
//
// "Not stated" is not "the same as the other one", and treating it as
// one is how an unstated basis becomes an assumed identical basis.
func TestAnUnstatedTemperatureIsADifferenceNotAMatch(t *testing.T) {
	a := shoreTank(15, false)
	b := shoreTank(15, false)
	b.TemperatureC = nil

	comparable, diffs := a.Comparable(b)
	if comparable {
		t.Fatal("a stated temperature was matched against an unstated one")
	}
	if !strings.Contains(strings.Join(diffs, " "), "stated on one side only") {
		t.Fatalf("the difference is not described: %v", diffs)
	}

	// And neither side stating it is also a difference: two unknowns
	// are not a match.
	a.TemperatureC = nil
	comparable, diffs = a.Comparable(b)
	if comparable {
		t.Fatal("two unstated temperatures were treated as matching")
	}
	if !strings.Contains(strings.Join(diffs, " "), "not stated on either side") {
		t.Fatalf("the difference is not described: %v", diffs)
	}
}

// TestTheAirVacuumDifferenceExceedsATypicalTolerance.
//
// The point of the fixture: 0.11% is small, and a 0.5% tolerance on
// 60,000 MT is 300 MT while the air/vacuum artefact is about 64 MT --
// so the basis error is a fifth of the entire allowance.
func TestTheAirVacuumDifferenceExceedsATypicalTolerance(t *testing.T) {
	loaded := meas("loading", 60000, shoreTank(15, true))
	discharged := meas("discharge", 58200, shoreTank(15, false))

	reconciled, applied, err := Reconcile(loaded, discharged)
	if err != nil {
		t.Fatalf("a reconcilable pair was refused: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("conversions applied = %v", applied)
	}
	artefact := math.Abs(reconciled.Value - discharged.Value)
	if artefact < 50 {
		t.Fatalf("the conversion moved the figure by only %.2f MT", artefact)
	}
	// It is now comparable, and the conversion is a STATED assumption.
	if ok, diffs := loaded.Basis.Comparable(reconciled.Basis); !ok {
		t.Fatalf("still incomparable after reconciliation: %v", diffs)
	}
	if !strings.Contains(applied[0], "STANDARD factor") {
		t.Fatalf("the conversion does not disclose that it used a standard factor: %s", applied[0])
	}
	if !strings.Contains(applied[0], "not the cargo's measured factor") {
		t.Fatalf("the conversion overstates itself: %s", applied[0])
	}
}

// TestReconcileRefusesWhatItCannotConvert. It never reconciles
// silently.
func TestReconcileRefusesWhatItCannotConvert(t *testing.T) {
	loaded := meas("loading", 60000, shoreTank(15, false))
	discharged := meas("discharge", 58200, shoreTank(20, false)) // temperature differs
	_, applied, err := Reconcile(loaded, discharged)
	if !errors.Is(err, ErrIncomparableBasis) {
		t.Fatalf("an unconvertible pair was reconciled: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("conversions were applied and then abandoned: %v", applied)
	}
	if !strings.Contains(err.Error(), "temperature") {
		t.Fatalf("the refusal does not name what remains: %v", err)
	}
}

// TestToleranceIsAPercentageOfTheContractQuantity, not of the loaded
// figure. The difference matters when the loaded figure is itself
// disputed.
func TestToleranceIsAPercentageOfTheContractQuantity(t *testing.T) {
	r := request()
	r.ContractQuantity = 50000 // less than the loaded 60,000
	q, err := Compute(r)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(q.ToleranceAllowance-250) > 1e-9 {
		t.Fatalf("allowance = %.3f, want 0.5%% of 50,000 = 250", q.ToleranceAllowance)
	}
	if _, err := Compute(Request{Loaded: r.Loaded, Discharged: r.Discharged,
		Tolerance: r.Tolerance}); err == nil {
		t.Fatal("a quantum was computed with no contract quantity")
	}
}

// TestOnlyTheExcessOverToleranceIsClaimed, with the other reading
// offered as the alternative.
func TestOnlyTheExcessOverToleranceIsClaimed(t *testing.T) {
	r := request()
	r.Price = &Price{PerUnit: 620, Currency: "USD", Basis: "contract price",
		AsOf: t0, EvidenceRefs: []string{"ev:contract"}}

	q, err := Compute(r)
	if err != nil {
		t.Fatal(err)
	}
	// 1,800 short, 300 allowed, 1,500 excess.
	if math.Abs(q.ExcessOverTolerance-1500) > 1e-9 {
		t.Fatalf("excess = %.3f, want 1500", q.ExcessOverTolerance)
	}
	if q.Amount == nil || math.Abs(*q.Amount-1500*620) > 1e-6 {
		t.Fatalf("amount = %v", q.Amount)
	}
	if q.Alternative == nil {
		t.Fatal("no alternative computation was offered")
	}
	if math.Abs(q.Alternative.Quantity-1800) > 1e-9 {
		t.Fatalf("the alternative quantity is %.3f, want the full 1800", q.Alternative.Quantity)
	}
	// The two readings must differ by exactly the allowance.
	if math.Abs((q.Alternative.Quantity-q.ExcessOverTolerance)-q.ToleranceAllowance) > 1e-9 {
		t.Fatal("the two readings do not differ by the allowance")
	}
}

// TestAShortfallInsideTheToleranceIsSaidSo.
func TestAShortfallInsideTheToleranceIsSaidSo(t *testing.T) {
	r := request()
	r.Discharged.Value = 59800 // 200 short, allowance 300
	q, err := Compute(r)
	if err != nil {
		t.Fatal(err)
	}
	if !q.WithinTolerance {
		t.Fatal("a shortfall inside the tolerance was not reported as such")
	}
	if q.ExcessOverTolerance != 0 {
		t.Fatalf("excess = %.3f inside the tolerance", q.ExcessOverTolerance)
	}
	if !strings.Contains(q.Report(), "WITHIN the contractual tolerance") {
		t.Fatalf("the report does not say so:\n%s", q.Report())
	}
}

// TestAToleranceAtTheSellersOptionBecomesAnAssumption.
//
// A shortfall inside a seller's-option tolerance may be an election
// rather than a loss, and a quantum that did not say so would be
// presenting a contractual right as a shortfall.
func TestAToleranceAtTheSellersOptionBecomesAnAssumption(t *testing.T) {
	q, err := Compute(request())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(q.Assumptions, " ")
	if !strings.Contains(joined, "at the seller's option") {
		t.Fatalf("the option is not recorded: %v", q.Assumptions)
	}
	if !strings.Contains(joined, "election rather than a loss") {
		t.Fatalf("the consequence is not stated: %v", q.Assumptions)
	}

	// And when the contract is silent, that becomes a missing input.
	r := request()
	r.Tolerance.AtWhoseOption = ""
	q, err = Compute(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(q.MissingInputs, " "), "at whose option") {
		t.Fatalf("the silence is not recorded as missing: %v", q.MissingInputs)
	}
}

// TestATolerianceWithNoClauseIsRefused. A tolerance with no clause is
// somebody's recollection.
func TestAToleranceWithNoClauseIsRefused(t *testing.T) {
	r := request()
	r.Tolerance.Clause = ""
	if _, err := Compute(r); !errors.Is(err, ErrNoTolerance) {
		t.Fatalf("an uncited tolerance was applied: %v", err)
	}
}

// TestACrossCurrencyQuantumNeedsAnFXBasis.
func TestACrossCurrencyQuantumNeedsAnFXBasis(t *testing.T) {
	r := request()
	r.Price = &Price{PerUnit: 620, Currency: "USD", Basis: "contract price",
		AsOf: t0, EvidenceRefs: []string{"ev:contract"}}
	r.TargetCurrency = "EUR"

	if _, err := Compute(r); !errors.Is(err, ErrNoFX) {
		t.Fatalf("a cross-currency quantum was computed with no rate: %v", err)
	}

	r.FX = &FX{From: "USD", To: "EUR", Rate: 0.92, AsOf: t0, Source: "ECB reference rate"}
	q, err := Compute(r)
	if err != nil {
		t.Fatal(err)
	}
	if q.Currency != "EUR" {
		t.Fatalf("currency = %s", q.Currency)
	}
	if math.Abs(*q.Amount-1500*620*0.92) > 1e-6 {
		t.Fatalf("amount = %v", *q.Amount)
	}
	if !strings.Contains(strings.Join(q.Assumptions, " "), "different conversion date") {
		t.Fatalf("the FX date sensitivity is not stated: %v", q.Assumptions)
	}
}

// TestAnFXRateForTheWrongPairIsRefused.
func TestAnFXRateForTheWrongPairIsRefused(t *testing.T) {
	r := request()
	r.Price = &Price{PerUnit: 620, Currency: "USD", Basis: "contract price",
		AsOf: t0, EvidenceRefs: []string{"ev:contract"}}
	r.TargetCurrency = "EUR"
	r.FX = &FX{From: "GBP", To: "EUR", Rate: 1.17, AsOf: t0, Source: "ECB"}
	if _, err := Compute(r); !errors.Is(err, ErrNoFX) {
		t.Fatalf("a rate for the wrong pair was applied: %v", err)
	}
}

// TestAnUndatedFXRateIsRefused: the same pair on two dates is two
// different numbers.
func TestAnUndatedFXRateIsRefused(t *testing.T) {
	fx := FX{From: "USD", To: "EUR", Rate: 0.92, Source: "ECB"}
	if err := fx.Validate(); err == nil {
		t.Fatal("an undated FX rate was accepted")
	}
	fx.AsOf = t0
	fx.Source = ""
	if err := fx.Validate(); err == nil {
		t.Fatal("an unsourced FX rate was accepted")
	}
}

// TestAQuantumWithNoPriceIsStatedInQuantityTerms, and says so.
func TestAQuantumWithNoPriceIsStatedInQuantityTerms(t *testing.T) {
	q, err := Compute(request())
	if err != nil {
		t.Fatal(err)
	}
	if q.Amount != nil {
		t.Fatal("a monetary amount was produced with no price basis")
	}
	if !strings.Contains(strings.Join(q.MissingInputs, " "), "no price basis") {
		t.Fatalf("the absence is not recorded: %v", q.MissingInputs)
	}
}

// TestAPriceNeedsAStatedBasis. Contract price, market price on a date
// and replacement cost give very different answers.
func TestAPriceNeedsAStatedBasis(t *testing.T) {
	p := Price{PerUnit: 620, Currency: "USD", AsOf: t0, EvidenceRefs: []string{"ev:c"}}
	if err := p.Validate(); !errors.Is(err, ErrNoPrice) {
		t.Fatalf("a price with no basis was accepted: %v", err)
	}
	p.Basis = "contract price"
	p.EvidenceRefs = nil
	if err := p.Validate(); !errors.Is(err, ErrNoPrice) {
		t.Fatalf("a price citing no evidence was accepted: %v", err)
	}
}

// TestUnitMismatchIsRefused.
func TestUnitMismatchIsRefused(t *testing.T) {
	r := request()
	r.Discharged.Unit = "BBL"
	if _, err := Compute(r); !errors.Is(err, ErrUnitMismatch) {
		t.Fatalf("measurements in different units were differenced: %v", err)
	}
}

// TestAMeasurementNeedsAMethodAndEvidence.
func TestAMeasurementNeedsAMethodAndEvidence(t *testing.T) {
	m := meas("x", 100, Basis{})
	if err := m.Validate(); !errors.Is(err, ErrNoBasis) {
		t.Fatalf("a measurement with no method was accepted: %v", err)
	}
	m = meas("x", 100, shoreTank(15, false))
	m.EvidenceRefs = nil
	if err := m.Validate(); err == nil {
		t.Fatal("a measurement citing no evidence was accepted")
	}
	m = meas("x", 100, shoreTank(15, false))
	m.Unit = ""
	if err := m.Validate(); err == nil {
		t.Fatal("a measurement with no unit was accepted")
	}
}

// TestDifferentMethodsAreNotComparable. Shore tank and ship's figures
// have different systematic errors.
func TestDifferentMethodsAreNotComparable(t *testing.T) {
	a := shoreTank(15, false)
	b := a
	b.Method = "ship's figures"
	if ok, diffs := a.Comparable(b); ok {
		t.Fatal("shore tank and ship's figures were treated as comparable")
	} else if !strings.Contains(strings.Join(diffs, " "), "method") {
		t.Fatalf("the difference is not named: %v", diffs)
	}
}

// TestTheReportCarriesTheAssumptionsAndTheAlternative.
func TestTheReportCarriesTheAssumptionsAndTheAlternative(t *testing.T) {
	r := request()
	r.Price = &Price{PerUnit: 620, Currency: "USD", Basis: "contract price",
		AsOf: t0, EvidenceRefs: []string{"ev:contract"}}
	q, err := Compute(r)
	if err != nil {
		t.Fatal(err)
	}
	rep := q.Report()
	for _, want := range []string{"assumptions:", "ALTERNATIVE", "excess over tolerance"} {
		if !strings.Contains(rep, want) {
			t.Errorf("the report omits %q:\n%s", want, rep)
		}
	}
}

// TestComputationIsDeterministic.
func TestComputationIsDeterministic(t *testing.T) {
	r := request()
	r.Price = &Price{PerUnit: 620, Currency: "USD", Basis: "contract price",
		AsOf: t0, EvidenceRefs: []string{"ev:contract"}}
	first, err := Compute(r)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := Compute(r)
		if err != nil {
			t.Fatal(err)
		}
		if got.Report() != first.Report() {
			t.Fatal("the quantum varies between runs")
		}
	}
}
