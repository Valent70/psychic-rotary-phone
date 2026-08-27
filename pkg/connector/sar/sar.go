// Package sar is R-050's SAR (synthetic-aperture-radar) ingestion
// contract — one of the five real-world source types the requirement
// traceability matrix named as the genuine engineering gap in
// pkg/connector. Honest scope, stated once here rather than buried:
// this sandbox has no reachable path to any real satellite/SAR
// provider (Capella, ICEYE, Umbra, or otherwise), so this package
// cannot and does not claim to be "live" in this session. What it DOES
// provide is the real, tested machinery a live provider integration
// would plug straight into without anything downstream changing:
//
//  1. Contract — parses raw SAR-detection payloads, structurally
//     validates them (well-formed JSON, every required field present),
//     semantically validates them (positions and confidence in-range),
//     and canonicalizes a valid detection into
//     pkg/evidence/ontology.Evidence{Type: TypeSARObservation}. It
//     implements pkg/connector.Decoder, so it plugs straight into
//     connector.Drain.
//  2. SimulatedConnector — a deterministic, seeded fixture connector
//     implementing pkg/blockers/livedata.FeedConnector, always tagged
//     ModeSynthetic, always Mode() == "SIMULATED". Swapping it for a
//     real SAR provider's client is the only change a live integration
//     needs; Contract/Drain do not care what produced the payload.
package sar

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"veriqo/pkg/evidence/ontology"
)

// Structural errors — malformed JSON or valid JSON missing/mistyping a
// required field. Always wrapped with ErrMalformed so a caller can
// errors.Is against one sentinel regardless of which field failed.
var (
	ErrMalformed           = errors.New("sar: malformed detection record")
	ErrSemanticallyInvalid = errors.New("sar: semantically invalid detection record")
)

// wireDetection is this package's SAR-detection wire schema: a single
// vessel-shaped radar return from one satellite pass. Field names are
// plausible, illustrative SAR-detection terminology, not a claim of
// byte-for-byte compatibility with any specific commercial provider's
// actual API.
type wireDetection struct {
	SceneID       string   `json:"scene_id"`
	DetectionID   string   `json:"detection_id"`
	Platform      string   `json:"platform"` // e.g. "Sentinel-1", "ICEYE-X", "Capella"
	Latitude      *float64 `json:"latitude"`
	Longitude     *float64 `json:"longitude"`
	LengthMeters  *float64 `json:"length_m,omitempty"`
	ConfidencePct *float64 `json:"confidence_pct"` // sensor-reported detection confidence, 0..100
	VesselIMOHint string   `json:"vessel_imo_hint,omitempty"`
	TimestampUTC  int64    `json:"timestamp_utc"`
}

// decodeWire parses raw JSON into a wireDetection and checks that
// every required field is PRESENT — pure structural validation,
// deliberately performed before any semantic (range) check so a
// truncated or wrong-schema payload fails with a structural reason,
// never a confusing semantic one.
func decodeWire(payload string) (wireDetection, error) {
	var w wireDetection
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return wireDetection{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if strings.TrimSpace(w.SceneID) == "" {
		return wireDetection{}, fmt.Errorf("%w: missing scene_id", ErrMalformed)
	}
	if strings.TrimSpace(w.DetectionID) == "" {
		return wireDetection{}, fmt.Errorf("%w: missing detection_id", ErrMalformed)
	}
	if strings.TrimSpace(w.Platform) == "" {
		return wireDetection{}, fmt.Errorf("%w: missing platform", ErrMalformed)
	}
	if w.Latitude == nil || w.Longitude == nil {
		return wireDetection{}, fmt.Errorf("%w: missing latitude/longitude", ErrMalformed)
	}
	if w.ConfidencePct == nil {
		return wireDetection{}, fmt.Errorf("%w: missing confidence_pct", ErrMalformed)
	}
	if w.TimestampUTC <= 0 {
		return wireDetection{}, fmt.Errorf("%w: missing or non-positive timestamp_utc", ErrMalformed)
	}
	return w, nil
}

// validateSemantics checks in-range/consistency rules that a
// structurally well-formed record can still violate: a detection
// stating latitude=200 parsed cleanly but describes nowhere on Earth.
func (w wireDetection) validateSemantics() error {
	if *w.Latitude < -90 || *w.Latitude > 90 {
		return fmt.Errorf("%w: latitude %v out of range [-90,90]", ErrSemanticallyInvalid, *w.Latitude)
	}
	if *w.Longitude < -180 || *w.Longitude > 180 {
		return fmt.Errorf("%w: longitude %v out of range [-180,180]", ErrSemanticallyInvalid, *w.Longitude)
	}
	if *w.ConfidencePct < 0 || *w.ConfidencePct > 100 {
		return fmt.Errorf("%w: confidence_pct %v out of range [0,100]", ErrSemanticallyInvalid, *w.ConfidencePct)
	}
	if w.LengthMeters != nil && *w.LengthMeters <= 0 {
		return fmt.Errorf("%w: length_m %v must be > 0 when present", ErrSemanticallyInvalid, *w.LengthMeters)
	}
	return nil
}

// toOntologyEvidence canonicalizes a validated wireDetection into
// ontology.Evidence{Type: TypeSARObservation}, whose own required-
// attribute rule (pkg/evidence/ontology.go) demands "scene_id" — set
// here, alongside every other facet this record knows.
func (w wireDetection) toOntologyEvidence(source string, receivedAtTick uint64) (ontology.Evidence, error) {
	attrs := map[string]string{
		"scene_id":     w.SceneID,
		"detection_id": w.DetectionID,
		"platform":     w.Platform,
		"latitude":     fmt.Sprintf("%.6f", *w.Latitude),
		"longitude":    fmt.Sprintf("%.6f", *w.Longitude),
	}
	if w.LengthMeters != nil {
		attrs["length_m"] = fmt.Sprintf("%.2f", *w.LengthMeters)
	}
	if w.VesselIMOHint != "" {
		attrs["vessel_imo_hint"] = w.VesselIMOHint
	}
	return ontology.New(ontology.Evidence{
		Type:       ontology.TypeSARObservation,
		Subject:    fmt.Sprintf("sar_detection:%s:%s", w.SceneID, w.DetectionID),
		Predicate:  "sar_detected",
		Object:     w.Platform,
		Source:     source,
		ObservedAt: uint64(w.TimestampUTC), // #nosec G115 -- decodeWire already rejects TimestampUTC <= 0
		ReceivedAt: receivedAtTick,
		Confidence: *w.ConfidencePct / 100.0,
		Attributes: attrs,
	})
}

// Contract is this package's pkg/connector.Decoder implementation.
// Source identifies who is asserting this evidence (the ontology-level
// Source string) — typically the connector/subscription identity, not
// a per-record field.
type Contract struct {
	Source string
}

// SourceType implements pkg/connector.Decoder.
func (c Contract) SourceType() string { return "SAR" }

// Decode implements pkg/connector.Decoder: structural validation,
// semantic validation, then canonicalization — in that order, so a
// caller reading the returned error always learns about the FIRST
// thing wrong with the record, not the last.
func (c Contract) Decode(payload string, receivedAtTick uint64) (ontology.Evidence, error) {
	w, err := decodeWire(payload)
	if err != nil {
		return ontology.Evidence{}, err
	}
	if err := w.validateSemantics(); err != nil {
		return ontology.Evidence{}, err
	}
	source := c.Source
	if source == "" {
		source = "sar:unspecified"
	}
	return w.toOntologyEvidence(source, receivedAtTick)
}
