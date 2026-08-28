package contradiction

import (
	"testing"

	"veriqo/pkg/insurance/evidence"
)

// TestPairwiseSupportMatchesThreePartyWorkedExample reproduces §12's
// worked structure: PARTY A, PARTY B and the INSURER each submit
// evidence, plus an INDEPENDENT surveyor. The engine computes all six
// named pairwise relationships (A<->B, A<->C, B<->C, A<->Independent,
// B<->Independent, C<->Independent) and every one of them must land on
// one of §12's four named outcomes (SUPPORTED / CONTRADICTED /
// PARTIALLY_SUPPORTED / UNRESOLVED).
//
// Scenario: two claim keys are in play.
//
//	K1 = container_seal_status   (INTACT / BROKEN)
//	K2 = damage_extent           (SEVERE / MINOR)
//
//	A (claimant):    K1=INTACT  K2=SEVERE
//	B (respondent):  K1=INTACT  K2=MINOR
//	C (insurer):     K1=INTACT  K2=SEVERE
//	D (independent): K1=BROKEN  K2=SEVERE
func TestPairwiseSupportMatchesThreePartyWorkedExample(t *testing.T) {
	e := NewEngine()

	a := mustEvidence(t, "seal", "party-a", 1, evidence.OriginClaimant, evidence.SourceInterest{Interest: evidence.InterestClaimant})
	b := mustEvidence(t, "seal", "party-b", 1, evidence.OriginRespondent, evidence.SourceInterest{Interest: evidence.InterestCarrier})
	c := mustEvidence(t, "seal", "party-c", 1, evidence.OriginInsurer, evidence.SourceInterest{Interest: evidence.InterestInsurer})
	d := mustEvidence(t, "seal", "party-d", 1, evidence.OriginIndependent, evidence.SourceInterest{Interest: evidence.InterestIndependent})

	k1 := "CASE-12|container_seal_status"
	k2 := "CASE-12|damage_extent"

	must(t, e.Submit(a, k1, "INTACT", 1.0, 1))
	must(t, e.Submit(a, k2, "SEVERE", 1.0, 1))
	must(t, e.Submit(b, k1, "INTACT", 1.0, 1))
	must(t, e.Submit(b, k2, "MINOR", 1.0, 1))
	must(t, e.Submit(c, k1, "INTACT", 1.0, 1))
	must(t, e.Submit(c, k2, "SEVERE", 1.0, 1))
	must(t, e.Submit(d, k1, "BROKEN", 1.0, 1))
	must(t, e.Submit(d, k2, "SEVERE", 1.0, 1))

	check := func(label, idX, idY string, want SupportStatus) {
		t.Helper()
		got, err := e.PairwiseSupport(idX, idY, 1, 1000)
		must(t, err)
		if !IsKnownSupportStatus(got.Status) {
			t.Fatalf("%s: status %q is not one of the four blueprint §12 outcomes", label, got.Status)
		}
		if got.Status != want {
			t.Fatalf("%s: expected %s, got %s (agree=%d conflict=%d)", label, want, got.Status, got.AgreeingClaims, got.ConflictingClaims)
		}
	}

	// A<->B: agree on K1 (INTACT), conflict on K2 (SEVERE vs MINOR).
	check("A<->B", a.EvidenceID(), b.EvidenceID(), SupportPartiallySupported)
	// A<->C (claimant<->insurer): agree on both K1 and K2.
	check("A<->C", a.EvidenceID(), c.EvidenceID(), SupportSupported)
	// B<->C: agree on K1, conflict on K2.
	check("B<->C", b.EvidenceID(), c.EvidenceID(), SupportPartiallySupported)
	// A<->Independent: conflict on K1 (INTACT vs BROKEN), agree on K2.
	check("A<->Independent", a.EvidenceID(), d.EvidenceID(), SupportPartiallySupported)
	// B<->Independent: conflict on both K1 and K2.
	check("B<->Independent", b.EvidenceID(), d.EvidenceID(), SupportContradicted)
	// C<->Independent: conflict on K1, agree on K2.
	check("C<->Independent", c.EvidenceID(), d.EvidenceID(), SupportPartiallySupported)
}

// TestPairwiseSupportUnresolvedWhenNoSharedClaims proves the golden
// rule at the three-party layer: when two pieces of evidence have no
// overlapping claim key at all, there is genuinely not enough evidence
// to say SUPPORTED, CONTRADICTED or PARTIALLY_SUPPORTED -- the correct
// answer is UNRESOLVED, never a guess.
func TestPairwiseSupportUnresolvedWhenNoSharedClaims(t *testing.T) {
	e := NewEngine()
	a := mustEvidence(t, "x", "party-x", 1, evidence.OriginClaimant, evidence.SourceInterest{Interest: evidence.InterestClaimant})
	b := mustEvidence(t, "y", "party-y", 1, evidence.OriginInsurer, evidence.SourceInterest{Interest: evidence.InterestInsurer})
	must(t, e.Submit(a, "CASE-13|claim_one", "V1", 1.0, 1))
	must(t, e.Submit(b, "CASE-13|claim_two", "V2", 1.0, 1))

	got, err := e.PairwiseSupport(a.EvidenceID(), b.EvidenceID(), 1, 1000)
	must(t, err)
	if got.Status != SupportUnresolved {
		t.Fatalf("expected UNRESOLVED with no shared claims, got %s", got.Status)
	}
	if got.SharedClaims != 0 {
		t.Fatalf("expected 0 shared claims, got %d", got.SharedClaims)
	}
}

func TestPairwiseSupportRejectsEmptyEvidenceIDs(t *testing.T) {
	e := NewEngine()
	if _, err := e.PairwiseSupport("", "EV-1", 1, 100); err != ErrEmptyEvidenceID {
		t.Fatalf("expected ErrEmptyEvidenceID, got %v", err)
	}
	if _, err := e.PairwiseSupport("EV-1", "", 1, 100); err != ErrEmptyEvidenceID {
		t.Fatalf("expected ErrEmptyEvidenceID, got %v", err)
	}
}

// TestIndependentSupportCountDefersToDependencyGraph reproduces §10's
// own worked example (Photo A -> Statement A -> Survey A is ONE
// independent source, not three) and confirms this package never
// substitutes its own naive len(ids) count.
func TestIndependentSupportCountDefersToDependencyGraph(t *testing.T) {
	graph := evidence.NewDependencyGraph()
	photo := "EV-photo-a"
	statement := "EV-statement-a"
	survey := "EV-survey-a"
	must(t, graph.AddDependency(statement, photo))
	must(t, graph.AddDependency(survey, statement))

	got, err := IndependentSupportCount(graph, []string{photo, statement, survey})
	must(t, err)
	if got != 1 {
		t.Fatalf("expected 1 independent root source (not a naive count of 3), got %d", got)
	}

	// A genuinely independent set of three unrelated sources is,
	// correctly, three.
	got2, err := IndependentSupportCount(graph, []string{"EV-unrelated-1", "EV-unrelated-2", "EV-unrelated-3"})
	must(t, err)
	if got2 != 3 {
		t.Fatalf("expected 3 independent roots for unrelated evidence, got %d", got2)
	}
}

func TestIndependentSupportCountRejectsNilGraph(t *testing.T) {
	if _, err := IndependentSupportCount(nil, []string{"EV-1"}); err != ErrNilDependencyGraph {
		t.Fatalf("expected ErrNilDependencyGraph, got %v", err)
	}
}

func TestKnownSupportStatusesHasAllFourBlueprintOutcomes(t *testing.T) {
	all := []SupportStatus{SupportSupported, SupportContradicted, SupportPartiallySupported, SupportUnresolved}
	for _, s := range all {
		if !IsKnownSupportStatus(s) {
			t.Fatalf("expected %s to be a known support status", s)
		}
	}
	if IsKnownSupportStatus(SupportStatus("NOT_A_REAL_STATUS")) {
		t.Fatal("expected an unmodelled status to be rejected")
	}
}
