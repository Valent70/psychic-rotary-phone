package quantum

import (
	"reflect"
	"testing"
)

// TestQuantumContradictionBlueprintWorkedExample reproduces blueprint
// §21's own worked example EXACTLY:
//
//	Claimant:  USD 2,500,000
//	Invoice evidence: USD 1,900,000
//	Salvage:   USD   300,000
//	Deductible: USD  100,000
//
// and asserts the blueprint's own stated result:
//
//	claimed          = 2,500,000
//	supported_gross  = 1,900,000
//	salvage          =   300,000
//	deductible       =   100,000
//	unresolved       =   200,000
//
// See the derivation of the Unresolved formula in the doc comment on
// DetectDiscrepancy (discrepancy.go): unresolved is computed, real
// arithmetic over these four numbers -- not a hard-coded constant.
func TestQuantumContradictionBlueprintWorkedExample(t *testing.T) {
	claimed := NewEvidenceBackedAmount(MajorUnits(2_500_000), "EV-CLAIMANT-STATEMENT")
	supportedGross := NewEvidenceBackedAmount(MajorUnits(1_900_000), "EV-INVOICE-001")
	salvage := NewEvidenceBackedAmount(MajorUnits(300_000), "EV-SALVAGE-SALE")
	deductible := NewEvidenceBackedAmount(MajorUnits(100_000), "EV-POLICY-DEDUCTIBLE-CLAUSE")

	got, err := DetectDiscrepancy("QD-21-WORKED-EXAMPLE", claimed, supportedGross, salvage, deductible, "USD")
	if err != nil {
		t.Fatalf("DetectDiscrepancy returned error: %v", err)
	}

	if got.Claimed.Amount != MajorUnits(2_500_000) {
		t.Errorf("Claimed = %s, want %s", got.Claimed.Amount, MajorUnits(2_500_000))
	}
	if got.SupportedGross.Amount != MajorUnits(1_900_000) {
		t.Errorf("SupportedGross = %s, want %s", got.SupportedGross.Amount, MajorUnits(1_900_000))
	}
	if got.Salvage.Amount != MajorUnits(300_000) {
		t.Errorf("Salvage = %s, want %s", got.Salvage.Amount, MajorUnits(300_000))
	}
	if got.Deductible.Amount != MajorUnits(100_000) {
		t.Errorf("Deductible = %s, want %s", got.Deductible.Amount, MajorUnits(100_000))
	}
	if want := MajorUnits(200_000); got.Unresolved != want {
		t.Fatalf("Unresolved = %s, want %s (blueprint §21's own stated figure)", got.Unresolved, want)
	}
	if !got.HasUnresolvedGap() {
		t.Fatalf("HasUnresolvedGap() = false, want true")
	}
	if got.ReviewStatus != ReviewStatusHumanReviewRequired {
		t.Fatalf("ReviewStatus = %q, want %q -- VICE must never pick a number itself", got.ReviewStatus, ReviewStatusHumanReviewRequired)
	}
	// VICE never overwrites either party's figure with the other, or
	// with the computed indicative value -- both original figures must
	// still be readable, unaltered, off the result.
	if got.Claimed.Amount == got.SupportedGross.Amount {
		t.Fatalf("test setup invalid: claimed and supported_gross must differ to exercise the contradiction path")
	}
}

// TestDetectDiscrepancyDeterminism proves the same-inputs-same-output
// requirement for the discrepancy path too.
func TestDetectDiscrepancyDeterminism(t *testing.T) {
	claimed := NewEvidenceBackedAmount(MajorUnits(2_500_000), "EV-CLAIMANT-STATEMENT")
	supportedGross := NewEvidenceBackedAmount(MajorUnits(1_900_000), "EV-INVOICE-001")
	salvage := NewEvidenceBackedAmount(MajorUnits(300_000), "EV-SALVAGE-SALE")
	deductible := NewEvidenceBackedAmount(MajorUnits(100_000), "EV-POLICY-DEDUCTIBLE-CLAUSE")

	first, err := DetectDiscrepancy("QD-DETERMINISM-1", claimed, supportedGross, salvage, deductible, "USD")
	if err != nil {
		t.Fatalf("first DetectDiscrepancy returned error: %v", err)
	}
	second, err := DetectDiscrepancy("QD-DETERMINISM-1", claimed, supportedGross, salvage, deductible, "USD")
	if err != nil {
		t.Fatalf("second DetectDiscrepancy returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("DetectDiscrepancy is not deterministic:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

// TestAdversarialQuantumGreaterThanInvoice is blueprint §35's
// adversarial case #13, "Quantum greater than invoice": the asserted
// quantum figure exceeds what the invoice evidence supports. VICE
// must surface the gap, not silently accept the claimant's larger
// figure nor silently cap it to the invoice figure.
func TestAdversarialQuantumGreaterThanInvoice(t *testing.T) {
	// A different set of numbers from the §21 worked example, to prove
	// the formula generalises rather than being pinned to one example.
	claimed := NewEvidenceBackedAmount(MajorUnits(500_000), "EV-CLAIMANT-QUANTUM")
	invoiceSupported := NewEvidenceBackedAmount(MajorUnits(300_000), "EV-INVOICE-042")
	salvage := NewEvidenceBackedAmount(MajorUnits(20_000), "EV-SALVAGE-042")
	deductible := NewEvidenceBackedAmount(MajorUnits(10_000), "EV-DEDUCTIBLE-042")

	if claimed.Amount <= invoiceSupported.Amount {
		t.Fatalf("test setup invalid: this case requires quantum > invoice")
	}

	disc, err := DetectDiscrepancy("QD-ADV-13", claimed, invoiceSupported, salvage, deductible, "USD")
	if err != nil {
		t.Fatalf("DetectDiscrepancy returned error: %v", err)
	}

	// gap = 500,000 - 300,000 = 200,000; unresolved = 200,000 - 20,000 - 10,000 = 170,000
	wantUnresolved := MajorUnits(170_000)
	if disc.Unresolved != wantUnresolved {
		t.Fatalf("Unresolved = %s, want %s", disc.Unresolved, wantUnresolved)
	}
	if !disc.HasUnresolvedGap() {
		t.Fatalf("HasUnresolvedGap() = false, want true when claimed quantum exceeds invoice evidence")
	}
	// The claimant's larger figure must never silently become "the"
	// value -- both figures remain independently readable.
	if disc.Claimed.Amount != claimed.Amount {
		t.Errorf("Claimed was altered: got %s, want %s", disc.Claimed.Amount, claimed.Amount)
	}
	if disc.SupportedGross.Amount != invoiceSupported.Amount {
		t.Errorf("SupportedGross was altered: got %s, want %s", disc.SupportedGross.Amount, invoiceSupported.Amount)
	}
	if disc.ReviewStatus != ReviewStatusHumanReviewRequired {
		t.Fatalf("ReviewStatus = %q, want %q", disc.ReviewStatus, ReviewStatusHumanReviewRequired)
	}

	// Also cross-check via an independent Calculation over the
	// evidence-supported figures alone (§20 formula), to show the two
	// engines agree on the evidence-only indicative value: this is the
	// figure VICE can defend from evidence, distinct from Unresolved.
	calc, err := Compute(ComputeInput{
		CalculationID: "CALC-ADV-13-SUPPORTED",
		GrossLoss:     invoiceSupported,
		Salvage:       salvage,
		Deductible:    deductible,
		Currency:      "USD",
		ExchangeRate:  UnitExchangeRate(),
		RateSource:    "SAME_CURRENCY",
	})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	// 300,000 - 20,000 - 10,000 = 270,000
	if want := MajorUnits(270_000); calc.IndicativeClaimValue != want {
		t.Fatalf("evidence-only IndicativeClaimValue = %s, want %s", calc.IndicativeClaimValue, want)
	}
	// Sanity: by construction (Unresolved = claimed - supported_gross -
	// salvage - deductible), the claimed figure must always be exactly
	// reconstructible as supported_gross + Unresolved + salvage +
	// deductible. This is the algebraic identity behind the Unresolved
	// formula, checked here against the actual computed value rather
	// than just asserted in a comment.
	reconstructedClaimed := invoiceSupported.Amount.Add(disc.Unresolved).Add(salvage.Amount).Add(deductible.Amount)
	if reconstructedClaimed != claimed.Amount {
		t.Fatalf("supported_gross (%s) + unresolved (%s) + salvage (%s) + deductible (%s) = %s, want claimed (%s)",
			invoiceSupported.Amount, disc.Unresolved, salvage.Amount, deductible.Amount, reconstructedClaimed, claimed.Amount)
	}
}

func TestDetectDiscrepancyValidation(t *testing.T) {
	claimed := NewEvidenceBackedAmount(MajorUnits(1), "EV-1")
	supported := NewEvidenceBackedAmount(MajorUnits(1), "EV-2")
	salvage := NewEvidenceBackedAmount(0)
	deductible := NewEvidenceBackedAmount(0)

	if _, err := DetectDiscrepancy("", claimed, supported, salvage, deductible, "USD"); err != ErrEmptyDiscrepancyID {
		t.Fatalf("err = %v, want %v", err, ErrEmptyDiscrepancyID)
	}
	if _, err := DetectDiscrepancy("QD-1", claimed, supported, salvage, deductible, ""); err != ErrEmptyCurrency {
		t.Fatalf("err = %v, want %v", err, ErrEmptyCurrency)
	}
	negative := NewEvidenceBackedAmount(-1, "EV-NEG")
	if _, err := DetectDiscrepancy("QD-1", negative, supported, salvage, deductible, "USD"); err != ErrNegativeAmount {
		t.Fatalf("err = %v, want %v", err, ErrNegativeAmount)
	}
}

// TestDetectDiscrepancyNoGapWhenFiguresAgree confirms a degenerate but
// important case: when the claimant's figure and the evidence-
// supported figure are identical, there is no disputed gross-loss gap
// left once salvage/deductible are netted -- Unresolved should reflect
// that (non-positive), not manufacture a phantom dispute.
func TestDetectDiscrepancyNoGapWhenFiguresAgree(t *testing.T) {
	agreed := NewEvidenceBackedAmount(MajorUnits(1_000_000), "EV-AGREED")
	salvage := NewEvidenceBackedAmount(MajorUnits(50_000), "EV-SALV")
	deductible := NewEvidenceBackedAmount(MajorUnits(20_000), "EV-DED")

	disc, err := DetectDiscrepancy("QD-NO-GAP", agreed, agreed, salvage, deductible, "USD")
	if err != nil {
		t.Fatalf("DetectDiscrepancy returned error: %v", err)
	}
	// gap = 0, unresolved = 0 - 50,000 - 20,000 = -70,000 (negative:
	// the undisputed reductions exceed the (zero) disputed gap).
	if disc.HasUnresolvedGap() {
		t.Fatalf("HasUnresolvedGap() = true, want false when claimed == supported_gross")
	}
	if want := MajorUnits(-70_000); disc.Unresolved != want {
		t.Fatalf("Unresolved = %s, want %s", disc.Unresolved, want)
	}
}
