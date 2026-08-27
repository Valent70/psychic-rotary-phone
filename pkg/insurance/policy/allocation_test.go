package policy

import (
	"errors"
	"math/rand"
	"testing"

	"veriqo/pkg/insurance/quantum"
)

func defaultAllocationVersion() Version {
	return Version{
		PolicyID: "POL-1", VersionID: "POL-1-V1", PolicyNumber: "PN-1",
		Insurer: "Primary Insurer", Insured: "Insured Co",
		EffectiveFrom: 1, EffectiveTo: 5000, Kind: KindOriginal,
	}
}

func versionWithParticipants(coInsurers, reinsurers []Participant) Version {
	v := defaultAllocationVersion()
	v.Participants = append(append([]Participant{}, coInsurers...), reinsurers...)
	return v
}

func TestAllocateCoInsuranceSingleInsurer(t *testing.T) {
	v := defaultAllocationVersion() // no participants: insurer bears 100%
	total := quantum.MajorUnits(100_000)
	allocs, err := v.AllocateCoInsurance(total)
	if err != nil {
		t.Fatalf("AllocateCoInsurance: %v", err)
	}
	if len(allocs) != 1 {
		t.Fatalf("expected 1 allocation (insurer only), got %d", len(allocs))
	}
	if allocs[0].Amount != total {
		t.Fatalf("expected insurer to receive the full %s, got %s", total, allocs[0].Amount)
	}
	if allocs[0].Role != AllocationRoleInsurerPrimary {
		t.Fatalf("expected AllocationRoleInsurerPrimary, got %s", allocs[0].Role)
	}
}

func TestAllocateCoInsuranceMultipleCoInsurersSumsExactly(t *testing.T) {
	v := versionWithParticipants([]Participant{
		{PartyID: "PTY-CO-1", Role: ParticipantCoInsurer, BasisPoints: 30_000},
		{PartyID: "PTY-CO-2", Role: ParticipantCoInsurer, BasisPoints: 25_000},
	}, nil)
	total := quantum.MajorUnits(1_000_000) // 1,000,000.00 in minor units, deliberately not evenly divisible
	allocs, err := v.AllocateCoInsurance(total)
	if err != nil {
		t.Fatalf("AllocateCoInsurance: %v", err)
	}
	var sum quantum.Amount
	for _, a := range allocs {
		sum += a.Amount
	}
	if sum != total {
		t.Fatalf("allocations must sum to EXACTLY the total payment: expected %s, got %s", total, sum)
	}
	if len(allocs) != 3 { // 2 co-insurers + insurer's own primary share
		t.Fatalf("expected 3 allocations, got %d: %+v", len(allocs), allocs)
	}
}

func TestAllocateCoInsuranceRejectsNonPositivePayment(t *testing.T) {
	v := defaultAllocationVersion()
	if _, err := v.AllocateCoInsurance(0); !errors.Is(err, ErrNonPositivePayment) {
		t.Fatalf("expected ErrNonPositivePayment for 0, got %v", err)
	}
	if _, err := v.AllocateCoInsurance(-1); !errors.Is(err, ErrNonPositivePayment) {
		t.Fatalf("expected ErrNonPositivePayment for negative, got %v", err)
	}
}

func TestAllocateReinsuranceSumsExactlyAndComputesRetention(t *testing.T) {
	v := versionWithParticipants(nil, []Participant{
		{PartyID: "PTY-RE-1", Role: ParticipantReinsurer, BasisPoints: 40_000, Basis: BasisTreaty},
		{PartyID: "PTY-RE-2", Role: ParticipantReinsurer, BasisPoints: 15_000, Basis: BasisFacultative},
	})
	insurerPrimary := quantum.MajorUnits(777_777)
	allocs, err := v.AllocateReinsurance(insurerPrimary)
	if err != nil {
		t.Fatalf("AllocateReinsurance: %v", err)
	}
	var sum quantum.Amount
	var retained quantum.Amount
	for _, a := range allocs {
		sum += a.Amount
		if a.Role == AllocationRoleInsurerNetRetained {
			retained = a.Amount
		}
	}
	if sum != insurerPrimary {
		t.Fatalf("reinsurance allocations must sum to exactly the insurer's primary amount: expected %s, got %s",
			insurerPrimary, sum)
	}
	if retained <= 0 || retained >= insurerPrimary {
		t.Fatalf("expected a real net-retained amount strictly between 0 and the primary amount, got %s", retained)
	}
}

// TestAllocateByBasisPointsAlwaysSumsToTotal is a randomised property
// test: for many random totals and share splits, the largest-remainder
// allocation always sums to EXACTLY the total, with no float rounding
// drift and no off-by-one — the "money/allocation hardening" the
// mandate's §41 demands.
func TestAllocateByBasisPointsAlwaysSumsToTotal(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 500; trial++ {
		total := quantum.Amount(rng.Int63n(10_000_000_00) + 1)
		n := rng.Intn(5) + 1
		shares := map[string]int64{}
		remaining := int64(ParticipationScale)
		for i := 0; i < n && remaining > 0; i++ {
			bp := rng.Int63n(remaining) + 1
			if i == n-1 {
				bp = remaining
			}
			shares[string(rune('A'+i))] = bp
			remaining -= bp
		}
		got := allocateByBasisPoints(total, shares)
		var sum quantum.Amount
		for _, amt := range got {
			sum += amt
			if amt < 0 {
				t.Fatalf("trial %d: negative allocation %s", trial, amt)
			}
		}
		if sum != total {
			t.Fatalf("trial %d: shares=%v total=%s got sum=%s (must be exact)", trial, shares, total, sum)
		}
	}
}

// TestAllocationIsDeterministicAcrossRepeatedCalls proves the same
// inputs produce byte-identical (here: value-identical, in stable
// order) output every time — required because Go map iteration order
// is randomised and this function must not leak that randomness into
// its result.
func TestAllocationIsDeterministicAcrossRepeatedCalls(t *testing.T) {
	v := versionWithParticipants([]Participant{
		{PartyID: "PTY-CO-1", Role: ParticipantCoInsurer, BasisPoints: 33_333},
		{PartyID: "PTY-CO-2", Role: ParticipantCoInsurer, BasisPoints: 33_333},
		{PartyID: "PTY-CO-3", Role: ParticipantCoInsurer, BasisPoints: 33_333},
	}, nil)
	total := quantum.MajorUnits(100)
	first, err := v.AllocateCoInsurance(total)
	if err != nil {
		t.Fatalf("AllocateCoInsurance: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := v.AllocateCoInsurance(total)
		if err != nil {
			t.Fatalf("AllocateCoInsurance (rerun %d): %v", i, err)
		}
		if len(again) != len(first) {
			t.Fatalf("rerun %d: length changed", i)
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("rerun %d: allocation %d changed: %+v vs %+v", i, j, first[j], again[j])
			}
		}
	}
}
