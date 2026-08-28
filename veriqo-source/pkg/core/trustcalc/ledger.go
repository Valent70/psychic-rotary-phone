// Belief Ledger — the P0 gap named identically by both the v7.10
// short-form audit ("Belief Ledger: Observe() -> immutable event ->
// hash chain -> replay -> exact state equality. Belum ada.") and the
// full repository audit ("Trust calculation is real; trust-history
// replayability is incomplete").
//
// Design discipline carried over from pkg/moat/digitaltwin (the
// repo's other hash-chained replay log, which this package
// deliberately mirrors rather than reinventing):
//
//   - Every Observe() call, when a Ledger is attached, is first
//     appended as an immutable Entry before the in-memory Beta
//     posterior is mutated. The chain hash covers Seq, Subject,
//     Matched, Weight and PrevHash — NOT wall-clock time, so the
//     chain (and any downstream replay) is byte-reproducible from the
//     logical event sequence alone. This directly addresses the full
//     audit's §27 finding about wall-clock telemetry leaking into
//     canonical/replayed evidence: WallClock is recorded on Entry as
//     metadata for operators, but deliberately excluded from Hash.
//   - Replay(priorAlpha, priorBeta) rebuilds a brand-new Calculus from
//     the ledger alone and returns it, so a caller can assert
//     replayed.Belief(subject) == live.Belief(subject) field-for-field
//     — the exact "same history -> Trust State A; replay(history) ->
//     Trust State B; A == B" proof the short-form audit's recommended
//     next step spelled out verbatim.
//   - VerifyChain re-derives every hash and rejects any tampering,
//     the same mechanical (not claimed) verification discipline
//     scripts/verify.sh already applies to the rest of the repo.
package trustcalc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"sync"
	"time"
)

// Entry is one immutable, hash-chained record of a single Observe()
// call. WallClock is informational only (operator telemetry) and is
// explicitly excluded from Hash — see package doc.
type Entry struct {
	Seq       uint64
	Subject   string
	Matched   bool
	Weight    float64
	WallClock time.Time
	PrevHash  string
	Hash      string
}

// Ledger is an append-only, hash-chained log of Observe() events for
// one Calculus. Safe for concurrent use.
type Ledger struct {
	mu      sync.RWMutex
	entries []Entry
}

// NewLedger returns an empty Ledger, genesis-linked (PrevHash of the
// first entry is the fixed genesis hash below, matching the pattern
// pkg/moat/digitaltwin already uses for its own chain).
func NewLedger() *Ledger { return &Ledger{} }

const ledgerGenesisHash = "TRUSTCALC_LEDGER_GENESIS"

func hashEntry(prevHash string, seq uint64, subject string, matched bool, weight float64) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], seq)
	h.Write(seqBuf[:])
	h.Write([]byte(subject))
	if matched {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	var wBuf [8]byte
	binary.BigEndian.PutUint64(wBuf[:], math.Float64bits(weight))
	h.Write(wBuf[:])
	return hex.EncodeToString(h.Sum(nil))
}

// append records one Observe() event and returns the new Entry. Not
// exported: only Calculus.Observe should append, so the ledger can
// never drift from the actual Beta updates it claims to record.
func (l *Ledger) append(subject string, matched bool, weight float64) Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := ledgerGenesisHash
	seq := uint64(len(l.entries))
	if seq > 0 {
		prev = l.entries[seq-1].Hash
	}
	e := Entry{
		Seq:       seq,
		Subject:   subject,
		Matched:   matched,
		Weight:    weight,
		WallClock: time.Now().UTC(),
		PrevHash:  prev,
		Hash:      hashEntry(prev, seq, subject, matched, weight),
	}
	l.entries = append(l.entries, e)
	return e
}

// Entries returns a defensive copy of the full immutable history, in
// order, for audit/export.
func (l *Ledger) Entries() []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Len is the number of recorded events.
func (l *Ledger) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

// VerifyChain mechanically re-derives every entry's hash from its
// content and PrevHash and reports the first mismatch found — proving
// (not claiming) the log has not been tampered with or reordered.
func (l *Ledger) VerifyChain() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	prev := ledgerGenesisHash
	for i, e := range l.entries {
		if e.Seq != uint64(i) {
			return fmt.Errorf("trustcalc: ledger entry %d has out-of-order Seq=%d", i, e.Seq)
		}
		if e.PrevHash != prev {
			return fmt.Errorf("trustcalc: ledger entry %d PrevHash mismatch: expected %s, got %s", i, prev, e.PrevHash)
		}
		want := hashEntry(e.PrevHash, e.Seq, e.Subject, e.Matched, e.Weight)
		if e.Hash != want {
			return fmt.Errorf("trustcalc: ledger entry %d hash mismatch (tampered content): expected %s, got %s", i, want, e.Hash)
		}
		prev = e.Hash
	}
	return nil
}

// Replay deterministically rebuilds a brand-new Calculus by re-running
// every recorded Observe() in sequence against a fresh Beta(priorAlpha,
// priorBeta) prior — the ledger's raison d'être. The returned
// Calculus is NOT wired back to this Ledger (Observe on it will not
// append here), so callers can freely compare it against the live
// Calculus without risk of double-recording.
//
// This is the concrete mechanism behind the audit's required
// adversarial proof: given the SAME history, live.Belief(subject) and
// Replay(...).Belief(subject) must be byte-for-byte / field-for-field
// equal for every subject — see TestBeliefLedgerReplayEqualsLiveState.
func (l *Ledger) Replay(priorAlpha, priorBeta float64) (*Calculus, error) {
	if err := l.VerifyChain(); err != nil {
		return nil, fmt.Errorf("trustcalc: refusing to replay a tampered ledger: %w", err)
	}
	l.mu.RLock()
	entries := make([]Entry, len(l.entries))
	copy(entries, l.entries)
	l.mu.RUnlock()

	replayed := New(priorAlpha, priorBeta)
	for _, e := range entries {
		if _, err := replayed.Observe(e.Subject, e.Matched, e.Weight); err != nil {
			return nil, fmt.Errorf("trustcalc: replay failed at seq %d: %w", e.Seq, err)
		}
	}
	return replayed, nil
}

// NewWithLedger builds a Calculus exactly like New, but every
// subsequent Observe() call is also appended to ledger as an
// immutable, hash-chained event BEFORE the in-memory Beta posterior is
// updated — so the ledger can never be missing an event the live
// Calculus reflects. ledger may be shared/inspected concurrently via
// its own exported methods.
func NewWithLedger(priorAlpha, priorBeta float64, ledger *Ledger) *Calculus {
	c := New(priorAlpha, priorBeta)
	c.ledger = ledger
	return c
}

// Ledger returns the Calculus's attached Ledger, or nil if none was
// configured (New, not NewWithLedger).
func (c *Calculus) Ledger() *Ledger { return c.ledger }
