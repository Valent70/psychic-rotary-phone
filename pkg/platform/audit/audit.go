// Package audit implements the investor/auditor trail contract: a strictly
// ordered, append-only, hash-chained record store.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrOutOfOrder     = errors.New("audit: index already written or out of order")
	ErrTamperDetected = errors.New("audit: chain verification failed")
)

// AuditRecord is one append-only entry.
type AuditRecord struct {
	Index    uint64
	Actor    string
	Action   string
	Payload  string
	PrevHash string
	Hash     string
}

// AuditStore is a strictly ordered, append-only, hash-chained log.
type AuditStore struct {
	mu      sync.Mutex
	records []AuditRecord
	head    string
}

func NewAuditStore() *AuditStore {
	return &AuditStore{}
}

func hashRecord(index uint64, actor, action, payload, prev string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("idx=%d|actor=%s|action=%s|payload=%s|prev=%s", index, actor, action, payload, prev)))
	return hex.EncodeToString(sum[:])
}

// Append is strictly ordered: it always appends at the next index and can
// never be told to write "the same index twice" because there's no
// caller-supplied index — the store itself assigns it atomically under
// lock. Under 50-way concurrent contention, exactly one goroutine's record
// lands at each index; this is proven by TestConcurrentAppend_Serialized.
func (s *AuditStore) Append(actor, action, payload string) (AuditRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := uint64(len(s.records)) + 1
	h := hashRecord(index, actor, action, payload, s.head)
	rec := AuditRecord{Index: index, Actor: actor, Action: action, Payload: payload, PrevHash: s.head, Hash: h}
	s.records = append(s.records, rec)
	s.head = h
	return rec, nil
}

func (s *AuditStore) Snapshot() []AuditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditRecord, len(s.records))
	copy(out, s.records)
	return out
}

func (s *AuditStore) RootHash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.head
}

// Auditor independently re-derives every hash rather than trusting stored
// values — this is what makes the trail verifiable by a third party (an
// investor's own auditor), not just self-consistent.
type Auditor struct{}

func (Auditor) VerifyChain(records []AuditRecord) error {
	prev := ""
	for i, r := range records {
		wantIdx := uint64(i) + 1
		if r.Index != wantIdx {
			return fmt.Errorf("%w: index gap at position %d", ErrTamperDetected, i)
		}
		if r.PrevHash != prev {
			return fmt.Errorf("%w: broken linkage at index %d", ErrTamperDetected, r.Index)
		}
		recomputed := hashRecord(r.Index, r.Actor, r.Action, r.Payload, r.PrevHash)
		if recomputed != r.Hash {
			return fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, r.Index)
		}
		prev = recomputed
	}
	return nil
}

func (a Auditor) VerifySnapshot(records []AuditRecord, expectedRootHash string) error {
	if err := a.VerifyChain(records); err != nil {
		return err
	}
	if len(records) == 0 {
		if expectedRootHash != "" {
			return fmt.Errorf("%w: expected empty root hash for empty log", ErrTamperDetected)
		}
		return nil
	}
	last := records[len(records)-1]
	if last.Hash != expectedRootHash {
		return fmt.Errorf("%w: root hash mismatch", ErrTamperDetected)
	}
	return nil
}
