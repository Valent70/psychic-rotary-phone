package scale

import (
	"context"
	"sync"
	"time"
)

// Faults configures FaultInjectingNodeProvider's deterministic fault
// injection: real loss, duplication, and unauthorized-submission
// scenarios exercised against a real (if simulated) NodeProvider,
// rather than only a hand-written test double reimplementing the whole
// interface from scratch each time.
type Faults struct {
	// DropEveryNth, when > 0, silently drops (never delivers) every
	// Nth submitted record -- a real loss injection.
	DropEveryNth int
	// DuplicateEveryNth, when > 0, delivers every Nth submitted record
	// TWICE -- a real duplication injection.
	DuplicateEveryNth int
	// InjectUnauthorized, when true, appends one ProcessedRecord
	// attributed to a NodeID that was never provisioned by this run --
	// a real unauthorized-acceptance injection.
	InjectUnauthorized bool
}

// FaultInjectingNodeProvider wraps a real NodeProvider (Inner) and
// deterministically injects the faults named in Faults, so this
// package's own integrity checks (checkIntegrity, checkAuthorization)
// can be exercised against a genuinely faulty provider rather than only
// scale_test.go's hand-rolled fakeProvider. It is itself a real
// NodeProvider satisfying the unmodified interface -- Mode() reports
// whatever Inner reports, so RunQualification's REAL-mode refusal still
// applies correctly if this wrapper is ever layered onto a REAL
// provider.
type FaultInjectingNodeProvider struct {
	Inner  NodeProvider
	Faults Faults

	mu    sync.Mutex
	count int
}

func (p *FaultInjectingNodeProvider) Mode() string { return p.Inner.Mode() }

func (p *FaultInjectingNodeProvider) Provision(ctx context.Context, n int) ([]Node, error) {
	return p.Inner.Provision(ctx, n)
}

func (p *FaultInjectingNodeProvider) Submit(ctx context.Context, node Node, rec EvidenceRecord) error {
	p.mu.Lock()
	p.count++
	n := p.count
	p.mu.Unlock()

	if p.Faults.DropEveryNth > 0 && n%p.Faults.DropEveryNth == 0 {
		return nil // silently dropped: a real fault, never actually delivered
	}
	if err := p.Inner.Submit(ctx, node, rec); err != nil {
		return err
	}
	if p.Faults.DuplicateEveryNth > 0 && n%p.Faults.DuplicateEveryNth == 0 {
		_ = p.Inner.Submit(ctx, node, rec) // deliberate duplicate delivery; a second submit error here is not this run's failure mode to report
	}
	return nil
}

func (p *FaultInjectingNodeProvider) Collect(ctx context.Context) ([]ProcessedRecord, error) {
	recs, err := p.Inner.Collect(ctx)
	if err != nil {
		return nil, err
	}
	if p.Faults.InjectUnauthorized {
		recs = append(recs, ProcessedRecord{
			NodeID: "unregistered-intruder-node", RecordID: "ev-injected-unauthorized", ReceivedAt: time.Now(),
		})
	}
	return recs, nil
}

func (p *FaultInjectingNodeProvider) Destroy(ctx context.Context, nodes []Node) error {
	return p.Inner.Destroy(ctx, nodes)
}
