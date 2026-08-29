package raftlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/insurance/evidence"
)

// testEvidenceOntology mirrors pkg/insurance/evidence's own mustEvidence
// test helper exactly (same field values), for the same reason
// testManifestDraft mirrors validDraft.
func testEvidenceOntology(t *testing.T, subject, source string, observedAt uint64) ontology.Evidence {
	t.Helper()
	e, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeDocument, Subject: subject, Predicate: "describes",
		Object: "cargo_condition", Source: source, ObservedAt: observedAt,
		Confidence: 0.9, Attributes: map[string]string{"document_hash": "deadbeef"},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	return e
}

func testEvidenceRecord(t *testing.T, subject, source string, observedAt uint64) evidence.Record {
	t.Helper()
	rec, err := evidence.New("CASE-1", testEvidenceOntology(t, subject, source, observedAt), "PTY-1", evidence.OriginClaimant)
	if err != nil {
		t.Fatalf("evidence.New: %v", err)
	}
	return rec
}

func testStrength() evidence.Strength {
	return evidence.Strength{
		Authenticity: evidence.AuthenticitySupported, Integrity: evidence.IntegrityVerified,
		Completeness: evidence.CompletenessComplete, ContradictionLevel: evidence.ContradictionLevelNone,
	}
}

// evidenceLifecycleCommands returns a real, ordered command sequence: a
// trusted authority is registered and granted trust, a Record is
// submitted, its Strength is assessed, Status is derived, and rights are
// granted -- exactly the sequence a live caller would issue, expressed
// as EvidenceCommands.
func evidenceLifecycleCommands(rec evidence.Record, authorityID string, tick uint64) []EvidenceCommand {
	return []EvidenceCommand{
		{Op: EvidenceOpRegisterAuthority, Entry: &provenance.Entry{ID: authorityID, Kind: provenance.KindReviewer, Name: "Governance Lead"}},
		{Op: EvidenceOpGrantTrust, AuthorityID: authorityID, PolicyRef: "policy://rights-grant-v1", GrantedBy: "compliance-officer", Tick: tick},
		{Op: EvidenceOpSubmit, Record: &rec},
		{Op: EvidenceOpSetStrength, EvidenceID: rec.EvidenceID(), Strength: strengthPtr(testStrength())},
		{Op: EvidenceOpVerifyStatus, EvidenceID: rec.EvidenceID()},
		{Op: EvidenceOpSetRights, EvidenceID: rec.EvidenceID(), RightsState: provenance.RightsDisputeUseAllowed, AuthorityID: authorityID},
	}
}

func strengthPtr(s evidence.Strength) *evidence.Strength { return &s }

func encodeEvidenceCommand(t *testing.T, cmd EvidenceCommand) []byte {
	t.Helper()
	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("json.Marshal(EvidenceCommand): %v", err)
	}
	return b
}

func buildEvidenceCluster(t *testing.T, n int) (*MemTransport, []*Node, []*EvidenceAdapter, context.CancelFunc) {
	t.Helper()
	trans := NewMemTransport()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = string(rune('A' + i))
	}
	nodes := make([]*Node, n)
	adapters := make([]*EvidenceAdapter, n)
	for i, id := range ids {
		peers := []string{}
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		adapter := NewEvidenceAdapter(provenance.NewRegistry(), evidence.NewRegistry())
		adapters[i] = adapter
		node := NewNode(Config{ID: id, Peers: peers, Transport: trans, FSM: adapter,
			ElectionTimeoutMin: 500 * time.Millisecond, ElectionTimeoutMax: 900 * time.Millisecond,
			HeartbeatInterval: 20 * time.Millisecond})
		node.SetSnapshotter(adapter)
		nodes[i] = node
		trans.Register(node)
	}
	ctx, cancel := context.WithCancel(context.Background())
	for _, node := range nodes {
		go node.Run(ctx)
	}
	return trans, nodes, adapters, cancel
}

func proposeEvidenceCommands(t *testing.T, leader *Node, cmds []EvidenceCommand) uint64 {
	t.Helper()
	var lastIdx uint64
	for _, cmd := range cmds {
		idx, _, err := leader.Propose(encodeEvidenceCommand(t, cmd))
		if err != nil {
			t.Fatalf("Propose(%s): %v", cmd.Op, err)
		}
		lastIdx = idx
	}
	return lastIdx
}

// TestEvidenceCluster_RightsGrantConvergesAcrossRealConsensus is the
// Evidence Record authority layer's own "Node A -> authorized -> Node B"
// proof, using real raftlite consensus: a genuine 3-node cluster
// registers a trust authority, grants it trust, submits a Record,
// assesses Strength, derives Status, and grants Rights -- all via
// Propose() on the leader -- and every follower's own, independently
// applied EvidenceAdapter converges on the identical Status/Rights,
// with the SAME authority-gate discipline (SetRights only succeeded
// because the SAME log's GrantTrust command legitimately trusted
// authorityID first).
func TestEvidenceCluster_RightsGrantConvergesAcrossRealConsensus(t *testing.T) {
	_, nodes, adapters, cancel := buildEvidenceCluster(t, 3)
	defer cancel()

	leader := waitForLeader(t, nodes, 3*time.Second)
	rec := testEvidenceRecord(t, "S-CLUSTER-1", "src", 100)
	const authorityID = "governance-lead-cluster"
	cmds := evidenceLifecycleCommands(rec, authorityID, 1)
	lastIdx := proposeEvidenceCommands(t, leader, cmds)

	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := leader.WaitApplied(ctx, lastIdx); err != nil {
		t.Fatalf("leader never applied all entries: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		allConverged := true
		for _, a := range adapters {
			_, evReg := a.Registries()
			got, ok := evReg.Get(rec.EvidenceID())
			if !ok || got.Rights != provenance.RightsDisputeUseAllowed {
				allConverged = false
				break
			}
		}
		if allConverged {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cluster never converged all nodes to the granted Rights state within timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}

	for i, a := range adapters {
		_, evReg := a.Registries()
		got, ok := evReg.Get(rec.EvidenceID())
		if !ok {
			t.Fatalf("node %d: record not found", i)
		}
		if got.Rights != provenance.RightsDisputeUseAllowed {
			t.Fatalf("node %d: expected RightsDisputeUseAllowed, got %s", i, got.Rights)
		}
		// testStrength() sets Authenticity=Supported with no independent
		// corroboration recorded, so DeriveStatus's own priority chain
		// (pkg/insurance/evidence/evidence.go) derives exactly
		// StatusAuthenticitySupported -- asserted precisely, not just
		// "non-empty", since VERIFY_STATUS replaying to the wrong
		// derived value would be exactly the kind of silent divergence
		// this test exists to catch.
		if got.Status != evidence.StatusAuthenticitySupported {
			t.Fatalf("node %d: expected Status=AUTHENTICITY_SUPPORTED (derived from testStrength()), got %s", i, got.Status)
		}
	}
}

// TestEvidenceAdapterSnapshotRestoreRoundTripPreservesRightsGrant is the
// adapter-level round-trip proof, mirroring
// TestManifestAdapterSnapshotRestoreRoundTripPreservesFinalizedState.
func TestEvidenceAdapterSnapshotRestoreRoundTripPreservesRightsGrant(t *testing.T) {
	source := NewEvidenceAdapter(provenance.NewRegistry(), evidence.NewRegistry())
	rec := testEvidenceRecord(t, "S-ROUNDTRIP-1", "src", 100)
	const authorityID = "governance-lead-roundtrip"
	for _, cmd := range evidenceLifecycleCommands(rec, authorityID, 1) {
		provReg, evReg := source.Registries()
		if err := applyEvidenceCommand(provReg, evReg, cmd); err != nil {
			t.Fatalf("applyEvidenceCommand(%s): %v", cmd.Op, err)
		}
		source.mu.Lock()
		source.log = append(source.log, cmd)
		source.mu.Unlock()
	}
	snap, err := source.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := NewEvidenceAdapter(provenance.NewRegistry(), evidence.NewRegistry())
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	_, wantEvReg := source.Registries()
	want, ok := wantEvReg.Get(rec.EvidenceID())
	if !ok {
		t.Fatal("source: record not found")
	}
	_, gotEvReg := restored.Registries()
	got, ok := gotEvReg.Get(rec.EvidenceID())
	if !ok {
		t.Fatal("restored: record not found")
	}
	if got.Rights != want.Rights || got.Status != want.Status {
		t.Fatalf("restored state diverged: got Rights=%s Status=%s, want Rights=%s Status=%s", got.Rights, got.Status, want.Rights, want.Status)
	}
	if got.Rights != provenance.RightsDisputeUseAllowed {
		t.Fatalf("expected the restored record to carry the real granted Rights, got %s", got.Rights)
	}
}

// TestEvidenceAdapterRestoreRejectsRightsGrantFromAnUntrustedAuthority
// simulates the exact attack the Evidence Record layer's INV-010 exists
// to close: a forged snapshot whose command log contains a SET_RIGHTS
// command citing an authority ID that NO REGISTER_AUTHORITY/GRANT_TRUST
// pair in the SAME log ever actually trusted. Restore's real replay
// through evidence.Registry.SetRights must refuse it
// (ErrRightsGrantNotAuthorized), fail-closed.
func TestEvidenceAdapterRestoreRejectsRightsGrantFromAnUntrustedAuthority(t *testing.T) {
	adapter := NewEvidenceAdapter(provenance.NewRegistry(), evidence.NewRegistry())
	seedRec := testEvidenceRecord(t, "S-SEED-1", "src", 100)
	provReg, evReg := adapter.Registries()
	if err := evReg.Submit(seedRec); err != nil {
		t.Fatalf("seed Submit: %v", err)
	}
	_ = provReg
	preRestoreProv, preRestoreEv := adapter.Registries()

	rec := testEvidenceRecord(t, "S-FORGED-1", "src", 200)
	forged := []EvidenceCommand{
		{Op: EvidenceOpSubmit, Record: &rec},
		// No REGISTER_AUTHORITY/GRANT_TRUST for "never-trusted-authority"
		// anywhere in this log.
		{Op: EvidenceOpSetRights, EvidenceID: rec.EvidenceID(), RightsState: provenance.RightsCustomerFacingAllowed, AuthorityID: "never-trusted-authority"},
	}
	data, err := json.Marshal(forged)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	if err := adapter.Restore(data); err == nil {
		t.Fatal("expected Restore to reject a SET_RIGHTS command citing an authority never granted trust in the same log")
	}
	gotProv, gotEv := adapter.Registries()
	if gotProv != preRestoreProv || gotEv != preRestoreEv {
		t.Fatal("expected both live registry pointers to be left untouched after a rejected Restore (fail-closed)")
	}
	if _, ok := gotEv.Get(seedRec.EvidenceID()); !ok {
		t.Fatal("expected the pre-existing seed record to survive the rejected Restore untouched")
	}
}

func TestEvidenceAdapterRestoreErrorNamesTheRealGate(t *testing.T) {
	adapter := NewEvidenceAdapter(provenance.NewRegistry(), evidence.NewRegistry())
	rec := testEvidenceRecord(t, "S-FORGED-2", "src", 300)
	forged := []EvidenceCommand{
		{Op: EvidenceOpSubmit, Record: &rec},
		{Op: EvidenceOpSetRights, EvidenceID: rec.EvidenceID(), RightsState: provenance.RightsCustomerFacingAllowed, AuthorityID: "never-trusted-authority-2"},
	}
	data, _ := json.Marshal(forged)
	err := adapter.Restore(data)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, evidence.ErrRightsGrantNotAuthorized) {
		t.Fatalf("expected the rejection to wrap evidence.ErrRightsGrantNotAuthorized, got: %v", err)
	}
}
