package raftlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/evidence/manifest"
)

// testManifestDraft mirrors pkg/evidence/manifest/manifest_test.go's own
// validDraft helper exactly (same field values), so a manifest built here
// and one built there are structurally identical -- this file is not
// inventing a second, looser fixture.
func testManifestDraft(evidenceID string) manifest.Manifest {
	return manifest.Manifest{
		TenantID: "TENANT-1", CaseID: "CASE-1", EvidenceID: evidenceID,
		URI: "s3://bucket/obj", Filename: "doc.pdf", MediaType: "application/pdf",
		ByteSize: 1024, SHA256: "sha256:deadbeef",
		Method: "manual upload", Collector: "PTY-1", Source: "claimant",
		AcquiredAt: 10, ReceivedAt: 10, System: "veriqo-cre", SystemVersion: "v1",
		HashStatus: "COMPUTED", Classification: "INTERNAL",
		AcquisitionRecord: "manual upload logged by claims handler PTY-1",
	}
}

// fullLifecycleManifestCommands returns the exact ordered command sequence
// that drives evidenceID from DRAFT to FINALIZED -- the wire-format
// equivalent of manifest_test.go's advanceThroughFullLifecycle, expressed
// as ManifestCommands instead of direct Registry calls.
func fullLifecycleManifestCommands(evidenceID string, tick uint64) []ManifestCommand {
	draft := testManifestDraft(evidenceID)
	return []ManifestCommand{
		{Op: ManifestOpRegisterDraft, Draft: &draft},
		{Op: ManifestOpRecordCustodyEvent, EvidenceID: evidenceID, EventID: evidenceID + "-received", Actor: "PTY-1", Action: manifest.CustodyReceived, Tick: tick, Reason: "received"},
		{Op: ManifestOpAdvance, EvidenceID: evidenceID, To: manifest.StateIngested, Tick: tick},
		{Op: ManifestOpRecordCustodyEvent, EvidenceID: evidenceID, EventID: evidenceID + "-hashed", Actor: "PTY-1", Action: manifest.CustodyHashed, Tick: tick, Reason: "hashed", ContentHash: "sha256:deadbeef"},
		{Op: ManifestOpAdvance, EvidenceID: evidenceID, To: manifest.StateIntegrityAssessed, Tick: tick},
		{Op: ManifestOpAdvance, EvidenceID: evidenceID, To: manifest.StateProvenanceComplete, Tick: tick},
		{Op: ManifestOpRecordCustodyEvent, EvidenceID: evidenceID, EventID: evidenceID + "-reviewed", Actor: "PTY-1", Action: manifest.CustodyReviewed, Tick: tick, Reason: "reviewed", ContentHash: "sha256:deadbeef"},
		{Op: ManifestOpAdvance, EvidenceID: evidenceID, To: manifest.StateReadyForFinalization, Tick: tick},
		{Op: ManifestOpAdvance, EvidenceID: evidenceID, To: manifest.StateFinalized, Tick: tick},
	}
}

func encodeManifestCommand(t *testing.T, cmd ManifestCommand) []byte {
	t.Helper()
	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("json.Marshal(ManifestCommand): %v", err)
	}
	return b
}

// buildManifestCluster is buildSumCluster's twin, wiring a real
// ManifestAdapter (backed by its own fresh manifest.Registry) into each
// node instead of the synthetic sumFSM -- so compaction/InstallSnapshot
// exercises this repository's actual Evidence Manifest authority layer,
// not a placeholder.
func buildManifestCluster(t *testing.T, n int) (*MemTransport, []*Node, []*ManifestAdapter, context.CancelFunc) {
	t.Helper()
	trans := NewMemTransport()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = string(rune('A' + i))
	}
	nodes := make([]*Node, n)
	adapters := make([]*ManifestAdapter, n)
	for i, id := range ids {
		peers := []string{}
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		adapter := NewManifestAdapter(manifest.NewRegistry())
		adapters[i] = adapter
		// Same widened timing as buildSumCluster (snapshot_test.go) --
		// this environment's own history of scheduler-contention flakes
		// under a heavy go test ./... run required exactly this margin.
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

func proposeManifestCommands(t *testing.T, leader *Node, cmds []ManifestCommand) uint64 {
	t.Helper()
	var lastIdx uint64
	for _, cmd := range cmds {
		idx, _, err := leader.Propose(encodeManifestCommand(t, cmd))
		if err != nil {
			t.Fatalf("Propose(%s): %v", cmd.Op, err)
		}
		lastIdx = idx
	}
	return lastIdx
}

// TestManifestCluster_FinalizedEvidenceConvergesAcrossRealConsensus is the
// reviewer's literal "Node A -> FINALIZED Evidence -> ... -> Node B"
// scenario, answered for real this time: a genuine 3-node raftlite
// cluster (leader election, AppendEntries replication, majority commit)
// drives one EvidenceID through its full lifecycle to FINALIZED via
// Propose() on the leader, and every follower's OWN, independently
// applied ManifestAdapter converges on the byte-identical ManifestHash
// and CustodyChainHead -- via the real committed-log replication path,
// not two Registry instances driven by the same test goroutine.
func TestManifestCluster_FinalizedEvidenceConvergesAcrossRealConsensus(t *testing.T) {
	_, nodes, adapters, cancel := buildManifestCluster(t, 3)
	defer cancel()

	leader := waitForLeader(t, nodes, 3*time.Second)
	const evidenceID = "EV-CLUSTER-1"
	cmds := fullLifecycleManifestCommands(evidenceID, 10)
	lastIdx := proposeManifestCommands(t, leader, cmds)

	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := leader.WaitApplied(ctx, lastIdx); err != nil {
		t.Fatalf("leader never applied all entries: %v", err)
	}

	// Give followers a moment to receive and apply the same committed
	// entries via real AppendEntries RPCs.
	var want manifest.Manifest
	deadline := time.Now().Add(3 * time.Second)
	for {
		allConverged := true
		for _, a := range adapters {
			m, err := a.Registry().Latest(evidenceID)
			if err != nil || m.State != manifest.StateFinalized {
				allConverged = false
				break
			}
		}
		if allConverged {
			want, _ = adapters[0].Registry().Latest(evidenceID)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cluster never converged all nodes to FINALIZED within timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}

	for i, a := range adapters {
		got, err := a.Registry().Latest(evidenceID)
		if err != nil {
			t.Fatalf("node %d: Latest: %v", i, err)
		}
		if got.State != manifest.StateFinalized {
			t.Fatalf("node %d: expected FINALIZED, got %s", i, got.State)
		}
		if got.ManifestHash != want.ManifestHash {
			t.Fatalf("node %d: ManifestHash diverged from node 0: got=%s want=%s", i, got.ManifestHash, want.ManifestHash)
		}
		if got.CustodyChainHead != want.CustodyChainHead {
			t.Fatalf("node %d: CustodyChainHead diverged from node 0: got=%s want=%s", i, got.CustodyChainHead, want.CustodyChainHead)
		}
		if err := manifest.VerifyManifestHash(got); err != nil {
			t.Fatalf("node %d: its own FINALIZED hash must independently verify: %v", i, err)
		}
		if err := a.Registry().VerifyCustodyChain(evidenceID); err != nil {
			t.Fatalf("node %d: its own custody chain must independently verify: %v", i, err)
		}
	}
}

// TestManifestCluster_StragglerCatchesUpViaRealInstallSnapshot is
// InstallSnapshot_StragglerCatchUpViaCompaction's twin for the manifest
// authority layer: a follower isolated long enough for the connected
// majority to compact past what it has can ONLY catch up via a real
// InstallSnapshot RPC carrying ManifestAdapter.Snapshot()'s bytes -- this
// is "snapshot restoration" made live-integrated, not merely reasoned
// about.
func TestManifestCluster_StragglerCatchesUpViaRealInstallSnapshot(t *testing.T) {
	trans, nodes, adapters, cancel := buildManifestCluster(t, 3)
	defer cancel()

	leader := waitForLeader(t, nodes, 3*time.Second)
	var leaderAdapter, stragglerAdapter *ManifestAdapter
	var straggler *Node
	for i, n := range nodes {
		if n == leader {
			leaderAdapter = adapters[i]
			continue
		}
		if straggler == nil {
			straggler = n
			stragglerAdapter = adapters[i]
		}
	}

	for _, n := range nodes {
		if n == straggler {
			continue
		}
		trans.DropTo(n.id, straggler.id)
		trans.DropTo(straggler.id, n.id)
	}

	// Drive enough full-lifecycle EvidenceIDs (9 commands each) past
	// DefaultCompactionThreshold=64 so the connected majority is forced
	// to compact past what the straggler has.
	const evidenceCount = 10 // 90 commands total
	var lastIdx uint64
	var finalEvidenceID string
	for i := 0; i < evidenceCount; i++ {
		evidenceID := "EV-STRAGGLER-" + string(rune('A'+i))
		finalEvidenceID = evidenceID
		lastIdx = proposeManifestCommands(t, leader, fullLifecycleManifestCommands(evidenceID, 10))
	}

	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := leader.WaitApplied(ctx, lastIdx); err != nil {
		t.Fatalf("leader never applied all entries: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for leader.LastSnapshotIndex() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if leader.LastSnapshotIndex() == 0 {
		t.Fatal("leader never compacted its log — test setup invalid, cannot prove InstallSnapshot path")
	}

	for _, n := range nodes {
		if n == straggler {
			continue
		}
		trans.HealTo(n.id, straggler.id)
		trans.HealTo(straggler.id, n.id)
	}

	if _, err := leaderAdapter.Registry().Latest(finalEvidenceID); err != nil {
		t.Fatalf("leader-side Latest: %v", err)
	}

	deadline = time.Now().Add(3 * time.Second)
	var stragglerManifest manifest.Manifest
	for time.Now().Before(deadline) {
		m, err := stragglerAdapter.Registry().Latest(finalEvidenceID)
		if err == nil && m.State == manifest.StateFinalized {
			stragglerManifest = m
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stragglerManifest.State != manifest.StateFinalized {
		t.Fatalf("straggler never converged to FINALIZED for %s", finalEvidenceID)
	}

	// The hard assertion: the straggler's state MUST have arrived via a
	// real InstallSnapshot RPC (the entries it needs no longer exist as
	// log entries anywhere in the cluster), not a lucky AppendEntries
	// replay.
	if straggler.LastSnapshotIndex() == 0 {
		t.Fatal("straggler converged but LastSnapshotIndex()==0 — it did not go through InstallSnapshot")
	}

	leaderManifest, err := leaderAdapter.Registry().Latest(finalEvidenceID)
	if err != nil {
		t.Fatalf("leader Latest: %v", err)
	}
	if stragglerManifest.ManifestHash != leaderManifest.ManifestHash {
		t.Fatalf("straggler's post-InstallSnapshot ManifestHash diverged: got=%s want=%s", stragglerManifest.ManifestHash, leaderManifest.ManifestHash)
	}
	if err := manifest.VerifyManifestHash(stragglerManifest); err != nil {
		t.Fatalf("straggler's InstallSnapshot-restored manifest must independently verify: %v", err)
	}
	if err := stragglerAdapter.Registry().VerifyCustodyChain(finalEvidenceID); err != nil {
		t.Fatalf("straggler's InstallSnapshot-restored custody chain must independently verify: %v", err)
	}
	t.Logf("straggler caught up via real InstallSnapshot: LastSnapshotIndex=%d ManifestHash=%s (matches leader)", straggler.LastSnapshotIndex(), stragglerManifest.ManifestHash)
}

// TestManifestAdapterSnapshotRestoreRoundTripPreservesFinalizedState is
// the adapter-level (no cluster needed) honest round-trip proof: what
// Snapshot() emits, a fresh adapter's Restore() reconstructs byte-for-
// byte identically.
func TestManifestAdapterSnapshotRestoreRoundTripPreservesFinalizedState(t *testing.T) {
	source := NewManifestAdapter(manifest.NewRegistry())
	const evidenceID = "EV-ROUNDTRIP-1"
	for _, cmd := range fullLifecycleManifestCommands(evidenceID, 10) {
		if err := applyManifestCommand(source.Registry(), cmd); err != nil {
			t.Fatalf("applyManifestCommand(%s): %v", cmd.Op, err)
		}
		source.mu.Lock()
		source.log = append(source.log, cmd)
		source.mu.Unlock()
	}
	snap, err := source.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := NewManifestAdapter(manifest.NewRegistry())
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	want, err := source.Registry().Latest(evidenceID)
	if err != nil {
		t.Fatalf("source Latest: %v", err)
	}
	got, err := restored.Registry().Latest(evidenceID)
	if err != nil {
		t.Fatalf("restored Latest: %v", err)
	}
	if got.ManifestHash != want.ManifestHash || got.State != manifest.StateFinalized {
		t.Fatalf("restored state diverged: got State=%s Hash=%s, want State=%s Hash=%s", got.State, got.ManifestHash, want.State, want.ManifestHash)
	}
	if err := manifest.VerifyManifestHash(got); err != nil {
		t.Fatalf("restored manifest must independently verify: %v", err)
	}
}

// TestManifestAdapterRestoreRejectsAForgedFinalizeWithNoPrerequisites
// simulates an attacker who intercepts a real snapshot payload and
// splices in a fabricated ADVANCE-to-FINALIZED command for a brand-new
// EvidenceID with NO preceding RegisterDraft/custody events at all --
// i.e. a forged snapshot trying to manufacture FINALIZED authority
// out of nothing. Restore's real replay through applyManifestCommand
// must refuse it (the target Registry doesn't even exist yet, so
// ErrManifestNotFound fires), and fail-closed: the adapter's live
// Registry must be left completely untouched.
func TestManifestAdapterRestoreRejectsAForgedFinalizeWithNoPrerequisites(t *testing.T) {
	adapter := NewManifestAdapter(manifest.NewRegistry())
	// Seed the live registry with something real, so we can prove it
	// survives the rejected Restore untouched.
	seedDraft := testManifestDraft("EV-SEED-1")
	if _, err := adapter.Registry().RegisterDraft(seedDraft); err != nil {
		t.Fatalf("seed RegisterDraft: %v", err)
	}
	preRestoreRegistry := adapter.Registry()

	forged := []ManifestCommand{
		{Op: ManifestOpAdvance, EvidenceID: "EV-FORGED-NEVER-REGISTERED", To: manifest.StateFinalized, Tick: 10},
	}
	data, err := json.Marshal(forged)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	if err := adapter.Restore(data); err == nil {
		t.Fatal("expected Restore to reject a forged FINALIZED command with no RegisterDraft behind it")
	}
	if adapter.Registry() != preRestoreRegistry {
		t.Fatal("expected the live Registry pointer to be left untouched after a rejected Restore (fail-closed)")
	}
	if _, err := adapter.Registry().Latest("EV-SEED-1"); err != nil {
		t.Fatalf("expected the pre-existing seed manifest to survive the rejected Restore untouched: %v", err)
	}
}

// TestManifestAdapterRestoreRejectsAReplayOmittingReviewedEvent is the
// same attack TestReplayOmittingReviewedEventCannotReachFinalized proves
// against the bare Registry, now proven against the real Snapshot/Restore
// wire path: a command log that drops the REVIEWED custody event but
// still tries to ADVANCE all the way to FINALIZED must be refused by
// Restore's replay, not silently accepted.
func TestManifestAdapterRestoreRejectsAReplayOmittingReviewedEvent(t *testing.T) {
	adapter := NewManifestAdapter(manifest.NewRegistry())
	const evidenceID = "EV-OMIT-REVIEWED-1"
	full := fullLifecycleManifestCommands(evidenceID, 10)

	var tampered []ManifestCommand
	for _, cmd := range full {
		if cmd.Op == ManifestOpRecordCustodyEvent && cmd.Action == manifest.CustodyReviewed {
			continue // the attacker's log drops this event
		}
		tampered = append(tampered, cmd)
	}
	data, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	preRestoreRegistry := adapter.Registry()
	if err := adapter.Restore(data); err == nil {
		t.Fatal("expected Restore to reject a command log that omits the REVIEWED custody event")
	}
	if adapter.Registry() != preRestoreRegistry {
		t.Fatal("expected the live Registry pointer to be left untouched after a rejected Restore (fail-closed)")
	}
}

// TestManifestAdapterRestoreErrorMessageNamesTheRealGate is a lightweight
// sanity check that the forged-log rejection above is really coming from
// manifest's own transitionPrerequisiteLocked gate (via
// ErrTransitionPrerequisiteNotMet), not from some unrelated JSON/decoding
// error that would pass even a log with a real prerequisite gap.
func TestManifestAdapterRestoreErrorMessageNamesTheRealGate(t *testing.T) {
	adapter := NewManifestAdapter(manifest.NewRegistry())
	const evidenceID = "EV-OMIT-REVIEWED-2"
	full := fullLifecycleManifestCommands(evidenceID, 10)
	var tampered []ManifestCommand
	for _, cmd := range full {
		if cmd.Op == ManifestOpRecordCustodyEvent && cmd.Action == manifest.CustodyReviewed {
			continue
		}
		tampered = append(tampered, cmd)
	}
	data, _ := json.Marshal(tampered)
	err := adapter.Restore(data)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, manifest.ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected the rejection to wrap manifest.ErrTransitionPrerequisiteNotMet, got: %v", err)
	}
	if !strings.Contains(err.Error(), string(manifest.StateReadyForFinalization)) {
		t.Fatalf("expected the error to name the specific transition that failed, got: %v", err)
	}
}
