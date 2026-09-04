package independence

import (
	"errors"
	"strings"
	"testing"
)

func full(id string) Source {
	m := map[Dimension]string{}
	for _, d := range Dimensions() {
		m[d] = id + "-" + string(d)
	}
	return Source{ID: id, Attributes: m}
}

// --- FC-003: an absence of work is not a determination ------------------

// TestUnassessedDimensionYieldsUnknownNotIndependent is the regression
// test for FC-003, and the rule the whole package exists for.
func TestUnassessedDimensionYieldsUnknownNotIndependent(t *testing.T) {
	a := full("ais-provider")
	b := full("sar-provider")

	// Remove ONE disqualifying dimension from one source. Everything
	// else differs, so a naive implementation concludes independence.
	delete(b.Attributes, Producer)

	r, err := Assess(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict == Independent {
		t.Fatal("a pair with an unassessed PRODUCER was called INDEPENDENT: " +
			"finding no dependency without looking is not a finding of independence")
	}
	if r.Verdict != Unknown {
		t.Fatalf("verdict = %s, want UNKNOWN", r.Verdict)
	}
	if r.Verdict.SatisfiesIndependenceRequirement() {
		t.Fatal("UNKNOWN satisfies an independence requirement")
	}
	// And it must say WHICH dimension was not assessed, or a caller
	// cannot tell ignorance from dependence.
	if !strings.Contains(r.Explanation, string(Producer)) {
		t.Fatalf("the explanation does not name the unassessed dimension: %q", r.Explanation)
	}
}

// TestUnknownIsNotCountedTowardsCorroboration is the negative test: the
// aggregate count is the other place the promotion could leak.
func TestUnknownIsNotCountedTowardsCorroboration(t *testing.T) {
	assessed := full("ais-provider")
	unassessed := Source{ID: "an-unassessed-feed"}

	n, unknown, err := EffectiveIndependentCount([]Source{assessed, unassessed})
	if err != nil {
		t.Fatal(err)
	}
	if n >= 2 {
		t.Fatalf("an unassessed pair produced a corroboration count of %d", n)
	}
	if len(unknown) == 0 {
		t.Fatal("the unassessed pair was not named, so a caller cannot tell a low count " +
			"caused by dependence from one caused by nobody having looked")
	}
	// The distinction must survive into the human-readable statement.
	s, err := Statement([]Source{assessed, unassessed})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "UNASSESSED") {
		t.Fatalf("the statement hides the unassessed pair: %q", s)
	}
	if !strings.Contains(s, "not because a relationship was found") {
		t.Fatalf("the statement does not distinguish ignorance from dependence: %q", s)
	}
}

// TestFullyAssessedSourcesStillCorroborate is the positive test. A rule
// that refused everything would satisfy the two tests above and be
// useless.
func TestFullyAssessedSourcesStillCorroborate(t *testing.T) {
	n, unknown, err := EffectiveIndependentCount([]Source{
		full("ais-provider"), full("sar-provider"), full("port-authority"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("three fully assessed, wholly distinct sources counted as %d", n)
	}
	if len(unknown) != 0 {
		t.Fatalf("fully assessed sources reported as unassessed: %v", unknown)
	}
	r, err := RequireIndependent(full("a"), full("b"))
	if err != nil {
		t.Fatalf("two fully assessed distinct sources were refused: %v", err)
	}
	if r.Confidence != 1 {
		t.Fatalf("confidence = %v for a fully assessed pair", r.Confidence)
	}
}

// --- Law 6: correlated sources do not count independently ---------------

// TestThreeFeedsFromOneProducerCountAsOne is Law 6 stated as the case
// it was written about.
func TestThreeFeedsFromOneProducerCountAsOne(t *testing.T) {
	mk := func(id string) Source {
		s := full(id)
		s.Attributes[Producer] = "reuters"
		return s
	}
	n, _, err := EffectiveIndependentCount([]Source{
		mk("reuters-direct"), mk("aggregator"), mk("syndicated-copy"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("three feeds reselling one producer counted as %d independent sources", n)
	}
}

func TestASharedSensorDefeatsIndependence(t *testing.T) {
	a := full("feed-a")
	b := full("feed-b")
	b.Attributes[Sensor] = a.Attributes[Sensor]
	r, err := Assess(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != Dependent {
		t.Fatalf("two feeds off one sensor network are %s", r.Verdict)
	}
	if !strings.Contains(r.Explanation, string(Sensor)) {
		t.Fatalf("the explanation does not name the shared dimension: %q", r.Explanation)
	}
}

func TestSharedOwnershipDefeatsIndependence(t *testing.T) {
	a := full("subsidiary-a")
	b := full("subsidiary-b")
	b.Attributes[Ownership] = a.Attributes[Ownership]
	r, _ := Assess(a, b)
	if r.Verdict != Dependent {
		t.Fatalf("two commonly owned sources are %s", r.Verdict)
	}
}

// TestASharedNonDisqualifyingDimensionIsPartialNotFatal. Two genuinely
// different producers using the same acquisition method are still two
// observations; the correlation is real and it is not the same as
// dependence.
func TestASharedNonDisqualifyingDimensionIsPartialNotFatal(t *testing.T) {
	a := full("feed-a")
	b := full("feed-b")
	b.Attributes[Acquisition] = a.Attributes[Acquisition]
	r, err := Assess(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != PartiallyIndependent {
		t.Fatalf("verdict = %s, want PARTIALLY_INDEPENDENT", r.Verdict)
	}
	// And it still does not satisfy a requirement for independence: a
	// partial answer is information, not a permission.
	if r.Verdict.SatisfiesIndependenceRequirement() {
		t.Fatal("PARTIALLY_INDEPENDENT satisfies an independence requirement")
	}
}

func TestASourceIsNotIndependentOfItself(t *testing.T) {
	a := full("feed-a")
	r, err := Assess(a, a)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != Dependent {
		t.Fatalf("a source assessed against itself is %s", r.Verdict)
	}
	if _, _, err := EffectiveIndependentCount([]Source{a, a}); err == nil {
		t.Fatal("the same source listed twice was counted as two")
	}
}

// TestTheCountIsOrderIndependent. A corroboration count that depended
// on how a caller assembled its slice would be unreproducible, and
// replay would disagree with the original run.
func TestTheCountIsOrderIndependent(t *testing.T) {
	shared := full("shared-producer")
	other := full("other")
	other.Attributes[Producer] = shared.Attributes[Producer]
	third := full("third")

	forward, _, err := EffectiveIndependentCount([]Source{shared, other, third})
	if err != nil {
		t.Fatal(err)
	}
	backward, _, err := EffectiveIndependentCount([]Source{third, other, shared})
	if err != nil {
		t.Fatal(err)
	}
	if forward != backward {
		t.Fatalf("the count depends on slice order: %d vs %d", forward, backward)
	}
	if forward != 2 {
		t.Fatalf("count = %d, want 2 (the shared-producer pair collapses to one)", forward)
	}
}

// TestRequireIndependentDistinguishesIgnoranceFromDependence.
func TestRequireIndependentDistinguishesIgnoranceFromDependence(t *testing.T) {
	a := full("a")
	unassessed := Source{ID: "b"}
	if _, err := RequireIndependent(a, unassessed); !errors.Is(err, ErrUnassessed) {
		t.Fatalf("an unassessed pair returned %v, want ErrUnassessed", err)
	}

	dependent := full("c")
	dependent.Attributes[Producer] = a.Attributes[Producer]
	if _, err := RequireIndependent(a, dependent); !errors.Is(err, ErrNotIndependent) {
		t.Fatalf("a dependent pair returned %v, want ErrNotIndependent", err)
	}
}

// TestConfidenceIsCoverageNotProbability. A number that mixed "we
// looked and they differ" with "we did not look" would reproduce the
// exact confusion this package prevents.
func TestConfidenceIsCoverageNotProbability(t *testing.T) {
	a := full("a")
	b := full("b")
	delete(b.Attributes, Temporal)
	delete(b.Attributes, Acquisition)
	r, err := Assess(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := 4.0 / 6.0
	if r.Confidence != want {
		t.Fatalf("confidence = %v, want %v (four of six dimensions assessed)", r.Confidence, want)
	}
	if len(r.Unassessed) != 2 {
		t.Fatalf("Unassessed = %v", r.Unassessed)
	}
}

// TestUnassessedNonDisqualifyingDimensionsYieldCorrelated. All three
// disqualifying dimensions differ, but the processing chain was never
// examined: that is weaker than INDEPENDENT and stronger than UNKNOWN,
// and collapsing either way loses information a reviewer needs.
func TestUnassessedNonDisqualifyingDimensionsYieldCorrelated(t *testing.T) {
	a := full("a")
	b := full("b")
	delete(b.Attributes, Transformation)
	r, err := Assess(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != Correlated {
		t.Fatalf("verdict = %s, want CORRELATED", r.Verdict)
	}
	if r.Verdict.SatisfiesIndependenceRequirement() {
		t.Fatal("CORRELATED satisfies an independence requirement")
	}
	if !r.Verdict.Assessed() {
		t.Fatal("CORRELATED reports as unassessed; it rests on real work")
	}
}

func TestMalformedSourcesAreRefused(t *testing.T) {
	if _, err := Assess(Source{}, full("b")); !errors.Is(err, ErrNoSource) {
		t.Fatalf("a source with no id was assessed: %v", err)
	}
	bad := Source{ID: "x", Attributes: map[Dimension]string{"VIBES": "good"}}
	if _, err := Assess(bad, full("b")); err == nil {
		t.Fatal("an unknown dimension was accepted")
	}
}

// TestTheDisqualifyingSetIsDeliberate pins the design decision rather
// than leaving it to be re-derived from the code. If somebody moves a
// dimension between the sets, this test makes them do it on purpose.
func TestTheDisqualifyingSetIsDeliberate(t *testing.T) {
	dis := map[Dimension]bool{}
	for _, d := range DisqualifyingDimensions() {
		dis[d] = true
	}
	for _, d := range []Dimension{Producer, Sensor, Ownership} {
		if !dis[d] {
			t.Errorf("%s is no longer disqualifying: two sources sharing it would be "+
				"reported as independent observations", d)
		}
	}
	for _, d := range []Dimension{Acquisition, Transformation, Temporal} {
		if dis[d] {
			t.Errorf("%s became disqualifying: two genuinely different producers using the "+
				"same method would now be reported as one observation", d)
		}
	}
	if len(DisqualifyingDimensions()) != 3 {
		t.Fatalf("the disqualifying set has %d members", len(DisqualifyingDimensions()))
	}
}

func TestAnEmptySourceSetCorroboratesNothing(t *testing.T) {
	n, _, err := EffectiveIndependentCount(nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("no sources produced a corroboration count of %d", n)
	}
}
