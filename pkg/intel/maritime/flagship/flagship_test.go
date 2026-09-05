package flagship

import (
	"strings"
	"testing"

	"veriqo/pkg/intel/maritime"
)

// TestThePassportValidates. The case is only worth showing if the
// document it produces would survive its own rules.
func TestThePassportValidates(t *testing.T) {
	if _, err := Passport(); err != nil {
		t.Fatal(err)
	}
}

// TestTheCaseComesOutAmbiguous.
//
// Deliberate. A worked example that resolves cleanly demonstrates the
// machinery on the one input shape that never occurs, and teaches the
// reader that VERIQO produces answers. The demonstration worth having
// is a case where the evidence is genuinely insufficient and the
// system is still useful -- by naming what would settle it.
func TestTheCaseComesOutAmbiguous(t *testing.T) {
	p, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	if p.Decision.ConfidenceState != "INSUFFICIENT_TO_DECIDE" {
		t.Fatalf("the flagship case resolves to %q; a demo that always concludes "+
			"teaches the wrong thing", p.Decision.ConfidenceState)
	}
	leading := 0
	for _, h := range p.Hypotheses {
		if strings.EqualFold(h.Standing, "LEADING") {
			leading++
		}
	}
	if leading != 0 {
		t.Errorf("%d hypotheses lead in a case declared undecidable", leading)
	}
	if len(p.DisproofRoute) < 4 {
		t.Errorf("an undecidable case offers only %d ways forward; 'we cannot tell' is "+
			"useful only beside 'here is what would tell us'", len(p.DisproofRoute))
	}
}

// TestTheSyntheticNoticeIsOnTheFaceOfTheDocument.
//
// Not in a footnote. This document is built to be handed to a lawyer
// or an adjuster, and the first thing they must know is that none of
// it is real.
func TestTheSyntheticNoticeIsOnTheFaceOfTheDocument(t *testing.T) {
	p, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	r := p.Render()
	head := r
	if i := strings.Index(r, "CASE\n"); i > 0 {
		head = r[:i]
	}
	if !strings.Contains(strings.ToUpper(head), "SYNTHETIC") {
		t.Fatal("the synthetic notice is not above the case header")
	}
	if !strings.Contains(strings.ToUpper(head), "NOTHING WHATEVER ABOUT VERIQO'S BEHAVIOUR ON REAL DATA") {
		t.Error("the notice does not say what the case fails to establish")
	}
}

// TestTheChainTheAuditNamedIsPresent.
//
// vessel -> cargo -> voyage -> port event -> AIS -> weather ->
// document -> contradiction -> independence -> hypotheses ->
// decision -> passport -> disproof.
func TestTheChainTheAuditNamedIsPresent(t *testing.T) {
	p, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	r := strings.ToLower(p.Render())
	for _, link := range []string{"draught", "voyage charter", "port agent",
		"ais", "metocean", "bill of lading", "contradiction", "independence",
		"hypotheses", "disproof route"} {
		if !strings.Contains(r, link) {
			t.Errorf("the chain is missing %q", link)
		}
	}
	if len(p.Observations) < 5 {
		t.Errorf("%d observations; the chain is thinner than the case it claims to be",
			len(p.Observations))
	}
	if len(p.Sources) < 4 {
		t.Errorf("%d sources", len(p.Sources))
	}
}

// TestAnUnresolvedContradictionStaysInTheDocument.
//
// The conflict between the draught change and the bill of lading is
// the case. A passport that resolved it silently would be a sales
// document.
func TestAnUnresolvedContradictionStaysInTheDocument(t *testing.T) {
	p, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	open := 0
	for _, c := range p.Contradictions {
		if c.Resolved == "" {
			open++
		}
	}
	if open == 0 {
		t.Fatal("every contradiction is resolved in a case declared undecidable")
	}
	if !strings.Contains(p.Render(), "UNRESOLVED. It stays in the passport.") {
		t.Error("an unresolved contradiction does not say so in the render")
	}
}

// TestCopyDetectionCutsTheCorroboration.
//
// Five accounts, three observations. If this ever reports five, the
// case is quietly claiming corroboration it does not have.
func TestCopyDetectionCutsTheCorroboration(t *testing.T) {
	an, err := CopyAnalysis()
	if err != nil {
		t.Fatal(err)
	}
	n, _ := an.EffectiveCount()
	if n >= len(Accounts()) {
		t.Fatalf("%d accounts survived as %d observations; the copies were not caught",
			len(Accounts()), n)
	}
	p, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	if p.Independence.EffectiveObservations != n {
		t.Errorf("the passport reports %d effective observations, the analysis found %d",
			p.Independence.EffectiveObservations, n)
	}
	if p.Independence.EffectiveObservations >= p.Independence.Producers {
		t.Error("the effective count is not below the producer count, so the passport " +
			"presents five producers as five observations")
	}
}

// TestTheGapIsNotCalledSpoofing.
//
// The case is built with the classic ship-to-ship signature precisely
// because it is the one a system is most likely to over-call.
func TestTheGapIsNotCalledSpoofing(t *testing.T) {
	p, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	r := strings.ToUpper(p.Render())
	for _, phrase := range []string{"WENT DARK", "DELIBERATELY CONCEAL", "EVADING",
		"SANCTIONS EVASION"} {
		if strings.Contains(r, phrase) && !strings.Contains(r, "REFUSED") {
			t.Errorf("the passport contains %q", phrase)
		}
	}
	if !strings.Contains(p.Decision.Conclusion, "not established") {
		t.Errorf("the conclusion is not a refusal to conclude: %q", p.Decision.Conclusion)
	}
}

// TestTheTrackActuallyContainsTheGap.
//
// The case's central observation has to arise from the detector rather
// than from the narrative. If the constructed track stops producing a
// gap, the passport is describing something that is not there.
func TestTheTrackActuallyContainsTheGap(t *testing.T) {
	tr, err := Track()
	if err != nil {
		t.Fatal(err)
	}
	gaps := tr.Gaps(maritime.DefaultGapPolicy())
	if len(gaps) != 1 {
		t.Fatalf("%d gaps in the constructed track, want 1", len(gaps))
	}
	dr := tr.DraughtChange(3.0)
	if len(dr) == 0 {
		t.Fatal("the draught change the case rests on is not detected")
	}
}

// TestTheTriageOffersNoConclusion.
func TestTheTriageOffersNoConclusion(t *testing.T) {
	tri, err := Triage()
	if err != nil {
		t.Fatal(err)
	}
	if tri.Distinguishable() {
		t.Fatal("the gap is reported as decidable with no modality contracted for")
	}
	if len(tri.Needs()) == 0 {
		t.Fatal("nothing is named that would settle it")
	}
}

// TestEveryHypothesisNamesWhatWouldSeparateIt.
func TestEveryHypothesisNamesWhatWouldSeparateIt(t *testing.T) {
	p, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range p.Hypotheses {
		if strings.TrimSpace(h.Discriminator) == "" {
			t.Errorf("%s offers no discriminator, so it competes with nothing", h.Ref)
		}
	}
}

// TestTheRenderIsDeterministic. The document is a deliverable; two
// builds must diff to nothing.
func TestTheRenderIsDeterministic(t *testing.T) {
	a, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Passport()
	if err != nil {
		t.Fatal(err)
	}
	if a.Render() != b.Render() {
		t.Fatal("two builds of the flagship passport differ")
	}
}
