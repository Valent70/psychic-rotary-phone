package worker

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"veriqo/pkg/evidence/redaction"
)

const secret = "Acme Holdings Ltd"

func run(t *testing.T, kind Kind, original []byte, terms ...string) (Release, error) {
	t.Helper()
	p := NewPipeline()
	return p.Run(Request{
		Kind:                kind,
		Original:            original,
		OriginalVersionID:   "EV-1",
		DerivativeVersionID: "EV-1-R1",
		PinnedOriginalHash:  redaction.Hash(original),
		ForbiddenTerms:      terms,
	})
}

// --- the trap this package exists to avoid ---------------------------

// TestCompressionWouldHaveHiddenTheTerm is the most important test in
// this package. It proves that the forbidden term is genuinely absent
// from the raw container bytes of the ORIGINAL -- meaning a pipeline
// that verified the container instead of the decompressed view would
// report success on a document where nothing had been removed.
//
// If this test ever fails, it means the fixture stopped compressing,
// and every other test here becomes weaker without saying so.
func TestCompressionWouldHaveHiddenTheTerm(t *testing.T) {
	for _, tc := range []struct {
		kind     Kind
		original []byte
	}{
		{KindXLSX, buildXLSX(t, []string{secret, "Ordinary text"})},
		{KindPPTX, buildPPTX(t, []string{secret})},
		{KindPDF, buildPDF(t, secret)},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			if bytes.Contains(tc.original, []byte(secret)) {
				t.Fatalf("%s fixture stores the term uncompressed: this test no longer proves anything", tc.kind)
			}
			view, err := Inspectable(tc.kind, tc.original)
			if err != nil {
				t.Fatalf("Inspectable: %v", err)
			}
			if !bytes.Contains(view, []byte(secret)) {
				t.Fatalf("the inspectable view of the %s original does not contain the term: "+
					"verification would pass vacuously", tc.kind)
			}
		})
	}
}

// --- the happy path, per format --------------------------------------

func TestEachWorkerProducesAVerifiedDerivative(t *testing.T) {
	for _, tc := range []struct {
		kind     Kind
		original []byte
	}{
		{KindXLSX, buildXLSX(t, []string{secret, "Ordinary text", "Another " + secret + " mention"})},
		{KindPPTX, buildPPTX(t, []string{secret, "A slide about " + secret})},
		{KindPDF, buildPDF(t, "Claim submitted by "+secret+" on 3 March")},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			rel, err := run(t, tc.kind, tc.original, secret)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			view, err := Inspectable(tc.kind, rel.Derivative())
			if err != nil {
				t.Fatalf("Inspectable(derivative): %v", err)
			}
			if bytes.Contains(bytes.ToLower(view), bytes.ToLower([]byte(secret))) {
				t.Fatal("the forbidden term survives in the derivative's decompressed content")
			}
			if !rel.Chain().Verified || !rel.Chain().Absent() {
				t.Fatalf("a released derivative must carry a verified chain: %s", rel.Chain().Explain())
			}
			if rel.Chain().OriginalHash != redaction.Hash(tc.original) {
				t.Fatal("the chain does not pin the original's hash")
			}
			if rel.Chain().DerivativeHash == rel.Chain().OriginalHash {
				t.Fatal("the derivative has the original's hash: it is not a new version")
			}
			if len(rel.Manifest().PartsModified) == 0 {
				t.Fatal("the manifest records no modified part")
			}
			if rel.Manifest().Replacements[secret] == 0 {
				t.Fatal("the manifest counts no replacement")
			}
		})
	}
}

// TestTheOriginalIsNeverModified is Article 17 as a test rather than a
// promise.
func TestTheOriginalIsNeverModified(t *testing.T) {
	original := buildXLSX(t, []string{secret, "Ordinary text"})
	before := redaction.Hash(original)
	keep := append([]byte(nil), original...)
	if _, err := run(t, KindXLSX, original, secret); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if redaction.Hash(original) != before || !bytes.Equal(original, keep) {
		t.Fatal("the worker modified the original in place")
	}
}

// TestTheDerivativeIsDeterministic proves the derivative's hash is a
// property of its content and not of the clock. Without this, two
// redactions of the same document produce different evidence versions
// and neither can be reproduced by a third party.
func TestTheDerivativeIsDeterministic(t *testing.T) {
	original := buildPPTX(t, []string{secret, "A second slide"})
	a, err := run(t, KindPPTX, original, secret)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	b, err := run(t, KindPPTX, original, secret)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !bytes.Equal(a.Derivative(), b.Derivative()) {
		t.Fatal("two redactions of the same original produced different bytes")
	}
}

// --- fail-closed ------------------------------------------------------

// TestAnEncryptedPDFIsRefusedNotWarned is the VERIFY_FAIL -> NO_RELEASE
// rule. The worker cannot read the content, so it must not report the
// content as clean.
func TestAnEncryptedPDFIsRefusedNotWarned(t *testing.T) {
	original := buildPDF(t, "Claim by "+secret)
	original = bytes.Replace(original, []byte("/Size 6"), []byte("/Size 6 /Encrypt 9 0 R"), 1)
	_, err := run(t, KindPDF, original, secret)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want ErrRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("the refusal must say why: %v", err)
	}
}

// TestAnIncrementallyUpdatedPDFIsRefused. A term redacted in the latest
// revision is still recoverable from the previous one, which is exactly
// the "hidden content unrecoverable" requirement failing.
func TestAnIncrementallyUpdatedPDFIsRefused(t *testing.T) {
	original := buildPDF(t, "Claim by "+secret)
	original = append(original, []byte("\n6 0 obj\n<< >>\nendobj\ntrailer\n<< /Size 7 >>\nstartxref\n0\n%%EOF\n")...)
	_, err := run(t, KindPDF, original, secret)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want ErrRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), "incremental") {
		t.Fatalf("the refusal must name the incremental update: %v", err)
	}
}

// TestAPDFWithAFilterTheWorkerCannotDecodeIsRefused.
func TestAPDFWithAFilterTheWorkerCannotDecodeIsRefused(t *testing.T) {
	original := buildPDF(t, "Claim by "+secret)
	original = bytes.Replace(original, []byte("/Filter /FlateDecode"), []byte("/Filter /LZWDecode"), 1)
	_, err := run(t, KindPDF, original, secret)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want ErrRefused, got %v", err)
	}
}

// TestABinaryAttachmentCarryingTheTermIsRefused. The worker cannot
// redact a PNG. Releasing the package while claiming absence would be a
// false statement about content it never examined.
func TestABinaryAttachmentCarryingTheTermIsRefused(t *testing.T) {
	original := buildPPTXWithImage(t, "A slide about "+secret, "PNG\x00\x00"+secret+"\x00")
	_, err := run(t, KindPPTX, original, secret)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want ErrRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), "image1.png") {
		t.Fatalf("the refusal must name the part: %v", err)
	}
}

// TestAWorkerThatChangedNothingIsRefused. A document with nothing
// removed must not acquire a redaction provenance record.
func TestAWorkerThatChangedNothingIsRefused(t *testing.T) {
	original := buildXLSX(t, []string{"Nothing sensitive here", "Ordinary text"})
	_, err := run(t, KindXLSX, original, secret)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want ErrRefused for a no-op redaction, got %v", err)
	}
}

// TestAChangedOriginalIsRefused proves the pinned hash is load-bearing.
func TestAChangedOriginalIsRefused(t *testing.T) {
	original := buildXLSX(t, []string{secret})
	p := NewPipeline()
	_, err := p.Run(Request{
		Kind: KindXLSX, Original: original,
		OriginalVersionID: "EV-1", DerivativeVersionID: "EV-1-R1",
		PinnedOriginalHash: redaction.Hash([]byte("some other document")),
		ForbiddenTerms:     []string{secret},
	})
	if !errors.Is(err, ErrOriginalChanged) {
		t.Fatalf("want ErrOriginalChanged, got %v", err)
	}
}

// TestTheDerivativeMustBeANewVersion.
func TestTheDerivativeMustBeANewVersion(t *testing.T) {
	original := buildXLSX(t, []string{secret})
	p := NewPipeline()
	_, err := p.Run(Request{
		Kind: KindXLSX, Original: original,
		OriginalVersionID: "EV-1", DerivativeVersionID: "EV-1",
		ForbiddenTerms: []string{secret},
	})
	if err == nil || !strings.Contains(err.Error(), "new version") {
		t.Fatalf("want a new-version refusal, got %v", err)
	}
}

// TestARedactionWithNoTermsIsRefused. A copy is not a redaction.
func TestARedactionWithNoTermsIsRefused(t *testing.T) {
	if _, err := run(t, KindXLSX, buildXLSX(t, []string{secret})); !errors.Is(err, ErrNoTerms) {
		t.Fatalf("want ErrNoTerms, got %v", err)
	}
}

// TestAMarkerContainingTheTermIsRefused. Replacing "Acme" with
// "[REDACTED: Acme]" removes nothing.
func TestAMarkerContainingTheTermIsRefused(t *testing.T) {
	p := NewPipeline()
	p.Marker = "[REDACTED: " + secret + "]"
	original := buildXLSX(t, []string{secret})
	_, err := p.Run(Request{
		Kind: KindXLSX, Original: original,
		OriginalVersionID: "EV-1", DerivativeVersionID: "EV-1-R1",
		ForbiddenTerms: []string{secret},
	})
	if err == nil || !strings.Contains(err.Error(), "marker contains") {
		t.Fatalf("want a marker refusal, got %v", err)
	}
}

// TestAnUnsupportedFormatIsRefusedNotPassedThrough. The dangerous
// failure would be treating an unknown format as bytes to copy.
func TestAnUnsupportedFormatIsRefusedNotPassedThrough(t *testing.T) {
	_, err := run(t, Kind("DOCX"), []byte("anything"), secret)
	if !errors.Is(err, ErrUnsupportedKind) {
		t.Fatalf("want ErrUnsupportedKind, got %v", err)
	}
}

// --- case and escaping ------------------------------------------------

// TestCaseVariantsAreRemoved. A redaction that removes "Acme Holdings
// Ltd" and leaves "ACME HOLDINGS LTD" has removed nothing; the verifier
// checks a case-folded encoding, so a worker doing less would fail its
// own pipeline.
func TestCaseVariantsAreRemoved(t *testing.T) {
	original := buildXLSX(t, []string{
		strings.ToUpper(secret), strings.ToLower(secret), secret,
	})
	rel, err := run(t, KindXLSX, original, secret)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	view, err := Inspectable(KindXLSX, rel.Derivative())
	if err != nil {
		t.Fatalf("Inspectable: %v", err)
	}
	if strings.Contains(strings.ToLower(string(view)), strings.ToLower(secret)) {
		t.Fatal("a case variant survived")
	}
}

// TestXMLEscapedFormsAreRemoved. A name with an ampersand appears in
// the XML as "R&amp;D", and a worker searching only for "R&D" would
// leave it while reporting success.
func TestXMLEscapedFormsAreRemoved(t *testing.T) {
	const term = "R&D Partners"
	original := buildXLSX(t, []string{"Report by R&amp;D Partners", "Ordinary text"})
	rel, err := run(t, KindXLSX, original, term)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	view, err := Inspectable(KindXLSX, rel.Derivative())
	if err != nil {
		t.Fatalf("Inspectable: %v", err)
	}
	for _, form := range []string{"R&D Partners", "R&amp;D Partners"} {
		if strings.Contains(string(view), form) {
			t.Fatalf("the escaped form %q survived", form)
		}
	}
}

// --- the ledger event -------------------------------------------------

// TestEveryReleaseCarriesADisclosureEvent is Article 24: every
// disclosure emits a ledger event.
func TestEveryReleaseCarriesADisclosureEvent(t *testing.T) {
	original := buildPPTX(t, []string{secret})
	rel, err := run(t, KindPPTX, original, secret)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	e := rel.LedgerEvent()
	if e.Action == "" || e.OriginalVersionID == "" || e.DerivativeVersionID == "" {
		t.Fatalf("incomplete disclosure event: %+v", e)
	}
	if e.OriginalHash == e.DerivativeHash {
		t.Fatal("the event records the same hash for both versions")
	}
	if e.EncodingsChecked != len(redaction.Encodings()) {
		t.Fatalf("the event records %d encodings checked, the verifier checks %d",
			e.EncodingsChecked, len(redaction.Encodings()))
	}
}

// TestTheChainStatesTheCompressionLimitation. A reader must be able to
// tell that absence was established over the decompressed view, since
// that is the scope of the claim.
func TestTheChainStatesTheCompressionLimitation(t *testing.T) {
	rel, err := run(t, KindXLSX, buildXLSX(t, []string{secret}), secret)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, l := range rel.Chain().Limitations {
		if strings.Contains(l, "decompressed inspectable view") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the chain does not state the compression limitation: %v", rel.Chain().Limitations)
	}
}

// TestKindsAreAllWired proves every declared format has a worker. A
// Kind constant with no redactor would fail only when somebody used it.
func TestKindsAreAllWired(t *testing.T) {
	p := NewPipeline()
	for _, k := range Kinds() {
		if _, ok := p.redactors[k]; !ok {
			t.Fatalf("Kind %s is declared but has no worker", k)
		}
	}
}

// TestTheRedactedPDFIsStructurallyValid checks that the rebuilt
// cross-reference table points at the objects it claims to.
//
// A redaction that removes the term by corrupting the file removes the
// evidence along with it. Every offset in the rebuilt xref must land on
// the "N 0 obj" it indexes, and startxref must land on the table.
func TestTheRedactedPDFIsStructurallyValid(t *testing.T) {
	original := buildPDF(t, "Claim submitted by "+secret)
	rel, err := run(t, KindPDF, original, secret)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	doc := rel.Derivative()

	sx := bytes.LastIndex(doc, []byte("startxref"))
	if sx < 0 {
		t.Fatal("the derivative has no startxref")
	}
	var xrefAt int
	if _, err := fmt.Sscanf(string(bytes.TrimSpace(doc[sx+len("startxref"):])), "%d", &xrefAt); err != nil {
		t.Fatalf("startxref is not a number: %v", err)
	}
	if xrefAt < 0 || xrefAt >= len(doc) {
		t.Fatalf("startxref points outside the file: %d of %d", xrefAt, len(doc))
	}
	if !bytes.HasPrefix(doc[xrefAt:], []byte("xref")) {
		t.Fatalf("startxref does not point at the cross-reference table, found %q", doc[xrefAt:min(xrefAt+16, len(doc))])
	}

	// Walk the table and check each in-use entry lands on its object.
	lines := bytes.Split(doc[xrefAt:], []byte("\n"))
	if len(lines) < 3 {
		t.Fatal("the cross-reference table is truncated")
	}
	var first, count int
	if _, err := fmt.Sscanf(string(lines[1]), "%d %d", &first, &count); err != nil {
		t.Fatalf("the subsection header is malformed: %q", lines[1])
	}
	checked := 0
	for i := 0; i < count && 2+i < len(lines); i++ {
		entry := lines[2+i]
		if len(entry) < 18 || entry[17] != 'n' {
			continue // free entry
		}
		var off int
		if _, err := fmt.Sscanf(string(entry[:10]), "%d", &off); err != nil {
			t.Fatalf("entry %d is malformed: %q", i, entry)
		}
		objNum := first + i
		want := []byte(fmt.Sprintf("%d 0 obj", objNum))
		if off <= 0 || off >= len(doc) || !bytes.HasPrefix(doc[off:], want) {
			t.Fatalf("xref entry for object %d points at offset %d, which is not %q", objNum, off, want)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no in-use cross-reference entry was checked: the test proves nothing")
	}
	if !bytes.HasSuffix(bytes.TrimSpace(doc), []byte("%%EOF")) {
		t.Fatal("the derivative does not end with an end-of-file marker")
	}
	if bytes.Count(doc, []byte("%%EOF")) != 1 {
		t.Fatal("the derivative has more than one end-of-file marker: it would be refused as incrementally updated")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
