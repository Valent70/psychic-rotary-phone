package raftlite

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

// sumFSM is a minimal deterministic FSM+Snapshotter: it sums every
// applied command (interpreted as a little-endian uint64) and its
// Snapshot()/Restore() round-trip that running sum exactly. Determinism
// here is what makes the hash-identity assertion below meaningful: two
// independently-driven replicas that applied the same committed log (via
// AppendEntries OR via InstallSnapshot) must reach the identical sum.
// It has its own mutex because Apply() is invoked by applyCommitted()
// OUTSIDE the Node's lock, while Snapshot()/Restore() are invoked WHILE
// the Node's lock is held (compactLocked / HandleInstallSnapshot) —
// without this, -race correctly flags a data race on sum.
type sumFSM struct {
	mu  sync.Mutex
	sum uint64
}

func newSumFSM() *sumFSM { return &sumFSM{} }

func (f *sumFSM) Apply(e LogEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(e.Command) == 8 {
		f.sum += binary.LittleEndian.Uint64(e.Command)
	}
	return nil
}

func (f *sumFSM) Snapshot() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, f.sum)
	return b, nil
}

func (f *sumFSM) Restore(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(data) != 8 {
		f.sum = 0
		return nil
	}
	f.sum = binary.LittleEndian.Uint64(data)
	return nil
}

func (f *sumFSM) Sum() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sum
}

func encodeU64(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}

// buildSumCluster is like buildCluster but wires a real per-node
// sumFSM+Snapshotter instead of the KG adapter, so compaction has
// something real to snapshot.
func buildSumCluster(t *testing.T, n int) (*MemTransport, []*Node, []*sumFSM, context.CancelFunc) {
	t.Helper()
	trans := NewMemTransport()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = string(rune('A' + i))
	}
	nodes := make([]*Node, n)
	fsms := make([]*sumFSM, n)
	for i, id := range ids {
		peers := []string{}
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		fsm := newSumFSM()
		fsms[i] = fsm
		// Election/heartbeat timing is wider than raft.go's plain
		// defaults (150ms/300ms/50ms) and than leadertransfer_stress_
		// test.go/newCheckQuorumCluster's own 200ms/400ms/20ms, which
		// this file itself used until a THIRD occurrence of the same
		// underlying flake class proved even that insufficient. History,
		// each with a different symptom of the identical root cause (the
		// majority pair's own heartbeat delayed by scheduler contention
		// long enough for the still-connected follower to time out and
		// force a spurious, but legitimate-looking, re-election):
		//   1. Real GitHub Actions CI, go test ./... right after a 63s
		//      chaos-package run: "majority term changed from 1 to 3
		//      while minority was isolated" -- fixed by widening
		//      50ms/100ms/10ms(ish) defaults to 200ms/400ms/20ms.
		//   2. This session's own local go test ./..., under similarly
		//      heavy concurrent load: "isolated minority node incremented
		//      its term" -- a DIFFERENT race (isolation applied before
		//      the minority's leaderLive flag had genuinely settled),
		//      fixed by a 150ms settle-wait before isolating, see the
		//      time.Sleep immediately below in
		//      TestPreVote_IsolatedMinorityNeverPoisonsTerm.
		//   3. Immediately after fix #2, a THIRD local go test ./...
		//      run (again right after a heavy chaos-package run) hit yet
		//      another symptom of #1's same root cause: "original leader
		//      lost leadership despite the challenger being isolated" --
		//      200ms/400ms/20ms was demonstrably NOT enough margin under
		//      this environment's real contention, proving the exact
		//      value chosen in fix #1 was a guess that turned out
		//      insufficient, not a hard bound. Widened further to
		//      500ms/900ms/20ms: HeartbeatInterval stays 20ms, so a
		//      follower now tolerates missing ~25-45 consecutive
		//      heartbeats (vs ~10-20 before) before timing out --
		//      verified against real repeated go test ./... runs, not
		//      just this package in isolation, before landing.
		node := NewNode(Config{ID: id, Peers: peers, Transport: trans, FSM: fsm,
			ElectionTimeoutMin: 500 * time.Millisecond, ElectionTimeoutMax: 900 * time.Millisecond,
			HeartbeatInterval: 20 * time.Millisecond})
		node.SetSnapshotter(fsm)
		nodes[i] = node
		trans.Register(node)
	}
	ctx, cancel := context.WithCancel(context.Background())
	for _, node := range nodes {
		go node.Run(ctx)
	}
	return trans, nodes, fsms, cancel
}

// TestInstallSnapshot_StragglerCatchUpViaCompaction is the P0 proof: a
// follower partitioned away long enough for the leader to compact past
// what it has can ONLY catch up via InstallSnapshot (the entries it
// needs no longer exist as log entries anywhere in the cluster). We
// assert LastSnapshotIndex()>0 on the straggler specifically so this
// test cannot pass by "accidentally" catching up through ordinary
// AppendEntries — closing the exact gap named in the audit transcripts.
func TestInstallSnapshot_StragglerCatchUpViaCompaction(t *testing.T) {
	trans, nodes, fsms, cancel := buildSumCluster(t, 3)
	defer cancel()

	// 3s, not 2s: matches buildSumCluster's widened 500ms/900ms election
	// timeout range (see that function's own comment) -- a worst-case
	// first election plus one retry round can approach 2s on its own
	// under real contention.
	leader := waitForLeader(t, nodes, 3*time.Second)
	var straggler *Node
	var stragglerFSM *sumFSM
	for i, n := range nodes {
		if n != leader {
			straggler = n
			stragglerFSM = fsms[i]
			break
		}
	}

	// Isolate the straggler completely (both directions, all peers).
	for _, n := range nodes {
		if n == straggler {
			continue
		}
		trans.DropTo(n.id, straggler.id)
		trans.DropTo(straggler.id, n.id)
	}

	// Propose enough commands that the connected majority compacts past
	// what the straggler has (DefaultCompactionThreshold=64).
	const total = 200
	var lastIdx uint64
	for i := uint64(1); i <= total; i++ {
		idx, _, err := leader.Propose(encodeU64(i))
		if err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
		lastIdx = idx
	}
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := leader.WaitApplied(ctx, lastIdx); err != nil {
		t.Fatalf("leader never applied all entries: %v", err)
	}

	// Confirm the leader really did compact (this is what forces
	// InstallSnapshot to be the ONLY viable catch-up path below).
	deadline := time.Now().Add(2 * time.Second)
	for leader.LastSnapshotIndex() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if leader.LastSnapshotIndex() == 0 {
		t.Fatal("leader never compacted its log — test setup invalid, cannot prove InstallSnapshot path")
	}

	// Heal the partition and let the straggler catch up.
	for _, n := range nodes {
		if n == straggler {
			continue
		}
		trans.HealTo(n.id, straggler.id)
		trans.HealTo(straggler.id, n.id)
	}

	wantSum := uint64(total * (total + 1) / 2)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if stragglerFSM.Sum() == wantSum {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if stragglerFSM.Sum() != wantSum {
		t.Fatalf("straggler FSM did not converge: got sum=%d want=%d", stragglerFSM.Sum(), wantSum)
	}
	// The hard assertion: the straggler's state MUST have arrived via
	// InstallSnapshot, not a lucky AppendEntries replay of entries that
	// (per the check above) no longer exist in the leader's log.
	if straggler.LastSnapshotIndex() == 0 {
		t.Fatal("straggler converged but LastSnapshotIndex()==0 — it did not go through InstallSnapshot, which is exactly the gap this test exists to close")
	}
	t.Logf("straggler caught up via InstallSnapshot: LastSnapshotIndex=%d sum=%d (matches leader)", straggler.LastSnapshotIndex(), stragglerFSM.Sum())
}

// TestPreVote_IsolatedMinorityNeverPoisonsTerm proves the fix for the
// real bug this session's testing surfaced: a node partitioned into the
// minority must NOT be able to keep incrementing its term (which would
// otherwise force a disruptive re-election once the partition heals,
// even though the isolated node could never actually win).
func TestPreVote_IsolatedMinorityNeverPoisonsTerm(t *testing.T) {
	trans, nodes, _, cancel := buildSumCluster(t, 3)
	defer cancel()

	leader := waitForLeader(t, nodes, 3*time.Second)
	var minority *Node
	for _, n := range nodes {
		if n != leader {
			minority = n
			break
		}
	}

	// waitForLeader returns the instant the winning node flips its own
	// IsLeader() flag -- BEFORE it has necessarily sent its first
	// heartbeat to either follower. A follower's leaderLive only becomes
	// true once HandleAppendEntries actually runs for it (see raft.go),
	// and HandlePreVote grants on the OTHER condition (!leaderLive) as
	// well as log freshness. Isolating the minority node in that gap
	// races its own (possibly already-counting-down) election timer
	// against the leader's first heartbeat: if the minority's PreVote
	// reaches a not-yet-isolated peer whose leaderLive is still false,
	// that peer can legitimately grant it, letting the minority win
	// pre-vote and bump its term WITHOUT ever contacting the leader --
	// exactly the "isolated node incremented its term" failure this test
	// exists to rule out, and exactly what was seen on a real GitHub
	// Actions run of this branch. Waiting several heartbeat intervals
	// (HeartbeatInterval=20ms in buildSumCluster) here guarantees every
	// follower has received at least one real heartbeat -- leaderLive
	// genuinely true -- before the partition is introduced, closing the
	// race at its root instead of narrowing it with wider timeouts (which
	// was already tried for this same test and evidently insufficient).
	time.Sleep(150 * time.Millisecond)

	// Isolate the minority node from both remaining peers.
	for _, n := range nodes {
		if n == minority {
			continue
		}
		trans.DropTo(n.id, minority.id)
		trans.DropTo(minority.id, n.id)
	}

	majorityTermBefore := leader.Term()

	// Give the isolated node many election-timeout cycles to try (and
	// fail) pre-vote repeatedly. Scaled up alongside buildSumCluster's
	// widened 500ms/900ms election timeout range (see that function's
	// own comment for why) so this still covers several full cycles
	// rather than only one or two.
	time.Sleep(3 * time.Second)

	if minority.Term() != majorityTermBefore && minority.Term() > majorityTermBefore {
		// With PreVote, an isolated node's currentTerm must stay pinned
		// at whatever it knew before isolation — it never got a
		// majority pre-vote, so it never incremented.
	}
	if minority.Term() > majorityTermBefore {
		t.Fatalf("isolated minority node incremented its term to %d (majority still at %d) — PreVote failed to prevent term poisoning", minority.Term(), majorityTermBefore)
	}

	// The majority side must have been completely undisturbed —
	// same leader, term never bumped by the isolated node's (nonexistent)
	// RequestVotes.
	if !leader.IsLeader() {
		t.Fatal("original leader lost leadership despite the challenger being isolated and pre-vote-blocked")
	}
	if leader.Term() != majorityTermBefore {
		t.Fatalf("majority term changed from %d to %d while minority was isolated — should be impossible with PreVote", majorityTermBefore, leader.Term())
	}

	// Heal and confirm the cluster converges to exactly one stable
	// leader afterward. We do NOT require the ORIGINAL leader to still
	// be leader: the previously-isolated node's own election timer may
	// have already been counting down and can legitimately fire and win
	// a fair, equal-log election the instant connectivity returns,
	// before a fresh heartbeat arrives — that's correct Raft behavior,
	// not something PreVote is meant to prevent. PreVote's guarantee is
	// narrower and already proven above: an isolated node cannot
	// increment its term (and thus cannot disrupt anyone) WHILE it
	// remains unreachable.
	for _, n := range nodes {
		if n == minority {
			continue
		}
		trans.HealTo(n.id, minority.id)
		trans.HealTo(minority.id, n.id)
	}
	deadline := time.Now().Add(3 * time.Second)
	var stableLeader *Node
	for time.Now().Before(deadline) {
		leaders := 0
		for _, n := range nodes {
			if n.IsLeader() {
				leaders++
				stableLeader = n
			}
		}
		if leaders == 1 {
			break
		}
		stableLeader = nil
		time.Sleep(20 * time.Millisecond)
	}
	if stableLeader == nil {
		t.Fatal("cluster did not converge to exactly one leader after healing")
	}
}
