package quantum

import "testing"

func TestMajorUnits(t *testing.T) {
	if got, want := MajorUnits(2_500_000), Amount(250_000_000); got != want {
		t.Fatalf("MajorUnits(2_500_000) = %d, want %d", got, want)
	}
	if got, want := MajorUnits(0), Amount(0); got != want {
		t.Fatalf("MajorUnits(0) = %d, want %d", got, want)
	}
}

func TestMajorUnitsFraction(t *testing.T) {
	if got, want := MajorUnitsFraction(1_900_000, 50), Amount(190_000_050); got != want {
		t.Fatalf("MajorUnitsFraction(1_900_000, 50) = %d, want %d", got, want)
	}
	if got, want := MajorUnitsFraction(-5, 25), Amount(-525); got != want {
		t.Fatalf("MajorUnitsFraction(-5, 25) = %d, want %d", got, want)
	}
}

func TestAmountAddSub(t *testing.T) {
	a := MajorUnits(1_000_000)
	b := MajorUnits(400_000)
	if got, want := a.Add(b), MajorUnits(1_400_000); got != want {
		t.Fatalf("Add = %v, want %v", got, want)
	}
	if got, want := a.Sub(b), MajorUnits(600_000); got != want {
		t.Fatalf("Sub = %v, want %v", got, want)
	}
}

func TestAmountString(t *testing.T) {
	cases := []struct {
		a    Amount
		want string
	}{
		{MajorUnits(2_500_000), "2500000.00"},
		{MajorUnitsFraction(1_900_000, 5), "1900000.05"},
		{Amount(0), "0.00"},
		{Amount(-150), "-1.50"},
	}
	for _, c := range cases {
		if got := c.a.String(); got != c.want {
			t.Errorf("Amount(%d).String() = %q, want %q", c.a, got, c.want)
		}
	}
}

func TestExchangeRateUnitAndPositive(t *testing.T) {
	u := UnitExchangeRate()
	if u.Scaled != RateScale {
		t.Fatalf("UnitExchangeRate().Scaled = %d, want %d", u.Scaled, RateScale)
	}
	if !u.IsPositive() {
		t.Fatalf("UnitExchangeRate() should be positive")
	}
	if (ExchangeRate{}).IsPositive() {
		t.Fatalf("zero-value ExchangeRate should not be positive")
	}
	if NewExchangeRate(-1).IsPositive() {
		t.Fatalf("negative ExchangeRate should not be positive")
	}
}

func TestEvidenceBackedAmountStatus(t *testing.T) {
	withEvidence := NewEvidenceBackedAmount(MajorUnits(100), "EV-001")
	if withEvidence.Status() != EvidenceStatusSupported {
		t.Fatalf("expected SUPPORTED, got %s", withEvidence.Status())
	}
	withoutEvidence := NewEvidenceBackedAmount(MajorUnits(0))
	if withoutEvidence.Status() != EvidenceStatusUnsupported {
		t.Fatalf("expected UNSUPPORTED_NO_EVIDENCE, got %s", withoutEvidence.Status())
	}
	// Zero amount + zero evidence must ALSO be unsupported: a zero
	// amount is not automatically "proven zero".
	zeroButAsserted := EvidenceBackedAmount{Amount: 0}
	if zeroButAsserted.Status() != EvidenceStatusUnsupported {
		t.Fatalf("zero amount with no evidence must be UNSUPPORTED_NO_EVIDENCE, got %s", zeroButAsserted.Status())
	}
}

func TestMergeEvidenceIDsDeduplicatesAndSorts(t *testing.T) {
	a := NewEvidenceBackedAmount(MajorUnits(1), "EV-003", "EV-001")
	b := NewEvidenceBackedAmount(MajorUnits(2), "EV-001", "EV-002")
	got := mergeEvidenceIDs(a, b)
	want := []string{"EV-001", "EV-002", "EV-003"}
	if len(got) != len(want) {
		t.Fatalf("mergeEvidenceIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mergeEvidenceIDs = %v, want %v", got, want)
		}
	}
}

func TestMergeEvidenceIDsEmpty(t *testing.T) {
	got := mergeEvidenceIDs(EvidenceBackedAmount{}, EvidenceBackedAmount{})
	if got != nil {
		t.Fatalf("mergeEvidenceIDs with no evidence anywhere = %v, want nil", got)
	}
}
