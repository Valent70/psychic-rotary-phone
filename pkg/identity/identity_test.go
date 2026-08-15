package identity

import (
	"encoding/json"
	"errors"
	"testing"
)

func imo(v string) Identifier      { return Identifier{Kind: KindIMO, Value: v} }
func callsign(v string) Identifier { return Identifier{Kind: KindCallsign, Value: v} }
func name(v string) Identifier     { return Identifier{Kind: KindName, Value: v} }

func newResolverWithAuthorities(t *testing.T) *Resolver {
	t.Helper()
	r := NewResolver()
	must(t, r.RegisterAuthority(Authority{SourceID: "flag-registry", Weight: 1.0,
		AuthoritativeFor: []Kind{KindIMO, KindRegistryID, KindFlag}}))
	must(t, r.RegisterAuthority(Authority{SourceID: "aggregator", Weight: 0.4}))
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnregisteredSourceCannotAssertIdentity(t *testing.T) {
	r := newResolverWithAuthorities(t)
	if _, err := r.Assert("actor", "some-scraper", imo("9998887"), callsign("ABCD"), 1, "scraped"); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("unregistered source asserted identity: %v", err)
	}
}

func TestMalformedIdentifierRejected(t *testing.T) {
	r := newResolverWithAuthorities(t)
	cases := []Identifier{{Kind: "", Value: "x"}, {Kind: KindIMO, Value: "  "}, {Kind: "NOPE", Value: "x"}}
	for _, c := range cases {
		if _, err := r.Assert("a", "flag-registry", c, imo("1"), 1, "r"); err == nil {
			t.Fatalf("malformed identifier accepted: %+v", c)
		}
	}
}

func TestAuthorityAffectsConfidence(t *testing.T) {
	r := newResolverWithAuthorities(t)
	authoritative, err := r.Assert("a", "flag-registry", imo("9998887"), imo("9998887"), 1, "self")
	must(t, err)
	weak, err := r.Assert("a", "aggregator", imo("1111111"), imo("1111111"), 1, "self")
	must(t, err)
	if authoritative.Confidence <= weak.Confidence {
		t.Fatalf("authority ignored: authoritative=%.3f weak=%.3f", authoritative.Confidence, weak.Confidence)
	}
}

func TestDiscriminatingPowerRanksIMOAboveName(t *testing.T) {
	r := newResolverWithAuthorities(t)
	strong, err := r.Assert("a", "flag-registry", imo("9998887"), imo("9998888"), 1, "registry link")
	must(t, err)
	weak, err := r.Assert("a", "flag-registry", name("EVER GIVEN"), name("EVER GIVEN"), 1, "name match")
	must(t, err)
	if strong.Confidence <= weak.Confidence {
		t.Fatalf("a shared NAME scored as high as a shared IMO: %.3f vs %.3f", weak.Confidence, strong.Confidence)
	}
}

func TestMergeBindsIdentifiersAndIsOrderInvariant(t *testing.T) {
	a := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return a.Merge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 10, "verified")
	})
	must2(t, func() (Event, error) {
		return a.Merge("op", "flag-registry", callsign("ABCD"), name("EVER GIVEN"), 11, "verified")
	})

	b := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return b.Merge("op", "flag-registry", name("EVER GIVEN"), callsign("ABCD"), 10, "verified")
	})
	must2(t, func() (Event, error) {
		return b.Merge("op", "flag-registry", callsign("ABCD"), imo("9998887"), 11, "verified")
	})

	ida, err := a.EntityIDAt(imo("9998887"), 100)
	must(t, err)
	idb, err := b.EntityIDAt(name("EVER GIVEN"), 100)
	must(t, err)
	if ida != idb {
		t.Fatalf("entity ID is not merge-order invariant: %s vs %s", ida, idb)
	}
	same, err := a.SameEntityAt(imo("9998887"), name("EVER GIVEN"), 100)
	must(t, err)
	if !same {
		t.Fatal("transitively merged identifiers did not resolve to one entity")
	}
}

// TestUnmergePreservesHistoricalReplay is the audit's named invariant:
//
//	Entity A merged with B -> later proven incorrect -> UNMERGE ->
//	historical replay remains valid.
func TestUnmergePreservesHistoricalReplay(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return r.Merge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 10, "believed same")
	})

	beforeID, err := r.EntityIDAt(imo("9998887"), 50)
	must(t, err)
	same, err := r.SameEntityAt(imo("9998887"), callsign("ABCD"), 50)
	must(t, err)
	if !same {
		t.Fatal("merge did not take effect")
	}

	// The merge is later proven wrong.
	if _, err := r.Unmerge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 100, "callsign reassigned"); err != nil {
		t.Fatalf("Unmerge: %v", err)
	}

	// Present-day resolution reflects the correction.
	nowSame, err := r.SameEntityAt(imo("9998887"), callsign("ABCD"), 200)
	must(t, err)
	if nowSame {
		t.Fatal("unmerge did not take effect for current resolution")
	}

	// Historical replay at tick 50 STILL returns what VERIQO believed
	// then — the whole point.
	histSame, err := r.SameEntityAt(imo("9998887"), callsign("ABCD"), 50)
	must(t, err)
	if !histSame {
		t.Fatal("unmerge retroactively rewrote history — replay of an earlier decision is now invalid")
	}
	histID, err := r.EntityIDAt(imo("9998887"), 50)
	must(t, err)
	if histID != beforeID {
		t.Fatalf("historical entity ID changed after unmerge: %s -> %s", beforeID, histID)
	}
}

func TestUnmergeRequiresAnExistingMerge(t *testing.T) {
	r := newResolverWithAuthorities(t)
	if _, err := r.Unmerge("op", "flag-registry", imo("1"), imo("2"), 1, "nope"); !errors.Is(err, ErrNotMerged) {
		t.Fatalf("unmerge of unmerged identifiers accepted: %v", err)
	}
}

func TestConflictDetectedBetweenCloseCandidates(t *testing.T) {
	r := newResolverWithAuthorities(t)
	// Two equally-authoritative assertions pointing at different
	// entities.
	must2(t, func() (Event, error) {
		return r.Assert("a", "flag-registry", callsign("ABCD"), imo("1111111"), 1, "registry A")
	})
	must2(t, func() (Event, error) {
		return r.Assert("a", "flag-registry", callsign("ABCD"), imo("2222222"), 1, "registry B")
	})
	_, err := r.Resolve(callsign("ABCD"), 10, 0)
	if !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("conflicting identity assertions did not raise a conflict: %v", err)
	}
}

func TestLowConfidenceRejectedByThreshold(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return r.Assert("a", "aggregator", name("SEA STAR"), imo("3333333"), 1, "weak name match")
	})
	if _, err := r.ResolveWithThreshold(name("SEA STAR"), 10, 0.9); !errors.Is(err, ErrLowConfidence) {
		t.Fatalf("weak identity evidence accepted at high threshold: %v", err)
	}
}

func TestCorroborationRaisesConfidenceButNeverReachesOne(t *testing.T) {
	r := newResolverWithAuthorities(t)
	for i := 0; i < 20; i++ {
		must2(t, func() (Event, error) {
			return r.Assert("a", "aggregator", callsign("WXYZ"), imo("4444444"), uint64(i), "repeat")
		})
	}
	cands, err := r.Candidates(callsign("WXYZ"), 100)
	must(t, err)
	if len(cands) == 0 {
		t.Fatal("no candidates")
	}
	if cands[0].Confidence >= 1 {
		t.Fatalf("unbounded confidence inflation from repeated weak assertions: %.6f", cands[0].Confidence)
	}
	if cands[0].Confidence <= 0.4 {
		t.Fatalf("corroboration did not raise confidence at all: %.6f", cands[0].Confidence)
	}
}

func TestCorrectionRedirectsResolution(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return r.Merge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 5, "ok")
	})
	must2(t, func() (Event, error) {
		return r.Correct("op", "flag-registry", imo("9998880"), imo("9998887"), 10, "typo in source feed")
	})
	id, err := r.EntityIDAt(imo("9998880"), 20)
	must(t, err)
	want, err := r.EntityIDAt(imo("9998887"), 20)
	must(t, err)
	if id != want {
		t.Fatal("correction did not redirect resolution")
	}
	// Before the correction was known, the wrong identifier was its own
	// entity.
	before, err := r.EntityIDAt(imo("9998880"), 6)
	must(t, err)
	if before == want {
		t.Fatal("correction leaked backwards in time")
	}
}

func TestLedgerChainAndRebuild(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) { return r.Merge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 1, "a") })
	must2(t, func() (Event, error) {
		return r.Assert("op", "aggregator", callsign("ABCD"), name("EVER GIVEN"), 2, "b")
	})
	must2(t, func() (Event, error) {
		return r.Unmerge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 3, "c")
	})
	must(t, r.VerifyChain())

	rebuilt, err := Rebuild(r.Ledger(), []Authority{
		{SourceID: "flag-registry", Weight: 1, AuthoritativeFor: []Kind{KindIMO}},
		{SourceID: "aggregator", Weight: 0.4},
	})
	must(t, err)
	if rebuilt.Head() != r.Head() {
		t.Fatal("rebuilt identity ledger has a different head")
	}
	for _, tick := range []uint64{0, 1, 2, 3, 100} {
		a, _ := r.EntityIDAt(imo("9998887"), tick)
		b, _ := rebuilt.EntityIDAt(imo("9998887"), tick)
		if a != b {
			t.Fatalf("rebuilt resolver diverged at tick %d: %s vs %s", tick, a, b)
		}
	}

	tampered := r.Ledger()
	tampered[1].Confidence = 0.99
	if _, err := Rebuild(tampered, nil); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("tampered identity ledger rebuilt: %v", err)
	}
}

// TestVerifyColdReplayMatchesRealExport is the unit-level proof behind
// cmd/veriqo-cold-replay's own entity-resolution closure (audit item
// P0-E): a real ledger, a real set of EntityIDAt queries recorded
// against it, marshaled to JSON and handed to VerifyColdReplay exactly
// the way a genuinely separate process would receive it, must report
// Matched=true.
func TestVerifyColdReplayMatchesRealExport(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) { return r.Merge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 1, "a") })

	entityID, err := r.EntityIDAt(imo("9998887"), 1)
	must(t, err)

	exp := ColdReplayExport{
		Ledger: r.Ledger(),
		Authorities: []Authority{
			{SourceID: "flag-registry", Weight: 1, AuthoritativeFor: []Kind{KindIMO}},
			{SourceID: "aggregator", Weight: 0.4},
		},
		Queries: []ColdReplayQuery{
			{Alias: imo("9998887"), AsOfTick: 1, ExpectedEntityID: entityID},
			{Alias: callsign("ABCD"), AsOfTick: 1, ExpectedEntityID: entityID},
		},
	}
	data, err := json.Marshal(exp)
	must(t, err)

	v, err := VerifyColdReplay(data)
	must(t, err)
	if !v.Matched {
		t.Fatalf("expected a genuine export to match, got mismatches: %v", v.Mismatches)
	}
	if v.QueriesChecked != 2 {
		t.Fatalf("expected 2 queries checked, got %d", v.QueriesChecked)
	}
}

// TestVerifyColdReplayCatchesAWrongExpectation is the adversarial
// proof this is a real check, not a rubber stamp: an export whose
// ExpectedEntityID does not match what the ledger actually resolves to
// must be reported as a mismatch.
func TestVerifyColdReplayCatchesAWrongExpectation(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) { return r.Merge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 1, "a") })

	exp := ColdReplayExport{
		Ledger:      r.Ledger(),
		Authorities: []Authority{{SourceID: "flag-registry", Weight: 1, AuthoritativeFor: []Kind{KindIMO}}},
		Queries:     []ColdReplayQuery{{Alias: imo("9998887"), AsOfTick: 1, ExpectedEntityID: "not-the-real-entity-id"}},
	}
	data, err := json.Marshal(exp)
	must(t, err)

	v, err := VerifyColdReplay(data)
	must(t, err)
	if v.Matched {
		t.Fatal("expected a wrong expected entity ID to be caught, not silently matched")
	}
	if len(v.Mismatches) != 1 {
		t.Fatalf("expected exactly 1 mismatch, got %v", v.Mismatches)
	}
}

// TestVerifyColdReplayRefusesATamperedLedger proves the same fail-
// closed guarantee Rebuild already provides propagates through
// VerifyColdReplay: a corrupted ledger must be refused outright, not
// silently rebuilt and queried against.
func TestVerifyColdReplayRefusesATamperedLedger(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) { return r.Merge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 1, "a") })

	tampered := r.Ledger()
	tampered[0].Confidence = 0.99
	exp := ColdReplayExport{Ledger: tampered}
	data, err := json.Marshal(exp)
	must(t, err)

	if _, err := VerifyColdReplay(data); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("expected a tampered ledger to be refused with ErrChainBroken, got %v", err)
	}
}

func TestHistoryReturnsEveryTouchingEvent(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) { return r.Merge("op", "flag-registry", imo("9998887"), callsign("ABCD"), 1, "a") })
	must2(t, func() (Event, error) { return r.Assert("op", "aggregator", callsign("ABCD"), name("X"), 2, "b") })
	if got := len(r.History(callsign("ABCD"))); got != 2 {
		t.Fatalf("expected 2 history events, got %d", got)
	}
	if got := len(r.History(imo("9998887"))); got != 1 {
		t.Fatalf("expected 1 history event, got %d", got)
	}
}

func TestKnownKindsCoversTheAuditList(t *testing.T) {
	if len(KnownKinds()) != 20 {
		t.Fatalf("expected 20 identifier kinds (13 original + 7 added for audit item P0-03: Charterer, "+
			"Terminal, Refinery, Trade, Shipment, BillOfLading, GeographicEntity), got %d", len(KnownKinds()))
	}
}

func org(v string) Identifier      { return Identifier{Kind: KindOrganization, Value: v} }
func registry(v string) Identifier { return Identifier{Kind: KindRegistryID, Value: v} }

// --- Audit item P0-03 adversarial scenarios ---------------------------

// TestVesselRenamingPreservesIdentityAndDoesNotAdoptTheOldName is the
// audit's named "vessel renaming" scenario: a vessel keeps its IMO but
// changes its displayed name (a routine, legitimate event). Identity
// must survive the rename (old and new names both resolve to the one
// vessel), and if a DIFFERENT vessel later reuses the freed-up old
// name, that must NOT silently merge it into the renamed vessel's
// identity -- NAME alone is far too weak evidence for that.
func TestVesselRenamingPreservesIdentityAndDoesNotAdoptTheOldName(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return r.Merge("op", "flag-registry", imo("9998887"), name("SEA STAR"), 1, "vessel registered")
	})
	must2(t, func() (Event, error) {
		return r.Merge("op", "flag-registry", imo("9998887"), name("OCEAN PRIDE"), 10, "renamed per IMO filing")
	})

	same, err := r.SameEntityAt(name("SEA STAR"), name("OCEAN PRIDE"), 20)
	must(t, err)
	if !same {
		t.Fatal("renamed vessel's old and new names no longer resolve to the same entity")
	}

	// A different, unrelated vessel later reuses the freed-up old name.
	// Only a weak NAME-based signal links it to the renamed vessel's
	// history -- that must not be treated as identity.
	must2(t, func() (Event, error) {
		return r.Assert("op", "aggregator", imo("1112223"), name("SEA STAR"), 30, "new vessel registered under the freed name")
	})
	stillSame, err := r.SameEntityAt(imo("9998887"), imo("1112223"), 40)
	must(t, err)
	if stillSame {
		t.Fatal("a different vessel reusing a freed vessel name was incorrectly merged into the original vessel's identity")
	}
}

// TestVesselSaleChangesOwnerWithoutMergingOldAndNewOwnerEntities is the
// audit's named "vessel sale" scenario: ownership is a RELATIONSHIP
// between two distinct entities (vessel, owner), not an identity claim
// -- so it must be recorded via Assert (evidence), never Merge (same
// entity). Two different owners of the same vessel over time must
// never be treated as the same organization.
func TestVesselSaleChangesOwnerWithoutMergingOldAndNewOwnerEntities(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return r.Merge("op", "flag-registry", imo("9998887"), name("SEA STAR"), 1, "vessel registered")
	})
	must2(t, func() (Event, error) {
		return r.Assert("op", "flag-registry", imo("9998887"), org("SHIPCO ALPHA"), 5, "ownership at registration")
	})
	// The vessel is sold.
	must2(t, func() (Event, error) {
		return r.Assert("op", "flag-registry", imo("9998887"), org("SHIPCO BETA"), 20, "bill of sale filed")
	})

	sameOwner, err := r.SameEntityAt(org("SHIPCO ALPHA"), org("SHIPCO BETA"), 30)
	must(t, err)
	if sameOwner {
		t.Fatal("the vessel's former and current owners were incorrectly merged into one entity")
	}
	// The vessel's own identity must be completely unaffected by who
	// owns it.
	before, err := r.EntityIDAt(imo("9998887"), 5)
	must(t, err)
	after, err := r.EntityIDAt(imo("9998887"), 30)
	must(t, err)
	if before != after {
		t.Fatal("the vessel's identity changed when its ownership changed")
	}
}

// TestCompanyMergerCombinesTwoOrganizationsGoingForwardOnly is the
// audit's named "company merger" scenario: two previously-distinct
// companies become one entity at the tick the merger is filed, and
// historical replay before that tick must still show them separate.
func TestCompanyMergerCombinesTwoOrganizationsGoingForwardOnly(t *testing.T) {
	r := newResolverWithAuthorities(t)
	beforeMerger, err := r.SameEntityAt(org("HOLDCO ALPHA"), org("HOLDCO BETA"), 10)
	must(t, err)
	if beforeMerger {
		t.Fatal("two independent companies were already treated as the same entity before any merger event")
	}

	must2(t, func() (Event, error) {
		return r.Merge("op", "flag-registry", org("HOLDCO ALPHA"), org("HOLDCO BETA"), 50, "corporate merger, registry filing 2026-M-1")
	})

	afterMerger, err := r.SameEntityAt(org("HOLDCO ALPHA"), org("HOLDCO BETA"), 60)
	must(t, err)
	if !afterMerger {
		t.Fatal("a filed corporate merger did not combine the two organizations' identity")
	}
	stillSeparateHistorically, err := r.SameEntityAt(org("HOLDCO ALPHA"), org("HOLDCO BETA"), 10)
	must(t, err)
	if stillSeparateHistorically {
		t.Fatal("the merger retroactively rewrote history -- the two companies now appear merged before the merger was ever filed")
	}
}

// TestCompanySplitUnmergesFormerlyCombinedOrganizations is the audit's
// named "company split" scenario: a conglomerate divests a division
// into an independent company, using Unmerge -- the same mechanism
// that closes a wrongly-merged vessel identity, applied here to an
// intentional, correct corporate separation.
func TestCompanySplitUnmergesFormerlyCombinedOrganizations(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return r.Merge("op", "flag-registry", org("CONGLOMERATE"), org("DIVISION SPINCO"), 1, "division operated under parent entity")
	})
	stillOneCompany, err := r.SameEntityAt(org("CONGLOMERATE"), org("DIVISION SPINCO"), 10)
	must(t, err)
	if !stillOneCompany {
		t.Fatal("the division was not initially recorded as part of the parent entity")
	}

	must2(t, func() (Event, error) {
		return r.Unmerge("op", "flag-registry", org("CONGLOMERATE"), org("DIVISION SPINCO"), 50, "divestiture, spin-off filing 2026-S-1")
	})

	afterSplit, err := r.SameEntityAt(org("CONGLOMERATE"), org("DIVISION SPINCO"), 60)
	must(t, err)
	if afterSplit {
		t.Fatal("the spun-off division still resolves as the same entity as its former parent after the split")
	}
	beforeSplit, err := r.SameEntityAt(org("CONGLOMERATE"), org("DIVISION SPINCO"), 10)
	must(t, err)
	if !beforeSplit {
		t.Fatal("the split retroactively rewrote history -- replay before the split no longer shows the pre-split combined entity")
	}
}

// TestDuplicateEntityRecordsSurfaceAsCandidateNotAutoMerge is the
// audit's named "duplicate entities" scenario: two independently
// data-entered registry records that MIGHT denote the same real
// vessel must be surfaced as a low-confidence candidate for review,
// never silently coalesced by a weak, non-authoritative signal alone.
func TestDuplicateEntityRecordsSurfaceAsCandidateNotAutoMerge(t *testing.T) {
	r := newResolverWithAuthorities(t)
	reg1, reg2 := registry("REG-1001"), registry("REG-1002")
	must2(t, func() (Event, error) {
		return r.Assert("dedup-bot", "aggregator", reg1, reg2, 1, "possible duplicate: matching name and near-identical address")
	})

	same, err := r.SameEntityAt(reg1, reg2, 10)
	must(t, err)
	if same {
		t.Fatal("two merely-similar registry records were silently auto-merged instead of surfaced for review")
	}

	cands, err := r.Candidates(reg1, 10)
	must(t, err)
	wantID := entityIDOf([]string{reg2.Key()})
	var found *Candidate
	for i := range cands {
		if cands[i].EntityID == wantID {
			found = &cands[i]
			break
		}
	}
	if found == nil {
		t.Fatal("the possible-duplicate record did not appear as a candidate at all -- the evidence was dropped, not just left unmerged")
	}
	if found.Confidence <= 0 || found.Confidence >= 0.5 {
		t.Fatalf("expected the duplicate candidate at low-but-nonzero confidence for human review, got %.4f", found.Confidence)
	}

	if _, err := r.ResolveWithThreshold(reg1, 10, 0.5); !errors.Is(err, ErrLowConfidence) {
		t.Fatalf("a weak duplicate-detection signal cleared the auto-resolve confidence threshold: %v", err)
	}
}

// TestDeliberateFalseMatchFromWeakSourceLosesToAuthoritativeIdentity is
// the audit's named "deliberate false match" scenario: an adversarial
// or merely-wrong low-authority source asserts that a real, registry-
// verified vessel is the same as a second, unrelated vessel (the
// sanctions-evasion pattern: claiming a flagged vessel's IMO equals a
// clean vessel's). Because Assert never merges and confidence is
// authority-weighted, the false claim must never displace or tie with
// the authoritative truth.
func TestDeliberateFalseMatchFromWeakSourceLosesToAuthoritativeIdentity(t *testing.T) {
	r := newResolverWithAuthorities(t)
	must2(t, func() (Event, error) {
		return r.Merge("op", "flag-registry", imo("9998887"), name("REAL SHIP"), 1, "verified registry filing")
	})
	must2(t, func() (Event, error) {
		return r.Assert("adversary", "aggregator", imo("9998887"), imo("6666666"), 5,
			"unverified claim submitted by a low-authority source that this is the same vessel as a second IMO")
	})

	res, err := r.Resolve(imo("9998887"), 10, 0)
	must(t, err)
	if res.Conflict {
		t.Fatal("a single weak, non-authoritative false-match claim was strong enough to create an unresolved identity conflict against a verified merge")
	}
	trueID, err := r.EntityIDAt(imo("9998887"), 10)
	must(t, err)
	if res.EntityID != trueID {
		t.Fatal("the adversarial false-match claim displaced the authoritative, registry-verified vessel identity")
	}
	falseMatch, err := r.SameEntityAt(imo("9998887"), imo("6666666"), 10)
	must(t, err)
	if falseMatch {
		t.Fatal("an unverified assertion alone was enough to merge two different vessels' identities")
	}
}

func must2(t *testing.T, res func() (Event, error)) {
	t.Helper()
	if _, err := res(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
