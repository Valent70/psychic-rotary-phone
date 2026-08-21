package contradiction

import (
	"testing"

	"veriqo/pkg/insurance/evidence"
)

// TestSubmitAndDetectAreOriginBlind is the concrete, checkable form of
// blueprint §28's Neutrality Engine rule: "Tidak boleh ada:
// INSURER_EVIDENCE = trusted atau CLAIMANT_EVIDENCE = untrusted."
//
// Both Submit and Detect are, by construction, a SINGLE code path with
// no branch anywhere on evidence.Origin (this can be confirmed by
// inspection of contradiction.go: Origin is read exactly once, in
// Detect, purely to attach a descriptive %s to the free-text Conflict
// message -- never in any condition). This test proves it
// mechanically: run the SAME contradiction scenario twice, once with
// claimant-vs-insurer Origins and once with the Origins swapped
// (mirror image), and assert the two runs produce structurally
// identical severity, resolution and human_review_required -- the only
// fields that could possibly reveal a hidden Origin-based branch.
func TestSubmitAndDetectAreOriginBlind(t *testing.T) {
	run := func(t *testing.T, originFirst, originSecond evidence.Origin) ContradictionRecord {
		e := NewEngine()
		first := mustEvidence(t, "damage", "src-first", 1, originFirst, evidence.SourceInterest{})
		second := mustEvidence(t, "damage", "src-second", 1, originSecond, evidence.SourceInterest{})
		claimKey := "CASE-NEUTRAL|damage_extent"
		must(t, e.Submit(first, claimKey, "SEVERE", 1.0, 1))
		must(t, e.Submit(second, claimKey, "MINOR", 1.0, 1))

		records, err := e.Detect(claimKey, PairClaimantInsurer, "", 1, 1000)
		must(t, err)
		if len(records) != 1 {
			t.Fatalf("expected exactly 1 contradiction record, got %d", len(records))
		}
		return records[0]
	}

	direct := run(t, evidence.OriginClaimant, evidence.OriginInsurer)
	mirrored := run(t, evidence.OriginInsurer, evidence.OriginClaimant)

	if direct.Severity != mirrored.Severity {
		t.Fatalf("severity differs by which Origin submitted first: %s vs %s -- Origin is leaking into the decision", direct.Severity, mirrored.Severity)
	}
	if direct.Resolution != mirrored.Resolution {
		t.Fatalf("resolution differs by which Origin submitted first: %s vs %s -- Origin is leaking into the decision", direct.Resolution, mirrored.Resolution)
	}
	if direct.HumanReviewRequired != mirrored.HumanReviewRequired {
		t.Fatal("human_review_required differs by which Origin submitted first -- Origin is leaking into the decision")
	}
}

// TestPairwiseSupportIsOriginBlind is the same proof applied to the
// three-party support computation (§12): swapping which party (A/B)
// has which Origin must not change the computed SupportStatus.
func TestPairwiseSupportIsOriginBlind(t *testing.T) {
	run := func(t *testing.T, originA, originB evidence.Origin) SupportStatus {
		e := NewEngine()
		a := mustEvidence(t, "extent", "src-a", 1, originA, evidence.SourceInterest{})
		b := mustEvidence(t, "extent", "src-b", 1, originB, evidence.SourceInterest{})
		claimKey := "CASE-NEUTRAL-2|extent"
		must(t, e.Submit(a, claimKey, "SEVERE", 1.0, 1))
		must(t, e.Submit(b, claimKey, "MINOR", 1.0, 1))
		got, err := e.PairwiseSupport(a.EvidenceID(), b.EvidenceID(), 1, 1000)
		must(t, err)
		return got.Status
	}

	direct := run(t, evidence.OriginClaimant, evidence.OriginInsurer)
	mirrored := run(t, evidence.OriginInsurer, evidence.OriginClaimant)
	if direct != mirrored {
		t.Fatalf("support status differs by which Origin submitted first: %s vs %s -- Origin is leaking into the decision", direct, mirrored)
	}
}

// TestDeclaredInterestNeverPicksAWinner is the concrete, checkable form
// of blueprint §11's "Interest ≠ False". Two pieces of evidence
// contradict each other: one from OriginIndependent with
// InterestIndependent declared, the other from OriginInsurer with
// InterestInsurer declared (a textbook potential conflict of interest).
// Submitted with EQUAL reliability -- reliability is the ONLY lever
// this package's wrapped ArbitrationEngine actually uses to weigh
// evidence, and this package never derives reliability from Interest
// (see Submit's own doc comment) -- the real engine has no basis to
// prefer either side. The result MUST remain UNRESOLVED /
// human_review_required=true. If a future change made this test start
// asserting the independent evidence "won" purely because of its
// declared interest, that would be exactly the anti-pattern the
// blueprint forbids.
func TestDeclaredInterestNeverPicksAWinner(t *testing.T) {
	e := NewEngine()

	independent := mustEvidence(t, "cause", "src-independent", 1, evidence.OriginIndependent, evidence.SourceInterest{
		Interest: evidence.InterestIndependent, PotentialConflict: false,
	})
	insurer := mustEvidence(t, "cause", "src-insurer", 1, evidence.OriginInsurer, evidence.SourceInterest{
		Interest: evidence.InterestInsurer, Relationship: "underwriter of the policy", PotentialConflict: true,
	})

	claimKey := "CASE-COI|cause_of_loss"
	must(t, e.Submit(independent, claimKey, "PRE_EXISTING_DAMAGE", 1.0, 1))
	must(t, e.Submit(insurer, claimKey, "LOADING_DAMAGE", 1.0, 1))

	records, err := e.Detect(claimKey, PairClaimantInsurer, "", 1, 1000)
	must(t, err)
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 contradiction record, got %d", len(records))
	}
	rec := records[0]

	if rec.Resolution != ResolutionUnresolved {
		t.Fatalf("a declared conflict of interest must NEVER by itself resolve a contradiction -- expected UNRESOLVED, got %s (resolution=%s implies a winner was picked)", rec.Resolution, rec.Resolution)
	}
	if !rec.HumanReviewRequired {
		t.Fatal("expected human_review_required=true: equal-reliability evidence in genuine disagreement must go to a human, never be auto-resolved by declared interest")
	}
	if rec.Severity != SeverityHigh {
		t.Fatalf("expected HIGH severity for a genuinely tied, contested claim, got %s", rec.Severity)
	}

	// The interest context IS surfaced (§11: "VICE hanya menampilkan
	// source_interest ... dan membiarkan reviewer menilai") -- but only
	// as context, never as the basis for the Resolution/Severity above.
	if rec.EvidenceASourceInterest == nil || rec.EvidenceBSourceInterest == nil {
		t.Fatal("expected both sides' declared SourceInterest to be surfaced as context for the human reviewer")
	}
	interests := map[evidence.Interest]bool{
		rec.EvidenceASourceInterest.Interest: true,
		rec.EvidenceBSourceInterest.Interest: true,
	}
	if !interests[evidence.InterestIndependent] || !interests[evidence.InterestInsurer] {
		t.Fatalf("expected both INDEPENDENT and INSURER_INTEREST to be visible in the surfaced context, got %v", interests)
	}
}

// TestDeclaredInterestNeverPicksAWinnerEvenWhenOneSideIsUncontested
// covers the companion case: when only the insurer submits evidence at
// all (no contradiction exists yet), a declared interest must not
// retroactively make its evidence untrusted either. Detect on an
// uncontested claim must not surface any "penalty" -- there is simply
// no ContradictionRecord to produce.
func TestDeclaredInterestNeverPicksAWinnerEvenWhenOneSideIsUncontested(t *testing.T) {
	e := NewEngine()
	insurer := mustEvidence(t, "cause", "src-insurer-only", 1, evidence.OriginInsurer, evidence.SourceInterest{
		Interest: evidence.InterestInsurer, PotentialConflict: true,
	})
	claimKey := "CASE-COI-2|cause_of_loss"
	must(t, e.Submit(insurer, claimKey, "WEAR_AND_TEAR", 1.0, 1))

	records, err := e.Detect(claimKey, PairClaimantInsurer, "", 1, 1000)
	must(t, err)
	if records != nil {
		t.Fatalf("a single, uncontested source (even a declared-interest one) must not be flagged as a contradiction: got %v", records)
	}
}
