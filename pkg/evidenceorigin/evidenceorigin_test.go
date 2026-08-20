package evidenceorigin

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/datarights"
)

func validEnvelope(origin livedata.DataMode) Envelope {
	collected := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	return Envelope{
		DataOrigin:       origin,
		SourceID:         "AIS-VENDOR-A",
		SourceType:       "AIS",
		ProviderID:       "provider-1",
		LicenseReference: "LIC-2026-001",
		RightsStatus:     datarights.Qualified,
		CollectionTime:   collected,
		ReceivedTime:     collected.Add(5 * time.Minute),
		ContentHash:      "abc123",
		CanonicalHash:    "def456",
	}
}

func TestValidateAcceptsWellFormedEnvelope(t *testing.T) {
	for _, origin := range []livedata.DataMode{livedata.ModeSynthetic, livedata.ModeReplay, livedata.ModeLive} {
		if err := validEnvelope(origin).Validate(); err != nil {
			t.Errorf("origin=%s: expected a well-formed envelope to validate, got %v", origin, err)
		}
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	base := validEnvelope(livedata.ModeSynthetic)
	cases := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{"data_origin", func(e *Envelope) { e.DataOrigin = "" }},
		{"source_id", func(e *Envelope) { e.SourceID = "" }},
		{"source_type", func(e *Envelope) { e.SourceType = "" }},
		{"provider_id", func(e *Envelope) { e.ProviderID = "" }},
		{"rights_status", func(e *Envelope) { e.RightsStatus = "" }},
		{"content_hash", func(e *Envelope) { e.ContentHash = "" }},
		{"collection_time", func(e *Envelope) { e.CollectionTime = time.Time{} }},
		{"received_time", func(e *Envelope) { e.ReceivedTime = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := base
			tc.mutate(&env)
			if err := env.Validate(); !errors.Is(err, ErrMissingField) {
				t.Fatalf("expected ErrMissingField for missing %s, got %v", tc.name, err)
			}
		})
	}
}

func TestValidateRejectsUnknownDataOrigin(t *testing.T) {
	env := validEnvelope(livedata.ModeSynthetic)
	env.DataOrigin = "TOTALLY_MADE_UP"
	if err := env.Validate(); !errors.Is(err, ErrUnknownDataOrigin) {
		t.Fatalf("expected ErrUnknownDataOrigin, got %v", err)
	}
}

func TestValidateRejectsReceivedBeforeCollected(t *testing.T) {
	env := validEnvelope(livedata.ModeLive)
	env.ReceivedTime = env.CollectionTime.Add(-time.Minute)
	if err := env.Validate(); !errors.Is(err, ErrTimeOrderInvalid) {
		t.Fatalf("expected ErrTimeOrderInvalid, got %v", err)
	}
}

// TestValidateClaimRejectsSyntheticClaimedAsLive is the audit's single
// most emphasized rule: SYNTHETIC data claimed as live/qualified-live
// evidence must be rejected, fail-closed.
func TestValidateClaimRejectsSyntheticClaimedAsLive(t *testing.T) {
	env := validEnvelope(livedata.ModeSynthetic)
	if err := ValidateClaim(env, true); !errors.Is(err, ErrFalseLiveClaim) {
		t.Fatalf("expected ErrFalseLiveClaim for SYNTHETIC claimed as live, got %v", err)
	}
}

// TestValidateClaimRejectsReplayClaimedAsLive is the same rule's other
// named case: a replayed record is exactly what anti-replay defenses
// exist to catch, and it must never be presented as live either.
func TestValidateClaimRejectsReplayClaimedAsLive(t *testing.T) {
	env := validEnvelope(livedata.ModeReplay)
	if err := ValidateClaim(env, true); !errors.Is(err, ErrFalseLiveClaim) {
		t.Fatalf("expected ErrFalseLiveClaim for REPLAY claimed as live, got %v", err)
	}
}

func TestValidateClaimAcceptsGenuineLiveClaimedAsLive(t *testing.T) {
	env := validEnvelope(livedata.ModeLive)
	if err := ValidateClaim(env, true); err != nil {
		t.Fatalf("expected a genuinely LIVE envelope claimed as live to be accepted, got %v", err)
	}
}

func TestValidateClaimAcceptsSyntheticNotClaimedAsLive(t *testing.T) {
	env := validEnvelope(livedata.ModeSynthetic)
	if err := ValidateClaim(env, false); err != nil {
		t.Fatalf("expected SYNTHETIC not claimed live to be accepted, got %v", err)
	}
}

func TestValidateClaimAcceptsReplayNotClaimedAsLive(t *testing.T) {
	env := validEnvelope(livedata.ModeReplay)
	if err := ValidateClaim(env, false); err != nil {
		t.Fatalf("expected REPLAY not claimed live to be accepted, got %v", err)
	}
}

func TestValidateClaimPropagatesStructuralValidationFirst(t *testing.T) {
	env := validEnvelope(livedata.ModeSynthetic)
	env.SourceID = "" // structurally invalid, independent of the live-claim rule
	if err := ValidateClaim(env, true); !errors.Is(err, ErrMissingField) {
		t.Fatalf("expected structural validation to run first and report ErrMissingField, got %v", err)
	}
}

func TestEnvelopeJSONFieldNames(t *testing.T) {
	env := validEnvelope(livedata.ModeLive)
	blob, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"data_origin", "source_id", "source_type", "provider_id",
		"license_reference", "rights_status", "collection_time",
		"received_time", "content_hash", "canonical_hash",
	} {
		if _, ok := raw[field]; !ok {
			t.Errorf("expected JSON field %q, got keys %v", field, raw)
		}
	}
}
