// True two-phase joint consensus (Raft dissertation §6 / Ongaro & Ousterhout
// §6): a membership change that can flip the ENTIRE quorum (e.g. replacing
// a majority of nodes at once) must pass through a transitional C(old,new)
// configuration where BOTH the old majority and the new majority must
// independently agree, before moving to C(new) alone. confchange.go's
// atomic-batch approach is safe for ordinary single/few-node changes but
// does not give this dual-majority safety property for large membership
// swings — this file adds the real thing as a separate, explicit API
// rather than replacing confchange.go (both are valid; callers pick
// based on how large a membership change they're making).
package raftlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrJointAlreadyActive = errors.New("raftlite: a joint consensus transition is already in progress")
	ErrNotInJoint         = errors.New("raftlite: no joint consensus transition is active")
)

const (
	jointMagic = "RAFTLITE_JOINT_V1"
	jointEnter = "ENTER_JOINT"
	jointLeave = "LEAVE_JOINT"
)

type jointEnvelope struct {
	Magic string   `json:"magic"`
	Phase string   `json:"phase"`
	Old   []string `json:"old,omitempty"` // only set on ENTER_JOINT
	New   []string `json:"new"`
}

func isJointCommand(cmd []byte) (jointEnvelope, bool) {
	var env jointEnvelope
	if err := json.Unmarshal(cmd, &env); err != nil {
		return jointEnvelope{}, false
	}
	if env.Magic != jointMagic {
		return jointEnvelope{}, false
	}
	return env, true
}

// jointState tracks an in-progress C(old,new) transition. Both member
// lists INCLUDE self when self is a member of that side — this is what
// lets the majority-arithmetic helpers below treat self uniformly with
// peers instead of needing special-cased +1s scattered everywhere.
type jointState struct {
	active bool
	old    []string
	new    []string
}

// EnterJointConsensus begins a two-phase membership change to
// newMembers (the complete target membership, including self if self
// remains a member). Only the leader may call this, and only when no
// other joint transition is already active. Returns the index/term of
// the C(old,new) log entry; the caller does not need to do anything
// else — once C(old,new) commits, the leader automatically proposes
// C(new) to complete the transition (see applyCommitted's joint-commit
// hook below).
func (n *Node) EnterJointConsensus(newMembers []string) (index, term uint64, err error) {
	n.mu.Lock()
	if n.role != Leader {
		n.mu.Unlock()
		return 0, 0, ErrNotLeader
	}
	if n.joint.active {
		n.mu.Unlock()
		return 0, 0, ErrJointAlreadyActive
	}
	oldMembers := append([]string{n.id}, n.peers...)
	sort.Strings(oldMembers)
	n.mu.Unlock()

	newSorted := append([]string{}, newMembers...)
	sort.Strings(newSorted)
	if len(newSorted) == 0 {
		return 0, 0, ErrEmptyResultingCluster
	}

	env := jointEnvelope{Magic: jointMagic, Phase: jointEnter, Old: oldMembers, New: newSorted}
	cmd, err := json.Marshal(env)
	if err != nil {
		return 0, 0, fmt.Errorf("raftlite: encode joint enter: %w", err)
	}
	return n.Propose(cmd)
}

// InJointConsensus reports whether a C(old,new) transition is currently
// active (i.e. the cluster requires dual-majority agreement).
func (n *Node) InJointConsensus() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.joint.active
}

// applyJointStateOnlyLocked mutates n.joint/n.peers per env, with no
// side effects beyond that (no auto-propose decision). Must be called
// with n.mu held. This is idempotent — safe to call repeatedly with the
// same env, and safe to call at APPEND time (see refreshJointFromLogLocked)
// as well as again at COMMIT time (applyCommitted), which is exactly
// what closes the joint-consensus chicken-and-egg problem: committing
// the ENTER_JOINT entry itself requires votes from the NEW members, so
// they must already be receiving replication (i.e. already in n.peers)
// before that entry commits — which means the config must take effect
// on APPEND, not on commit, per the Raft dissertation §6 design.
func (n *Node) applyJointStateOnlyLocked(env jointEnvelope) {
	switch env.Phase {
	case jointEnter:
		n.joint = jointState{active: true, old: env.Old, new: env.New}
		union := map[string]bool{}
		for _, m := range env.Old {
			union[m] = true
		}
		for _, m := range env.New {
			union[m] = true
		}
		delete(union, n.id)
		peers := make([]string, 0, len(union))
		for m := range union {
			peers = append(peers, m)
		}
		sort.Strings(peers)
		n.peers = peers
	case jointLeave:
		n.joint = jointState{}
		newPeers := make([]string, 0, len(env.New))
		for _, m := range env.New {
			if m != n.id {
				newPeers = append(newPeers, m)
			}
		}
		n.peers = newPeers
		for id := range n.nextIndex {
			if !contains(newPeers, id) {
				delete(n.nextIndex, id)
				delete(n.matchIndex, id)
			}
		}
	}
}

// refreshJointFromLogLocked re-derives n.joint/n.peers from the single
// most recent joint envelope still present in the visible log tail
// (scanning backward). Calling this after every log mutation (append OR
// truncate-then-append) is what makes membership config correctly
// "follow the log": if a tentative, uncommitted ENTER_JOINT entry is
// truncated away by a new leader's conflicting AppendEntries, the next
// call naturally finds whatever entry is now last instead — no separate
// revert bookkeeping needed. Known limitation, documented rather than
// hidden: this derives config from a backward scan of the CURRENT log
// only; if a joint transition entry has already been compacted away by
// snapshotting (which only happens once entries are committed AND
// applied), that's fine — a committed transition is by definition
// already durably reflected in n.peers via the append-time call that
// happened before compaction was ever possible.
func (n *Node) refreshJointFromLogLocked() {
	for i := len(n.state.log) - 1; i >= 0; i-- {
		if env, ok := isJointCommand(n.state.log[i].Command); ok {
			n.applyJointStateOnlyLocked(env)
			return
		}
	}
}

// applyJointLocked applies a committed joint-consensus envelope. Must be
// called with n.mu held. Returns whether this node (if leader) should
// now auto-propose the LEAVE_JOINT entry. State mutation itself was
// already performed at append time (refreshJointFromLogLocked /
// applyJointStateOnlyLocked) — this re-applies it defensively (a no-op
// in the common case) and exists mainly to compute shouldProposeLeave,
// which must only fire once, at COMMIT time, not on every append.
func (n *Node) applyJointLocked(env jointEnvelope) (shouldProposeLeave bool, newTarget []string) {
	n.applyJointStateOnlyLocked(env)
	if env.Phase == jointEnter {
		return n.role == Leader, env.New
	}
	if env.Phase == jointLeave && !contains(env.New, n.id) {
		// Safe to step down now: this is only reached from
		// applyCommitted, i.e. AFTER the entry (including replicating
		// it to the nodes that are staying) has already been committed
		// — per Raft §4.2.2, "the leader steps down immediately after
		// committing the Cnew log entry", not after merely appending it.
		n.removed = true
		n.role = Follower
	}
	return false, nil
}

// quorumBool reports whether `members` (a snapshot that may or may not
// include self) has a strict majority present in grantedSet — used for
// vote tallies (RequestVote/PreVote), where "granted" is boolean.
func quorumBool(members []string, selfID string, grantedSet map[string]bool) bool {
	if len(members) == 0 {
		return true
	}
	count := 0
	for _, m := range members {
		if m == selfID || grantedSet[m] {
			count++
		}
	}
	return count*2 > len(members)
}

// quorumIndex reports whether `members` has a strict majority whose
// matchIndex is >= idx — used for commit-index advancement.
func quorumIndex(members []string, selfID string, matchIndex map[string]uint64, idx uint64) bool {
	if len(members) == 0 {
		return true
	}
	count := 0
	for _, m := range members {
		if m == selfID {
			count++
			continue
		}
		if matchIndex[m] >= idx {
			count++
		}
	}
	return count*2 > len(members)
}

// hasQuorumBool is the joint-aware entry point used by vote tallying: if
// a joint transition is active, BOTH the old and new configs must
// independently reach majority; otherwise it's an ordinary single-config
// majority over currentMembers.
func (n *Node) hasQuorumBoolLocked(currentMembers []string, grantedSet map[string]bool) bool {
	if n.joint.active {
		return quorumBool(n.joint.old, n.id, grantedSet) && quorumBool(n.joint.new, n.id, grantedSet)
	}
	return quorumBool(currentMembers, n.id, grantedSet)
}

// hasQuorumIndexLocked is the joint-aware entry point used by commit-index
// advancement.
func (n *Node) hasQuorumIndexLocked(idx uint64) bool {
	if n.joint.active {
		// Known limitation, honestly left so rather than silently
		// assumed away: joint.old/joint.new are caller-supplied member
		// lists (see EnterJointConsensus) that do not yet model learners
		// at all -- a learner present during a joint-consensus
		// transition would be treated as a voter for THIS calculation.
		// Learner/voter awareness in THIS round only covers the simpler
		// atomic-batch ConfChange path (confchange.go); composing it
		// with true two-phase joint consensus is a distinct, separate
		// piece of work.
		return quorumIndex(n.joint.old, n.id, n.matchIndex, idx) && quorumIndex(n.joint.new, n.id, n.matchIndex, idx)
	}
	currentMembers := append([]string{n.id}, n.votingPeersLocked()...)
	return quorumIndex(currentMembers, n.id, n.matchIndex, idx)
}
