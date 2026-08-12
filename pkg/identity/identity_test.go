package identity

import (
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
	if len(KnownKinds()) != 13 {
		t.Fatalf("expected 13 identifier kinds, got %d", len(KnownKinds()))
	}
}

func must2(t *testing.T, res func() (Event, error)) {
	t.Helper()
	if _, err := res(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
