// Package corpus is the Article 18 corpus qualification architecture.
//
// # What the review asked for, and what is honestly possible here
//
// The review set out a five-level assurance ladder for Article 18:
//
//	L1  static correctness    is the algorithm right?
//	L2  fixture correctness   do the fixtures represent real structure?
//	L3  corpus qualification  does it hold over a real document corpus?
//	L4  independent           does an outside party fail to recover?
//	L5  production evidence   does it stay safe after deployment?
//
// and made the point that matters most:
//
//	PDF A -> supported -> verified
//	PDF B -> unsupported -> rejected
//	PDF C -> unsupported -> rejected
//	PDF D -> unsupported -> rejected
//
//	Ini aman. Tetapi belum membuktikan VERIQO dapat menangani
//	real-world PDF population. Karena mungkin 60-80% dokumen
//	real-world masuk ke B/C/D.
//
// That is exactly right, and it is the reason this package computes a
// COVERAGE RATIO rather than a pass rate. A pass rate over a corpus the
// worker mostly refuses reads as 100% and means nothing. The number
// that matters is: of the documents presented, what fraction could the
// worker actually redact?
//
// # What this package is
//
// It is the corpus test ARCHITECTURE: a taxonomy of real-world
// structural variants, a generator that builds each variant as a real
// container, a runner that measures accept/refuse per variant, and a
// harness that reads an EXTERNAL corpus from disk when one is provided.
//
// It is not a corpus. Ten thousand real PDFs cannot be synthesised, and
// a generated document is by construction one this codebase already
// understands -- which is precisely the bias L3 exists to escape. So:
//
//	L2  reached, and this package is what reaches it: the variants are
//	    structurally real, not toy fixtures, and the ratio is measured
//	    rather than assumed.
//	L3  NOT reached. Requires a corpus of documents VERIQO did not
//	    create. VERIQO_CORPUS_DIR points the same runner at one when it
//	    exists; until then the runner reports that it found none.
//
// Claiming L3 from generated documents would be the most comfortable
// lie available in this whole engagement, because the numbers would
// look excellent.
package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"veriqo/pkg/evidence/redaction/worker"
)

// Variant is one structural feature a real-world document may carry.
//
// The list is the review's, plus what the workers already refuse. Each
// entry states whether VERIQO's workers are EXPECTED to handle it, so
// that the runner distinguishes a known limit from a regression: a
// variant that flips from accepted to refused is a defect, and one that
// was always refused is a gap.
type Variant struct {
	// ID is stable and citable from the assurance matrix.
	ID string
	// Kind is the format it applies to.
	Kind worker.Kind
	// Feature names the structural property.
	Feature string
	// Expected is what the worker should do. This is a statement of the
	// design, not of the outcome; the runner compares them.
	Expected Disposition
	// Why explains the expectation, especially every refusal, because
	// an unexplained refusal is indistinguishable from a bug.
	Why string
	// TermUnreadable marks a variant whose forbidden term this
	// codebase cannot see even in the inspectable view -- because the
	// document is encrypted, uses a filter we do not decode, or is
	// structurally broken.
	//
	// It is not a convenience for the tests. It is the honest statement
	// of the coverage limit: for these documents VERIQO cannot tell
	// whether a term is present, which is precisely why the worker
	// must refuse rather than report absence.
	TermUnreadable bool
	// RealWorldWeight is a coarse judgement of how common the feature
	// is in real document populations. It is NOT measured -- it is an
	// estimate, and it is labelled as one everywhere it is reported --
	// but omitting it entirely would leave a reader unable to tell a
	// rare structure from a ubiquitous one.
	RealWorldWeight Weight
}

// Disposition is what a worker does with a document.
type Disposition string

const (
	// Accepted: the worker produced a verified derivative.
	Accepted Disposition = "ACCEPTED"
	// Refused: the worker declined, safely and by design.
	Refused Disposition = "REFUSED"
	// Failed: the worker errored in a way that is neither a clean
	// acceptance nor a designed refusal. This is always a defect.
	Failed Disposition = "FAILED"
)

// Weight is an estimate of a feature's prevalence in real documents.
type Weight string

const (
	Ubiquitous Weight = "UBIQUITOUS" // present in most documents
	Common     Weight = "COMMON"     // present in a large minority
	Occasional Weight = "OCCASIONAL" // present sometimes
	Rare       Weight = "RARE"       // present in specialised populations
)

// Variants is the taxonomy. Every entry is generated as a real
// container by the builders in generate.go.
var Variants = []Variant{
	// --- OOXML, accepted -------------------------------------------
	{ID: "XLSX-SHARED-STRINGS", Kind: worker.KindXLSX, Feature: "cell text in the shared string table",
		Expected: Accepted, RealWorldWeight: Ubiquitous,
		Why: "where Excel actually stores cell text; a worker that missed it would redact nothing"},
	{ID: "XLSX-INLINE-STRINGS", Kind: worker.KindXLSX, Feature: "cell text inline in the sheet",
		Expected: Accepted, RealWorldWeight: Common,
		Why: "the other place text lives, used by streaming writers"},
	{ID: "XLSX-ESCAPED-XML", Kind: worker.KindXLSX, Feature: "a term containing XML-escapable characters",
		Expected: Accepted, RealWorldWeight: Common,
		Why: "R&D Partners is stored as R&amp;D Partners; a worker matching only the raw form leaves it"},
	{ID: "XLSX-UNICODE", Kind: worker.KindXLSX, Feature: "non-ASCII text",
		Expected: Accepted, RealWorldWeight: Ubiquitous,
		Why: "names outside ASCII are the normal case, not an edge case"},
	{ID: "XLSX-MIXED-CASE", Kind: worker.KindXLSX, Feature: "the term in several cases",
		Expected: Accepted, RealWorldWeight: Ubiquitous,
		Why: "the verifier checks a case-folded encoding, so the worker must too"},
	{ID: "XLSX-HIDDEN-SHEET", Kind: worker.KindXLSX, Feature: "the term on a hidden sheet",
		Expected: Accepted, RealWorldWeight: Occasional,
		Why: "hidden is a display property; the bytes are in the package like any other sheet"},
	{ID: "XLSX-COMMENTS", Kind: worker.KindXLSX, Feature: "the term in a cell comment",
		Expected: Accepted, RealWorldWeight: Common,
		Why: "comments are XML parts and are rewritten like any other"},
	{ID: "XLSX-DOCPROPS", Kind: worker.KindXLSX, Feature: "the term in document metadata",
		Expected: Accepted, RealWorldWeight: Common,
		Why: "metadata survives every visual redaction and is where a name most often escapes"},
	{ID: "PPTX-SLIDE-TEXT", Kind: worker.KindPPTX, Feature: "slide body text",
		Expected: Accepted, RealWorldWeight: Ubiquitous, Why: "the ordinary case"},
	{ID: "PPTX-SPEAKER-NOTES", Kind: worker.KindPPTX, Feature: "the term in speaker notes",
		Expected: Accepted, RealWorldWeight: Common,
		Why: "notes are a separate part that a slide-only redactor would miss entirely"},

	// --- OOXML, refused ---------------------------------------------
	{ID: "PPTX-EMBEDDED-IMAGE", Kind: worker.KindPPTX, Feature: "the term inside an embedded image",
		Expected: Refused, RealWorldWeight: Common,
		Why: "the worker cannot read pixels; releasing the package would claim absence for content never examined"},
	{ID: "XLSX-EMBEDDED-OBJECT", Kind: worker.KindXLSX, Feature: "the term inside an embedded binary object",
		Expected: Refused, RealWorldWeight: Occasional,
		Why: "an OLE or nested package the worker does not parse"},

	// --- PDF, accepted -----------------------------------------------
	{ID: "PDF-FLATE-CONTENT", Kind: worker.KindPDF, Feature: "text in a FlateDecode content stream",
		Expected: Accepted, RealWorldWeight: Ubiquitous, Why: "the standard shape of a modern PDF page"},
	{ID: "PDF-UNCOMPRESSED", Kind: worker.KindPDF, Feature: "text in an uncompressed content stream",
		Expected: Accepted, RealWorldWeight: Occasional, Why: "produced by simple generators"},
	{ID: "PDF-METADATA", Kind: worker.KindPDF, Feature: "the term in the document information dictionary",
		Expected: Accepted, RealWorldWeight: Common,
		Why: "/Title and /Author survive a visual redaction and are trivially recoverable"},

	// --- PDF, refused -------------------------------------------------
	{ID: "PDF-ENCRYPTED", Kind: worker.KindPDF, Feature: "an encrypted document",
		Expected: Refused, RealWorldWeight: Common,
		Why: "the worker cannot read the streams, so it must not report them clean"},
	{ID: "PDF-INCREMENTAL", Kind: worker.KindPDF, Feature: "incremental updates",
		Expected: Refused, RealWorldWeight: Common,
		Why: "earlier revisions remain in the file: a term redacted in the latest is recoverable from a previous one"},
	{ID: "PDF-OBJECT-STREAM", Kind: worker.KindPDF, Feature: "objects compressed inside an object stream",
		Expected: Refused, RealWorldWeight: Ubiquitous,
		Why: "the dominant shape of PDF 1.5+ output; this is the single largest coverage gap and it is not hidden"},
	{ID: "PDF-XREF-STREAM", Kind: worker.KindPDF, Feature: "a cross-reference stream rather than a table",
		Expected: Refused, RealWorldWeight: Ubiquitous,
		Why: "travels with object streams in PDF 1.5+; same gap"},
	{ID: "PDF-LZW", Kind: worker.KindPDF, Feature: "an LZWDecode stream filter",
		Expected: Refused, RealWorldWeight: Rare, TermUnreadable: true,
		Why: "a filter the worker does not decode, so the term is invisible even to the inspector"},
	{ID: "PDF-ANNOTATION", Kind: worker.KindPDF, Feature: "the term in an annotation",
		Expected: Accepted, RealWorldWeight: Common,
		Why: "annotation dictionaries are rewritten with the rest of the object"},
	{ID: "PDF-ATTACHMENT", Kind: worker.KindPDF, Feature: "the term in an embedded file attachment",
		Expected: Accepted, RealWorldWeight: Occasional,
		Why: "the worker rewrites an embedded file's stream bytes like any other stream, so a TEXTUAL " +
			"attachment is redacted. It does not parse the attachment as a document, so a nested " +
			"format with its own compression or encryption is covered only by the filter refusals above -- " +
			"a real limit, and a narrower one than refusing every attachment would suggest"},
	{ID: "PDF-MALFORMED", Kind: worker.KindPDF, Feature: "a structurally malformed document",
		Expected: Refused, RealWorldWeight: Occasional, TermUnreadable: true,
		Why: "a file the worker cannot parse must be refused, never partially processed"},
}

// Outcome is what happened for one variant.
type Outcome struct {
	Variant  Variant
	Actual   Disposition
	Detail   string
	Matches  bool
	Leaked   bool
	LeakNote string
}

// Coverage is the honest headline: not a pass rate, a coverage ratio.
type Coverage struct {
	Total    int
	Accepted int
	Refused  int
	Failed   int
	// Mismatched counts variants whose actual disposition differed
	// from the design. Any non-zero value is a defect or a stale
	// expectation, never a tuning parameter.
	Mismatched []string
	// Leaked counts variants where a forbidden term survived into a
	// RELEASED derivative. This must always be zero; a non-zero value
	// is the worst outcome the whole article can produce.
	Leaked []string
	// WeightedGap names the refused variants that are common or
	// ubiquitous in real documents. This is the number the review
	// asked about: the share of the real population VERIQO cannot
	// process.
	WeightedGap []string
}

// AcceptRatio is the fraction of variants the workers could actually
// redact. It is deliberately the headline instead of a pass rate.
func (c Coverage) AcceptRatio() float64 {
	if c.Total == 0 {
		return 0
	}
	return float64(c.Accepted) / float64(c.Total)
}

// Headline states the result in a form that cannot be quoted
// misleadingly.
func (c Coverage) Headline() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d structural variants: %d accepted, %d refused by design, %d failed. ",
		c.Total, c.Accepted, c.Refused, c.Failed)
	fmt.Fprintf(&b, "Coverage ratio %.0f%% -- this is the share of variants the workers can redact, ",
		c.AcceptRatio()*100)
	b.WriteString("NOT a pass rate. Every refusal is safe and none of them is capability.")
	if len(c.WeightedGap) > 0 {
		fmt.Fprintf(&b, "\n%d refused variant(s) are COMMON or UBIQUITOUS in real documents: %s. ",
			len(c.WeightedGap), strings.Join(c.WeightedGap, ", "))
		b.WriteString("A real-world population would land there in bulk.")
	}
	if len(c.Leaked) > 0 {
		fmt.Fprintf(&b, "\nLEAK: %s. A forbidden term survived into a released derivative.",
			strings.Join(c.Leaked, ", "))
	}
	return b.String()
}

// Report renders the full grid.
func (c Coverage) Report(outcomes []Outcome) string {
	var b strings.Builder
	b.WriteString("VERIQO Article 18 corpus qualification -- L2 (fixture correctness)\n")
	b.WriteString("L3 requires documents VERIQO did not create. See ExternalCorpus.\n\n")
	sorted := append([]Outcome(nil), outcomes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Variant.ID < sorted[j].Variant.ID })
	for _, o := range sorted {
		mark := "  "
		if !o.Matches {
			mark = "!!"
		}
		fmt.Fprintf(&b, "%s %-22s %-6s %-9s %-11s %s\n", mark, o.Variant.ID, o.Variant.Kind,
			o.Actual, o.Variant.RealWorldWeight, o.Variant.Feature)
		if !o.Matches {
			fmt.Fprintf(&b, "     expected %s: %s\n", o.Variant.Expected, o.Detail)
		}
	}
	b.WriteString("\n")
	b.WriteString(c.Headline())
	b.WriteString("\n")
	return b.String()
}

// ExternalCorpusDir is the environment variable that points the same
// runner at a real corpus.
//
// It exists so that reaching L3 is a matter of supplying documents
// rather than of writing new code -- and so that the absence of a
// corpus is visible as an empty result rather than as an unwritten
// feature.
const ExternalCorpusDir = "VERIQO_CORPUS_DIR"

// ExternalCorpus reads documents from the directory named by
// VERIQO_CORPUS_DIR, if it is set.
//
// It returns (nil, false) when no corpus is configured, which is the
// state this repository is in and which the report states plainly.
func ExternalCorpus() ([]string, bool) {
	dir := strings.TrimSpace(os.Getenv(ExternalCorpusDir))
	if dir == "" {
		return nil, false
	}
	var found []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".pdf", ".xlsx", ".pptx":
			found = append(found, p)
		}
		return nil
	})
	sort.Strings(found)
	return found, true
}

// KindOf maps a file extension onto a worker kind.
func KindOf(path string) (worker.Kind, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return worker.KindPDF, true
	case ".xlsx":
		return worker.KindXLSX, true
	case ".pptx":
		return worker.KindPPTX, true
	}
	return "", false
}
