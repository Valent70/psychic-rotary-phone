// Package evidenceorigin is the rights-and-provenance registry's
// envelope type: the one struct that ties data_origin (is this record
// real, or a fixture?), the rights that govern it
// (pkg/datarights.RightsStatus), and the hashes needed to detect
// tampering into a single audit-ready unit.
//
// The single most important rule in this package -- carried over
// unchanged from pkg/blockers/livedata, not reimplemented -- is that
// content whose DataOrigin is SYNTHETIC or REPLAY must never be claimed
// as LIVE (or presented as qualified live evidence) by a caller. See
// ValidateClaim, which wraps livedata.EnforceLiveProvenance directly.
package evidenceorigin

import (
	"errors"
	"fmt"
	"time"

	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/datarights"
)

// Envelope binds one piece of evidence to its full origin story: where
// it came from, whose rights govern it, when it was collected versus
// received, and the hashes needed to detect tampering (ContentHash) or
// verify normalization (CanonicalHash).
type Envelope struct {
	DataOrigin       livedata.DataMode       `json:"data_origin"`
	SourceID         string                  `json:"source_id"`
	SourceType       string                  `json:"source_type"`
	ProviderID       string                  `json:"provider_id"`
	LicenseReference string                  `json:"license_reference"`
	RightsStatus     datarights.RightsStatus `json:"rights_status"`
	CollectionTime   time.Time               `json:"collection_time"`
	ReceivedTime     time.Time               `json:"received_time"`
	ContentHash      string                  `json:"content_hash"`
	CanonicalHash    string                  `json:"canonical_hash"`
}

// Errors.
var (
	ErrMissingField      = errors.New("evidenceorigin: envelope is missing a required field")
	ErrTimeOrderInvalid  = errors.New("evidenceorigin: received_time is before collection_time")
	ErrFalseLiveClaim    = errors.New("evidenceorigin: non-live data_origin claimed as live/qualified-live evidence")
	ErrUnknownDataOrigin = errors.New("evidenceorigin: unknown data_origin")
)

// Validate fail-closed checks that an Envelope is structurally sound:
// every field the registry depends on to establish provenance is
// present, and the collection/receipt timestamps are causally ordered.
// It does NOT by itself check the live-claim rule -- that is
// ValidateClaim's job, kept separate because it needs an explicit
// caller-supplied claim (see ValidateClaim's doc comment for why).
func (e Envelope) Validate() error {
	if e.DataOrigin == "" {
		return fmt.Errorf("%w: data_origin", ErrMissingField)
	}
	switch e.DataOrigin {
	case livedata.ModeSynthetic, livedata.ModeReplay, livedata.ModeLive:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownDataOrigin, e.DataOrigin)
	}
	if e.SourceID == "" {
		return fmt.Errorf("%w: source_id", ErrMissingField)
	}
	if e.SourceType == "" {
		return fmt.Errorf("%w: source_type", ErrMissingField)
	}
	if e.ProviderID == "" {
		return fmt.Errorf("%w: provider_id", ErrMissingField)
	}
	if e.RightsStatus == "" {
		return fmt.Errorf("%w: rights_status", ErrMissingField)
	}
	if e.ContentHash == "" {
		return fmt.Errorf("%w: content_hash", ErrMissingField)
	}
	if e.CollectionTime.IsZero() {
		return fmt.Errorf("%w: collection_time", ErrMissingField)
	}
	if e.ReceivedTime.IsZero() {
		return fmt.Errorf("%w: received_time", ErrMissingField)
	}
	if e.ReceivedTime.Before(e.CollectionTime) {
		return ErrTimeOrderInvalid
	}
	return nil
}

// ValidateClaim is the audit's single most emphasized rule in this
// section, made explicit and impossible to bypass: it fail-closed
// rejects an envelope whose DataOrigin is SYNTHETIC or REPLAY when
// claimedLive says the caller is presenting it as live (or as qualified
// live evidence). claimedLive is an explicit, separate parameter rather
// than something inferred from RightsStatus or elsewhere in the
// envelope, because the whole point of this check is to catch a CALLER
// asserting something the DATA does not itself support -- inferring the
// claim from the envelope would let the same lie the check exists to
// catch also poison the input the check relies on.
//
// This deliberately reuses livedata.EnforceLiveProvenance -- the exact
// function pkg/blockers/livedata.Pipeline.Ingest itself calls -- rather
// than reimplementing the fail-closed rule a second time, by mapping
// this package's concepts onto livedata's: the tag being asserted is
// ModeLive when claimedLive is true (env.DataOrigin otherwise, which
// never trips the check on its own), and the "producer" is REAL only
// when the envelope's actual DataOrigin is genuinely ModeLive. A
// SYNTHETIC or REPLAY envelope claimed live therefore hits exactly the
// same "a SIMULATED-mode connector must never produce a record tagged
// LIVE" rule livedata's own pipeline enforces on ingest.
func ValidateClaim(env Envelope, claimedLive bool) error {
	if err := env.Validate(); err != nil {
		return err
	}
	claimedTag := env.DataOrigin
	if claimedLive {
		claimedTag = livedata.ModeLive
	}
	producerMode := "SIMULATED"
	if env.DataOrigin == livedata.ModeLive {
		producerMode = "REAL"
	}
	if err := livedata.EnforceLiveProvenance(claimedTag, producerMode); err != nil {
		return fmt.Errorf("%w: source %s (data_origin=%s): %s", ErrFalseLiveClaim, env.SourceID, env.DataOrigin, err.Error())
	}
	return nil
}
