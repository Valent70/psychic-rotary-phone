package causation

import (
	"regexp"
	"strings"
	"testing"

	"veriqo/pkg/insurance/evidence"
)

func buildSet(t *testing.T) *HypothesisSet {
	t.Helper()
	hs, err := NewHypothesisSet("CASE-042", "CLM-042", "What caused the cargo damage discovered at delivery?")
	if err != nil {
		t.Fatalf("NewHypothesisSet: %v", err)
	}
	return hs
}

func TestExplainRejectsNilOrEmptySet(t *testing.T) {
	if _, err := Explain(nil, nil); err != ErrEmptyHypothesisSet {
		t.Fatalf("expected ErrEmptyHypothesisSet for nil set, got %v", err)
	}
	hs := buildSet(t)
	if _, err := Explain(hs, nil); err != ErrEmptyHypothesisSet {
		t.Fatalf("expected ErrEmptyHypothesisSet for empty set, got %v", err)
	}
}

// forbiddenBareCausation is what the blueprint's §17 explicitly says
// VICE must never output: a bare, unqualified "<something> caused the
// damage" sentence with no hedge. This regex looks for that exact
// unqualified shape (a "caused" clause NOT preceded, within the same
// sentence, by hedging language) so every test below can assert
// structurally, not just by eyeballing one string, that Explain never
// produces it.
var forbiddenBareCausation = regexp.MustCompile(`(?i)^[A-Za-z ]+ caused (the|this) [a-z]+\.$`)

func assertNeverBareCausation(t *testing.T, narrative string) {
	t.Helper()
	for _, sentence := range strings.Split(narrative, ". ") {
		s := strings.TrimSpace(sentence)
		if s == "" {
			continue
		}
		if !strings.HasSuffix(s, ".") {
			s += "."
		}
		if forbiddenBareCausation.MatchString(s) {
			t.Fatalf("narrative contains a bare unqualified causation sentence: %q (full narrative: %q)", s, narrative)
		}
	}
	// The blueprint's own forbidden example, checked literally too.
	if strings.Contains(narrative, "Carrier caused the damage.") {
		t.Fatalf("narrative reproduces the blueprint's own forbidden example verbatim: %q", narrative)
	}
}

// TestExplainReproducesBlueprintSection17Example is the named test for
// the blueprint's §17 exact worked output shape.
func TestExplainReproducesBlueprintSection17Example(t *testing.T) {
	hs := buildSet(t)
	for _, id := range []HypothesisID{"H1", "H2", "H3", "H4"} {
		h, _ := NewHypothesis(id, "desc for "+string(id))
		if err := hs.Add(h); err != nil {
			t.Fatalf("Add(%s): %v", id, err)
		}
	}
	// H3 has more supporting evidence than any alternative.
	for _, ev := range []string{"EV-1", "EV-2", "EV-3"} {
		if err := hs.AddSupportingEvidence("H3", ev); err != nil {
			t.Fatalf("AddSupportingEvidence: %v", err)
		}
	}
	if err := hs.AddSupportingEvidence("H1", "EV-9"); err != nil {
		t.Fatalf("AddSupportingEvidence: %v", err)
	}
	if err := hs.AddMissingEvidence("H3", "the custody record between discharge and warehouse receipt is incomplete"); err != nil {
		t.Fatalf("AddMissingEvidence: %v", err)
	}

	exp, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	want := "Evidence currently supports Hypothesis H3 more strongly than alternative hypotheses; however, causation remains unresolved because the custody record between discharge and warehouse receipt is incomplete."
	if exp.Narrative != want {
		t.Fatalf("narrative mismatch:\n got:  %q\n want: %q", exp.Narrative, want)
	}
	if exp.LeadingHypothesis != "H3" {
		t.Fatalf("expected LeadingHypothesis H3, got %s", exp.LeadingHypothesis)
	}
	assertNeverBareCausation(t, exp.Narrative)
}

// TestExplainAdversarialNeverHidesTheGap is the required adversarial
// test: a hypothesis with OVERWHELMINGLY more supporting evidence than
// every alternative, but with at least one MissingEvidence entry
// recorded against it, must never be presented by Explain as though it
// were fully proven — the gap must be explicitly surfaced in either the
// Narrative or the Caveats (this test checks both are actually
// present).
func TestExplainAdversarialNeverHidesTheGap(t *testing.T) {
	hs := buildSet(t)
	for _, id := range []HypothesisID{"H1", "H2", "H3", "H4", "H5", "H6", "H7", "H8"} {
		h, _ := NewHypothesis(id, "desc for "+string(id))
		hs.Add(h)
	}

	// H3 gets overwhelming support: 20 independent supporting items
	// versus at most 1 for any other hypothesis.
	for i := 0; i < 20; i++ {
		if err := hs.AddSupportingEvidence("H3", "EV-"+string(rune('A'+i))); err != nil {
			t.Fatalf("AddSupportingEvidence #%d: %v", i, err)
		}
	}
	hs.AddSupportingEvidence("H2", "EV-weak")

	// But H3 also carries one recorded evidence gap.
	if err := hs.AddMissingEvidence("H3", "calibration certificate for the reefer unit has not been produced"); err != nil {
		t.Fatalf("AddMissingEvidence: %v", err)
	}

	h3, _ := hs.Get("H3")
	if h3.Status == StatusSupported {
		t.Fatal("golden rule violated: H3 must not be reported SUPPORTED while a MissingEvidence entry is on record")
	}
	if h3.Status != StatusPartiallySupported {
		t.Fatalf("expected PARTIALLY_SUPPORTED, got %s", h3.Status)
	}

	exp, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.LeadingHypothesis != "H3" {
		t.Fatalf("expected H3 to lead on overwhelming supporting evidence, got %s", exp.LeadingHypothesis)
	}

	// The gap must be surfaced — either in the narrative itself, or
	// explicitly itemised in Caveats. Require it to appear in BOTH,
	// since Explain is built to do both.
	if !strings.Contains(exp.Narrative, "calibration certificate") {
		t.Fatalf("expected the narrative to surface the missing-evidence gap, got: %q", exp.Narrative)
	}
	foundInCaveats := false
	for _, c := range exp.Caveats {
		if strings.Contains(c, "calibration certificate") {
			foundInCaveats = true
		}
	}
	if !foundInCaveats {
		t.Fatalf("expected Caveats to explicitly list the missing-evidence gap, got: %v", exp.Caveats)
	}
	assertNeverBareCausation(t, exp.Narrative)

	// And the narrative must never claim H3 is fully proven: it must
	// always frame it as RELATIVE support, via the fixed hedge phrase.
	if !strings.Contains(exp.Narrative, "more strongly than alternative hypotheses") {
		t.Fatalf("expected the relative-support hedge phrase, got: %q", exp.Narrative)
	}
	if !strings.Contains(exp.Narrative, "causation remains unresolved") {
		t.Fatalf("expected the unresolved hedge phrase, got: %q", exp.Narrative)
	}
}

// TestExplainAdversarialConflictingSurveyReports maps blueprint §35
// adversarial case #9 ("Conflicting survey reports") onto two
// hypotheses whose supporting evidence directly conflicts: a survey
// report for H3 (carrier handling) and a separate, contradicting survey
// report for H6 (terminal handling) leave both equally supported.
// Explain must not manufacture a false single leader.
func TestExplainAdversarialConflictingSurveyReports(t *testing.T) {
	hs := buildSet(t)
	for _, id := range []HypothesisID{"H3", "H6"} {
		h, _ := NewHypothesis(id, "desc for "+string(id))
		hs.Add(h)
	}
	// Survey-A supports H3; Survey-B supports H6. Each survey also
	// directly contradicts the OTHER hypothesis — the shape of two
	// genuinely conflicting survey reports.
	hs.AddSupportingEvidence("H3", "SURVEY-A")
	hs.AddContradictingEvidence("H6", "SURVEY-A")
	hs.AddSupportingEvidence("H6", "SURVEY-B")
	hs.AddContradictingEvidence("H3", "SURVEY-B")

	h3, _ := hs.Get("H3")
	h6, _ := hs.Get("H6")
	if h3.Status != StatusPartiallySupported || h6.Status != StatusPartiallySupported {
		t.Fatalf("expected both hypotheses PARTIALLY_SUPPORTED (1 support, 1 contradiction each), got H3=%s H6=%s", h3.Status, h6.Status)
	}

	exp, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.LeadingHypothesis != "" {
		t.Fatalf("expected NO single leading hypothesis for tied conflicting evidence, got %s", exp.LeadingHypothesis)
	}
	if len(exp.Contenders) != 2 {
		t.Fatalf("expected 2 tied contenders, got %d: %v", len(exp.Contenders), exp.Contenders)
	}
	if !strings.Contains(exp.Narrative, "H3") || !strings.Contains(exp.Narrative, "H6") {
		t.Fatalf("expected both H3 and H6 named in the tied narrative, got: %q", exp.Narrative)
	}
	if !strings.Contains(exp.Narrative, "causation remains unresolved") {
		t.Fatalf("expected the unresolved hedge phrase, got: %q", exp.Narrative)
	}
	assertNeverBareCausation(t, exp.Narrative)
}

// TestExplainNoSupportAtAll covers the case where nothing in the set
// has any supporting evidence recorded — Explain must not invent a
// leader out of thin air, and must not phrase this as a negative
// finding ("nothing caused this") either.
func TestExplainNoSupportAtAll(t *testing.T) {
	hs := buildSet(t)
	h1, _ := NewHypothesis("H1", "desc")
	hs.Add(h1)
	h2, _ := NewHypothesis("H2", "desc")
	hs.Add(h2)
	hs.AddMissingEvidence("H1", "packing list")

	exp, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.LeadingHypothesis != "" {
		t.Fatalf("expected no leading hypothesis, got %s", exp.LeadingHypothesis)
	}
	if !strings.Contains(exp.Narrative, "unresolved") {
		t.Fatalf("expected an unresolved narrative, got: %q", exp.Narrative)
	}
	found := false
	for _, c := range exp.Caveats {
		if strings.Contains(c, "packing list") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the recorded gap to surface in caveats even with zero evidence recorded, got: %v", exp.Caveats)
	}
	assertNeverBareCausation(t, exp.Narrative)
}

// TestExplainCleanSupportedHypothesisStillHedged verifies the golden
// rule at its strictest: even a hypothesis with a clean SUPPORTED
// status (no missing evidence, no contradicting evidence at all) is
// STILL never phrased as a final, certain causation conclusion.
func TestExplainCleanSupportedHypothesisStillHedged(t *testing.T) {
	hs := buildSet(t)
	h1, _ := NewHypothesis("H1", "desc")
	hs.Add(h1)
	h2, _ := NewHypothesis("H2", "desc")
	hs.Add(h2)
	hs.AddSupportingEvidence("H1", "EV-1")
	hs.AddSupportingEvidence("H1", "EV-2")

	h1After, _ := hs.Get("H1")
	if h1After.Status != StatusSupported {
		t.Fatalf("expected H1 SUPPORTED, got %s", h1After.Status)
	}

	exp, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.LeadingHypothesis != "H1" {
		t.Fatalf("expected H1 to lead, got %s", exp.LeadingHypothesis)
	}
	if !strings.Contains(exp.Narrative, "causation remains unresolved") {
		t.Fatalf("expected the unresolved hedge even for a SUPPORTED hypothesis, got: %q", exp.Narrative)
	}
	if len(exp.Caveats) == 0 {
		t.Fatal("expected at least one caveat even for a clean SUPPORTED hypothesis (VICE never issues a final determination)")
	}
	assertNeverBareCausation(t, exp.Narrative)
}

// TestExplainIsDeterministic calls Explain twice on the same
// HypothesisSet and requires byte-identical output — VICE's causation
// output must be reproducible (blueprint §37: deterministic replay).
func TestExplainIsDeterministic(t *testing.T) {
	hs := buildSet(t)
	for _, id := range []HypothesisID{"H1", "H2", "H3"} {
		h, _ := NewHypothesis(id, "desc")
		hs.Add(h)
	}
	hs.AddSupportingEvidence("H2", "EV-1")
	hs.AddSupportingEvidence("H2", "EV-2")
	hs.AddMissingEvidence("H2", "some gap")

	exp1, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain (1): %v", err)
	}
	exp2, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain (2): %v", err)
	}
	if exp1.Narrative != exp2.Narrative {
		t.Fatalf("Explain is not deterministic: %q vs %q", exp1.Narrative, exp2.Narrative)
	}
	if exp1.LeadingHypothesis != exp2.LeadingHypothesis {
		t.Fatal("Explain LeadingHypothesis is not deterministic")
	}
}

// TestExplainUsesDependencyGraphForRanking shows that supplying a
// DependencyGraph to Explain can change which hypothesis leads: without
// it, three dependent items look like 3 independent supporters; with
// it, they collapse to 1, which can flip the relative ranking against a
// genuinely-independent smaller set of evidence on another hypothesis.
func TestExplainUsesDependencyGraphForRanking(t *testing.T) {
	hs := buildSet(t)
	h3, _ := NewHypothesis("H3", "carrier handling")
	hs.Add(h3)
	h6, _ := NewHypothesis("H6", "terminal handling")
	hs.Add(h6)

	// H3's three supporting items are all dependent on one root photo.
	hs.AddSupportingEvidence("H3", "PhotoA")
	hs.AddSupportingEvidence("H3", "StatementA")
	hs.AddSupportingEvidence("H3", "SurveyA")

	// H6 has two genuinely independent supporting items.
	hs.AddSupportingEvidence("H6", "CCTV-Log")
	hs.AddSupportingEvidence("H6", "TerminalReport")

	dg := evidence.NewDependencyGraph()
	dg.AddDependency("StatementA", "PhotoA")
	dg.AddDependency("SurveyA", "StatementA")

	withoutDG, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain (no dg): %v", err)
	}
	if withoutDG.LeadingHypothesis != "H3" {
		t.Fatalf("expected H3 to lead without independence adjustment (3 vs 2), got %s", withoutDG.LeadingHypothesis)
	}

	withDG, err := Explain(hs, dg)
	if err != nil {
		t.Fatalf("Explain (with dg): %v", err)
	}
	if withDG.LeadingHypothesis != "H6" {
		t.Fatalf("expected H6 to lead once H3's dependent evidence collapses to 1 independent root (1 vs 2), got %s", withDG.LeadingHypothesis)
	}
	assertNeverBareCausation(t, withDG.Narrative)
}
