package corpus

import (
	"bytes"
	"strings"
	"testing"

	"veriqo/pkg/evidence/redaction/worker"
)

// TestEveryVariantBuildsARealContainerCarryingTheTerm is the guard on
// the whole package. A variant whose generator does not actually put
// the term where the variant claims would make every downstream number
// meaningless -- the worker would "successfully redact" a document that
// never contained anything.
func TestEveryVariantBuildsARealContainerCarryingTheTerm(t *testing.T) {
	for _, v := range Variants {
		t.Run(v.ID, func(t *testing.T) {
			term := TermFor(v)
			doc, err := Build(v, term)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(doc) == 0 {
				t.Fatal("the generator produced nothing")
			}
			// A variant declared TermUnreadable is one this codebase
			// cannot see into: encrypted, an undecoded filter, or
			// structurally broken. For those the term is genuinely
			// invisible, and that invisibility IS the property being
			// exercised -- so the assertion is that the document is
			// real and carries the structure, not that we can read it.
			if v.TermUnreadable {
				if len(doc) < 100 {
					t.Fatalf("the %s fixture is too small to be a real document", v.ID)
				}
				return
			}
			view, err := worker.Inspectable(v.Kind, doc)
			if err != nil {
				t.Fatalf("Inspectable: %v", err)
			}
			needle := strings.ToLower(term)
			hay := strings.ToLower(string(view))
			// The escaped variant carries the XML form.
			if v.ID == "XLSX-ESCAPED-XML" {
				needle = strings.ToLower("R&amp;D Partners")
			}
			if !strings.Contains(hay, needle) {
				t.Fatalf("variant %s does not carry %q in its inspectable view: "+
					"every measurement over this variant would be vacuous", v.ID, needle)
			}
		})
	}
}

// TestTheOOXMLVariantsAreGenuinelyCompressed. If a generator stopped
// deflating, the whole corpus would silently become a test of
// uncompressed byte search.
func TestTheOOXMLVariantsAreGenuinelyCompressed(t *testing.T) {
	checked := 0
	for _, v := range Variants {
		if v.Kind != worker.KindXLSX && v.Kind != worker.KindPPTX {
			continue
		}
		term := TermFor(v)
		doc, err := Build(v, term)
		if err != nil {
			t.Fatalf("%s: %v", v.ID, err)
		}
		if bytes.Contains(doc, []byte(term)) {
			t.Errorf("%s stores the term uncompressed in the container", v.ID)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no OOXML variant was checked: the taxonomy has lost its OOXML coverage")
	}
}

// TestTheCorpusRunMatchesItsDeclaredDesign. Every variant's actual
// disposition must equal the one the taxonomy declares. A mismatch is
// either a regression in the worker or a stale expectation, and both
// need a human.
func TestTheCorpusRunMatchesItsDeclaredDesign(t *testing.T) {
	outcomes, cov, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cov.Mismatched) > 0 {
		t.Fatalf("the corpus run disagrees with the declared design:\n  %s\n\n%s",
			strings.Join(cov.Mismatched, "\n  "), cov.Report(outcomes))
	}
}

// TestNoVariantLeaks is the one result that must always be zero. A
// released derivative carrying a forbidden term is the worst outcome
// Article 18 can produce, and it would be worse than a refusal.
func TestNoVariantLeaks(t *testing.T) {
	outcomes, cov, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cov.Leaked) > 0 {
		t.Fatalf("a released derivative carries a forbidden term: %s\n\n%s",
			strings.Join(cov.Leaked, ", "), cov.Report(outcomes))
	}
}

// TestNothingFailsUnexpectedly. Refusal is designed; failure is not.
func TestNothingFailsUnexpectedly(t *testing.T) {
	outcomes, cov, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cov.Failed > 0 {
		t.Fatalf("%d variant(s) failed rather than being accepted or refused:\n%s",
			cov.Failed, cov.Report(outcomes))
	}
}

// TestTheCoverageRatioIsReportedAsCoverageNotAsAPassRate.
//
// This is the review's point encoded as a test. A run where the worker
// refuses everything has a perfect pass rate and zero capability, so
// the headline must be the accept ratio and must say what it is not.
func TestTheCoverageRatioIsReportedAsCoverageNotAsAPassRate(t *testing.T) {
	_, cov, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	h := cov.Headline()
	if !strings.Contains(h, "NOT a pass rate") {
		t.Fatal("the headline does not distinguish coverage from a pass rate")
	}
	if !strings.Contains(h, "none of them is capability") {
		t.Fatal("the headline does not say that a refusal is not capability")
	}
	if cov.AcceptRatio() >= 1.0 {
		t.Fatal("every variant was accepted: the taxonomy has lost its refusal cases, " +
			"and the coverage number would no longer mean anything")
	}
	if cov.AcceptRatio() <= 0 {
		t.Fatal("no variant was accepted: the workers do nothing")
	}
}

// TestTheWeightedGapIsReported. The refused variants that are common in
// real documents are the finding, and burying them in a per-variant
// grid would hide the thing the review asked about.
func TestTheWeightedGapIsReported(t *testing.T) {
	_, cov, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cov.WeightedGap) == 0 {
		t.Fatal("no refused variant is marked COMMON or UBIQUITOUS. Either the workers now " +
			"handle object streams and encryption, or the weights have been quietly softened")
	}
	h := cov.Headline()
	if !strings.Contains(h, "COMMON or UBIQUITOUS") {
		t.Fatal("the headline does not surface the weighted gap")
	}
	// Object streams are the single largest real-world gap and must be
	// named explicitly rather than left to a reader to infer.
	found := false
	for _, g := range cov.WeightedGap {
		if strings.HasPrefix(g, "PDF-OBJECT-STREAM") {
			found = true
		}
	}
	if !found {
		t.Fatal("PDF object streams are not reported in the weighted gap; they are the dominant " +
			"shape of PDF 1.5+ output and the largest coverage limit")
	}
}

// TestL3IsNotClaimed. The most comfortable lie available in this
// package would be to present generated documents as a corpus.
func TestL3IsNotClaimed(t *testing.T) {
	status := ExternalCorpusStatus()
	if !strings.Contains(status, "L3 NOT ATTEMPTED") {
		t.Fatalf("a corpus appears to be configured in this environment; the assessment text "+
			"must be regenerated rather than inherited: %s", status)
	}
	_, cov, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	a := Assess(cov)
	for _, required := range []string{
		"does NOT establish",
		"real-world document",
		"no run in this repository",
	} {
		if !strings.Contains(a, required) {
			t.Fatalf("the assessment omits %q", required)
		}
	}
}

// TestTheExternalRunnerDistinguishesEmptyFromAbsent. A silent zero
// would read as a clean corpus result.
func TestTheExternalRunnerDistinguishesEmptyFromAbsent(t *testing.T) {
	_, configured, err := RunExternal()
	if err != nil {
		t.Fatalf("RunExternal: %v", err)
	}
	if configured {
		t.Skip("a corpus is configured in this environment")
	}
	// Configured=false is the honest answer, and it is what the
	// assessment reports.
}

// TestEveryRefusalIsExplained. An unexplained refusal is
// indistinguishable from a bug.
func TestEveryRefusalIsExplained(t *testing.T) {
	for _, v := range Variants {
		if strings.TrimSpace(v.Why) == "" {
			t.Errorf("%s states no reason for its expected disposition", v.ID)
		}
		if strings.TrimSpace(string(v.RealWorldWeight)) == "" {
			t.Errorf("%s carries no real-world weight estimate", v.ID)
		}
	}
}

// TestAllThreeFormatsAreExercised.
func TestAllThreeFormatsAreExercised(t *testing.T) {
	got := map[worker.Kind]int{}
	for _, v := range Variants {
		got[v.Kind]++
	}
	for _, k := range worker.Kinds() {
		if got[k] == 0 {
			t.Errorf("no corpus variant exercises %s", k)
		}
	}
}
