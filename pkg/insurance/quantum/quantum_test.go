package quantum

import (
	"reflect"
	"testing"
)

// TestComputeFormulaCleanNumbers verifies the §20 formula
//
//	GROSS LOSS + MITIGATION - SALVAGE - DEDUCTIBLE = INDICATIVE CLAIM VALUE
//
// with clean, hand-verifiable numbers:
//
//	1,000,000 + 50,000 - 200,000 - 100,000 = 750,000
func TestComputeFormulaCleanNumbers(t *testing.T) {
	in := ComputeInput{
		CalculationID: "CALC-CLEAN-1",
		GrossLoss:     NewEvidenceBackedAmount(MajorUnits(1_000_000), "EV-GROSS-1"),
		Mitigation:    NewEvidenceBackedAmount(MajorUnits(50_000), "EV-MIT-1"),
		Salvage:       NewEvidenceBackedAmount(MajorUnits(200_000), "EV-SALV-1"),
		Deductible:    NewEvidenceBackedAmount(MajorUnits(100_000), "EV-DED-1"),
		Currency:      "USD",
		ExchangeRate:  UnitExchangeRate(),
		RateSource:    "SAME_CURRENCY",
	}
	calc, err := Compute(in)
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	want := MajorUnits(750_000)
	if calc.IndicativeClaimValue != want {
		t.Fatalf("IndicativeClaimValue = %s, want %s", calc.IndicativeClaimValue, want)
	}
	if calc.Formula != FormulaIndicativeClaimValue {
		t.Fatalf("Formula = %q, want %q", calc.Formula, FormulaIndicativeClaimValue)
	}
	if calc.CalculationVersion != CurrentCalculationVersion {
		t.Fatalf("CalculationVersion = %q, want %q", calc.CalculationVersion, CurrentCalculationVersion)
	}
	if calc.ReviewStatus != ReviewStatusPending {
		t.Fatalf("ReviewStatus = %q, want %q (all operands were evidence-backed)", calc.ReviewStatus, ReviewStatusPending)
	}
	if !calc.IsFullySupported() {
		t.Fatalf("expected IsFullySupported() == true when every operand has evidence")
	}
	wantEvidence := []string{"EV-DED-1", "EV-GROSS-1", "EV-MIT-1", "EV-SALV-1"}
	if !reflect.DeepEqual(calc.InputEvidence, wantEvidence) {
		t.Fatalf("InputEvidence = %v, want %v", calc.InputEvidence, wantEvidence)
	}
}

// TestComputeZeroInvoiceEvidenceIsNotSilentZero proves the golden-rule
// requirement: a GrossLoss with NO supporting evidence at all must be
// reported as explicitly unsupported, never presented as if it were a
// confirmed "$0 loss, fully computed" result.
func TestComputeZeroInvoiceEvidenceIsNotSilentZero(t *testing.T) {
	in := ComputeInput{
		CalculationID: "CALC-NO-EVIDENCE-1",
		GrossLoss:     EvidenceBackedAmount{Amount: 0}, // no evidence IDs at all
		Mitigation:    NewEvidenceBackedAmount(0, "EV-MIT-1"),
		Salvage:       NewEvidenceBackedAmount(0, "EV-SALV-1"),
		Deductible:    NewEvidenceBackedAmount(0, "EV-DED-1"),
		Currency:      "USD",
		ExchangeRate:  UnitExchangeRate(),
		RateSource:    "SAME_CURRENCY",
	}
	calc, err := Compute(in)
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if calc.GrossLoss.Status() != EvidenceStatusUnsupported {
		t.Fatalf("GrossLoss.Status() = %q, want %q", calc.GrossLoss.Status(), EvidenceStatusUnsupported)
	}
	if calc.ReviewStatus != ReviewStatusHumanReviewRequired {
		t.Fatalf("ReviewStatus = %q, want %q -- a calc with no invoice evidence must never look like a settled, fully-computed figure", calc.ReviewStatus, ReviewStatusHumanReviewRequired)
	}
	if calc.IsFullySupported() {
		t.Fatalf("IsFullySupported() must be false when GrossLoss has no evidence")
	}
	// The arithmetic result (0) is still exposed -- VICE does not hide
	// numbers -- but nothing about the record may be read as "this
	// claim has zero loss, confirmed".
	if calc.IndicativeClaimValue != 0 {
		t.Fatalf("IndicativeClaimValue = %s, want 0 given all-zero evidenced operands", calc.IndicativeClaimValue)
	}
}

// TestComputeDeterminism proves blueprint §36/§37's "quantum
// calculation reproducible" requirement directly: the exact same
// ComputeInput, computed twice, must yield value-identical results.
func TestComputeDeterminism(t *testing.T) {
	in := ComputeInput{
		CalculationID: "CALC-DETERMINISM-1",
		GrossLoss:     NewEvidenceBackedAmount(MajorUnits(1_900_000), "EV-INV-002", "EV-INV-001"),
		Mitigation:    NewEvidenceBackedAmount(MajorUnits(10_000), "EV-MIT-001"),
		Salvage:       NewEvidenceBackedAmount(MajorUnits(300_000), "EV-SALV-001"),
		Deductible:    NewEvidenceBackedAmount(MajorUnits(100_000), "EV-POLICY-001"),
		Currency:      "USD",
		ExchangeRate:  UnitExchangeRate(),
		RateSource:    "SAME_CURRENCY",
	}
	first, err := Compute(in)
	if err != nil {
		t.Fatalf("first Compute returned error: %v", err)
	}
	second, err := Compute(in)
	if err != nil {
		t.Fatalf("second Compute returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Compute is not deterministic:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	// Run it a third time from an independently-constructed but
	// equal-value input to rule out any hidden dependency on shared
	// backing arrays.
	inCopy := ComputeInput{
		CalculationID: "CALC-DETERMINISM-1",
		GrossLoss:     NewEvidenceBackedAmount(MajorUnits(1_900_000), "EV-INV-002", "EV-INV-001"),
		Mitigation:    NewEvidenceBackedAmount(MajorUnits(10_000), "EV-MIT-001"),
		Salvage:       NewEvidenceBackedAmount(MajorUnits(300_000), "EV-SALV-001"),
		Deductible:    NewEvidenceBackedAmount(MajorUnits(100_000), "EV-POLICY-001"),
		Currency:      "USD",
		ExchangeRate:  UnitExchangeRate(),
		RateSource:    "SAME_CURRENCY",
	}
	third, err := Compute(inCopy)
	if err != nil {
		t.Fatalf("third Compute returned error: %v", err)
	}
	if !reflect.DeepEqual(first, third) {
		t.Fatalf("Compute is not deterministic across independently-built equal inputs:\nfirst: %#v\nthird: %#v", first, third)
	}
}

func TestComputeValidation(t *testing.T) {
	base := ComputeInput{
		CalculationID: "CALC-VALID-1",
		GrossLoss:     NewEvidenceBackedAmount(MajorUnits(100), "EV-1"),
		Currency:      "USD",
		ExchangeRate:  UnitExchangeRate(),
		RateSource:    "SAME_CURRENCY",
	}

	t.Run("empty calculation id", func(t *testing.T) {
		in := base
		in.CalculationID = ""
		if _, err := Compute(in); err != ErrEmptyCalculationID {
			t.Fatalf("err = %v, want %v", err, ErrEmptyCalculationID)
		}
	})
	t.Run("empty currency", func(t *testing.T) {
		in := base
		in.Currency = ""
		if _, err := Compute(in); err != ErrEmptyCurrency {
			t.Fatalf("err = %v, want %v", err, ErrEmptyCurrency)
		}
	})
	t.Run("empty rate source", func(t *testing.T) {
		in := base
		in.RateSource = ""
		if _, err := Compute(in); err != ErrEmptyRateSource {
			t.Fatalf("err = %v, want %v", err, ErrEmptyRateSource)
		}
	})
	t.Run("zero exchange rate", func(t *testing.T) {
		in := base
		in.ExchangeRate = ExchangeRate{}
		if _, err := Compute(in); err != ErrInvalidExchangeRate {
			t.Fatalf("err = %v, want %v", err, ErrInvalidExchangeRate)
		}
	})
	t.Run("negative exchange rate", func(t *testing.T) {
		in := base
		in.ExchangeRate = NewExchangeRate(-1)
		if _, err := Compute(in); err != ErrInvalidExchangeRate {
			t.Fatalf("err = %v, want %v", err, ErrInvalidExchangeRate)
		}
	})
	t.Run("negative gross loss", func(t *testing.T) {
		in := base
		in.GrossLoss = NewEvidenceBackedAmount(-1, "EV-1")
		if _, err := Compute(in); err != ErrNegativeAmount {
			t.Fatalf("err = %v, want %v", err, ErrNegativeAmount)
		}
	})
}

func TestMarkReviewed(t *testing.T) {
	calc := Calculation{ReviewStatus: ReviewStatusPending}
	reviewed := calc.MarkReviewed()
	if reviewed.ReviewStatus != ReviewStatusReviewed {
		t.Fatalf("ReviewStatus = %q, want %q", reviewed.ReviewStatus, ReviewStatusReviewed)
	}
	// MarkReviewed must not mutate the receiver (Calculation is a value
	// type; this pins that behaviour so a future refactor to a pointer
	// method would be caught).
	if calc.ReviewStatus != ReviewStatusPending {
		t.Fatalf("original Calculation was mutated: ReviewStatus = %q", calc.ReviewStatus)
	}
}

func TestIsKnownReviewStatus(t *testing.T) {
	for _, s := range []ReviewStatus{ReviewStatusPending, ReviewStatusHumanReviewRequired, ReviewStatusReviewed} {
		if !IsKnownReviewStatus(s) {
			t.Errorf("IsKnownReviewStatus(%q) = false, want true", s)
		}
	}
	if IsKnownReviewStatus("NOT_A_REAL_STATUS") {
		t.Errorf("IsKnownReviewStatus should reject an unmodelled status")
	}
}

func TestComputeDefaultsCalculationVersion(t *testing.T) {
	in := ComputeInput{
		CalculationID: "CALC-VERSION-DEFAULT",
		GrossLoss:     NewEvidenceBackedAmount(MajorUnits(1), "EV-1"),
		Currency:      "USD",
		ExchangeRate:  UnitExchangeRate(),
		RateSource:    "SAME_CURRENCY",
	}
	calc, err := Compute(in)
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if calc.CalculationVersion != CurrentCalculationVersion {
		t.Fatalf("CalculationVersion = %q, want default %q", calc.CalculationVersion, CurrentCalculationVersion)
	}

	in.CalculationVersion = "quantum-v2-experimental"
	calc2, err := Compute(in)
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if calc2.CalculationVersion != "quantum-v2-experimental" {
		t.Fatalf("CalculationVersion = %q, want explicit override to be respected", calc2.CalculationVersion)
	}
}
