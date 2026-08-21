package connector

import (
	"context"
	"testing"

	"veriqo/pkg/evidence/provenance"
)

// fakeGDELTAdapter is a minimal, in-test-only stand-in proving the
// interfaces are genuinely implementable by a third-party-shaped
// adapter without VERIQO's core importing anything GDELT-specific.
type fakeGDELTAdapter struct{}

func (fakeGDELTAdapter) Name() string { return "gdelt" }

func (fakeGDELTAdapter) FetchEvents(ctx context.Context, since uint64) ([]ExternalEvent, error) {
	return []ExternalEvent{{
		EventID: "gdelt-1", Summary: "port congestion reported", Location: "SGSIN", ObservedAt: since + 1,
		Provenance: provenance.ExternalEvidence{
			SourceID: "gdelt-feed", ProviderID: "gdelt", DatasetID: "gdelt-events", DeliveryID: "d1",
			OriginClass: provenance.OriginRealExternalUnverified, RightsState: provenance.RightsUnknownPendingContract,
			PayloadHash: "abc", CanonicalHash: "def", SchemaVersion: provenance.SchemaVersionV1,
			CorrectionState: provenance.CorrectionNone, AttestationState: provenance.AttestationUnattested,
		},
	}}, nil
}

type fakeWorldMonitorAdapter struct{}

func (fakeWorldMonitorAdapter) Name() string { return "world_monitor" }

func (fakeWorldMonitorAdapter) Enrich(ctx context.Context, subject string) ([]RiskSignal, error) {
	return []RiskSignal{{
		SignalID: "wm-1", Subject: subject, Category: "sanctions_adjacent", Score: 0.4, Rationale: "mentioned near a listed entity",
		Provenance: provenance.ExternalEvidence{
			SourceID: "world-monitor-feed", ProviderID: "world_monitor", DatasetID: "wm-signals", DeliveryID: "d1",
			OriginClass: provenance.OriginRealExternalUnverified, RightsState: provenance.RightsUnknownPendingContract,
			PayloadHash: "abc", CanonicalHash: "def", SchemaVersion: provenance.SchemaVersionV1,
			CorrectionState: provenance.CorrectionNone, AttestationState: provenance.AttestationUnattested,
		},
	}}, nil
}

func TestExternalEventSourceIsPluggableWithoutACoreDependency(t *testing.T) {
	var src ExternalEventSource = fakeGDELTAdapter{}
	events, err := src.FetchEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventID != "gdelt-1" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestRiskEnrichmentSourceIsPluggableWithoutACoreDependency(t *testing.T) {
	var src RiskEnrichmentSource = fakeWorldMonitorAdapter{}
	signals, err := src.Enrich(context.Background(), "vessel-123")
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 || signals[0].Subject != "vessel-123" {
		t.Fatalf("unexpected signals: %+v", signals)
	}
}

// TestExternalEventNeverCarriesMoreThanUnverifiedRightsByDefault proves
// the "never primary truth merely because it is available" guarantee
// is mechanically checkable: an event/signal from any adapter that has
// not gone through pkg/evidence/provenance.Registry.GrantTrust remains
// Permits()-denied for every externally-visible or qualification use.
func TestExternalEventNeverCarriesMoreThanUnverifiedRightsByDefault(t *testing.T) {
	events, err := (fakeGDELTAdapter{}).FetchEvents(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	ev := events[0].Provenance
	for _, use := range []provenance.Use{provenance.UseCustomerFacing, provenance.UseCalibration, provenance.UseDispute, provenance.UseTraining} {
		if ev.Permits(use) {
			t.Fatalf("a GDELT event must not permit %s by default", use)
		}
	}
	signals, err := (fakeWorldMonitorAdapter{}).Enrich(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	rs := signals[0].Provenance
	for _, use := range []provenance.Use{provenance.UseCustomerFacing, provenance.UseCalibration, provenance.UseDispute, provenance.UseTraining} {
		if rs.Permits(use) {
			t.Fatalf("a World Monitor signal must not permit %s by default", use)
		}
	}
}
