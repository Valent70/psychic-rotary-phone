// Package provenance is the rights-and-provenance registry's chain-of-
// custody ledger: a Provider identifying one data source, and a Chain
// recording, in order and tamper-evidently, every hop a piece of
// evidence passed through -- who produced it, who transformed it, who
// validated it.
//
// Chain deliberately reuses this repo's existing hash-chained
// append-only ledger pattern (Index/PrevHash/Hash fields, a
// canonical-string hashRecord function, a VerifyChain method that
// re-derives every hash) rather than inventing a new chaining scheme --
// see pkg/moat/fusion.FusionRecord/Engine.VerifyChain for the pattern
// this follows.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// Provider identifies one data source/provider in the rights &
// provenance registry.
type Provider struct {
	ProviderID        string
	Name              string
	ContractReference string
}

// Hop is one hash-chained entry in a Chain: one thing that happened to a
// piece of evidence, by whom, in order.
type Hop struct {
	Index       uint64
	ProviderID  string
	Action      string // e.g. "PRODUCED", "TRANSFORMED", "VALIDATED", "RECEIVED"
	ContentHash string // hash of the evidence/content as of this hop
	Tick        uint64
	PrevHash    string
	Hash        string
}

// ErrTamperDetected mirrors fusion.ErrTamperDetected and
// kg.Graph.VerifyChain's own invariant: a chain whose stored hash
// doesn't match what re-hashing its fields produces, or whose PrevHash
// linkage is broken, has been tampered with.
var ErrTamperDetected = errors.New("provenance: hash chain verification failed")

// Chain is one append-only, hash-chained provenance record for a single
// piece of evidence -- e.g. one manifest, one record, one document --
// tracking every hop (producer, transformer, validator) it passed
// through, in the order it passed through them. Safe for concurrent use.
type Chain struct {
	mu      sync.RWMutex
	Subject string // what this chain traces the provenance of
	hops    []Hop
	head    string
}

// NewChain starts an empty provenance chain for subject.
func NewChain(subject string) *Chain {
	return &Chain{Subject: subject}
}

// Append records one more hop, hash-linked to the previous hop (or ""
// for the first hop in the chain), and returns the recorded Hop
// (including its computed Hash).
func (c *Chain) Append(providerID, action, contentHash string, tick uint64) Hop {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := Hop{
		Index: uint64(len(c.hops)), ProviderID: providerID, Action: action,
		ContentHash: contentHash, Tick: tick, PrevHash: c.head,
	}
	h.Hash = hashHop(h)
	c.hops = append(c.hops, h)
	c.head = h.Hash
	return h
}

// Hops returns a copy of every hop recorded so far, in order. Callers
// mutating the returned slice cannot corrupt the chain's internal state.
func (c *Chain) Hops() []Hop {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Hop, len(c.hops))
	copy(out, c.hops)
	return out
}

// Head returns the hash of the most recently appended hop, or "" for an
// empty chain.
func (c *Chain) Head() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.head
}

// hashHop computes the canonical, deterministic hash of a Hop chained
// to the previous hop's hash -- the same shape as fusion.hashRecord.
func hashHop(h Hop) string {
	raw := fmt.Sprintf("idx=%d|provider=%s|action=%s|content=%s|tick=%d|prev=%s",
		h.Index, h.ProviderID, h.Action, h.ContentHash, h.Tick, h.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerifyChain re-derives every hop's hash from its stored fields and
// confirms the chain is unbroken -- the same tamper-detection contract
// as fusion.Engine.VerifyChain and kg.Graph.VerifyChain.
func (c *Chain) VerifyChain() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	prev := ""
	for _, h := range c.hops {
		if h.PrevHash != prev {
			return ErrTamperDetected
		}
		want := hashHop(Hop{
			Index: h.Index, ProviderID: h.ProviderID, Action: h.Action,
			ContentHash: h.ContentHash, Tick: h.Tick, PrevHash: h.PrevHash,
		})
		if want != h.Hash {
			return ErrTamperDetected
		}
		prev = h.Hash
	}
	return nil
}
