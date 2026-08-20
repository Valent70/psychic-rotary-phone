package dr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrCheckpointTampered mirrors fusion.ErrTamperDetected /
// kg.Graph.VerifyChain's tamper-detection contract: a checkpoint chain
// whose re-derived hash does not match a stored Hash, or whose PrevHash
// linkage is broken, is reported this way rather than as a generic
// error, so callers can distinguish "tampered" from "I/O failure" the
// same way every other hash-chained audit log in this codebase does.
var ErrCheckpointTampered = errors.New("dr: checkpoint chain hash verification failed (tamper detected)")

// contentHash computes a deterministic SHA-256 hash of the FSM's
// current key/value contents, sorted by key first so the hash does not
// depend on Go's randomized map iteration order -- the same
// "content-addressed, order-independent" discipline as
// reconciliation.computeFinalRoot and pkg/moat/evidencegraph.Graph.RootHash.
func (f *kvFSM) contentHash() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	keys := make([]string, 0, len(f.data))
	for k := range f.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	h.Write([]byte("veriqo.dr.kvfsm_state/v1\n"))
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{'='})
		h.Write([]byte(f.data[k]))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Checkpoint is one hash-chained, point-in-time snapshot of a single
// region's replicated state -- the DR harness's explicit Checkpoint
// component named by the audit (previously implicit in this package: a
// bare before/after RPO value comparison, with no verifiable record of
// what state a region actually held at each named point in the
// scenario). Sequence is a monotonic per-region counter, never a
// wall-clock timestamp, per this repository's determinism discipline
// (no time.Now() on any deterministic accounting path -- see dr.go's
// own package doc and Write's doc comment for why that discipline
// matters here specifically). Hash/PrevHash follow the exact
// chained-hash shape used throughout this repository (see
// pkg/moat/fusion.FusionRecord and Engine.VerifyChain, pkg/moat/kg
// .Mutation) so this chain is verified the same way every other
// hash-chained audit log in this codebase is verified.
type Checkpoint struct {
	RegionID  string
	Sequence  uint64
	StateHash string // kvFSM.contentHash() at the moment this checkpoint was taken
	Label     string // e.g. "baseline", "after-failover-write", "post-heal-convergence"
	PrevHash  string
	Hash      string
}

// hashCheckpoint computes the canonical, deterministic hash of a
// Checkpoint chained to the previous checkpoint's hash -- mirrors
// fusion.hashRecord exactly (same field-concatenation shape, same
// SHA-256 domain-prefixed-by-field-list approach).
func hashCheckpoint(c Checkpoint) string {
	raw := fmt.Sprintf("region=%s|seq=%d|state=%s|label=%s|prev=%s",
		c.RegionID, c.Sequence, c.StateHash, c.Label, c.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CheckpointChain is one region's append-only, hash-chained checkpoint
// log. Safe for concurrent use.
type CheckpointChain struct {
	mu       sync.RWMutex
	regionID string
	entries  []Checkpoint
	head     string
}

func newCheckpointChain(regionID string) *CheckpointChain {
	return &CheckpointChain{regionID: regionID}
}

// Append takes a new checkpoint at the next sequence number, chained to
// this region's current head, and returns the recorded entry.
func (c *CheckpointChain) Append(stateHash, label string) Checkpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := Checkpoint{
		RegionID:  c.regionID,
		Sequence:  uint64(len(c.entries)),
		StateHash: stateHash,
		Label:     label,
		PrevHash:  c.head,
	}
	cp.Hash = hashCheckpoint(cp)
	c.entries = append(c.entries, cp)
	c.head = cp.Hash
	return cp
}

// Len reports how many checkpoints have been taken for this region.
func (c *CheckpointChain) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Entries returns a defensive copy of every checkpoint taken so far, in
// order.
func (c *CheckpointChain) Entries() []Checkpoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Checkpoint, len(c.entries))
	copy(out, c.entries)
	return out
}

// VerifyChain re-derives every checkpoint's hash from its stored fields
// and confirms the PrevHash linkage is unbroken -- the same
// tamper-detection contract as fusion.Engine.VerifyChain and
// kg.Graph.VerifyChain.
func (c *CheckpointChain) VerifyChain() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	prev := ""
	for _, cp := range c.entries {
		if cp.PrevHash != prev {
			return ErrCheckpointTampered
		}
		want := hashCheckpoint(Checkpoint{
			RegionID: cp.RegionID, Sequence: cp.Sequence, StateHash: cp.StateHash,
			Label: cp.Label, PrevHash: cp.PrevHash,
		})
		if want != cp.Hash {
			return ErrCheckpointTampered
		}
		prev = cp.Hash
	}
	return nil
}

// Checkpointer is implemented by any RegionProvider that can also take
// hash-chained state checkpoints. LocalRegionProvider is the only
// implementation today. RunQualification checkpoints on a best-effort
// basis via a type assertion: a provider that does not implement
// Checkpointer still runs the full DR scenario, just without checkpoint
// measurements -- kept as an optional, additive interface rather than a
// new required method on RegionProvider, per this package's "adapters
// interpret into or out of the native engine, they never replace it"
// discipline (a REAL-mode provider is refused before this matters
// anyway -- see RunQualification's first check).
type Checkpointer interface {
	// CheckpointAll takes a point-in-time Checkpoint of every region's
	// current state, each appended to that region's own hash chain, and
	// returns the full set taken. label documents which point in the DR
	// scenario this call captures.
	CheckpointAll(label string) []Checkpoint
	// CheckpointChains returns every region's own checkpoint chain, for
	// independent verification.
	CheckpointChains() map[string]*CheckpointChain
}

// CheckpointAll implements Checkpointer for LocalRegionProvider.
func (p *LocalRegionProvider) CheckpointAll(label string) []Checkpoint {
	p.mu.Lock()
	ids := append([]string(nil), p.ids...)
	fsms := p.fsms
	chains := p.chains
	p.mu.Unlock()

	cps := make([]Checkpoint, 0, len(ids))
	for _, id := range ids {
		hash := fsms[id].contentHash()
		cps = append(cps, chains[id].Append(hash, label))
	}
	return cps
}

// CheckpointChains implements Checkpointer for LocalRegionProvider.
func (p *LocalRegionProvider) CheckpointChains() map[string]*CheckpointChain {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]*CheckpointChain, len(p.chains))
	for k, v := range p.chains {
		out[k] = v
	}
	return out
}
