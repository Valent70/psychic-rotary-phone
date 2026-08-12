package entity

import "testing"

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestThreeAliasesResolveToSameCanonicalEntity is the concrete case
// the audit named: "Vessel A / IMO123 / Callsign XYZ" arriving as
// three different strings must resolve to one canonical entity.
func TestThreeAliasesResolveToSameCanonicalEntity(t *testing.T) {
	r := NewRegistry()
	imo := Alias{Kind: "IMO", Value: "9998887"}
	callsign := Alias{Kind: "CALLSIGN", Value: "ABCD1"}
	name := Alias{Kind: "NAME", Value: "MV EXAMPLE"}

	_, err := r.Merge("port-authority", imo, callsign, 1)
	must(t, err)
	_, err = r.Merge("vessel-registry", callsign, name, 2)
	must(t, err) // transitively links name -> callsign -> imo

	idIMO, ok := r.Resolve(imo)
	if !ok {
		t.Fatalf("expected IMO alias to resolve")
	}
	idCallsign, _ := r.Resolve(callsign)
	idName, _ := r.Resolve(name)

	if idIMO != idCallsign || idCallsign != idName {
		t.Fatalf("expected all three aliases to resolve to the same canonical ID, got imo=%s callsign=%s name=%s", idIMO, idCallsign, idName)
	}

	aliases := r.AliasesFor(imo)
	if len(aliases) != 3 {
		t.Fatalf("expected 3 aliases in the resolved entity, got %d: %+v", len(aliases), aliases)
	}
}

// TestUnrelatedAliasesResolveDifferently proves the registry doesn't
// collapse everything into one entity.
func TestUnrelatedAliasesResolveDifferently(t *testing.T) {
	r := NewRegistry()
	a, err := r.Register("x", Alias{Kind: "IMO", Value: "1111111"}, 1)
	must(t, err)
	b, err := r.Register("x", Alias{Kind: "IMO", Value: "2222222"}, 1)
	must(t, err)
	if a == b {
		t.Fatalf("expected unrelated vessels to have different canonical IDs")
	}
}

// TestCanonicalIDIsMergeOrderInvariant is the core correctness proof:
// the same final equivalence class must produce the same canonical ID
// regardless of which pairs were merged in which order.
func TestCanonicalIDIsMergeOrderInvariant(t *testing.T) {
	imo := Alias{Kind: "IMO", Value: "9998887"}
	callsign := Alias{Kind: "CALLSIGN", Value: "ABCD1"}
	name := Alias{Kind: "NAME", Value: "MV EXAMPLE"}
	mmsi := Alias{Kind: "MMSI", Value: "123456789"}

	r1 := NewRegistry()
	mergeOK(t, r1, "a", imo, callsign, 1)
	mergeOK(t, r1, "a", callsign, name, 2)
	mergeOK(t, r1, "a", name, mmsi, 3)
	id1, _ := r1.Resolve(imo)

	r2 := NewRegistry()
	mergeOK(t, r2, "a", mmsi, name, 1)
	mergeOK(t, r2, "a", name, callsign, 2)
	mergeOK(t, r2, "a", callsign, imo, 3)
	id2, _ := r2.Resolve(imo)

	if id1 != id2 {
		t.Fatalf("expected merge-order-invariant canonical ID, got %s vs %s", id1, id2)
	}
}

func mergeOK(t *testing.T, r *Registry, actor string, a, b Alias, tick uint64) CanonicalID {
	t.Helper()
	id, err := r.Merge(actor, a, b, tick)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestVerifyChainDetectsTamper proves the ledger's hash chain is
// tamper-evident, consistent with every sibling MOAT package.
func TestVerifyChainDetectsTamper(t *testing.T) {
	r := NewRegistry()
	_, err := r.Merge("a", Alias{Kind: "IMO", Value: "1"}, Alias{Kind: "CALLSIGN", Value: "X"}, 1)
	must(t, err)
	if err := r.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain before tamper, got %v", err)
	}
	r.log[0].CanonicalID = "tampered"
	if err := r.VerifyChain(); err == nil {
		t.Fatalf("expected VerifyChain to detect tampering")
	}
}

// TestRebuildReproducesCanonicalIDsFromLedgerAlone is the replay
// determinism proof: given only the append-only merge log, Rebuild
// must reproduce the exact same canonical IDs the live registry
// computed.
func TestRebuildReproducesCanonicalIDsFromLedgerAlone(t *testing.T) {
	live := NewRegistry()
	imo := Alias{Kind: "IMO", Value: "9998887"}
	callsign := Alias{Kind: "CALLSIGN", Value: "ABCD1"}
	name := Alias{Kind: "NAME", Value: "MV EXAMPLE"}
	mergeOK(t, live, "a", imo, callsign, 1)
	mergeOK(t, live, "b", callsign, name, 2)

	liveID, _ := live.Resolve(imo)

	replayed, err := Rebuild(live.ReplayAll())
	must(t, err)
	replayedID, ok := replayed.Resolve(imo)
	if !ok {
		t.Fatalf("expected replayed registry to resolve imo alias")
	}
	if replayedID != liveID {
		t.Fatalf("Rebuild canonical ID %s != live registry canonical ID %s", replayedID, liveID)
	}
	if replayed.Head() != live.Head() {
		t.Fatalf("Rebuild head hash %s != live head hash %s", replayed.Head(), live.Head())
	}
}

// TestMergeRejectsEmptyAlias guards against silent malformed input.
func TestMergeRejectsEmptyAlias(t *testing.T) {
	r := NewRegistry()
	_, err := r.Merge("a", Alias{Kind: "", Value: "x"}, Alias{Kind: "IMO", Value: "1"}, 1)
	if err == nil {
		t.Fatalf("expected error for empty alias Kind")
	}
}

// TestNoOpMergeStillRecorded proves an already-equivalent merge is
// still appended to the audit trail rather than silently dropped.
func TestNoOpMergeStillRecorded(t *testing.T) {
	r := NewRegistry()
	imo := Alias{Kind: "IMO", Value: "9998887"}
	callsign := Alias{Kind: "CALLSIGN", Value: "ABCD1"}
	mergeOK(t, r, "a", imo, callsign, 1)
	mergeOK(t, r, "a", imo, callsign, 2) // already same class
	if len(r.ReplayAll()) != 2 {
		t.Fatalf("expected the redundant merge to still be recorded, got %d records", len(r.ReplayAll()))
	}
}
