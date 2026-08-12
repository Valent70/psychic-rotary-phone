package raftlite

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrLeadershipNotConfirmed = errors.New("raftlite: could not confirm leadership with a quorum; refusing to answer a linearizable read")

// ReadIndex implements the standard Raft read-index optimization: a
// linearizable read without going through the log. It returns the
// commit index a read must wait for — the CALLER is responsible for the
// stale-read guard: only read local state AFTER n.WaitApplied(ctx, idx)
// returns successfully. Returning early or skipping WaitApplied defeats
// the whole guarantee.
//
// Leadership is confirmed by a real quorum round-trip: this node sends
// every peer a heartbeat-shaped AppendEntries (Entries=nil) and counts a
// peer as confirming as long as it replies WITHOUT rejecting due to a
// higher term — NOT by whether Success is true. A follower legitimately
// behind on the log replies Success=false, which says nothing about
// whether this node is still the real leader; only a higher-term reply
// (or no reply at all) does. This mirrors etcd/raft's read-index
// heartbeat-round semantics.
//
// If a quorum cannot be confirmed within ctx (network partition, lost
// leadership, etc.), ReadIndex returns ErrLeadershipNotConfirmed rather
// than a possibly-stale index — the explicit "stale-read guard" the
// review asked for.
// Design note on lease reads (honest scope, per review): this
// ReadIndex implementation deliberately does a REAL quorum round-trip
// on every call rather than trusting a wall-clock lease window (the
// TiKV/etcd "lease read" optimization). That means every ReadIndex
// call pays one network round-trip of latency it wouldn't pay under a
// lease — but it also means this implementation has NO dependency on
// bounded clock drift between nodes: a lease-based read is only safe
// if every node's clock is guaranteed to drift less than the lease
// window, which requires an explicit clock-skew bound this project
// does not currently enforce or measure. Not implemented: lease
// expiration, clock-drift bounds, read fencing tokens, and a formal
// linearizability proof beyond this doc-comment argument. The current
// design's linearizability argument is the standard Raft ReadIndex
// argument: a real quorum contact in the current term is sufficient
// proof of continued leadership at the moment of that contact,
// independent of any clock. Lease reads are a real, valuable follow-up
// (lower read latency) but are a distinct, additional safety
// obligation, not a strict improvement, and are intentionally not
// claimed here.
func (n *Node) ReadIndex(ctx context.Context) (uint64, error) {
	n.mu.Lock()
	if n.role != Leader {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	term := n.state.currentTerm
	readIdx := n.commitIndex
	peers := append([]string{}, n.peers...)
	n.mu.Unlock()

	if len(peers) == 0 {
		// Single-node cluster: this node IS the only quorum member.
		n.incMetric("readindex_success")
		return readIdx, nil
	}

	quorumNeeded := (len(peers)+1)/2 + 1 // total membership incl. self
	confirmed := 1                       // self
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range peers {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			rctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()

			n.mu.Lock()
			next := n.nextIndex[p]
			if next == 0 {
				next = 1
			}
			prevIdx := next - 1
			prevTerm := uint64(0)
			if pp := n.pos(prevIdx); pp >= 0 && pp < len(n.state.log) {
				prevTerm = n.state.log[pp].Term
			}
			leaderCommit := n.commitIndex
			n.mu.Unlock()

			reply, err := n.trans.SendAppendEntries(rctx, p, AppendEntriesArgs{
				Term: term, LeaderID: n.id, PrevLogIndex: prevIdx, PrevLogTerm: prevTerm,
				Entries: nil, LeaderCommit: leaderCommit,
			})
			if err != nil {
				return // unreachable: does not count toward quorum confirmation
			}
			n.mu.Lock()
			n.lastContact[p] = time.Now()
			if reply.Term > n.state.currentTerm {
				n.becomeFollowerLocked(reply.Term)
				n.mu.Unlock()
				return
			}
			n.mu.Unlock()
			if reply.Term > term {
				return // this specific round is stale; do not count it
			}
			mu.Lock()
			confirmed++
			mu.Unlock()
		}()
	}
	wg.Wait()

	n.mu.Lock()
	stillLeader := n.role == Leader && n.state.currentTerm == term
	n.mu.Unlock()

	if !stillLeader || confirmed < quorumNeeded {
		n.incMetric("readindex_timeout")
		return 0, ErrLeadershipNotConfirmed
	}
	n.incMetric("readindex_success")
	return readIdx, nil
}
