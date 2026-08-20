package raftlite

import (
	"context"
	"errors"
	"sync"
)

// MemTransport is an in-process transport used for tests and in-sandbox
// benchmarks. It supports simulated partitions (DropTo/HealTo) so chaos-lite
// tests can exercise leader failover without real sockets. Real network
// delivery is pkg/transport/rafttcp (mTLS TCP), which satisfies the same
// Transport interface.
type MemTransport struct {
	mu      sync.RWMutex
	nodes   map[string]*Node
	dropped map[[2]string]bool // [from][to] -> dropped
}

func NewMemTransport() *MemTransport {
	return &MemTransport{
		nodes:   make(map[string]*Node),
		dropped: make(map[[2]string]bool),
	}
}

func (t *MemTransport) Register(n *Node) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodes[n.id] = n
}

// DropTo simulates a one-way network partition from -> to.
func (t *MemTransport) DropTo(from, to string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropped[[2]string{from, to}] = true
}

func (t *MemTransport) HealTo(from, to string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.dropped, [2]string{from, to})
}

func (t *MemTransport) HealAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropped = make(map[[2]string]bool)
}

var ErrPartitioned = errors.New("raftlite/mem: link partitioned")

func (t *MemTransport) isDropped(from, to string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.dropped[[2]string{from, to}]
}

func (t *MemTransport) SendRequestVote(ctx context.Context, target string, args RequestVoteArgs) (RequestVoteReply, error) {
	if t.isDropped(args.CandidateID, target) {
		return RequestVoteReply{}, ErrPartitioned
	}
	t.mu.RLock()
	node, ok := t.nodes[target]
	t.mu.RUnlock()
	if !ok {
		return RequestVoteReply{}, errors.New("raftlite/mem: unknown target " + target)
	}
	select {
	case <-ctx.Done():
		return RequestVoteReply{}, ctx.Err()
	default:
	}
	return node.HandleRequestVote(args), nil
}

func (t *MemTransport) SendAppendEntries(ctx context.Context, target string, args AppendEntriesArgs) (AppendEntriesReply, error) {
	if t.isDropped(args.LeaderID, target) {
		return AppendEntriesReply{}, ErrPartitioned
	}
	t.mu.RLock()
	node, ok := t.nodes[target]
	t.mu.RUnlock()
	if !ok {
		return AppendEntriesReply{}, errors.New("raftlite/mem: unknown target " + target)
	}
	select {
	case <-ctx.Done():
		return AppendEntriesReply{}, ctx.Err()
	default:
	}
	return node.HandleAppendEntries(args), nil
}

func (t *MemTransport) SendInstallSnapshot(ctx context.Context, target string, args InstallSnapshotArgs) (InstallSnapshotReply, error) {
	if t.isDropped(args.LeaderID, target) {
		return InstallSnapshotReply{}, ErrPartitioned
	}
	t.mu.RLock()
	node, ok := t.nodes[target]
	t.mu.RUnlock()
	if !ok {
		return InstallSnapshotReply{}, errors.New("raftlite/mem: unknown target " + target)
	}
	select {
	case <-ctx.Done():
		return InstallSnapshotReply{}, ctx.Err()
	default:
	}
	return node.HandleInstallSnapshot(args), nil
}

func (t *MemTransport) SendPreVote(ctx context.Context, target string, args RequestVoteArgs) (RequestVoteReply, error) {
	if t.isDropped(args.CandidateID, target) {
		return RequestVoteReply{}, ErrPartitioned
	}
	t.mu.RLock()
	node, ok := t.nodes[target]
	t.mu.RUnlock()
	if !ok {
		return RequestVoteReply{}, errors.New("raftlite/mem: unknown target " + target)
	}
	select {
	case <-ctx.Done():
		return RequestVoteReply{}, ctx.Err()
	default:
	}
	return node.HandlePreVote(args), nil
}

func (t *MemTransport) SendTimeoutNow(ctx context.Context, target string, args TimeoutNowArgs) (TimeoutNowReply, error) {
	if t.isDropped(args.LeaderID, target) {
		return TimeoutNowReply{}, ErrPartitioned
	}
	t.mu.RLock()
	node, ok := t.nodes[target]
	t.mu.RUnlock()
	if !ok {
		return TimeoutNowReply{}, errors.New("raftlite/mem: unknown target " + target)
	}
	select {
	case <-ctx.Done():
		return TimeoutNowReply{}, ctx.Err()
	default:
	}
	return node.HandleTimeoutNow(args), nil
}
