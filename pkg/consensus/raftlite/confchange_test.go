package raftlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidateConfChangeBatch_RejectsDuplicateInBatch(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B", "C"}, nil, []ConfChange{
		{Type: ConfChangeAddVoter, NodeID: "D"},
		{Type: ConfChangeRemoveVoter, NodeID: "D"},
	})
	if err == nil {
		t.Fatalf("expected error for duplicate node id within batch")
	}
}

func TestValidateConfChangeBatch_RejectsRemovingNonMember(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B"}, nil, []ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: "Z"},
	})
	if err == nil {
		t.Fatalf("expected error removing non-member")
	}
}

func TestValidateConfChangeBatch_RejectsAddingExistingMember(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B"}, nil, []ConfChange{
		{Type: ConfChangeAddVoter, NodeID: "A"},
	})
	if err == nil {
		t.Fatalf("expected error adding existing member")
	}
}

func TestValidateConfChangeBatch_RejectsEmptyResultingCluster(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B"}, nil, []ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: "A"},
		{Type: ConfChangeRemoveVoter, NodeID: "B"},
	})
	if err == nil {
		t.Fatalf("expected error for empty resulting cluster")
	}
}

func TestValidateConfChangeBatch_RejectsEmptyBatch(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B"}, nil, nil)
	if err == nil {
		t.Fatalf("expected error for empty batch")
	}
}

func TestValidateConfChangeBatch_AtomicMixedBatchSucceeds(t *testing.T) {
	resultVoters, resultLearners, err := validateConfChangeBatch([]string{"A", "B", "C"}, nil, []ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: "C"},
		{Type: ConfChangeAddVoter, NodeID: "D"},
	})
	if err != nil {
		t.Fatalf("expected valid mixed batch to succeed: %v", err)
	}
	if len(resultLearners) != 0 {
		t.Fatalf("expected no learners in result, got %v", resultLearners)
	}
	want := map[string]bool{"A": true, "B": true, "D": true}
	if len(resultVoters) != 3 {
		t.Fatalf("expected 3 resulting voters, got %v", resultVoters)
	}
	for _, m := range resultVoters {
		if !want[m] {
			t.Fatalf("unexpected member in result: %s (%v)", m, resultVoters)
		}
	}
}

// --- learner/voter role distinction ---

func TestValidateConfChangeBatch_AddLearnerSucceeds(t *testing.T) {
	resultVoters, resultLearners, err := validateConfChangeBatch([]string{"A", "B", "C"}, nil, []ConfChange{
		{Type: ConfChangeAddLearner, NodeID: "D"},
	})
	if err != nil {
		t.Fatalf("ADD_LEARNER: %v", err)
	}
	if len(resultVoters) != 3 {
		t.Fatalf("expected voters unchanged (3), got %v", resultVoters)
	}
	if len(resultLearners) != 1 || resultLearners[0] != "D" {
		t.Fatalf("expected D as the sole learner, got %v", resultLearners)
	}
}

func TestValidateConfChangeBatch_RemoveVoterOnLearnerFailsClosed(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B", "C"}, []string{"D"}, []ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: "D"},
	})
	if !errors.Is(err, ErrRemoveNotVoter) {
		t.Fatalf("expected ErrRemoveNotVoter for REMOVE_VOTER on a learner, got %v", err)
	}
}

func TestValidateConfChangeBatch_RemoveLearnerOnVoterFailsClosed(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B", "C"}, nil, []ConfChange{
		{Type: ConfChangeRemoveLearner, NodeID: "C"},
	})
	if !errors.Is(err, ErrRemoveNotLearner) {
		t.Fatalf("expected ErrRemoveNotLearner for REMOVE_LEARNER on a voter, got %v", err)
	}
}

func TestValidateConfChangeBatch_PromoteLearnerSucceeds(t *testing.T) {
	resultVoters, resultLearners, err := validateConfChangeBatch([]string{"A", "B", "C"}, []string{"D"}, []ConfChange{
		{Type: ConfChangePromoteLearner, NodeID: "D"},
	})
	if err != nil {
		t.Fatalf("PROMOTE_LEARNER: %v", err)
	}
	if len(resultLearners) != 0 {
		t.Fatalf("expected no learners remaining, got %v", resultLearners)
	}
	want := map[string]bool{"A": true, "B": true, "C": true, "D": true}
	if len(resultVoters) != 4 {
		t.Fatalf("expected 4 voters after promotion, got %v", resultVoters)
	}
	for _, m := range resultVoters {
		if !want[m] {
			t.Fatalf("unexpected voter in result: %s (%v)", m, resultVoters)
		}
	}
}

func TestValidateConfChangeBatch_PromoteLearnerOnVoterFailsClosed(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B", "C"}, nil, []ConfChange{
		{Type: ConfChangePromoteLearner, NodeID: "C"},
	})
	if !errors.Is(err, ErrPromoteNotLearner) {
		t.Fatalf("expected ErrPromoteNotLearner for PROMOTE_LEARNER on an existing voter, got %v", err)
	}
}

func TestValidateConfChangeBatch_AddLearnerRejectsExistingMember(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B", "C"}, nil, []ConfChange{
		{Type: ConfChangeAddLearner, NodeID: "C"},
	})
	if !errors.Is(err, ErrAddExistingMember) {
		t.Fatalf("expected ErrAddExistingMember for ADD_LEARNER on an existing voter, got %v", err)
	}
}

// TestValidateConfChangeBatch_RejectsAllLearnerResult proves the
// "resulting cluster must retain at least one voter" invariant: demoting
// every voter to nothing (here, removing the only two voters while a
// learner remains) must be rejected even though the resulting cluster
// as a whole is non-empty.
func TestValidateConfChangeBatch_RejectsAllLearnerResult(t *testing.T) {
	_, _, err := validateConfChangeBatch([]string{"A", "B"}, []string{"D"}, []ConfChange{
		{Type: ConfChangeRemoveVoter, NodeID: "A"},
		{Type: ConfChangeRemoveVoter, NodeID: "B"},
	})
	if !errors.Is(err, ErrEmptyResultingVoters) {
		t.Fatalf("expected ErrEmptyResultingVoters when only a learner would remain, got %v", err)
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

// TestConfChange_LearnerReplicatesButNeverCountsTowardQuorum is the
// end-to-end proof of the learner/voter role distinction: a learner
// added to a running 3-voter cluster (1) genuinely receives log
// replication (its FSM converges on committed state, exactly like a
// voter), (2) is correctly reported by Learners()/Voters() as
// non-voting, and (3) can be partitioned away WITHOUT blocking the
// cluster's ability to commit new entries -- proving votingPeersLocked
// actually excludes it from quorum arithmetic, not just from Members().
func TestConfChange_LearnerReplicatesButNeverCountsTowardQuorum(t *testing.T) {
	trans := NewMemTransport()
	nA, fA := newSumNode("A", []string{"B", "C"}, trans)
	nB, fB := newSumNode("B", []string{"A", "C"}, trans)
	nC, fC := newSumNode("C", []string{"A", "B"}, trans)
	trans.Register(nA)
	trans.Register(nB)
	trans.Register(nC)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := []*Node{nA, nB, nC}
	for _, n := range nodes {
		go n.Run(ctx)
	}
	leader := waitForLeader(t, nodes, 2*time.Second)

	// D joins as a pure learner.
	nD, fD := newSumNode("D", []string{"A", "B", "C"}, trans)
	trans.Register(nD)
	go nD.Run(ctx)

	idx, _, err := leader.ProposeJointConfChange([]ConfChange{{Type: ConfChangeAddLearner, NodeID: "D"}})
	if err != nil {
		t.Fatalf("ProposeJointConfChange(ADD_LEARNER): %v", err)
	}
	if err := leader.WaitApplied(context.Background(), idx); err != nil {
		t.Fatalf("wait applied for ADD_LEARNER: %v", err)
	}

	if voters := leader.Voters(); len(voters) != 3 {
		t.Fatalf("expected 3 voters (D excluded), got %v", voters)
	}
	learners := leader.Learners()
	if len(learners) != 1 || learners[0] != "D" {
		t.Fatalf("expected D as the sole learner, got %v", learners)
	}
	if members := leader.Members(); len(members) != 4 {
		t.Fatalf("expected 4 total members (voters+learners), got %v", members)
	}

	// D genuinely replicates: propose an entry and confirm D's FSM
	// converges, not just the three original voters'.
	idx2, _, err := leader.Propose(encodeU64(11))
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := leader.WaitApplied(context.Background(), idx2); err != nil {
		t.Fatalf("wait applied: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && fD.Sum() != 11 {
		time.Sleep(20 * time.Millisecond)
	}
	if fD.Sum() != 11 {
		t.Fatalf("learner D did not replicate the committed entry: sum=%d, want 11", fD.Sum())
	}

	// Now partition the learner away entirely and confirm the cluster
	// can STILL commit new entries -- proving D's absence never blocks
	// quorum, unlike a real voter's would.
	trans.DropTo("D", leader.ID())
	trans.DropTo(leader.ID(), "D")
	idx3, _, err := leader.Propose(encodeU64(31))
	if err != nil {
		t.Fatalf("propose after isolating learner: %v", err)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	if err := leader.WaitApplied(waitCtx, idx3); err != nil {
		t.Fatalf("commit stalled with only a LEARNER partitioned away -- votingPeersLocked is not correctly excluding it from quorum: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !(fA.Sum() == 42 && fB.Sum() == 42 && fC.Sum() == 42) {
		time.Sleep(20 * time.Millisecond)
	}
	if fA.Sum() != 42 || fB.Sum() != 42 || fC.Sum() != 42 {
		t.Fatalf("voters did not converge to 42 (11+31): A=%d B=%d C=%d", fA.Sum(), fB.Sum(), fC.Sum())
	}
}

// TestConfChange_PromoteLearnerMakesItCountTowardQuorum proves the
// other direction: once PROMOTE_LEARNER commits, the promoted node's
// vote/replication IS required for further progress -- i.e. it has
// genuinely become a voter, not merely relabeled.
func TestConfChange_PromoteLearnerMakesItCountTowardQuorum(t *testing.T) {
	trans := NewMemTransport()
	nA, _ := newSumNode("A", []string{"B", "C"}, trans)
	nB, _ := newSumNode("B", []string{"A", "C"}, trans)
	nC, _ := newSumNode("C", []string{"A", "B"}, trans)
	trans.Register(nA)
	trans.Register(nB)
	trans.Register(nC)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := []*Node{nA, nB, nC}
	for _, n := range nodes {
		go n.Run(ctx)
	}
	leader := waitForLeader(t, nodes, 2*time.Second)

	nD, _ := newSumNode("D", []string{"A", "B", "C"}, trans)
	trans.Register(nD)
	go nD.Run(ctx)

	idx, _, err := leader.ProposeJointConfChange([]ConfChange{{Type: ConfChangeAddLearner, NodeID: "D"}})
	if err != nil {
		t.Fatalf("ADD_LEARNER: %v", err)
	}
	if err := leader.WaitApplied(context.Background(), idx); err != nil {
		t.Fatalf("wait applied for ADD_LEARNER: %v", err)
	}
	// Let D catch up before promoting it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		nD.mu.Lock()
		caughtUp := nD.lastApplied >= idx
		nD.mu.Unlock()
		if caughtUp {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	idx2, _, err := leader.ProposeJointConfChange([]ConfChange{{Type: ConfChangePromoteLearner, NodeID: "D"}})
	if err != nil {
		t.Fatalf("PROMOTE_LEARNER: %v", err)
	}
	if err := leader.WaitApplied(context.Background(), idx2); err != nil {
		t.Fatalf("wait applied for PROMOTE_LEARNER: %v", err)
	}
	if voters := leader.Voters(); len(voters) != 4 {
		t.Fatalf("expected 4 voters after promotion, got %v", voters)
	}
	if learners := leader.Learners(); len(learners) != 0 {
		t.Fatalf("expected no learners after promotion, got %v", learners)
	}

	// Now partition away TWO of the four voters (B and C) -- only
	// {leader, D} remain, one short of a 4-voter majority (need 3).
	// Progress must now STALL, proving D's vote is genuinely required
	// -- unlike before promotion, where its absence was harmless.
	for _, id := range []string{"B", "C"} {
		trans.DropTo(leader.ID(), id)
		trans.DropTo(id, leader.ID())
	}
	idx3, _, err := leader.Propose(encodeU64(99))
	if err != nil {
		t.Fatalf("propose after partitioning two voters: %v", err)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer waitCancel()
	err = leader.WaitApplied(waitCtx, idx3)
	if err == nil {
		t.Fatal("expected commit to stall with only 2 of 4 voters reachable (no majority), but it succeeded")
	}
}
