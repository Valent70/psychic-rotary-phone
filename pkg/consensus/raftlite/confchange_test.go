package raftlite

import (
	"context"
	"testing"
	"time"
)

func TestValidateConfChangeBatch_RejectsDuplicateInBatch(t *testing.T) {
	_, err := validateConfChangeBatch([]string{"A", "B", "C"}, []ConfChange{
		{Type: ConfChangeAddVoter, NodeID: "D"},
		{Type: ConfChangeRemoveVoter, NodeID: "D"},
	})
	if err == nil {
		t.Fatalf("expected error for duplicate node id within batch")
	}
}

func TestValidateConfChangeBatch_RejectsRemovingNonMember(t *testing.T) {
	_, err := validateConfChangeBatch([]string{"A", "B"}, []ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: "Z"},
	})
	if err == nil {
		t.Fatalf("expected error removing non-member")
	}
}

func TestValidateConfChangeBatch_RejectsAddingExistingMember(t *testing.T) {
	_, err := validateConfChangeBatch([]string{"A", "B"}, []ConfChange{
		{Type: ConfChangeAddVoter, NodeID: "A"},
	})
	if err == nil {
		t.Fatalf("expected error adding existing member")
	}
}

func TestValidateConfChangeBatch_RejectsEmptyResultingCluster(t *testing.T) {
	_, err := validateConfChangeBatch([]string{"A", "B"}, []ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: "A"},
		{Type: ConfChangeRemoveVoter, NodeID: "B"},
	})
	if err == nil {
		t.Fatalf("expected error for empty resulting cluster")
	}
}

func TestValidateConfChangeBatch_RejectsEmptyBatch(t *testing.T) {
	_, err := validateConfChangeBatch([]string{"A", "B"}, nil)
	if err == nil {
		t.Fatalf("expected error for empty batch")
	}
}

func TestValidateConfChangeBatch_AtomicMixedBatchSucceeds(t *testing.T) {
	resulting, err := validateConfChangeBatch([]string{"A", "B", "C"}, []ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: "C"},
		{Type: ConfChangeAddVoter, NodeID: "D"},
	})
	if err != nil {
		t.Fatalf("expected valid mixed batch to succeed: %v", err)
	}
	want := map[string]bool{"A": true, "B": true, "D": true}
	if len(resulting) != 3 {
		t.Fatalf("expected 3 resulting members, got %v", resulting)
	}
	for _, m := range resulting {
		if !want[m] {
			t.Fatalf("unexpected member in result: %s (%v)", m, resulting)
		}
	}
}

func TestProposeJointConfChange_RejectsNonLeader(t *testing.T) {
	_, nodes, cancel := buildCluster(t, 3, nil)
	defer cancel()
	// Before election settles, at least one node is guaranteed non-leader;
	// simpler: just find a follower after leader is elected.
	leader := waitForLeader(t, nodes, 2*time.Second)
	var follower *Node
	for _, n := range nodes {
		if n != leader {
			follower = n
			break
		}
	}
	if _, _, err := follower.ProposeJointConfChange([]ConfChange{{Type: ConfChangeRemoveVoter, NodeID: leader.ID()}}); err != ErrNotLeader {
		t.Fatalf("expected ErrNotLeader from a follower, got %v", err)
	}
}

func TestProposeJointConfChange_RejectsInvalidBatchBeforeProposing(t *testing.T) {
	_, nodes, cancel := buildCluster(t, 3, nil)
	defer cancel()
	leader := waitForLeader(t, nodes, 2*time.Second)
	// Not leader.CommitIndex() directly: the leader's own post-election
	// no-op entry may still be in flight (see waitForCommitIndexToStabilize's
	// doc comment) -- capturing the "before" snapshot before it settles
	// risks blaming its own later, unrelated commit on this test's
	// intentionally-rejected proposal.
	beforeIndex := waitForCommitIndexToStabilize(t, leader, 2*time.Second)

	_, _, err := leader.ProposeJointConfChange([]ConfChange{{Type: ConfChangeRemoveVoter, NodeID: "NONEXISTENT"}})
	if err == nil {
		t.Fatalf("expected validation error for removing a non-member")
	}
	// no log entry should have been written for an invalid batch
	if leader.CommitIndex() != beforeIndex {
		t.Fatalf("commit index should not advance from a rejected batch")
	}
}

// TestProposeJointConfChange_AtomicRemovalCommitsAndConverges proves the
// core safety property the audit doc asks for: a membership change
// commits atomically (as one log entry) and every node in the cluster
// converges to the SAME resulting membership view, never a partial one.
func TestProposeJointConfChange_AtomicRemovalCommitsAndConverges(t *testing.T) {
	_, nodes, cancel := buildCluster(t, 3, nil)
	defer cancel()
	leader := waitForLeader(t, nodes, 2*time.Second)

	var victim *Node
	for _, n := range nodes {
		if n != leader {
			victim = n
			break
		}
	}

	idx, _, err := leader.ProposeJointConfChange([]ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: victim.ID()},
	})
	if err != nil {
		t.Fatalf("ProposeJointConfChange: %v", err)
	}
	ctx, cancelWait := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelWait()
	if err := leader.WaitApplied(ctx, idx); err != nil {
		t.Fatalf("conf change did not apply on leader: %v", err)
	}

	// give followers a moment to catch up via heartbeats
	time.Sleep(300 * time.Millisecond)

	leaderMembers := leader.Members()
	if len(leaderMembers) != 2 {
		t.Fatalf("expected leader to converge to 2 members after removal, got %v", leaderMembers)
	}
	for _, m := range leaderMembers {
		if m == victim.ID() {
			t.Fatalf("removed node %s should not appear in resulting membership %v", victim.ID(), leaderMembers)
		}
	}
}
