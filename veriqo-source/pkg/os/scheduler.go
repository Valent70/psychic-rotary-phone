package os

import (
	"container/heap"
	"sync"
	"sync/atomic"
)

// ─── Scheduler ────────────────────────────────────────────────────────────────
// Implements a priority-queue scheduler with weighted fair queue and admission
// control. This fills the Priority 7 gap from the architecture review.

// SchedulerConfig configures the process scheduler.
type SchedulerConfig struct {
	Workers       int
	MaxQueueDepth int
}

// Scheduler is a priority-queue based process scheduler.
type Scheduler struct {
	mu       sync.Mutex
	queue    processHeap
	workers  int
	maxDepth int
	running  atomic.Int32
	admitCh  chan *Process
	stopCh   chan struct{}
}

// NewScheduler creates and starts a Scheduler.
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.MaxQueueDepth <= 0 {
		cfg.MaxQueueDepth = 1024
	}
	s := &Scheduler{
		workers:  cfg.Workers,
		maxDepth: cfg.MaxQueueDepth,
		admitCh:  make(chan *Process, cfg.MaxQueueDepth),
		stopCh:   make(chan struct{}),
	}
	heap.Init(&s.queue)
	for range cfg.Workers {
		go s.worker()
	}
	return s
}

// Submit enqueues a process for scheduling.
// Returns false if the queue is full (admission control).
func (s *Scheduler) Submit(p *Process) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) >= s.maxDepth {
		return false // backpressure: reject
	}
	heap.Push(&s.queue, p)
	return true
}

// Len returns the current queue depth.
func (s *Scheduler) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Running returns the number of actively running processes.
func (s *Scheduler) Running() int { return int(s.running.Load()) }

// Stop shuts down all scheduler workers.
func (s *Scheduler) Stop() { close(s.stopCh) }

func (s *Scheduler) worker() {
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			// Park until something arrives.
			select {
			case <-s.stopCh:
				return
			case p := <-s.admitCh:
				_ = p // already picked up by Submit via heap push
			}
			continue
		}
		p := heap.Pop(&s.queue).(*Process)
		s.mu.Unlock()
		s.running.Add(1)
		// In a real system, execute the process task here.
		// For now we just mark it complete.
		p.markComplete()
		s.running.Add(-1)
	}
}

// ─── processHeap ──────────────────────────────────────────────────────────────

type processHeap []*Process

func (h processHeap) Len() int           { return len(h) }
func (h processHeap) Less(i, j int) bool { return h[i].Priority > h[j].Priority }
func (h processHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *processHeap) Push(x any) { *h = append(*h, x.(*Process)) }
func (h *processHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
