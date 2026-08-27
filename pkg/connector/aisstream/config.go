// Package aisstream implements PART 3 and PART 4 of the external-
// integration audit mandate: an isolated AISstream-compatible AIS
// (Automatic Identification System) stream connector and its message
// normalization into canonical VERIQO maritime events.
//
// Honest scope, stated once here rather than buried: this package has
// never been run against the real aisstream.io endpoint. This sandbox
// has no route to that host, a real deployment needs a real API key
// and a real commercial/data-rights decision (see the provenance
// discussion below), and neither is in scope for a code-only task.
// What this package DOES provide, and what its tests DO exercise, is
// the full connector state machine — config-driven filtering,
// reconnect-with-backoff, heartbeat handling, malformed-message
// rejection, hashing, provenance construction, and normalization —
// against an injected fake transport, with zero real network access.
//
// # Transport seam
//
// This package deliberately does NOT hand-roll RFC 6455 WebSocket
// framing. Per the audit's own guidance ("simpler and equally valid:
// implement it as an interface-driven design where the actual
// net.Conn/transport is injectable"), the wire is abstracted behind
// Transport/MessageStream: a real implementation dials aisstream.io
// (or any AISstream-compatible endpoint) over TLS and speaks the
// WebSocket framing itself, using only net/http and stdlib crypto/tls
// underneath; this package's tests instead supply a small in-memory
// fake that never touches a socket. Swapping in a real Transport is
// the only change needed to go live — the connector, filtering,
// hashing, provenance and normalization logic here does not change.
//
// # Provenance defaults (hard requirement, non-negotiable)
//
// Per the audit doc, verbatim: "DO NOT classify AISstream data as
// QUALIFIED_EXTERNAL_EVIDENCE or VERIFIED_EXTERNAL_EVIDENCE
// automatically. Default: origin_class = REAL_EXTERNAL_UNVERIFIED,
// rights_state = UNKNOWN_PENDING_CONTRACT, unless explicitly
// configured otherwise through an authorized source policy." This
// package has NO configuration field, override, or code path that
// produces any OriginClass/RightsState other than
// provenance.OriginRealExternalUnverified /
// provenance.RightsUnknownPendingContract on every single
// provenance.ExternalEvidence it constructs — that is a hard,
// unconditional invariant of handleRaw, not a default that a caller
// can bypass. Upgrading a source's trust is an external, deliberate,
// attributable act that happens ONLY through
// pkg/evidence/provenance.Registry.GrantTrust, entirely outside this
// package. This is the "better" option the audit doc itself names,
// and it is the one implemented here.
package aisstream

import (
	"fmt"
	"strings"
	"time"
)

// SecretProvider supplies the AISstream API key from wherever a
// deployment actually keeps secrets (environment variable, secret
// manager, vault) — never hardcoded, and never read directly by this
// package from os.Getenv itself, so a test or a deployment can swap in
// any backing store without this package caring.
type SecretProvider interface {
	APIKey() (string, error)
}

// BoundingBox is a single [southwest, northeast]-style box, expressed
// as [2][2]float64{ {lat1, lon1}, {lat2, lon2} }. Corner order within
// each pair does not matter — boxContains normalizes min/max itself —
// so callers may supply either diagonal.
type BoundingBox [2][2]float64

// Config is the configuration-driven description of one AISstream
// subscription: which endpoint, which geography, which vessels, which
// message types, and how aggressively to reconnect. Every connector
// behavior PART 3 requires is driven from this struct — nothing here
// is hardcoded in client.go.
type Config struct {
	// URL is the AISstream-compatible WebSocket endpoint. Stored and
	// handed to Transport.Connect verbatim; this package never dials
	// it directly.
	URL string

	// BoundingBoxes filters received messages to those reporting a
	// position inside at least one box. Empty means "no bounding-box
	// filter" (all positions pass).
	BoundingBoxes []BoundingBox
	// MMSIFilter restricts accepted messages to these MMSIs. Empty
	// means "no MMSI filter".
	MMSIFilter []int64
	// MessageTypeFilter restricts accepted messages to these
	// MessageType values (e.g. "PositionReport", "ShipStaticData").
	// Empty means "no message-type filter".
	MessageTypeFilter []string

	// HeartbeatTimeout, if > 0, is the maximum time this connector
	// will wait for ANY message (heartbeat or data) before treating
	// the connection as stale and forcing a reconnect. 0 disables
	// heartbeat-timeout detection.
	HeartbeatTimeout time.Duration
	// RespondToHeartbeat, if true, makes the connector write a small
	// HeartbeatAck frame back on the stream whenever it observes a
	// heartbeat message — a best-effort keepalive response; failure
	// to send it is not fatal on its own (the next read's heartbeat
	// timeout will notice a truly dead connection).
	RespondToHeartbeat bool

	// MaxRetries bounds the number of consecutive reconnect failures
	// (dial failures or dropped/timed-out connections) before Run
	// gives up and returns ErrMaxRetriesExceeded. 0 means unlimited
	// retries — Run then only ever exits via ctx cancellation.
	MaxRetries int
	// InitialBackoff, MaxBackoff, BackoffMultiplier define the
	// exponential, capped backoff applied before every reconnect
	// attempt after the first.
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64

	// RawRetention controls what happens to each raw message's bytes
	// after processing. Nil means "use the connector's default"
	// (a bounded, most-recent-N ring buffer) — this package never
	// unconditionally discards raw bytes without an explicit,
	// documented retention policy in place.
	RawRetention RawRetention

	// ProviderID, DatasetID, SourceID identify this subscription in
	// every provenance.ExternalEvidence this connector constructs.
	// All three are required (validate() rejects empty values).
	ProviderID string
	DatasetID  string
	SourceID   string
}

// DefaultConfig returns a Config with sane, documented defaults for
// every field withDefaults() is willing to backfill. It is not a
// complete, ready-to-run Config on its own (URL/API-key-bearing
// deployments still need their own bounding boxes / filters), but a
// starting point.
func DefaultConfig() Config {
	return Config{
		URL:               "wss://stream.aisstream.io/v0/stream",
		InitialBackoff:    time.Second,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 2.0,
		ProviderID:        "aisstream.io",
		DatasetID:         "ais-positions",
		SourceID:          "aisstream-ws",
	}
}

// withDefaults backfills zero-valued fields from DefaultConfig. It
// never overrides a value the caller actually set.
func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if strings.TrimSpace(c.URL) == "" {
		c.URL = d.URL
	}
	if c.InitialBackoff <= 0 {
		c.InitialBackoff = d.InitialBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = d.MaxBackoff
	}
	if c.BackoffMultiplier <= 1 {
		c.BackoffMultiplier = d.BackoffMultiplier
	}
	if strings.TrimSpace(c.ProviderID) == "" {
		c.ProviderID = d.ProviderID
	}
	if strings.TrimSpace(c.DatasetID) == "" {
		c.DatasetID = d.DatasetID
	}
	if strings.TrimSpace(c.SourceID) == "" {
		c.SourceID = d.SourceID
	}
	return c
}

// validate enforces every structural invariant Run relies on. Called
// once, inside New, so a misconfigured Connector can never be
// constructed in the first place — fail closed at construction time
// rather than failing confusingly mid-stream.
func (c Config) validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("%w: url is empty", ErrConfigInvalid)
	}
	if strings.TrimSpace(c.ProviderID) == "" {
		return fmt.Errorf("%w: provider_id is empty", ErrConfigInvalid)
	}
	if strings.TrimSpace(c.DatasetID) == "" {
		return fmt.Errorf("%w: dataset_id is empty", ErrConfigInvalid)
	}
	if strings.TrimSpace(c.SourceID) == "" {
		return fmt.Errorf("%w: source_id is empty", ErrConfigInvalid)
	}
	if c.InitialBackoff <= 0 {
		return fmt.Errorf("%w: initial_backoff must be > 0", ErrConfigInvalid)
	}
	if c.MaxBackoff < c.InitialBackoff {
		return fmt.Errorf("%w: max_backoff must be >= initial_backoff", ErrConfigInvalid)
	}
	if c.BackoffMultiplier <= 1 {
		return fmt.Errorf("%w: backoff_multiplier must be > 1", ErrConfigInvalid)
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("%w: max_retries must be >= 0", ErrConfigInvalid)
	}
	if c.HeartbeatTimeout < 0 {
		return fmt.Errorf("%w: heartbeat_timeout must be >= 0", ErrConfigInvalid)
	}
	return nil
}
