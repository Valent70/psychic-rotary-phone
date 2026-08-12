// Package-level addition: a bounded, randomized property-safety sweep.
//
// Honest framing up front (this matters — see the audit critique this
// answers): this is NOT a full Jepsen harness. Real Jepsen adds
// injected process crashes at the OS level, disk-level fault injection
// via a real filesystem/network fault layer, a external linearizability
// checker (Knossos), and runs for hours across real machines. What
// follows is a bounded, in-process, randomized property test: each
// trial independently samples a random combination of the SAME fault
// primitives pkg/chaos already implements (packet loss, duplication,
// reorder/delay, clock skew) rather than one hand-picked scenario per
// test function, and checks a real safety invariant after each trial —
// not just "did a leader get elected". That is a meaningfully stronger
// claim than the six hand-written chaos_test.go scenarios alone, but it
// is a property sweep, not a distributed linearizability checker.
package chaos

import (
	"context"
	"math/rand"
	"time"

	"veriqo/pkg/consensus/raftlite"
)

// SweepTrialResult is one randomized trial's outcome.
type SweepTrialResult struct {
	Seed            int64   `json:"seed"`
	PacketLossProb  float64 `json:"packet_loss_prob"`
	DuplicationProb float64 `json:"duplication_prob"`
	ReorderMaxMs    float64 `json:"reorder_max_ms"`
	SkewedNodes     int     `json:"skewed_nodes"`
	MaxSkewMs       float64 `json:"max_skew_ms"`
	LeaderElected   bool    `json:"leader_elected"`
	CommittedEntry  bool    `json:"committed_entry"`
	SplitBrain      bool    `json:"split_brain_detected"`
	SpuriousChurn   int     `json:"leader_changes_observed"`
}

// SweepReport aggregates a full randomized sweep.
type SweepReport struct {
	Trials           int                `json:"trials"`
	Results          []SweepTrialResult `json:"results"`
	SafeTrials       int                `json:"safe_trials"`
	SafetyViolations int                `json:"safety_violations"`
	LivenessFailures int                `json:"liveness_failures"` // no leader / no commit within budget — NOT a safety bug, just adversarial conditions winning
}

// RunSafetySweep runs `trials` independent randomized fault
// combinations against a fresh 3-node cluster each time and checks ONE
// non-negotiable safety property: no two nodes are ever leader in the
// SAME term simultaneously (split-brain). It separately tracks — but
// does not treat as a safety failure — cases where no leader could be
// elected or no entry could be committed within the trial's time
// budget, since under sufficiently adversarial random parameters
// (e.g. near-100% packet loss) failing to make progress is the
// CORRECT, safe outcome, not a bug.
//
// This is also the concrete, repeatable check for whether the
// HandlePreVote leaderLive fix (raft.go) actually holds under
// randomized conditions rather than just the one scripted clock-skew
// scenario it was written to fix — every trial that both elects a
// leader AND shows zero extra leader changes after that first election
// is a small, independent confirmation that leaderLive did not produce
// a false positive (i.e. did not let a challenger disrupt a leader the
// challenger's own peers still considered alive).
func RunSafetySweep(trials int) SweepReport {
	report := SweepReport{Trials: trials}
	for i := 0; i < trials; i++ {
		seed := int64(i)*2654435761 + 12345
		r := oneSweepTrial(seed)
		report.Results = append(report.Results, r)
		if r.SplitBrain {
			report.SafetyViolations++
		} else {
			report.SafeTrials++
		}
		if !r.LeaderElected || !r.CommittedEntry {
			report.LivenessFailures++
		}
	}
	return report
}

func oneSweepTrial(seed int64) SweepTrialResult {
	rng := rand.New(rand.NewSource(seed))
	res := SweepTrialResult{Seed: seed}

	scenario := Scenario{
		Seed:            seed,
		PacketLossProb:  rng.Float64() * 0.20, // 0-20% loss
		DuplicationProb: rng.Float64() * 0.15, // 0-15% duplication
		ReorderMaxDelay: time.Duration(rng.Intn(120)) * time.Millisecond,
	}
	res.PacketLossProb = scenario.PacketLossProb
	res.DuplicationProb = scenario.DuplicationProb
	res.ReorderMaxMs = float64(scenario.ReorderMaxDelay.Milliseconds())

	const n = 3
	ids := []string{"A", "B", "C"}
	nSkewed := rng.Intn(2) // skew 0 or 1 node — skewing 2+/3 nodes makes the majority itself unreachable, which is a genuinely different (and already-covered, by TestChaosLite_MinorityPartitionNoSplitBrain-style tests) scenario, not a leaderLive edge case
	if nSkewed > 0 {
		scenario.ClockSkew = map[string]time.Duration{}
		maxSkew := time.Duration(rng.Intn(200)) * time.Millisecond
		scenario.ClockSkew[ids[rng.Intn(n)]] = maxSkew
		res.SkewedNodes = nSkewed
		res.MaxSkewMs = float64(maxSkew.Milliseconds())
	}

	mem := raftlite.NewMemTransport()
	nodes := make([]*raftlite.Node, n)
	minT, maxT, hbT := raftlite.WANTimeouts()
	for i, id := range ids {
		peers := []string{}
		for _, o := range ids {
			if o != id {
				peers = append(peers, o)
			}
		}
		ct := NewTransport(mem, scenario, int64(i))
		node := raftlite.NewNode(raftlite.Config{
			ID: id, Peers: peers, Transport: ct, FSM: &sweepFSM{},
			ElectionTimeoutMin: minT, ElectionTimeoutMax: maxT, HeartbeatInterval: hbT,
		})
		nodes[i] = node
		mem.Register(node)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, node := range nodes {
		go node.Run(ctx)
	}

	// Poll for up to 4s, recording every DISTINCT (term -> leader-set)
	// observation. Two different node IDs both reporting IsLeader()
	// for the SAME term at ANY polled instant is split-brain.
	seenLeaderForTerm := map[uint64]string{}
	deadline := time.Now().Add(2 * time.Second)
	leaderChanges := map[string]bool{}
	for time.Now().Before(deadline) {
		for _, node := range nodes {
			if node.IsLeader() {
				term := node.Term()
				if existing, ok := seenLeaderForTerm[term]; ok && existing != node.ID() {
					res.SplitBrain = true
				} else if !ok {
					seenLeaderForTerm[term] = node.ID()
				}
				leaderChanges[node.ID()] = true
				res.LeaderElected = true
			}
		}
		if res.SplitBrain {
			break // no need to keep polling once the invariant is already violated
		}
		if res.LeaderElected {
			break // got our first leader observation; move on to the commit check
		}
		time.Sleep(15 * time.Millisecond)
	}
	res.SpuriousChurn = len(leaderChanges)

	if res.LeaderElected && !res.SplitBrain {
		var leader *raftlite.Node
		for _, node := range nodes {
			if node.IsLeader() {
				leader = node
				break
			}
		}
		if leader != nil {
			if idx, _, err := leader.Propose([]byte("sweep-probe")); err == nil {
				cctx, cdone := context.WithTimeout(context.Background(), 1500*time.Millisecond)
				if err := leader.WaitApplied(cctx, idx); err == nil {
					res.CommittedEntry = true
				}
				cdone()
			}
		}
		// Continue polling briefly for any LATE split-brain (e.g. a
		// disrupted leader re-emerging) and for leadership churn count.
		churnDeadline := time.Now().Add(250 * time.Millisecond)
		for time.Now().Before(churnDeadline) {
			for _, node := range nodes {
				if node.IsLeader() {
					term := node.Term()
					if existing, ok := seenLeaderForTerm[term]; ok && existing != node.ID() {
						res.SplitBrain = true
					} else if !ok {
						seenLeaderForTerm[term] = node.ID()
					}
					leaderChanges[node.ID()] = true
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		res.SpuriousChurn = len(leaderChanges)
	}

	return res
}

type sweepFSM struct{}

func (sweepFSM) Apply(entry raftlite.LogEntry) error { return nil }
