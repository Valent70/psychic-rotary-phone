package timeline

import (
	"errors"
	"testing"
)

func mustCondition(t *testing.T, id string, stage Stage, tick uint64) Condition {
	t.Helper()
	c, err := NewCondition(id, stage, tick, "EV-BASE")
	if err != nil {
		t.Fatalf("NewCondition: %v", err)
	}
	return c
}

func mustDV(t *testing.T, value string, ev ...string) DimensionValue {
	t.Helper()
	dv, err := NewDimensionValue(value, ev...)
	if err != nil {
		t.Fatalf("NewDimensionValue: %v", err)
	}
	return dv
}

func TestNewDimensionValueRequiresEvidence(t *testing.T) {
	if _, err := NewDimensionValue("500 cartons"); !errors.Is(err, ErrDimensionValueNoEvidence) {
		t.Fatalf("expected ErrDimensionValueNoEvidence, got %v", err)
	}
}

func TestUnresolvedDimensionIsExplicitlyUnknown(t *testing.T) {
	u := UnresolvedDimension()
	if u.Known {
		t.Fatal("expected UnresolvedDimension to be Known=false")
	}
	if u.Value != "" || len(u.SourceEvidence) != 0 {
		t.Fatalf("expected zero-value payload, got %+v", u)
	}
}

func TestNewConditionRejectsEmptyID(t *testing.T) {
	if _, err := NewCondition("", StageConditionLoading, 0); !errors.Is(err, ErrEmptyConditionID) {
		t.Fatalf("expected ErrEmptyConditionID, got %v", err)
	}
}

func TestNewConditionRejectsUnknownStage(t *testing.T) {
	if _, err := NewCondition("COND-1", Stage("BOGUS"), 0); !errors.Is(err, ErrUnknownStage) {
		t.Fatalf("expected ErrUnknownStage, got %v", err)
	}
}

func TestNewConditionDefaultsAllDimensionsUnresolved(t *testing.T) {
	c := mustCondition(t, "COND-1", StageConditionLoading, 100)
	for _, dv := range []DimensionValue{c.Quantity, c.Quality, c.Packaging, c.Seal, c.Temperature, c.Moisture, c.PhysicalCondition} {
		if dv.Known {
			t.Fatalf("expected every dimension to default to unresolved, got %+v", dv)
		}
	}
}

// TestCompareAlwaysReturnsAllSevenDimensionsInFixedOrder: Compare must
// never silently omit a dimension, whether it changed, was unchanged,
// or could not be evaluated at all.
func TestCompareAlwaysReturnsAllSevenDimensionsInFixedOrder(t *testing.T) {
	loading := mustCondition(t, "COND-LOAD", StageConditionLoading, 100)
	delivery := mustCondition(t, "COND-DEL", StageConditionDelivery, 200)

	diffs := Compare(loading, delivery)
	want := []Dimension{
		DimensionQuantity, DimensionQuality, DimensionPackaging, DimensionSeal,
		DimensionTemperature, DimensionMoisture, DimensionPhysicalCondition,
	}
	if len(diffs) != len(want) {
		t.Fatalf("expected %d differences, got %d", len(want), len(diffs))
	}
	for i, d := range diffs {
		if d.Dimension != want[i] {
			t.Fatalf("position %d: expected %s, got %s", i, want[i], d.Dimension)
		}
		if d.Resolved {
			t.Fatalf("expected %s to be unresolved (no data on either side), got resolved", d.Dimension)
		}
	}
}

func TestCompareDetectsChangeWhenBothKnown(t *testing.T) {
	loading := mustCondition(t, "COND-LOAD", StageConditionLoading, 100)
	loading.Quantity = mustDV(t, "500 cartons", "EV-PACKING-LIST")
	loading.Seal = mustDV(t, "INTACT", "EV-SEAL-LOAD")

	delivery := mustCondition(t, "COND-DEL", StageConditionDelivery, 200)
	delivery.Quantity = mustDV(t, "480 cartons", "EV-DELIVERY-TALLY")
	delivery.Seal = mustDV(t, "BROKEN", "EV-SEAL-DELIVERY")

	diffs := Compare(loading, delivery)
	byDim := indexByDimension(diffs)

	qty := byDim[DimensionQuantity]
	if !qty.Resolved || !qty.Changed {
		t.Fatalf("expected quantity to be resolved and changed, got %+v", qty)
	}
	if qty.Before.Value != "500 cartons" || qty.After.Value != "480 cartons" {
		t.Fatalf("unexpected before/after: %+v", qty)
	}

	seal := byDim[DimensionSeal]
	if !seal.Resolved || !seal.Changed {
		t.Fatalf("expected seal to be resolved and changed, got %+v", seal)
	}

	// Untouched dimensions remain unresolved.
	temp := byDim[DimensionTemperature]
	if temp.Resolved {
		t.Fatalf("expected temperature to remain unresolved, got %+v", temp)
	}
}

func TestCompareNoChangeWhenValuesMatch(t *testing.T) {
	loading := mustCondition(t, "COND-LOAD", StageConditionLoading, 100)
	loading.Quality = mustDV(t, "GOOD", "EV-QUALITY-LOAD")
	delivery := mustCondition(t, "COND-DEL", StageConditionDelivery, 200)
	delivery.Quality = mustDV(t, "good", "EV-QUALITY-DELIVERY") // case-insensitive match

	diffs := Compare(loading, delivery)
	quality := indexByDimension(diffs)[DimensionQuality]
	if !quality.Resolved || quality.Changed {
		t.Fatalf("expected quality to be resolved and unchanged, got %+v", quality)
	}
}

func TestCompareOneSidedInsufficientData(t *testing.T) {
	loading := mustCondition(t, "COND-LOAD", StageConditionLoading, 100)
	loading.Moisture = mustDV(t, "8%", "EV-MOISTURE-LOAD")
	delivery := mustCondition(t, "COND-DEL", StageConditionDelivery, 200)
	// delivery.Moisture left unresolved deliberately

	diffs := Compare(loading, delivery)
	moisture := indexByDimension(diffs)[DimensionMoisture]
	if moisture.Resolved {
		t.Fatal("expected moisture to be unresolved when only one side has data")
	}
	if moisture.Changed {
		t.Fatal("Changed must be false when Resolved is false — it carries no meaning")
	}
	if moisture.Note == "" {
		t.Fatal("expected an explicit insufficient-data note, not a silently empty one")
	}
}

// TestCompareNumericToleranceIgnoresUnitFormatting: a bare numeric
// comparison should treat "-18C" and "-18.0" as equal, since the
// leading numbers match.
func TestCompareNumericComparisonIgnoresTrailingUnitNoise(t *testing.T) {
	loading := mustCondition(t, "COND-LOAD", StageConditionLoading, 100)
	loading.Temperature = mustDV(t, "-18C", "EV-TEMP-LOAD")
	delivery := mustCondition(t, "COND-DEL", StageConditionDelivery, 200)
	delivery.Temperature = mustDV(t, "-18.0C", "EV-TEMP-DELIVERY")

	diffs := Compare(loading, delivery)
	temp := indexByDimension(diffs)[DimensionTemperature]
	if !temp.Resolved || temp.Changed {
		t.Fatalf("expected -18C and -18.0C to compare equal, got %+v", temp)
	}
}

func TestCompareNumericDifferenceIsDetected(t *testing.T) {
	loading := mustCondition(t, "COND-LOAD", StageConditionLoading, 100)
	loading.Temperature = mustDV(t, "-18C", "EV-TEMP-LOAD")
	delivery := mustCondition(t, "COND-DEL", StageConditionDelivery, 200)
	delivery.Temperature = mustDV(t, "4C", "EV-TEMP-DELIVERY") // reefer failure: warmed to 4C

	diffs := Compare(loading, delivery)
	temp := indexByDimension(diffs)[DimensionTemperature]
	if !temp.Resolved || !temp.Changed {
		t.Fatalf("expected -18C vs 4C to be flagged as changed, got %+v", temp)
	}
}

func TestUnresolvedDimensionsAndChangedDimensionsHelpers(t *testing.T) {
	loading := mustCondition(t, "COND-LOAD", StageConditionLoading, 100)
	loading.Quantity = mustDV(t, "500", "EV-1")
	loading.Seal = mustDV(t, "INTACT", "EV-2")

	delivery := mustCondition(t, "COND-DEL", StageConditionDelivery, 200)
	delivery.Quantity = mustDV(t, "480", "EV-3") // changed
	delivery.Seal = mustDV(t, "INTACT", "EV-4")  // unchanged
	// packaging, temperature, moisture, physical_condition, quality: unresolved

	diffs := Compare(loading, delivery)
	unresolved := UnresolvedDimensions(diffs)
	changed := ChangedDimensions(diffs)

	if len(unresolved) != 5 {
		t.Fatalf("expected 5 unresolved dimensions, got %d: %v", len(unresolved), unresolved)
	}
	if len(changed) != 1 || changed[0] != DimensionQuantity {
		t.Fatalf("expected only quantity to be changed, got %v", changed)
	}
}

func TestDimensionsReturnsSevenInFixedOrder(t *testing.T) {
	dims := Dimensions()
	if len(dims) != 7 {
		t.Fatalf("expected 7 dimensions, got %d", len(dims))
	}
	want := []Dimension{
		DimensionQuantity, DimensionQuality, DimensionPackaging, DimensionSeal,
		DimensionTemperature, DimensionMoisture, DimensionPhysicalCondition,
	}
	for i, d := range want {
		if dims[i] != d {
			t.Fatalf("position %d: expected %s, got %s", i, d, dims[i])
		}
	}
}

// TestChainStoresIntermediateEvents: the §15 diagram's CUSTODY_EVENTS
// / TRANSPORT_EVENTS / INCIDENT_EVENTS between loading and delivery
// are pure storage this package attaches no analysis to; this test
// only confirms the structure round-trips.
func TestChainStoresIntermediateEvents(t *testing.T) {
	loading := mustCondition(t, "COND-LOAD", StageConditionLoading, 100)
	delivery := mustCondition(t, "COND-DEL", StageConditionDelivery, 5000)
	chain := Chain{
		Loading: loading,
		CustodyEvents: []CustodyEvent{
			{EventID: "EVT-HANDOVER-1", Holder: "Terminal A", Action: "received", Tick: 150, SourceEvidence: []string{"EV-1"}},
		},
		TransportEvents: []TransportEvent{
			{EventID: "EVT-VOYAGE-1", Description: "ocean leg", Tick: 200, SourceEvidence: []string{"EV-2"}},
		},
		IncidentEvents: []IncidentEvent{
			{EventID: "EVT-INCIDENT-1", Description: "heavy weather", Tick: 300, SourceEvidence: []string{"EV-3"}},
		},
		Delivery: delivery,
	}
	if len(chain.CustodyEvents) != 1 || len(chain.TransportEvents) != 1 || len(chain.IncidentEvents) != 1 {
		t.Fatalf("expected the chain to store all intermediate events, got %+v", chain)
	}
	diffs := Compare(chain.Loading, chain.Delivery)
	if len(diffs) != 7 {
		t.Fatalf("expected Compare(chain.Loading, chain.Delivery) to still work, got %d diffs", len(diffs))
	}
}

func indexByDimension(diffs []Difference) map[Dimension]Difference {
	out := make(map[Dimension]Difference, len(diffs))
	for _, d := range diffs {
		out[d.Dimension] = d
	}
	return out
}
