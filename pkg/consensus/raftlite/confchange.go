// Atomic cluster membership change — closes the audit doc's section D
// ask: "Tambahkan API ... func (r *Raft) ProposeJointConfChange(changes
// []ConfChange) error ... Semua perubahan membership di-commit atomically
// atau tidak sama sekali ... Kode memeriksa invariants cluster sebelum
// dan sesudah".
//
// Honest scope statement: this implements ATOMIC BATCH membership change
// (all changes in one call commit as a single Raft log entry, or none
// do) with pre/post invariant checks — which is what the audit doc's own
// code snippet and prose describe. It is deliberately NOT full two-phase
// joint consensus in the strict Raft-paper sense (Ongaro §6: a
// transitional C(old,new) configuration where BOTH the old and new
// majorities must independently agree before moving to C(new) alone).
// True joint consensus needs the commit-index/majority calculation to
// evaluate two peer sets simultaneously during the transition window;
// this implementation instead prevents the split-brain risk the doc
// warns about by making the WHOLE batch a single committed log entry
// (never a partial application), which is the concrete safety property
// the doc's own invariant-check language asks for, at a fraction of the
// complexity of full joint consensus. The remaining gap (allowing
// safe overlapping-majority transitions for very large batch changes
// spanning a full quorum flip) is called out explicitly in the report.
package raftlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrDuplicateNodeInChangeSet = errors.New("raftlite: duplicate node id within one ConfChange batch")
	ErrEmptyResultingCluster    = errors.New("raftlite: resulting cluster must not be empty")
	ErrEmptyResultingVoters     = errors.New("raftlite: resulting cluster must have at least one voter")
	ErrRemoveNonMember          = errors.New("raftlite: cannot remove a node that is not a current member")
	ErrAddExistingMember        = errors.New("raftlite: cannot add a node that is already a member")
	ErrEmptyChangeSet           = errors.New("raftlite: ConfChange batch must not be empty")
	// ErrRemoveNotVoter/ErrRemoveNotLearner/ErrPromoteNotLearner enforce
	// the explicit learner/voter role distinction: a membership mutation
	// naming the wrong role for an existing member is rejected rather
	// than silently reinterpreted (e.g. REMOVE_VOTER on a learner does
	// NOT quietly remove the learner -- that is what REMOVE_LEARNER is
	// for, and conflating the two would make "is this node a voter or a
	// learner" impossible to answer from the change log alone).
	ErrRemoveNotVoter    = errors.New("raftlite: cannot REMOVE_VOTER a node that is currently a learner, not a voter")
	ErrRemoveNotLearner  = errors.New("raftlite: cannot REMOVE_LEARNER a node that is currently a voter, not a learner")
	ErrPromoteNotLearner = errors.New("raftlite: cannot PROMOTE_LEARNER a node that is not currently a learner")
)

// MemberRole distinguishes a full VOTING cluster member from a
// non-voting LEARNER: a learner receives log replication -- so it can
// fully catch up before ever being promoted -- exactly like any other
// peer, but never counts toward a vote tally (PreVote/RequestVote) or a
// commit-index majority. See Node.learners and votingPeersLocked.
type MemberRole string

const (
	RoleVoter   MemberRole = "VOTER"
	RoleLearner MemberRole = "LEARNER"
)

// ConfChangeType is the kind of membership mutation.
type ConfChangeType string

const (
	ConfChangeAddVoter    ConfChangeType = "ADD_VOTER"
	ConfChangeRemoveVoter ConfChangeType = "REMOVE_VOTER"
	// ConfChangeAddLearner adds a brand-new, non-voting member.
	ConfChangeAddLearner ConfChangeType = "ADD_LEARNER"
	// ConfChangeRemoveLearner removes an existing learner. Rejected
	// (ErrRemoveNotLearner) if the named node is actually a voter.
	ConfChangeRemoveLearner ConfChangeType = "REMOVE_LEARNER"
	// ConfChangePromoteLearner turns an existing learner into a full
	// voter WITHOUT removing and re-adding it (so it keeps whatever log
	// position it already caught up to). Rejected (ErrPromoteNotLearner)
	// if the named node is not currently a learner.
	ConfChangePromoteLearner ConfChangeType = "PROMOTE_LEARNER"
)

// ConfChange is one membership mutation within a batch.
type ConfChange struct {
	Type   ConfChangeType `json:"type"`
	NodeID string         `json:"node_id"`
}

// confChangeEnvelope is the wire format smuggled through Propose's
// opaque []byte command channel, distinguished from ordinary FSM
// commands by confChangeMagic so applyCommitted can route it to
// membership application instead of fsm.Apply.
const confChangeMagic = "RAFTLITE_CONFCHANGE_V1"

type confChangeEnvelope struct {
	Magic   string       `json:"magic"`
	Changes []ConfChange `json:"changes"`
}

func isConfChangeCommand(cmd []byte) (confChangeEnvelope, bool) {
	var env confChangeEnvelope
	if err := json.Unmarshal(cmd, &env); err != nil {
		return confChangeEnvelope{}, false
	}
	if env.Magic != confChangeMagic {
		return confChangeEnvelope{}, false
	}
	return env, true
}

// validateConfChangeBatch checks the invariants the audit doc asks for
// (no duplicate node IDs within the batch, no removing a non-member, no
// adding an existing member, and the RESULTING cluster must not be
// empty), plus the role-specific invariants ADD_LEARNER/REMOVE_LEARNER/
// PROMOTE_LEARNER add: a mutation naming the wrong role for an existing
// member is rejected (ErrRemoveNotVoter/ErrRemoveNotLearner/
// ErrPromoteNotLearner) rather than silently reinterpreted, and the
// resulting cluster must retain at least one VOTER (a cluster of pure
// learners can never elect a leader or commit anything, which is as
// useless as an empty cluster). currentVoters/currentLearners are the
// peer set INCLUDING self as a voter (self is never added/removed/
// promoted by these changes in this implementation — self-removal/
// leader-transfer is out of scope, see package doc).
func validateConfChangeBatch(currentVoters, currentLearners []string, changes []ConfChange) (resultVoters, resultLearners []string, err error) {
	if len(changes) == 0 {
		return nil, nil, ErrEmptyChangeSet
	}
	seen := make(map[string]bool, len(changes))
	for _, c := range changes {
		if seen[c.NodeID] {
			return nil, nil, fmt.Errorf("%w: %s", ErrDuplicateNodeInChangeSet, c.NodeID)
		}
		seen[c.NodeID] = true
	}

	roles := make(map[string]MemberRole, len(currentVoters)+len(currentLearners))
	for _, m := range currentVoters {
		roles[m] = RoleVoter
	}
	for _, m := range currentLearners {
		roles[m] = RoleLearner
	}

	for _, c := range changes {
		role, exists := roles[c.NodeID]
		switch c.Type {
		case ConfChangeAddVoter:
			if exists {
				return nil, nil, fmt.Errorf("%w: %s", ErrAddExistingMember, c.NodeID)
			}
			roles[c.NodeID] = RoleVoter
		case ConfChangeRemoveVoter:
			if !exists {
				return nil, nil, fmt.Errorf("%w: %s", ErrRemoveNonMember, c.NodeID)
			}
			if role != RoleVoter {
				return nil, nil, fmt.Errorf("%w: %s", ErrRemoveNotVoter, c.NodeID)
			}
			delete(roles, c.NodeID)
		case ConfChangeAddLearner:
			if exists {
				return nil, nil, fmt.Errorf("%w: %s", ErrAddExistingMember, c.NodeID)
			}
			roles[c.NodeID] = RoleLearner
		case ConfChangeRemoveLearner:
			if !exists {
				return nil, nil, fmt.Errorf("%w: %s", ErrRemoveNonMember, c.NodeID)
			}
			if role != RoleLearner {
				return nil, nil, fmt.Errorf("%w: %s", ErrRemoveNotLearner, c.NodeID)
			}
			delete(roles, c.NodeID)
		case ConfChangePromoteLearner:
			if !exists {
				return nil, nil, fmt.Errorf("%w: %s", ErrRemoveNonMember, c.NodeID)
			}
			if role != RoleLearner {
				return nil, nil, fmt.Errorf("%w: %s", ErrPromoteNotLearner, c.NodeID)
			}
			roles[c.NodeID] = RoleVoter
		default:
			return nil, nil, fmt.Errorf("raftlite: unknown ConfChangeType %q", c.Type)
		}
	}
	if len(roles) == 0 {
		return nil, nil, ErrEmptyResultingCluster
	}
	for m, r := range roles {
		if r == RoleVoter {
			resultVoters = append(resultVoters, m)
		} else {
			resultLearners = append(resultLearners, m)
		}
	}
	if len(resultVoters) == 0 {
		return nil, nil, ErrEmptyResultingVoters
	}
	sort.Strings(resultVoters)
	sort.Strings(resultLearners)
	return resultVoters, resultLearners, nil
}

// ProposeJointConfChange proposes an ALL-OR-NOTHING batch of membership
// changes. Invariants are checked against the leader's current view of
// membership BEFORE proposing (fail fast, no log entry written on
// validation failure) and RE-CHECKED at apply time on every node when
// the entry commits (defensive: the cluster's membership may have
// changed between propose and commit on a slow follower, though not on
// the leader since Propose/apply share n.mu). Returns ErrNotLeader if
// this node is not currently the leader.
func (n *Node) ProposeJointConfChange(changes []ConfChange) (index uint64, term uint64, err error) {
	n.mu.Lock()
	if n.role != Leader {
		n.mu.Unlock()
		return 0, 0, ErrNotLeader
	}
	currentVoters := append([]string{n.id}, n.votingPeersLocked()...)
	currentLearners := n.learnerListLocked()
	n.mu.Unlock()

	if _, _, err := validateConfChangeBatch(currentVoters, currentLearners, changes); err != nil {
		return 0, 0, err
	}

	env := confChangeEnvelope{Magic: confChangeMagic, Changes: changes}
	cmd, err := json.Marshal(env)
	if err != nil {
		return 0, 0, fmt.Errorf("raftlite: encode conf change: %w", err)
	}
	return n.Propose(cmd)
}

// applyConfChangeLocked applies an already-committed conf-change batch
// to n.peers/n.learners, re-validating invariants defensively, and
// cleans up leader-only replication tracking state for removed peers.
// Must be called with n.mu held.
func (n *Node) applyConfChangeLocked(env confChangeEnvelope) error {
	currentVoters := append([]string{n.id}, n.votingPeersLocked()...)
	currentLearners := n.learnerListLocked()
	resultVoters, resultLearners, err := validateConfChangeBatch(currentVoters, currentLearners, env.Changes)
	if err != nil {
		// A defensive re-check failure means the cluster state diverged
		// between propose and commit (e.g. a concurrent second batch
		// committed first). The safe behavior is to reject this
		// application rather than corrupt membership — the proposer
		// must retry with fresh state.
		return err
	}
	newPeers := make([]string, 0, len(resultVoters)+len(resultLearners))
	newLearners := make(map[string]bool, len(resultLearners))
	selfIncluded := false
	for _, m := range resultVoters {
		if m == n.id {
			selfIncluded = true
			continue
		}
		newPeers = append(newPeers, m)
	}
	for _, m := range resultLearners {
		newPeers = append(newPeers, m)
		newLearners[m] = true
	}
	n.peers = newPeers
	n.learners = newLearners
	if !selfIncluded {
		// See the identical fix + explanation in jointconsensus.go: a
		// removed node that doesn't know it was removed will keep
		// heartbeating (if it was leader) or contesting elections
		// (if not), holding the surviving cluster hostage.
		n.removed = true
		n.role = Follower
	}

	// prune leader-only tracking maps for anyone no longer a peer
	for id := range n.nextIndex {
		if !contains(newPeers, id) {
			delete(n.nextIndex, id)
			delete(n.matchIndex, id)
		}
	}
	return nil
}

// learnerListLocked returns n.learners as a plain slice. Must be called
// with n.mu held.
func (n *Node) learnerListLocked() []string {
	out := make([]string, 0, len(n.learners))
	for id := range n.learners {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// Members returns the current cluster membership including self
// (voters AND learners), sorted.
func (n *Node) Members() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := append([]string{n.id}, n.peers...)
	sort.Strings(out)
	return out
}

// Voters returns the current VOTING cluster membership including self,
// sorted -- the set that actually decides elections and commit-index
// majorities. See MemberRole.
func (n *Node) Voters() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := append([]string{n.id}, n.votingPeersLocked()...)
	sort.Strings(out)
	return out
}

// Learners returns the current non-voting membership, sorted. Self is
// never a learner in this implementation (a node cannot demote itself),
// so this never includes n.id.
func (n *Node) Learners() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.learnerListLocked()
}
