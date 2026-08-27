// Package core defines the foundational contracts for Veriqo's Enterprise OS.
// Every package in the system must operate within these type boundaries.
// VEP-001: Foundation Types — DRC, DARI, VectorClock, TraceID.
package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

// ─── Identity primitives ─────────────────────────────────────────────────────

// TraceID is a 128-bit globally unique identifier for request tracing.
type TraceID [16]byte

// NewTraceID generates a cryptographically random TraceID.
func NewTraceID() TraceID {
	var id TraceID
	if _, err := rand.Read(id[:]); err != nil {
		panic(fmt.Sprintf("core: failed to generate TraceID: %v", err))
	}
	return id
}

// String returns the hex-encoded TraceID.
func (t TraceID) String() string { return hex.EncodeToString(t[:]) }

// Zero returns true if the TraceID is the zero value.
func (t TraceID) Zero() bool { return t == TraceID{} }

// TenantID uniquely identifies a tenant within the OS.
type TenantID string

// ProcessID uniquely identifies a TrustProcess execution.
type ProcessID string

// DomainID identifies a registered domain (maritime, commodity, trade, supplychain).
type DomainID string

// NodeID is the stable identity of a cluster node.
type NodeID string

// ─── VectorClock ─────────────────────────────────────────────────────────────

// VectorClock is a Lamport vector clock for causal ordering across nodes.
type VectorClock map[NodeID]uint64

// Increment returns a new VectorClock with the given node's counter incremented.
func (vc VectorClock) Increment(n NodeID) VectorClock {
	next := vc.Clone()
	next[n]++
	return next
}

// Merge returns the element-wise maximum of two vector clocks.
func (vc VectorClock) Merge(other VectorClock) VectorClock {
	result := vc.Clone()
	for k, v := range other {
		if result[k] < v {
			result[k] = v
		}
	}
	return result
}

// HappensBefore returns true if vc → other (strict causal predecessor).
func (vc VectorClock) HappensBefore(other VectorClock) bool {
	atLeastOne := false
	for k, v := range vc {
		if other[k] < v {
			return false
		}
		if other[k] > v {
			atLeastOne = true
		}
	}
	for k, v := range other {
		if _, ok := vc[k]; !ok && v > 0 {
			atLeastOne = true
		}
	}
	return atLeastOne
}

// Concurrent returns true if neither clock causally precedes the other.
func (vc VectorClock) Concurrent(other VectorClock) bool {
	return !vc.HappensBefore(other) && !other.HappensBefore(vc)
}

// Clone performs a deep copy.
func (vc VectorClock) Clone() VectorClock {
	c := make(VectorClock, len(vc))
	for k, v := range vc {
		c[k] = v
	}
	return c
}

// ─── DRC — Determinism-Reliability Contract ───────────────────────────────────

// DRC captures the operational contract a process must honour.
// Every execution must prove compliance via Evidence.
type DRC struct {
	// Determinism: the same inputs must always produce the same outputs.
	InputSchema           string   // JSON Schema ID for input validation
	ExecutionGraphID      string   // Identifies the algorithm/pipeline version
	AllowedNonDeterminism []string // explicit allowed sources (e.g. "clock.wall")

	// Reliability: operational quality targets.
	SLOTarget      float64 // 0.0–1.0 (e.g. 0.999 = 99.9%)
	MaxLatency     time.Duration
	RetryPolicy    RetryPolicy
	CircuitBreaker CircuitBreakerPolicy
}

// RetryPolicy configures retry behaviour on transient failure.
type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
}

// CircuitBreakerPolicy configures the circuit breaker.
type CircuitBreakerPolicy struct {
	Threshold      int // consecutive failures to trip
	HalfOpenAfter  time.Duration
	SuccessToClose int // successes needed to close from half-open
}

// ─── DARI — Determinism-Audit-Reliability-Interpretability ───────────────────

// DARI is the full runtime contract attached to every TrustProcess execution.
// It is stored alongside the execution evidence so every decision can be
// replayed, audited, and explained.
type DARI struct {
	// Determinism proof
	TraceID          TraceID
	InputHash        [32]byte // SHA-256 of canonicalised inputs
	ExecutionGraphID string
	Clock            VectorClock

	// Audit trail
	EvidenceStoreID string
	Provenance      []ProvenanceEntry
	Timestamp       time.Time

	// Reliability contract
	DRC DRC

	// Interpretability
	DomainModelID   string
	ExplanationHook string // registered explanation handler ID
}

// ProvenanceEntry records a single causal ancestor of this execution.
type ProvenanceEntry struct {
	TraceID   TraceID
	Relation  ProvenanceRelation
	Summary   string
	Timestamp time.Time
}

// ProvenanceRelation describes the causal link.
type ProvenanceRelation string

const (
	ProvenanceDerivedFrom ProvenanceRelation = "derived_from"
	ProvenanceTriggeredBy ProvenanceRelation = "triggered_by"
	ProvenanceAggregates  ProvenanceRelation = "aggregates"
	ProvenanceSupersedes  ProvenanceRelation = "supersedes"
)

// ─── Canonical result types ───────────────────────────────────────────────────

// Severity is a standardised risk/severity scale used across all domains.
type Severity int

const (
	SeverityNone     Severity = 0
	SeverityInfo     Severity = 1
	SeverityLow      Severity = 2
	SeverityMedium   Severity = 3
	SeverityHigh     Severity = 4
	SeverityCritical Severity = 5
)

func (s Severity) String() string {
	return [...]string{"NONE", "INFO", "LOW", "MEDIUM", "HIGH", "CRITICAL"}[s]
}

// Decision is the canonical output of any OS decision engine.
type Decision struct {
	Approved    bool
	Severity    Severity
	Explanation string
	Confidence  float64 // 0.0–1.0
	DARI        DARI
}

// ─── Sequence generator ───────────────────────────────────────────────────────

// Seq is a monotonic sequence counter safe for concurrent use.
type Seq struct{ n atomic.Uint64 }

// Next returns the next sequence number.
func (s *Seq) Next() uint64 { return s.n.Add(1) }

// Last returns the current value without incrementing.
func (s *Seq) Last() uint64 { return s.n.Load() }
