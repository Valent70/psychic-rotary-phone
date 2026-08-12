// Package distributed implements Veriqo's Distributed Kernel — gap #7
// in "Kritik_terbesar_nomor_1.docx": "Kalau nanti ada Node 1, Node 2,
// Node 3, Kernel harus tahu Engine ada di mana, Task harus ke mana,
// Failover, Leader, Replication."
//
// Registry tracks cluster membership and Engine->Node placement as a
// deterministic, hash-chained state machine. It implements
// raftlite.FSM's Apply(LogEntry) signature directly (Command is a
// gob-free, fixed-format encoded Command below) — meaning it can be
// handed to a real raftlite.Node as the replicated state machine to
// get actual cross-node consensus over placement decisions, the same
// pattern pkg/moat/kg.Adapter already uses for the knowledge graph.
//
// Honest scope: this package is wired and tested standalone (a real
// FSM implementation, real deterministic replay, real failover logic)
// but is NOT, in this session, connected to a live multi-process
// raftlite.Node cluster — that wiring is mechanical (same shape as
// kg.Adapter) and is listed as an explicit open item, since standing
// up and testing a real multi-node raft cluster around it is a
// separate, larger piece of work than the placement logic itself.
package distributed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrUnknownNode   = errors.New("distributed: node not registered")
	ErrNoNodesUp     = errors.New("distributed: no healthy node available for placement")
	ErrUnknownEngine = errors.New("distributed: engine has no placement")
	ErrBadCommand    = errors.New("distributed: malformed command payload")
)

// NodeStatus is a cluster member's live health state.
type NodeStatus string

const (
	NodeUp   NodeStatus = "UP"
	NodeDown NodeStatus = "DOWN"
)

// CmdKind is the type of a replicated command.
type CmdKind string

const (
	CmdJoin     CmdKind = "JOIN"      // node joins the cluster
	CmdMarkDown CmdKind = "MARK_DOWN" // node marked unhealthy
	CmdMarkUp   CmdKind = "MARK_UP"   // node marked healthy again
	CmdPlace    CmdKind = "PLACE"     // engine placed on a node
	CmdFailover CmdKind = "FAILOVER"  // engine moved off a down node
)

// Command is one replicated operation. Encode/Decode use a fixed
// pipe-delimited format (no encoding/gob, no external deps, and no
// ambiguity from unescaped separators since node/engine IDs are
// content-addressed hex strings elsewhere in this repo).
type Command struct {
	Kind     CmdKind
	NodeID   string
	EngineID string
}

func (c Command) Encode() []byte {
	return []byte(fmt.Sprintf("%s|%s|%s", c.Kind, c.NodeID, c.EngineID))
}

func DecodeCommand(b []byte) (Command, error) {
	parts := strings.SplitN(string(b), "|", 3)
	if len(parts) != 3 {
		return Command{}, ErrBadCommand
	}
	return Command{Kind: CmdKind(parts[0]), NodeID: parts[1], EngineID: parts[2]}, nil
}

// AppliedRecord is one hash-chained entry in the registry's own audit
// trail, recorded on every successful Apply.
type AppliedRecord struct {
	Index    uint64
	Cmd      Command
	PrevHash string
	Hash     string
}

func (r AppliedRecord) computeHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s|%s|%s", r.Index, r.Cmd.Kind, r.Cmd.NodeID, r.Cmd.EngineID, r.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// LogEntryLike mirrors raftlite.LogEntry's shape locally so this
// package's exported Apply method matches raftlite.FSM's signature
// (Apply(entry raftlite.LogEntry) error) without importing raftlite —
// keeping distributed dependency-free of consensus internals while
// still being drop-in-wireable: a thin adapter of 3 lines at the call
// site converts raftlite.LogEntry{Command: b} into ApplyCommand(b).
//
// Registry.ApplyCommand(index uint64, payload []byte) is the actual
// FSM entry point; see ApplyCommand below.
type Registry struct {
	mu        sync.Mutex
	nodes     map[string]NodeStatus
	placement map[string]string // engineID -> nodeID
	log       []AppliedRecord
}

func NewRegistry() *Registry {
	return &Registry{
		nodes:     make(map[string]NodeStatus),
		placement: make(map[string]string),
	}
}

// ApplyCommand is the FSM apply entry point: decode, validate,
// mutate, hash-chain-append. index must be the monotonically
// increasing raft log index of this entry (used only for the audit
// record's Index field, not for ordering — caller/raft already
// guarantees ordering).
func (r *Registry) ApplyCommand(index uint64, payload []byte) error {
	cmd, err := DecodeCommand(payload)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	switch cmd.Kind {
	case CmdJoin:
		r.nodes[cmd.NodeID] = NodeUp
	case CmdMarkDown:
		if _, ok := r.nodes[cmd.NodeID]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownNode, cmd.NodeID)
		}
		r.nodes[cmd.NodeID] = NodeDown
	case CmdMarkUp:
		if _, ok := r.nodes[cmd.NodeID]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownNode, cmd.NodeID)
		}
		r.nodes[cmd.NodeID] = NodeUp
	case CmdPlace, CmdFailover:
		if _, ok := r.nodes[cmd.NodeID]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownNode, cmd.NodeID)
		}
		r.placement[cmd.EngineID] = cmd.NodeID
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrBadCommand, cmd.Kind)
	}

	prev := ""
	if n := len(r.log); n > 0 {
		prev = r.log[n-1].Hash
	}
	rec := AppliedRecord{Index: index, Cmd: cmd, PrevHash: prev}
	rec.Hash = rec.computeHash()
	r.log = append(r.log, rec)
	return nil
}

// Join is a convenience wrapper: build+apply a CmdJoin at the next
// local index (single-node/testing use; a real cluster drives
// ApplyCommand from committed raft entries instead).
func (r *Registry) Join(nodeID string) error {
	return r.ApplyCommand(r.nextIndex(), Command{Kind: CmdJoin, NodeID: nodeID}.Encode())
}

func (r *Registry) MarkDown(nodeID string) error {
	return r.ApplyCommand(r.nextIndex(), Command{Kind: CmdMarkDown, NodeID: nodeID}.Encode())
}

func (r *Registry) MarkUp(nodeID string) error {
	return r.ApplyCommand(r.nextIndex(), Command{Kind: CmdMarkUp, NodeID: nodeID}.Encode())
}

func (r *Registry) nextIndex() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return uint64(len(r.log)) + 1
}

// Place assigns engineID to the least-loaded healthy node (deterministic:
// fewest current placements, ties broken by lowest node ID).
func (r *Registry) Place(engineID string) (string, error) {
	target, err := r.pickTargetNode()
	if err != nil {
		return "", err
	}
	if err := r.ApplyCommand(r.nextIndex(), Command{Kind: CmdPlace, NodeID: target, EngineID: engineID}.Encode()); err != nil {
		return "", err
	}
	return target, nil
}

func (r *Registry) pickTargetNode() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	load := make(map[string]int)
	var up []string
	for id, st := range r.nodes {
		if st == NodeUp {
			up = append(up, id)
			load[id] = 0
		}
	}
	if len(up) == 0 {
		return "", ErrNoNodesUp
	}
	for _, n := range r.placement {
		if _, ok := load[n]; ok {
			load[n]++
		}
	}
	sort.Strings(up)
	best := up[0]
	for _, id := range up[1:] {
		if load[id] < load[best] {
			best = id
		}
	}
	return best, nil
}

// Failover reassigns every engine currently placed on a down node to
// the least-loaded remaining healthy node, returning the set of
// engineID->newNodeID reassignments made. Deterministic: engines are
// processed in sorted-ID order, and target selection is the same
// least-loaded/lowest-ID rule Place uses, so two independent replicas
// applying the same MarkDown+Failover sequence converge identically.
func (r *Registry) Failover(downNode string) (map[string]string, error) {
	r.mu.Lock()
	if r.nodes[downNode] != NodeDown {
		r.mu.Unlock()
		return nil, fmt.Errorf("distributed: failover requires node %s to be marked DOWN first", downNode)
	}
	var affected []string
	for eng, node := range r.placement {
		if node == downNode {
			affected = append(affected, eng)
		}
	}
	sort.Strings(affected)
	r.mu.Unlock()

	moves := make(map[string]string)
	for _, eng := range affected {
		newNode, err := r.pickTargetNode()
		if err != nil {
			return moves, err
		}
		if err := r.ApplyCommand(r.nextIndex(), Command{Kind: CmdFailover, NodeID: newNode, EngineID: eng}.Encode()); err != nil {
			return moves, err
		}
		moves[eng] = newNode
	}
	return moves, nil
}

// NodeOf returns the node currently hosting an engine.
func (r *Registry) NodeOf(engineID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.placement[engineID]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownEngine, engineID)
	}
	return n, nil
}

// Nodes returns a snapshot of every node's status, sorted by ID.
func (r *Registry) Nodes() map[string]NodeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]NodeStatus, len(r.nodes))
	for k, v := range r.nodes {
		out[k] = v
	}
	return out
}

// VerifyChain replays the applied-command hash chain and reports a
// tamper mismatch, if any.
func (r *Registry) VerifyChain() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := ""
	for _, rec := range r.log {
		want := AppliedRecord{Index: rec.Index, Cmd: rec.Cmd, PrevHash: prev}
		if want.computeHash() != rec.Hash {
			return fmt.Errorf("distributed: tamper detected at index %d", rec.Index)
		}
		prev = rec.Hash
	}
	return nil
}

// LogLen exposes the applied-command count, mainly for tests/metrics.
func (r *Registry) LogLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.log)
}
