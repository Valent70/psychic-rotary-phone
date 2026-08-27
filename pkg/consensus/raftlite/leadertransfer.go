package raftlite

import (
	"context"
	"errors"
	"time"
)

// TimeoutNowArgs/Reply implement Raft's leadership-transfer extension:
// a leader that wants to hand off cleanly sends TimeoutNow to a
// fully-caught-up follower, which then skips the rest of its election
// wait and starts a real election immediately — winning it, in the
// common case, faster and with zero availability gap for the cluster.
type TimeoutNowArgs struct {
	Term     uint64
	LeaderID string
}

type TimeoutNowReply struct {
	Term     uint64
	Accepted bool
}

var (
	ErrUnknownTransferTarget     = errors.New("raftlite: transfer target is not a current cluster member")
	ErrTransferTargetNotCaughtUp = errors.New("raftlite: transfer target did not catch up to the leader's log within the deadline")
	ErrTransferRejected          = errors.New("raftlite: transfer target rejected TimeoutNow")
)

// HandleTimeoutNow is the RPC handler a follower runs on receipt. It
// performs NO persistent-state mutation of its own (other than a term
// bump if the leader's term is newer, via the existing
// becomeFollowerLocked path) — it only signals Run()'s election-wait
// loop to stop waiting and start a real election now.
func (n *Node) HandleTimeoutNow(args TimeoutNowArgs) TimeoutNowReply {
	n.mu.Lock()
	if args.Term < n.state.currentTerm {
		reply := TimeoutNowReply{Term: n.state.currentTerm, Accepted: false}
		n.mu.Unlock()
		return reply
	}
	if args.Term > n.state.currentTerm {
		n.becomeFollowerLocked(args.Term)
	}
	term := n.state.currentTerm
	n.mu.Unlock()

	select {
	case n.timeoutNowCh <- struct{}{}:
	default:
	}
	return TimeoutNowReply{Term: term, Accepted: true}
}

// TransferLeadership performs a graceful handoff to targetID: it first
// gives the target focused replication rounds (up to 1s) to fully catch
// up to the leader's last log index — a transfer to a lagging follower
// would either fail its election or, worse, win it while missing
// entries — then sends TimeoutNow and steps itself down to Follower so
// it stops contesting the term the target is about to claim.
//
// It does NOT bump this node's own term: only the target's own
// subsequent election does that, exactly as an organic election would.
func (n *Node) TransferLeadership(ctx context.Context, targetID string) error {
	n.mu.Lock()
	if n.role != Leader {
		n.mu.Unlock()
		return ErrNotLeader
	}
	found := false
	for _, p := range n.peers {
		if p == targetID {
			found = true
			break
		}
	}
	if !found {
		n.mu.Unlock()
		return ErrUnknownTransferTarget
	}
	term := n.state.currentTerm
	lastIdx, _ := n.lastLogInfoLocked()
	n.mu.Unlock()

	deadline := time.Now().Add(1 * time.Second)
	caughtUp := false
	for time.Now().Before(deadline) {
		n.replicateTo(ctx, targetID, term)
		n.mu.Lock()
		caughtUp = n.matchIndex[targetID] >= lastIdx
		stillLeader := n.role == Leader && n.state.currentTerm == term
		n.mu.Unlock()
		if !stillLeader {
			return ErrNotLeader
		}
		if caughtUp {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !caughtUp {
		n.incMetric("leadership_transfers_failed")
		return ErrTransferTargetNotCaughtUp
	}

	reply, err := n.trans.SendTimeoutNow(ctx, targetID, TimeoutNowArgs{Term: term, LeaderID: n.id})
	if err != nil || !reply.Accepted {
		n.incMetric("leadership_transfers_failed")
		return ErrTransferRejected
	}

	n.mu.Lock()
	if n.role == Leader && n.state.currentTerm == term {
		n.role = Follower
		// Deliberately leaderLive=false, NOT true: this node just gave
		// up leadership on purpose and must be immediately willing to
		// grant PreVote/RequestVote to targetID's upcoming election —
		// leaderLive=true would make HandlePreVote reject that election
		// outright (it means "I still believe MY leader is alive",
		// which is now false; there is no leader until targetID wins).
		// Found as a real bug via this file's own test: with
		// leaderLive=true here, the 3-node test cluster's transfer
		// target could only ever collect its own PreVote (the OTHER
		// follower is unaffected, but the just-stepped-down former
		// leader — a full cluster member — wrongly refused), leaving it
		// one vote short of the pre-vote quorum every time.
		n.leaderLive = false
	}
	n.mu.Unlock()
	n.incMetric("leadership_transfers")
	return nil
}
