package worker

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// The worker has been exercised only through the corpus runner, which
// tests it against fixtures the corpus package builds. These are
// direct unit tests of the refusals themselves, because a refusal
// reached through a fixture is a refusal whose trigger nobody pinned
// down.

const term = "CONFIDENTIAL-SUBJECT-7Q"

// xlsx builds a genuine, genuinely compressed OOXML container.
func xlsx(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	// Deterministic order so a failure is reproducible.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(parts[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sheet(text string) map[string]string {
	return map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
		"xl/workbook.xml":     `<?xml version="1.0"?><workbook/>`,
		"xl/sharedStrings.xml": `<?xml version="1.0"?><sst><si><t>` + text +
			`</t></si></sst>`,
	}
}

// TestARedactionWithNothingToRedactIsRefused.
//
// An empty term matches everywhere and would report a spectacular
// replacement count over a document it did not clean.
func TestARedactionWithNothingToRedactIsRefused(t *testing.T) {
	doc := xlsx(t, sheet("SUBJECT: "+term))
	for _, terms := range [][]string{nil, {}, {""}, {term, ""}} {
		if _, _, err := Redact(KindXLSX, doc, terms); err == nil {
			t.Fatalf("a redaction with terms %v was accepted", terms)
		}
	}
}

// TestAnUnknownKindIsRefused.
func TestAnUnknownKindIsRefused(t *testing.T) {
	if _, _, err := Redact(Kind("DOCX"), xlsx(t, sheet("x")), []string{term}); err == nil {
		t.Fatal("an unsupported kind was processed")
	}
	if Kind("DOCX").Valid() {
		t.Fatal("DOCX reports as a supported kind")
	}
	for _, k := range []Kind{KindPDF, KindXLSX, KindPPTX} {
		if !k.Valid() {
			t.Fatalf("%s is not valid", k)
		}
	}
}

// TestTheXMLEscapedFormIsAlsoRemoved.
//
// A term written as "A&amp;B" is the same term to a reader and a
// different byte string to a naive search, which is exactly how a
// derivative is released with the content still in it.
func TestTheXMLEscapedFormIsAlsoRemoved(t *testing.T) {
	const tricky = "ACME & SONS"
	doc := xlsx(t, sheet("SUBJECT: ACME &amp; SONS"))
	out, m, err := Redact(KindXLSX, doc, []string{tricky})
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	if m.Replacements == 0 {
		t.Fatal("the escaped form was not replaced")
	}
	view, err := Inspect(KindXLSX, out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(view), bytes.ToLower([]byte(tricky))) {
		t.Fatal("the escaped form survived into the inspectable view")
	}
}

// TestVerificationSearchesTheInspectableViewNotTheRawBytes.
//
// FC-002. Searching raw bytes would report a compressed document
// clean because the term is not there in that form.
func TestVerificationSearchesTheInspectableViewNotTheRawBytes(t *testing.T) {
	doc := xlsx(t, sheet("SUBJECT: "+term))
	// The raw container does not contain the term as plain bytes,
	// because the parts are deflated.
	if bytes.Contains(doc, []byte(term)) {
		t.Skip("this container is not compressed, so the fixture no longer makes the point")
	}
	view, err := Inspect(KindXLSX, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(view, []byte(term)) {
		t.Fatal("the inspectable view does not contain a term the document carries; " +
			"verification against this view would report a dirty document clean")
	}
}

// TestTheManifestRecordsBothDigestsAndTheDerivativeDiffers.
func TestTheManifestRecordsBothDigestsAndTheDerivativeDiffers(t *testing.T) {
	doc := xlsx(t, sheet("SUBJECT: "+term))
	_, m, err := Redact(KindXLSX, doc, []string{term})
	if err != nil {
		t.Fatal(err)
	}
	if m.OriginalSHA256 == "" || m.DerivativeSHA256 == "" {
		t.Fatalf("manifest digests: %+v", m)
	}
	if m.OriginalSHA256 == m.DerivativeSHA256 {
		t.Fatal("the derivative is byte-identical to the original")
	}
	if !m.Verified {
		t.Fatal("a released derivative is not marked verified")
	}
	if m.PartsProcessed == 0 {
		t.Fatal("no parts were processed")
	}
	if len(m.Limits) == 0 {
		t.Fatal("a verified derivative states no limits")
	}
	if m.Algorithm == "" {
		t.Fatal("the manifest names no algorithm, so a reader cannot tell what produced it")
	}
}

// TestRedactionIsDeterministic.
//
// Two redactions of the same input must produce the same bytes, or the
// derivative's hash is a function of when it was produced rather than
// of what it contains -- and replay comparison stops meaning anything.
func TestRedactionIsDeterministic(t *testing.T) {
	doc := xlsx(t, sheet("SUBJECT: "+term))
	a, ma, err := Redact(KindXLSX, doc, []string{term})
	if err != nil {
		t.Fatal(err)
	}
	b, mb, err := Redact(KindXLSX, doc, []string{term})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two redactions of the same input produced different bytes")
	}
	if ma.DerivativeSHA256 != mb.DerivativeSHA256 {
		t.Fatal("two redactions produced different digests")
	}
}

// TestTheInputIsNeverModified. A worker that mutated its input would
// corrupt the original evidence it was given.
func TestTheInputIsNeverModified(t *testing.T) {
	doc := xlsx(t, sheet("SUBJECT: "+term))
	before := append([]byte(nil), doc...)
	if _, _, err := Redact(KindXLSX, doc, []string{term}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc, before) {
		t.Fatal("the worker modified its input")
	}
}

// TestAnEmbeddedBinaryPartIsRefusedForTheWholeDocument.
//
// Processing the readable parts and releasing the rest would assert
// cleanliness of bytes nobody read.
func TestAnEmbeddedBinaryPartIsRefusedForTheWholeDocument(t *testing.T) {
	parts := sheet("SUBJECT: " + term)
	parts["xl/media/image1.png"] = "\x89PNG\r\n\x1a\n binary content"
	out, _, err := Redact(KindXLSX, xlsx(t, parts), []string{term})
	if err == nil {
		t.Fatal("a document containing an unreadable part was processed")
	}
	if !IsRefusal(err) {
		t.Fatalf("the embedded binary produced a failure rather than a refusal: %v", err)
	}
	if out != nil {
		t.Fatal("a derivative was produced alongside the refusal")
	}
	var r *Refusal
	if !asRefusal(err, &r) {
		t.Fatalf("not a typed refusal: %v", err)
	}
	if !strings.Contains(r.Structure, "EMBEDDED-BINARY") {
		t.Fatalf("the refusal does not name the structure: %+v", r)
	}
	if !strings.Contains(r.Reason, "neither be found nor removed") {
		t.Fatalf("the refusal does not say why it matters: %s", r.Reason)
	}
}

func asRefusal(err error, out **Refusal) bool {
	for err != nil {
		if r, ok := err.(*Refusal); ok {
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

// TestAMalformedContainerIsRefusedRatherThanPartiallyProcessed.
func TestAMalformedContainerIsRefusedRatherThanPartiallyProcessed(t *testing.T) {
	good := xlsx(t, sheet("SUBJECT: "+term))
	for name, doc := range map[string][]byte{
		"empty":     {},
		"truncated": good[:len(good)/3],
		"garbage":   bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 64),
	} {
		out, m, err := Redact(KindXLSX, doc, []string{term})
		if err == nil {
			t.Fatalf("%s was accepted as an XLSX", name)
		}
		if out != nil {
			t.Fatalf("%s produced a derivative", name)
		}
		if m.Verified {
			t.Fatalf("%s was marked verified", name)
		}
	}
}

// TestInspectOnAMalformedContainerFails.
//
// It must not return an empty view, because an empty view contains no
// forbidden term and would verify clean.
func TestInspectOnAMalformedContainerFails(t *testing.T) {
	view, err := Inspect(KindXLSX, []byte("not a zip"))
	if err == nil {
		t.Fatalf("Inspect returned a view of %d bytes for a malformed container; an "+
			"empty view contains no forbidden term and verifies clean", len(view))
	}
}

// TestCaseVariationDoesNotEvade.
func TestCaseVariationDoesNotEvade(t *testing.T) {
	doc := xlsx(t, sheet("subject: "+strings.ToLower(term)+" and "+strings.ToUpper(term)))
	out, m, err := Redact(KindXLSX, doc, []string{term})
	if err != nil {
		t.Fatal(err)
	}
	if m.Replacements < 2 {
		t.Fatalf("%d replacements for two case variants", m.Replacements)
	}
	view, err := Inspect(KindXLSX, out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(view), bytes.ToLower([]byte(term))) {
		t.Fatal("a case variant survived")
	}
}

// TestARefusalReadsAsASentence. A refusal a caller cannot act on is an
// outage with better manners.
func TestARefusalReadsAsASentence(t *testing.T) {
	r := &Refusal{Structure: "PDF-ENCRYPTED", Reason: "the document is encrypted"}
	msg := r.Error()
	if !strings.Contains(msg, "PDF-ENCRYPTED") || !strings.Contains(msg, "encrypted") {
		t.Fatalf("the refusal does not carry both parts: %q", msg)
	}
	if !IsRefusal(r) {
		t.Fatal("IsRefusal does not recognise a Refusal")
	}
	if IsRefusal(nil) {
		t.Fatal("IsRefusal(nil) is true")
	}
}
