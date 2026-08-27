package raftlite

import "time"

// Timeout profiles for common deployment topologies.
//
// Background (honest note): prior sessions shipped ElectionTimeoutMin/Max
// as Config fields but NewNode silently discarded them (`_ =
// cfg.ElectionTimeoutMin`), so every node ran the same hardcoded 150-300ms
// LAN-tuned window regardless of what a caller configured. That is a real
// bug, not a design choice — it is why TestChaos_ClockSkew_LeaderElectedDespiteSkew
// was flaky: there was no way to actually widen the window for a skewed
// link, because the knob was wired to nothing. Fixed in raft.go (Node now
// stores and uses n.electionMin/n.electionMax). These profiles are the
// first real consumers of the fix.
//
// The only correctness rule that matters here (Raft dissertation §9.3,
// broadly restated): heartbeat interval << expected one-way message
// latency + clock skew << election timeout. If skew or link latency can
// approach the election timeout, followers will spuriously time out and
// trigger unnecessary elections — exactly the flakiness this file fixes.

// LANTimeouts is the default: low-latency, low-skew network (same
// datacenter / same rack). This matches the previous hardcoded behavior.
func LANTimeouts() (min, max, heartbeat time.Duration) {
	return 150 * time.Millisecond, 300 * time.Millisecond, 50 * time.Millisecond
}

// WANTimeouts is for cross-region or otherwise lossy/high-latency links
// where clock skew and network jitter of 50-150ms are expected. The
// election window is widened proportionally (roughly 4x the LAN profile)
// so that skew alone cannot regularly exceed it, while the heartbeat
// interval is widened enough to keep the ratio (heartbeat << election
// timeout) intact rather than just scaling the timeout in isolation.
func WANTimeouts() (min, max, heartbeat time.Duration) {
	return 600 * time.Millisecond, 1200 * time.Millisecond, 150 * time.Millisecond
}

// ApplyTimeoutProfile fills a Config's timeout fields from a (min, max,
// heartbeat) triple, but only for zero-valued fields, so callers can
// override individual values after calling this.
func ApplyTimeoutProfile(cfg *Config, min, max, heartbeat time.Duration) {
	if cfg.ElectionTimeoutMin == 0 {
		cfg.ElectionTimeoutMin = min
	}
	if cfg.ElectionTimeoutMax == 0 {
		cfg.ElectionTimeoutMax = max
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = heartbeat
	}
}
