package raftlite

import (
	"encoding/json"
	"fmt"
	"sync"

	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/insurance/evidence"
)

// EvidenceAdapter is fsm_manifest_adapter.go's twin for
// pkg/insurance/evidence.Registry (the Evidence Record authority layer,
// as opposed to manifest.Registry's Evidence Manifest authority layer),
// closing the same "snapshot restoration"/"distributed replication"
// yellow items for this second authority type.
//
// Evidence Record authority is NOT self-contained the way Manifest is:
// evidence.Registry.SetRights only succeeds when authorityID names an
// entity whose trust was actually granted in a SEPARATE
// pkg/evidence/provenance.Registry (Authority Round 2's own trust-source
// gate). A snapshot/replication story for evidence.Registry that ignored
// this would be dishonest -- restoring Record state without also
// restoring which authorities were ACTUALLY trusted at that point would
// either lose real SetRights grants (if replayed against an empty,
// untrusted provenance.Registry) or silently require an out-of-band,
// unverified provenance.Registry to already be present and consistent on
// every node. So EvidenceAdapter's command log includes provenance's own
// two mutators (Register, GrantTrust) alongside evidence.Registry's five
// (Submit, SetStrength, VerifyStatus, SetRights, MarkSuperseded) --
// every command replays through its own real, gated method, on a pair
// of freshly-built registries, exactly like ManifestAdapter.

// EvidenceOp names one of the seven real mutators this adapter replays
// through -- two from provenance.Registry, five from evidence.Registry.
type EvidenceOp string

const (
	EvidenceOpRegisterAuthority EvidenceOp = "REGISTER_AUTHORITY" // provenance.Registry.Register
	EvidenceOpGrantTrust        EvidenceOp = "GRANT_TRUST"        // provenance.Registry.GrantTrust
	EvidenceOpSubmit            EvidenceOp = "SUBMIT"             // evidence.Registry.Submit
	EvidenceOpSetStrength       EvidenceOp = "SET_STRENGTH"       // evidence.Registry.SetStrength
	EvidenceOpVerifyStatus      EvidenceOp = "VERIFY_STATUS"      // evidence.Registry.VerifyStatus
	EvidenceOpSetRights         EvidenceOp = "SET_RIGHTS"         // evidence.Registry.SetRights
	EvidenceOpMarkSuperseded    EvidenceOp = "MARK_SUPERSEDED"    // evidence.Registry.MarkSuperseded
)

// EvidenceCommand is the wire format for every mutation this adapter can
// replay. Like ManifestCommand, it carries exactly the arguments its
// target method takes -- never a result field like Status or Rights,
// which only the real method call may compute or accept from a
// TrustGranted-checked caller.
type EvidenceCommand struct {
	Op EvidenceOp `json:"op"`

	// REGISTER_AUTHORITY
	Entry *provenance.Entry `json:"entry,omitempty"`

	// GRANT_TRUST (AuthorityID shared with SET_RIGHTS below)
	AuthorityID    string `json:"authority_id,omitempty"`
	PolicyRef      string `json:"policy_ref,omitempty"`
	AttestationRef string `json:"attestation_ref,omitempty"`
	GrantedBy      string `json:"granted_by,omitempty"`

	// SUBMIT
	Record *evidence.Record `json:"record,omitempty"`

	// SET_STRENGTH (EvidenceID shared with VERIFY_STATUS/SET_RIGHTS)
	EvidenceID string             `json:"evidence_id,omitempty"`
	Strength   *evidence.Strength `json:"strength,omitempty"`

	// SET_RIGHTS
	RightsState provenance.RightsState `json:"rights_state,omitempty"`

	// MARK_SUPERSEDED
	SupersededID    string `json:"superseded_id,omitempty"`
	BySupersedingID string `json:"by_superseding_id,omitempty"`
	Actor           string `json:"actor,omitempty"`
	Reason          string `json:"reason,omitempty"`

	// Tick is shared by GRANT_TRUST and MARK_SUPERSEDED.
	Tick uint64 `json:"tick,omitempty"`
}

// EvidenceAdapter implements raftlite.FSM and raftlite.Snapshotter for
// the (provenance.Registry, evidence.Registry) pair. mu guards both
// registry pointers and the command log, for the same reason
// ManifestAdapter's mu does.
type EvidenceAdapter struct {
	mu      sync.RWMutex
	provReg *provenance.Registry
	evReg   *evidence.Registry
	log     []EvidenceCommand
}

func NewEvidenceAdapter(provReg *provenance.Registry, evReg *evidence.Registry) *EvidenceAdapter {
	return &EvidenceAdapter{provReg: provReg, evReg: evReg}
}

// Registries returns the adapter's current live registry pair -- safe to
// call concurrently with Apply/Restore.
func (a *EvidenceAdapter) Registries() (*provenance.Registry, *evidence.Registry) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.provReg, a.evReg
}

// applyEvidenceCommand dispatches cmd to the ONE real, gated method it
// names -- the single choke point Apply and Restore both go through.
func applyEvidenceCommand(provReg *provenance.Registry, evReg *evidence.Registry, cmd EvidenceCommand) error {
	switch cmd.Op {
	case EvidenceOpRegisterAuthority:
		if cmd.Entry == nil {
			return fmt.Errorf("evidence-adapter: REGISTER_AUTHORITY command missing entry")
		}
		return provReg.Register(*cmd.Entry)
	case EvidenceOpGrantTrust:
		return provReg.GrantTrust(cmd.AuthorityID, cmd.PolicyRef, cmd.AttestationRef, cmd.GrantedBy, cmd.Tick)
	case EvidenceOpSubmit:
		if cmd.Record == nil {
			return fmt.Errorf("evidence-adapter: SUBMIT command missing record")
		}
		return evReg.Submit(*cmd.Record)
	case EvidenceOpSetStrength:
		if cmd.Strength == nil {
			return fmt.Errorf("evidence-adapter: SET_STRENGTH command missing strength")
		}
		return evReg.SetStrength(cmd.EvidenceID, *cmd.Strength)
	case EvidenceOpVerifyStatus:
		_, err := evReg.VerifyStatus(cmd.EvidenceID)
		return err
	case EvidenceOpSetRights:
		return evReg.SetRights(cmd.EvidenceID, cmd.RightsState, provReg, cmd.AuthorityID)
	case EvidenceOpMarkSuperseded:
		return evReg.MarkSuperseded(cmd.SupersededID, cmd.BySupersedingID, cmd.Actor, cmd.Reason, cmd.Tick)
	default:
		return fmt.Errorf("evidence-adapter: unknown op %q", cmd.Op)
	}
}

// Apply decodes entry.Command and applies it via applyEvidenceCommand.
// Only appended to the adapter's own replayable log on success, exactly
// like ManifestAdapter.Apply.
func (a *EvidenceAdapter) Apply(entry LogEntry) error {
	if len(entry.Command) == 0 {
		return nil // heartbeat / no-op entry
	}
	var cmd EvidenceCommand
	if err := json.Unmarshal(entry.Command, &cmd); err != nil {
		return fmt.Errorf("evidence-adapter: decode failed at index %d: %w", entry.Index, err)
	}
	provReg, evReg := a.Registries()
	if err := applyEvidenceCommand(provReg, evReg, cmd); err != nil {
		return err
	}
	a.mu.Lock()
	a.log = append(a.log, cmd)
	a.mu.Unlock()
	return nil
}

// Snapshot encodes the adapter's full command log as JSON -- the
// original sequence of provenance/evidence mutator calls, never
// TrustGranted, Status, Strength, or Rights fields directly.
func (a *EvidenceAdapter) Snapshot() ([]byte, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return json.Marshal(a.log)
}

// Restore rebuilds BOTH registries from scratch by replaying every
// command through its real, gated method -- so a restored SetRights
// grant is only honored if the SAME snapshot's own GrantTrust command(s)
// legitimately established that authority first; a snapshot that tried
// to smuggle in a SET_RIGHTS command citing an authority no
// REGISTER_AUTHORITY/GRANT_TRUST pair in the SAME log actually trusted
// is refused by SetRights' own ErrRightsGrantNotAuthorized, exactly as it
// would be against a live caller. Fail-closed: on any replay failure,
// both live registry pointers are left completely untouched.
func (a *EvidenceAdapter) Restore(data []byte) error {
	var cmds []EvidenceCommand
	if err := json.Unmarshal(data, &cmds); err != nil {
		return fmt.Errorf("evidence-adapter: snapshot decode failed: %w", err)
	}
	rebuiltProv := provenance.NewRegistry()
	rebuiltEv := evidence.NewRegistry()
	for i, cmd := range cmds {
		if err := applyEvidenceCommand(rebuiltProv, rebuiltEv, cmd); err != nil {
			return fmt.Errorf("evidence-adapter: snapshot replay/verify failed at command %d (%s): %w", i, cmd.Op, err)
		}
	}
	a.mu.Lock()
	a.provReg = rebuiltProv
	a.evReg = rebuiltEv
	a.log = cmds
	a.mu.Unlock()
	return nil
}

// EncodeEvidence* are convenience helpers for callers of Node.Propose()
// so they don't hand-roll JSON, mirroring EncodeUpsertNode/
// EncodeManifestAdvance above.
func EncodeEvidenceRegisterAuthority(entry provenance.Entry) []byte {
	b, _ := json.Marshal(EvidenceCommand{Op: EvidenceOpRegisterAuthority, Entry: &entry})
	return b
}

func EncodeEvidenceGrantTrust(authorityID, policyRef, attestationRef, grantedBy string, tick uint64) []byte {
	b, _ := json.Marshal(EvidenceCommand{
		Op: EvidenceOpGrantTrust, AuthorityID: authorityID, PolicyRef: policyRef,
		AttestationRef: attestationRef, GrantedBy: grantedBy, Tick: tick,
	})
	return b
}

func EncodeEvidenceSubmit(rec evidence.Record) []byte {
	b, _ := json.Marshal(EvidenceCommand{Op: EvidenceOpSubmit, Record: &rec})
	return b
}

func EncodeEvidenceSetStrength(evidenceID string, strength evidence.Strength) []byte {
	b, _ := json.Marshal(EvidenceCommand{Op: EvidenceOpSetStrength, EvidenceID: evidenceID, Strength: &strength})
	return b
}

func EncodeEvidenceVerifyStatus(evidenceID string) []byte {
	b, _ := json.Marshal(EvidenceCommand{Op: EvidenceOpVerifyStatus, EvidenceID: evidenceID})
	return b
}

func EncodeEvidenceSetRights(evidenceID string, state provenance.RightsState, authorityID string) []byte {
	b, _ := json.Marshal(EvidenceCommand{Op: EvidenceOpSetRights, EvidenceID: evidenceID, RightsState: state, AuthorityID: authorityID})
	return b
}

func EncodeEvidenceMarkSuperseded(supersededID, bySupersedingID, actor, reason string, tick uint64) []byte {
	b, _ := json.Marshal(EvidenceCommand{
		Op: EvidenceOpMarkSuperseded, SupersededID: supersededID, BySupersedingID: bySupersedingID,
		Actor: actor, Reason: reason, Tick: tick,
	})
	return b
}
