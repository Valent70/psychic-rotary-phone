package source

import (
	"errors"
	"strings"
	"testing"
)

func reading(d Dimension, g Grade) Reading {
	r := Reading{Dimension: d, Grade: g, Basis: "assessed against the case record"}
	if g == Partial {
		r.Remainder = "the producer's identity is known and the chain to us is not"
	}
	if g == NotAssessed {
		r.Basis = ""
	}
	return r
}

func full(t *testing.T, override map[Dimension]Grade) *Vector {
	t.Helper()
	var rs []Reading
	for _, d := range Dimensions() {
		g := Confirmed
		if o, ok := override[d]; ok {
			g = o
		}
		rs = append(rs, reading(d, g))
	}
	v, err := NewVector("src:test", rs...)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestThereIsNoCombinedScore. The absence is the design: these
// questions are not commensurable, and averaging them lets a lawyer's
// objection be offset by a fresh timestamp.
func TestThereIsNoCombinedScore(t *testing.T) {
	v := full(t, nil)
	out := v.Render()
	if strings.Contains(out, "%") {
		t.Fatalf("a percentage reached the profile:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "score") &&
		!strings.Contains(out, "no combined score") {
		t.Fatalf("the profile offers a score:\n%s", out)
	}
	if !strings.Contains(out, "not commensurable") {
		t.Fatalf("the profile does not say why there is no score:\n%s", out)
	}
	// Weakest returns a DIMENSION, not a number.
	d, _ := v.Weakest()
	if !d.Valid() {
		t.Fatalf("Weakest returned %q", d)
	}
}

// TestOnlyLegalityCanMakeASourceUnusable.
//
// Everything else weakens it, and weakness is for a human to weigh.
// Marking more dimensions disqualifying would make the vector
// unusable; marking fewer would let a lawyer's objection be outvoted.
func TestOnlyLegalityCanMakeASourceUnusable(t *testing.T) {
	for _, d := range Dimensions() {
		v := full(t, map[Dimension]Grade{d: Unknown})
		err := v.Usable()
		if d == Legality {
			if err == nil {
				t.Fatal("a source with unknown legality was usable")
			}
			if !strings.Contains(err.Error(), "No strength on another dimension compensates") {
				t.Fatalf("the refusal does not say why: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s at UNKNOWN made the source unusable: %v", d, err)
		}
		if d.Disqualifying() {
			t.Fatalf("%s is marked disqualifying", d)
		}
	}
}

// TestAWeaknessCannotBeOffsetByAStrength.
//
// The worked case from the audit: attribution unknown, authenticity
// confirmed. That is not "somewhat trustworthy" -- it is a document we
// can prove is genuine and cannot say who wrote.
func TestAWeaknessCannotBeOffsetByAStrength(t *testing.T) {
	v := full(t, map[Dimension]Grade{Attribution: Unknown})
	d, g := v.Weakest()
	if d != Attribution || g != Unknown {
		t.Fatalf("weakest = %s (%s)", d, g)
	}
	// Every other dimension being CONFIRMED changes nothing about the
	// weakest one, which is the whole point.
	if v.Readings[Authenticity].Grade != Confirmed {
		t.Fatal("the fixture no longer contrasts a strength with a weakness")
	}
	if !strings.Contains(v.Render(), "weakest dimension: ATTRIBUTION") {
		t.Fatalf("the profile does not lead with the weakness:\n%s", v.Render())
	}
}

// TestEveryDimensionMustBeGraded.
//
// An omitted dimension is not "not applicable": it is one nobody
// looked at, and its absence is exactly how a weakness disappears.
func TestEveryDimensionMustBeGraded(t *testing.T) {
	var rs []Reading
	for _, d := range Dimensions()[:5] {
		rs = append(rs, reading(d, Confirmed))
	}
	_, err := NewVector("src:test", rs...)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("a partial vector was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "how a weakness disappears") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
	if len(Dimensions()) != 9 {
		t.Fatalf("%d dimensions", len(Dimensions()))
	}
}

// TestAdverseIsWorseThanUnknownForRelianceAndBetterForInformation.
func TestAdverseIsWorseThanUnknownForRelianceAndBetterForInformation(t *testing.T) {
	v := full(t, map[Dimension]Grade{
		Attribution: Unknown, ReliabilityHistory: Adverse,
	})
	d, g := v.Weakest()
	if d != ReliabilityHistory || g != Adverse {
		t.Fatalf("weakest = %s (%s); ADVERSE should outrank UNKNOWN as the weakest for "+
			"the purpose of relying on the source", d, g)
	}
	if !Adverse.Answered() {
		t.Fatal("ADVERSE was classified as unanswered; somebody looked and found something")
	}
	if Adverse.Supports() {
		t.Fatal("an adverse finding supports a claim")
	}
	if !strings.Contains(v.Render(), "ADVERSE findings on: RELIABILITY_HISTORY") {
		t.Fatalf("adverse findings are easy to lose in the table:\n%s", v.Render())
	}
}

// TestNotAssessedAndUnknownAreDifferent. One means nobody looked; the
// other means somebody tried.
func TestNotAssessedAndUnknownAreDifferent(t *testing.T) {
	if NotAssessed == Unknown {
		t.Fatal("NOT_ASSESSED and UNKNOWN are the same value")
	}
	if NotAssessed.Answered() {
		t.Fatal("NOT_ASSESSED reports that somebody looked")
	}
	if !Unknown.Answered() {
		t.Fatal("UNKNOWN reports that nobody looked")
	}
	v := full(t, map[Dimension]Grade{Corroboration: NotAssessed})
	un := v.Unanswered()
	if len(un) != 1 || un[0] != Corroboration {
		t.Fatalf("unanswered = %v", un)
	}
	if !strings.Contains(v.Render(), "nobody has looked at: CORROBORATION") {
		t.Fatalf("the profile does not surface it:\n%s", v.Render())
	}
	// And the zero grade is NOT_ASSESSED, so an unpopulated reading
	// claims nothing.
	var r Reading
	if r.Grade != NotAssessed {
		t.Fatalf("the zero grade is %s", r.Grade)
	}
}

// TestPartialMustSayWhatIsMissing. Without that it is a shrug.
func TestPartialMustSayWhatIsMissing(t *testing.T) {
	r := Reading{Dimension: Provenance, Grade: Partial, Basis: "some of the chain is known"}
	if err := r.Validate(); err == nil {
		t.Fatal("a PARTIAL reading with no remainder validated")
	}
	r.Remainder = "the hop between the aggregator and us is unrecorded"
	if err := r.Validate(); err != nil {
		t.Fatalf("a PARTIAL reading with a remainder was refused: %v", err)
	}
	// And a graded reading with no basis is refused.
	if err := (Reading{Dimension: Legality, Grade: Confirmed}).Validate(); err == nil {
		t.Fatal("a grade with no basis validated")
	}
}

// TestEveryDimensionStatesItsQuestion, so a reader can tell whether it
// is the one they care about.
func TestEveryDimensionStatesItsQuestion(t *testing.T) {
	for _, d := range Dimensions() {
		if strings.TrimSpace(d.Question()) == "" {
			t.Fatalf("%s asks nothing", d)
		}
	}
	for _, g := range Grades() {
		if !g.Valid() || strings.HasPrefix(g.String(), "Grade(") {
			t.Fatalf("%d has no name", int(g))
		}
	}
	if len(Grades()) != 5 {
		t.Fatalf("%d grades", len(Grades()))
	}
}
