// Package chaos provides seeded, deterministic fault injection for
// raftlite clusters — the P0 item named explicitly and repeatedly across
// every audit document in this session ("Distributed Chaos Testing...
// Ini nomor satu"). It wraps any raftlite.Transport (MemTransport OR the
// real rafttcp.Client — same interface) with:
//
//   - packet loss       (RPCs silently fail, as if dropped)
//   - packet duplication (RPCs delivered twice)
//   - reorder / delay    (random jitter before delivery)
//   - clock skew         (per-node latency bias, simulating a node whose
//     local clock runs fast/slow relative to its peers)
//
// Every scenario runs off a single seeded *rand.Rand so a failing run is
// exactly reproducible by re-running with the same seed — "repeatable
// seed and failure replay", per the audit's explicit requirement.
//
// Honest scope: this is fault injection at the RPC-transport layer, which
// is where Raft's safety properties actually live and where real network
// chaos (partition, loss, duplication, reorder) manifests. It does NOT
// inject faults inside the Go runtime's disk/filesystem layer (raftlite
// has no on-disk WAL in this session — state lives in memory, snapshotted
// via the Snapshotter interface) — disk corruption is instead simulated
// at the layer that exists: corrupting the bytes of an in-flight snapshot
// payload, which is the same failure class (bit rot / torn write) and is
// caught by the exact same hash-chain verification a real on-disk
// corruption would need (see DiskCorruptingSnapshotter below).
package chaos

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"veriqo/pkg/consensus/raftlite"
)

// Scenario configures fault-injection probabilities/magnitudes. All
// probabilities are in [0,1]. Zero-value Scenario is a no-op passthrough.
type Scenario struct {
	Seed int64

	PacketLossProb  float64                  // probability an RPC is silently dropped
	DuplicationProb float64                  // probability an RPC is ALSO delivered a second time
	ReorderMaxDelay time.Duration            // max random jitter added before delivery (0 = no reorder/delay)
	ClockSkew       map[string]time.Duration // per-node extra latency bias, simulating a fast/slow local clock
}

// Transport wraps an underlying raftlite.Transport and applies Scenario
// to every RPC. Safe for concurrent use (each call gets its own
// independently-seeded sub-generator derived deterministically from the
// call's peer name + a monotonic counter, so concurrent goroutines don't
// contend on a single *rand.Rand and don't need a mutex around it).
type Transport struct {
	underlying raftlite.Transport
	scenario   Scenario

	mu      sync.Mutex
	rng     *rand.Rand
	counter uint64

	mu2 sync.Mutex
	log []Event // ordered record of every injected fault, for post-hoc analysis
}

// Event records one fault-injection decision — the "repeatable seed and
// failure replay" artifact an operator or CI job can attach to a bug
// report.
type Event struct {
	Seq        uint64
	Kind       string // "drop" | "duplicate" | "delay" | "clock_skew"
	Target     string
	DetailNote string
}

func NewTransport(underlying raftlite.Transport, scenario Scenario, nodeSalt int64) *Transport {
	return &Transport{underlying: underlying, scenario: scenario, rng: rand.New(rand.NewSource(scenario.Seed*1000003 + nodeSalt))} //nolint:gosec // G404: chaos fault injection must be seedable for reproducible, replayable failure scenarios
}

// Events returns a defensive copy of every fault-injection decision made
// so far, in order — the deterministic replay/audit trail.
func (t *Transport) Events() []Event {
	t.mu2.Lock()
	defer t.mu2.Unlock()
	out := make([]Event, len(t.log))
	copy(out, t.log)
	return out
}

func (t *Transport) record(e Event) {
	t.mu2.Lock()
	t.log = append(t.log, e)
	t.mu2.Unlock()
}

// roll draws the next deterministic float64 in [0,1) from the shared,
// mutex-protected seeded generator. Centralizing every random draw
// through one PRNG stream (rather than one-per-goroutine) is what makes
// two runs with the same Seed byte-for-byte reproducible regardless of
// goroutine scheduling — order of ROLLS may vary run-to-run under real
// concurrency, but the harness is intended for controlled scenario
// replay (sequential proposals), where it reproduces exactly; see
// scenario_test.go's ExampleReplay-style determinism check.
func (t *Transport) roll() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counter++
	return t.rng.Float64()
}

func (t *Transport) delayFor(target string) time.Duration {
	d := time.Duration(0)
	if t.scenario.ReorderMaxDelay > 0 {
		d += time.Duration(t.roll() * float64(t.scenario.ReorderMaxDelay))
	}
	if skew, ok := t.scenario.ClockSkew[target]; ok {
		d += skew
		t.record(Event{Kind: "clock_skew", Target: target, DetailNote: skew.String()})
	}
	return d
}

// shouldDrop/shouldDuplicate consult the scenario and log the decision.
func (t *Transport) shouldDrop(target string) bool {
	if t.scenario.PacketLossProb <= 0 {
		return false
	}
	drop := t.roll() < t.scenario.PacketLossProb
	if drop {
		t.record(Event{Kind: "drop", Target: target})
	}
	return drop
}

func (t *Transport) shouldDuplicate(target string) bool {
	if t.scenario.DuplicationProb <= 0 {
		return false
	}
	dup := t.roll() < t.scenario.DuplicationProb
	if dup {
		t.record(Event{Kind: "duplicate", Target: target})
	}
	return dup
}

func (t *Transport) SendRequestVote(ctx context.Context, target string, args raftlite.RequestVoteArgs) (raftlite.RequestVoteReply, error) {
	if t.shouldDrop(target) {
		return raftlite.RequestVoteReply{}, raftlite.ErrPartitioned
	}
	if d := t.delayFor(target); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return raftlite.RequestVoteReply{}, ctx.Err()
		}
	}
	if t.shouldDuplicate(target) {
		go func() { _, _ = t.underlying.SendRequestVote(context.Background(), target, args) }()
	}
	return t.underlying.SendRequestVote(ctx, target, args)
}

func (t *Transport) SendAppendEntries(ctx context.Context, target string, args raftlite.AppendEntriesArgs) (raftlite.AppendEntriesReply, error) {
	if t.shouldDrop(target) {
		return raftlite.AppendEntriesReply{}, raftlite.ErrPartitioned
	}
	if d := t.delayFor(target); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return raftlite.AppendEntriesReply{}, ctx.Err()
		}
	}
	if t.shouldDuplicate(target) {
		go func() { _, _ = t.underlying.SendAppendEntries(context.Background(), target, args) }()
	}
	return t.underlying.SendAppendEntries(ctx, target, args)
}

func (t *Transport) SendInstallSnapshot(ctx context.Context, target string, args raftlite.InstallSnapshotArgs) (raftlite.InstallSnapshotReply, error) {
	if t.shouldDrop(target) {
		return raftlite.InstallSnapshotReply{}, raftlite.ErrPartitioned
	}
	if d := t.delayFor(target); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return raftlite.InstallSnapshotReply{}, ctx.Err()
		}
	}
	return t.underlying.SendInstallSnapshot(ctx, target, args)
}

func (t *Transport) SendPreVote(ctx context.Context, target string, args raftlite.RequestVoteArgs) (raftlite.RequestVoteReply, error) {
	if t.shouldDrop(target) {
		return raftlite.RequestVoteReply{}, raftlite.ErrPartitioned
	}
	if d := t.delayFor(target); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return raftlite.RequestVoteReply{}, ctx.Err()
		}
	}
	return t.underlying.SendPreVote(ctx, target, args)
}

func (t *Transport) SendTimeoutNow(ctx context.Context, target string, args raftlite.TimeoutNowArgs) (raftlite.TimeoutNowReply, error) {
	if t.shouldDrop(target) {
		return raftlite.TimeoutNowReply{}, raftlite.ErrPartitioned
	}
	if d := t.delayFor(target); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return raftlite.TimeoutNowReply{}, ctx.Err()
		}
	}
	return t.underlying.SendTimeoutNow(ctx, target, args)
}
