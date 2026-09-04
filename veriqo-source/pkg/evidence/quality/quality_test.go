package quality

import (
	"errors"
	"strings"
	"testing"
)

func strong(a Attribute) Judgement {
	return Judgement{Attribute: a, Grade: Strong, Basis: "assessed and recorded"}
}

func allStrong() []Judgement {
	out := make([]Judgement, 0, len(Attributes()))
	for _, a := range Attributes() {
		out = append(out, strong(a))
	}
	return out
}

// TestAllNineAttributesAreMaterialised. Omission and non-assessment
// look identical in a map, and only one of them is a determination.
func TestAllNineAttributesAreMaterialised(t *testing.T) {
	a, err := New("EV-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(a.Judgements) != 9 {
		t.Fatalf("the assessment holds %d attributes, want 9", len(a.Judgements))
	}
	if len(a.NotAssessedAttributes()) != 9 {
		t.Fatal("an empty assessment does not report all nine as unassessed")
	}
}

// TestUnassessedIsNotInsufficient is the distinction the whole package
// turns on. "We looked and it is poor" and "we never looked" lead to
// different next actions.
func TestUnassessedIsNotInsufficient(t *testing.T) {
	partial, err := New("EV-1", strong(Authenticity), strong(Integrity))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d, why, err := partial.Decide()
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d != Unassessable {
		t.Fatalf("a partly-assessed vector decided %s, want UNASSESSABLE", d)
	}
	if !strings.Contains(why, "nobody looked") {
		t.Fatalf("the reason does not distinguish absence of work from poor evidence: %s", why)
	}

	// And the opposite: fully assessed, one attribute absent.
	js := allStrong()
	js[4] = Judgement{Attribute: Independence, Grade: Absent,
		Basis: "the survey was commissioned by the insurer"}
	full, err := New("EV-1", js...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d, why, err = full.Decide()
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d != Insufficient {
		t.Fatalf("a vector with an absent attribute decided %s, want INSUFFICIENT", d)
	}
	if !strings.Contains(why, "INDEPENDENCE") {
		t.Fatalf("the reason does not name the deficient attribute: %s", why)
	}
}

// TestStrongIntegrityDoesNotOffsetAbsentIndependence. There is no
// score, deliberately: a well-preserved copy of one party's assertion
// is exactly that.
func TestStrongIntegrityDoesNotOffsetAbsentIndependence(t *testing.T) {
	js := allStrong()
	for i := range js {
		if js[i].Attribute == Independence {
			js[i] = Judgement{Attribute: Independence, Grade: Absent,
				Basis: "acquired through an interested party"}
		}
	}
	a, err := New("EV-1", js...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d, _, err := a.Decide()
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d == Supports || d == SupportsWithLimits {
		t.Fatal("eight strong attributes outvoted an absent INDEPENDENCE: the model is scoring")
	}
}

// TestAdequateMustStateItsLimits. ADEQUATE without limits is read as
// STRONG by every reader who is in a hurry.
func TestAdequateMustStateItsLimits(t *testing.T) {
	j := Judgement{Attribute: Scope, Grade: Adequate, Basis: "covers the sampled parcel"}
	if err := j.Validate(); err == nil || !strings.Contains(err.Error(), "states no limits") {
		t.Fatalf("want a limits refusal, got %v", err)
	}
	j.Limits = "does not cover the unsampled parcels in the same hold"
	if err := j.Validate(); err != nil {
		t.Fatalf("an adequate judgement with limits must validate: %v", err)
	}
}

// TestAnAssessedAttributeMustStateItsBasis, and an unassessed one may
// not: a reason attached to an unasked question is how "we never
// checked" comes to read as "we checked".
func TestAnAssessedAttributeMustStateItsBasis(t *testing.T) {
	j := Judgement{Attribute: Integrity, Grade: Strong}
	if err := j.Validate(); !errors.Is(err, ErrNoBasis) {
		t.Fatalf("want ErrNoBasis, got %v", err)
	}
	j = Judgement{Attribute: Integrity, Grade: NotAssessed, Basis: "we are confident about this one"}
	if err := j.Validate(); !errors.Is(err, ErrBasisWithoutWork) {
		t.Fatalf("want ErrBasisWithoutWork, got %v", err)
	}
}

// TestLimitsTravelWithTheConclusion.
func TestLimitsTravelWithTheConclusion(t *testing.T) {
	js := allStrong()
	for i := range js {
		if js[i].Attribute == Scope {
			js[i] = Judgement{Attribute: Scope, Grade: Adequate,
				Basis: "covers the sampled parcel", Limits: "not the unsampled parcels"}
		}
	}
	a, err := New("EV-1", js...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d, why, err := a.Decide()
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d != SupportsWithLimits {
		t.Fatalf("decided %s, want SUPPORTS_WITH_LIMITS", d)
	}
	if !strings.Contains(why, "limits travel with any conclusion") {
		t.Fatalf("the decision does not carry the limits forward: %s", why)
	}
}

// TestEveryStrongVectorSupports, so the model is not unsatisfiable.
func TestEveryStrongVectorSupports(t *testing.T) {
	a, err := New("EV-1", allStrong()...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d, _, err := a.Decide()
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d != Supports {
		t.Fatalf("an all-strong vector decided %s: the model can never be satisfied", d)
	}
}

// TestEveryAttributeAsksADistinctQuestion. Two attributes with the same
// question would be one attribute counted twice.
func TestEveryAttributeAsksADistinctQuestion(t *testing.T) {
	seen := map[string]Attribute{}
	for _, a := range Attributes() {
		q := a.Question()
		if strings.TrimSpace(q) == "" {
			t.Errorf("%s asks no question", a)
			continue
		}
		if prior, dup := seen[q]; dup {
			t.Errorf("%s and %s ask the same question", prior, a)
		}
		seen[q] = a
	}
	if len(Attributes()) != 9 {
		t.Fatalf("the model has %d attributes, want the nine the review named", len(Attributes()))
	}
}

// TestNotAssessedIsNotSufficient.
func TestNotAssessedIsNotSufficient(t *testing.T) {
	if NotAssessed.Sufficient() {
		t.Fatal("an unasked question has an answer")
	}
	for _, g := range []Grade{Weak, Absent} {
		if g.Sufficient() {
			t.Errorf("%s is sufficient", g)
		}
	}
	for _, g := range []Grade{Strong, Adequate} {
		if !g.Sufficient() {
			t.Errorf("%s is not sufficient", g)
		}
	}
}

// TestTheReportSaysThereIsNoScore. A reader who takes away a number has
// taken away the opposite of the model.
func TestTheReportSaysThereIsNoScore(t *testing.T) {
	a, err := New("EV-1", allStrong()...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := a.Report()
	if !strings.Contains(r, "There is no score") {
		t.Fatal("the report does not say there is no score")
	}
	for _, attr := range Attributes() {
		if !strings.Contains(r, string(attr)) {
			t.Errorf("the report omits %s", attr)
		}
	}
}
