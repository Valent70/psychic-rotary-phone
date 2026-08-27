package os

import (
	"sync"
	"time"

	"veriqo/pkg/core"
)

// ProcessState is the lifecycle state of a TrustProcess.
type ProcessState string

const (
	ProcessStatePending  ProcessState = "pending"
	ProcessStateRunning  ProcessState = "running"
	ProcessStateStopped  ProcessState = "stopped"
	ProcessStateFailed   ProcessState = "failed"
	ProcessStateComplete ProcessState = "complete"
)

// Process is a single unit of trusted execution within the OS.
// Every OS decision is made in the context of a named process.
type Process struct {
	mu        sync.RWMutex
	ID        core.ProcessID
	TenantID  core.TenantID
	DomainID  core.DomainID
	Name      string
	Priority  int // higher = higher scheduler priority
	DRC       core.DRC
	State     ProcessState
	StartedAt time.Time
	StoppedAt time.Time
	Metadata  map[string]string
}

func (p *Process) markComplete() {
	p.mu.Lock()
	p.State = ProcessStateComplete
	p.mu.Unlock()
}

func (p *Process) markStopped(at time.Time) {
	p.mu.Lock()
	p.State = ProcessStateStopped
	p.StoppedAt = at
	p.mu.Unlock()
}

func (p *Process) snapshot() *Process {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snapshot := Process{
		ID:        p.ID,
		TenantID:  p.TenantID,
		DomainID:  p.DomainID,
		Name:      p.Name,
		Priority:  p.Priority,
		DRC:       p.DRC,
		State:     p.State,
		StartedAt: p.StartedAt,
		StoppedAt: p.StoppedAt,
	}
	if p.Metadata != nil {
		snapshot.Metadata = make(map[string]string, len(p.Metadata))
		for key, value := range p.Metadata {
			snapshot.Metadata[key] = value
		}
	}
	return &snapshot
}
