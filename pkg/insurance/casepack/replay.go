package casepack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"veriqo/pkg/lineage"
)

// This file closes MVP §80 item 14 / Final Design §20 "C5" / spec §73:
// per-case insurance replay. It was left deliberately OPEN by the prior
// (R21) round rather than fake-closed — see
// docs/governance/INSURANCE_RESIDUAL_GATE_REGISTER.md item INS-D1 — with
// the stated reason that a cold replay needs a real serialised case
// snapshot, not a wiring shortcut over drive-determinism.
//
// What makes THIS a real cold replay rather than a restatement of
// TestDrivingIsDeterministic: the replay path below (ReplayFromSnapshot)
// takes ONLY a []byte. It never receives, closes over, or otherwise has
// access to the original Case value or its ledger. Between Snapshot and
// ReplayFromSnapshot in ColdReplay's own body, the original Case is not
// touched again — the only channel connecting live drive to replay is
// the serialized bytes, exactly as a real cross-process replay would be:
// write the snapshot to durable storage, read it back in a separate
// process, replay.
//
// pkg/replay remains the ONE canonical replay engine in this kernel and
// is NOT duplicated here (Final Design §39's forbidden list, enforced
// tree-wide by pkg/insurance/guardrails). What this file adds is the
// insurance-domain snapshot format pkg/replay's own contract requires
// from every domain that binds to it (a durable, content-addressed
// input a caller can reconstruct from) and the comparison that proves
// the reconstruction is faithful. canonical.Binding.AttachReplay
// (pkg/insurance/canonical) already accepts a real ReplayPackageID
// today; SnapshotID below is that identity.

// Snapshot serialises c into canonical JSON — the durable, content-
// addressed form a cold replay reconstructs from. Every field of Case
// (ID, Title, Narrative, EngineeringCoverage, ExpectedOutputs, Parties,
// Evidence) is plain data with no closures or live references, so this
// round-trips exactly: Attributes maps encode with Go's own
// deterministic (sorted-key) map marshalling, and every other field is
// a struct or slice whose JSON field order follows the struct's fixed
// declaration order. Two calls over an unchanged Case therefore produce
// byte-identical bytes, which is what makes SnapshotID below a genuine
// content address rather than an incidental one.
func (c Case) Snapshot() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("casepack: refusing to snapshot an invalid case: %w", err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("casepack: encoding snapshot: %w", err)
	}
	return b, nil
}

// SnapshotID content-addresses a snapshot the same way the rest of this
// domain content-addresses evidence: SHA-256 over the canonical bytes.
// This is the ReplayPackageID canonical.Binding.AttachReplay expects.
func SnapshotID(snapshot []byte) string {
	h := sha256.Sum256(snapshot)
	return "INSREPLAY-" + hex.EncodeToString(h[:])
}

var ErrEmptySnapshot = errors.New("casepack: replay snapshot must be non-empty")

// ReplayFromSnapshot reconstructs a Case from COLD-STORED BYTES ALONE —
// no reference to any prior in-memory Case, ledger, or Result — and
// drives it through the exact same production path (Drive) a live case
// takes, against a brand-new lineage.Ledger. This is the "Real Historical
// Case -> Replay Engine -> Production Logic" half of Final Design §20's
// design law: replay uses the SAME engine a live case uses, never a
// second one.
func ReplayFromSnapshot(snapshot []byte) (*Result, error) {
	if len(snapshot) == 0 {
		return nil, ErrEmptySnapshot
	}
	var c Case
	if err := json.Unmarshal(snapshot, &c); err != nil {
		return nil, fmt.Errorf("casepack: replay: decoding snapshot: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("casepack: replay: decoded case is invalid: %w", err)
	}
	return Drive(c, lineage.NewLedger())
}

// ColdReplayReport is the derived answer to spec §73 / Final Design §20
// "C5": did a case, replayed cold from nothing but its own serialised
// snapshot, reproduce the SAME material result a live run produced. Pass
// is derived from Failures — the same discipline every gate in
// pkg/insurance/verification uses, so there is no way to write PASS by
// hand.
type ColdReplayReport struct {
	CaseID     string `json:"case_id"`
	SnapshotID string `json:"snapshot_id"`

	EvidenceRootHashMatch  bool `json:"evidence_root_hash_match"`
	PreservationHashMatch  bool `json:"preservation_hash_match"`
	QuantumResultMatch     bool `json:"quantum_result_match"`
	PolicyVersionMatch     bool `json:"policy_version_match"`
	CoverageFactCountMatch bool `json:"coverage_fact_count_match"`

	Failures []string `json:"failures,omitempty"`
}

// Pass is derived from Failures — never settable directly.
func (r ColdReplayReport) Pass() bool { return len(r.Failures) == 0 }

// ColdReplay drives c live, serialises it, replays ONLY from those
// serialised bytes (ReplayFromSnapshot never sees c or the live Result
// again), and compares the two Results across every field a real
// cross-process cold replay is required to reproduce. A mismatch on ANY
// axis is a real, named failure — this never collapses to a single bit.
func ColdReplay(c Case) (live *Result, replayed *Result, report ColdReplayReport, err error) {
	live, err = Drive(c, lineage.NewLedger())
	if err != nil {
		return nil, nil, ColdReplayReport{}, fmt.Errorf("casepack: cold replay: live drive: %w", err)
	}

	snap, err := c.Snapshot()
	if err != nil {
		return live, nil, ColdReplayReport{}, err
	}
	snapID := SnapshotID(snap)

	replayed, rerr := ReplayFromSnapshot(snap)
	if rerr != nil {
		return live, nil, ColdReplayReport{
			CaseID: string(c.ID), SnapshotID: snapID,
			Failures: []string{fmt.Sprintf("replay from snapshot failed outright: %v", rerr)},
		}, nil
	}

	r := ColdReplayReport{CaseID: string(c.ID), SnapshotID: snapID}

	r.EvidenceRootHashMatch = live.Manifest.EvidenceRootHash == replayed.Manifest.EvidenceRootHash &&
		live.Manifest.EvidenceRootHash != ""
	if !r.EvidenceRootHashMatch {
		r.Failures = append(r.Failures, fmt.Sprintf(
			"evidence root hash diverged between live drive and cold replay: live=%s replay=%s",
			live.Manifest.EvidenceRootHash, replayed.Manifest.EvidenceRootHash))
	}

	r.PreservationHashMatch = live.Order != nil && replayed.Order != nil && live.Order.Hash() == replayed.Order.Hash()
	if !r.PreservationHashMatch {
		r.Failures = append(r.Failures, "preservation order hash diverged between live drive and cold replay")
	}

	r.QuantumResultMatch = live.Quantum.IndicativeClaimValue == replayed.Quantum.IndicativeClaimValue &&
		live.Quantum.CalculationVersion == replayed.Quantum.CalculationVersion
	if !r.QuantumResultMatch {
		r.Failures = append(r.Failures, fmt.Sprintf(
			"quantum result diverged: live=%s replay=%s",
			live.Quantum.IndicativeClaimValue, replayed.Quantum.IndicativeClaimValue))
	}

	r.PolicyVersionMatch = live.PolicyVersion.VersionID == replayed.PolicyVersion.VersionID &&
		live.PolicyVersion.VersionID != ""
	if !r.PolicyVersionMatch {
		r.Failures = append(r.Failures, "resolved policy version diverged between live drive and cold replay")
	}

	r.CoverageFactCountMatch = live.Gates.CoverageTraceability.FactCount == replayed.Gates.CoverageTraceability.FactCount
	if !r.CoverageFactCountMatch {
		r.Failures = append(r.Failures, "coverage fact count diverged between live drive and cold replay")
	}

	return live, replayed, r, nil
}

// RunColdReplay runs ColdReplay over every synthetic case in the pack
// and returns each case's report in deterministic (CaseID) order. Used
// by RunAssurance (assurance.go) to fold cold replay into the same
// evidence artifact the other four gates already produce, and directly
// by cmd/veriqo-readiness for the insurance_cold_replay gate.
func RunColdReplay() []ColdReplayReport {
	var out []ColdReplayReport
	for _, c := range All() {
		_, _, report, err := ColdReplay(c)
		if err != nil {
			out = append(out, ColdReplayReport{
				CaseID:   string(c.ID),
				Failures: []string{fmt.Sprintf("cold replay could not run at all: %v", err)},
			})
			continue
		}
		out = append(out, report)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CaseID < out[j].CaseID })
	return out
}
