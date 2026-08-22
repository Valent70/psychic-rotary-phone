package raftlite

import (
	"encoding/json"
	"testing"
)

// TestBecomeLeader_ResumesStrandedJointConsensusTransition is the named
// test the R-029 caveat asked for: "leader-during-reconfiguration
// scenario". It is a direct, deterministic, same-package test of the
// exact mechanism the scenario turns on, rather than a live multi-node
// cluster racing real goroutines and timers against each other --
// see the honest note in this round's governance write-up on why:
// several attempts at a full cluster-level version (killing a real
// leader mid-transition and waiting for a real re-election) proved
// unreliable to make deterministic in this sandbox, for reasons that
// turned out to be about Raft's OWN liveness properties under a
// partially-partitioned survivor set (PreVote's leaderLive check, and
// joint consensus's dual-majority quorum leaving zero slack once a
// member is lost) rather than about the fix itself. This test isolates
// the ACTUAL mechanism under scrutiny -- becomeLeader's handling of an
// already-active joint-consensus transition -- with no timing
// dependency at all.
//
// The scenario: a node receives (via ordinary log replication) an
// ENTER_JOINT entry from a leader that then fails before ever
// proposing LEAVE_JOINT -- exactly what happens when
// applyCommitted's own comment admits was "best-effort: if leadership
// is lost mid-transition, the new leader must re-drive LEAVE_JOINT
// itself". This node is later elected leader itself (simulated
// directly here, without a real election) while still carrying that
// stranded, active joint-consensus state. Before the becomeLeader fix
// in raft.go (same round), nothing resumed it: InJointConsensus() would
// stay true forever, and the cluster would be stuck on the stricter
// dual-majority quorum permanently. becomeLeader must notice
// n.joint.active and append its own LEAVE_JOINT entry, in its own new
// term, exactly like the no-op entry two lines above it in raft.go
// already does for the analogous "commit carry-over entries from a
// prior term" problem (Raft §5.4.2).
func TestBecomeLeader_ResumesStrandedJointConsensusTransition(t *testing.T) {
	n := NewNode(Config{ID: "B", Peers: []string{"A", "C", "D"}, Transport: NewMemTransport()})

	// Simulate this node having already received (via ordinary
	// AppendEntries replication, applied at append time per
	// refreshJointFromLogLocked's own contract) an ENTER_JOINT entry
	// from a leader that is now gone. Old={A,B,C}, adding D.
	env := jointEnvelope{Magic: jointMagic, Phase: jointEnter, Old: []string{"A", "B", "C"}, New: []string{"A", "B", "C", "D"}}
	cmd, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal ENTER_JOINT: %v", err)
	}

	n.mu.Lock()
	n.state.log = append(n.state.log, LogEntry{Term: 1, Index: 1, Command: cmd})
	n.refreshJointFromLogLocked() // exactly what HandleAppendEntries calls after every append
	n.role = Candidate            // about to win an election, per Run()'s own state machine
	n.state.currentTerm = 1
	stillActive := n.joint.active
	n.mu.Unlock()

	if !stillActive {
		t.Fatal("test setup: joint consensus should already be active after receiving ENTER_JOINT")
	}
	if !n.InJointConsensus() {
		t.Fatal("test setup: InJointConsensus() should report true")
	}

	// This node now wins its election -- becomeLeader is Run()'s own
	// entry point for that (see raft.go's startElection ->
	// n.becomeLeader(term) call), invoked directly here since this test
	// is not driving a real election.
	n.becomeLeader(1)

	if n.InJointConsensus() {
		t.Fatal("becomeLeader did not resume the stranded joint-consensus transition -- InJointConsensus() still true after winning the election; the cluster would be stuck on the stricter dual-majority quorum forever")
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	foundLeave := false
	for _, e := range n.state.log {
		if leaveEnv, ok := isJointCommand(e.Command); ok && leaveEnv.Phase == jointLeave {
			foundLeave = true
			if len(leaveEnv.New) != 4 {
				t.Fatalf("LEAVE_JOINT entry has wrong target membership: %v, want the 4-member new config", leaveEnv.New)
			}
		}
	}
	if !foundLeave {
		t.Fatal("becomeLeader did not append a LEAVE_JOINT entry for the stranded transition")
	}
	if len(n.peers) != 3 {
		t.Fatalf("peers after resuming LEAVE_JOINT = %v, want 3 (A, C, D)", n.peers)
	}
}

// TestBecomeLeader_DoesNotResumeWhenNoJointTransitionIsActive is the
// negative case: becomeLeader must never fabricate a LEAVE_JOINT entry
// when no joint transition is in progress -- proving the fix in raft.go
// is gated correctly, not an unconditional append.
func TestBecomeLeader_DoesNotResumeWhenNoJointTransitionIsActive(t *testing.T) {
	n := NewNode(Config{ID: "B", Peers: []string{"A", "C"}, Transport: NewMemTransport()})
	n.mu.Lock()
	n.role = Candidate
	n.state.currentTerm = 1
	n.mu.Unlock()

	n.becomeLeader(1)

	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range n.state.log {
		if _, ok := isJointCommand(e.Command); ok {
			t.Fatalf("becomeLeader fabricated a joint-consensus entry (%q) when no transition was active", e.Command)
		}
	}
}
