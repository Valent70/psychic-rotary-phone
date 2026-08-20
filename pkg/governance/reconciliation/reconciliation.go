// Package reconciliation is external audit item A6's Evidence
// Reconciliation Engine: deterministic accounting over a stream of
// evidence envelopes, producing the audit's own named counters
// (input_count, accepted_count, rejected_count, duplicate_count,
// lost_count, reordered_count, hash_mismatch_count,
// unauthorized_accept_count, final_root) and checking its own named
// acceptance invariants.
//
// This is reused, per docs/AUDIT_A1_A18_GAP_MAPPING.md's A6 entry, by
// audit item A7's 100-node harness for exactly the accept/reject/
// duplicate/lost accounting a real qualification run needs — a caller
// building a bigger harness should call this package rather than
// re-implement counting, matching pkg/blockers/scale.IntegrityReport's
// narrower, harness-specific version of the same idea.
package reconciliation

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
)

// Envelope is one unit of evidence submitted for reconciliation.
type Envelope struct {
	ID         string
	Sequence   uint64
	Hash       string // claimed content hash
	Payload    string // content the hash should match — ComputeHash(Payload) must equal Hash
	Authorized bool   // whether the submitting principal is authorized to submit at all
}

// ComputeHash is the one hash function this package uses for envelope
// content — SHA-256, matching the repository-wide convention (see
// pkg/rwc/types.go's identical discipline for the same reason: one
// named, auditable hash path).
func ComputeHash(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// RejectReason is why Process refused an envelope.
type RejectReason string

const (
	ReasonDuplicate    RejectReason = "duplicate"
	ReasonHashMismatch RejectReason = "hash_mismatch"
	ReasonUnauthorized RejectReason = "unauthorized"
)

type rejectedEnvelope struct {
	Envelope Envelope
	Reason   RejectReason
}

// Engine processes envelopes one at a time, classifying each
// deterministically. It never "loses" an envelope itself — every
// Process call ends in exactly one of Accepted or Rejected, which is
// what backs the input==accepted+rejected invariant Finalize checks.
type Engine struct {
	mu sync.Mutex

	seenIDs   map[string]bool
	maxSeq    uint64
	sawAnySeq bool

	accepted []Envelope
	rejected []rejectedEnvelope
	reordered int
}

func NewEngine() *Engine {
	return &Engine{seenIDs: make(map[string]bool)}
}

// Process classifies one envelope: unauthorized submitters are rejected
// before any other check (a hard gate, checked first so it can never be
// bypassed by e.g. a duplicate check short-circuiting); then hash
// integrity; then duplication by ID. A sequence number lower than the
// highest already seen is counted as reordered (informational — it is
// NOT itself a rejection reason; a legitimately-reordered-but-authentic
// envelope is still real evidence).
func (e *Engine) Process(env Envelope) (accepted bool, reason RejectReason) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sawAnySeq && env.Sequence < e.maxSeq {
		e.reordered++
	}
	if env.Sequence > e.maxSeq || !e.sawAnySeq {
		e.maxSeq = env.Sequence
		e.sawAnySeq = true
	}

	if !env.Authorized {
		e.rejected = append(e.rejected, rejectedEnvelope{env, ReasonUnauthorized})
		return false, ReasonUnauthorized
	}
	if ComputeHash(env.Payload) != env.Hash {
		e.rejected = append(e.rejected, rejectedEnvelope{env, ReasonHashMismatch})
		return false, ReasonHashMismatch
	}
	if e.seenIDs[env.ID] {
		e.rejected = append(e.rejected, rejectedEnvelope{env, ReasonDuplicate})
		return false, ReasonDuplicate
	}

	e.seenIDs[env.ID] = true
	e.accepted = append(e.accepted, env)
	return true, ""
}

// Report is Finalize's full, auditable output — field names match the
// audit's own list verbatim.
type Report struct {
	InputCount              int
	AcceptedCount           int
	RejectedCount           int
	DuplicateCount          int
	LostCount               int
	ReorderedCount          int
	HashMismatchCount       int
	UnauthorizedAcceptCount int
	FinalRoot               string
}

// Finalize computes the full Report. expectedInputCount is the number
// of envelopes the caller declares were actually sent (e.g. from a
// manifest, or a workload generator's own declared count) — LostCount is
// deliberately derived from this EXTERNAL expectation, never guessed
// from what the engine itself received, because an engine cannot detect
// the absence of something it was never told to expect.
func (e *Engine) Finalize(expectedInputCount int) Report {
	e.mu.Lock()
	defer e.mu.Unlock()

	input := len(e.accepted) + len(e.rejected)
	lost := expectedInputCount - input
	if lost < 0 {
		// More envelopes arrived than were declared expected — not a
		// "loss"; report 0 here rather than a misleading negative, the
		// anomaly itself is visible via InputCount > expectedInputCount.
		lost = 0
	}

	duplicate, hashMismatch := 0, 0
	for _, r := range e.rejected {
		switch r.Reason {
		case ReasonDuplicate:
			duplicate++
		case ReasonHashMismatch:
			hashMismatch++
		}
	}

	// unauthorized_accept_count is an independent re-scan of Accepted,
	// not a running counter incremented inside Process — Process's own
	// gate should make this structurally impossible (Authorized==false
	// is rejected before an envelope ever reaches e.accepted), so this
	// re-scan is the same "verify the gate actually worked, don't just
	// trust it" discipline as pkg/rwc.InterpretVerdict's consistency
	// check: if this is ever non-zero, it proves the gate itself is
	// broken, which is exactly the security-relevant fact this counter
	// exists to surface.
	unauthorizedAccept := 0
	for _, a := range e.accepted {
		if !a.Authorized {
			unauthorizedAccept++
		}
	}

	return Report{
		InputCount: input, AcceptedCount: len(e.accepted), RejectedCount: len(e.rejected),
		DuplicateCount: duplicate, LostCount: lost, ReorderedCount: e.reordered,
		HashMismatchCount: hashMismatch, UnauthorizedAcceptCount: unauthorizedAccept,
		FinalRoot: computeFinalRoot(e.accepted),
	}
}

// computeFinalRoot is a deterministic Merkle-style root over every
// accepted envelope's own hash, sorted by ID first so the root does not
// depend on arrival order — the same "content-addressed, order-
// independent" discipline as pkg/moat/evidencegraph.Graph.RootHash.
func computeFinalRoot(accepted []Envelope) string {
	sorted := make([]Envelope, len(accepted))
	copy(sorted, accepted)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	h := sha256.New()
	h.Write([]byte("veriqo.reconciliation.final_root/v1\n"))
	for _, e := range sorted {
		h.Write([]byte(e.ID))
		h.Write([]byte{'|'})
		h.Write([]byte(e.Hash))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Invariant is one of the audit's named acceptance invariants.
type Invariant string

const (
	InvariantInputEqualsAcceptedPlusRejected Invariant = "input == accepted + rejected"
	InvariantZeroLost                        Invariant = "lost == 0"
	InvariantZeroUnauthorized                Invariant = "unauthorized == 0"
	InvariantZeroHashMismatch                Invariant = "hash_mismatch == 0"
)

// Verify checks Report against the audit's own 4 named acceptance
// invariants, returning every violated one (empty slice = all 4 hold).
// This is deterministic arithmetic over already-computed counters —
// no new classification logic, so Verify can never disagree with how
// Process/Finalize actually counted.
func (r Report) Verify() []Invariant {
	var violations []Invariant
	if r.InputCount != r.AcceptedCount+r.RejectedCount {
		violations = append(violations, InvariantInputEqualsAcceptedPlusRejected)
	}
	if r.LostCount != 0 {
		violations = append(violations, InvariantZeroLost)
	}
	if r.UnauthorizedAcceptCount != 0 {
		violations = append(violations, InvariantZeroUnauthorized)
	}
	if r.HashMismatchCount != 0 {
		violations = append(violations, InvariantZeroHashMismatch)
	}
	return violations
}

// Pass reports whether every acceptance invariant holds.
func (r Report) Pass() bool { return len(r.Verify()) == 0 }
