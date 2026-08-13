// Package scale is the scale_qualification blocker's qualification
// machinery: a NodeProvider abstraction plus a workload runner that
// distributes evidence records across nodes and verifies exactly-once
// delivery, throughput, and convergence. SimulatedNodeProvider is a
// goroutine-based fixture (mode "SIMULATED") standing in for the real
// 100-physical/cloud-node deployment the scale_qualification blocker
// ultimately requires; a future RealNodeProvider satisfying the same
// interface is a provider swap, not a rewrite.
package scale

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"veriqo/pkg/blockers"
)

// Node identifies one participant in a scale run.
type Node struct {
	ID string
}

// EvidenceRecord is one unit of work distributed to a node. In a real
// deployment this is a genuine evidence record; here it is a synthetic
// stand-in with the same shape.
type EvidenceRecord struct {
	ID      string
	Payload string
}

// NodeProvider provisions nodes, accepts evidence submissions addressed
// to a specific node, and reports back everything every node actually
// processed. Real implementations run on physical or cloud nodes;
// SimulatedNodeProvider runs each node as a goroutine.
type NodeProvider interface {
	// Provision brings up n nodes and returns their identities.
	Provision(ctx context.Context, n int) ([]Node, error)
	// Submit hands one evidence record to a specific node for processing.
	Submit(ctx context.Context, node Node, rec EvidenceRecord) error
	// Collect blocks until all submitted records have been processed by
	// their nodes (or ctx expires) and returns every processed record
	// together with the ID of the node that processed it.
	Collect(ctx context.Context) ([]ProcessedRecord, error)
	// Destroy tears the nodes down.
	Destroy(ctx context.Context, nodes []Node) error
	// Mode identifies how this provider produces its nodes -- "SIMULATED"
	// or "REAL". RunQualification refuses to record a REAL-mode result;
	// that path belongs to pkg/governance/qualification.
	Mode() string
}

// ProcessedRecord is what a node reports back after handling a record.
type ProcessedRecord struct {
	NodeID     string
	RecordID   string
	ReceivedAt time.Time
}

// SimulatedNodeProvider runs each node as its own goroutine reading from
// a dedicated inbox channel. It is a genuine concurrent system --
// records really are distributed across independently scheduled
// goroutines and really can race -- but it runs on one process, so it
// qualifies the *distribution and integrity-checking logic*, not real
// multi-machine network/failure behavior.
type SimulatedNodeProvider struct {
	mu        sync.Mutex
	inboxes   map[string]chan EvidenceRecord
	processed chan ProcessedRecord
	wg        sync.WaitGroup
}

// NewSimulatedNodeProvider constructs an empty provider. Call Provision
// before submitting work.
func NewSimulatedNodeProvider() *SimulatedNodeProvider {
	return &SimulatedNodeProvider{
		inboxes:   make(map[string]chan EvidenceRecord),
		processed: make(chan ProcessedRecord, 1<<16),
	}
}

func (p *SimulatedNodeProvider) Mode() string { return "SIMULATED" }

// Provision starts n goroutines, each with its own buffered inbox.
func (p *SimulatedNodeProvider) Provision(ctx context.Context, n int) ([]Node, error) {
	if n <= 0 {
		return nil, errors.New("scale: node count must be positive")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	nodes := make([]Node, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("sim-node-%04d", i)
		inbox := make(chan EvidenceRecord, 4096)
		p.inboxes[id] = inbox
		nodes = append(nodes, Node{ID: id})
		p.wg.Add(1)
		go p.runNode(ctx, id, inbox)
	}
	return nodes, nil
}

func (p *SimulatedNodeProvider) runNode(ctx context.Context, id string, inbox chan EvidenceRecord) {
	defer p.wg.Done()
	for {
		select {
		case rec, ok := <-inbox:
			if !ok {
				return
			}
			p.processed <- ProcessedRecord{NodeID: id, RecordID: rec.ID, ReceivedAt: time.Now()}
		case <-ctx.Done():
			return
		}
	}
}

// Submit routes rec to node's inbox. Real network partition/loss
// behavior is out of scope for a single-process fixture; the fakeProvider
// in this package's tests exercises loss/duplication detection directly
// instead.
func (p *SimulatedNodeProvider) Submit(ctx context.Context, node Node, rec EvidenceRecord) error {
	p.mu.Lock()
	inbox, ok := p.inboxes[node.ID]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("scale: unknown node %q", node.ID)
	}
	select {
	case inbox <- rec:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Collect closes every inbox once queued work has drained, waits for
// all node goroutines to exit, and returns everything they processed.
func (p *SimulatedNodeProvider) Collect(ctx context.Context) ([]ProcessedRecord, error) {
	p.mu.Lock()
	for _, inbox := range p.inboxes {
		close(inbox)
	}
	p.mu.Unlock()

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	close(p.processed)

	var out []ProcessedRecord
	for rec := range p.processed {
		out = append(out, rec)
	}
	return out, nil
}

// Destroy is a no-op for the simulated provider: goroutines already
// exited in Collect. A real provider would deprovision machines here.
func (p *SimulatedNodeProvider) Destroy(ctx context.Context, nodes []Node) error { return nil }

// IntegrityReport is the result of comparing what was submitted against
// what was actually processed.
type IntegrityReport struct {
	Submitted  int
	Processed  int
	Lost       []string
	Duplicated []string
}

func (r IntegrityReport) Clean() bool { return len(r.Lost) == 0 && len(r.Duplicated) == 0 }

// checkIntegrity compares submitted record IDs against processed ones.
func checkIntegrity(submittedIDs []string, processed []ProcessedRecord) IntegrityReport {
	want := make(map[string]int, len(submittedIDs))
	for _, id := range submittedIDs {
		want[id]++
	}
	seen := make(map[string]int, len(processed))
	for _, p := range processed {
		seen[p.RecordID]++
	}
	report := IntegrityReport{Submitted: len(submittedIDs), Processed: len(processed)}
	for id, wantCount := range want {
		gotCount := seen[id]
		if gotCount == 0 {
			report.Lost = append(report.Lost, id)
		} else if gotCount > wantCount {
			report.Duplicated = append(report.Duplicated, id)
		}
	}
	return report
}

// RunQualification distributes recordCount evidence records round-robin
// across nodeCount nodes provisioned from provider, waits for
// processing, checks integrity, measures throughput, and records the
// outcome on contract via RecordFixtureRun. It refuses to run against a
// provider whose Mode() is "REAL" -- that evidence belongs in
// pkg/governance/qualification, not here.
func RunQualification(ctx context.Context, contract *blockers.Contract, provider NodeProvider, nodeCount, recordCount int) (blockers.RunResult, error) {
	if provider.Mode() == "REAL" {
		return blockers.RunResult{}, errors.New("scale: RunQualification refuses a REAL-mode provider; submit that evidence through pkg/governance/qualification instead")
	}

	nodes, err := provider.Provision(ctx, nodeCount)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("scale: provision: %w", err)
	}
	defer func() { _ = provider.Destroy(ctx, nodes) }()

	submittedIDs := make([]string, 0, recordCount)
	start := time.Now()
	for i := 0; i < recordCount; i++ {
		id := fmt.Sprintf("ev-%08d", i)
		node := nodes[i%len(nodes)]
		if err := provider.Submit(ctx, node, EvidenceRecord{ID: id, Payload: "synthetic"}); err != nil {
			return blockers.RunResult{}, fmt.Errorf("scale: submit %s: %w", id, err)
		}
		submittedIDs = append(submittedIDs, id)
	}

	processed, err := provider.Collect(ctx)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("scale: collect: %w", err)
	}
	elapsed := time.Since(start)

	report := checkIntegrity(submittedIDs, processed)
	result := blockers.RunResult{
		BlockerID: contract.ID,
		Mode:      provider.Mode(),
		Pass:      report.Clean(),
		Measurements: map[string]string{
			"node_count":        fmt.Sprintf("%d", nodeCount),
			"records_submitted": fmt.Sprintf("%d", report.Submitted),
			"records_processed": fmt.Sprintf("%d", report.Processed),
			"elapsed":           elapsed.String(),
			"throughput_per_s":  fmt.Sprintf("%.2f", float64(recordCount)/elapsed.Seconds()),
		},
	}
	if !report.Clean() {
		result.FailureReason = fmt.Sprintf("lost=%d duplicated=%d", len(report.Lost), len(report.Duplicated))
	}

	if err := contract.RecordFixtureRun(result); err != nil {
		return result, err
	}
	return result, nil
}
