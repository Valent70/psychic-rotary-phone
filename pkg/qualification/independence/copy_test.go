package independence

import (
	"strings"
	"testing"
)

const wire = "Vessel reported alongside an unidentified tanker east of the Singapore " +
	"Strait during a six-hour AIS reporting gap on 14 June, with a draught increase " +
	"consistent with a cargo transfer."

// TestFiftyCopiesOfOneStoryAreOneObservation.
//
// The false-corroboration failure, at the scale it actually occurs. A
// consumer counting producers sees fifty; the evidence is one.
func TestFiftyCopiesOfOneStoryAreOneObservation(t *testing.T) {
	var accounts []Account
	for i := 0; i < 50; i++ {
		accounts = append(accounts, Account{
			ID:       string(rune('a'+i%26)) + strings.Repeat("x", i/26+1),
			Producer: ProducerID("producer:" + string(rune('a'+i))),
			Text:     wire,
		})
	}
	an, err := Detect(DefaultPolicy(), accounts...)
	if err != nil {
		t.Fatal(err)
	}
	n, stmt := an.EffectiveCount()
	if n != 1 {
		t.Fatalf("50 verbatim copies reduced to %d observations, not 1", n)
	}
	if !strings.Contains(stmt, "NOT a finding that") {
		t.Errorf("the count is not accompanied by its limit: %q", stmt)
	}
}

// TestASharedTranspositionIsEvidenceOfCopying.
//
// Two people independently reporting the right IMO is unremarkable.
// Two people independently transposing the same two digits is not.
func TestASharedTranspositionIsEvidenceOfCopying(t *testing.T) {
	an, err := Detect(DefaultPolicy(),
		Account{ID: "a", Producer: "p1", Values: map[string]string{"imo": "9401267"}},
		Account{ID: "b", Producer: "p2", Values: map[string]string{"imo": "9401267"}},
		Account{ID: "c", Producer: "p3", Values: map[string]string{"imo": "9401267"}},
		Account{ID: "d", Producer: "p4", Values: map[string]string{"imo": "9401627"}},
		Account{ID: "e", Producer: "p5", Values: map[string]string{"imo": "9401627"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(an.Dependencies) != 1 {
		t.Fatalf("%d dependencies found, want 1: %v", len(an.Dependencies), an.Dependencies)
	}
	d := an.Dependencies[0]
	if d.A != "d" || d.B != "e" {
		t.Fatalf("the wrong pair was linked: %s ~ %s", d.A, d.B)
	}
	if n, _ := an.EffectiveCount(); n != 4 {
		t.Fatalf("5 accounts reduced to %d, want 4", n)
	}
}

// TestAgreeingWithTheMajorityIsNotEvidenceOfCopying.
//
// The inverse, and the one that would wreck the measure if it were
// wrong: independent sources agree on the truth. Treating agreement as
// dependence would collapse every honest corroboration.
func TestAgreeingWithTheMajorityIsNotEvidenceOfCopying(t *testing.T) {
	an, err := Detect(DefaultPolicy(),
		Account{ID: "a", Producer: "p1", Values: map[string]string{"imo": "9401267", "flag": "PA"}},
		Account{ID: "b", Producer: "p2", Values: map[string]string{"imo": "9401267", "flag": "PA"}},
		Account{ID: "c", Producer: "p3", Values: map[string]string{"imo": "9401267", "flag": "PA"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(an.Dependencies) != 0 {
		t.Fatalf("three sources agreeing on the truth were called copies: %v", an.Dependencies)
	}
}

// TestAReprintWithAParagraphAddedIsCaught.
//
// The commonest real syndication shape, and the case that motivated
// using containment rather than Jaccard: a verbatim reprint with one
// sentence added scores 0.84 on Jaccard, under any threshold that does
// not also catch unrelated accounts.
func TestAReprintWithAParagraphAddedIsCaught(t *testing.T) {
	an, err := Detect(DefaultPolicy(),
		Account{ID: "origin", Producer: "p1", Text: wire},
		Account{ID: "reprint", Producer: "p2",
			Text: wire + " Industry sources declined to comment on the report."},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(an.Dependencies) != 1 {
		t.Fatalf("a verbatim reprint plus a sentence was not caught (Jaccard would " +
			"have scored it 0.84 and missed it)")
	}
	if !strings.Contains(an.Dependencies[0].Basis[0], "Jaccard") {
		t.Errorf("the basis does not show the measure that would have missed it: %q",
			an.Dependencies[0].Basis[0])
	}
}

// TestAShortGenericSentenceIsNotEvidenceOfCopying.
//
// Containment is worthless on short text: there are only so many ways
// to write one sentence about a tanker. Without the length guard this
// measure would report copying between any two brief accounts of the
// same event, which is a finding about the English language.
func TestAShortGenericSentenceIsNotEvidenceOfCopying(t *testing.T) {
	an, err := Detect(DefaultPolicy(),
		Account{ID: "long", Producer: "p1", Text: wire},
		Account{ID: "short", Producer: "p2",
			Text: "A tanker was reported alongside a vessel east of the strait."},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(an.Dependencies) != 0 {
		t.Fatalf("a short generic sentence was called a copy: %v", an.Dependencies)
	}
}

// TestThereIsNoFunctionThatReportsIndependence.
//
// Demotion only. Finding no shared error establishes that no shared
// error was found; it does not establish independence, and a method
// named Independent() would be read as though it did.
func TestThereIsNoFunctionThatReportsIndependence(t *testing.T) {
	an, err := Detect(DefaultPolicy(),
		Account{ID: "a", Producer: "p1", Text: wire},
		Account{ID: "b", Producer: "p2", Text: "An entirely different account of another event."},
	)
	if err != nil {
		t.Fatal(err)
	}
	var x any = an
	if _, ok := x.(interface{ Independent(string, string) bool }); ok {
		t.Fatal("Analysis has an Independent method; absence of evidence would be " +
			"read as evidence of absence")
	}
	if _, ok := x.(interface{ IndependenceScore() float64 }); ok {
		t.Fatal("Analysis carries an independence score")
	}
	_, stmt := an.EffectiveCount()
	if !strings.Contains(stmt, "undeclared common upstream would look exactly like this") {
		t.Errorf("the statement does not admit what it cannot see: %q", stmt)
	}
}

// TestAnUnexaminedAccountIsReportedAsUnexamined.
//
// An account with no values and no text is neither dependent nor
// independent. Counting it as a surviving cluster inflates the
// corroboration, so it is named.
func TestAnUnexaminedAccountIsReportedAsUnexamined(t *testing.T) {
	an, err := Detect(DefaultPolicy(),
		Account{ID: "a", Producer: "p1", Text: wire},
		Account{ID: "empty", Producer: "p2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(an.UncomparedAccounts) != 1 || an.UncomparedAccounts[0] != "empty" {
		t.Fatalf("the unexamined account was not flagged: %v", an.UncomparedAccounts)
	}
	if _, stmt := an.EffectiveCount(); !strings.Contains(stmt, "not examined at all") {
		t.Errorf("the statement does not mention the unexamined account: %q", stmt)
	}
}

// TestTooFewReportersMeansNoMajorityAnalysis.
//
// With two accounts, "deviation from the majority" is deviation from
// the other one, and every disagreement becomes a shared error for
// somebody.
func TestTooFewReportersMeansNoMajorityAnalysis(t *testing.T) {
	an, err := Detect(DefaultPolicy(),
		Account{ID: "a", Producer: "p1", Values: map[string]string{"imo": "1"}},
		Account{ID: "b", Producer: "p2", Values: map[string]string{"imo": "1"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(an.Fields) != 0 {
		t.Fatalf("majority analysis ran on %d reporters", 2)
	}
	if _, stmt := an.EffectiveCount(); !strings.Contains(stmt, "did not run") {
		t.Errorf("the statement does not say the analysis did not run: %q", stmt)
	}
}

// TestDetectionIsDeterministicRegardlessOfInputOrder.
func TestDetectionIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	a := Account{ID: "a", Producer: "p1", Values: map[string]string{"imo": "9401267"}}
	b := Account{ID: "b", Producer: "p2", Values: map[string]string{"imo": "9401627"}}
	c := Account{ID: "c", Producer: "p3", Values: map[string]string{"imo": "9401627"}}
	d := Account{ID: "d", Producer: "p4", Values: map[string]string{"imo": "9401267"}}
	one, err := Detect(DefaultPolicy(), a, b, c, d)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Detect(DefaultPolicy(), d, c, b, a)
	if err != nil {
		t.Fatal(err)
	}
	n1, _ := one.EffectiveCount()
	n2, _ := two.EffectiveCount()
	if n1 != n2 {
		t.Fatalf("reordering the input changed the count: %d vs %d", n1, n2)
	}
}

// TestOneAccountIsNotAnAnalysis.
func TestOneAccountIsNotAnAnalysis(t *testing.T) {
	if _, err := Detect(DefaultPolicy(), Account{ID: "a", Producer: "p1", Text: wire}); err == nil {
		t.Fatal("copy detection ran over one account")
	}
	if _, err := Detect(DefaultPolicy(),
		Account{ID: "a", Producer: "p1"}, Account{ID: "a", Producer: "p2"}); err == nil {
		t.Fatal("two accounts with the same id were accepted")
	}
}
