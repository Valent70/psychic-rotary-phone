// node.go — the core Raft node implementation.
package raft

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Node is a single Raft participant.
type Node struct {
	mu sync.Mutex

	// identity
	id  NodeID
	cfg Config

	// persistent state (must be saved before responding to RPCs)
	currentTerm Term
	votedFor    NodeID

	// volatile state
	role        Role
	commitIndex Index
	lastApplied Index
	leadID      NodeID

	// leader-only volatile state
	nextIndex  map[NodeID]Index // next log index to send to each follower
	matchIndex map[NodeID]Index // highest replicated index for each follower

	// membership
	membership  MembershipConfig
	jointConfig *JointConfig // non-nil during a Joint Consensus change

	// snapshot state
	snapshotIndex Index
	snapshotTerm  Term

	// metrics counters (atomic)
	metrics nodeMetrics

	// control
	stopCh      chan struct{}
	stopped     atomic.Bool
	heartbeatTk *time.Ticker
	electionTk  *time.Ticker
	rng         *rand.Rand
}

type nodeMetrics struct {
	elections     atomic.Uint64
	heartbeats    atomic.Uint64
	proposals     atomic.Uint64
	snapshots     atomic.Uint64
	configChanges atomic.Uint64
}

// NewNode creates and starts a Raft node.
func NewNode(cfg Config) (*Node, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("raft: node ID must not be empty")
	}
	if cfg.LogStore == nil || cfg.StateStore == nil || cfg.FSM == nil || cfg.Transport == nil {
		return nil, fmt.Errorf("raft: LogStore, StateStore, FSM and Transport are required")
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = 150 * time.Millisecond
	}
	if cfg.ElectionTimeout == 0 {
		cfg.ElectionTimeout = 300 * time.Millisecond
	}
	if cfg.MaxAppendEntries == 0 {
		cfg.MaxAppendEntries = 100
	}

	n := &Node{
		id:         cfg.ID,
		cfg:        cfg,
		role:       Follower,
		membership: cfg.InitialConfig,
		nextIndex:  make(map[NodeID]Index),
		matchIndex: make(map[NodeID]Index),
		stopCh:     make(chan struct{}),
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	// Recover durable state.
	term, voted, err := cfg.StateStore.LoadState()
	if err != nil {
		return nil, fmt.Errorf("raft: load state: %w", err)
	}
	n.currentTerm = term
	n.votedFor = voted

	go n.run()
	return n, nil
}

// ─── public API ───────────────────────────────────────────────────────────────

// Propose submits a command for replication. Only succeeds if this node is Leader.
func (n *Node) Propose(payload []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.role != Leader {
		return fmt.Errorf("raft: not leader (current leader: %s)", n.leadID)
	}
	n.metrics.proposals.Add(1)
	return n.appendEntry(EntryNormal, payload)
}

// ProposeConfig initiates a membership change via Joint Consensus.
func (n *Node) ProposeConfig(newCfg MembershipConfig) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.role != Leader {
		return fmt.Errorf("raft: not leader")
	}
	if n.jointConfig != nil && n.jointConfig.IsJoint() {
		return fmt.Errorf("raft: membership change already in progress")
	}
	n.jointConfig = &JointConfig{Old: n.membership, New: newCfg}
	n.metrics.configChanges.Add(1)
	cfg, err := encodeConfig(*n.jointConfig)
	if err != nil {
		return err
	}
	return n.appendEntry(EntryConfig, cfg)
}

// TransferLeadership initiates a leader transfer to the given target node.
// The current leader steps down after the target catches up.
func (n *Node) TransferLeadership(target NodeID) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.role != Leader {
		return fmt.Errorf("raft: not leader")
	}
	if !n.membership.IsVoter(target) {
		return fmt.Errorf("raft: %s is not a voter", target)
	}
	// Step down: become Follower so target can win election.
	n.becomeFollower(n.currentTerm)
	return nil
}

// State returns a snapshot of the node's public state.
func (n *Node) State() NodeState {
	n.mu.Lock()
	defer n.mu.Unlock()
	return NodeState{
		ID:          n.id,
		Role:        n.role,
		Term:        n.currentTerm,
		CommitIndex: n.commitIndex,
		LeaderID:    n.leadID,
	}
}

// Stop shuts down the node.
func (n *Node) Stop() {
	if n.stopped.CompareAndSwap(false, true) {
		close(n.stopCh)
	}
}

// NodeState is a snapshot of a node's public state for introspection.
type NodeState struct {
	ID          NodeID
	Role        Role
	Term        Term
	CommitIndex Index
	LeaderID    NodeID
}

// ─── RPC handlers (called by SimTransport or gRPC server) ────────────────────

// HandleRequestVote processes a RequestVote RPC.
func (n *Node) HandleRequestVote(req RequestVoteRequest) (RequestVoteResponse, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := RequestVoteResponse{Term: n.currentTerm, VoterID: n.id}

	if req.Term < n.currentTerm {
		return resp, nil
	}
	if req.Term > n.currentTerm && !req.PreVote {
		n.becomeFollower(req.Term)
	}

	// PreVote: don't update term or votedFor, just check eligibility.
	if req.PreVote {
		resp.VoteGranted = n.logUpToDate(req.LastLogIndex, req.LastLogTerm)
		return resp, nil
	}

	// Leader stickiness: if we heard from a leader recently, decline.
	if n.leadID != "" && n.leadID != req.CandidateID {
		return resp, nil
	}

	canVote := (n.votedFor == "" || n.votedFor == req.CandidateID) &&
		n.logUpToDate(req.LastLogIndex, req.LastLogTerm)

	if canVote {
		n.votedFor = req.CandidateID
		_ = n.cfg.StateStore.SaveState(n.currentTerm, n.votedFor)
		resp.VoteGranted = true
	}
	return resp, nil
}

// HandleAppendEntries processes an AppendEntries RPC (heartbeat or replication).
func (n *Node) HandleAppendEntries(req AppendEntriesRequest) (AppendEntriesResponse, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := AppendEntriesResponse{
		Term:       n.currentTerm,
		FollowerID: n.id,
	}

	if req.Term < n.currentTerm {
		return resp, nil
	}
	if req.Term >= n.currentTerm {
		n.becomeFollower(req.Term)
		n.leadID = req.LeaderID
	}

	// Consistency check.
	if req.PrevLogIndex > 0 {
		prevEntry, err := n.cfg.LogStore.GetEntry(req.PrevLogIndex)
		if err != nil || prevEntry.Term != req.PrevLogTerm {
			// Fast backtrack.
			if err == nil {
				resp.ConflictTerm = prevEntry.Term
				resp.ConflictIndex = n.firstIndexForTerm(prevEntry.Term)
			} else {
				resp.ConflictIndex = n.cfg.LogStore.LastIndex() + 1
			}
			return resp, nil
		}
	}

	// Append new entries.
	if len(req.Entries) > 0 {
		if err := n.cfg.LogStore.AppendEntries(req.Entries); err != nil {
			return resp, err
		}
	}

	// Advance commit index.
	if req.LeaderCommit > n.commitIndex {
		lastNew := n.cfg.LogStore.LastIndex()
		n.commitIndex = min(req.LeaderCommit, lastNew)
		n.applyCommitted()
	}

	resp.Success = true
	resp.MatchIndex = n.cfg.LogStore.LastIndex()
	return resp, nil
}

// HandleInstallSnapshot processes an InstallSnapshot RPC.
func (n *Node) HandleInstallSnapshot(req InstallSnapshotRequest) (InstallSnapshotResponse, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := InstallSnapshotResponse{Term: n.currentTerm, FollowerID: n.id}

	if req.Term < n.currentTerm {
		return resp, nil
	}
	n.becomeFollower(req.Term)
	n.leadID = req.LeaderID

	// Install snapshot.
	if err := n.cfg.FSM.Restore(req.Data); err != nil {
		return resp, fmt.Errorf("raft: install snapshot: %w", err)
	}
	n.snapshotIndex = req.LastIncludedIndex
	n.snapshotTerm = req.LastIncludedTerm
	n.commitIndex = req.LastIncludedIndex
	n.lastApplied = req.LastIncludedIndex
	_ = n.cfg.LogStore.DeleteFrom(0) // truncate entire log
	n.metrics.snapshots.Add(1)

	resp.Success = true
	return resp, nil
}

// ─── internal ─────────────────────────────────────────────────────────────────

func (n *Node) run() {
	n.mu.Lock()
	ht := n.cfg.HeartbeatTimeout
	et := n.cfg.ElectionTimeout
	n.mu.Unlock()

	n.heartbeatTk = time.NewTicker(ht)
	n.electionTk = time.NewTicker(n.randomElectionTimeout(et))
	defer n.heartbeatTk.Stop()
	defer n.electionTk.Stop()

	for {
		select {
		case <-n.stopCh:
			n.mu.Lock()
			n.role = Dead
			n.mu.Unlock()
			return
		case <-n.heartbeatTk.C:
			n.mu.Lock()
			if n.role == Leader {
				n.metrics.heartbeats.Add(1)
				n.replicateToAll()
			}
			n.mu.Unlock()
		case <-n.electionTk.C:
			n.mu.Lock()
			if n.role != Leader {
				n.startElection()
			}
			n.mu.Unlock()
			n.electionTk.Reset(n.randomElectionTimeout(et))
		}
	}
}

func (n *Node) randomElectionTimeout(base time.Duration) time.Duration {
	extra := time.Duration(n.rng.Int63n(int64(base)))
	return base + extra
}

func (n *Node) startElection() {
	// PreVote round: check eligibility without incrementing term.
	if !n.runPreVote() {
		return
	}

	n.currentTerm++
	n.votedFor = n.id
	_ = n.cfg.StateStore.SaveState(n.currentTerm, n.votedFor)
	n.role = Candidate
	n.leadID = ""
	n.metrics.elections.Add(1)

	lastIdx := n.cfg.LogStore.LastIndex()
	lastTerm := n.termForIndex(lastIdx)
	req := RequestVoteRequest{
		Term:         n.currentTerm,
		CandidateID:  n.id,
		LastLogIndex: lastIdx,
		LastLogTerm:  lastTerm,
	}

	granted := map[NodeID]bool{n.id: true}

	n.mu.Unlock()
	defer n.mu.Lock()

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, peer := range n.membership.Voters {
		if peer == n.id {
			continue
		}
		wg.Add(1)
		go func(peer NodeID) {
			defer wg.Done()
			resp, err := n.cfg.Transport.SendRequestVote(peer, req)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if resp.VoteGranted {
				granted[peer] = true
			}
		}(peer)
	}
	wg.Wait()

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.role != Candidate || n.currentTerm != req.Term {
		return // state changed during vote collection
	}

	var won bool
	if n.jointConfig != nil && n.jointConfig.IsJoint() {
		won = n.jointConfig.Quorum(granted)
	} else {
		won = majority(n.membership.Voters, granted)
	}
	if won {
		n.becomeLeader()
	}
}

func (n *Node) runPreVote() bool {
	lastIdx := n.cfg.LogStore.LastIndex()
	lastTerm := n.termForIndex(lastIdx)
	req := RequestVoteRequest{
		Term:         n.currentTerm + 1, // prospective term
		CandidateID:  n.id,
		LastLogIndex: lastIdx,
		LastLogTerm:  lastTerm,
		PreVote:      true,
	}

	granted := map[NodeID]bool{n.id: true}

	n.mu.Unlock()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, peer := range n.membership.Voters {
		if peer == n.id {
			continue
		}
		wg.Add(1)
		go func(peer NodeID) {
			defer wg.Done()
			resp, err := n.cfg.Transport.SendRequestVote(peer, req)
			if err != nil {
				return
			}
			mu.Lock()
			if resp.VoteGranted {
				granted[peer] = true
			}
			mu.Unlock()
		}(peer)
	}
	wg.Wait()
	n.mu.Lock()
	return majority(n.membership.Voters, granted)
}

func (n *Node) becomeLeader() {
	n.role = Leader
	n.leadID = n.id

	// Initialise leader replication state.
	nextIdx := n.cfg.LogStore.LastIndex() + 1
	for _, peer := range n.membership.Voters {
		n.nextIndex[peer] = nextIdx
		n.matchIndex[peer] = 0
	}

	// Append a barrier entry to confirm leadership.
	_ = n.appendEntry(EntryBarrier, nil)
	n.replicateToAll()
}

func (n *Node) becomeFollower(term Term) {
	n.role = Follower
	n.currentTerm = term
	n.votedFor = ""
	_ = n.cfg.StateStore.SaveState(n.currentTerm, "")
}

func (n *Node) appendEntry(t EntryType, payload []byte) error {
	idx := n.cfg.LogStore.LastIndex() + 1
	e := Entry{Index: idx, Term: n.currentTerm, Type: t, Payload: payload}
	return n.cfg.LogStore.AppendEntries([]Entry{e})
}

func (n *Node) replicateToAll() {
	for _, peer := range n.membership.Voters {
		if peer == n.id {
			continue
		}
		go n.replicateTo(peer)
	}
	for _, peer := range n.membership.Learners {
		go n.replicateTo(peer)
	}
	n.advanceCommitIndex()
}

func (n *Node) replicateTo(peer NodeID) {
	n.mu.Lock()
	if n.role != Leader {
		n.mu.Unlock()
		return
	}
	nextIdx := n.nextIndex[peer]
	prevIdx := nextIdx - 1
	prevTerm := n.termForIndex(prevIdx)
	lastIdx := n.cfg.LogStore.LastIndex()

	var entries []Entry
	for i := nextIdx; i <= lastIdx && len(entries) < n.cfg.MaxAppendEntries; i++ {
		e, err := n.cfg.LogStore.GetEntry(i)
		if err != nil {
			break
		}
		entries = append(entries, e)
	}
	req := AppendEntriesRequest{
		Term:         n.currentTerm,
		LeaderID:     n.id,
		PrevLogIndex: prevIdx,
		PrevLogTerm:  prevTerm,
		Entries:      entries,
		LeaderCommit: n.commitIndex,
	}
	n.mu.Unlock()

	resp, err := n.cfg.Transport.SendAppendEntries(peer, req)
	if err != nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	if resp.Term > n.currentTerm {
		n.becomeFollower(resp.Term)
		return
	}
	if n.role != Leader {
		return
	}
	if resp.Success {
		if resp.MatchIndex > n.matchIndex[peer] {
			n.matchIndex[peer] = resp.MatchIndex
			n.nextIndex[peer] = resp.MatchIndex + 1
		}
		n.advanceCommitIndex()
	} else {
		// Fast backtrack.
		if resp.ConflictTerm > 0 {
			last := n.lastIndexForTerm(resp.ConflictTerm)
			if last > 0 {
				n.nextIndex[peer] = last + 1
			} else {
				n.nextIndex[peer] = resp.ConflictIndex
			}
		} else {
			n.nextIndex[peer] = resp.ConflictIndex
		}
		if n.nextIndex[peer] < 1 {
			n.nextIndex[peer] = 1
		}
	}
}

// advanceCommitIndex advances commitIndex to the highest index replicated
// on a majority of voters, for the current term only (§5.4.2).
func (n *Node) advanceCommitIndex() {
	if n.role != Leader {
		return
	}
	last := n.cfg.LogStore.LastIndex()
	for i := last; i > n.commitIndex; i-- {
		e, err := n.cfg.LogStore.GetEntry(i)
		if err != nil || e.Term != n.currentTerm {
			continue
		}
		votes := 1 // self
		for _, peer := range n.membership.Voters {
			if peer != n.id && n.matchIndex[peer] >= i {
				votes++
			}
		}
		if votes >= n.membership.Quorum() {
			n.commitIndex = i
			n.applyCommitted()
			break
		}
	}
}

// applyCommitted applies all committed-but-not-yet-applied entries to the FSM.
func (n *Node) applyCommitted() {
	for n.lastApplied < n.commitIndex {
		n.lastApplied++
		e, err := n.cfg.LogStore.GetEntry(n.lastApplied)
		if err != nil {
			return
		}
		switch e.Type {
		case EntryConfig:
			n.applyConfigEntry(e)
		case EntryNormal:
			_ = n.cfg.FSM.Apply(e)
		}
	}
}

func (n *Node) applyConfigEntry(e Entry) {
	joint, err := decodeConfig(e.Payload)
	if err != nil {
		return
	}
	if !joint.IsJoint() {
		return
	}
	// If we just applied the joint config, transition to new config.
	n.membership = joint.New
	n.jointConfig = nil
}

func (n *Node) logUpToDate(candLastIdx Index, candLastTerm Term) bool {
	myLastIdx := n.cfg.LogStore.LastIndex()
	myLastTerm := n.termForIndex(myLastIdx)
	if candLastTerm != myLastTerm {
		return candLastTerm > myLastTerm
	}
	return candLastIdx >= myLastIdx
}

func (n *Node) termForIndex(idx Index) Term {
	if idx == 0 {
		return 0
	}
	e, err := n.cfg.LogStore.GetEntry(idx)
	if err != nil {
		return 0
	}
	return e.Term
}

func (n *Node) firstIndexForTerm(t Term) Index {
	last := n.cfg.LogStore.LastIndex()
	for i := last; i >= 1; i-- {
		e, err := n.cfg.LogStore.GetEntry(i)
		if err != nil || e.Term < t {
			return i + 1
		}
	}
	return 1
}

func (n *Node) lastIndexForTerm(t Term) Index {
	last := n.cfg.LogStore.LastIndex()
	for i := last; i >= 1; i-- {
		e, err := n.cfg.LogStore.GetEntry(i)
		if err != nil {
			continue
		}
		if e.Term == t {
			return i
		}
	}
	return 0
}

// ─── config encoding ──────────────────────────────────────────────────────────

func encodeConfig(j JointConfig) ([]byte, error) { return json.Marshal(j) }
func decodeConfig(b []byte) (JointConfig, error) {
	var j JointConfig
	return j, json.Unmarshal(b, &j)
}

func min(a, b Index) Index {
	if a < b {
		return a
	}
	return b
}
