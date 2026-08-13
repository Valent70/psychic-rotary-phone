// Package dr is the multi_region_dr blocker's qualification machinery: a
// RegionProvider abstraction plus a scenario runner that fails a region,
// measures real recovery time (RTO) and real data loss (RPO), heals the
// region, and confirms it converges. LocalRegionProvider models each
// region as a raftlite.Node in one process, connected through
// raftlite.MemTransport's existing DropTo/HealTo partition support --
// so "region failure" here is a genuine network partition applied to a
// genuine Raft cluster, not a scripted status flip. It qualifies the
// failover/recovery *logic*; it does not qualify real cross-datacenter
// network behavior, which is what the multi_region_dr blocker ultimately
// requires from real infrastructure.
package dr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"veriqo/pkg/blockers"
	"veriqo/pkg/consensus/raftlite"
)

// Region identifies one participant in a DR run.
type Region struct{ ID string }

// RegionProvider provisions regions, replicates writes across them, and
// can partition or heal a specific region. Real implementations run
// against real cloud regions; LocalRegionProvider runs each region as an
// in-process raft node.
type RegionProvider interface {
	Mode() string
	CreateRegions(ctx context.Context, n int) ([]Region, error)
	CurrentLeader(ctx context.Context) (Region, error)
	Write(ctx context.Context, key, value string) error
	Read(regionID, key string) (value string, ok bool)
	Partition(regionID string) error
	Heal(regionID string) error
	Destroy(ctx context.Context) error
}

// kvFSM is a minimal replicated key-value store: committed log entries
// are "SET\x00key\x00value" commands applied to an in-memory map.
type kvFSM struct {
	mu   sync.RWMutex
	data map[string]string
}

func newKVFSM() *kvFSM { return &kvFSM{data: make(map[string]string)} }

func encodeSet(key, value string) []byte {
	return []byte("SET\x00" + key + "\x00" + value)
}

func (f *kvFSM) Apply(entry raftlite.LogEntry) error {
	parts := splitCmd(entry.Command)
	if len(parts) != 3 || parts[0] != "SET" {
		return nil // no-op / config-change markers land here too; ignore
	}
	f.mu.Lock()
	f.data[parts[1]] = parts[2]
	f.mu.Unlock()
	return nil
}

func (f *kvFSM) get(key string) (string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	v, ok := f.data[key]
	return v, ok
}

func splitCmd(b []byte) []string {
	var parts []string
	start := 0
	for i, c := range b {
		if c == 0 {
			parts = append(parts, string(b[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, string(b[start:]))
	return parts
}

// LocalRegionProvider models regions as raftlite nodes sharing an
// in-process MemTransport, whose real DropTo/HealTo partition support
// stands in for a region-level network failure.
type LocalRegionProvider struct {
	mu     sync.Mutex
	trans  *raftlite.MemTransport
	nodes  map[string]*raftlite.Node
	fsms   map[string]*kvFSM
	ids    []string
	cancel context.CancelFunc
}

func NewLocalRegionProvider() *LocalRegionProvider { return &LocalRegionProvider{} }

func (p *LocalRegionProvider) Mode() string { return "SIMULATED" }

func (p *LocalRegionProvider) CreateRegions(ctx context.Context, n int) ([]Region, error) {
	if n < 3 {
		return nil, errors.New("dr: at least 3 regions are required to tolerate one region failure with quorum")
	}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("region-%d", i)
	}
	trans := raftlite.NewMemTransport()
	nodes := make(map[string]*raftlite.Node, n)
	fsms := make(map[string]*kvFSM, n)
	for _, id := range ids {
		peers := make([]string, 0, n-1)
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		fsm := newKVFSM()
		node := raftlite.NewNode(raftlite.Config{
			ID: id, Peers: peers, Transport: trans, FSM: fsm,
			ElectionTimeoutMin: 30 * time.Millisecond,
			ElectionTimeoutMax: 60 * time.Millisecond,
			HeartbeatInterval:  10 * time.Millisecond,
		})
		trans.Register(node)
		nodes[id] = node
		fsms[id] = fsm
	}

	runCtx, cancel := context.WithCancel(context.Background())
	for _, node := range nodes {
		go node.Run(runCtx)
	}

	p.mu.Lock()
	p.trans, p.nodes, p.fsms, p.ids, p.cancel = trans, nodes, fsms, ids, cancel
	p.mu.Unlock()

	if _, err := p.CurrentLeader(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("dr: no leader elected after CreateRegions: %w", err)
	}
	regions := make([]Region, n)
	for i, id := range ids {
		regions[i] = Region{ID: id}
	}
	return regions, nil
}

// CurrentLeader polls until exactly one node reports itself Leader (or
// ctx expires), guarding against the transient window where an old
// leader hasn't yet stepped down from a stale term.
func (p *LocalRegionProvider) CurrentLeader(ctx context.Context) (Region, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		nodes := p.nodes
		p.mu.Unlock()

		var leaders []*raftlite.Node
		var maxTerm uint64
		for _, n := range nodes {
			if n.IsLeader() {
				if n.Term() > maxTerm {
					maxTerm = n.Term()
					leaders = []*raftlite.Node{n}
				} else if n.Term() == maxTerm {
					leaders = append(leaders, n)
				}
			}
		}
		if len(leaders) == 1 {
			return Region{ID: leaders[0].ID()}, nil
		}
		if time.Now().After(deadline) {
			return Region{}, fmt.Errorf("dr: no single leader converged (candidates=%d)", len(leaders))
		}
		select {
		case <-ctx.Done():
			return Region{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (p *LocalRegionProvider) Write(ctx context.Context, key, value string) error {
	leader, err := p.CurrentLeader(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	node := p.nodes[leader.ID]
	p.mu.Unlock()
	idx, _, err := node.Propose(encodeSet(key, value))
	if err != nil {
		return fmt.Errorf("dr: propose on %s: %w", leader.ID, err)
	}
	return node.WaitApplied(ctx, idx)
}

func (p *LocalRegionProvider) Read(regionID, key string) (string, bool) {
	p.mu.Lock()
	fsm, ok := p.fsms[regionID]
	p.mu.Unlock()
	if !ok {
		return "", false
	}
	return fsm.get(key)
}

func (p *LocalRegionProvider) Partition(regionID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.nodes[regionID]; !ok {
		return fmt.Errorf("dr: unknown region %q", regionID)
	}
	for _, other := range p.ids {
		if other == regionID {
			continue
		}
		p.trans.DropTo(regionID, other)
		p.trans.DropTo(other, regionID)
	}
	return nil
}

func (p *LocalRegionProvider) Heal(regionID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.nodes[regionID]; !ok {
		return fmt.Errorf("dr: unknown region %q", regionID)
	}
	for _, other := range p.ids {
		if other == regionID {
			continue
		}
		p.trans.HealTo(regionID, other)
		p.trans.HealTo(other, regionID)
	}
	return nil
}

func (p *LocalRegionProvider) Destroy(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	for _, n := range p.nodes {
		n.Shutdown()
	}
	return nil
}

// RunQualification fails the current leader region, measures how long
// recovery takes (RTO) and whether any acknowledged write is lost
// (RPO), confirms the surviving majority keeps serving writes, heals
// the failed region, and confirms it converges. It refuses to run
// against a REAL-mode provider -- that evidence belongs in
// pkg/governance/qualification.
func RunQualification(ctx context.Context, contract *blockers.Contract, provider RegionProvider, regionCount int) (blockers.RunResult, error) {
	if provider.Mode() == "REAL" {
		return blockers.RunResult{}, errors.New("dr: RunQualification refuses a REAL-mode provider; submit that evidence through pkg/governance/qualification instead")
	}

	if _, err := provider.CreateRegions(ctx, regionCount); err != nil {
		return blockers.RunResult{}, fmt.Errorf("dr: create regions: %w", err)
	}
	defer func() { _ = provider.Destroy(ctx) }()

	if err := provider.Write(ctx, "k1", "before-failure"); err != nil {
		return blockers.RunResult{}, fmt.Errorf("dr: baseline write: %w", err)
	}

	failedRegion, err := provider.CurrentLeader(ctx)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("dr: find leader: %w", err)
	}
	rtoStart := time.Now()
	if err := provider.Partition(failedRegion.ID); err != nil {
		return blockers.RunResult{}, fmt.Errorf("dr: partition: %w", err)
	}

	newLeader, err := waitForDifferentLeader(ctx, provider, failedRegion.ID)
	rto := time.Since(rtoStart)
	result := blockers.RunResult{BlockerID: contract.ID, Mode: provider.Mode()}
	if err != nil {
		result.FailureReason = fmt.Sprintf("no failover leader elected after region partition: %v", err)
		if recErr := contract.RecordFixtureRun(result); recErr != nil {
			return result, recErr
		}
		return result, errors.New(result.FailureReason)
	}
	_ = newLeader

	if err := provider.Write(ctx, "k2", "after-failure"); err != nil {
		result.FailureReason = fmt.Sprintf("majority did not keep serving writes after region failure: %v", err)
		if recErr := contract.RecordFixtureRun(result); recErr != nil {
			return result, recErr
		}
		return result, errors.New(result.FailureReason)
	}

	v1, ok1 := provider.Read(newLeader.ID, "k1")
	rpoLost := 0
	if !ok1 || v1 != "before-failure" {
		rpoLost = 1
	}

	if err := provider.Heal(failedRegion.ID); err != nil {
		return blockers.RunResult{}, fmt.Errorf("dr: heal: %w", err)
	}
	converged := waitForConvergence(failedRegion.ID, provider, "k2", "after-failure", 5*time.Second)

	result.Pass = rpoLost == 0 && converged
	result.Measurements = map[string]string{
		"region_count":       fmt.Sprintf("%d", regionCount),
		"failed_region":      failedRegion.ID,
		"recovered_leader":   newLeader.ID,
		"rto":                rto.String(),
		"rpo_lost_writes":    fmt.Sprintf("%d", rpoLost),
		"healed_convergence": fmt.Sprintf("%v", converged),
	}
	if !result.Pass {
		if rpoLost != 0 {
			result.FailureReason = "acknowledged write lost across region failure (RPO > 0)"
		} else {
			result.FailureReason = "healed region did not converge with the surviving majority"
		}
	}

	if err := contract.RecordFixtureRun(result); err != nil {
		return result, err
	}
	return result, nil
}

func waitForDifferentLeader(ctx context.Context, provider RegionProvider, oldLeaderID string) (Region, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		leader, err := provider.CurrentLeader(ctx)
		if err == nil && leader.ID != oldLeaderID {
			return leader, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return Region{}, err
			}
			return Region{}, errors.New("leader did not change after partitioning it")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForConvergence(regionID string, provider RegionProvider, key, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got, ok := provider.Read(regionID, key); ok && got == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
