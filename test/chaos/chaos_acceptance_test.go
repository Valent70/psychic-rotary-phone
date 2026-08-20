// Package chaos is the V7.11.1 P0-2 formal acceptance layer for
// distributed fault tolerance.
//
// The audit asked for an acceptance suite that genuinely runs multi-node
// partition, latency, loss, reorder, duplication, recovery and
// convergence — not unit tests that name those words. Each test below
// drives pkg/chaos's deterministic orchestrator over a five-node cluster
// and asserts the cluster invariants at every tick, then writes an
// evidence artifact naming the seeds that were executed.
//
// Determinism is the acceptance property that makes the rest useful: a
// failure here is reported with the seed that produced it, and rerunning
// that seed reproduces the exact fault schedule.
package chaos

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	engine "veriqo/pkg/chaos"
)

func basePlan(seed uint64, kinds []engine.FaultKind) engine.FaultPlan {
	return engine.FaultPlan{
		Nodes: []string{"n1", "n2", "n3", "n4", "n5"},
		Seed:  seed, Ticks: 120, FaultsPerTick: 0.4, HealAfter: 8, QuorumSize: 3,
		Kinds: kinds,
	}
}

// AcceptanceRecord is the evidence artifact for one scenario.
type AcceptanceRecord struct {
	Scenario   string   `json:"scenario"`
	Seeds      []uint64 `json:"seeds"`
	Nodes      int      `json:"nodes"`
	Quorum     int      `json:"quorum"`
	TicksEach  uint64   `json:"ticks_each"`
	Faults     int      `json:"faults_applied"`
	Commits    int      `json:"commits"`
	Refusals   int      `json:"refusals"`
	Violations int      `json:"violations"`
	RunHashes  []string `json:"run_hashes"`
	Passed     bool     `json:"passed"`
}

var collected []AcceptanceRecord

func runScenario(t *testing.T, name string, kinds []engine.FaultKind, seeds []uint64) AcceptanceRecord {
	t.Helper()
	rec := AcceptanceRecord{Scenario: name, Seeds: seeds, Nodes: 5, Quorum: 3, TicksEach: 120}
	for _, seed := range seeds {
		rep, err := engine.Run(basePlan(seed, kinds))
		if err != nil {
			t.Fatalf("%s seed %d: %v", name, seed, err)
		}
		rec.Faults += rep.FaultsApplied
		rec.Commits += rep.Commits
		rec.Refusals += rep.Refusals
		rec.Violations += len(rep.Violations)
		rec.RunHashes = append(rec.RunHashes, rep.RunHash)
		if err := rep.Err(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if rec.Faults == 0 {
		t.Fatalf("%s applied no faults; the scenario proved nothing", name)
	}
	rec.Passed = rec.Violations == 0
	collected = append(collected, rec)
	return rec
}

func TestNetworkPartitionAcceptance(t *testing.T) {
	rec := runScenario(t, "network_partition",
		[]engine.FaultKind{engine.FaultPartition, engine.FaultMinorityIsolate},
		[]uint64{1, 2, 3, 4, 5})
	if rec.Refusals == 0 {
		t.Fatal("a partition scenario must at some point lack quorum and refuse to commit")
	}
	if rec.Commits == 0 {
		t.Fatal("a majority partition must still make progress")
	}
}

func TestLatencyLossReorderDuplicationAcceptance(t *testing.T) {
	// These four faults degrade delivery without removing quorum, so the
	// cluster must remain both available and consistent throughout.
	rec := runScenario(t, "delivery_degradation",
		[]engine.FaultKind{engine.FaultLatency, engine.FaultJitter, engine.FaultLoss,
			engine.FaultReorder, engine.FaultDuplication},
		[]uint64{11, 12, 13, 14, 15})
	if rec.Commits == 0 {
		t.Fatal("degraded delivery must not stop progress entirely")
	}
}

func TestCrashRestartAndRecoveryAcceptance(t *testing.T) {
	runScenario(t, "crash_restart_recovery",
		[]engine.FaultKind{engine.FaultNodeCrash, engine.FaultNodeRestart,
			engine.FaultDiskFailure, engine.FaultLeaderLoss},
		[]uint64{21, 22, 23, 24, 25})
}

func TestClockSkewAcceptance(t *testing.T) {
	// Clock skew must not affect outcomes at all: the whole repo is
	// tick-ordered rather than wall-clock ordered, and this is the test
	// that would catch a regression back to time.Now().
	runScenario(t, "clock_skew",
		[]engine.FaultKind{engine.FaultClockSkew, engine.FaultLatency},
		[]uint64{31, 32, 33})
}

func TestCombinedChaosAcceptance(t *testing.T) {
	runScenario(t, "combined",
		[]engine.FaultKind{engine.FaultPartition, engine.FaultLatency, engine.FaultLoss,
			engine.FaultReorder, engine.FaultDuplication, engine.FaultNodeCrash,
			engine.FaultNodeRestart, engine.FaultDiskFailure, engine.FaultClockSkew,
			engine.FaultLeaderLoss, engine.FaultMinorityIsolate},
		[]uint64{41, 42, 43, 44, 45, 46, 47, 48})
}

func TestConvergenceAfterFullHeal(t *testing.T) {
	plan := basePlan(77, []engine.FaultKind{engine.FaultPartition, engine.FaultNodeCrash,
		engine.FaultMinorityIsolate})
	c, err := engine.NewCluster(plan)
	if err != nil {
		t.Fatal(err)
	}
	c.Apply(engine.Fault{Tick: 1, Kind: engine.FaultPartition, Nodes: []string{"n4", "n5"}})
	for i := 0; i < 20; i++ {
		if !c.Propose("v") {
			t.Fatalf("the majority side must commit at iteration %d", i)
		}
	}
	c.Apply(engine.Fault{Tick: 30, Kind: engine.FaultHeal, Nodes: []string{"n4", "n5"}})
	c.Converge()
	if v := c.CheckInvariants(30); len(v) != 0 {
		t.Fatalf("convergence must leave every node consistent: %+v", v)
	}
}

func TestChaosRunsAreReproducibleFromTheirSeed(t *testing.T) {
	kinds := []engine.FaultKind{engine.FaultPartition, engine.FaultLoss, engine.FaultNodeCrash}
	a, err := engine.Run(basePlan(4242, kinds))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, err := engine.Run(basePlan(4242, kinds))
		if err != nil {
			t.Fatal(err)
		}
		if a.RunHash != b.RunHash {
			t.Fatalf("run %d diverged from the same seed: %s vs %s", i, a.RunHash, b.RunHash)
		}
	}
}

// TestMain writes the consolidated acceptance evidence after every
// scenario has run, so the artifact reflects the whole layer rather than
// whichever test happened to finish last.
func TestMain(m *testing.M) {
	code := m.Run()
	if len(collected) > 0 {
		dir := filepath.Join("..", "..", "evidence")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			if b, err := json.MarshalIndent(collected, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "chaos-acceptance.json"), append(b, '\n'), 0o644)
			}
		}
	}
	os.Exit(code)
}
