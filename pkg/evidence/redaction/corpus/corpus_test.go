package corpus

import (
	"bytes"
	"strings"
	"testing"

	"veriqo/pkg/evidence/redaction/worker"
)

// --- FC-005: the fixtures must be genuine -------------------------------

// TestEveryVariantBuildsARealContainerCarryingTheTerm is the positive
// test for FC-005.
//
// An earlier corpus wrote the forbidden term into an ordinary
// dictionary and NAMED the structure in a comment. Every test passed
// and nothing was exercised. So each fixture must actually build, and
// the term must actually be somewhere inside it -- either in the raw
// bytes, or recoverable from the inspectable view.
func TestEveryVariantBuildsARealContainerCarryingTheTerm(t *testing.T) {
	for _, v := range Variants {
		doc, err := Build(v)
		if err != nil {
			t.Errorf("%s: %v", v.ID, err)
			continue
		}
		if len(doc) == 0 {
			t.Errorf("%s: the fixture is empty", v.ID)
			continue
		}
		if bytes.Contains(bytes.ToLower(doc), bytes.ToLower([]byte(Term))) {
			continue // plainly present, which is fine for the plain variants
		}
		view, err := worker.Inspect(v.Kind, doc)
		if err != nil {
			// Encrypted, LZW and malformed fixtures are unreadable by
			// design; that is what TermUnreadable records.
			if v.TermUnreadable {
				continue
			}
			t.Errorf("%s: the fixture is neither plainly nor inspectably readable: %v", v.ID, err)
			continue
		}
		if !bytes.Contains(bytes.ToLower(view), bytes.ToLower([]byte(Term))) && !v.TermUnreadable {
			t.Errorf("%s: THE FIXTURE DOES NOT CARRY THE TERM. It exercises nothing, and any "+
				"redaction of it passes vacuously", v.ID)
		}
	}
}

// TestTheObjectStreamFixtureIsGenuine is the mutation test for FC-005,
// and it is the one that would have caught the original defect.
//
// If the fixture is real, the term is INSIDE a compressed container
// and is therefore NOT findable in the raw bytes. A downgraded fixture
// that merely writes the term into a plain dictionary fails here.
func TestTheObjectStreamFixtureIsGenuine(t *testing.T) {
	for _, id := range []string{"PDF-OBJECT-STREAM", "PDF-XREF-STREAM"} {
		v := variant(t, id)
		doc, err := Build(v)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if bytes.Contains(doc, []byte(Term)) {
			t.Fatalf("%s: THE FIXTURE IS NOT GENUINE. The term is findable in the raw bytes, "+
				"so the container was never built and the unpacking code is not exercised", id)
		}
		// And it must be genuinely recoverable once unpacked -- a
		// fixture that hides the term by not containing it would pass
		// the check above and test nothing.
		view, err := worker.Inspect(v.Kind, doc)
		if err != nil {
			t.Fatalf("%s: the fixture does not inspect: %v", id, err)
		}
		if !bytes.Contains(view, []byte(Term)) {
			t.Fatalf("%s: the term is not in the fixture at all", id)
		}
		if !bytes.Contains(doc, []byte("/Type /ObjStm")) &&
			!bytes.Contains(doc, []byte("/Type/ObjStm")) {
			t.Fatalf("%s: the fixture declares no object stream", id)
		}
	}
	// The cross-reference variant must carry a real binary xref stream.
	doc, _ := Build(variant(t, "PDF-XREF-STREAM"))
	if !bytes.Contains(doc, []byte("/W [1 4 2]")) {
		t.Fatal("PDF-XREF-STREAM: no /W entry width array; the cross-reference stream is not real")
	}
	if bytes.Contains(doc, []byte("\ntrailer")) {
		t.Fatal("PDF-XREF-STREAM: the fixture still has a classic trailer, so it is not a " +
			"cross-reference-stream document")
	}
}

// TestTheOOXMLVariantsAreGenuinelyCompressed is the negative test for
// FC-005: a zip fixture written with stored (uncompressed) entries
// would leave the term visible in the raw bytes and would not exercise
// the part-extraction path.
func TestTheOOXMLVariantsAreGenuinelyCompressed(t *testing.T) {
	checked := 0
	for _, v := range Variants {
		if v.Kind == worker.KindPDF {
			continue
		}
		doc, err := Build(v)
		if err != nil {
			t.Fatalf("%s: %v", v.ID, err)
		}
		if !bytes.HasPrefix(doc, []byte("PK")) {
			t.Errorf("%s: the fixture is not a zip container", v.ID)
			continue
		}
		if _, err := worker.Inspect(v.Kind, doc); err != nil {
			t.Errorf("%s: the OPC package does not open: %v", v.ID, err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no OOXML variant was checked; this test governs nothing")
	}
}

// TestTheUbiquitousPDF15StructuresAreNoLongerRefused is the regression
// test for FC-005: the two structures every PDF produced since Acrobat
// 6 uses by default must be handled, not refused.
func TestTheUbiquitousPDF15StructuresAreNoLongerRefused(t *testing.T) {
	outcomes, _, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outcomes {
		switch o.Variant.ID {
		case "PDF-OBJECT-STREAM", "PDF-XREF-STREAM":
			if o.Actual != Accepted {
				t.Errorf("%s is %s: %s", o.Variant.ID, o.Actual, o.Detail)
			}
			if o.Variant.RealWorldWeight != Ubiquitous {
				t.Errorf("%s is no longer weighted UBIQUITOUS; the reason it had to be handled "+
					"has been edited away", o.Variant.ID)
			}
		}
	}
}

// --- FC-002: verification must not be vacuous ---------------------------

// TestEachWorkerProducesAVerifiedDerivative is the positive test.
func TestEachWorkerProducesAVerifiedDerivative(t *testing.T) {
	seen := map[worker.Kind]bool{}
	for _, v := range Variants {
		if v.Expected != Accepted {
			continue
		}
		doc, err := Build(v)
		if err != nil {
			t.Fatalf("%s: %v", v.ID, err)
		}
		_, m, err := worker.Redact(v.Kind, doc, []string{Term})
		if err != nil {
			t.Errorf("%s: %v", v.ID, err)
			continue
		}
		if !m.Verified {
			t.Errorf("%s: a derivative was produced and never verified", v.ID)
		}
		if m.OriginalSHA256 == "" || m.DerivativeSHA256 == "" {
			t.Errorf("%s: the manifest does not carry both hashes", v.ID)
		}
		if m.OriginalSHA256 == m.DerivativeSHA256 {
			t.Errorf("%s: the derivative is byte-identical to the original", v.ID)
		}
		seen[v.Kind] = true
	}
	for _, k := range []worker.Kind{worker.KindPDF, worker.KindXLSX, worker.KindPPTX} {
		if !seen[k] {
			t.Errorf("no %s variant was verified; that worker is untested here", k)
		}
	}
}

// TestCompressionWouldHaveHiddenTheTerm is the negative test, and it is
// the one that states FC-002 as a demonstration rather than a warning.
//
// It proves that a byte search over the DERIVATIVE would pass on a
// document that still contains the term -- which is exactly what the
// original defect did.
func TestCompressionWouldHaveHiddenTheTerm(t *testing.T) {
	v := variant(t, "PDF-FLATE-CONTENT")
	doc, err := Build(v)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture carries the term inside a Flate stream.
	if bytes.Contains(doc, []byte(Term)) {
		t.Fatal("the fixture's term is not compressed; this test would not demonstrate anything")
	}
	// A naive byte search over the ORIGINAL therefore reports it clean.
	// That is the vacuous verification.
	view, err := worker.Inspect(v.Kind, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(view, []byte(Term)) {
		t.Fatal("the inspectable view does not find the term either; the demonstration fails")
	}
	// The two disagree, and only one of them is verification.
}

// TestNoVariantLeaks is the mutation test: the strongest statement the
// corpus can make.
func TestNoVariantLeaks(t *testing.T) {
	outcomes, cov, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(cov.Leaked) > 0 {
		t.Fatalf("MUTANT SURVIVED: a forbidden term reached a released derivative in: %s",
			strings.Join(cov.Leaked, ", "))
	}
	for _, o := range outcomes {
		if o.Leaked {
			t.Errorf("MUTANT SURVIVED: %s leaked: %s", o.Variant.ID, o.LeakNote)
		}
	}
	if cov.Failed != 0 {
		t.Fatalf("%d variant(s) FAILED, which is always a defect: %v", cov.Failed, cov.Mismatched)
	}
	if len(cov.Mismatched) > 0 {
		t.Fatalf("variants diverged from their stated design: %v", cov.Mismatched)
	}
}

// TestTheChainStatesTheCompressionLimitation is the regression test:
// the manifest must SAY what its verification does not cover.
func TestTheChainStatesTheCompressionLimitation(t *testing.T) {
	doc, err := Build(variant(t, "PDF-FLATE-CONTENT"))
	if err != nil {
		t.Fatal(err)
	}
	_, m, err := worker.Redact(worker.KindPDF, doc, []string{Term})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Limits) == 0 {
		t.Fatal("the manifest states no limits; ADEQUATE with no limits reads as complete")
	}
	joined := strings.ToLower(strings.Join(m.Limits, " "))
	if !strings.Contains(joined, "inspectable view") {
		t.Fatalf("the manifest does not state what its verification covers: %v", m.Limits)
	}
}

// --- FC-008: coverage is not a pass rate --------------------------------

// TestTheCoverageRatioIsReportedAsCoverageNotAsAPassRate.
func TestTheCoverageRatioIsReportedAsCoverageNotAsAPassRate(t *testing.T) {
	_, cov, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	h := cov.Headline()
	if !strings.Contains(h, "NOT a pass rate") {
		t.Fatalf("the headline does not disclaim the pass-rate reading:\n%s", h)
	}
	if !strings.Contains(h, "refused by design") {
		t.Fatalf("the headline does not separate refusals from failures:\n%s", h)
	}
	// The arithmetic must actually be coverage, not passes: refusals
	// are excluded from the numerator.
	if cov.Refused == 0 {
		t.Fatal("no variant is refused; this test can no longer distinguish the two readings")
	}
	want := float64(cov.Accepted) / float64(cov.Total)
	if cov.AcceptRatio() != want {
		t.Fatalf("AcceptRatio = %v, want %v", cov.AcceptRatio(), want)
	}
	if cov.AcceptRatio() >= 1.0 {
		t.Fatal("the ratio reports full coverage while variants are refused")
	}
}

// TestTheWeightedGapIsReported is the negative test: the refusals that
// are common in real documents must be NAMED, not absorbed into a
// percentage.
func TestTheWeightedGapIsReported(t *testing.T) {
	_, cov, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(cov.WeightedGap) == 0 {
		t.Fatal("no weighted gap is reported; either every common structure is handled " +
			"(and this test should be deleted deliberately) or the gap is being hidden")
	}
	h := cov.Headline()
	for _, g := range cov.WeightedGap {
		id := strings.Split(g, " ")[0]
		if !strings.Contains(h, id) {
			t.Errorf("the headline does not name the gap %s", id)
		}
	}
	if !strings.Contains(h, "COMMON or UBIQUITOUS") {
		t.Fatalf("the headline does not say why these gaps matter:\n%s", h)
	}
}

// TestWeightedCoverageIsReportedAsAnEstimate is the regression test.
// The weights are VERIQO's judgement, not a measurement, and the word
// must travel with the number.
func TestWeightedCoverageIsReportedAsAnEstimate(t *testing.T) {
	_, cov, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	h := cov.Headline()
	if !strings.Contains(h, "ESTIMATE") {
		t.Fatalf("the weighted figure travels without the word ESTIMATE:\n%s", h)
	}
	if !strings.Contains(h, PrevalenceBasis) {
		t.Fatal("the headline omits the basis on which the weights were chosen")
	}
	if !strings.Contains(PrevalenceBasis, "not measurements") {
		t.Fatal("the prevalence basis no longer says the weights are not measurements")
	}
	// And the level statement must say no outside document was used.
	if Level() == L2 && !strings.Contains(LevelStatement(), "outside VERIQO") {
		t.Fatalf("the level statement does not disclose that every fixture is VERIQO's own:\n%s",
			LevelStatement())
	}
}

// --- FC-010: irreversibility must not be overclaimed --------------------

// TestXMLEscapedFormsAreRemoved is the positive test. A term written
// as "A&amp;B" is the same term to a reader and a different byte
// string to a naive search.
func TestXMLEscapedFormsAreRemoved(t *testing.T) {
	v := variant(t, "XLSX-ESCAPED-XML")
	doc, err := Build(v)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := worker.Redact(v.Kind, doc, []string{Term})
	if err != nil {
		t.Fatal(err)
	}
	view, err := worker.Inspect(v.Kind, out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(view), bytes.ToLower([]byte(Term))) {
		t.Fatal("the term survived in a document that carried XML-escapable characters")
	}
	// The unescaped view is searched too, so a term hidden by escaping
	// is caught rather than reported clean.
	if bytes.Contains(view, []byte("&amp;"+Term)) {
		t.Fatal("an escaped occurrence survived")
	}
}

// TestRedactionThatStripsOneEncodingOnly is the negative test: a
// mixed-case document must not be redacted for one casing only.
func TestRedactionThatStripsOneEncodingOnly(t *testing.T) {
	v := variant(t, "XLSX-MIXED-CASE")
	doc, err := Build(v)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := worker.Redact(v.Kind, doc, []string{Term})
	if err != nil {
		t.Fatal(err)
	}
	view, err := worker.Inspect(v.Kind, out)
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range []string{Term, strings.ToLower(Term), strings.ToUpper(Term)} {
		if bytes.Contains(view, []byte(form)) {
			t.Errorf("the %q casing survived redaction", form)
		}
	}
}

// TestVisualOnlyRedactionPresentedAsIrreversible is the mutation test.
//
// It does not test the redactor. It tests what the redactor CLAIMS:
// this worker replaces text and does not prove a rasterised glyph is
// unrecoverable, and the manifest must say so. A manifest that stopped
// saying it would be the overclaim FC-010 records.
func TestVisualOnlyRedactionPresentedAsIrreversible(t *testing.T) {
	doc, err := Build(variant(t, "PDF-FLATE-CONTENT"))
	if err != nil {
		t.Fatal(err)
	}
	_, m, err := worker.Redact(worker.KindPDF, doc, []string{Term})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.ToLower(strings.Join(m.Limits, " "))
	if !strings.Contains(joined, "visual") {
		t.Fatalf("MUTANT SURVIVED: the manifest makes no statement about visual redaction, "+
			"so a reader may take the derivative as proven irrecoverable: %v", m.Limits)
	}
	for _, w := range []string{"irreversible", "unrecoverable", "guaranteed"} {
		for _, l := range m.Limits {
			if strings.Contains(strings.ToLower(l), w) &&
				!strings.Contains(strings.ToLower(l), "not "+w) &&
				!strings.Contains(strings.ToLower(l), "does not") {
				t.Errorf("MUTANT SURVIVED: the manifest claims %q: %s", w, l)
			}
		}
	}
}

// TestTheDerivativeIsDeterministic is the regression test.
//
// Two redactions of the same input must produce the same bytes. If
// they do not -- because a zip timestamp came from the clock, or a
// compression level changed -- then the derivative's hash is a
// function of when it was produced rather than of what it contains,
// and no two parties can confirm they hold the same document.
func TestTheDerivativeIsDeterministic(t *testing.T) {
	for _, v := range Variants {
		if v.Expected != Accepted {
			continue
		}
		doc, err := Build(v)
		if err != nil {
			t.Fatal(err)
		}
		first, m1, err := worker.Redact(v.Kind, doc, []string{Term})
		if err != nil {
			t.Fatalf("%s: %v", v.ID, err)
		}
		for i := 0; i < 3; i++ {
			again, m2, err := worker.Redact(v.Kind, doc, []string{Term})
			if err != nil {
				t.Fatalf("%s: %v", v.ID, err)
			}
			if !bytes.Equal(first, again) {
				t.Fatalf("%s: two redactions of the same input produced different bytes; "+
					"the derivative's hash is not a function of its content", v.ID)
			}
			if m1.DerivativeSHA256 != m2.DerivativeSHA256 {
				t.Fatalf("%s: the manifest hash varies between runs", v.ID)
			}
		}
	}
}

// --- helpers -------------------------------------------------------------

func variant(t *testing.T, id string) Variant {
	t.Helper()
	for _, v := range Variants {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("no variant %q; this test cites a variant that was removed", id)
	return Variant{}
}

// TestEveryVariantIsReachable prevents the corpus from silently
// shrinking: a variant removed from the list is a structure nobody
// tests any more.
func TestEveryVariantIsReachable(t *testing.T) {
	if len(Variants) < 23 {
		t.Fatalf("the corpus has shrunk to %d variants", len(Variants))
	}
	seen := map[string]bool{}
	for _, v := range Variants {
		if seen[v.ID] {
			t.Errorf("duplicate variant %s", v.ID)
		}
		seen[v.ID] = true
		if v.Why == "" {
			t.Errorf("%s states no reason for its expectation; an unexplained refusal is "+
				"indistinguishable from a bug", v.ID)
		}
		if v.RealWorldWeight == "" {
			t.Errorf("%s carries no prevalence weight", v.ID)
		}
	}
}
