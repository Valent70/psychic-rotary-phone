// Package ledger closes WAVE A item 5 of the canonical-truth-path
// mandate: "Saat ini fusion hash chain = in-memory. Itu belum cukup."
//
// WHAT ALREADY EXISTED, AND IS NOT REBUILT HERE. pkg/storage/wal is a
// real, complete, tested write-ahead log: CRC-checked, hash-chained
// records; a fsync policy; segment rotation, compaction, retention and
// legal holds; and a recovery classifier that tells a corrupt TAIL (a
// crash mid-append, truncate it) apart from a corrupt MIDDLE (bit rot
// or tampering, fail closed). None of that is reimplemented. The
// mandate's actual finding was narrower and sharper: that WAL's ONLY
// importer anywhere in the tree was cmd/veriqo-readiness, so nothing on
// the live execution path ever wrote a byte to disk.
//
// This package is the missing seam, and nothing more: a typed event
// vocabulary, a canonical encoding, and the append/reconstruct/verify
// operations pkg/lifecycle needs — over pkg/storage/wal, unchanged.
//
// WHAT IT DOES NOT CLAIM. A durable ledger is not an external trust
// anchor. Everything here is written, read and verified by VERIQO
// itself on storage VERIQO controls. An operator with write access to
// the directory and the ability to recompute hashes can rewrite the
// whole chain consistently; what they cannot do is alter a record
// without moving Head(), which every certificate and replay package in
// this repository commits to. Making VERIQO not be the only party that
// can vouch for this ledger is the mandate's own section VIII (external
// trust anchor: KMS-signed checkpoint / RFC 3161 TSA / independent
// witness / transparency log), which is genuinely external and remains
// open. Head() is the natural hook a real anchor would sign; see
// docs/governance/CANONICAL_TRUTH_PATH_RESIDUAL_REGISTER.md.
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/storage/wal"
)

// EventKind is the durable event vocabulary. It is exactly the minimum
// set the mandate's section VI enumerates, no more: an event kind with
// no real producer would be a placeholder, and this package's whole
// point is that what is on disk is what actually happened.
type EventKind string

const (
	// EventEvidence: one source submission entered a case.
	EventEvidence EventKind = "EVIDENCE"
	// EventSourceProvenance: the provenance/rights posture a submission
	// arrived under.
	EventSourceProvenance EventKind = "SOURCE_PROVENANCE"
	// EventIdentity: the identity-ledger state a case's entity was
	// resolved against.
	EventIdentity EventKind = "IDENTITY"
	// EventTrust: the trust evaluation that gated the case's evidence.
	EventTrust EventKind = "TRUST"
	// EventFusion: the arbitration outcome.
	EventFusion EventKind = "FUSION"
	// EventContradiction: the contradiction record for the claim.
	EventContradiction EventKind = "CONTRADICTION"
	// EventDecision: the native decision engine's action and the
	// execution root that produced it.
	EventDecision EventKind = "DECISION"
	// EventReviewer: a human-review requirement raised, or a reviewer's
	// disposition recorded.
	EventReviewer EventKind = "REVIEWER"
	// EventRelease: a decision released for use (or withheld).
	EventRelease EventKind = "RELEASE"
)

// KnownKinds is every modelled event kind, in a fixed order. An
// unrecognised kind is rejected by Append rather than written, so a
// future binary's vocabulary cannot silently land in an old ledger.
func KnownKinds() []EventKind {
	return []EventKind{EventEvidence, EventSourceProvenance, EventIdentity,
		EventTrust, EventFusion, EventContradiction, EventDecision,
		EventReviewer, EventRelease}
}

var knownKinds = func() map[EventKind]bool {
	m := make(map[EventKind]bool)
	for _, k := range KnownKinds() {
		m[k] = true
	}
	return m
}()

var (
	// ErrUnknownKind refuses an event whose kind this binary does not
	// model — fail-closed, never "write it anyway and hope".
	ErrUnknownKind = errors.New("ledger: unknown event kind")
	// ErrMissingField refuses an event that cannot be joined back to the
	// case it belongs to.
	ErrMissingField = errors.New("ledger: required event field missing")
	// ErrCorrupt is returned when a record on disk does not decode, or
	// when the reconstructed chain does not verify.
	ErrCorrupt = errors.New("ledger: durable record failed verification")
)

// Event is one durable record. Every field is part of Hash, so a record
// cannot be edited on disk without detection — and pkg/storage/wal
// independently chains the encoded bytes, so a record cannot be
// REMOVED or REORDERED without detection either.
//
// Ticks, not wall-clock timestamps: this repository's standing
// no-wall-clock discipline (see pkg/evidence/provenance's own note on
// why an ingest adapter is the single place real time enters).
type Event struct {
	Kind EventKind `json:"kind"`
	// CaseID, ExecutionID and CorrelationID are the joins. CaseID is
	// mandatory: an event that cannot be attributed to a case is not an
	// audit record, it is noise.
	CaseID        string `json:"case_id"`
	ExecutionID   string `json:"execution_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Tenant        string `json:"tenant,omitempty"`
	Actor         string `json:"actor,omitempty"`
	// Subject is what this event is ABOUT (a source ID, an entity, a
	// trust subject), and Ref is the real identifier or hash it commits
	// to. Ref is mandatory for the same reason CaseID is: an event with
	// no reference commits to nothing.
	Subject string `json:"subject,omitempty"`
	Ref     string `json:"ref"`
	// Detail is human-readable context. It participates in Hash, so it
	// cannot be edited after the fact any more than Ref can.
	Detail        string `json:"detail,omitempty"`
	PolicyVersion string `json:"policy_version,omitempty"`
	Tick          uint64 `json:"tick"`
	// Payload is optional, kind-specific structured state that a
	// RECOVERING process genuinely needs and cannot re-derive from Ref
	// alone. Today exactly one kind uses it: EventTrust carries the full
	// trust transition ledger, because "reconstruct the ledger and
	// continue execution" (the mandate's section VII, step 8-10) is not
	// satisfiable if the durable record holds only a trust ROOT HASH — a
	// fresh process could verify that hash against nothing, having lost
	// the transitions the running process held in memory.
	//
	// It is deliberately not a general-purpose blob: an event that puts
	// state here is asserting that the state is required for recovery,
	// and every byte of it participates in Hash exactly like Ref does, so
	// it is as tamper-evident as any other field.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Hash is this event's own content commitment, independent of the
	// WAL record hash that chains it.
	Hash string `json:"hash"`
}

func (e Event) validate() error {
	if !knownKinds[e.Kind] {
		return fmt.Errorf("%w: %q", ErrUnknownKind, e.Kind)
	}
	if strings.TrimSpace(e.CaseID) == "" {
		return fmt.Errorf("%w: case_id", ErrMissingField)
	}
	if strings.TrimSpace(e.Ref) == "" {
		return fmt.Errorf("%w: ref (kind=%s)", ErrMissingField, e.Kind)
	}
	return nil
}

// ComputeHash is the event's content commitment. Hand-rolled and
// key-ordered rather than hashing the JSON, for the same cross-language
// reproducibility reason pkg/evidence/provenance.ExternalEvidence.ID
// and pkg/execution.Context.hash both document.
func (e Event) ComputeHash() string {
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.ledger_event/v1\nkind=%s\ncase_id=%s\nexecution_id=%s\n",
		e.Kind, e.CaseID, e.ExecutionID)
	fmt.Fprintf(&b, "correlation_id=%s\ntenant=%s\nactor=%s\n",
		e.CorrelationID, e.Tenant, e.Actor)
	fmt.Fprintf(&b, "subject=%s\nref=%s\ndetail=%s\npolicy_version=%s\ntick=%d\n",
		e.Subject, e.Ref, e.Detail, e.PolicyVersion, e.Tick)
	// Payload is hashed by its bytes, and hashed unconditionally (an
	// absent payload contributes the empty string), so "no payload" and
	// "a payload of zero bytes" cannot collide with each other or with a
	// payload that was stripped after the fact.
	fmt.Fprintf(&b, "payload_len=%d\npayload=", len(e.Payload))
	b.Write(e.Payload)
	b.WriteString("\n")
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Config parameterises the durable ledger. Every field maps straight
// onto pkg/storage/wal.Config — this package adds no storage knobs of
// its own.
type Config struct {
	// Dir is the directory the write-ahead log lives in.
	Dir string
	// Sync is the fsync policy. Defaults to wal.SyncAlways, the only
	// policy under which a returned Append means "durable", which is the
	// correct default for evidence.
	Sync wal.SyncPolicy
	// SyncEvery is used only by wal.SyncInterval.
	SyncEvery int
	// SegmentBytes triggers segment rotation.
	SegmentBytes int64
}

// Ledger is the durable event ledger.
type Ledger struct {
	mu  sync.Mutex
	log *wal.Log
}

// Open opens (creating if needed) a durable ledger, running WAL
// recovery first and returning its report — so a caller can see, and
// must decide what to do about, a crash that truncated the tail.
//
// Durable is set unconditionally: this log holds evidence, which makes
// wal.SyncNever illegal and makes retention refuse to delete
// uncheckpointed segments. Those are the WAL's own guarantees, invoked
// here rather than re-implemented.
func Open(cfg Config) (*Ledger, *wal.RecoveryReport, error) {
	sync := cfg.Sync
	if sync == "" {
		sync = wal.SyncAlways
	}
	log, rep, err := wal.Open(wal.Config{
		Dir: cfg.Dir, Sync: sync, SyncEvery: cfg.SyncEvery,
		SegmentBytes: cfg.SegmentBytes, Durable: true,
	})
	if err != nil {
		return nil, rep, err
	}
	return &Ledger{log: log}, rep, nil
}

// Append writes one event durably and commits it. Under the default
// SyncAlways policy, Append returning nil means the bytes are on the
// device: a process killed immediately afterwards still finds the
// record on restart, which is what WAVE A item 6's crash test proves.
func (l *Ledger) Append(ev Event) (Event, error) {
	if err := ev.validate(); err != nil {
		return Event{}, err
	}
	ev.Hash = ev.ComputeHash()
	payload, err := json.Marshal(ev)
	if err != nil {
		return Event{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, err := l.log.Append(ev.Tick, payload)
	if err != nil {
		return Event{}, err
	}
	// Commit immediately: an evidence ledger has no notion of a
	// speculative append that might be rolled back. Every event this
	// package writes is a statement about something that already
	// happened.
	if err := l.log.Commit(rec.Seq); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// AppendAll writes a whole event set in order, stopping at the first
// failure. It returns the ledger head AFTER the batch, which is the
// value a certificate commits to.
//
// It is deliberately not atomic across the batch: the WAL's own
// recovery classifier already handles a partial batch correctly (the
// unwritten tail simply is not there, and Events() reconstructs what
// genuinely reached disk). Inventing a transaction here would mean
// buffering evidence in memory until the batch completed, which is the
// exact durability gap this package exists to close.
func (l *Ledger) AppendAll(evs []Event) (head string, seq uint64, err error) {
	for i, ev := range evs {
		if _, err := l.Append(ev); err != nil {
			h, s := l.Head()
			return h, s, fmt.Errorf("ledger: appending event %d of %d (kind=%s): %w",
				i+1, len(evs), ev.Kind, err)
		}
	}
	h, s := l.Head()
	return h, s, nil
}

// EmptyHead is the head of a durable ledger that EXISTS but holds no
// records yet. It is a named sentinel rather than the empty string
// because those two states are materially different and a certificate
// must not conflate them: "" means no durable ledger was attached to
// this deployment at all, EmptyHead means one was attached and this is
// the first thing it ever recorded. The same distinction, for the same
// reason, as pkg/canonical.TrustLedgerHead's own empty-ledger sentinel.
const EmptyHead = "veriqo.ledger/v1:empty"

// Head returns the durable ledger head: the hash of the last WAL record
// and its sequence number, or EmptyHead when nothing has been written
// yet. This is the single value every certificate, replay package and
// (eventually) external anchor commits to.
func (l *Ledger) Head() (string, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, seq := l.log.Head()
	if h == "" {
		return EmptyHead, seq
	}
	return h, seq
}

// Events reconstructs every event from the durable log, in order,
// verifying each one's own content hash as it goes. It is what a fresh
// process calls after a crash to rebuild state it no longer has in
// memory (WAVE A item 6, step 8: "reconstruct ledger").
//
// A record whose stored Hash does not match its own recomputed content
// fails ErrCorrupt rather than being returned: this package will not
// hand a caller an event it cannot vouch for. That is the same
// fail-closed posture pkg/storage/wal takes on a corrupt middle, one
// layer up.
func (l *Ledger) Events() ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recs, err := l.log.Read()
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(recs))
	for _, r := range recs {
		var ev Event
		if err := json.Unmarshal(r.Payload, &ev); err != nil {
			return nil, fmt.Errorf("%w: record seq %d does not decode: %v", ErrCorrupt, r.Seq, err)
		}
		if want := ev.ComputeHash(); want != ev.Hash {
			return nil, fmt.Errorf("%w: record seq %d carries hash %s but its content hashes to %s",
				ErrCorrupt, r.Seq, ev.Hash, want)
		}
		out = append(out, ev)
	}
	return out, nil
}

// EventsForCase returns every event belonging to one case, in ledger
// order — the durable counterpart of pkg/lineage's in-memory case view.
func (l *Ledger) EventsForCase(caseID string) ([]Event, error) {
	all, err := l.Events()
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, ev := range all {
		if ev.CaseID == caseID {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Verify re-reads the whole ledger and re-derives its head from the
// records on disk, independently of the in-memory head the running
// process is carrying. A mismatch means the file was altered underneath
// a live process.
func (l *Ledger) Verify() error {
	if _, err := l.Events(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	recs, err := l.log.Read()
	if err != nil {
		return err
	}
	head, seq := l.log.Head()
	if len(recs) == 0 {
		if head != "" || seq != 0 { //nolint:staticcheck // l.log.Head() (the WAL's own head), not Ledger.Head() -- an empty WAL genuinely reports "", and EmptyHead is this package's presentation of that, not the WAL's value
			return fmt.Errorf("%w: empty log reports head=%q seq=%d", ErrCorrupt, head, seq)
		}
		return nil
	}
	last := recs[len(recs)-1]
	if last.Hash != head || last.Seq != seq {
		return fmt.Errorf("%w: on-disk tail is seq=%d hash=%s but the log reports seq=%d hash=%s",
			ErrCorrupt, last.Seq, last.Hash, seq, head)
	}
	return nil
}

// Close syncs and closes the underlying log.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.log.Close()
}

// CoverageReport is what KindCoverage returns: which of the mandate's
// nine required event kinds a given ledger (or case) actually contains.
// It exists so "the minimum event set is written" is a checkable fact
// about real bytes on disk, not a claim in a document.
type CoverageReport struct {
	Present []EventKind `json:"present"`
	Missing []EventKind `json:"missing"`
}

// Complete reports whether every modelled kind appeared.
func (c CoverageReport) Complete() bool { return len(c.Missing) == 0 }

// KindCoverage classifies a set of events against KnownKinds.
func KindCoverage(events []Event) CoverageReport {
	seen := map[EventKind]bool{}
	for _, ev := range events {
		seen[ev.Kind] = true
	}
	var rep CoverageReport
	for _, k := range KnownKinds() {
		if seen[k] {
			rep.Present = append(rep.Present, k)
		} else {
			rep.Missing = append(rep.Missing, k)
		}
	}
	sort.Slice(rep.Present, func(i, j int) bool { return rep.Present[i] < rep.Present[j] })
	sort.Slice(rep.Missing, func(i, j int) bool { return rep.Missing[i] < rep.Missing[j] })
	return rep
}
