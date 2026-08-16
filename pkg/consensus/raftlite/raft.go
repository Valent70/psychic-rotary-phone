// Package raftlite is a compact, dependency-free Raft-family consensus
// engine written for this session because the prior session's
// pkg/consensus/raft source is not present in this sandbox (filesystem
// resets between sessions). It implements leader election, log
// replication, commit-index advancement, and single-phase (non-joint)
// membership change. It does NOT implement joint (two-phase) consensus
// or leadership transfer — those remain open, see the accompanying report.
package raftlite

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"sync"
	"time"

	"veriqo/pkg/platform/observability"
)

type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

var (
	ErrNotLeader = errors.New("raftlite: not leader")
	ErrShutdown  = errors.New("raftlite: node shut down")
	ErrStaleTerm = errors.New("raftlite: stale term")

	// ErrEntryOverwritten is WaitAppliedTerm's signal that the log
	// entry originally proposed at an index was overwritten by a
	// different leader's entry (same index, different term) before
	// this node's copy of it was ever applied -- see WaitAppliedTerm's
	// doc comment for why WaitApplied alone cannot detect this.
	ErrEntryOverwritten = errors.New("raftlite: entry at this index was overwritten by a different leader before it applied")
)

// noOpCommand marks a leader-election no-op entry (see becomeLeader) —
// never delivered to the FSM, never matches isJointCommand or
// isConfChangeCommand (not valid JSON), exists purely so a new leader
// has something in its own term to commit, which per Raft §5.4.2 is
// what allows it to also commit carry-over entries from prior terms.
var noOpCommand = []byte("\x00RAFTLITE_NOOP\x00")

// LogEntry is one replicated log entry.
type LogEntry struct {
	Term    uint64
	Index   uint64
	Command []byte // opaque payload, decoded by the FSM
}

// FSM is the state machine every committed entry is applied to.
// pkg/moat/kg.Graph is wired to this interface via Adapter (fsm_adapter.go).
type FSM interface {
	Apply(entry LogEntry) error
}

// ClusterEventSink receives leader/term/membership change notifications —
// consumed by the KG adapter to keep GetClusterStatus() live.
type ClusterEventSink interface {
	OnLeaderChange(term uint64, leaderID string)
	OnMembershipChange(members []string)
}

// RequestVoteArgs/Reply and AppendEntriesArgs/Reply are the two RPCs.
type RequestVoteArgs struct {
	Term         uint64
	CandidateID  string
	LastLogIndex uint64
	LastLogTerm  uint64
}
type RequestVoteReply struct {
	Term        uint64
	VoteGranted bool
}
type AppendEntriesArgs struct {
	Term         uint64
	LeaderID     string
	PrevLogIndex uint64
	PrevLogTerm  uint64
	Entries      []LogEntry
	LeaderCommit uint64
}
type AppendEntriesReply struct {
	Term    uint64
	Success bool
	// ConflictIndex/Term implement the standard fast-backtrack optimization.
	ConflictIndex uint64
	ConflictTerm  uint64
}

// Transport abstracts the wire. pkg/transport/rafttcp implements this over
// real mTLS TCP sockets; tests use the in-memory transport in transport_mem.go.
type Transport interface {
	SendRequestVote(ctx context.Context, target string, args RequestVoteArgs) (RequestVoteReply, error)
	SendAppendEntries(ctx context.Context, target string, args AppendEntriesArgs) (AppendEntriesReply, error)
	SendInstallSnapshot(ctx context.Context, target string, args InstallSnapshotArgs) (InstallSnapshotReply, error)
	SendPreVote(ctx context.Context, target string, args RequestVoteArgs) (RequestVoteReply, error)
	SendTimeoutNow(ctx context.Context, target string, args TimeoutNowArgs) (TimeoutNowReply, error)
}

type persistentState struct {
	currentTerm uint64
	votedFor    string
	log         []LogEntry // 1-indexed logically; log[0] is a sentinel
}

// Node is one raftlite cluster member.
type Node struct {
	mu sync.Mutex

	id    string
	peers []string // other member IDs, excludes self
	trans Transport
	fsm   FSM
	sink  ClusterEventSink

	state persistentState
	role  Role

	commitIndex uint64
	lastApplied uint64

	// leader-only volatile state
	nextIndex  map[string]uint64
	matchIndex map[string]uint64

	heartbeatTimeout time.Duration
	electionMin      time.Duration
	electionMax      time.Duration
	resetElectionCh  chan struct{}

	// leaderLive is the actual disruptive-server-problem guard (Raft
	// dissertation §9.6). It is true whenever this node has heard from
	// a current-term-or-newer leader (a legitimate AppendEntries,
	// Success or not) or has just granted a real vote, and is only
	// flipped back to false by Run() itself, the instant this node's
	// OWN election timer genuinely elapses with no reset in between.
	// PreVote consults it: a node must refuse to endorse a would-be
	// candidate while it still believes its current leader is alive,
	// REGARDLESS of whether the candidate's log looks fresh enough.
	//
	// Before this fix, HandlePreVote only checked log up-to-dateness —
	// the RequestVote safety check — and never checked leader
	// liveness, which is the entire point of PreVote existing as a
	// separate RPC. That gap is exactly what let a node experiencing
	// one-directional message loss (e.g. every inbound RPC exceeding
	// its 100ms per-call deadline under a clock-skew scenario, while
	// its own outbound RPCs stayed fast) win real elections from a
	// healthy majority anyway, repeatedly disrupting a perfectly live
	// leader — found via TestChaos_ClockSkew_LeaderElectedDespiteSkew
	// intermittently failing even after election-timeout tuning fixed
	// nothing (see timeouts.go and the gap-closure report).
	leaderLive bool

	shutdownCh chan struct{}
	shutdown   bool

	appliedNotify chan struct{}

	// applyMu serializes applyCommitted's entire compute-apply-commit
	// sequence across its two call sites (the leader's post-replication
	// path and the follower's AppendEntries handler): without it, two
	// concurrent invocations could both read the same starting
	// lastApplied, build overlapping toApply batches, and double-apply
	// entries to the FSM. It also fixes a real WaitApplied race found
	// via a genuine CI failure (TestKGAdapter_EndToEndCommit: WaitApplied
	// returned nil but g.GetNode("n1") was still absent) -- see
	// applyCommitted's comment for why lastApplied must not be visible
	// to waiters until fsm.Apply has actually run for that entry.
	applyMu sync.Mutex

	// --- snapshot / log compaction state (offset-aware log) ---
	// logBase is the raft Index of n.state.log[0] (the sentinel). Before
	// any compaction logBase==0 and behavior is identical to the
	// pre-snapshot implementation. After Compact(upto), log[0] becomes a
	// sentinel for `upto` and logBase==upto; every function that used to
	// index the log slice directly by raft Index now goes through pos().
	logBase           uint64
	lastSnapshotIndex uint64
	lastSnapshotTerm  uint64
	snapshotter       Snapshotter // optional; nil means compaction is a no-op
	snapshotData      []byte      // cached bytes of the most recent snapshot, served to laggards via InstallSnapshot

	// preVoteEnabled guards against the disruptive-server problem (Ongaro
	// dissertation §9.6 / etcd's PreVote): a partitioned node must not be
	// able to bump the cluster term with RequestVote it can never win.
	preVoteEnabled bool

	// joint tracks an in-progress two-phase C(old,new) membership
	// transition; see jointconsensus.go.
	joint jointState

	// removed is set when this node applies a config change (joint or
	// confchange) that no longer includes itself. A removed node must
	// never again start an election or act as leader — otherwise, as a
	// leader that lost its followers' recognition but doesn't know it,
	// it would keep sending heartbeats that hold the REMAINING cluster
	// hostage in Follower state forever (found as a real, reproducible
	// bug via -count=1 -race repetition in this session: the surviving
	// {A,B,D} cluster would intermittently never elect a leader because
	// the just-removed node C kept heartbeating them as a ghost leader).
	removed bool

	// storage is the optional durability seam (see wal.go). nil means
	// fully in-memory, byte-for-byte the same behavior every prior
	// session of this project tested against.
	storage Storage
	// metrics is the optional observability seam (see
	// pkg/platform/observability). nil-safe: every call site checks it.
	metrics *observability.Registry

	// lastContact records, per peer, the last time this node (as leader)
	// received ANY non-error reply from that peer — used by CheckQuorum
	// to detect real isolation vs. transient/partial loss.
	lastContact map[string]time.Time
	// checkQuorumEnabled/Timeout: see checkquorum.go.
	checkQuorumEnabled bool
	checkQuorumTimeout time.Duration

	// timeoutNowCh delivers a real TimeoutNow RPC (see leadertransfer.go)
	// into Run()'s election-wait select, letting a leadership-transfer
	// target skip the rest of its randomized election timeout and start
	// an election immediately, as Raft's leadership-transfer extension
	// requires.
	timeoutNowCh chan struct{}
}

type Config struct {
	ID                 string
	Peers              []string
	Transport          Transport
	FSM                FSM
	Sink               ClusterEventSink // optional
	ElectionTimeoutMin time.Duration
	ElectionTimeoutMax time.Duration
	HeartbeatInterval  time.Duration

	// Storage, if set, makes this node's term/vote/log/snapshot-boundary
	// state survive a process restart (see wal.go). Nil (the default)
	// preserves the exact in-memory-only behavior of every prior
	// session's tests.
	Storage Storage
	// Metrics, if set, receives operational counters (wal_writes,
	// wal_failures, checkquorum_stepdowns, readindex_success,
	// readindex_timeout, leadership_transfers, snapshot_chunks_sent,
	// snapshot_chunks_received — see pkg/platform/observability). Nil is
	// safe and disables metrics entirely.
	Metrics *observability.Registry

	// CheckQuorumEnabled defaults to true: a leader that cannot confirm
	// contact with a majority within CheckQuorumTimeout steps down
	// rather than continuing to (falsely) believe it is still leader.
	// See checkquorum.go.
	CheckQuorumDisabled bool
	CheckQuorumTimeout  time.Duration
}

func NewNode(cfg Config) *Node {
	if cfg.ElectionTimeoutMin == 0 {
		cfg.ElectionTimeoutMin = 150 * time.Millisecond
	}
	if cfg.ElectionTimeoutMax == 0 {
		cfg.ElectionTimeoutMax = 300 * time.Millisecond
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 50 * time.Millisecond
	}
	if cfg.CheckQuorumTimeout == 0 {
		cfg.CheckQuorumTimeout = cfg.ElectionTimeoutMin
	}
	n := &Node{
		id:                 cfg.ID,
		peers:              cfg.Peers,
		trans:              cfg.Transport,
		fsm:                cfg.FSM,
		sink:               cfg.Sink,
		role:               Follower,
		nextIndex:          make(map[string]uint64),
		matchIndex:         make(map[string]uint64),
		heartbeatTimeout:   cfg.HeartbeatInterval,
		electionMin:        cfg.ElectionTimeoutMin,
		electionMax:        cfg.ElectionTimeoutMax,
		resetElectionCh:    make(chan struct{}, 1),
		shutdownCh:         make(chan struct{}),
		appliedNotify:      make(chan struct{}, 1),
		preVoteEnabled:     true,
		storage:            cfg.Storage,
		metrics:            cfg.Metrics,
		lastContact:        make(map[string]time.Time),
		checkQuorumEnabled: !cfg.CheckQuorumDisabled,
		checkQuorumTimeout: cfg.CheckQuorumTimeout,
		timeoutNowCh:       make(chan struct{}, 1),
	}
	n.state.log = []LogEntry{{Term: 0, Index: 0}} // sentinel

	if cfg.Storage != nil {
		if snap, ok, err := cfg.Storage.Load(); err == nil && ok {
			n.state.currentTerm = snap.Term
			n.state.votedFor = snap.VotedFor
			if len(snap.Log) > 0 {
				n.state.log = snap.Log
			}
			n.logBase = snap.LogBase
			n.lastSnapshotIndex = snap.LastSnapshotIndex
			n.lastSnapshotTerm = snap.LastSnapshotTerm
		}
		// A missing file (fresh node) or a corrupt WAL both fall through
		// to the zero-value/sentinel state above rather than failing
		// NewNode outright: refusing to start the process is worse for
		// availability than starting clean, since Raft's own
		// log-matching property will resync this node from the leader
		// regardless (the corruption is logged via the returned err at
		// the call site if the caller chooses to check it explicitly).
	}
	return n
}

// incMetric is a nil-safe counter increment — every raftlite call site
// uses this instead of touching n.metrics directly, so Metrics being
// unset (nil) never needs a guard at the call site.
func (n *Node) incMetric(name string) {
	if n.metrics != nil {
		n.metrics.Counter(name).Inc()
	}
}

// persistLocked writes the full current durable state via n.storage.
// Callers MUST already hold n.mu. A no-op when Storage is unset.
func (n *Node) persistLocked() {
	if n.storage == nil {
		return
	}
	snap := StateSnapshot{
		Term:              n.state.currentTerm,
		VotedFor:          n.state.votedFor,
		Log:               append([]LogEntry(nil), n.state.log...),
		LogBase:           n.logBase,
		LastSnapshotIndex: n.lastSnapshotIndex,
		LastSnapshotTerm:  n.lastSnapshotTerm,
	}
	if err := n.storage.Persist(snap); err != nil {
		n.incMetric("wal_failures")
		return
	}
	n.incMetric("wal_writes")
}

func randDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min))) // #nosec G404 -- election-timeout jitter, not security-sensitive; must stay seedable for deterministic replay
}

// SetTransport (re)wires the node's Transport after construction — used
// when a node process restarts and needs to attach a freshly dialed
// client, or in tests simulating a real reconnect rather than a
// MemTransport partition flag.
func (n *Node) SetTransport(t Transport) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.trans = t
}

func (n *Node) ID() string { return n.id }

func (n *Node) Role() Role {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.role
}

func (n *Node) Term() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state.currentTerm
}

func (n *Node) IsLeader() bool { return n.Role() == Leader }

// Run drives the election/heartbeat loop until ctx is cancelled or
// Shutdown() is called.
//
// A real, reproduced bug lived here: this loop's own top never checked
// ctx.Done()/shutdownCh -- it relied entirely on leaderLoop noticing
// shutdown internally and returning, then trusted the NEXT iteration to
// stop. But leaderLoop's ctx.Done()/shutdownCh exit paths return without
// demoting n.role away from Leader, so once every peer is already gone
// (e.g., every node in a cluster is being torn down in the same
// process, which is exactly what a bounded test run does at the end of
// each case), this loop re-reads role=Leader and calls leaderLoop(ctx)
// again -- which immediately re-observes the same already-closed
// ctx.Done()/shutdownCh and returns again, forever, in as tight a spin
// as replicateToAll's real per-iteration lock/goroutine overhead
// allows. The only thing that could ever break the spin was
// leaderLoop's own select happening to catch its heartbeat ticker
// instead of the (also always-ready) shutdown channels on some
// iteration, which is not guaranteed to happen promptly and was
// observed, once something finally waited on Run() actually returning
// (pkg/blockers/dr's Destroy(), which nothing had ever done before),
// to take up to several seconds under real -race CPU contention rather
// than the microseconds a shutdown should take. Checking both channels
// directly here, before ever re-entering the Leader case, makes
// termination immediate and independent of role or ticker timing.
func (n *Node) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.shutdownCh:
			return
		default:
		}

		n.mu.Lock()
		role := n.role
		n.mu.Unlock()

		switch role {
		case Follower, Candidate:
			timer := time.NewTimer(randDuration(n.electionMin, n.electionMax))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-n.shutdownCh:
				timer.Stop()
				return
			case <-n.resetElectionCh:
				timer.Stop()
			case <-n.timeoutNowCh:
				// A real TimeoutNow RPC from a leader performing a
				// graceful handoff (see leadertransfer.go): skip the
				// rest of the randomized election wait and start an
				// election immediately, as Raft's leadership-transfer
				// extension specifies.
				timer.Stop()
				n.mu.Lock()
				n.leaderLive = false
				n.mu.Unlock()
				n.startElection(ctx)
			case <-timer.C:
				// The timeout genuinely elapsed with no intervening
				// reset: this node no longer believes its leader is
				// alive. Only now is it honest for PreVote to start
				// endorsing challengers (see leaderLive doc comment).
				n.mu.Lock()
				n.leaderLive = false
				n.mu.Unlock()
				n.startElection(ctx)
			}
		case Leader:
			n.leaderLoop(ctx)
		}
	}
}

func (n *Node) Shutdown() {
	n.mu.Lock()
	if n.shutdown {
		n.mu.Unlock()
		return
	}
	n.shutdown = true
	n.mu.Unlock()
	close(n.shutdownCh)
}

// resetElection signals Run()'s election-timeout select to restart with a
// fresh randomized timeout. Safe to call while n.mu is held by the caller
// (it is, from HandleAppendEntries/HandleRequestVote) — it only touches the
// buffered channel, never a shared field, avoiding the data race a plain
// field write would have had against Run()'s unlocked timer read.
func (n *Node) resetElection() {
	select {
	case n.resetElectionCh <- struct{}{}:
	default:
	}
}

// runPreVote asks peers "would you vote for me at term+1?" WITHOUT
// mutating any persistent state (no term bump, no votedFor write). Only
// if a majority say yes does startElection proceed to a real election.
// This is what stops a minority node isolated by a network partition
// from repeatedly incrementing its term and forcing a disruptive
// re-election storm once the partition heals (Raft dissertation §9.6) —
// found as a live bug via chaos testing in this session, see report.
func (n *Node) runPreVote(ctx context.Context) bool {
	if !n.preVoteEnabled {
		return true
	}
	n.mu.Lock()
	nextTerm := n.state.currentTerm + 1
	lastIdx, lastTerm := n.lastLogInfoLocked()
	peers := append([]string{}, n.peers...)
	n.mu.Unlock()

	if len(peers) == 0 {
		return true
	}
	n.mu.Lock()
	currentMembers := append([]string{n.id}, n.peers...)
	n.mu.Unlock()

	type result struct {
		peer    string
		granted bool
	}
	results := make(chan result, len(peers))

	var wg sync.WaitGroup
	for _, p := range peers {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			rctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			reply, err := n.trans.SendPreVote(rctx, p, RequestVoteArgs{
				Term: nextTerm, CandidateID: n.id, LastLogIndex: lastIdx, LastLogTerm: lastTerm,
			})
			if err != nil {
				results <- result{p, false}
				return
			}
			results <- result{p, reply.VoteGranted}
		}()
	}
	wg.Wait()
	close(results)
	grantedSet := map[string]bool{}
	for r := range results {
		if r.granted {
			grantedSet[r.peer] = true
		}
	}
	n.mu.Lock()
	won := n.hasQuorumBoolLocked(currentMembers, grantedSet)
	n.mu.Unlock()
	return won
}

func (n *Node) startElection(ctx context.Context) {
	n.mu.Lock()
	removed := n.removed
	n.mu.Unlock()
	if removed {
		return
	}
	if !n.runPreVote(ctx) {
		// Pre-vote quorum not reached: stay a follower/candidate at the
		// CURRENT term. Critically we never incremented currentTerm, so
		// an isolated minority node cannot poison the cluster term once
		// the partition heals — it just keeps failing pre-vote quietly.
		return
	}

	n.mu.Lock()
	n.role = Candidate
	n.state.currentTerm++
	term := n.state.currentTerm
	n.state.votedFor = n.id
	n.persistLocked()
	lastIdx, lastTerm := n.lastLogInfoLocked()
	n.mu.Unlock()

	n.mu.Lock()
	currentMembers := append([]string{n.id}, n.peers...)
	peersSnapshot := append([]string{}, n.peers...)
	n.mu.Unlock()

	granted := map[string]bool{}
	var grantedMu sync.Mutex

	var wg sync.WaitGroup
	for _, p := range peersSnapshot {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			rctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			reply, err := n.trans.SendRequestVote(rctx, p, RequestVoteArgs{
				Term: term, CandidateID: n.id, LastLogIndex: lastIdx, LastLogTerm: lastTerm,
			})
			if err != nil {
				return
			}
			n.mu.Lock()
			if reply.Term > n.state.currentTerm {
				n.becomeFollowerLocked(reply.Term)
				n.mu.Unlock()
				return
			}
			n.mu.Unlock()
			if reply.VoteGranted {
				grantedMu.Lock()
				granted[p] = true
				gcopy := make(map[string]bool, len(granted))
				for k, v := range granted {
					gcopy[k] = v
				}
				grantedMu.Unlock()
				n.mu.Lock()
				won := n.hasQuorumBoolLocked(currentMembers, gcopy)
				n.mu.Unlock()
				if won {
					n.becomeLeader(term)
				}
			}
		}()
	}
	wg.Wait()
}

func (n *Node) becomeLeader(term uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.role == Leader || n.state.currentTerm != term {
		return
	}
	n.role = Leader
	// A leader always considers itself the live leader — without this,
	// a freshly elected node whose leaderLive happened to be false
	// (the normal case: it just got here via ITS OWN election timeout
	// firing, which is exactly what sets leaderLive=false) would
	// incorrectly grant a PreVote to any challenger against itself the
	// instant one arrived, undermining the fix in HandlePreVote. Found
	// via repeated runs of TestChaos_ClockSkew_LeaderElectedDespiteSkew
	// still failing ~1-in-10 after the HandlePreVote liveness check
	// alone was added.
	n.leaderLive = true
	lastIdx, _ := n.lastLogInfoLocked()
	now := time.Now()
	n.lastContact = make(map[string]time.Time, len(n.peers))
	for _, p := range n.peers {
		n.nextIndex[p] = lastIdx + 1
		n.matchIndex[p] = 0
		// Grace window: a freshly elected leader hasn't contacted anyone
		// yet, but that isn't isolation — seed "now" so CheckQuorum gives
		// it a full checkQuorumTimeout to complete its first replication
		// round before it could ever consider itself cut off.
		n.lastContact[p] = now
	}

	// Raft only commits entries from a PRIOR term indirectly, by
	// counting replicas of a LATER entry from the leader's OWN current
	// term (dissertation §5.4.2). Without ever proposing anything in its
	// own term, a freshly elected leader can leave earlier, fully
	// replicated-but-uncommitted entries (e.g. an in-flight membership
	// change) stuck forever if no new client command happens to arrive.
	// The standard fix — and what real Raft implementations (etcd, etc.)
	// do — is to append a no-op entry in the new term immediately.
	noOpIndex := n.logBase + uint64(len(n.state.log))
	n.state.log = append(n.state.log, LogEntry{Term: term, Index: noOpIndex, Command: noOpCommand})
	n.persistLocked()
	if n.sink != nil {
		n.sink.OnLeaderChange(term, n.id)
	}
}

func (n *Node) becomeFollowerLocked(term uint64) {
	n.role = Follower
	n.state.currentTerm = term
	n.state.votedFor = ""
	n.persistLocked()
}

func (n *Node) lastLogInfoLocked() (uint64, uint64) {
	last := n.state.log[len(n.state.log)-1]
	return last.Index, last.Term
}

func (n *Node) leaderLoop(ctx context.Context) {
	ticker := time.NewTicker(n.heartbeatTimeout)
	defer ticker.Stop()
	for {
		n.replicateToAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-n.shutdownCh:
			return
		case <-ticker.C:
			n.checkQuorumTick()
			n.mu.Lock()
			isLeader := n.role == Leader
			n.mu.Unlock()
			if !isLeader {
				return
			}
		}
	}
}

func (n *Node) replicateToAll(ctx context.Context) {
	n.mu.Lock()
	if n.role != Leader {
		n.mu.Unlock()
		return
	}
	term := n.state.currentTerm
	peers := append([]string{}, n.peers...)
	n.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range peers {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.replicateTo(ctx, p, term)
		}()
	}
	wg.Wait()
	n.advanceCommitIndex()
	n.applyCommitted()
}

// pos converts a raft log Index into a position in n.state.log, valid
// only when index >= n.logBase. Callers at or below logBase must not
// call this — that range is compacted away and only reachable via a
// snapshot (see snapshot.go).
func (n *Node) pos(index uint64) int { return int(index - n.logBase) } // #nosec G115 -- callers hold index >= n.logBase as an invariant (checked at call sites), so this never underflows in practice

func (n *Node) replicateTo(ctx context.Context, peer string, term uint64) {
	n.mu.Lock()
	if n.role != Leader || n.state.currentTerm != term {
		n.mu.Unlock()
		return
	}
	next := n.nextIndex[peer]
	if next == 0 {
		next = 1
	}

	// The entries this peer needs starting at `next` have already been
	// compacted out of the in-memory log (next-1 < logBase) — it cannot
	// be caught up via AppendEntries at all; it needs the snapshot.
	if next <= n.logBase {
		snapIndex, snapTerm, data := n.lastSnapshotIndex, n.lastSnapshotTerm, n.snapshotData
		leaderID := n.id
		n.mu.Unlock()
		n.sendInstallSnapshot(ctx, peer, term, leaderID, snapIndex, snapTerm, data)
		return
	}

	prevIdx := next - 1
	prevTerm := uint64(0)
	if p := n.pos(prevIdx); p >= 0 && p < len(n.state.log) {
		prevTerm = n.state.log[p].Term
	}
	var entries []LogEntry
	if p := n.pos(next); p < len(n.state.log) {
		entries = append(entries, n.state.log[p:]...)
	}
	leaderCommit := n.commitIndex
	n.mu.Unlock()

	rctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	reply, err := n.trans.SendAppendEntries(rctx, peer, AppendEntriesArgs{
		Term: term, LeaderID: n.id, PrevLogIndex: prevIdx, PrevLogTerm: prevTerm,
		Entries: entries, LeaderCommit: leaderCommit,
	})
	if err != nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	// Any non-error reply — Success true OR false — is real evidence
	// this peer is alive and reachable, which is exactly what
	// CheckQuorum needs to distinguish "isolated leader" from "peer just
	// happens to be behind on the log". A rejected AppendEntries due to
	// log mismatch is not the same thing as a network partition.
	n.lastContact[peer] = time.Now()
	if reply.Term > n.state.currentTerm {
		n.becomeFollowerLocked(reply.Term)
		return
	}
	if n.role != Leader || n.state.currentTerm != term {
		return
	}
	// Stale-reply guard: under chaos-level delay/duplication, more than
	// one replicateTo() call to the SAME peer can be in flight at once
	// (a fresh heartbeat tick fires before a delayed earlier reply comes
	// back), and their replies can arrive out of order. If nextIndex has
	// already moved on from what THIS request used (prevIdx+1), this
	// reply is for a superseded request — applying it (in either
	// direction) would corrupt leader state. Simply ignore it; the
	// in-flight request that actually matches current nextIndex will
	// still get its own reply processed normally.
	if n.nextIndex[peer] != prevIdx+1 {
		return
	}
	if reply.Success {
		newMatch := prevIdx + uint64(len(entries))
		if newMatch > n.matchIndex[peer] {
			n.matchIndex[peer] = newMatch
		}
		n.nextIndex[peer] = n.matchIndex[peer] + 1
	} else {
		// fast backtrack using conflict hint
		if reply.ConflictTerm != 0 {
			idx := prevIdx
			for idx > n.logBase && n.state.log[n.pos(idx)].Term == reply.ConflictTerm {
				idx--
			}
			n.nextIndex[peer] = idx + 1
		} else if reply.ConflictIndex > 0 {
			n.nextIndex[peer] = reply.ConflictIndex
		} else if n.nextIndex[peer] > 1 {
			n.nextIndex[peer]--
		}
		if n.nextIndex[peer] < 1 {
			n.nextIndex[peer] = 1
		}
	}
}

func (n *Node) advanceCommitIndex() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.role != Leader {
		return
	}
	if len(n.state.log) == 0 {
		// A properly initialized node always carries at least a
		// sentinel entry at logBase; this guard exists so that
		// len(n.state.log)-1 below can never underflow into a huge
		// uint64 (gosec G115) if that invariant is ever violated,
		// instead of committing against a garbage index.
		return
	}
	lastIdx := n.logBase + uint64(len(n.state.log)-1) // #nosec G115 -- guarded by the len==0 check immediately above
	for idx := lastIdx; idx > n.commitIndex && idx > n.logBase; idx-- {
		if n.state.log[n.pos(idx)].Term != n.state.currentTerm {
			continue // only commit entries from current term directly (Raft §5.4.2)
		}
		if n.hasQuorumIndexLocked(idx) {
			n.commitIndex = idx
			break
		}
	}
}

// applyCommitted drains commitIndex into the FSM. n.lastApplied is
// deliberately NOT bumped until every entry in this batch has actually
// been handed to fsm.Apply (or its conf-change/joint/no-op equivalent)
// below -- WaitApplied's whole contract is "the entry has been applied
// to the FSM", and a caller polling n.lastApplied must never observe it
// advance before that is true. An earlier version bumped lastApplied
// while still holding n.mu, before the (necessarily lock-free, since
// FSM.Apply can be arbitrarily slow) apply loop below had run -- real
// CI caught the resulting race: TestKGAdapter_EndToEndCommit's
// WaitApplied returned nil while g.GetNode("n1") was still absent,
// because the waiter woke up between the counter bump and the actual
// KG mutation. applyMu (held for this whole function) additionally
// prevents two concurrent calls to applyCommitted -- possible via this
// function's two call sites, the leader's post-replication path and
// the follower's AppendEntries handler -- from computing overlapping
// toApply batches and double-applying entries to the FSM.
func (n *Node) applyCommitted() {
	n.applyMu.Lock()
	defer n.applyMu.Unlock()

	n.mu.Lock()
	toApply := []LogEntry{}
	newApplied := n.lastApplied
	for newApplied < n.commitIndex {
		newApplied++
		toApply = append(toApply, n.state.log[n.pos(newApplied)])
	}
	n.mu.Unlock()
	if len(toApply) == 0 {
		return
	}
	for _, e := range toApply {
		// Conf-change entries are routed to atomic membership application
		// instead of the FSM — the FSM must never see the internal
		// confChangeEnvelope wire format (see confchange.go).
		if env, ok := isConfChangeCommand(e.Command); ok {
			n.mu.Lock()
			_ = n.applyConfChangeLocked(env) // divergence handled defensively inside; never silently corrupts state
			members := append([]string{n.id}, n.peers...)
			n.mu.Unlock()
			if n.sink != nil {
				n.sink.OnMembershipChange(members)
			}
			continue
		}
		if env, ok := isJointCommand(e.Command); ok {
			n.mu.Lock()
			shouldProposeLeave, newTarget := n.applyJointLocked(env)
			members := append([]string{n.id}, n.peers...)
			n.mu.Unlock()
			if n.sink != nil {
				n.sink.OnMembershipChange(members)
			}
			if shouldProposeLeave {
				leaveEnv := jointEnvelope{Magic: jointMagic, Phase: jointLeave, New: newTarget}
				if cmd, err := json.Marshal(leaveEnv); err == nil {
					_, _, _ = n.Propose(cmd) // best-effort: if leadership is lost mid-transition, the new leader must re-drive LEAVE_JOINT itself (see report's known-gap note)
				}
			}
			continue
		}
		if len(e.Command) == len(noOpCommand) && string(e.Command) == string(noOpCommand) {
			continue
		}
		if n.fsm != nil {
			_ = n.fsm.Apply(e)
		}
	}
	n.mu.Lock()
	n.lastApplied = newApplied
	n.mu.Unlock()
	select {
	case n.appliedNotify <- struct{}{}:
	default:
	}
	n.MaybeCompact(newApplied)
}

// Propose appends a new command to the leader's log. Returns ErrNotLeader
// if this node is not currently the leader (no forwarding — caller must
// retry against the actual leader, same as upstream Raft client contract).
func (n *Node) Propose(cmd []byte) (index uint64, term uint64, err error) {
	n.mu.Lock()
	if n.role != Leader {
		n.mu.Unlock()
		return 0, 0, ErrNotLeader
	}
	term = n.state.currentTerm
	index = n.logBase + uint64(len(n.state.log))
	n.state.log = append(n.state.log, LogEntry{Term: term, Index: index, Command: cmd})
	if env, ok := isJointCommand(cmd); ok {
		n.applyJointStateOnlyLocked(env)
	}
	n.persistLocked()
	n.mu.Unlock()
	return index, term, nil
}

// WaitApplied blocks until LastApplied() >= index or ctx is done.
//
// Caveat sharp enough to repeat here, not just on WaitAppliedTerm: raft
// commit indices are positions in a log, not identities of the entries
// that once occupied them. If the entry Propose() returned this index
// for was on a leader that lost leadership before replicating it to a
// majority, a LATER leader can legitimately commit a completely
// different entry at the exact same index. WaitApplied(ctx, index)
// returning nil only proves "something is now applied at this index";
// it does NOT prove that something is the caller's own entry. Prefer
// WaitAppliedTerm when the term Propose() returned is available (it
// always is) and the caller actually cares which entry landed.
func (n *Node) WaitApplied(ctx context.Context, index uint64) error {
	for {
		n.mu.Lock()
		applied := n.lastApplied
		n.mu.Unlock()
		if applied >= index {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-n.appliedNotify:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// WaitAppliedTerm is WaitApplied strengthened to also confirm the
// entry that actually applied at index is the SAME entry the caller
// proposed, by checking it still carries the term Propose() returned
// alongside index. Found necessary via a real, reproducible failure:
// TestKGAdapter_EndToEndCommit occasionally observed WaitApplied
// return nil for an entry that was never actually applied, because a
// real leadership change (the leader that accepted the Propose call
// stepped down before replicating to a majority) let a later leader's
// unrelated entry commit at that same numeric index first. Plain
// WaitApplied cannot distinguish "my entry applied" from "some entry
// now occupies that slot" -- this can.
//
// Returns ErrEntryOverwritten if the entry at index has a different
// term than expected (the caller's entry was genuinely discarded, not
// applied -- Propose() succeeding never guaranteed eventual commit,
// same as upstream Raft's client contract). Returns nil, with no
// distinction from "confirmed", if index has already been compacted
// out of the log by the time this returns: compaction only occurs
// well after application, past DefaultCompactionThreshold, so this is
// a graceful-degradation case rather than one this method is meant to
// protect against.
func (n *Node) WaitAppliedTerm(ctx context.Context, index, term uint64) error {
	if err := n.WaitApplied(ctx, index); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if index < n.logBase || index >= n.logBase+uint64(len(n.state.log)) {
		return nil
	}
	if n.state.log[n.pos(index)].Term != term {
		return ErrEntryOverwritten
	}
	return nil
}

func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

func (n *Node) LastApplied() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastApplied
}

// PeerReplicationState reports {nextIndex, matchIndex} per peer — for
// operator dashboards and diagnostics (e.g. spotting a peer stuck far
// behind, or nextIndex/matchIndex disagreeing in a way that indicates a
// stalled replication stream).
func (n *Node) PeerReplicationState() map[string][2]uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string][2]uint64, len(n.peers))
	for _, p := range n.peers {
		out[p] = [2]uint64{n.nextIndex[p], n.matchIndex[p]}
	}
	return out
}

// --- RPC handlers, invoked by Transport implementations on receipt ---

func (n *Node) HandleRequestVote(args RequestVoteArgs) RequestVoteReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	if args.Term < n.state.currentTerm {
		return RequestVoteReply{Term: n.state.currentTerm, VoteGranted: false}
	}
	if args.Term > n.state.currentTerm {
		n.becomeFollowerLocked(args.Term)
	}
	lastIdx, lastTerm := n.lastLogInfoLocked()
	logOK := args.LastLogTerm > lastTerm || (args.LastLogTerm == lastTerm && args.LastLogIndex >= lastIdx)

	if (n.state.votedFor == "" || n.state.votedFor == args.CandidateID) && logOK {
		n.state.votedFor = args.CandidateID
		n.persistLocked()
		n.leaderLive = true // we now expect this candidate to become leader shortly
		n.resetElection()
		return RequestVoteReply{Term: n.state.currentTerm, VoteGranted: true}
	}
	return RequestVoteReply{Term: n.state.currentTerm, VoteGranted: false}
}

// HandlePreVote answers "would you vote for this candidate if it started
// a real election?" without side effects on persistent state (no term
// bump, no votedFor write) — safe to call even from a stale/partitioned
// peer's request, unlike HandleRequestVote.
//
// Two independent conditions must both hold, matching Raft dissertation
// §9.6 exactly:
//  1. logOK — the candidate's log is at least as up to date as ours
//     (the RequestVote safety check).
//  2. !n.leaderLive — WE do not currently believe our own leader is
//     alive (the liveness/disruption check PreVote exists for).
//
// Condition 2 was missing entirely in the prior implementation: it
// granted PreVote on log freshness alone, which meant any node with a
// competitive log — even one that was itself experiencing one-way
// message loss and had NOT genuinely lost its leader — could collect
// PreVote grants from a perfectly healthy majority and go on to force
// real, disruptive elections against a live leader. That is precisely
// the failure mode PreVote was invented to prevent.
func (n *Node) HandlePreVote(args RequestVoteArgs) RequestVoteReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	if args.Term < n.state.currentTerm {
		return RequestVoteReply{Term: n.state.currentTerm, VoteGranted: false}
	}
	if n.leaderLive {
		return RequestVoteReply{Term: n.state.currentTerm, VoteGranted: false}
	}
	lastIdx, lastTerm := n.lastLogInfoLocked()
	logOK := args.LastLogTerm > lastTerm || (args.LastLogTerm == lastTerm && args.LastLogIndex >= lastIdx)
	return RequestVoteReply{Term: n.state.currentTerm, VoteGranted: logOK}
}

func (n *Node) HandleAppendEntries(args AppendEntriesArgs) AppendEntriesReply {
	n.mu.Lock()

	if args.Term < n.state.currentTerm {
		reply := AppendEntriesReply{Term: n.state.currentTerm, Success: false}
		n.mu.Unlock()
		return reply
	}
	if args.Term > n.state.currentTerm || n.role != Follower {
		n.becomeFollowerLocked(args.Term)
	}
	// A real AppendEntries at our term or newer is legitimate contact
	// from a current leader, regardless of whether the log-matching
	// checks below end up accepting or rejecting THIS entry — so this
	// is where leaderLive must be set, not conditioned on Success.
	n.leaderLive = true
	n.resetElection()

	// The leader thinks we need entries older than what we've already
	// compacted away. We can't validate PrevLogTerm against a log
	// position we no longer have — force the leader down to
	// logBase+1, which makes its replicateTo() fall into the
	// InstallSnapshot branch on the next round.
	if args.PrevLogIndex < n.logBase {
		reply := AppendEntriesReply{Term: n.state.currentTerm, Success: false, ConflictIndex: n.logBase + 1}
		n.mu.Unlock()
		return reply
	}

	pp := n.pos(args.PrevLogIndex)
	if pp >= len(n.state.log) {
		reply := AppendEntriesReply{Term: n.state.currentTerm, Success: false, ConflictIndex: n.logBase + uint64(len(n.state.log))}
		n.mu.Unlock()
		return reply
	}
	if n.state.log[pp].Term != args.PrevLogTerm {
		conflictTerm := n.state.log[pp].Term
		idx := args.PrevLogIndex
		for idx > n.logBase && n.state.log[n.pos(idx)].Term == conflictTerm {
			idx--
		}
		reply := AppendEntriesReply{Term: n.state.currentTerm, Success: false, ConflictIndex: idx + 1, ConflictTerm: conflictTerm}
		n.mu.Unlock()
		return reply
	}

	// truncate on conflict, then append new entries
	insertAt := pp + 1
	for i, e := range args.Entries {
		p := insertAt + i
		if p < len(n.state.log) {
			if n.state.log[p].Term != e.Term {
				n.state.log = n.state.log[:p]
				n.state.log = append(n.state.log, args.Entries[i:]...)
				break
			}
		} else {
			n.state.log = append(n.state.log, args.Entries[i:]...)
			break
		}
	}
	n.refreshJointFromLogLocked()
	n.persistLocked()

	if args.LeaderCommit > n.commitIndex {
		lastNew := args.PrevLogIndex + uint64(len(args.Entries))
		if args.LeaderCommit < lastNew {
			n.commitIndex = args.LeaderCommit
		} else {
			n.commitIndex = lastNew
		}
	}
	term := n.state.currentTerm
	n.mu.Unlock()
	n.applyCommitted()
	return AppendEntriesReply{Term: term, Success: true}
}
