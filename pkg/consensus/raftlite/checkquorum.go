package raftlite

import "time"

// checkQuorumTick runs once per heartbeat interval from leaderLoop. It
// is the liveness half of leader safety: a leader that has NOT heard
// back (success or failure — any non-transport-error reply counts, see
// replicateTo) from a majority of the cluster within checkQuorumTimeout
// steps down to Follower rather than continuing to act as leader while
// possibly partitioned away from the rest of the cluster.
//
// This deliberately does NOT trigger on a single slow/lost heartbeat —
// only on sustained isolation from a majority across the whole timeout
// window — so it must not produce false positives under ordinary
// jitter or transient packet loss (see checkquorum_test.go, which
// proves both directions: no false-positive stepdown under partial
// loss, and a real stepdown under full isolation).
func (n *Node) checkQuorumTick() {
	n.mu.Lock()
	if !n.checkQuorumEnabled || n.role != Leader {
		n.mu.Unlock()
		return
	}
	// Only voters count toward the majority CheckQuorum needs -- a
	// leader that has heard from nothing but learners (non-voting by
	// definition) is exactly as isolated from real decision-making
	// authority as one that has heard from no one at all.
	voters := n.votingPeersLocked()
	quorumNeeded := (len(voters)+1)/2 + 1 // total voting membership incl. self
	contacted := 1                        // self always counts
	now := time.Now()
	for _, p := range voters {
		if last, ok := n.lastContact[p]; ok && now.Sub(last) <= n.checkQuorumTimeout {
			contacted++
		}
	}
	if contacted >= quorumNeeded {
		n.mu.Unlock()
		return
	}

	// Genuinely isolated from a majority: step down. Do NOT bump the
	// term (this node has no reason to believe a new term exists yet —
	// bumping here would just poison the term needlessly the moment
	// connectivity returns) and clear leaderLive so this node's own
	// election timer, once it fires, is free to grant PreVote/RequestVote
	// to a legitimate new candidate instead of guarding a leader (itself)
	// that no longer has quorum backing.
	n.role = Follower
	n.leaderLive = false
	n.incMetric("checkquorum_stepdowns")
	n.mu.Unlock()
}
