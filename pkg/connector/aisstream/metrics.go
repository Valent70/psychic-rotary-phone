package aisstream

import "sync/atomic"

// Metrics is a small set of plain exported counters, safe for
// concurrent use (each field is an atomic.Uint64) — no external
// metrics library, matching the rest of this repo's stdlib-only
// discipline.
type Metrics struct {
	// Received counts every raw message read off the wire, valid or
	// not, heartbeat or data.
	Received atomic.Uint64
	// Accepted counts messages that decoded successfully, passed
	// every configured filter, and were delivered to the Handler as a
	// NormalizedEvent.
	Accepted atomic.Uint64
	// Rejected counts malformed messages (invalid JSON, or valid JSON
	// missing a required field) — always counted, never silently
	// dropped.
	Rejected atomic.Uint64
	// Filtered counts well-formed, non-heartbeat messages that were
	// excluded by the bounding-box, MMSI, or message-type filter.
	Filtered atomic.Uint64
	// Heartbeats counts heartbeat/keepalive messages observed.
	Heartbeats atomic.Uint64
	// Reconnects counts successful (re)connections established,
	// including the very first one.
	Reconnects atomic.Uint64
	// ReconnectFailures counts failed dial attempts.
	ReconnectFailures atomic.Uint64
}

// MetricsSnapshot is a point-in-time, non-atomic copy of Metrics,
// convenient for assertions and logging.
type MetricsSnapshot struct {
	Received          uint64
	Accepted          uint64
	Rejected          uint64
	Filtered          uint64
	Heartbeats        uint64
	Reconnects        uint64
	ReconnectFailures uint64
}

// Snapshot returns a consistent-enough (each field loaded
// independently) copy of the current counters.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Received:          m.Received.Load(),
		Accepted:          m.Accepted.Load(),
		Rejected:          m.Rejected.Load(),
		Filtered:          m.Filtered.Load(),
		Heartbeats:        m.Heartbeats.Load(),
		Reconnects:        m.Reconnects.Load(),
		ReconnectFailures: m.ReconnectFailures.Load(),
	}
}
