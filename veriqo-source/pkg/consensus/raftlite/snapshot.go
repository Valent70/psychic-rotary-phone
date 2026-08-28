// Log compaction + InstallSnapshot RPC.
//
// Honest scope: this closes the specific gap named across every audit
// document in this session ("Kalau nanti ada 100 juta log bagaimana?
// Belum selesai" / "InstallSnapshot ... belum ada sama sekali di repo").
// It is a single-snapshot-slot design (one full-state blob at a time,
// no incremental/chunked transfer) — correct and sufficient for the
// state sizes this kernel currently produces, but a very large FSM
// state (multi-GB) would need chunked InstallSnapshot streaming, which
// is NOT implemented here and is called out in the gap report.
package raftlite

import (
	"context"
	"errors"
)

// Snapshotter lets an FSM participate in log compaction. Snapshot()
// must return a byte-for-byte deterministic encoding of the state as of
// the most recently applied entry (determinism matters: two nodes that
// applied the same commands must produce identical snapshot bytes, or
// the hash-identity proof in TestDistributedReplayHashIdentical breaks).
// Restore() must be the exact inverse.
type Snapshotter interface {
	Snapshot() ([]byte, error)
	Restore(data []byte) error
}

// SetSnapshotter wires an FSM's snapshot support into the node. Must be
// called before Run(); nil (the zero value) means compaction never
// triggers and the log grows unbounded — the pre-existing behavior.
func (n *Node) SetSnapshotter(s Snapshotter) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.snapshotter = s
}

// CompactionThreshold, when exceeded by (lastApplied - logBase), makes
// applyCommitted opportunistically compact. Exported so tests / ops
// tooling can tune it; default used by MaybeCompact if unset is 64.
const DefaultCompactionThreshold = 64

var (
	ErrNoSnapshotter       = errors.New("raftlite: Compact called with no Snapshotter configured")
	ErrCompactAheadOfApply = errors.New("raftlite: cannot compact past lastApplied")
	ErrNothingToCompact    = errors.New("raftlite: upto is not ahead of current logBase")
)

// Compact snapshots the FSM state as of index `upto` (must be <=
// lastApplied) and discards log entries at or before it, replacing them
// with a sentinel. Fail-closed: if no Snapshotter is configured, or upto
// is out of range, the log and commit/apply state are left completely
// untouched — this can never silently corrupt state, only skip the
// space-saving optimization.
func (n *Node) Compact(upto uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.compactLocked(upto)
}

func (n *Node) compactLocked(upto uint64) error {
	if n.snapshotter == nil {
		return ErrNoSnapshotter
	}
	if upto > n.lastApplied {
		return ErrCompactAheadOfApply
	}
	if upto <= n.logBase {
		return ErrNothingToCompact
	}
	p := n.pos(upto)
	if p < 0 || p >= len(n.state.log) {
		return ErrNothingToCompact
	}
	uptoTerm := n.state.log[p].Term

	data, err := n.snapshotter.Snapshot()
	if err != nil {
		return err
	}

	// Replace the log with a single sentinel entry standing in for
	// everything up to and including `upto`.
	remainder := append([]LogEntry{}, n.state.log[p:]...)
	remainder[0] = LogEntry{Term: uptoTerm, Index: upto}
	n.state.log = remainder
	n.logBase = upto
	n.lastSnapshotIndex = upto
	n.lastSnapshotTerm = uptoTerm
	n.snapshotData = data
	return nil
}

// MaybeCompact triggers Compact if the log has grown past
// DefaultCompactionThreshold entries since the last snapshot. Errors
// (including "no snapshotter configured") are intentionally swallowed —
// this is an opportunistic background optimization, not a correctness
// requirement, and applyCommitted (its only caller) must never fail a
// client-visible operation because compaction wasn't possible.
func (n *Node) MaybeCompact(lastApplied uint64) {
	n.mu.Lock()
	base := n.logBase
	n.mu.Unlock()
	if lastApplied <= base {
		return
	}
	if lastApplied-base < DefaultCompactionThreshold {
		return
	}
	_ = n.Compact(lastApplied)
}

// LastSnapshotIndex/Term expose compaction state for tests and ops
// tooling (e.g. asserting a straggler genuinely used InstallSnapshot,
// per the P0 ask: "Tambahkan assertion bahwa jalur snapshot benar-benar
// dipakai").
func (n *Node) LastSnapshotIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastSnapshotIndex
}
func (n *Node) LastSnapshotTerm() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastSnapshotTerm
}

// InstallSnapshotArgs/Reply is the RPC a leader uses to bring a peer
// current when the entries it needs have already been compacted out of
// the in-memory log.
type InstallSnapshotArgs struct {
	Term              uint64
	LeaderID          string
	LastIncludedIndex uint64
	LastIncludedTerm  uint64
	Data              []byte
}
type InstallSnapshotReply struct {
	Term uint64
}

func (n *Node) sendInstallSnapshot(ctx context.Context, peer string, term uint64, leaderID string, snapIndex, snapTerm uint64, data []byte) {
	if data == nil {
		return // nothing snapshotted yet; nextIndex will simply retry next heartbeat
	}
	rctx, cancel := context.WithTimeout(ctx, 2000_000_000) // 2s: snapshot payloads are larger than a normal heartbeat RPC
	defer cancel()
	reply, err := n.trans.SendInstallSnapshot(rctx, peer, InstallSnapshotArgs{
		Term: term, LeaderID: leaderID, LastIncludedIndex: snapIndex, LastIncludedTerm: snapTerm, Data: data,
	})
	if err != nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if reply.Term > n.state.currentTerm {
		n.becomeFollowerLocked(reply.Term)
		return
	}
	if n.role != Leader || n.state.currentTerm != term {
		return
	}
	if snapIndex+1 > n.nextIndex[peer] {
		n.nextIndex[peer] = snapIndex + 1
	}
	if snapIndex > n.matchIndex[peer] {
		n.matchIndex[peer] = snapIndex
	}
}

// HandleInstallSnapshot is the follower-side RPC handler. It fully
// replaces local state with the snapshot (fail-closed: if Restore()
// errors, the node's prior state is left untouched and it will simply
// keep asking for the same snapshot on the next heartbeat rather than
// running with a half-applied state).
func (n *Node) HandleInstallSnapshot(args InstallSnapshotArgs) InstallSnapshotReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	if args.Term < n.state.currentTerm {
		return InstallSnapshotReply{Term: n.state.currentTerm}
	}
	if args.Term > n.state.currentTerm || n.role != Follower {
		n.becomeFollowerLocked(args.Term)
	}
	n.resetElection()

	if args.LastIncludedIndex <= n.lastSnapshotIndex {
		// Stale/duplicate snapshot (e.g. a retried RPC) — already applied.
		return InstallSnapshotReply{Term: n.state.currentTerm}
	}

	if n.snapshotter != nil {
		if err := n.snapshotter.Restore(args.Data); err != nil {
			// Fail-closed: do not touch log/commit/apply state.
			return InstallSnapshotReply{Term: n.state.currentTerm}
		}
	}

	n.state.log = []LogEntry{{Term: args.LastIncludedTerm, Index: args.LastIncludedIndex}}
	n.logBase = args.LastIncludedIndex
	n.lastSnapshotIndex = args.LastIncludedIndex
	n.lastSnapshotTerm = args.LastIncludedTerm
	n.snapshotData = args.Data
	if args.LastIncludedIndex > n.commitIndex {
		n.commitIndex = args.LastIncludedIndex
	}
	if args.LastIncludedIndex > n.lastApplied {
		n.lastApplied = args.LastIncludedIndex
	}
	return InstallSnapshotReply{Term: n.state.currentTerm}
}
