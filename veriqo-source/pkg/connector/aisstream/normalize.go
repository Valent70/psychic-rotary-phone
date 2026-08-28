package aisstream

import (
	"fmt"

	"veriqo/pkg/evidence/ontology"
)

// NormalizedEvent is PART 4's canonical maritime event shape: one AIS
// wire message, decoded into typed facets (ship identity, position,
// voyage information, destination, navigation status, timestamp),
// plus the delivery/provenance metadata this connector attached on
// receipt.
//
// # Bitemporal fields
//
// pkg/evidence/ontology's own doc comment describes VERIQO's house
// style: ticks, not wall-clock timestamps, inside the deterministic
// kernel core, because the kernel has no wall clock and must replay
// identically. This package is deliberately NOT inside that core — it
// is the ingest/connector boundary, which is explicitly where real
// time is allowed to enter the system (ontology's own words: "a
// deployment's ingest adapter is the one place that maps real time to
// a tick"). So these three fields are uint64 unix-second values —
// consistent in TYPE with how pkg/connector/maritime.go already
// represents time as uint64 — not logical ticks:
//
//   - ObservedAt: when the vessel was actually in that state, per the
//     AIS message's own reported TimestampUTC. Set exactly once, at
//     construction (see normalize), and never overwritten afterward
//     by any code in this package — the source's own timestamp is
//     immutable once normalized.
//   - ReceivedAt: when this connector received the raw message off
//     the wire. Always >= ObservedAt in any real deployment (network
//     delay cannot be negative); see ToOntologyEvidence, which relies
//     on that ordering.
//   - NormalizedAt: when this normalization pass actually ran. Kept
//     separate from ReceivedAt because normalization can legitimately
//     run later than receipt (e.g. replayed from RawRetention), so
//     collapsing the two would silently lose that distinction.
type NormalizedEvent struct {
	// --- ship_identity ---
	MMSI     int64
	IMO      int64
	CallSign string
	ShipName string

	// --- position ---
	HasPosition bool
	Latitude    float64
	Longitude   float64
	HasCourse   bool
	CourseDeg   float64
	HasSpeed    bool
	SpeedKnots  float64

	// --- navigation_status ---
	HasNavStatus bool
	NavStatus    int

	// --- voyage_information / destination ---
	Destination string

	// MessageType is the source AIS message type this event was
	// decoded from (e.g. "PositionReport", "ShipStaticData").
	MessageType string

	// PayloadHash/CanonicalHash are carried through unchanged from the
	// provenance.ExternalEvidence constructed for the same message —
	// PART 4's "preserve original payload hash" requirement.
	PayloadHash   string
	CanonicalHash string

	// DeliverySeq is this connector's own monotonic delivery counter,
	// shared with the ExternalEvidence.DeliveryID constructed for the
	// same message (see handleRaw).
	DeliverySeq uint64

	// --- timestamp / bitemporal fields, see doc comment above ---
	ObservedAt   uint64
	ReceivedAt   uint64
	NormalizedAt uint64
}

// normalize decodes one validated wireMessage plus this connector's
// own delivery metadata into a NormalizedEvent. It is the only place
// ObservedAt is ever set — nothing downstream in this package touches
// it again.
func normalize(w wireMessage, seq uint64, receivedAt, normalizedAt uint64, payloadHash, canonicalHash string) NormalizedEvent {
	ev := NormalizedEvent{
		CallSign:      w.CallSign,
		ShipName:      w.ShipName,
		Destination:   w.Destination,
		MessageType:   w.MessageType,
		PayloadHash:   payloadHash,
		CanonicalHash: canonicalHash,
		DeliverySeq:   seq,
		ObservedAt:    uint64(w.TimestampUTC), // #nosec G115 -- normalize is only ever called (by handleRaw) on a wireMessage that already passed validate(), which rejects TimestampUTC <= 0
		ReceivedAt:    receivedAt,
		NormalizedAt:  normalizedAt,
	}
	if w.MMSI != nil {
		ev.MMSI = *w.MMSI
	}
	if w.IMO != nil {
		ev.IMO = *w.IMO
	}
	if w.Latitude != nil && w.Longitude != nil {
		ev.HasPosition = true
		ev.Latitude = *w.Latitude
		ev.Longitude = *w.Longitude
	}
	if w.Cog != nil {
		ev.HasCourse = true
		ev.CourseDeg = *w.Cog
	}
	if w.Sog != nil {
		ev.HasSpeed = true
		ev.SpeedKnots = *w.Sog
	}
	if w.NavStatus != nil {
		ev.HasNavStatus = true
		ev.NavStatus = *w.NavStatus
	}
	return ev
}

// ToOntologyEvidence bridges a NormalizedEvent into a real, validated
// ontology.Evidence{Type: ontology.TypeAISObservation, ...}, wiring
// Attributes["mmsi"] — ontology's own requiredAttributes entry for
// this type, see pkg/evidence/ontology/ontology.go — plus every other
// facet this event knows. It calls only the existing, unmodified
// ontology.New constructor; this package never redefines or bypasses
// ontology's own validation.
//
// source is the ontology-level Source string identifying who is
// asserting this evidence (typically the same value as this
// connector's Config.SourceID); confidence must be in [0,1] per
// ontology.Evidence's own rules — this package does not invent a
// confidence-scoring model, callers supply one appropriate to their
// deployment.
func (e NormalizedEvent) ToOntologyEvidence(source string, confidence float64) (ontology.Evidence, error) {
	attrs := map[string]string{"mmsi": fmt.Sprintf("%d", e.MMSI)}
	if e.IMO != 0 {
		attrs["imo"] = fmt.Sprintf("%d", e.IMO)
	}
	if e.CallSign != "" {
		attrs["call_sign"] = e.CallSign
	}
	if e.ShipName != "" {
		attrs["ship_name"] = e.ShipName
	}
	if e.Destination != "" {
		attrs["destination"] = e.Destination
	}
	if e.HasPosition {
		attrs["latitude"] = fmt.Sprintf("%.6f", e.Latitude)
		attrs["longitude"] = fmt.Sprintf("%.6f", e.Longitude)
	}
	if e.HasCourse {
		attrs["course_deg"] = fmt.Sprintf("%.2f", e.CourseDeg)
	}
	if e.HasSpeed {
		attrs["speed_kn"] = fmt.Sprintf("%.2f", e.SpeedKnots)
	}
	if e.HasNavStatus {
		attrs["nav_status"] = fmt.Sprintf("%d", e.NavStatus)
	}
	attrs["payload_hash"] = e.PayloadHash
	attrs["canonical_hash"] = e.CanonicalHash

	return ontology.New(ontology.Evidence{
		Type:       ontology.TypeAISObservation,
		Subject:    fmt.Sprintf("vessel:mmsi:%d", e.MMSI),
		Predicate:  "ais_observed",
		Object:     e.MessageType,
		Source:     source,
		ObservedAt: e.ObservedAt,
		ReceivedAt: e.ReceivedAt,
		Confidence: confidence,
		Attributes: attrs,
	})
}
