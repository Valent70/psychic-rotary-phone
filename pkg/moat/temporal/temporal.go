// Package temporal implements Veriqo's Temporal (bitemporal) Knowledge
// Graph — closing the audit doc's explicit gap #2: "time travel query,
// bitemporal, transaction time, valid time, snapshot graph".
//
// It wraps pkg/moat/kg's deterministic hash-chained mutation model with
// a second time axis. kg.Graph already gives "transaction time" for
// free (every Mutation carries an Index = logical write order). This
// package adds:
//
//   - Valid time: the caller-supplied [ValidFrom, ValidTo) window during
//     which a fact is asserted to hold in the real world — independent
//     of when it was recorded (transaction time = the Tick it was
//     ingested). A vessel's flag state on 2024-03-01 can be corrected on
//     2024-06-01 (transaction time), without losing the original record
//     of "what we believed on 2024-03-01" (bitemporal correction).
//   - Snapshot-at-tick: reconstruct the graph exactly as it was known
//     AT a given transaction tick (audit / "what did we know when").
//   - AsOf(validTick): reconstruct the graph's belief about the WORLD as
//     of a given valid-time tick, using only facts whose valid window
//     covers it, using the LATEST correction on record for each fact
//     (time travel query over valid time, not transaction time).
//
// Design invariants (same discipline as kg/fusion/causal): no
// time.Now(), no UUIDs, append-only, hash-chained, deterministic replay.
package temporal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

var (
	ErrTamperDetected = errors.New("temporal: hash chain verification failed")
	ErrInvalidWindow  = errors.New("temporal: ValidTo must be zero (open-ended) or greater than ValidFrom")
)

// FactID identifies one temporal fact slot: a subject+predicate pair,
// analogous to fusion.Claim.Key(). Multiple Versions of the same FactID
// represent bitemporal corrections/updates over time.
type FactID string

func NewFactID(subject, predicate string) FactID {
	return FactID(subject + "|" + predicate)
}

// Version is one bitemporal assertion of a fact's value.
type Version struct {
	Fact  FactID
	Value string

	// Valid time: when this fact is asserted true in the real world.
	// ValidTo == 0 means "still open / no known end".
	ValidFrom uint64
	ValidTo   uint64

	// Transaction time: when Veriqo recorded this assertion (the logical
	// ingestion tick). Always monotonically non-decreasing per FactID in
	// practice, though the engine does not require it.
	TxTick uint64

	ActorID string
}

// Record is one hash-chained log entry.
type Record struct {
	Index    uint64
	Version  Version
	PrevHash string
	Hash     string
}

// Engine is the live bitemporal fact store.
type Engine struct {
	mu sync.RWMutex

	log  []Record
	head string

	// versionsByFact: all versions ever recorded for a fact, in
	// insertion (transaction) order — the "time travel over transaction
	// time" index.
	versionsByFact map[FactID][]Version
}

func NewEngine() *Engine {
	return &Engine{versionsByFact: make(map[FactID][]Version)}
}

func canonicalBytes(idx uint64, v Version, prev string) []byte {
	return []byte(fmt.Sprintf(
		"idx=%d|fact=%s|value=%s|validfrom=%d|validto=%d|txtick=%d|actor=%s|prev=%s|",
		idx, v.Fact, v.Value, v.ValidFrom, v.ValidTo, v.TxTick, v.ActorID, prev,
	))
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Assert records a new bitemporal version of a fact. It never mutates or
// removes a prior Version — corrections are new Versions with a later
// TxTick, preserving the full bitemporal history.
func (e *Engine) Assert(v Version) (Record, error) {
	_, span := telemetry.StartSpan(context.Background(), "temporal.Assert")
	defer span.End()

	e.mu.Lock()
	defer e.mu.Unlock()
	return e.assertLocked(v)
}

func (e *Engine) assertLocked(v Version) (Record, error) {
	if v.Fact == "" {
		return Record{}, errors.New("temporal: empty fact id")
	}
	if v.ValidTo != 0 && v.ValidTo <= v.ValidFrom {
		return Record{}, ErrInvalidWindow
	}

	index := uint64(len(e.log)) + 1
	prev := e.head
	cb := canonicalBytes(index, v, prev)
	h := hashOf(cb)

	rec := Record{Index: index, Version: v, PrevHash: prev, Hash: h}
	e.log = append(e.log, rec)
	e.head = h
	e.versionsByFact[v.Fact] = append(e.versionsByFact[v.Fact], v)

	return rec, nil
}

// SnapshotAtTransactionTick reconstructs, for every fact, the LATEST
// version whose TxTick <= tick — i.e. "what did Veriqo believe, across
// all facts, as of this point in recording history". This is the
// transaction-time snapshot / "what did we know when" query.
func (e *Engine) SnapshotAtTransactionTick(tick uint64) map[FactID]Version {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[FactID]Version)
	facts := sortedFactIDs(e.versionsByFact)
	for _, f := range facts {
		var latest *Version
		for i := range e.versionsByFact[f] {
			v := e.versionsByFact[f][i]
			if v.TxTick > tick {
				continue
			}
			if latest == nil || v.TxTick > latest.TxTick {
				vv := v
				latest = &vv
			}
		}
		if latest != nil {
			out[f] = *latest
		}
	}
	return out
}

// AsOf reconstructs Veriqo's CURRENT (latest transaction-time) belief
// about the state of the WORLD at a given valid-time tick — the true
// bitemporal time-travel query: "as of the most recent corrections we
// have, what did we believe was true in the world at valid-time T".
// Among versions whose [ValidFrom,ValidTo) window covers validTick, it
// picks the one with the highest TxTick (i.e. the most recent
// correction), so later corrections to historical facts are honored.
func (e *Engine) AsOf(validTick uint64) map[FactID]Version {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[FactID]Version)
	facts := sortedFactIDs(e.versionsByFact)
	for _, f := range facts {
		var best *Version
		for i := range e.versionsByFact[f] {
			v := e.versionsByFact[f][i]
			if !covers(v, validTick) {
				continue
			}
			if best == nil || v.TxTick > best.TxTick {
				vv := v
				best = &vv
			}
		}
		if best != nil {
			out[f] = *best
		}
	}
	return out
}

func covers(v Version, validTick uint64) bool {
	if validTick < v.ValidFrom {
		return false
	}
	if v.ValidTo != 0 && validTick >= v.ValidTo {
		return false
	}
	return true
}

// History returns every version ever recorded for a fact, in transaction
// order — the full bitemporal audit trail for that fact.
func (e *Engine) History(fact FactID) []Version {
	e.mu.RLock()
	defer e.mu.RUnlock()
	src := e.versionsByFact[fact]
	out := make([]Version, len(src))
	copy(out, src)
	return out
}

// Corrections returns only the versions of a fact that were themselves
// corrections — i.e. their valid-time window overlaps a version recorded
// EARLIER (lower TxTick) for the same fact — surfacing exactly the
// "we changed our mind about the past" events an auditor cares about.
func (e *Engine) Corrections(fact FactID) []Version {
	e.mu.RLock()
	defer e.mu.RUnlock()
	src := e.versionsByFact[fact]
	if len(src) < 2 {
		return nil
	}
	var out []Version
	for i := 1; i < len(src); i++ {
		cur := src[i]
		for j := 0; j < i; j++ {
			earlier := src[j]
			if windowsOverlap(cur, earlier) && cur.TxTick > earlier.TxTick {
				out = append(out, cur)
				break
			}
		}
	}
	return out
}

func windowsOverlap(a, b Version) bool {
	aEnd := a.ValidTo
	bEnd := b.ValidTo
	if aEnd == 0 {
		aEnd = ^uint64(0)
	}
	if bEnd == 0 {
		bEnd = ^uint64(0)
	}
	return a.ValidFrom < bEnd && b.ValidFrom < aEnd
}

func sortedFactIDs(m map[FactID][]Version) []FactID {
	out := make([]FactID, 0, len(m))
	for f := range m {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (e *Engine) Head() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.head
}

func (e *Engine) Snapshot() []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Record, len(e.log))
	copy(out, e.log)
	return out
}

func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for i, rec := range e.log {
		wantIndex := uint64(i) + 1
		if rec.Index != wantIndex {
			return fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		if rec.PrevHash != prev {
			return fmt.Errorf("%w: broken chain linkage at index %d", ErrTamperDetected, rec.Index)
		}
		recomputed := hashOf(canonicalBytes(rec.Index, rec.Version, rec.PrevHash))
		if recomputed != rec.Hash {
			return fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, rec.Index)
		}
		prev = recomputed
	}
	return nil
}

// Rebuild reconstructs a fresh Engine from an exported log, proving
// deterministic replay (same head, same AsOf/SnapshotAtTransactionTick
// results as the original).
func Rebuild(log []Record) (*Engine, error) {
	e := NewEngine()
	for i, rec := range log {
		wantIndex := uint64(i) + 1
		if rec.Index != wantIndex {
			return nil, fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		got, err := e.assertLocked(rec.Version)
		if err != nil {
			return nil, fmt.Errorf("temporal: replay assert failed at %d: %w", rec.Index, err)
		}
		if got.Hash != rec.Hash {
			return nil, fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, rec.Index)
		}
	}
	return e, nil
}
