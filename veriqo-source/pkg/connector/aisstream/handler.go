package aisstream

import "veriqo/pkg/evidence/provenance"

// Handler receives the outcome of processing every raw message this
// connector reads. Exactly one of HandleAccepted / HandleFiltered /
// HandleRejected / HandleHeartbeat is called per raw message — a
// message is never silently dropped without one of these firing.
type Handler interface {
	// HandleAccepted is called for a well-formed, non-heartbeat
	// message that passed every configured filter: event is the
	// normalized maritime event (PART 4), evidence is the
	// provenance.ExternalEvidence constructed for it (PART 3),
	// already Validate()-checked.
	HandleAccepted(event NormalizedEvent, evidence provenance.ExternalEvidence)
	// HandleFiltered is called for a well-formed, non-heartbeat
	// message excluded by the bounding-box, MMSI, or message-type
	// filter. evidence is still a fully constructed, validated
	// provenance envelope — filtering happens after provenance
	// construction, not instead of it, so a filtered-out message
	// still leaves an audit trail of having arrived. reason is a
	// short machine-readable string identifying which filter
	// excluded it.
	HandleFiltered(evidence provenance.ExternalEvidence, reason string)
	// HandleRejected is called for a malformed message: invalid JSON,
	// or JSON missing a required field. raw is the original bytes as
	// received (never mutated), err wraps ErrMalformedMessage with
	// the concrete reason.
	HandleRejected(raw []byte, err error)
	// HandleHeartbeat is called for a heartbeat/keepalive message.
	// observedAt is that message's own reported TimestampUTC (unix
	// seconds).
	HandleHeartbeat(observedAt int64)
}
