package maritime

import "testing"

// TestOwnershipType_LegalBeneficialControlNeverMix is the exact
// adversarial proof both audits ask for and neither had yet: record
// DIFFERENT percentages under LEGAL, BENEFICIAL, and CONTROL for the
// SAME owner/target pair on the SAME graph, then prove
// AllOwnersInChain returns a DIFFERENT effective percent per
// OwnershipType — i.e. the traversal never silently sums or mixes
// ownership kinds it wasn't asked about, closing "Legal/Beneficial/
// Control ownership sudah dipisah via OwnershipType, tetapi belum ada
// test eksplisit yang membuktikan hasil traversal Legal != Beneficial
// != Control untuk graf yang sama dengan data berbeda per jenis."
func TestOwnershipType_LegalBeneficialControlNeverMix(t *testing.T) {
	r := NewRegistry()
	owner := mustRegister(t, r, KindBeneficialOwner, "PERSON_X")
	vessel := mustRegister(t, r, KindVessel, "IMO_MIXED")

	og := NewOwnershipGraph(r)
	// Deliberately distinct percentages per type so a bug that mixes
	// or sums across types is unmissable in the assertions below.
	must(t, og.RecordOwnership(owner.ID, vessel.ID, 0.30, OwnershipLegal, 1))
	must(t, og.RecordOwnership(owner.ID, vessel.ID, 0.70, OwnershipBeneficial, 1))
	must(t, og.RecordOwnership(owner.ID, vessel.ID, 1.00, OwnershipControl, 1))

	cases := []struct {
		ownType OwnershipType
		want    float64
	}{
		{OwnershipLegal, 0.30},
		{OwnershipBeneficial, 0.70},
		{OwnershipControl, 1.00},
	}
	for _, tc := range cases {
		got := og.AllOwnersInChain(vessel.ID, tc.ownType, 0.0, 5)
		if len(got) != 1 {
			t.Fatalf("%s: expected exactly 1 owner, got %d: %+v", tc.ownType, len(got), got)
		}
		if got[0].EffectivePercent != tc.want {
			t.Errorf("%s: EffectivePercent = %v, want %v (traversal leaked another OwnershipType's data)", tc.ownType, got[0].EffectivePercent, tc.want)
		}
	}

	// A type that was never recorded for this pair at all (ECONOMIC)
	// must return zero owners — proving the isolation holds in both
	// directions: recorded types don't leak INTO unrecorded ones, and
	// querying an unrecorded type never silently falls back to
	// another type's data.
	econ := og.AllOwnersInChain(vessel.ID, OwnershipEconomic, 0.0, 5)
	if len(econ) != 0 {
		t.Fatalf("ECONOMIC ownership was never recorded for this pair, expected 0 results, got %+v", econ)
	}

	// Cross-check: querying with a threshold between two of the
	// recorded percentages (e.g. 0.5) must include CONTROL and
	// BENEFICIAL (>=0.5) but exclude LEGAL (0.30 < 0.5) — proving the
	// per-type EffectivePercent values above are actually load-bearing
	// in threshold filtering, not just cosmetic.
	if got := og.AllOwnersInChain(vessel.ID, OwnershipLegal, 0.5, 5); len(got) != 0 {
		t.Fatalf("LEGAL 0.30 should be filtered out by threshold 0.5, got %+v", got)
	}
	if got := og.AllOwnersInChain(vessel.ID, OwnershipBeneficial, 0.5, 5); len(got) != 1 {
		t.Fatalf("BENEFICIAL 0.70 should pass threshold 0.5, got %+v", got)
	}
}
