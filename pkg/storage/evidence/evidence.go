// Package evidence implements an append-only, Merkle-chained evidence store.
// Every record is SHA-256 hashed and linked to the previous record's hash,
// making any tampering detectable. This addresses the audit finding that the
// earlier evidence store may have been "just a slice/map" — this is not.
//
// VEP-006: Evidence Store.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"veriqo/pkg/core"
)

// Record is a single immutable evidence entry.
type Record struct {
	Seq         uint64       // monotone sequence within this store
	TraceID     core.TraceID // links back to the execution that produced it
	TenantID    core.TenantID
	DomainID    core.DomainID
	Kind        string   // e.g. "risk.assessment", "compliance.check"
	Payload     []byte   // canonical JSON or protobuf
	PayloadHash [32]byte // SHA-256(Payload)
	PrevHash    [32]byte // SHA-256 of the previous record (chain link)
	Hash        [32]byte // SHA-256(Seq ‖ TraceID ‖ PayloadHash ‖ PrevHash)
	Timestamp   time.Time
	Metadata    map[string]string
}

// Verify confirms that the record's Hash is consistent with its fields.
func (r *Record) Verify() error {
	wantPayload := sha256.Sum256(r.Payload)
	if wantPayload != r.PayloadHash {
		return fmt.Errorf("evidence: record %d: payload hash mismatch", r.Seq)
	}
	want := computeHash(r.Seq, r.TraceID, r.PayloadHash, r.PrevHash)
	if want != r.Hash {
		return fmt.Errorf("evidence: record %d: record hash mismatch", r.Seq)
	}
	return nil
}

// Store is an append-only, Merkle-chained evidence log.
// It is safe for concurrent use.
type Store struct {
	mu       sync.RWMutex
	id       string
	records  []*Record
	lastHash [32]byte
	seq      uint64
}

// NewStore creates a new empty evidence store with the given ID.
func NewStore(id string) *Store {
	return &Store{id: id}
}

// ID returns the store identifier.
func (s *Store) ID() string { return s.id }

// Append adds a new evidence record.
// The record is immutably chained to all previous records.
func (s *Store) Append(
	traceID core.TraceID,
	tenantID core.TenantID,
	domainID core.DomainID,
	kind string,
	payload []byte,
	metadata map[string]string,
) (*Record, error) {
	if kind == "" {
		return nil, errors.New("evidence: kind must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	seq := s.seq + 1
	payloadHash := sha256.Sum256(payload)
	hash := computeHash(seq, traceID, payloadHash, s.lastHash)

	rec := &Record{
		Seq:         seq,
		TraceID:     traceID,
		TenantID:    tenantID,
		DomainID:    domainID,
		Kind:        kind,
		Payload:     append([]byte(nil), payload...),
		PayloadHash: payloadHash,
		PrevHash:    s.lastHash,
		Hash:        hash,
		Timestamp:   time.Now().UTC(),
		Metadata:    copyMeta(metadata),
	}

	s.records = append(s.records, rec)
	s.lastHash = hash
	s.seq = seq
	return rec, nil
}

// Get returns the record at position seq (1-based).
func (s *Store) Get(seq uint64) (*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if seq == 0 || seq > uint64(len(s.records)) {
		return nil, &ErrNotFound{Seq: seq}
	}
	return s.records[seq-1], nil
}

// Len returns the number of records in the store.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// LastHash returns the Merkle chain tip hash.
func (s *Store) LastHash() [32]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastHash
}

// Query returns all records matching the given predicate.
func (s *Store) Query(pred func(*Record) bool) []*Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Record
	for _, r := range s.records {
		if pred(r) {
			out = append(out, r)
		}
	}
	return out
}

// Verify walks the entire chain and checks every hash.
// Returns the first error found, or nil if the chain is intact.
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var prev [32]byte
	for _, r := range s.records {
		if r.PrevHash != prev {
			return fmt.Errorf("evidence: broken chain at seq %d: prevHash mismatch", r.Seq)
		}
		if err := r.Verify(); err != nil {
			return err
		}
		prev = r.Hash
	}
	return nil
}

// AuditReport returns a summary of the chain state suitable for external audits.
type AuditReport struct {
	StoreID      string
	TotalRecords int
	ChainTip     string // hex-encoded last hash
	Intact       bool
	Error        string
}

// Audit verifies the chain and returns an AuditReport.
func (s *Store) Audit() AuditReport {
	err := s.Verify()
	lastHash := s.LastHash()
	rep := AuditReport{
		StoreID:      s.id,
		TotalRecords: s.Len(),
		ChainTip:     hex.EncodeToString(lastHash[:]),
		Intact:       err == nil,
	}
	if err != nil {
		rep.Error = err.Error()
	}
	return rep
}

// ─── hash computation ─────────────────────────────────────────────────────────

// computeHash produces SHA-256(seq ‖ traceID ‖ payloadHash ‖ prevHash).
func computeHash(seq uint64, traceID core.TraceID, payloadHash, prevHash [32]byte) [32]byte {
	h := sha256.New()
	var buf [8]byte
	buf[0] = byte(seq >> 56)
	buf[1] = byte(seq >> 48)
	buf[2] = byte(seq >> 40)
	buf[3] = byte(seq >> 32)
	buf[4] = byte(seq >> 24)
	buf[5] = byte(seq >> 16)
	buf[6] = byte(seq >> 8)
	buf[7] = byte(seq)
	h.Write(buf[:])
	h.Write(traceID[:])
	h.Write(payloadHash[:])
	h.Write(prevHash[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func copyMeta(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// ─── Errors ───────────────────────────────────────────────────────────────────

// ErrNotFound is returned when a record sequence number is out of range.
type ErrNotFound struct{ Seq uint64 }

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("evidence: record seq=%d not found", e.Seq)
}
