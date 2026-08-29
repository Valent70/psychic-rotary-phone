package raftlite

import (
	"encoding/json"
	"fmt"
	"sync"

	"veriqo/pkg/evidence/manifest"
)

// This file closes the two items the VERIQO Trust Authority Model round
// left honestly rated yellow -- "snapshot restoration: not actually
// live-integrated" and "distributed replication: deterministic
// equivalence, not real replication" -- for pkg/evidence/manifest, using
// exactly the pattern already proven twice in this codebase (fsm_adapter.go's
// KGAdapter for pkg/moat/kg, fsm_distributed_adapter.go's DistributedAdapter
// for pkg/kernel/distributed): an FSM+Snapshotter adapter that NEVER
// deserializes authoritative state (State, ManifestHash, CustodyChainHead)
// directly, only ever the ORIGINAL COMMAND SEQUENCE, replayed back through
// the real, gated Registry methods (RegisterDraft / RecordCustodyEvent /
// Advance) exactly as a live caller would have invoked them. This is what
// makes snapshot install and Node-A-to-Node-B replication provably the
// same operation as the manifest package's own existing replay-determinism
// guarantee (manifest_test.go's TestReplayReproducesIdenticalFinalizedState),
// rather than a parallel, weaker mechanism that could manufacture authority
// serialization/replay/replication were never supposed to be able to grant
// (INV-006/007/008/009).

// ManifestOp names which of the three real Registry mutators a
// ManifestCommand invokes. These are the ONLY three ways a
// manifest.Registry's state ever changes (RegisterDraft, RecordCustodyEvent,
// Advance) -- there is deliberately no fourth "SetState" op, because no
// such method exists on Registry to call.
type ManifestOp string

const (
	ManifestOpRegisterDraft      ManifestOp = "REGISTER_DRAFT"
	ManifestOpRecordCustodyEvent ManifestOp = "RECORD_CUSTODY_EVENT"
	ManifestOpAdvance            ManifestOp = "ADVANCE"
)

// ManifestCommand is the wire format Propose() callers encode into
// LogEntry.Command, and the same format ManifestAdapter's own command log
// (and therefore Snapshot()) is made of. Each command carries exactly the
// arguments its target Registry method takes -- never a result field like
// State or ManifestHash, which only the real method call may compute.
type ManifestCommand struct {
	Op ManifestOp `json:"op"`

	// REGISTER_DRAFT
	Draft *manifest.Manifest `json:"draft,omitempty"`

	// RECORD_CUSTODY_EVENT
	EvidenceID  string                 `json:"evidence_id,omitempty"`
	EventID     string                 `json:"event_id,omitempty"`
	Actor       string                 `json:"actor,omitempty"`
	Action      manifest.CustodyAction `json:"action,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
	ContentHash string                 `json:"content_hash,omitempty"`

	// ADVANCE (EvidenceID shared with RECORD_CUSTODY_EVENT above)
	To manifest.State `json:"to,omitempty"`

	// Tick is shared by RECORD_CUSTODY_EVENT and ADVANCE.
	Tick uint64 `json:"tick,omitempty"`
}

// ManifestAdapter implements raftlite.FSM and raftlite.Snapshotter for
// pkg/evidence/manifest.Registry.
//
// mu guards BOTH the Registry pointer and the command log, for the same
// reason KGAdapter's mu does: Apply() runs from applyCommitted OUTSIDE the
// Node's lock, while Restore() runs from HandleInstallSnapshot WHILE the
// Node's lock is held.
type ManifestAdapter struct {
	mu  sync.RWMutex
	reg *manifest.Registry
	log []ManifestCommand // the exact, ordered, replayable sequence of successfully-applied commands
}

func NewManifestAdapter(reg *manifest.Registry) *ManifestAdapter {
	return &ManifestAdapter{reg: reg}
}

// Registry returns the adapter's current live *manifest.Registry -- safe
// to call concurrently with Apply/Restore; a caller that holds onto the
// returned pointer across a Restore() will keep reading the PRE-restore
// registry, exactly like any other snapshot-of-a-pointer read.
func (a *ManifestAdapter) Registry() *manifest.Registry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reg
}

// applyManifestCommand dispatches cmd to the ONE real, gated Registry
// method it names -- this is the single choke point both Apply (live
// commit) and Restore (snapshot replay) go through, so the two paths
// cannot drift: whatever transitionPrerequisiteLocked/hash-chain checks a
// live Advance() enforces, a replayed Advance() enforces identically.
func applyManifestCommand(reg *manifest.Registry, cmd ManifestCommand) error {
	switch cmd.Op {
	case ManifestOpRegisterDraft:
		if cmd.Draft == nil {
			return fmt.Errorf("manifest-adapter: REGISTER_DRAFT command missing draft")
		}
		_, err := reg.RegisterDraft(*cmd.Draft)
		return err
	case ManifestOpRecordCustodyEvent:
		_, err := reg.RecordCustodyEvent(cmd.EvidenceID, cmd.EventID, cmd.Actor, cmd.Action, cmd.Tick, cmd.Reason, cmd.ContentHash)
		return err
	case ManifestOpAdvance:
		_, err := reg.Advance(cmd.EvidenceID, cmd.To, cmd.Tick)
		return err
	default:
		return fmt.Errorf("manifest-adapter: unknown op %q", cmd.Op)
	}
}

// Apply decodes entry.Command and applies it to the live Registry via
// applyManifestCommand. Only appended to the adapter's own replayable log
// on success -- a command the real Registry refused (e.g. a transition
// whose prerequisite was never met) never becomes part of what a later
// Snapshot()/Restore() would replay, exactly mirroring that it never
// became part of the Registry's own real state either.
func (a *ManifestAdapter) Apply(entry LogEntry) error {
	if len(entry.Command) == 0 {
		return nil // heartbeat / no-op entry
	}
	var cmd ManifestCommand
	if err := json.Unmarshal(entry.Command, &cmd); err != nil {
		return fmt.Errorf("manifest-adapter: decode failed at index %d: %w", entry.Index, err)
	}
	if err := applyManifestCommand(a.Registry(), cmd); err != nil {
		return err
	}
	a.mu.Lock()
	a.log = append(a.log, cmd)
	a.mu.Unlock()
	return nil
}

// Snapshot encodes the adapter's full command log as JSON -- the ORIGINAL
// sequence of RegisterDraft/RecordCustodyEvent/Advance calls, not the
// Registry's derived State/ManifestHash fields. Deterministic by
// construction: manifest.computeManifestHash/computeCustodyHash are pure
// functions of semantic content (no time.Now(), no randomness), so two
// independently-replayed copies of this exact log converge on identical
// hashes -- the same principle TestReplayReproducesIdenticalFinalizedState
// already proves for a manually-driven "Node A"/"Node B" pair, now proven
// again through the real InstallSnapshot RPC path (see
// fsm_manifest_adapter_test.go).
func (a *ManifestAdapter) Snapshot() ([]byte, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return json.Marshal(a.log)
}

// Restore rebuilds the Registry from a snapshot by REPLAYING every
// command through the real, gated Registry methods against a brand-new,
// empty Registry -- never by deserializing a Manifest's State or
// ManifestHash field directly and trusting it. This is what makes a
// forged snapshot (one whose command log has been tampered with to
// smuggle a fabricated State, or to skip a required custody event)
// structurally inert: the replay hits the exact same
// transitionPrerequisiteLocked / hash-chain-verification gates a live
// caller would, and a single failed command anywhere in the log fails
// the ENTIRE restore -- fail-closed, matching KGAdapter.Restore's own
// contract. On any failure the adapter's live Registry/log are left
// completely untouched; only on full success does the swap happen,
// atomically, under mu.
func (a *ManifestAdapter) Restore(data []byte) error {
	var cmds []ManifestCommand
	if err := json.Unmarshal(data, &cmds); err != nil {
		return fmt.Errorf("manifest-adapter: snapshot decode failed: %w", err)
	}
	rebuilt := manifest.NewRegistry()
	for i, cmd := range cmds {
		if err := applyManifestCommand(rebuilt, cmd); err != nil {
			return fmt.Errorf("manifest-adapter: snapshot replay/verify failed at command %d (%s): %w", i, cmd.Op, err)
		}
	}
	a.mu.Lock()
	a.reg = rebuilt
	a.log = cmds
	a.mu.Unlock()
	return nil
}

// EncodeManifestRegisterDraft/EncodeManifestRecordCustodyEvent/
// EncodeManifestAdvance are convenience helpers for callers of
// Node.Propose() so they don't hand-roll JSON, mirroring
// EncodeUpsertNode/EncodeUpsertEdge above.
func EncodeManifestRegisterDraft(draft manifest.Manifest) []byte {
	b, _ := json.Marshal(ManifestCommand{Op: ManifestOpRegisterDraft, Draft: &draft})
	return b
}

func EncodeManifestRecordCustodyEvent(evidenceID, eventID, actor string, action manifest.CustodyAction, tick uint64, reason, contentHash string) []byte {
	b, _ := json.Marshal(ManifestCommand{
		Op: ManifestOpRecordCustodyEvent, EvidenceID: evidenceID, EventID: eventID,
		Actor: actor, Action: action, Tick: tick, Reason: reason, ContentHash: contentHash,
	})
	return b
}

func EncodeManifestAdvance(evidenceID string, to manifest.State, tick uint64) []byte {
	b, _ := json.Marshal(ManifestCommand{Op: ManifestOpAdvance, EvidenceID: evidenceID, To: to, Tick: tick})
	return b
}
