// Package raft implements a production-grade Raft consensus protocol.
// Gaps filled from audit:
//   - Real WAL integration (LogStore interface backed by pkg/storage/wal)
//   - Snapshot: create / install / streaming / incremental / restore / checksum
//   - Membership change: Joint Consensus (§4 of the Raft paper)
//   - Leader transfer
//   - gRPC transport interface (RPC abstraction over network)
//   - Read-index, commit-index, applied-index tracking
package raft

import (
	"time"

	"veriqo/pkg/core"
)

// ─── Core types ───────────────────────────────────────────────────────────────

// NodeID is an alias for the cluster node identity.
type NodeID = core.NodeID

// Term is the Raft election term.
type Term uint64

// Index is the Raft log index (1-based; 0 means "none").
type Index uint64

// Role is the current Raft role of a node.
type Role byte

const (
	Follower  Role = iota
	Candidate Role = iota
	Leader    Role = iota
	Dead      Role = iota
)

func (r Role) String() string {
	return [...]string{"Follower", "Candidate", "Leader", "Dead"}[r]
}

// ─── Log entries ──────────────────────────────────────────────────────────────

// EntryType classifies a log entry.
type EntryType byte

const (
	EntryNormal  EntryType = iota // application command
	EntryConfig  EntryType = iota // membership configuration change
	EntryBarrier EntryType = iota // no-op barrier for leader confirmation
)

// Entry is a single Raft log record.
type Entry struct {
	Index   Index
	Term    Term
	Type    EntryType
	Payload []byte // serialised command or config change
}

// ─── Membership ───────────────────────────────────────────────────────────────

// MembershipConfig is a cluster membership configuration.
type MembershipConfig struct {
	Voters   []NodeID // voting members
	Learners []NodeID // non-voting, catch-up members
}

// IsVoter returns true if id is a voting member.
func (c MembershipConfig) IsVoter(id NodeID) bool {
	for _, v := range c.Voters {
		if v == id {
			return true
		}
	}
	return false
}

// Quorum returns the minimum number of positive votes for a majority.
func (c MembershipConfig) Quorum() int { return len(c.Voters)/2 + 1 }

// JointConfig represents the transient configuration during a Joint Consensus
// membership change. Both old and new configs must reach quorum independently.
type JointConfig struct {
	Old MembershipConfig
	New MembershipConfig
}

// IsJoint returns true if we're in a Joint Consensus transition.
func (j *JointConfig) IsJoint() bool { return len(j.New.Voters) > 0 }

// Quorum returns true if votes satisfy majority in both old and new configs.
func (j *JointConfig) Quorum(granted map[NodeID]bool) bool {
	return majority(j.Old.Voters, granted) && majority(j.New.Voters, granted)
}

func majority(voters []NodeID, granted map[NodeID]bool) bool {
	count := 0
	for _, v := range voters {
		if granted[v] {
			count++
		}
	}
	return count >= len(voters)/2+1
}

// ─── RPC messages ─────────────────────────────────────────────────────────────

// RequestVoteRequest is sent by a Candidate to solicit votes.
type RequestVoteRequest struct {
	Term         Term
	CandidateID  NodeID
	LastLogIndex Index
	LastLogTerm  Term
	PreVote      bool // true → PreVote round, does not increment term
}

// RequestVoteResponse is the reply to a RequestVoteRequest.
type RequestVoteResponse struct {
	Term        Term
	VoteGranted bool
	VoterID     NodeID
}

// AppendEntriesRequest is sent by the Leader to replicate entries and heartbeat.
type AppendEntriesRequest struct {
	Term         Term
	LeaderID     NodeID
	PrevLogIndex Index
	PrevLogTerm  Term
	Entries      []Entry
	LeaderCommit Index
}

// AppendEntriesResponse is the reply to an AppendEntriesRequest.
type AppendEntriesResponse struct {
	Term       Term
	Success    bool
	FollowerID NodeID
	// Fast backtrack fields (leader uses these to find conflicting entry quickly)
	ConflictTerm  Term
	ConflictIndex Index
	MatchIndex    Index
}

// InstallSnapshotRequest is sent by the Leader to install a snapshot on a lagging follower.
type InstallSnapshotRequest struct {
	Term              Term
	LeaderID          NodeID
	LastIncludedIndex Index
	LastIncludedTerm  Term
	Offset            int64
	Data              []byte
	Done              bool
	Checksum          [32]byte
}

// InstallSnapshotResponse is the reply to an InstallSnapshotRequest.
type InstallSnapshotResponse struct {
	Term       Term
	FollowerID NodeID
	Success    bool
}

// ─── Transport ────────────────────────────────────────────────────────────────

// Transport is the network abstraction for sending Raft RPCs.
// A real gRPC implementation satisfies this interface; tests use SimTransport.
type Transport interface {
	// SendRequestVote sends a RequestVote RPC to target.
	SendRequestVote(target NodeID, req RequestVoteRequest) (RequestVoteResponse, error)
	// SendAppendEntries sends an AppendEntries RPC to target.
	SendAppendEntries(target NodeID, req AppendEntriesRequest) (AppendEntriesResponse, error)
	// SendInstallSnapshot sends an InstallSnapshot RPC to target.
	SendInstallSnapshot(target NodeID, req InstallSnapshotRequest) (InstallSnapshotResponse, error)
}

// ─── Log store interface ───────────────────────────────────────────────────────

// LogStore persists Raft log entries.
// The in-memory store is used for tests; production wires pkg/storage/wal.
type LogStore interface {
	// FirstIndex returns the lowest index in the log (0 if empty).
	FirstIndex() Index
	// LastIndex returns the highest index in the log (0 if empty).
	LastIndex() Index
	// GetEntry returns the entry at index i.
	GetEntry(i Index) (Entry, error)
	// AppendEntries appends entries to the log, truncating any conflicting suffix first.
	AppendEntries(entries []Entry) error
	// DeleteFrom removes all entries with Index >= from.
	DeleteFrom(from Index) error
}

// ─── State store interface ─────────────────────────────────────────────────────

// StateStore persists Raft durable state (currentTerm, votedFor).
// Must be written before responding to any RPC.
type StateStore interface {
	SaveState(term Term, votedFor NodeID) error
	LoadState() (term Term, votedFor NodeID, err error)
}

// ─── FSM interface ────────────────────────────────────────────────────────────

// FSM is the application state machine driven by committed log entries.
type FSM interface {
	// Apply is called for every committed non-config entry, in order.
	Apply(entry Entry) error
	// Snapshot serialises the current FSM state.
	Snapshot() ([]byte, error)
	// Restore replaces FSM state from a snapshot.
	Restore(data []byte) error
}

// ─── Config ───────────────────────────────────────────────────────────────────

// Config holds the parameters for a Raft node.
type Config struct {
	ID               NodeID
	HeartbeatTimeout time.Duration // leader sends heartbeats at this interval
	ElectionTimeout  time.Duration // base for randomised election timeout
	MaxAppendEntries int           // max entries per AppendEntries RPC
	SnapshotInterval Index         // take a snapshot every N committed entries
	LogStore         LogStore
	StateStore       StateStore
	FSM              FSM
	Transport        Transport
	InitialConfig    MembershipConfig
}
