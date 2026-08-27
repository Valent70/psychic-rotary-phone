// Package snapshot implements Raft snapshot creation, installation,
// streaming, incremental delta, and checksum verification.
// Filling the gap identified in the consensus audit: "Snapshot — not yet done".
package snapshot

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Index is the Raft log index at which a snapshot was taken.
type Index uint64

// Term is the Raft term at which a snapshot was taken.
type Term uint64

// Metadata describes a snapshot without loading its data.
type Metadata struct {
	ID        string   // unique snapshot identifier
	Index     Index    // last log index included
	Term      Term     // last log term included
	Size      int64    // bytes
	Checksum  [32]byte // SHA-256 of data
	CreatedAt time.Time
	// Membership contains the cluster configuration at snapshot time.
	Membership []string // node IDs
}

// Snapshot is an immutable point-in-time capture of FSM state.
type Snapshot struct {
	Meta Metadata
	Data []byte
}

// Verify returns an error if the data does not match the stored checksum.
func (s *Snapshot) Verify() error {
	got := sha256.Sum256(s.Data)
	if got != s.Meta.Checksum {
		return fmt.Errorf("snapshot %s: checksum mismatch", s.Meta.ID)
	}
	return nil
}

// Reader returns a streaming reader for this snapshot's data.
func (s *Snapshot) Reader() io.Reader {
	return &chunkReader{data: s.Data}
}

// chunkReader streams snapshot data in configurable chunks.
type chunkReader struct {
	data   []byte
	offset int
}

func (r *chunkReader) Read(p []byte) (n int, err error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

// ─── Delta (incremental snapshot) ────────────────────────────────────────────

// Delta represents the difference between two consecutive snapshots.
// Applying a delta to base produces the new snapshot data.
type Delta struct {
	BaseID      string
	TargetID    string
	BaseIndex   Index
	TargetIndex Index
	Chunks      []DeltaChunk
	Checksum    [32]byte
}

// DeltaChunk is a region of change.
type DeltaChunk struct {
	Offset int64
	Data   []byte
	Kind   ChunkKind
}

// ChunkKind classifies the chunk type.
type ChunkKind byte

const (
	ChunkAdd    ChunkKind = 'A'
	ChunkDelete ChunkKind = 'D'
	ChunkEqual  ChunkKind = 'E'
)

// Diff computes a byte-level delta between base and target snapshots.
// This is a simple block-diff; production would use xdelta/bsdiff.
func Diff(base, target *Snapshot) *Delta {
	d := &Delta{
		BaseID:      base.Meta.ID,
		TargetID:    target.Meta.ID,
		BaseIndex:   base.Meta.Index,
		TargetIndex: target.Meta.Index,
	}

	const block = 4096
	maxLen := max(len(base.Data), len(target.Data))
	for off := 0; off < maxLen; off += block {
		bSlice := safeSlice(base.Data, off, off+block)
		tSlice := safeSlice(target.Data, off, off+block)
		if string(bSlice) == string(tSlice) {
			d.Chunks = append(d.Chunks, DeltaChunk{Offset: int64(off), Kind: ChunkEqual})
		} else {
			d.Chunks = append(d.Chunks, DeltaChunk{Offset: int64(off), Data: append([]byte(nil), tSlice...), Kind: ChunkAdd})
		}
	}
	d.Checksum = sha256.Sum256(target.Data)
	return d
}

// Apply reconstructs a target snapshot by patching base with delta.
func Apply(base *Snapshot, d *Delta) (*Snapshot, error) {
	if base.Meta.ID != d.BaseID {
		return nil, fmt.Errorf("snapshot delta: base ID mismatch: got %q want %q", base.Meta.ID, d.BaseID)
	}
	result := make([]byte, 0, len(base.Data))
	result = append(result, base.Data...)
	for _, c := range d.Chunks {
		off := int(c.Offset)
		switch c.Kind {
		case ChunkAdd:
			end := off + len(c.Data)
			if end > len(result) {
				result = append(result, make([]byte, end-len(result))...)
			}
			copy(result[off:], c.Data)
		case ChunkEqual:
			// unchanged
		}
	}
	got := sha256.Sum256(result)
	if got != d.Checksum {
		return nil, errors.New("snapshot delta: apply checksum mismatch")
	}
	return &Snapshot{
		Meta: Metadata{
			ID:       d.TargetID,
			Index:    d.TargetIndex,
			Checksum: got,
		},
		Data: result,
	}, nil
}

// ─── Store ────────────────────────────────────────────────────────────────────

// Store persists snapshots to disk.
type Store struct {
	mu      sync.RWMutex
	dir     string
	catalog []*Metadata // ordered by Index
}

// OpenStore opens or creates a snapshot store in the given directory.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("snapshot: mkdir %q: %w", dir, err)
	}
	s := &Store{dir: dir}
	if err := s.loadCatalog(); err != nil {
		return nil, err
	}
	return s, nil
}

// Save writes a snapshot to disk and updates the catalog.
func (s *Store) Save(snap *Snapshot) error {
	if err := snap.Verify(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	metaPath := filepath.Join(s.dir, snap.Meta.ID+".meta")
	dataPath := filepath.Join(s.dir, snap.Meta.ID+".snap")

	metaBytes, err := json.Marshal(snap.Meta)
	if err != nil {
		return err
	}
	if err := atomicWrite(metaPath, metaBytes); err != nil {
		return err
	}
	if err := atomicWrite(dataPath, snap.Data); err != nil {
		return err
	}

	s.catalog = append(s.catalog, &snap.Meta)
	sort.Slice(s.catalog, func(i, j int) bool { return s.catalog[i].Index < s.catalog[j].Index })
	return nil
}

// Load reads a snapshot by ID.
func (s *Store) Load(id string) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	metaPath := filepath.Join(s.dir, id+".meta")
	dataPath := filepath.Join(s.dir, id+".snap")

	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot: load meta %q: %w", id, err)
	}
	var meta Metadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot: load data %q: %w", id, err)
	}
	snap := &Snapshot{Meta: meta, Data: data}
	if err := snap.Verify(); err != nil {
		return nil, err
	}
	return snap, nil
}

// Latest returns metadata for the most recent snapshot, or nil if empty.
func (s *Store) Latest() *Metadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.catalog) == 0 {
		return nil
	}
	return s.catalog[len(s.catalog)-1]
}

// Catalog returns all snapshot metadata ordered by Index.
func (s *Store) Catalog() []*Metadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Metadata, len(s.catalog))
	copy(out, s.catalog)
	return out
}

// loadCatalog scans the directory for existing snapshots.
func (s *Store) loadCatalog() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("snapshot: readdir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return err
		}
		var m Metadata
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.catalog = append(s.catalog, &m)
	}
	sort.Slice(s.catalog, func(i, j int) bool { return s.catalog[i].Index < s.catalog[j].Index })
	return nil
}

// ─── Build helpers ────────────────────────────────────────────────────────────

// Builder creates a Snapshot from arbitrary FSM state.
type Builder struct {
	id         string
	index      Index
	term       Term
	membership []string
}

// NewBuilder starts building a snapshot with the given Raft position.
func NewBuilder(id string, index Index, term Term, membership []string) *Builder {
	return &Builder{id: id, index: index, term: term, membership: membership}
}

// Build serialises data and computes the checksum.
func (b *Builder) Build(data []byte) *Snapshot {
	checksum := sha256.Sum256(data)
	return &Snapshot{
		Meta: Metadata{
			ID:         b.id,
			Index:      b.index,
			Term:       b.term,
			Size:       int64(len(data)),
			Checksum:   checksum,
			CreatedAt:  time.Now().UTC(),
			Membership: append([]string(nil), b.membership...),
		},
		Data: append([]byte(nil), data...),
	}
}

// ─── Install ─────────────────────────────────────────────────────────────────

// InstallResult is returned after a snapshot is installed on a node.
type InstallResult struct {
	SnapshotID        string
	LastIncludedIndex Index
	LastIncludedTerm  Term
	BytesReceived     int64
}

// InstallFromStream reads a snapshot from a reader and verifies its checksum.
// Used when a follower receives a snapshot from the leader over gRPC.
func InstallFromStream(r io.Reader, expectedChecksum [32]byte, expectedSize int64) (*Snapshot, error) {
	data := make([]byte, 0, expectedSize)
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("snapshot: stream read: %w", err)
		}
	}
	got := sha256.Sum256(data)
	if got != expectedChecksum {
		return nil, fmt.Errorf("snapshot: stream checksum mismatch")
	}
	return &Snapshot{
		Meta: Metadata{Checksum: got, Size: int64(len(data))},
		Data: data,
	}, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	_ = f.Sync()
	_ = f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func safeSlice(b []byte, lo, hi int) []byte {
	if lo >= len(b) {
		return nil
	}
	if hi > len(b) {
		hi = len(b)
	}
	return b[lo:hi]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// encodeIndex serialises an Index to 8 bytes big-endian.
func encodeIndex(idx Index) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(idx))
	return b[:]
}

var _ = encodeIndex // suppress unused
