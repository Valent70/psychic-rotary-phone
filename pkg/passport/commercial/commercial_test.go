package commercial

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

func valid() Passport {
	return Passport{
		Case: Case{ID: "C1", From: t0, To: t0.Add(48 * time.Hour),
			Jurisdiction: "England & Wales", DeclaredPurpose: "a cargo dispute"},
		Question:     "did it happen?",
		Observations: []Observation{{Ref: "O1", What: "a thing was seen", At: t0, SourceRef: "S1"}},
		Sources: []Source{{Ref: "S1", Producer: "a registry", Acquisition: "licensed feed",
			Rights: "internal analysis only", Timestamp: t0}},
		Independence: Independence{Accounts: 3, Producers: 2, EffectiveObservations: 2,
			Assessed: []string{"PRODUCER"}, Unknown: []string{"UPSTREAM_FEED"},
			Note: "two clusters survived; that is not a finding of independence"},
		Hypotheses: []Hypothesis{
			{Ref: "H1", Statement: "it happened", Standing: "OPEN"},
			{Ref: "H2", Statement: "it did not", Standing: "OPEN"},
		},
		Qualification: Qualification{
			Verified: []string{"the digests match"},
			Unknown:  []string{"whether a second vessel was present"},
		},
		Decision: Decision{Conclusion: "not established", ConfidenceState: "INSUFFICIENT_TO_DECIDE",
			Authority: "human:a.analyst", AuthorityRole: "CASE_OWNER", At: t0},
		DisproofRoute:  []string{"obtain an image"},
		Replay:         Replay{Manifest: "replay:1", Policy: "policy:1", Version: "v1", Hash: "abc", Signature: "unsigned"},
		Limitations:    []string{"the case is synthetic"},
		AssuranceState: "INTERNALLY_ASSURED", ExternalStatus: "NOT_EXTERNALLY_QUALIFIED",
	}
}

func TestTheBaselinePassportIsValid(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestVeriqoMayNotBeTheDecisionAuthority.
//
// The commercial document is where the temptation is greatest: a
// passport signed by the system reads as objective, and is the one
// thing it must never be.
func TestVeriqoMayNotBeTheDecisionAuthority(t *testing.T) {
	for _, who := range []string{"VERIQO", "veriqo-engine", "agent:analyst-1",
		"service:reasoner"} {
		p := valid()
		p.Decision.Authority = who
		if err := p.Validate(); !errors.Is(err, ErrVeriqoDecided) {
			t.Errorf("%q was accepted as a decision authority: %v", who, err)
		}
	}
}

// TestAConfidencePercentageIsRefused.
//
// "72% confident" is the number a reader multiplies by an exposure and
// puts in a reserve calculation. There is no arithmetic behind it.
func TestAConfidencePercentageIsRefused(t *testing.T) {
	for _, c := range []string{"72%", "0.85", "HIGH (90%)"} {
		p := valid()
		p.Decision.ConfidenceState = c
		if err := p.Validate(); err == nil {
			t.Errorf("confidence state %q was accepted", c)
		}
	}
}

// TestASinglehypothesisIsRefused.
//
// One account of the evidence has not been tested against anything,
// and presenting it as a conclusion is the whole failure mode.
func TestASinglehypothesisIsRefused(t *testing.T) {
	p := valid()
	p.Hypotheses = p.Hypotheses[:1]
	if err := p.Validate(); !errors.Is(err, ErrNoAlternative) {
		t.Fatalf("a single-hypothesis passport was accepted: %v", err)
	}
}

// TestTwoLeadingHypothesesIsRefused.
func TestTwoLeadingHypothesesIsRefused(t *testing.T) {
	p := valid()
	p.Hypotheses[0].Standing = "LEADING"
	p.Hypotheses[1].Standing = "LEADING"
	if err := p.Validate(); err == nil {
		t.Fatal("two leading hypotheses were accepted")
	}
}

// TestAPassportWithNothingUnknownHasNotBeenExamined.
//
// The UNKNOWN column is the one that gets dropped, because a document
// without it is more persuasive. That is exactly why it is required.
func TestAPassportWithNothingUnknownHasNotBeenExamined(t *testing.T) {
	p := valid()
	p.Qualification.Unknown = nil
	if err := p.Validate(); !errors.Is(err, ErrMissingSection) {
		t.Fatalf("a passport claiming nothing is unknown was accepted: %v", err)
	}
}

// TestAPassportWithNoDisproofRouteIsRefused.
func TestAPassportWithNoDisproofRouteIsRefused(t *testing.T) {
	p := valid()
	p.DisproofRoute = nil
	err := p.Validate()
	if !errors.Is(err, ErrMissingSection) {
		t.Fatalf("an unfalsifiable passport was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "unfalsifiable") {
		t.Errorf("the refusal does not say what is wrong with it: %v", err)
	}
}

// TestAPassportWithNoLimitationsIsRefused.
func TestAPassportWithNoLimitationsIsRefused(t *testing.T) {
	p := valid()
	p.Limitations = nil
	if err := p.Validate(); !errors.Is(err, ErrMissingSection) {
		t.Fatal("a passport claiming to have settled everything was accepted")
	}
}

// TestAnObservationMustCiteASourceInTheTable.
//
// An observation with a dangling source reference is an assertion
// placed in the observations section, which is the most effective
// place in the document to hide one.
func TestAnObservationMustCiteASourceInTheTable(t *testing.T) {
	p := valid()
	p.Observations[0].SourceRef = "S99"
	if err := p.Validate(); err == nil {
		t.Fatal("an observation citing a source not in the provenance table was accepted")
	}
	p.Observations[0].SourceRef = ""
	if err := p.Validate(); err == nil {
		t.Fatal("an observation with no source was accepted")
	}
}

// TestASourceMustNameAProducerNotAVendor.
func TestASourceMustNameAProducerNotAVendor(t *testing.T) {
	p := valid()
	p.Sources[0].Producer = ""
	p.Sources[0].Vendor = "an aggregator"
	err := p.Validate()
	if err == nil {
		t.Fatal("a source with a vendor and no producer was accepted")
	}
	if !strings.Contains(err.Error(), "vendor is not a producer") {
		t.Errorf("the refusal does not make the distinction: %v", err)
	}
}

// TestADeclaredPurposeIsRequired.
//
// Evidence lawful to screen with may be unlawful to found a decision
// on. A passport that does not say what it is FOR cannot be checked
// against the rights its sources were acquired under.
func TestADeclaredPurposeIsRequired(t *testing.T) {
	p := valid()
	p.Case.DeclaredPurpose = ""
	err := p.Validate()
	if !errors.Is(err, ErrMissingSection) {
		t.Fatal("a passport with no declared purpose was accepted")
	}
	if !strings.Contains(err.Error(), "rights") {
		t.Errorf("the refusal does not say why it matters: %v", err)
	}
}

// TestTheIndependenceNoteIsRequired.
//
// "5 producers" without it reads as a finding that five independent
// parties agree.
func TestTheIndependenceNoteIsRequired(t *testing.T) {
	p := valid()
	p.Independence.Note = ""
	if err := p.Validate(); !errors.Is(err, ErrMissingSection) {
		t.Fatal("a producer count with no qualifying note was accepted")
	}
}

// TestTheRenderPutsExternalStatusLast.
//
// It is the thing the reader leaves with, and the section order is an
// argument: observations before hypotheses, contradictions before
// hypotheses, disproof before replay.
func TestTheRenderPutsExternalStatusLast(t *testing.T) {
	r := valid().Render()
	idx := func(s string) int { return strings.Index(r, "\n"+s+"\n") }
	obs, hyp := idx("OBSERVATIONS"), idx("HYPOTHESES")
	con, dis := idx("CONTRADICTIONS"), idx("DISPROOF ROUTE")
	rep, ext := idx("REPLAY"), idx("EXTERNAL STATUS")
	if !(obs < con && con < hyp) {
		t.Error("contradictions do not sit between observations and hypotheses")
	}
	if !(dis < rep) {
		t.Error("replay is offered before the route to attacking the conclusion")
	}
	if ext < rep {
		t.Error("external status is not last")
	}
	if !strings.Contains(r, "NOT_EXTERNALLY_QUALIFIED") {
		t.Error("the render does not carry the external status")
	}
	if !strings.Contains(r, "VERIQO assembled this decision. It did not take it.") {
		t.Error("the render does not say who decided")
	}
}

// TestAnEmptyContradictionSectionSaysWhatItMeans.
//
// "None found" is a statement about what was compared, not about the
// world, and a reader will take it the other way unless told.
func TestAnEmptyContradictionSectionSaysWhatItMeans(t *testing.T) {
	r := valid().Render()
	if !strings.Contains(r, "statement about what was compared") {
		t.Error("an empty contradictions section reads as an absence of conflict")
	}
}

// TestASourceWithUnknownRightsSaysUnknownRatherThanNothing.
func TestASourceWithUnknownRightsSaysUnknownRatherThanNothing(t *testing.T) {
	r := valid().Render()
	if !strings.Contains(r, "UNKNOWN -- which is not the same as unrestricted") {
		t.Error("a source with no stated lawful uses renders as though unrestricted")
	}
}

// TestTheRenderIsDeterministicAndFitsATerminal.
func TestTheRenderIsDeterministicAndFitsATerminal(t *testing.T) {
	p := valid()
	if p.Render() != p.Render() {
		t.Error("Render() is not deterministic")
	}
	for _, line := range strings.Split(p.Render(), "\n") {
		if len([]rune(line)) > 78 {
			t.Errorf("a %d-column line will wrap: %q", len([]rune(line)), line)
		}
	}
}
