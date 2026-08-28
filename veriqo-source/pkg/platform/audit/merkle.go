// This file extends package audit with VTECP-001 Capability 4's
// remaining requirements over what AuditStore/Auditor already provide:
// AuditStore is a linear hash CHAIN (each record commits to the
// previous one), which proves the whole log has not been tampered
// with once you already hold the whole log. It does not let a third
// party verify that ONE record is part of the log without handing
// them every other record, and it has no notion of periodically
// sealing a range of the log against an external reference point.
//
// This file adds both, per RFC 6962 (Certificate Transparency)'s
// Merkle Tree Hash construction -- chosen over a naive/Bitcoin-style
// pairwise tree specifically because RFC 6962's domain-separated leaf
// (0x00 prefix) and node (0x01 prefix) hashing defeats the standard
// second-preimage attack where an inner node's hash is replayed as a
// leaf hash:
//
//  1. MerkleRoot / GenerateInclusionProof / VerifyInclusionProof --
//     the "Verify API": a third party holding only ONE record, its
//     proof, and a root can confirm that record is really in the log
//     covered by that root, in O(log n) data, without the full log.
//  2. Anchorer / LocalAnchorer / AuditStore.Checkpoint -- periodically
//     seals a Merkle root of the log-so-far against an external
//     reference. VERIQO has NO real external anchoring integration
//     today (no blockchain, no third-party notarization service, no
//     regulator timestamping authority) -- per VTECP-001's anti-
//     fabrication rule ("use interface, adapter, mock, simulator,
//     contract test -- until real integration exists"), this file
//     defines only the Anchorer interface plus LocalAnchorer, an
//     explicitly-labeled in-memory simulator whose every AnchorReceipt
//     says so in AnchoredBy. A real Anchorer, when one exists, plugs
//     into Checkpoint without any caller-visible change.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrEmptyLeafSet    = errors.New("audit: cannot compute a Merkle root over zero leaves")
	ErrIndexOutOfRange = errors.New("audit: leaf index out of range")
	ErrProofInvalid    = errors.New("audit: merkle inclusion proof does not verify")
)

const (
	leafHashPrefix byte = 0x00
	nodeHashPrefix byte = 0x01
)

func leafHash(data []byte) []byte {
	h := sha256.New()
	h.Write([]byte{leafHashPrefix})
	h.Write(data)
	return h.Sum(nil)
}

func nodeHash(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{nodeHashPrefix})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// splitPoint returns the largest power of two strictly less than n,
// per RFC 6962 section 2.1's MTH definition.
func splitPoint(n int) int {
	k := 1
	for k*2 < n {
		k *= 2
	}
	return k
}

// mth computes the RFC 6962 Merkle Tree Hash over leaves, each entry
// being raw (not yet leaf-hashed) leaf data.
func mth(leaves [][]byte) []byte {
	switch n := len(leaves); {
	case n == 0:
		h := sha256.Sum256(nil)
		return h[:]
	case n == 1:
		return leafHash(leaves[0])
	default:
		k := splitPoint(n)
		left := mth(leaves[:k])
		right := mth(leaves[k:])
		return nodeHash(left, right)
	}
}

// MerkleProofStep is one sibling hash on the path from a leaf to the
// root, together with which side of the current subtree it sits on.
type MerkleProofStep struct {
	Hash          string `json:"hash"`
	SiblingIsLeft bool   `json:"sibling_is_left"`
}

// MerkleProof is a self-contained inclusion proof: everything a third
// party needs to confirm one record is included in the tree that
// produced Root, without seeing any other record.
type MerkleProof struct {
	LeafIndex int               `json:"leaf_index"`
	LeafCount int               `json:"leaf_count"`
	LeafData  string            `json:"leaf_data"` // hex of the raw leaf data (an AuditRecord.Hash)
	Steps     []MerkleProofStep `json:"steps"`
	Root      string            `json:"root"`
}

func leafDataFor(records []AuditRecord) ([][]byte, error) {
	out := make([][]byte, len(records))
	for i, r := range records {
		b, err := hex.DecodeString(r.Hash)
		if err != nil {
			return nil, fmt.Errorf("audit: record %d has a non-hex hash: %w", r.Index, err)
		}
		out[i] = b
	}
	return out, nil
}

// MerkleRoot computes the RFC 6962 Merkle root over records' own
// Hash values, in order.
func MerkleRoot(records []AuditRecord) (string, error) {
	if len(records) == 0 {
		return "", ErrEmptyLeafSet
	}
	leaves, err := leafDataFor(records)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(mth(leaves)), nil
}

// computePath implements RFC 6962 section 2.1.1's PATH() recursively:
// the audit path for leaf m within leaves, ordered from the leaf's own
// level up to (but not including) the root.
func computePath(leaves [][]byte, m int) []MerkleProofStep {
	n := len(leaves)
	if n <= 1 {
		return nil
	}
	k := splitPoint(n)
	if m < k {
		steps := computePath(leaves[:k], m)
		sibling := mth(leaves[k:])
		return append(steps, MerkleProofStep{Hash: hex.EncodeToString(sibling), SiblingIsLeft: false})
	}
	steps := computePath(leaves[k:], m-k)
	sibling := mth(leaves[:k])
	return append(steps, MerkleProofStep{Hash: hex.EncodeToString(sibling), SiblingIsLeft: true})
}

// GenerateInclusionProof builds the inclusion proof for records[index]
// against the Merkle root of the full records slice.
func GenerateInclusionProof(records []AuditRecord, index int) (MerkleProof, error) {
	if len(records) == 0 {
		return MerkleProof{}, ErrEmptyLeafSet
	}
	if index < 0 || index >= len(records) {
		return MerkleProof{}, fmt.Errorf("%w: %d (have %d records)", ErrIndexOutOfRange, index, len(records))
	}
	leaves, err := leafDataFor(records)
	if err != nil {
		return MerkleProof{}, err
	}
	root := mth(leaves)
	return MerkleProof{
		LeafIndex: index,
		LeafCount: len(records),
		LeafData:  records[index].Hash,
		Steps:     computePath(leaves, index),
		Root:      hex.EncodeToString(root),
	}, nil
}

// VerifyInclusionProof recomputes the root from proof.LeafData and
// proof.Steps and confirms it matches proof.Root -- this is the
// "Verify API": it needs nothing but the proof itself.
func VerifyInclusionProof(proof MerkleProof) error {
	leafBytes, err := hex.DecodeString(proof.LeafData)
	if err != nil {
		return fmt.Errorf("%w: leaf data is not valid hex", ErrProofInvalid)
	}
	cur := leafHash(leafBytes)
	for _, step := range proof.Steps {
		sib, err := hex.DecodeString(step.Hash)
		if err != nil {
			return fmt.Errorf("%w: sibling hash is not valid hex", ErrProofInvalid)
		}
		if step.SiblingIsLeft {
			cur = nodeHash(sib, cur)
		} else {
			cur = nodeHash(cur, sib)
		}
	}
	if hex.EncodeToString(cur) != proof.Root {
		return fmt.Errorf("%w: recomputed root does not match", ErrProofInvalid)
	}
	return nil
}

// VerifyRecordInclusion ties a proof to a specific concrete record
// (not just abstract leaf data) before verifying it.
func VerifyRecordInclusion(record AuditRecord, proof MerkleProof) error {
	if record.Hash != proof.LeafData {
		return fmt.Errorf("%w: proof leaf data does not match the given record's hash", ErrProofInvalid)
	}
	return VerifyInclusionProof(proof)
}

// AnchorReceipt is evidence that a Merkle root was committed to an
// anchor at a point in time. AnchoredBy always names which Anchorer
// implementation produced it, so a simulated anchor can never be
// mistaken for a real external commitment.
type AnchorReceipt struct {
	Root       string `json:"root"`
	Tick       uint64 `json:"tick"`
	AnchoredBy string `json:"anchored_by"`
	Reference  string `json:"reference"`
}

// Anchorer commits a Merkle root to some reference point external to
// this process. See this file's package doc comment for VERIQO's
// current, honest anchoring status: no real external Anchorer exists
// yet, only LocalAnchorer below.
type Anchorer interface {
	Anchor(root string, tick uint64) (AnchorReceipt, error)
}

// LocalAnchorer is an explicit in-memory SIMULATOR, not a real
// external anchor: it records an anchor request in a local,
// monotonically increasing sequence and returns a receipt honestly
// labeled as such. Safe to use in tests and in any deployment that has
// not yet integrated a real external anchoring service, but its
// receipts must never be represented as external-system proof.
type LocalAnchorer struct {
	mu  sync.Mutex
	seq uint64
	log []AnchorReceipt
}

// NewLocalAnchorer returns an empty local anchor simulator.
func NewLocalAnchorer() *LocalAnchorer {
	return &LocalAnchorer{}
}

// Anchor implements Anchorer.
func (a *LocalAnchorer) Anchor(root string, tick uint64) (AnchorReceipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	r := AnchorReceipt{
		Root:       root,
		Tick:       tick,
		AnchoredBy: "LocalAnchorer(simulator, not a real external anchor)",
		Reference:  fmt.Sprintf("local-sim-seq:%d", a.seq),
	}
	a.log = append(a.log, r)
	return r, nil
}

// Log returns every receipt this simulator has issued, oldest first.
func (a *LocalAnchorer) Log() []AnchorReceipt {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AnchorReceipt, len(a.log))
	copy(out, a.log)
	return out
}

// Checkpoint is a sealed Merkle root over a contiguous range of an
// AuditStore's records, plus the AnchorReceipt committing that root.
type Checkpoint struct {
	FromIndex  uint64        `json:"from_index"`
	ToIndex    uint64        `json:"to_index"`
	MerkleRoot string        `json:"merkle_root"`
	Anchor     AnchorReceipt `json:"anchor"`
}

// Checkpoint computes the Merkle root over every record currently in
// s and anchors it via anchorer -- the periodic "seal the ledger so
// far" operation VTECP-001 Capability 4 calls for. It does not mutate
// s.
func (s *AuditStore) Checkpoint(anchorer Anchorer, tick uint64) (Checkpoint, error) {
	records := s.Snapshot()
	if len(records) == 0 {
		return Checkpoint{}, ErrEmptyLeafSet
	}
	root, err := MerkleRoot(records)
	if err != nil {
		return Checkpoint{}, err
	}
	receipt, err := anchorer.Anchor(root, tick)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("audit: anchoring checkpoint: %w", err)
	}
	return Checkpoint{
		FromIndex:  records[0].Index,
		ToIndex:    records[len(records)-1].Index,
		MerkleRoot: root,
		Anchor:     receipt,
	}, nil
}

// VerifyCheckpoint re-derives cp.MerkleRoot from records (which must be
// exactly the [FromIndex, ToIndex] range the checkpoint claims) and
// confirms it matches -- the independent "has the ledger changed since
// this checkpoint was sealed" check a third party runs without trusting
// the AuditStore that produced it.
func VerifyCheckpoint(records []AuditRecord, cp Checkpoint) error {
	if len(records) == 0 {
		return fmt.Errorf("%w: no records to verify checkpoint against", ErrEmptyLeafSet)
	}
	if records[0].Index != cp.FromIndex || records[len(records)-1].Index != cp.ToIndex {
		return fmt.Errorf("%w: record range does not match checkpoint range", ErrProofInvalid)
	}
	root, err := MerkleRoot(records)
	if err != nil {
		return err
	}
	if root != cp.MerkleRoot {
		return fmt.Errorf("%w: merkle root mismatch: checkpoint=%s recomputed=%s", ErrProofInvalid, cp.MerkleRoot, root)
	}
	return nil
}
