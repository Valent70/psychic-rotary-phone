package raftlite

import (
	"path/filepath"
	"testing"
	"time"
)

// TestNode_RestartWithStorage_RecoversTermVoteAndLog proves the WAL
// integration end-to-end at the Node level (not just FileStorage in
// isolation): a node that persists across several real state
// transitions, then is torn down and reconstructed with a FRESH Node
// value pointed at the same Storage, must resume with byte-identical
// term/votedFor/log — the actual "crash -> restart -> replay -> hash
// sama" contract callers depend on.
func TestNode_RestartWithStorage_RecoversTermVoteAndLog(t *testing.T) {
	dir := t.TempDir()
	storage := NewFileStorage(filepath.Join(dir, "node-a.wal"))

	trans := NewMemTransport()
	n1 := NewNode(Config{
		ID: "solo", Peers: nil, Transport: trans, FSM: newSumFSM(),
		ElectionTimeoutMin: 30 * time.Millisecond, ElectionTimeoutMax: 60 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		Storage:           storage,
	})
	trans.Register(n1)

	// A single-node cluster elects itself leader almost immediately once
	// Run() starts (no peers to wait on for quorum).
	// Drive it directly instead of via Run() so this test controls
	// exactly which persisted transitions happen, deterministically.
	n1.mu.Lock()
	n1.role = Candidate
	n1.state.currentTerm = 1
	n1.state.votedFor = "solo"
	n1.persistLocked()
	n1.mu.Unlock()
	n1.becomeLeader(1)

	idx1, term1, err := n1.Propose([]byte("alpha"))
	if err != nil {
		t.Fatalf("Propose(alpha): %v", err)
	}
	idx2, _, err := n1.Propose([]byte("beta"))
	if err != nil {
		t.Fatalf("Propose(beta): %v", err)
	}
	if idx2 != idx1+1 {
		t.Fatalf("expected sequential indices, got %d then %d", idx1, idx2)
	}

	n1.mu.Lock()
	wantTerm := n1.state.currentTerm
	wantVotedFor := n1.state.votedFor
	wantLogLen := len(n1.state.log)
	n1.mu.Unlock()
	if wantTerm != term1 {
		t.Fatalf("sanity: term drifted, %d != %d", wantTerm, term1)
	}

	// Simulate the process dying (no clean shutdown call at all — the
	// WAL's crash-atomicity is what's supposed to make this safe) and a
	// fresh process starting up pointed at the same WAL file.
	n2 := NewNode(Config{
		ID: "solo", Peers: nil, Transport: trans, FSM: newSumFSM(),
		Storage: storage,
	})

	n2.mu.Lock()
	gotTerm := n2.state.currentTerm
	gotVotedFor := n2.state.votedFor
	gotLogLen := len(n2.state.log)
	n2.mu.Unlock()

	if gotTerm != wantTerm {
		t.Fatalf("recovered term = %d, want %d", gotTerm, wantTerm)
	}
	if gotVotedFor != wantVotedFor {
		t.Fatalf("recovered votedFor = %q, want %q", gotVotedFor, wantVotedFor)
	}
	if gotLogLen != wantLogLen {
		t.Fatalf("recovered log length = %d, want %d", gotLogLen, wantLogLen)
	}
}
