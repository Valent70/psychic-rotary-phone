package trust

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func mustUniform(t *testing.T, id string, k Kind) Trust {
	t.Helper()
	tr, err := Uniform(id, k)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestSourceTrustIsNotConclusionTrust is the rule the package exists
// for, and the flow that must not exist.
func TestSourceTrustIsNotConclusionTrust(t *testing.T) {
	src := mustUniform(t, "src:ais-provider", InSource)
	src, _ = src.Observe(200, 2)

	if _, err := Propagate(src, InConclusion, "claim:c1"); !errors.Is(err, ErrKindMismatch) {
		t.Fatal("A TRUSTED SOURCE PROPAGATED DIRECTLY TO A TRUSTED CONCLUSION")
	}
	// Nor from evidence.
	ev := mustUniform(t, "ev:1", InEvidence)
	ev, _ = ev.Observe(100, 0)
	if _, err := Propagate(ev, InConclusion, "claim:c1"); !errors.Is(err, ErrKindMismatch) {
		t.Fatal("EVIDENCE TRUST PROPAGATED TO CONCLUSION TRUST")
	}
	// Nor from a model: a well-behaved model does not make its output
	// true.
	m := mustUniform(t, "model:extract-v3", InModel)
	m, _ = m.Observe(500, 1)
	if _, err := Propagate(m, InConclusion, "claim:c1"); !errors.Is(err, ErrKindMismatch) {
		t.Fatal("MODEL TRUST PROPAGATED TO CONCLUSION TRUST")
	}
	// And the error says why, so a caller does not go looking for the
	// missing feature.
	_, err := Propagate(src, InConclusion, "claim:c1")
	if !strings.Contains(err.Error(), "assessed from its own evidence") {
		t.Fatalf("the refusal does not explain itself: %v", err)
	}
}

// TestALookupRequiresTheKind. A lookup by id alone would deliver the
// substitution through a convenience method.
func TestALookupRequiresTheKind(t *testing.T) {
	r := NewRegister()
	src := mustUniform(t, "shared-id", InSource)
	src, _ = src.Observe(100, 0)
	concl := mustUniform(t, "shared-id", InConclusion)
	concl, _ = concl.Observe(1, 9)
	r.Put(src)
	r.Put(concl)

	gotSrc, ok := r.Get(InSource, "shared-id")
	if !ok || gotSrc.Mean() < 0.9 {
		t.Fatalf("source trust = %v", gotSrc)
	}
	gotConcl, ok := r.Get(InConclusion, "shared-id")
	if !ok || gotConcl.Mean() > 0.3 {
		t.Fatalf("conclusion trust = %v", gotConcl)
	}
	// The same id under two kinds holds two different beliefs, which
	// is the whole point.
	if gotSrc.Mean() == gotConcl.Mean() {
		t.Fatal("the two kinds share one value")
	}
}

// TestTwoOfTwoIsNotTwoHundredOfTwoHundred. Reporting only the mean
// makes them indistinguishable.
func TestTwoOfTwoIsNotTwoHundredOfTwoHundred(t *testing.T) {
	small := mustUniform(t, "a", InSource)
	small, _ = small.Observe(2, 0)
	large := mustUniform(t, "b", InSource)
	large, _ = large.Observe(200, 0)

	if small.Mean() >= large.Mean() {
		// They are not equal under a Beta prior, but they are close;
		// what matters is the interval and the count.
		t.Logf("means: %.3f vs %.3f", small.Mean(), large.Mean())
	}
	sl, sh := small.Interval()
	ll, lh := large.Interval()
	if (sh - sl) <= (lh - ll) {
		t.Fatalf("the small sample's interval [%.2f,%.2f] is no wider than the large "+
			"sample's [%.2f,%.2f]", sl, sh, ll, lh)
	}
	if small.Grounded() {
		t.Fatal("two observations were reported as grounded")
	}
	if !large.Grounded() {
		t.Fatal("two hundred observations were reported as ungrounded")
	}
	if small.Band() != "UNGROUNDED" {
		t.Fatalf("a two-observation trust bands as %s", small.Band())
	}
	// And the count travels in the rendering.
	if !strings.Contains(small.String(), "2 observation(s)") {
		t.Fatalf("the observation count is not reported: %s", small)
	}
}

// TestThePriorIsStatedNotAssumed.
func TestThePriorIsStatedNotAssumed(t *testing.T) {
	u := mustUniform(t, "a", InSource)
	if u.Alpha != 1 || u.Beta != 1 {
		t.Fatalf("the uniform prior is Beta(%v,%v)", u.Alpha, u.Beta)
	}
	if u.Observations != 0 {
		t.Fatalf("the prior counted as %d observations", u.Observations)
	}
	if math.Abs(u.Mean()-0.5) > 1e-9 {
		t.Fatalf("the uniform prior's mean is %v", u.Mean())
	}
	// A confident prior is expressible, and it is a deliberate choice.
	confident, err := New("b", InSource, 9, 1)
	if err != nil {
		t.Fatal(err)
	}
	if confident.Mean() <= 0.8 {
		t.Fatalf("a Beta(9,1) prior has mean %v", confident.Mean())
	}
	if confident.Grounded() {
		t.Fatal("a prior alone made a trust grounded; the prior is not evidence")
	}
	if _, err := New("c", InSource, 0, 1); !errors.Is(err, ErrBadPrior) {
		t.Fatal("a degenerate prior was accepted")
	}
}

// TestObservationsMoveTheBeliefInTheRightDirection.
func TestObservationsMoveTheBeliefInTheRightDirection(t *testing.T) {
	tr := mustUniform(t, "a", InSource)
	before := tr.Mean()
	good, _ := tr.Observe(20, 0)
	bad, _ := tr.Observe(0, 20)
	if good.Mean() <= before {
		t.Fatal("successes did not raise the mean")
	}
	if bad.Mean() >= before {
		t.Fatal("failures did not lower the mean")
	}
	if good.Observations != 20 || bad.Observations != 20 {
		t.Fatalf("observation counts = %d / %d", good.Observations, bad.Observations)
	}
	// More evidence narrows the interval.
	l1, h1 := good.Interval()
	more, _ := good.Observe(200, 0)
	l2, h2 := more.Interval()
	if (h2 - l2) >= (h1 - l1) {
		t.Fatalf("more observations did not narrow the interval: [%.3f,%.3f] -> [%.3f,%.3f]",
			l1, h1, l2, h2)
	}
}

// TestPropagationAttenuatesAndNeverOvergrounds.
//
// A derived trust must never be reported as better grounded than what
// it came from.
func TestPropagationAttenuatesAndNeverOvergrounds(t *testing.T) {
	src := mustUniform(t, "src:a", InSource)
	src, _ = src.Observe(100, 0)

	ev, err := Propagate(src, InEvidence, "ev:1")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != InEvidence {
		t.Fatalf("the derived trust is of kind %s", ev.Kind)
	}
	if ev.Observations >= src.Observations {
		t.Fatalf("derived observations %d >= source's %d", ev.Observations, src.Observations)
	}
	// The interval must be no narrower than the source's.
	sl, sh := src.Interval()
	el, eh := ev.Interval()
	if (eh - el) < (sh - sl) {
		t.Fatalf("the derived trust is more confident than its source: "+
			"[%.3f,%.3f] vs [%.3f,%.3f]", el, eh, sl, sh)
	}
}

// TestOnlyThePermittedFlowsExist, so the omissions are as enforced as
// the inclusions.
func TestOnlyThePermittedFlowsExist(t *testing.T) {
	permitted := map[string]bool{}
	for _, p := range Propagations() {
		permitted[string(p.From)+"->"+string(p.To)] = true
		if p.Attenuation <= 0 || p.Attenuation >= 1 {
			t.Errorf("%s -> %s has attenuation %v; a flow must lose something and not "+
				"lose everything", p.From, p.To, p.Attenuation)
		}
		if p.Why == "" {
			t.Errorf("%s -> %s states no reason", p.From, p.To)
		}
	}
	for _, from := range Kinds() {
		for _, to := range Kinds() {
			if from == to {
				continue
			}
			tr := mustUniform(t, "x", from)
			tr, _ = tr.Observe(10, 0)
			_, err := Propagate(tr, to, "y")
			key := string(from) + "->" + string(to)
			if permitted[key] && err != nil {
				t.Errorf("the permitted flow %s was refused: %v", key, err)
			}
			if !permitted[key] && err == nil {
				t.Errorf("the unlisted flow %s was permitted", key)
			}
		}
	}
	// The three specific omissions that matter.
	for _, banned := range []string{"EVIDENCE->CONCLUSION", "MODEL->CONCLUSION", "SOURCE->CONCLUSION"} {
		if permitted[banned] {
			t.Errorf("%s is a permitted flow", banned)
		}
	}
}

// TestBandsAreCoarseAndUngroundedIsItsOwnBand. A reader must not be
// handed three decimal places over four observations.
func TestBandsAreCoarseAndUngroundedIsItsOwnBand(t *testing.T) {
	tr := mustUniform(t, "a", InSource)
	tr, _ = tr.Observe(3, 0)
	if tr.Band() != "UNGROUNDED" {
		t.Fatalf("a three-observation trust bands as %s", tr.Band())
	}
	tr, _ = tr.Observe(97, 0)
	if tr.Band() != "HIGH" {
		t.Fatalf("a 100/0 trust bands as %s", tr.Band())
	}
	low := mustUniform(t, "b", InSource)
	low, _ = low.Observe(2, 98)
	if low.Band() != "VERY_LOW" {
		t.Fatalf("a 2/100 trust bands as %s", low.Band())
	}
}

// TestTheRegisterKeepsTheSixKindsApart.
func TestTheRegisterKeepsTheSixKindsApart(t *testing.T) {
	r := NewRegister()
	for _, k := range Kinds() {
		tr := mustUniform(t, "subject-1", k)
		tr, _ = tr.Observe(10, 0)
		if err := r.Put(tr); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range Kinds() {
		if got := r.All(k); len(got) != 1 {
			t.Errorf("%s holds %d entries", k, len(got))
		}
	}
	if _, ok := r.Get(Kind("VIBES"), "subject-1"); ok {
		t.Fatal("an unknown kind resolved")
	}
	if !strings.Contains(r.Report(), "not trust in a conclusion") {
		t.Fatalf("the report does not state the rule:\n%s", r.Report())
	}
}

func TestMalformedTrustIsRefused(t *testing.T) {
	if _, err := Uniform("", InSource); !errors.Is(err, ErrNoSubject) {
		t.Fatal("a trust with no subject was built")
	}
	if _, err := Uniform("a", Kind("LUCK")); !errors.Is(err, ErrUnknownKind) {
		t.Fatal("an unknown kind was accepted")
	}
	tr := mustUniform(t, "a", InSource)
	if _, err := tr.Observe(-1, 0); err == nil {
		t.Fatal("a negative observation count was accepted")
	}
}

// TestIntervalStaysInBounds. A band that ran outside [0,1] would be
// arithmetically fine and meaningless as a rate.
func TestIntervalStaysInBounds(t *testing.T) {
	for _, obs := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1000, 0}, {0, 1000}} {
		tr := mustUniform(t, "a", InSource)
		tr, _ = tr.Observe(obs[0], obs[1])
		l, h := tr.Interval()
		if l < 0 || h > 1 || l > h {
			t.Errorf("%v produced the interval [%v,%v]", obs, l, h)
		}
	}
}
