package ladder

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/epistemic"
)

var lt = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// The audit's three sentences, as they would actually be recorded.
func stopped() Statement {
	return Statement{ID: "st:obs-1", Kind: Observation,
		Text:     "the vessel reported no position for six hours while at 1.00N 103.80E",
		Recorder: "ais-network-a", EvidenceRefs: []string{"evidenceversion:ais-1"},
		State: epistemic.Present, At: lt}
}

func draught() Statement {
	return Statement{ID: "st:obs-2", Kind: Observation,
		Text:     "reported draught rose from 7.1 m to 13.4 m across that window",
		Recorder: "ais-network-a", EvidenceRefs: []string{"evidenceversion:ais-2"},
		State: epistemic.Present, At: lt}
}

func discharged() Statement {
	return Statement{ID: "st:hyp-1", Kind: Hypothesis,
		Text:    "the vessel loaded cargo during the reporting gap",
		RestsOn: []contract.ID{"st:obs-1", "st:obs-2"},
		Alternatives: []string{
			"ballast was taken on and the earlier draught was stale",
			"both draught values are data-entry artefacts",
		},
		Discriminator: "the terminal's berth and crane records for the window",
		State:         epistemic.Present, At: lt}
}

// TestAStopIsNotADischarge.
//
// The error the package exists to name: "the vessel discharged cargo,
// because it stopped". The stop is an observation; the discharge is an
// account of it.
func TestAStopIsNotADischarge(t *testing.T) {
	bad := Statement{ID: "st:asr-1", Kind: Assertion,
		Text:         "the vessel discharged cargo at Port X",
		RestsOn:      []contract.ID{"st:obs-1"},
		StandsBehind: "human:analyst-1", At: lt}
	_, err := NewChain(stopped(), bad)
	if !errors.Is(err, ErrSkippedRung) {
		t.Fatalf("an assertion rested directly on an observation: %v", err)
	}
	if !strings.Contains(err.Error(), "skips the place where alternatives would have been") {
		t.Fatalf("the refusal does not name what was skipped: %v", err)
	}

	// The lawful form: the same claim as a hypothesis, with its
	// competitors, and an assertion resting on that.
	good := Statement{ID: "st:asr-1", Kind: Assertion,
		Text:         "on the evidence available, cargo was loaded during the gap",
		RestsOn:      []contract.ID{"st:hyp-1"},
		StandsBehind: "human:analyst-1", At: lt}
	if _, err := NewChain(stopped(), draught(), discharged(), good); err != nil {
		t.Fatalf("the lawful chain was refused: %v", err)
	}
}

// TestAnObservationRestsOnNothing. A statement built from others is an
// inference wearing the word.
func TestAnObservationRestsOnNothing(t *testing.T) {
	s := stopped()
	s.RestsOn = []contract.ID{"st:obs-2"}
	if err := s.Validate(); !errors.Is(err, ErrObservationDerived) {
		t.Fatalf("a derived observation validated: %v", err)
	}
	s = stopped()
	s.Recorder = ""
	if err := s.Validate(); err == nil {
		t.Fatal("an observation nobody made validated")
	}
	s = stopped()
	s.EvidenceRefs = nil
	if err := s.Validate(); err == nil {
		t.Fatal("a PRESENT observation with nothing producible validated")
	}
}

// TestAHypothesisWithNoAlternativesIsAnAssertionInDisguise.
func TestAHypothesisWithNoAlternativesIsAnAssertionInDisguise(t *testing.T) {
	h := discharged()
	h.Alternatives = nil
	if err := h.Validate(); err == nil {
		t.Fatal("a hypothesis with no competing accounts validated")
	}
	h = discharged()
	h.Discriminator = ""
	if err := h.Validate(); err == nil {
		t.Fatal("a hypothesis that names nothing that would separate it validated")
	}
}

// TestAnInferenceRestingOnAHypothesisIsRefused.
//
// It inherits that hypothesis's uncertainty and stops being
// mechanical, which is the moment a calculation becomes a judgement
// without anybody noticing.
func TestAnInferenceRestingOnAHypothesisIsRefused(t *testing.T) {
	inf := Statement{ID: "st:inf-1", Kind: Inference,
		Text:    "the loaded quantity was 6,300 tonnes",
		RestsOn: []contract.ID{"st:hyp-1"},
		Method:  "draught survey conversion", At: lt}
	_, err := NewChain(stopped(), draught(), discharged(), inf)
	if !errors.Is(err, ErrSkippedRung) {
		t.Fatalf("an inference rested on a hypothesis: %v", err)
	}
	if !strings.Contains(err.Error(), "stops being mechanical") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
	// Resting on observations is fine.
	inf.RestsOn = []contract.ID{"st:obs-2"}
	if _, err := NewChain(stopped(), draught(), inf); err != nil {
		t.Fatalf("an inference over observations was refused: %v", err)
	}
	// And it must name a method somebody else can repeat.
	inf.Method = ""
	if err := inf.Validate(); err == nil {
		t.Fatal("an inference nobody can repeat validated")
	}
}

// TestVeriqoMayNotTakeADecision.
//
// A system that decides is not a neutral one, and the whole
// positioning rests on not being a party.
func TestVeriqoMayNotTakeADecision(t *testing.T) {
	for _, who := range []string{"VERIQO", "veriqo", "the VERIQO platform", "veriqo-engine"} {
		d := Statement{ID: "st:dec-1", Kind: Decision,
			Text: "the claim is declined", RestsOn: []contract.ID{"st:asr-1"},
			TakenBy: who, Authority: "the policy", At: lt}
		if err := d.Validate(); !errors.Is(err, ErrNotOurs) {
			t.Fatalf("VERIQO took a decision as %q: %v", who, err)
		}
	}
	ok := Statement{ID: "st:dec-1", Kind: Decision,
		Text: "the claim is declined", RestsOn: []contract.ID{"st:asr-1"},
		TakenBy: "human:claims-handler-3", Authority: "policy section 4(b)", At: lt}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a properly attributed decision was refused: %v", err)
	}
	if Decision.VeriqoMayMake() {
		t.Fatal("VeriqoMayMake permits a decision")
	}
	for _, k := range []Kind{Observation, Inference, Hypothesis} {
		if !k.VeriqoMayMake() {
			t.Fatalf("VERIQO may not make an %s", k)
		}
	}
	if Assertion.VeriqoMayMake() {
		t.Fatal("VERIQO may originate an assertion without a human standing behind it")
	}
}

// TestAnUnownedAssertionIsARumourWithACitation.
func TestAnUnownedAssertionIsARumourWithACitation(t *testing.T) {
	a := Statement{ID: "st:asr-1", Kind: Assertion, Text: "cargo was loaded",
		RestsOn: []contract.ID{"st:hyp-1"}, At: lt}
	if err := a.Validate(); err == nil {
		t.Fatal("an assertion nobody stands behind validated")
	}
}

// TestOnlyADecisionIsIrreversible.
//
// The asymmetry the ladder exists for: the cost of getting the second
// rung wrong is borne at the fifth.
func TestOnlyADecisionIsIrreversible(t *testing.T) {
	for _, k := range Kinds() {
		want := k != Decision
		if k.Reversible() != want {
			t.Fatalf("%s.Reversible() = %v", k, k.Reversible())
		}
	}
}

// TestFoundationAnswersTheFirstQuestionAnOpposingExpertAsks.
func TestFoundationAnswersTheFirstQuestionAnOpposingExpertAsks(t *testing.T) {
	asr := Statement{ID: "st:asr-1", Kind: Assertion,
		Text:    "on the evidence available, cargo was loaded during the gap",
		RestsOn: []contract.ID{"st:hyp-1"}, StandsBehind: "human:analyst-1", At: lt}
	c, err := NewChain(stopped(), draught(), discharged(), asr)
	if err != nil {
		t.Fatal(err)
	}
	f, err := c.Foundation("st:asr-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 2 {
		t.Fatalf("the assertion rests on %d observations", len(f))
	}
	for _, o := range f {
		if o.Kind != Observation {
			t.Fatalf("Foundation returned a %s", o.Kind)
		}
	}
}

// TestJudgementDistanceCountsTheRungsWhereConfidenceCompounds.
func TestJudgementDistanceCountsTheRungsWhereConfidenceCompounds(t *testing.T) {
	// A second hypothesis built on the first: an account of an
	// account, which is where a dispute is most often lost.
	h2 := Statement{ID: "st:hyp-2", Kind: Hypothesis,
		Text:    "the cargo loaded was the disputed parcel",
		RestsOn: []contract.ID{"st:hyp-1"},
		Alternatives: []string{"a different parcel was loaded",
			"nothing was loaded and the draught is an artefact"},
		Discriminator: "the bill of lading for the parcel", At: lt}
	c, err := NewChain(stopped(), draught(), discharged(), h2)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[contract.ID]int{
		"st:obs-1": 0, "st:hyp-1": 1, "st:hyp-2": 2,
	} {
		got, err := c.JudgementDistance(id)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s has judgement distance %d, want %d", id, got, want)
		}
	}
	if !strings.Contains(c.Report(), "2 judgement(s) between this and the observations") {
		t.Fatalf("the report does not surface compounding judgement:\n%s", c.Report())
	}
}

// TestACycleIsRefused. Two statements resting on each other make the
// foundation unreachable.
func TestACycleIsRefused(t *testing.T) {
	a := discharged()
	a.ID = "st:hyp-a"
	a.RestsOn = []contract.ID{"st:hyp-b"}
	b := discharged()
	b.ID = "st:hyp-b"
	b.RestsOn = []contract.ID{"st:hyp-a"}
	if _, err := NewChain(a, b); err == nil {
		t.Fatal("a citation cycle was accepted")
	}
}

// TestTheReportSaysNoDecisionIsRecorded.
func TestTheReportSaysNoDecisionIsRecorded(t *testing.T) {
	c, err := NewChain(stopped(), draught(), discharged())
	if err != nil {
		t.Fatal(err)
	}
	r := c.Report()
	if !strings.Contains(r, "No DECISION is recorded here") {
		t.Fatalf("the report does not state the absence:\n%s", r)
	}
	// Each rung must carry the thing the rung below could not supply.
	for _, want := range []string{"recorded by ais-network-a", "competing:",
		"separated by:", "what was seen, what it was taken to mean, and who said so"} {
		if !strings.Contains(r, want) {
			t.Fatalf("the report omits %q:\n%s", want, r)
		}
	}
}

// TestEveryKindStatesTheQuestionAReaderShouldAsk.
func TestEveryKindStatesTheQuestionAReaderShouldAsk(t *testing.T) {
	for _, k := range Kinds() {
		if strings.TrimSpace(k.Question()) == "" {
			t.Fatalf("%s asks nothing of its reader", k)
		}
		got, err := Parse(k.String())
		if err != nil || got != k {
			t.Fatalf("Parse(%s) = %v, %v", k, got, err)
		}
	}
	if len(Kinds()) != 5 {
		t.Fatalf("%d kinds", len(Kinds()))
	}
	if _, err := Parse("CONCLUSION"); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("an invented kind parsed: %v", err)
	}
	// The zero value is OBSERVATION, which is the safest default: an
	// unpopulated statement claims the least.
	var s Statement
	if s.Kind != Observation {
		t.Fatalf("the zero kind is %s", s.Kind)
	}
}
