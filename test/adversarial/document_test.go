package adversarial

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"veriqo/pkg/evidence/redaction/corpus"
	"veriqo/pkg/evidence/redaction/worker"
)

// The redaction worker is the place hostile bytes actually arrive:
// somebody else's file, produced by somebody else's tool, with an
// interest in what the derivative says. The rule the whole package
// turns on is that anything the worker cannot decode is REFUSED, never
// reported clean -- a document nobody could read is a document nobody
// can certify as redacted.

func terms() []string { return []string{corpus.Term} }

// TestUndecodableStructuresAreRefusedNotPassed. Each of these is a
// real document that a naive text-replacer would process without
// error and hand back with the term intact somewhere it did not look.
func TestUndecodableStructuresAreRefusedNotPassed(t *testing.T) {
	for _, name := range []string{
		"PDF-ENCRYPTED", "PDF-LZW", "PDF-INCREMENTAL", "PDF-MALFORMED",
	} {
		v, ok := variant(name)
		if !ok {
			t.Fatalf("the corpus no longer contains %s", name)
		}
		doc, err := corpus.Build(v)
		if err != nil {
			t.Fatalf("%s: build: %v", name, err)
		}
		out, m, err := worker.Redact(v.Kind, doc, terms())
		if err == nil {
			t.Fatalf("%s was processed rather than refused", name)
		}
		if !worker.IsRefusal(err) {
			t.Fatalf("%s failed instead of refusing: %v", name, err)
		}
		if out != nil {
			t.Fatalf("%s produced a derivative alongside its refusal", name)
		}
		if m.Verified {
			t.Fatalf("%s was marked verified", name)
		}
	}
}

func variant(name string) (corpus.Variant, bool) {
	for _, v := range corpus.Variants {
		if v.ID == name {
			return v, true
		}
	}
	return corpus.Variant{}, false
}

// TestARefusalNamesTheStructureAndTheReason. A refusal a caller
// cannot act on is an outage with better manners.
func TestARefusalNamesTheStructureAndTheReason(t *testing.T) {
	v, ok := variant("PDF-ENCRYPTED")
	if !ok {
		t.Fatal("fixture missing")
	}
	doc, err := corpus.Build(v)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = worker.Redact(v.Kind, doc, terms())
	var r *worker.Refusal
	if !asRefusal(err, &r) {
		t.Fatalf("not a refusal: %v", err)
	}
	if strings.TrimSpace(r.Structure) == "" || strings.TrimSpace(r.Reason) == "" {
		t.Fatalf("the refusal is empty: %+v", r)
	}
	if !strings.Contains(strings.ToLower(r.Error()), "encrypt") {
		t.Fatalf("the refusal does not name what it hit: %s", r.Error())
	}
}

func asRefusal(err error, out **worker.Refusal) bool {
	for err != nil {
		if r, ok := err.(*worker.Refusal); ok {
			*out = r
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestAnInjectedInstructionInsideADocumentIsRedactedAsText.
//
// The injected payload is not special. It is bytes in a part, and the
// worker treats it as bytes: if it carries a term it is replaced, and
// nothing in it changes what the worker does.
func TestAnInjectedInstructionInsideADocumentIsRedactedAsText(t *testing.T) {
	hostile := injectedDocument + "\nSUBJECT: " + corpus.Term + "\n"
	doc, err := buildXLSX(hostile)
	if err != nil {
		t.Fatal(err)
	}
	out, m, err := worker.Redact(worker.KindXLSX, doc, terms())
	if err != nil {
		t.Fatalf("a well-formed hostile document was refused: %v", err)
	}
	if !m.Verified {
		t.Fatal("the derivative was not verified")
	}
	if m.Replacements == 0 {
		t.Fatal("nothing was replaced")
	}
	view, err := worker.Inspect(worker.KindXLSX, out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(view), bytes.ToLower([]byte(corpus.Term))) {
		t.Fatal("the term survived into the inspectable view")
	}
	// The instruction survives, because it is not a term. The point is
	// that surviving changes nothing: no code path reads it.
	if !bytes.Contains(view, []byte("maintenance mode")) {
		t.Skip("the fixture's instruction text did not survive normalisation; " +
			"the assertion below is not meaningful for this container")
	}
}

// TestAZipBombIsRefusedRatherThanExhaustingMemory. An OOXML container
// is a zip, and a part that inflates without limit is a denial of
// service dressed as a spreadsheet.
func TestAZipBombIsRefusedRatherThanExhaustingMemory(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatal(err)
	}
	// 256 MiB of a single repeated byte compresses to a few hundred
	// bytes and is well past the worker's per-part ceiling.
	chunk := bytes.Repeat([]byte("A"), 1<<20)
	for i := 0; i < 256; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() > 1<<20 {
		t.Fatalf("the fixture is not a bomb: %d compressed bytes", buf.Len())
	}
	if _, _, err := worker.Redact(worker.KindXLSX, buf.Bytes(), terms()); err == nil {
		t.Fatal("a decompression bomb was processed")
	}
}

// TestTruncatedAndEmptyContainersAreRefused. Zero bytes and a
// half-written upload are the two things an ingest endpoint sees most.
func TestTruncatedAndEmptyContainersAreRefused(t *testing.T) {
	good, err := buildXLSX("SUBJECT: " + corpus.Term)
	if err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string][]byte{
		"empty":       {},
		"truncated":   good[:len(good)/2],
		"garbage":     bytes.Repeat([]byte{0xde, 0xad}, 512),
		"pdf-as-xlsx": []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"),
	} {
		if _, _, err := worker.Redact(worker.KindXLSX, doc, terms()); err == nil {
			t.Fatalf("%s was accepted as an XLSX", name)
		}
	}
}

// TestAnEmptyTermIsRefused. A redaction told to remove "" matches
// everywhere and would report a spectacular replacement count over a
// document it did not actually clean.
func TestAnEmptyTermIsRefused(t *testing.T) {
	good, err := buildXLSX("SUBJECT: " + corpus.Term)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := worker.Redact(worker.KindXLSX, good, []string{""}); err == nil {
		t.Fatal("an empty term was accepted")
	}
	if _, _, err := worker.Redact(worker.KindXLSX, good, nil); err == nil {
		t.Fatal("a redaction with no terms was accepted")
	}
}

// TestCaseVariationDoesNotEvadeRedaction. "confidential-subject-7q"
// is the same secret.
func TestCaseVariationDoesNotEvadeRedaction(t *testing.T) {
	doc, err := buildXLSX("subject: " + strings.ToLower(corpus.Term) +
		" and " + strings.ToUpper(corpus.Term))
	if err != nil {
		t.Fatal(err)
	}
	out, m, err := worker.Redact(worker.KindXLSX, doc, terms())
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	if m.Replacements < 2 {
		t.Fatalf("case-varied occurrences were missed: %d replacement(s)", m.Replacements)
	}
	view, err := worker.Inspect(worker.KindXLSX, out)
	if err != nil {
		t.Fatal(err)
	}
	if containsFoldBytes(view, corpus.Term) {
		t.Fatal("a case variant survived")
	}
}

func containsFoldBytes(hay []byte, needle string) bool {
	return bytes.Contains(bytes.ToLower(hay), bytes.ToLower([]byte(needle)))
}

// TestTheManifestAlwaysStatesItsLimits. An ADEQUATE verdict with no
// stated limits is read as "clean" by every reader in a hurry, which
// is how a rasterised page of unredacted text gets released.
func TestTheManifestAlwaysStatesItsLimits(t *testing.T) {
	doc, err := buildXLSX("SUBJECT: " + corpus.Term)
	if err != nil {
		t.Fatal(err)
	}
	_, m, err := worker.Redact(worker.KindXLSX, doc, terms())
	if err != nil {
		t.Fatal(err)
	}
	if !m.Verified {
		t.Fatal("not verified")
	}
	if len(m.Limits) == 0 {
		t.Fatal("a verified derivative stated no limits")
	}
	joined := strings.ToLower(strings.Join(m.Limits, " "))
	if !strings.Contains(joined, "rasteris") && !strings.Contains(joined, "raster") {
		t.Fatalf("the limits do not mention rendered content: %v", m.Limits)
	}
	if m.OriginalSHA256 == m.DerivativeSHA256 {
		t.Fatal("the derivative is byte-identical to the original")
	}
}

// buildXLSX makes a genuine, genuinely compressed XLSX carrying text.
func buildXLSX(text string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/></Types>`,
		"xl/workbook.xml":     `<?xml version="1.0"?><workbook><sheets><sheet name="S1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/sharedStrings.xml": `<?xml version="1.0"?><sst count="1" uniqueCount="1"><si><t>` +
			escape(text) + `</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0"?><worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c></row></sheetData></worksheet>`,
	}
	for _, name := range []string{"[Content_Types].xml", "xl/workbook.xml",
		"xl/sharedStrings.xml", "xl/worksheets/sheet1.xml"} {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(parts[name])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
