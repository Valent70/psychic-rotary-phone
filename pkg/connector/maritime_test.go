package connector

import (
	"context"
	"strings"
	"testing"

	"veriqo/pkg/moat/domain/maritime"
)

func TestSyntheticAdapter_FeedEndToEnd_HonestProvenance(t *testing.T) {
	adapter := &SyntheticReplayableAdapter{Seed: 7, Count: 10}
	reg := maritime.NewRegistry()

	res, claims, err := Feed(context.Background(), adapter, reg, 1)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if res.Ingested != 10 || res.EntitiesCreated != 10 || res.ClaimsEmitted != 10 {
		t.Fatalf("unexpected feed result: %+v", res)
	}
	if !strings.HasPrefix(res.AdapterName, "synthetic:") {
		t.Fatalf("adapter name must honestly self-identify as synthetic, got %q", res.AdapterName)
	}
	for _, c := range claims {
		if c.Source != res.AdapterName {
			t.Fatalf("claim source %q does not match adapter identity %q — provenance broken", c.Source, res.AdapterName)
		}
		if c.Confidence < 0.5 || c.Confidence > 1.0 {
			t.Fatalf("confidence %f out of expected [0.5,1.0] range for synthetic adapter", c.Confidence)
		}
	}
	if reg.EntityCount() != 10 {
		t.Fatalf("registry should have 10 entities, got %d", reg.EntityCount())
	}
	if err := reg.VerifyChain(); err != nil {
		t.Fatalf("registry hash chain invalid after feed: %v", err)
	}
}

// TestSyntheticAdapter_Deterministic proves "replayable": same seed,
// same output — required for the synthetic fallback to be a credible
// stand-in for demos/tests until a live provider is wired in.
func TestSyntheticAdapter_Deterministic(t *testing.T) {
	a1 := &SyntheticReplayableAdapter{Seed: 99, Count: 5}
	a2 := &SyntheticReplayableAdapter{Seed: 99, Count: 5}
	o1, _ := a1.Fetch(context.Background())
	o2, _ := a2.Fetch(context.Background())
	for i := range o1 {
		if o1[i].VesselIMO != o2[i].VesselIMO || o1[i].Confidence != o2[i].Confidence {
			t.Fatalf("same-seed adapters diverged at index %d: %+v vs %+v", i, o1[i], o2[i])
		}
	}
}
