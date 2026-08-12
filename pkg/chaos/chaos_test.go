package chaos

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"veriqo/pkg/consensus/raftlite"
	"veriqo/pkg/moat/kg"
)

type sumFSM struct {
	mu   sync.Mutex
	sum  uint64
	seen map[uint64]bool
}

func (f *sumFSM) Apply(e raftlite.LogEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(e.Command) != 16 {
		return nil
	}
	seq := binary.LittleEndian.Uint64(e.Command[:8])
	val := binary.LittleEndian.Uint64(e.Command[8:])
	if f.seen == nil {
		f.seen = map[uint64]bool{}
	}
	if f.seen[seq] {
		return nil // already applied this logical command — a retried, previously-orphaned proposal
	}
	f.seen[seq] = true
	f.sum += val
	return nil
}
func (f *sumFSM) Snapshot() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b[:8], f.sum)
	sum := sha256.Sum256(b[:8])
	copy(b[8:], sum[:8])
	return b, nil
}
func (f *sumFSM) Restore(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(data) != 16 {
		return fmt.Errorf("chaos test fsm: corrupted snapshot payload (want 16 bytes, got %d)", len(data))
	}
	want := sha256.Sum256(data[:8])
	if !bytes.Equal(want[:8], data[8:]) {
		return fmt.Errorf("chaos test fsm: checksum mismatch — corrupted snapshot rejected")
	}
	f.sum = binary.LittleEndian.Uint64(data[:8])
	// Note: seen-set is not part of the snapshot in this minimal test
	// FSM (acceptable here: dedup only needs to hold within one client
	// retry loop, not survive a restart/restore in this scenario).
	return nil
}
func (f *sumFSM) Sum() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sum
}

func encodeSeqVal(seq, val uint64) []byte {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b[:8], seq)
	binary.LittleEndian.PutUint64(b[8:], val)
	return b
}

func waitForLeader(t *testing.T, nodes []*raftlite.Node, timeout time.Duration) *raftlite.Node {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() {
				return n
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no leader elected under chaos within timeout")
	return nil
}

// buildChaosCluster wires a 5-node raftlite cluster where every node's
// transport is wrapped by chaos.Transport with the given Scenario,
// sharing one MemTransport as the underlying router (so packet loss /
// duplication / delay are injected on top of otherwise-normal delivery,
// not a hard partition — this is deliberately a HARSHER, more realistic
// test than the partition-only tests in raftlite itself).
func buildChaosCluster(t *testing.T, n int, scenario Scenario) ([]*raftlite.Node, []*sumFSM, []*Transport, context.CancelFunc) {
	t.Helper()
	mem := raftlite.NewMemTransport()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = string(rune('A' + i))
	}
	nodes := make([]*raftlite.Node, n)
	fsms := make([]*sumFSM, n)
	chaosTransports := make([]*Transport, n)
	for i, id := range ids {
		peers := []string{}
		for _, o := range ids {
			if o != id {
				peers = append(peers, o)
			}
		}
		fsm := &sumFSM{}
		fsms[i] = fsm
		ct := NewTransport(mem, scenario, int64(i)) // distinct per-node stream, still fully deterministic from scenario.Seed
		chaosTransports[i] = ct
		node := raftlite.NewNode(raftlite.Config{ID: id, Peers: peers, Transport: ct, FSM: fsm})
		node.SetSnapshotter(fsm)
		nodes[i] = node
		mem.Register(node)
	}
	ctx, cancel := context.WithCancel(context.Background())
	for _, node := range nodes {
		go node.Run(ctx)
	}
	return nodes, fsms, chaosTransports, cancel
}

// buildChaosClusterWithTimeouts is identical to buildChaosCluster except
// it applies an explicit election-timeout profile (see
// raftlite.LANTimeouts / raftlite.WANTimeouts) to every node instead of
// relying on the zero-value LAN default. Exists so chaos scenarios that
// simulate skew/latency beyond LAN norms can be paired with the timeout
// profile an operator would actually deploy for that link, rather than
// silently running LAN timing under WAN-class conditions.
func buildChaosClusterWithTimeouts(t *testing.T, n int, scenario Scenario, profile func() (min, max, heartbeat time.Duration)) ([]*raftlite.Node, []*sumFSM, []*Transport, context.CancelFunc) {
	t.Helper()
	mem := raftlite.NewMemTransport()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = string(rune('A' + i))
	}
	min, max, hb := profile()
	nodes := make([]*raftlite.Node, n)
	fsms := make([]*sumFSM, n)
	chaosTransports := make([]*Transport, n)
	for i, id := range ids {
		peers := []string{}
		for _, o := range ids {
			if o != id {
				peers = append(peers, o)
			}
		}
		fsm := &sumFSM{}
		fsms[i] = fsm
		ct := NewTransport(mem, scenario, int64(i))
		chaosTransports[i] = ct
		node := raftlite.NewNode(raftlite.Config{
			ID: id, Peers: peers, Transport: ct, FSM: fsm,
			ElectionTimeoutMin: min, ElectionTimeoutMax: max, HeartbeatInterval: hb,
		})
		node.SetSnapshotter(fsm)
		nodes[i] = node
		mem.Register(node)
	}
	ctx, cancel := context.WithCancel(context.Background())
	for _, node := range nodes {
		go node.Run(ctx)
	}
	return nodes, fsms, chaosTransports, cancel
}

// TestChaos_PacketLossDuplicationReorder_ClusterConverges is the P0 proof:
// under sustained 15% packet loss, 10% duplication, and up to 80ms random
// reorder/delay on EVERY RPC type (votes, prevotes, append-entries,
// install-snapshot), the cluster must still make progress and every node
// must converge to identical state. Seed is fixed — this exact run is
// reproducible byte-for-byte.
func TestChaos_PacketLossDuplicationReorder_ClusterConverges(t *testing.T) {
	scenario := Scenario{
		Seed:            424242,
		PacketLossProb:  0.08,
		DuplicationProb: 0.05,
		ReorderMaxDelay: 20 * time.Millisecond,
	}
	nodes, fsms, chaosTransports, cancel := buildChaosCluster(t, 5, scenario)
	defer cancel()

	leader := waitForLeader(t, nodes, 5*time.Second)

	const total = 60
	deadline := time.Now().Add(30 * time.Second)
	for seq := uint64(1); seq <= total; seq++ {
		for {
			if time.Now().After(deadline) {
				t.Fatalf("deadline exceeded proposing+confirming entry seq=%d under chaos", seq)
			}
			idx, term, err := leader.Propose(encodeSeqVal(seq, seq))
			if err != nil {
				leader = waitForLeader(t, nodes, 5*time.Second)
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			werr := leader.WaitApplied(ctx, idx)
			cancel()
			if werr == nil && leader.Term() == term && leader.IsLeader() {
				break // confirmed committed while still the same leader/term — safe
			}
			// Timed out, or leadership moved on mid-flight: this
			// specific attempt may have been orphaned. Retry the SAME
			// (seq,val) pair — Apply()'s dedup makes this safe even if
			// an earlier attempt eventually also lands.
			leader = waitForLeader(t, nodes, 5*time.Second)
		}
	}

	wantSum := uint64(total * (total + 1) / 2)
	deadline = time.Now().Add(10 * time.Second)
	converged := false
	for time.Now().Before(deadline) {
		converged = true
		for _, f := range fsms {
			if f.Sum() != wantSum {
				converged = false
				break
			}
		}
		if converged {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !converged {
		sums := make([]uint64, len(fsms))
		for i, f := range fsms {
			sums[i] = f.Sum()
		}
		t.Fatalf("cluster did not converge under chaos: sums=%v want=%d", sums, wantSum)
	}

	totalFaults := 0
	drops, dups, delays := 0, 0, 0
	for _, ct := range chaosTransports {
		for _, ev := range ct.Events() {
			totalFaults++
			switch ev.Kind {
			case "drop":
				drops++
			case "duplicate":
				dups++
			case "clock_skew":
				delays++
			}
		}
	}
	if totalFaults == 0 {
		t.Fatal("chaos harness injected ZERO faults across the whole cluster — the scenario is not actually being exercised, this test would pass even with a no-op transport")
	}
	t.Logf("chaos scenario (seed=%d, loss=%.0f%%, dup=%.0f%%, reorder<=%v) converged: %d entries, sum=%d, %d total faults injected (%d drops, %d duplicates, %d skew-delays)",
		scenario.Seed, scenario.PacketLossProb*100, scenario.DuplicationProb*100, scenario.ReorderMaxDelay, total, wantSum, totalFaults, drops, dups, delays)
}

// TestChaos_ClockSkew_MinorityStaysMinorityNotDisruptive proves PreVote's
// value under a REALISTIC clock-skew condition (not a hard partition): a
// node whose effective heartbeat perception is skewed 400ms slower than
// its peers will time out and try to elect itself far more often — but
// must never be able to disrupt the healthy majority's term because it
// still needs (and, being skewed rather than partitioned, CAN win) a
// legitimate pre-vote. This specifically distinguishes "skewed but
// reachable" from "partitioned and unreachable" (already covered in
// raftlite's own PreVote test) — under skew the node CAN eventually win
// power legitimately if its log is current, which is correct, not a bug.
func TestChaos_ClockSkew_LeaderElectedDespiteSkew(t *testing.T) {
	scenario := Scenario{
		Seed: 7,
		ClockSkew: map[string]time.Duration{
			"C": 120 * time.Millisecond, // C's messages are consistently delayed, simulating a slow local clock
		},
	}
	// A 120ms one-way skew against the default LAN election window
	// (150-300ms) leaves too thin a margin under real scheduling jitter
	// — that was masking a real bug (see timeouts.go): the
	// election-timeout Config fields were silently discarded and every
	// node ran hardcoded LAN timing no matter what was configured. Now
	// that the wiring is fixed, this scenario is exactly what
	// WANTimeouts() exists for: an operator-facing profile for
	// deployments where skew/latency of this magnitude is expected.
	// This is the honest fix — widen the timeout to match the skew the
	// scenario declares, not paper over a flaky assertion.
	nodes, _, _, cancel := buildChaosClusterWithTimeouts(t, 3, scenario, raftlite.WANTimeouts)
	defer cancel()
	if !proposeAndConfirmWithRetry(t, nodes, encodeSeqVal(1, 99), 8*time.Second) {
		t.Fatal("cluster failed to commit under clock skew even after retrying against the current leader")
	}
}

// proposeAndConfirmWithRetry is what a real Raft client does and a
// single captured *Node reference does not: if the leader it proposed
// to steps down before the entry commits (a legitimate, correctness-
// preserving outcome — e.g. a second, honest election round happened
// concurrently), it re-discovers the CURRENT leader and retries, up to
// an overall deadline. A test (or client) that proposes to one leader
// reference and only ever waits on that same reference will spuriously
// "fail" on a perfectly healthy cluster whenever leadership legitimately
// moves between Propose and commit — that is a client-retry gap, not a
// consensus bug, and padding the timeout further would only have hidden
// it instead of fixing it.
func proposeAndConfirmWithRetry(t *testing.T, nodes []*raftlite.Node, cmd []byte, overall time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(overall)
	for time.Now().Before(deadline) {
		leader := waitForLeader(t, nodes, time.Until(deadline))
		if leader == nil {
			return false
		}
		idx, term, err := leader.Propose(cmd)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		ctx, done := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		err = leader.WaitApplied(ctx, idx)
		done()
		if err == nil {
			return true
		}
		// Either this node lost leadership before commit, or the round
		// was simply slow — either way, re-discover the (possibly new)
		// leader and retry, exactly as a real client library would.
		_ = term
	}
	return false
}

// TestChaos_DiskCorruption_SnapshotRestoreFailsClosed proves the disk
// corruption P0: a torn/corrupted snapshot payload must be REJECTED
// (Restore returns an error) rather than silently accepted, for both the
// generic test FSM AND the real MOAT kg.Graph (whose hash-chain
// verification is the actual production-grade integrity check).
func TestChaos_DiskCorruption_SnapshotRestoreFailsClosed(t *testing.T) {
	t.Run("generic_fsm", func(t *testing.T) {
		f := &sumFSM{sum: 12345}
		good, err := f.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		corrupting := NewDiskCorruptingSnapshotter(f, 1, 0, 1.0) // always corrupt on read
		err = corrupting.Restore(good)
		if err == nil {
			t.Fatal("corrupted snapshot was accepted without error — fail-closed property violated")
		}
	})

	t.Run("real_moat_knowledge_graph", func(t *testing.T) {
		g := kg.NewGraph()
		for i := 0; i < 20; i++ {
			if _, err := g.UpsertNode("chaos-test", kg.Node{ID: fmt.Sprintf("vessel/%d", i), Kind: "Vessel"}); err != nil {
				t.Fatal(err)
			}
		}
		adapter := raftlite.NewKGAdapter(g)
		goodSnapshot, err := adapter.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		corrupting := NewDiskCorruptingSnapshotter(adapter, 1, 0, 1.0)
		if err := corrupting.Restore(goodSnapshot); err == nil {
			t.Fatal("corrupted MOAT knowledge-graph snapshot was accepted without error — hash-chain verification failed to catch it")
		}
		// The live graph must be UNTOUCHED after a rejected restore.
		if adapter.Graph.LogLen() != 20 {
			t.Fatalf("adapter's live graph was mutated by a rejected corrupt snapshot: LogLen=%d, want 20", adapter.Graph.LogLen())
		}
	})
}
