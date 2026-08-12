package chaos

import (
	"errors"
	"testing"
)

func plan(seed uint64) FaultPlan {
	return FaultPlan{
		Nodes: []string{"n1", "n2", "n3", "n4", "n5"},
		Seed:  seed, Ticks: 90, FaultsPerTick: 0.35, HealAfter: 7, QuorumSize: 3,
		Kinds: []FaultKind{FaultPartition, FaultLatency, FaultLoss, FaultReorder,
			FaultDuplication, FaultNodeCrash, FaultNodeRestart, FaultDiskFailure,
			FaultClockSkew, FaultLeaderLoss, FaultMinorityIsolate},
	}
}

func TestPlanValidationRefusesMeaninglessExperiments(t *testing.T) {
	cases := map[string]func(*FaultPlan){
		"two nodes":   func(p *FaultPlan) { p.Nodes = []string{"a", "b"} },
		"zero ticks":  func(p *FaultPlan) { p.Ticks = 0 },
		"no kinds":    func(p *FaultPlan) { p.Kinds = nil },
		"weak quorum": func(p *FaultPlan) { p.QuorumSize = 2 },
		"never heals": func(p *FaultPlan) { p.HealAfter = 0 },
	}
	for name, mutate := range cases {
		p := plan(1)
		mutate(&p)
		if _, err := Schedule(p); !errors.Is(err, ErrPlanInvalid) {
			t.Fatalf("%s must be refused, got %v", name, err)
		}
	}
}

func TestScheduleIsDeterministicForASeed(t *testing.T) {
	a, err := Schedule(plan(42))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		b, _ := Schedule(plan(42))
		if len(a) != len(b) {
			t.Fatalf("schedule length must be stable: %d vs %d", len(a), len(b))
		}
		for j := range a {
			if faultKey(a[j]) != faultKey(b[j]) {
				t.Fatalf("schedule diverged at %d for the same seed", j)
			}
		}
	}
	c, _ := Schedule(plan(43))
	same := len(c) == len(a)
	if same {
		for j := range a {
			if faultKey(a[j]) != faultKey(c[j]) {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatal("a different seed must produce a different schedule")
	}
}

func TestEveryFaultKindIsExercisedAcrossSeeds(t *testing.T) {
	seen := map[FaultKind]bool{}
	for seed := uint64(0); seed < 25; seed++ {
		s, err := Schedule(plan(seed))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range s {
			seen[f.Kind] = true
		}
	}
	for _, want := range plan(0).Kinds {
		if !seen[want] {
			t.Fatalf("fault kind %s was never scheduled across 25 seeds", want)
		}
	}
	if !seen[FaultHeal] {
		t.Fatal("every fault must be followed by a scheduled heal")
	}
}

func TestFaultsAreAlwaysHealed(t *testing.T) {
	s, err := Schedule(plan(7))
	if err != nil {
		t.Fatal(err)
	}
	var faults, heals int
	for _, f := range s {
		if f.Kind == FaultHeal {
			heals++
		} else {
			faults++
		}
	}
	if heals == 0 || heals < faults/2 {
		t.Fatalf("recovery must be scheduled, got %d heals for %d faults", heals, faults)
	}
}

func TestRunHoldsEveryInvariantAcrossManySeeds(t *testing.T) {
	for seed := uint64(0); seed < 25; seed++ {
		rep, err := Run(plan(seed))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if err := rep.Err(); err != nil {
			t.Fatalf("seed %d violated an invariant: %v", seed, err)
		}
		if rep.FaultsApplied == 0 {
			t.Fatalf("seed %d applied no faults; the experiment proved nothing", seed)
		}
	}
}

func TestQuorumRefusalHappensUnderPartition(t *testing.T) {
	// A plan that partitions aggressively must produce at least some
	// refusals: a run where every proposal commits has not actually
	// tested the availability/consistency trade-off.
	p := plan(11)
	p.Kinds = []FaultKind{FaultPartition, FaultMinorityIsolate, FaultNodeCrash}
	p.FaultsPerTick = 0.9
	p.HealAfter = 25
	rep, err := Run(p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Refusals == 0 {
		t.Fatal("under heavy partition some proposals must be refused for lack of quorum")
	}
	if rep.Commits == 0 {
		t.Fatal("the cluster must still make progress when quorum is available")
	}
	if err := rep.Err(); err != nil {
		t.Fatalf("consistency must hold even when availability does not: %v", err)
	}
}

func TestRunIsBitForBitReplayable(t *testing.T) {
	a, err := Run(plan(99))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Run(plan(99))
	if err != nil {
		t.Fatal(err)
	}
	if a.RunHash != b.RunHash {
		t.Fatalf("the same seed must replay identically: %s vs %s", a.RunHash, b.RunHash)
	}
	if a.Commits != b.Commits || a.Refusals != b.Refusals {
		t.Fatal("commit and refusal counts must be reproducible")
	}
	c, _ := Run(plan(100))
	if c.RunHash == a.RunHash {
		t.Fatal("a different seed must produce a different run hash")
	}
}

func TestConvergenceRestoresEveryNodeAfterHealing(t *testing.T) {
	p := plan(5)
	c, err := NewCluster(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Apply(Fault{Tick: 1, Kind: FaultPartition, Nodes: []string{"n4", "n5"}})
	for i := 0; i < 5; i++ {
		if !c.Propose("value") {
			t.Fatal("a 3-node majority must still commit")
		}
	}
	// The isolated nodes are legitimately behind, so no violation yet.
	if v := c.CheckInvariants(1); len(v) != 0 {
		t.Fatalf("a lagging isolated node is not a violation: %+v", v)
	}
	c.Apply(Fault{Tick: 10, Kind: FaultHeal, Nodes: []string{"n4", "n5"}})
	c.Converge()
	if v := c.CheckInvariants(10); len(v) != 0 {
		t.Fatalf("after healing and convergence every node must agree: %+v", v)
	}
	for _, id := range c.order {
		if len(c.nodes[id].committed) != 5 {
			t.Fatalf("node %s has %d committed entries, expected 5", id, len(c.nodes[id].committed))
		}
	}
}

func TestDivergenceIsDetectedWhenItIsInjected(t *testing.T) {
	// The invariant checker must be capable of failing; a checker that
	// can only pass proves nothing. Here divergence is forced directly.
	c, err := NewCluster(plan(1))
	if err != nil {
		t.Fatal(err)
	}
	c.Propose("agreed")
	c.nodes["n3"].committed[1] = "forged"
	v := c.CheckInvariants(1)
	found := false
	for _, x := range v {
		if x.Invariant == "no_divergence" {
			found = true
		}
	}
	if !found {
		t.Fatalf("injected divergence must be detected: %+v", v)
	}
}

func TestTwoLeadersAreDetected(t *testing.T) {
	c, _ := NewCluster(plan(1))
	c.nodes["n1"].leader = true
	c.nodes["n2"].leader = true
	found := false
	for _, x := range c.CheckInvariants(1) {
		if x.Invariant == "single_leader_per_term" {
			found = true
		}
	}
	if !found {
		t.Fatal("two leaders in one term must be detected")
	}
}

func TestCommittedLossIsDetected(t *testing.T) {
	c, _ := NewCluster(plan(1))
	c.Propose("v1")
	delete(c.nodes["n2"].committed, 1)
	found := false
	for _, x := range c.CheckInvariants(1) {
		if x.Invariant == "no_committed_loss" {
			found = true
		}
	}
	if !found {
		t.Fatal("losing a committed entry on a joined node must be detected")
	}
}

func TestLeaderLossTriggersReElection(t *testing.T) {
	c, _ := NewCluster(plan(3))
	first := c.leader()
	if first == nil {
		t.Fatal("a cluster must start with a leader")
	}
	termBefore := c.term
	c.Apply(Fault{Tick: 1, Kind: FaultLeaderLoss})
	if c.term <= termBefore {
		t.Fatal("losing the leader must advance the term")
	}
	next := c.leader()
	if next == nil || next.id == first.id {
		t.Fatalf("a new leader must be elected, got %v", next)
	}
	if len(c.CheckInvariants(1)) != 0 {
		t.Fatal("re-election must not break any invariant")
	}
}

func TestDiskFailureDoesNotLoseCommittedState(t *testing.T) {
	c, _ := NewCluster(plan(2))
	c.Propose("durable")
	c.Apply(Fault{Tick: 1, Kind: FaultDiskFailure, Nodes: []string{"n2", "n3"}})
	for _, x := range c.CheckInvariants(1) {
		if x.Invariant == "no_committed_loss" {
			t.Fatalf("a disk fault must not lose committed state: %+v", x)
		}
	}
}

func TestReportErrorNamesTheSeedForReproduction(t *testing.T) {
	r := Report{Plan: plan(1234),
		Violations: []Violation{{Invariant: "no_divergence", Detail: "x", Tick: 9}}}
	err := r.Err()
	if err == nil || !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("violations must produce an error, got %v", err)
	}
	if !contains(err.Error(), "1234") {
		t.Fatalf("the failing seed must be reported so the run can be reproduced: %v", err)
	}
}

func faultKey(f Fault) string {
	return string(f.Kind) + "|" + f.Detail + "|" +
		strconvItoa(int(f.Tick)) + "|" + strconvItoa(f.Magnitude) + "|" + joinNodes(f.Nodes)
}

func joinNodes(n []string) string {
	out := ""
	for _, x := range n {
		out += x + ","
	}
	return out
}

func strconvItoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
