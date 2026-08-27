// memstore.go — in-memory LogStore and StateStore for tests.
package raft

import (
	"errors"
	"fmt"
	"sync"
)

// ─── MemLogStore ──────────────────────────────────────────────────────────────

// MemLogStore is a thread-safe in-memory LogStore.
type MemLogStore struct {
	mu      sync.RWMutex
	entries []Entry // entries[0].Index is the first stored entry
}

var _ LogStore = (*MemLogStore)(nil)

func (s *MemLogStore) FirstIndex() Index {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 {
		return 0
	}
	return s.entries[0].Index
}

func (s *MemLogStore) LastIndex() Index {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 {
		return 0
	}
	return s.entries[len(s.entries)-1].Index
}

func (s *MemLogStore) GetEntry(i Index) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.Index == i {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("raft: log entry %d not found", i)
}

func (s *MemLogStore) AppendEntries(entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Truncate conflicting suffix.
	if len(s.entries) > 0 {
		first := entries[0].Index
		if first <= s.entries[len(s.entries)-1].Index {
			s.entries = s.trimTo(first - 1)
		}
	}
	s.entries = append(s.entries, entries...)
	return nil
}

func (s *MemLogStore) DeleteFrom(from Index) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.trimTo(from - 1)
	return nil
}

func (s *MemLogStore) trimTo(last Index) []Entry {
	for i, e := range s.entries {
		if e.Index > last {
			return s.entries[:i]
		}
	}
	return s.entries
}

// ─── MemStateStore ────────────────────────────────────────────────────────────

// MemStateStore is an in-memory StateStore.
type MemStateStore struct {
	mu       sync.Mutex
	term     Term
	votedFor NodeID
}

var _ StateStore = (*MemStateStore)(nil)

func (s *MemStateStore) SaveState(term Term, votedFor NodeID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term = term
	s.votedFor = votedFor
	return nil
}

func (s *MemStateStore) LoadState() (Term, NodeID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term, s.votedFor, nil
}

// ─── NoopFSM ─────────────────────────────────────────────────────────────────

// NoopFSM is a no-op FSM for testing.
type NoopFSM struct{}

var _ FSM = (*NoopFSM)(nil)

func (*NoopFSM) Apply(_ Entry) error       { return nil }
func (*NoopFSM) Snapshot() ([]byte, error) { return []byte("{}"), nil }
func (*NoopFSM) Restore(_ []byte) error    { return nil }

// ─── RecordingFSM ─────────────────────────────────────────────────────────────

// RecordingFSM records every applied entry for test verification.
type RecordingFSM struct {
	mu      sync.Mutex
	applied []Entry
}

var _ FSM = (*RecordingFSM)(nil)

func (f *RecordingFSM) Apply(e Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, e)
	return nil
}

func (f *RecordingFSM) Snapshot() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []byte(fmt.Sprintf(`{"applied":%d}`, len(f.applied))), nil
}

func (f *RecordingFSM) Restore(_ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = nil
	return nil
}

func (f *RecordingFSM) Applied() []Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Entry, len(f.applied))
	copy(out, f.applied)
	return out
}

// ─── SimTransport ─────────────────────────────────────────────────────────────

// SimTransport is an in-process transport for cluster tests.
// It supports network partition simulation.
type SimTransport struct {
	mu        sync.RWMutex
	nodes     map[NodeID]*Node
	partition map[NodeID]bool // nodes in the minority partition
}

var _ Transport = (*SimTransport)(nil)

// NewSimTransport creates an empty in-process transport.
func NewSimTransport() *SimTransport {
	return &SimTransport{
		nodes:     make(map[NodeID]*Node),
		partition: make(map[NodeID]bool),
	}
}

// Register adds a node to the transport.
func (t *SimTransport) Register(id NodeID, n *Node) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodes[id] = n
}

// Partition isolates the given nodes from the rest of the cluster.
func (t *SimTransport) Partition(ids ...NodeID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range ids {
		t.partition[id] = true
	}
}

// Heal removes all partitions.
func (t *SimTransport) Heal() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.partition = make(map[NodeID]bool)
}

func (t *SimTransport) isPartitioned(src, dst NodeID) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.partition[src] != t.partition[dst]
}

var errPartitioned = errors.New("raft: network partitioned")

func (t *SimTransport) SendRequestVote(target NodeID, req RequestVoteRequest) (RequestVoteResponse, error) {
	if t.isPartitioned(req.CandidateID, target) {
		return RequestVoteResponse{}, errPartitioned
	}
	t.mu.RLock()
	n := t.nodes[target]
	t.mu.RUnlock()
	if n == nil {
		return RequestVoteResponse{}, fmt.Errorf("raft: node %s not found", target)
	}
	return n.HandleRequestVote(req)
}

func (t *SimTransport) SendAppendEntries(target NodeID, req AppendEntriesRequest) (AppendEntriesResponse, error) {
	if t.isPartitioned(req.LeaderID, target) {
		return AppendEntriesResponse{}, errPartitioned
	}
	t.mu.RLock()
	n := t.nodes[target]
	t.mu.RUnlock()
	if n == nil {
		return AppendEntriesResponse{}, fmt.Errorf("raft: node %s not found", target)
	}
	return n.HandleAppendEntries(req)
}

func (t *SimTransport) SendInstallSnapshot(target NodeID, req InstallSnapshotRequest) (InstallSnapshotResponse, error) {
	if t.isPartitioned(req.LeaderID, target) {
		return InstallSnapshotResponse{}, errPartitioned
	}
	t.mu.RLock()
	n := t.nodes[target]
	t.mu.RUnlock()
	if n == nil {
		return InstallSnapshotResponse{}, fmt.Errorf("raft: node %s not found", target)
	}
	return n.HandleInstallSnapshot(req)
}
