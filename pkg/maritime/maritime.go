// Package maritime provides a small, shared interface for maritime
// evidence-source adapters -- AIS position reports, port-call records,
// Notice of Readiness, Statement of Facts, and customer/counterparty
// claims -- that structurally normalize external-shaped evidence into
// Evidence records a caller can feed toward VERIQO's native evidence
// pipeline (canonical.SourceSubmission, lifecycle.EvidencePlan). This
// package never decides anything: it is purely an ingestion adapter,
// matching pkg/rwc's own "no second decision engine" discipline (see
// pkg/rwc/run.go's doc comment) applied one layer further upstream, at
// evidence acquisition rather than case construction.
//
// Package layout: one shared package rather than one tiny package per
// adapter (pkg/maritime/ais, pkg/maritime/portcall, ...). The literal
// per-adapter package layout was a suggestion in the originating audit
// text ("Claude coding sekarang: pkg/maritime/ais/, pkg/maritime/
// portcall/..."), not a hard requirement -- the requirement is that
// AIS/PortCall/NOR/SOF/CustomerClaims adapters exist and share one
// interface. Four nearly-identical single-type packages would only
// fragment import paths for no benefit; one package with clear
// per-file organization (ais.go, portcall.go, nor.go, sof.go,
// claims.go) satisfies the same intent with less Go ceremony.
//
// Not to be confused with pkg/moat/domain/maritime (also `package
// maritime`, at a different import path): that package is the typed
// domain-entity ontology (Vessel/Port/Voyage identity + relationships)
// consumed by the fusion/causal engines. This package sits upstream of
// that -- structurally normalized evidence an adapter fetched from an
// external-shaped source, before anything is resolved into a canonical
// entity. The two are deliberately separate concerns and this package
// does not import the other.
//
// Determinism: FetchRequest.Since/Until are logical ticks, not
// wall-clock time, and Evidence.Timestamp is likewise a logical tick --
// this repository forbids time.Now() on any deterministic path (see
// pkg/blockers/dr/dr.go's own doc comment for the same rule applied to
// the DR harness). Fixture-mode adapters in this package never call
// time.Now().
package maritime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Capabilities declares which acquisition modes one MaritimeEvidenceSource
// implementation actually supports. A caller must check this before
// assuming e.g. SupportsRealAPI is safe to rely on -- see each adapter's
// own doc comment for what its real-file/real-API mode currently does
// (a documented "not implemented in this environment" stub, same
// honesty discipline as this round's AWSKMSProvider and
// HTTPVulnerabilityProvider).
type Capabilities struct {
	SupportsFixture  bool
	SupportsReplay   bool
	SupportsRealFile bool
	SupportsRealAPI  bool
}

// FetchRequest is one evidence query against a MaritimeEvidenceSource.
// Since/Until are logical ticks (inclusive), matching every other
// caller-injected clock in this codebase (see pkg/rwc.CaseRequest.Tick's
// own doc comment) -- never wall-clock time.
type FetchRequest struct {
	VesselID string
	PortID   string
	Since    uint64
	Until    uint64
}

// Evidence is one structurally normalized maritime evidence record --
// the output every adapter in this package produces, regardless of
// which external-shaped source (AIS, a port-call system, an NOR/SOF
// document, a customer claim) it came from.
type Evidence struct {
	EventType  string // "arrival", "departure", "berthing", "nor_tendered", "sof_event", "claim", ...
	Timestamp  uint64 // logical tick, never time.Now()
	Location   string
	DeliveryID string // e.g. voyage/call reference; "" if not applicable to this EventType
	Sequence   int    // this record's position within its source's own delivery order
	RawPayload []byte // the adapter's own structured encoding of this record, for audit/replay

	// Provenance fields. pkg/evidenceorigin does not exist in this tree
	// as of this package's implementation (checked directly, not
	// assumed) -- if it lands later, its Envelope is the natural home
	// for these fields instead of this inline set; until then SourceID/
	// Provider follow canonical.SourceSubmission's own established
	// naming (see pkg/canonical/canonical.go) so a caller building a
	// SourceSubmission from an Evidence has a direct, unambiguous field
	// mapping.
	SourceID string
	Provider string
	Hash     string // sha256 over the record's content -- see HashEvidence
}

// MaritimeEvidenceSource is the shared interface every adapter in this
// package implements.
type MaritimeEvidenceSource interface {
	SourceID(ctx context.Context) (string, error)
	Capabilities(ctx context.Context) (Capabilities, error)
	Fetch(ctx context.Context, request FetchRequest) ([]Evidence, error)
}

// errRealModeNotImplemented is returned by every adapter's real-file/
// real-API path in this environment -- honestly, not silently degraded
// to a fixture response. See each adapter's own doc comment for what a
// real implementation would need to plug in.
type errRealModeNotImplemented struct{ adapter, mode string }

func (e *errRealModeNotImplemented) Error() string {
	return "maritime: " + e.adapter + ": " + e.mode + " mode is not implemented in this environment (no network/file egress available to verify against) -- SIMULATED/fixture mode is the only mode this package can honestly claim works here"
}

func newRealModeNotImplemented(adapter, mode string) error {
	return &errRealModeNotImplemented{adapter: adapter, mode: mode}
}

// hashEvidence computes the one deterministic content hash every
// fixture record in this package uses -- SHA-256 over the fields that
// identify it as the same real-world event, same discipline as
// pkg/rwc.sha256Hex / pkg/blockers/livedata.ComputeHash (each subsystem
// hand-rolls its own versioned SHA-256 format; this is this package's).
func hashEvidence(domain, eventType, vesselOrPortID string, timestamp uint64, seq int, payload []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.maritime/v1|domain=%s|event=%s|subject=%s|ts=%d|seq=%d|", domain, eventType, vesselOrPortID, timestamp, seq)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
