package uncertainty

import (
	"errors"
	"strings"
	"testing"
)

func j(d Dimension, l Level) Judgement {
	return Judgement{Dimension: d, Level: l, Basis: "assessed against the case record"}
}

func full(l Level) Vector {
	var js []Judgement
	for _, d := range Dimensions() {
		js = append(js, j(d, l))
	}
	v, err := New("claim:c1", js...)
	if err != nil {
		panic(err)
	}
	return v
}

// TestThereIsNoOverallScore is asserted structurally rather than
// stated in a comment: if a combining method is ever added, this test
// is where somebody has to decide to delete the rule.
func TestThereIsNoOverallScore(t *testing.T) {
	v := full(High)
	// The only summarising affordance is Weakest, and it returns a
	// DIMENSION -- a pointer to where to look, not a number.
	d, l := v.Weakest()
	if d == "" {
		t.Fatal("Weakest names no dimension")
	}
	if l != High {
		t.Fatalf("Weakest of an all-HIGH vector is %s", l)
	}
	if !strings.Contains(v.Report(), "There is no overall number") {
		t.Fatalf("the report does not state the rule:\n%s", v.Report())
	}
}

// TestAStrongDimensionDoesNotCarryAWeakOne. This is the substitution
// that makes a plausible narrative look established.
func TestAStrongDimensionDoesNotCarryAWeakOne(t *testing.T) {
	v, err := New("claim:c1",
		j(Identity, High), j(Temporal, High), j(Spatial, High),
		j(Source, High), j(Measurement, High), j(Independence, High),
		j(Model, High), j(Completeness, Low),
		j(Causal, Low))
	if err != nil {
		t.Fatal(err)
	}
	d, l := v.Weakest()
	if l != Low {
		t.Fatalf("seven HIGH dimensions moved the weakest to %s", l)
	}
	if d != Causal && d != Completeness {
		t.Fatalf("the weakest dimension is %s", d)
	}
	weak := v.Weak()
	if len(weak) != 2 {
		t.Fatalf("Weak() = %v, want the two LOW dimensions", weak)
	}
	// And the qualifications must name both, in the conclusion's own
	// words.
	q := strings.Join(v.Qualifications(), " ")
	if !strings.Contains(q, "causal") || !strings.Contains(q, "evidence completeness") {
		t.Fatalf("the qualifications omit a weak dimension: %v", v.Qualifications())
	}
}

// TestNotAssessedIsNotNone. One needs somebody to look; the other
// needs better evidence.
func TestNotAssessedIsNotNone(t *testing.T) {
	if NotAssessed == None {
		t.Fatal("NOT_ASSESSED and NONE are the same value")
	}
	if NotAssessed.Assessed() {
		t.Fatal("NOT_ASSESSED reports as assessed")
	}
	if !None.Assessed() {
		t.Fatal("NONE reports as unassessed")
	}
	// NOT_ASSESSED ranks BELOW NONE: not looking is worse than looking
	// and finding nothing.
	if NotAssessed.Rank() >= None.Rank() {
		t.Fatal("NOT_ASSESSED ranks at or above NONE")
	}
	// And the two produce different qualification sentences FOR THE
	// SAME DIMENSION. Comparing whole vectors would not show this:
	// a vector with one assessed dimension still carries eight
	// absence-of-work lines for the rest.
	assessedNone, err := New("c", j(Causal, None))
	if err != nil {
		t.Fatal(err)
	}
	unassessed, err := New("c")
	if err != nil {
		t.Fatal(err)
	}
	causalLine := func(v Vector) string {
		for _, q := range v.Qualifications() {
			if strings.HasPrefix(q, "causal") {
				return q
			}
		}
		return ""
	}
	assessedLine, unassessedLine := causalLine(assessedNone), causalLine(unassessed)
	if assessedLine == "" || unassessedLine == "" {
		t.Fatalf("no causal qualification was produced: %q / %q", assessedLine, unassessedLine)
	}
	if assessedLine == unassessedLine {
		t.Fatal("NONE and NOT_ASSESSED produce the same sentence for the same dimension")
	}
	if !strings.Contains(unassessedLine, "absence of work") {
		t.Fatalf("an unassessed dimension does not say so: %s", unassessedLine)
	}
	if strings.Contains(assessedLine, "absence of work") {
		t.Fatalf("an assessed NONE was described as an absence of work: %s", assessedLine)
	}
}

// TestEveryDimensionIsMaterialised. An omitted key and an unassessed
// one look identical in a map, and only one is a determination.
func TestEveryDimensionIsMaterialised(t *testing.T) {
	v, err := New("claim:c1", j(Identity, High))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Judgements) != len(Dimensions()) {
		t.Fatalf("%d of %d dimensions materialised", len(v.Judgements), len(Dimensions()))
	}
	for _, d := range Dimensions() {
		if _, ok := v.Judgements[d]; !ok {
			t.Errorf("%s is absent", d)
		}
	}
	if len(v.Unassessed()) != len(Dimensions())-1 {
		t.Fatalf("Unassessed() = %v", v.Unassessed())
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("a partially assessed vector is invalid: %v", err)
	}
}

// TestAnAssessedDimensionMustStateItsBasis. Without one it is a band
// somebody chose.
func TestAnAssessedDimensionMustStateItsBasis(t *testing.T) {
	bad := Judgement{Dimension: Identity, Level: High}
	if err := bad.Validate(); !errors.Is(err, ErrNoBasis) {
		t.Fatalf("an assessed dimension with no basis was accepted: %v", err)
	}
}

// TestABasisOnAnUnassessedDimensionIsRefused.
//
// This is the mutation the suite attacks: attaching a reason to an
// unasked question so the vector reads as though somebody looked.
func TestABasisOnAnUnassessedDimensionIsRefused(t *testing.T) {
	bad := Judgement{Dimension: Causal, Level: NotAssessed,
		Basis: "the mechanism is obvious"}
	if err := bad.Validate(); err == nil {
		t.Fatal("MUTANT SURVIVED: an unassessed dimension carried a reason, so the vector " +
			"reads as though somebody assessed it")
	}
}

// TestDominationIsAPartialOrder. A comparison that always returned an
// answer would be the overall score by another route.
func TestDominationIsAPartialOrder(t *testing.T) {
	a, _ := New("c1",
		j(Identity, High), j(Causal, Low), j(Temporal, High), j(Spatial, High),
		j(Source, High), j(Measurement, High), j(Completeness, High),
		j(Independence, High), j(Model, High))
	b, _ := New("c2",
		j(Identity, Low), j(Causal, High), j(Temporal, High), j(Spatial, High),
		j(Source, High), j(Measurement, High), j(Completeness, High),
		j(Independence, High), j(Model, High))

	if a.Dominates(b) || b.Dominates(a) {
		t.Fatal("two vectors trading identity against causality were ordered")
	}
	if Comparable(a, b) {
		t.Fatal("incomparable vectors reported as comparable")
	}
	if !full(High).Dominates(full(Low)) {
		t.Fatal("an all-HIGH vector does not dominate an all-LOW one")
	}
}

// TestQualificationsAreDeterministic, so a finding's limitations are
// stable across runs.
func TestQualificationsAreDeterministic(t *testing.T) {
	v, _ := New("c1", j(Causal, Low), j(Completeness, None), j(Identity, High))
	first := strings.Join(v.Qualifications(), "|")
	for i := 0; i < 50; i++ {
		if strings.Join(v.Qualifications(), "|") != first {
			t.Fatal("the qualifications vary between runs")
		}
	}
}

// TestQualificationsCoverEveryWeakAndUnassessedDimension. A conclusion
// cannot be stated without these, so nothing may be omitted.
func TestQualificationsCoverEveryWeakAndUnassessedDimension(t *testing.T) {
	v, _ := New("c1", j(Identity, High), j(Causal, Low), j(Measurement, None))
	qs := v.Qualifications()
	// Two weak + six unassessed = eight.
	if len(qs) != 8 {
		t.Fatalf("%d qualifications for two weak and six unassessed dimensions:\n%s",
			len(qs), strings.Join(qs, "\n"))
	}
}

// TestEveryDimensionHasAQuestion, so a report is readable by somebody
// who has not seen the source.
func TestEveryDimensionHasAQuestion(t *testing.T) {
	for _, d := range Dimensions() {
		if d.Question() == "" {
			t.Errorf("%s states no question", d)
		}
	}
	if Dimension("VIBES").Question() != "" {
		t.Fatal("an unknown dimension returned a question")
	}
}

// TestTheNineDimensionsAreTheSpecifiedOnes.
func TestTheNineDimensionsAreTheSpecifiedOnes(t *testing.T) {
	if len(Dimensions()) != 9 {
		t.Fatalf("%d dimensions", len(Dimensions()))
	}
	want := map[Dimension]bool{
		Identity: true, Temporal: true, Spatial: true, Source: true,
		Measurement: true, Causal: true, Completeness: true,
		Independence: true, Model: true,
	}
	for _, d := range Dimensions() {
		if !want[d] {
			t.Errorf("unexpected dimension %s", d)
		}
		delete(want, d)
	}
	for d := range want {
		t.Errorf("missing dimension %s", d)
	}
}

func TestUnknownDimensionsAndLevelsAreRefused(t *testing.T) {
	if err := (Judgement{Dimension: "LUCK", Level: High, Basis: "b"}).Validate(); !errors.Is(err, ErrUnknownDimension) {
		t.Fatal("an unknown dimension was accepted")
	}
	if err := (Judgement{Dimension: Identity, Level: "PRETTY_SURE", Basis: "b"}).Validate(); !errors.Is(err, ErrUnknownLevel) {
		t.Fatal("an unknown level was accepted")
	}
}

func TestAVectorMustNameItsSubject(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("a vector with no subject was built")
	}
}
