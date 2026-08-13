// Package timestamp implements a minimal, self-hosted timestamping
// authority (TSA) for release certificates -- audit item P1-09.
//
// A ReleaseCertificate's Timestamp field is a bare wall-clock uint64
// from the same build host that produces the certificate: nothing
// outside that process attests to it, so an operator controlling the
// clock and the release-signing key can claim any "now" they like.
// A genuine external RFC 3161 Time-Stamp Authority would close that
// gap completely, but querying one requires outbound network access
// this sandbox does not have (the same network restriction already
// documented against supply_chain_scan's vulnerability-DB lookups).
//
// What this package provides instead is the piece that does NOT
// require network access: a hash-chained, Ed25519-signed notary log,
// the same technique real Certificate Transparency logs use. Once an
// Entry is issued and appended to the persisted chain, it cannot be
// silently reordered, backdated, or dropped without breaking the hash
// chain -- and the chain is independently verifiable by anyone holding
// the authority's public key. The authority's signing key is
// deliberately separate from the release-signing key, so a compromise
// of one does not let an attacker also rewrite timestamp history.
//
// This closes the "wall clock, zero protection" gap; it does not
// close "trusted by a party outside this operator's control", which
// remains the honestly-open part of P1-09 until real external TSA
// access is available.
package timestamp

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

var (
	// ErrNotMonotonic is returned when a new entry's timestamp would
	// precede the chain's most recently issued timestamp.
	ErrNotMonotonic = errors.New("timestamp: new entry precedes the chain's last issued timestamp")
	// ErrChainBroken is returned by VerifyChain when an entry's
	// PrevHash does not match the hash of the entry before it.
	ErrChainBroken = errors.New("timestamp: entry's prev_hash does not match the preceding entry")
	// ErrHashMismatch is returned when an entry's own Hash does not
	// match its recomputed content hash -- the entry was edited after
	// issuance without re-signing.
	ErrHashMismatch = errors.New("timestamp: entry hash does not match its content")
	// ErrSignatureInvalid is returned when an entry's signature does
	// not verify against the supplied authority public key.
	ErrSignatureInvalid = errors.New("timestamp: signature does not verify against the authority key")
)

// Entry is one issued timestamp, chained to the entry issued before it.
type Entry struct {
	Seq       uint64 `json:"seq"`
	Subject   string `json:"subject"`   // what was timestamped, e.g. a ReleaseCertificate's CertificateHash
	Timestamp uint64 `json:"timestamp"` // unix seconds, from the authority's own clock
	PrevHash  string `json:"prev_hash"`
	Hash      string `json:"hash"`
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

func entryHash(seq uint64, subject string, ts uint64, prevHash string) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.timestamp_entry/v1\nseq=%d\nsubject=%s\nts=%d\nprev=%s\n", seq, subject, ts, prevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// Authority issues chained, signed timestamp Entries. Its zero value is
// a fresh chain; use Resume to continue an existing persisted chain.
type Authority struct {
	priv     ed25519.PrivateKey
	keyID    string
	seq      uint64
	lastHash string
	lastTS   uint64
}

// NewAuthority starts a fresh chain signed by priv.
func NewAuthority(priv ed25519.PrivateKey, keyID string) *Authority {
	return &Authority{priv: priv, keyID: keyID}
}

// Resume continues an existing chain: the next Issue chains from and
// must not precede tail, tail's own timestamp. Passing the zero Entry
// is equivalent to NewAuthority.
func Resume(priv ed25519.PrivateKey, keyID string, tail Entry) *Authority {
	return &Authority{priv: priv, keyID: keyID, seq: tail.Seq, lastHash: tail.Hash, lastTS: tail.Timestamp}
}

// Issue timestamps subject at now, chaining onto and signing over the
// authority's current head. now must not precede the last-issued
// timestamp -- an authority cannot be asked to backdate.
func (a *Authority) Issue(subject string, now uint64) (Entry, error) {
	if now < a.lastTS {
		return Entry{}, fmt.Errorf("%w: %d < %d", ErrNotMonotonic, now, a.lastTS)
	}
	seq := a.seq + 1
	hash := entryHash(seq, subject, now, a.lastHash)
	sig := ed25519.Sign(a.priv, []byte(hash))
	e := Entry{Seq: seq, Subject: subject, Timestamp: now, PrevHash: a.lastHash,
		Hash: hash, KeyID: a.keyID, Signature: hex.EncodeToString(sig)}
	a.seq, a.lastHash, a.lastTS = seq, hash, now
	return e, nil
}

// VerifyChain checks hash linkage, monotonic timestamps, content
// integrity and signature for every entry in order, against a single
// trusted authority public key. A chain that was reordered, had an
// entry removed, was backdated, or was re-signed by any other key
// fails here.
func VerifyChain(entries []Entry, pub ed25519.PublicKey) error {
	var prevHash string
	var prevTS uint64
	for i, e := range entries {
		if e.PrevHash != prevHash {
			return fmt.Errorf("%w at seq %d", ErrChainBroken, e.Seq)
		}
		if i > 0 && e.Timestamp < prevTS {
			return fmt.Errorf("%w at seq %d", ErrNotMonotonic, e.Seq)
		}
		if entryHash(e.Seq, e.Subject, e.Timestamp, e.PrevHash) != e.Hash {
			return fmt.Errorf("%w at seq %d", ErrHashMismatch, e.Seq)
		}
		sig, err := hex.DecodeString(e.Signature)
		if err != nil || !ed25519.Verify(pub, []byte(e.Hash), sig) {
			return fmt.Errorf("%w at seq %d", ErrSignatureInvalid, e.Seq)
		}
		prevHash, prevTS = e.Hash, e.Timestamp
	}
	return nil
}
