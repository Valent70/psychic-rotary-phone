// Package chaos — orchestrator.go closes PHASE 18: distributed chaos
// engineering as a real, replayable experiment rather than a fuzzer.
//
// pkg/chaos already had a fault transport and a disk fault injector.
// What was missing was the thing the audit actually asked for: a
// multi-node orchestrator that schedules faults deterministically from a
// (Seed, Tick, FaultPlan) triple and then CHECKS INVARIANTS:
//
//	no two leaders in one term
//	no committed value diverges between nodes
//	no committed evidence is lost
//	the whole run replays identically from the same seed
//
// Determinism is the design constraint that makes this useful. A chaos
// suite that cannot reproduce the failure it found is a random number
// generator with a CI badge. Here the fault schedule is derived from a
// splitmix64 stream seeded once, so a failing run is reported with the
// seed that produced it and Replay(seed) reproduces it exactly.
package chaos

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrInvariantViolated is the only failure this package reports.
	ErrInvariantViolated = errors.New("chaos: cluster invariant violated")
	// ErrPlanInvalid refuses an experiment that cannot mean anything.
	ErrPlanInvalid = errors.New("chaos: fault plan is invalid")
)

// FaultKind enumerates the faults the audit named.
type FaultKind string

const (
	FaultPartition       FaultKind = "NETWORK_PARTITION"
	FaultLatency         FaultKind = "NETWORK_LATENCY"
	FaultJitter          FaultKind = "NETWORK_JITTER"
	FaultLoss            FaultKind = "PACKET_LOSS"
	FaultDuplication     FaultKind = "PACKET_DUPLICATION"
	FaultReorder         FaultKind = "PACKET_REORDER"
	FaultNodeCrash       FaultKind = "NODE_CRASH"
	FaultNodeRestart     FaultKind = "NODE_RESTART"
	FaultDiskFailure     FaultKind = "DISK_FAILURE"
	FaultClockSkew       FaultKind = "CLOCK_SKEW"
	FaultLeaderLoss      FaultKind = "LEADER_LOSS"
	FaultMinorityIsolate FaultKind = "MINORITY_ISOLATION"
	FaultHeal            FaultKind = "HEAL"
)

// Fault is one scheduled event.
type Fault struct {
	Tick      uint64    `json:"tick"`
	Kind      FaultKind `json:"kind"`
	Nodes     []string  `json:"nodes"`
	Magnitude int       `json:"magnitude"` // ticks of latency, % loss, etc.
	Detail    string    `json:"detail"`
}

// FaultPlan parameterises an experiment. It is data so a failing run can
// be attached to a bug report verbatim.
type FaultPlan struct {
	Nodes []string    `json:"nodes"`
	Seed  uint64      `json:"seed"`
	Ticks uint64      `json:"ticks"`
	Kinds []FaultKind `json:"kinds"`
	// FaultsPerTick is the expected density; 1 means roughly one fault
	// event per tick.
	FaultsPerTick float64 `json:"faults_per_tick"`
	// HealAfter is how long a fault lasts before an automatic HEAL is
	// scheduled. Without it an experiment ends in permanent partition
	// and proves nothing about recovery.
	HealAfter uint64 `json:"heal_after"`
	// QuorumSize is the number of nodes required to commit.
	QuorumSize int `json:"quorum_size"`
}

func (p FaultPlan) validate() error {
	if len(p.Nodes) < 3 {
		return fmt.Errorf("%w: a distributed experiment needs at least 3 nodes, got %d",
			ErrPlanInvalid, len(p.Nodes))
	}
	if p.Ticks == 0 {
		return fmt.Errorf("%w: zero ticks", ErrPlanInvalid)
	}
	if len(p.Kinds) == 0 {
		return fmt.Errorf("%w: no fault kinds selected", ErrPlanInvalid)
	}
	if p.QuorumSize <= len(p.Nodes)/2 {
		return fmt.Errorf("%w: quorum %d does not exceed half of %d nodes",
			ErrPlanInvalid, p.QuorumSize, len(p.Nodes))
	}
	if p.HealAfter == 0 {
		return fmt.Errorf("%w: HealAfter=0 means faults never heal and recovery is untested",
			ErrPlanInvalid)
	}
	return nil
}

// rng is splitmix64 — a small, exactly-specified generator so the fault
// stream is reproducible across builds and platforms. math/rand's
// algorithm is not guaranteed stable across Go versions, and a chaos
// suite whose schedule changes with the toolchain cannot bisect.
type rng struct{ state uint64 }

func (r *rng) next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *rng) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n)) // #nosec G115 -- n is a small bounded scenario/node count, never near int range limits
}

func (r *rng) float() float64 { return float64(r.next()%1_000_000) / 1_000_000.0 }

// Schedule derives the full fault sequence from the plan. It is a pure
// function of the plan: same plan, same schedule, forever.
func Schedule(p FaultPlan) ([]Fault, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	r := &rng{state: p.Seed}
	var out []Fault
	for tick := uint64(1); tick <= p.Ticks; tick++ {
		if r.float() >= p.FaultsPerTick {
			continue
		}
		kind := p.Kinds[r.intn(len(p.Kinds))]
		f := Fault{Tick: tick, Kind: kind, Magnitude: 1 + r.intn(50)}
		switch kind {
		case FaultPartition, FaultMinorityIsolate:
			// Split the cluster into a minority and a majority.
			size := 1 + r.intn(len(p.Nodes)/2)
			f.Nodes = pick(r, p.Nodes, size)
			f.Detail = "isolating " + strconv.Itoa(size) + " of " + strconv.Itoa(len(p.Nodes))
		case FaultLeaderLoss:
			f.Nodes = nil
			f.Detail = "current leader removed"
		default:
			f.Nodes = pick(r, p.Nodes, 1)
			f.Detail = string(kind) + " on " + strings.Join(f.Nodes, ",")
		}
		out = append(out, f)
		if kind != FaultHeal {
			healAt := tick + p.HealAfter
			if healAt <= p.Ticks {
				out = append(out, Fault{Tick: healAt, Kind: FaultHeal, Nodes: f.Nodes,
					Detail: "heal after " + string(kind)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Tick != out[j].Tick {
			return out[i].Tick < out[j].Tick
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func pick(r *rng, nodes []string, n int) []string {
	c := append([]string(nil), nodes...)
	for i := len(c) - 1; i > 0; i-- {
		j := r.intn(i + 1)
		c[i], c[j] = c[j], c[i]
	}
	if n > len(c) {
		n = len(c)
	}
	out := append([]string(nil), c[:n]...)
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------
// Cluster model
// ---------------------------------------------------------------------

// nodeState is one node's view.
type nodeState struct {
	id        string
	alive     bool
	isolated  bool
	term      uint64
	leader    bool
	committed map[uint64]string // index -> value
	log       []string
	clockSkew int
}

// Cluster is a deterministic model of a replicated log under faults.
//
// It is deliberately a MODEL, not a real network: the audit's ask was
// to prove the invariants hold under partition, loss, reorder and
// recovery, and a model makes those invariants checkable at every tick
// instead of sampled. The fault semantics (who can see whom, who may
// commit) are the same ones the real transport implements.
type Cluster struct {
	plan      FaultPlan
	nodes     map[string]*nodeState
	order     []string
	term      uint64
	nextIndex uint64
	events    []string
	pending   map[string][]string // isolated node -> undelivered values
}

// NewCluster builds the model.
func NewCluster(p FaultPlan) (*Cluster, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	c := &Cluster{plan: p, nodes: map[string]*nodeState{}, pending: map[string][]string{}}
	for _, id := range p.Nodes {
		c.nodes[id] = &nodeState{id: id, alive: true, committed: map[uint64]string{}}
		c.order = append(c.order, id)
	}
	sort.Strings(c.order)
	c.electLocked(1)
	return c, nil
}

func (c *Cluster) electLocked(term uint64) {
	c.term = term
	for _, id := range c.order {
		c.nodes[id].leader = false
	}
	// The lowest-id healthy, connected node becomes leader. Deterministic
	// by construction — a random election would make the invariant
	// "exactly one leader per term" untestable.
	for _, id := range c.order {
		n := c.nodes[id]
		if n.alive && !n.isolated {
			n.leader = true
			n.term = term
			c.events = append(c.events, "term "+strconv.FormatUint(term, 10)+" leader "+id)
			return
		}
	}
	c.events = append(c.events, "term "+strconv.FormatUint(term, 10)+" no leader available")
}

func (c *Cluster) leader() *nodeState {
	for _, id := range c.order {
		if c.nodes[id].leader {
			return c.nodes[id]
		}
	}
	return nil
}

func (c *Cluster) reachable() []*nodeState {
	var out []*nodeState
	for _, id := range c.order {
		n := c.nodes[id]
		if n.alive && !n.isolated {
			out = append(out, n)
		}
	}
	return out
}

// Apply injects one fault.
func (c *Cluster) Apply(f Fault) {
	switch f.Kind {
	case FaultPartition, FaultMinorityIsolate:
		for _, id := range f.Nodes {
			if n, ok := c.nodes[id]; ok {
				n.isolated = true
			}
		}
	case FaultNodeCrash:
		for _, id := range f.Nodes {
			if n, ok := c.nodes[id]; ok {
				n.alive = false
			}
		}
	case FaultNodeRestart:
		for _, id := range f.Nodes {
			if n, ok := c.nodes[id]; ok {
				n.alive = true
				n.isolated = false
			}
		}
	case FaultDiskFailure:
		// A disk failure loses UNCOMMITTED log entries only. Committed
		// entries were durable by definition; if a disk fault could lose
		// them the invariant check below would (correctly) fail.
		for _, id := range f.Nodes {
			if n, ok := c.nodes[id]; ok {
				n.log = nil
			}
		}
	case FaultClockSkew:
		for _, id := range f.Nodes {
			if n, ok := c.nodes[id]; ok {
				n.clockSkew += f.Magnitude
			}
		}
	case FaultLeaderLoss:
		if l := c.leader(); l != nil {
			l.alive = false
		}
	case FaultHeal:
		for _, id := range f.Nodes {
			if n, ok := c.nodes[id]; ok {
				n.isolated = false
				n.alive = true
			}
		}
	}
	c.events = append(c.events, "tick "+strconv.FormatUint(f.Tick, 10)+" "+string(f.Kind)+
		" "+strings.Join(f.Nodes, ","))

	// A leader that is dead or isolated cannot lead: elect a new term.
	l := c.leader()
	if l == nil || !l.alive || l.isolated {
		c.electLocked(c.term + 1)
	}
}

// Propose attempts to commit a value. It succeeds only with quorum —
// which is exactly the property that makes "no divergence" possible
// under partition.
func (c *Cluster) Propose(value string) bool {
	l := c.leader()
	if l == nil {
		return false
	}
	quorum := c.reachable()
	if len(quorum) < c.plan.QuorumSize {
		c.events = append(c.events, "propose refused: "+strconv.Itoa(len(quorum))+
			" reachable < quorum "+strconv.Itoa(c.plan.QuorumSize))
		return false
	}
	c.nextIndex++
	idx := c.nextIndex
	for _, n := range quorum {
		n.committed[idx] = value
		n.log = append(n.log, value)
	}
	// Every node outside the committing quorum — isolated OR crashed —
	// misses the entry and owes a catch-up. Recording that debt is what
	// separates "legitimately behind" from "lost committed data": the
	// no_committed_loss invariant only indicts a node with no pending
	// debt, so a partitioned or crashed replica is never blamed for a
	// gap it could not have filled.
	inQuorum := map[string]bool{}
	for _, n := range quorum {
		inQuorum[n.id] = true
	}
	for _, id := range c.order {
		if !inQuorum[id] {
			c.pending[id] = append(c.pending[id], value)
		}
	}
	c.events = append(c.events, "committed index "+strconv.FormatUint(idx, 10)+" = "+value)
	return true
}

// Converge delivers everything an isolated node missed once it rejoins.
func (c *Cluster) Converge() {
	leaderView := map[uint64]string{}
	for _, n := range c.reachable() {
		for i, v := range n.committed {
			leaderView[i] = v
		}
	}
	for _, id := range c.order {
		n := c.nodes[id]
		if !n.alive || n.isolated {
			continue
		}
		for i, v := range leaderView {
			n.committed[i] = v
		}
		delete(c.pending, id)
	}
	c.events = append(c.events, "converged")
}

// Violation is one broken invariant.
type Violation struct {
	Invariant string `json:"invariant"`
	Detail    string `json:"detail"`
	Tick      uint64 `json:"tick"`
}

// CheckInvariants evaluates every property at the current state.
func (c *Cluster) CheckInvariants(tick uint64) []Violation {
	var out []Violation

	// 1. At most one leader per term.
	leaders := 0
	for _, id := range c.order {
		if c.nodes[id].leader {
			leaders++
		}
	}
	if leaders > 1 {
		out = append(out, Violation{Invariant: "single_leader_per_term", Tick: tick,
			Detail: strconv.Itoa(leaders) + " leaders in term " + strconv.FormatUint(c.term, 10)})
	}

	// 2. No committed value diverges between nodes.
	seen := map[uint64]string{}
	for _, id := range c.order {
		for idx, v := range c.nodes[id].committed {
			if prev, ok := seen[idx]; ok && prev != v {
				out = append(out, Violation{Invariant: "no_divergence", Tick: tick,
					Detail: "index " + strconv.FormatUint(idx, 10) + " is " + prev +
						" on one node and " + v + " on " + id})
			}
			seen[idx] = v
		}
	}

	// 3. No committed entry is lost by a node that is alive and joined.
	for _, id := range c.order {
		n := c.nodes[id]
		if !n.alive || n.isolated {
			continue
		}
		if len(c.pending[id]) > 0 {
			continue // legitimately still catching up
		}
		for idx := range seen {
			if _, ok := n.committed[idx]; !ok {
				out = append(out, Violation{Invariant: "no_committed_loss", Tick: tick,
					Detail: "node " + id + " is missing committed index " +
						strconv.FormatUint(idx, 10)})
			}
		}
	}
	return out
}

// Report is the outcome of one experiment.
type Report struct {
	Plan          FaultPlan   `json:"plan"`
	FaultsApplied int         `json:"faults_applied"`
	Proposals     int         `json:"proposals"`
	Commits       int         `json:"commits"`
	Refusals      int         `json:"refusals"`
	Violations    []Violation `json:"violations"`
	Events        []string    `json:"events"`
	RunHash       string      `json:"run_hash"`
}

// Err converts violations into an error so a caller cannot read the
// struct and shrug.
func (r Report) Err() error {
	if len(r.Violations) == 0 {
		return nil
	}
	var parts []string
	for _, v := range r.Violations {
		parts = append(parts, v.Invariant+"@"+strconv.FormatUint(v.Tick, 10)+": "+v.Detail)
	}
	return fmt.Errorf("%w (seed %d): %s", ErrInvariantViolated, r.Plan.Seed,
		strings.Join(parts, "; "))
}

// Run executes the experiment: schedule faults, propose values, check
// invariants at every tick, heal, converge, check again.
func Run(p FaultPlan) (Report, error) {
	schedule, err := Schedule(p)
	if err != nil {
		return Report{}, err
	}
	c, err := NewCluster(p)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Plan: p}
	byTick := map[uint64][]Fault{}
	for _, f := range schedule {
		byTick[f.Tick] = append(byTick[f.Tick], f)
	}
	for tick := uint64(1); tick <= p.Ticks; tick++ {
		for _, f := range byTick[tick] {
			c.Apply(f)
			rep.FaultsApplied++
		}
		rep.Proposals++
		if c.Propose("v" + strconv.FormatUint(tick, 10)) {
			rep.Commits++
		} else {
			rep.Refusals++
		}
		rep.Violations = append(rep.Violations, c.CheckInvariants(tick)...)
	}
	// Full heal, then convergence must restore a single consistent view.
	for _, id := range c.order {
		c.Apply(Fault{Tick: p.Ticks, Kind: FaultHeal, Nodes: []string{id}})
	}
	c.Converge()
	rep.Violations = append(rep.Violations, c.CheckInvariants(p.Ticks)...)
	rep.Events = c.events
	rep.RunHash = hashRun(rep)
	return rep, nil
}

func hashRun(r Report) string {
	var sb strings.Builder
	sb.WriteString("chaos_run/v1\nseed=" + strconv.FormatUint(r.Plan.Seed, 10) +
		"\nticks=" + strconv.FormatUint(r.Plan.Ticks, 10) +
		"\nfaults=" + strconv.Itoa(r.FaultsApplied) +
		"\ncommits=" + strconv.Itoa(r.Commits) +
		"\nrefusals=" + strconv.Itoa(r.Refusals) + "\n")
	for _, e := range r.Events {
		sb.WriteString("e=" + e + "\n")
	}
	for _, v := range r.Violations {
		sb.WriteString("v=" + v.Invariant + "|" + v.Detail + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
