package corpus

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
)

// Fixtures for the structural corpus.
//
// # The rule these are built under
//
// FC-005 (FIXTURE_NOT_GENUINE) is in the register because an earlier
// version of the PDF 1.5 fixtures did not build real containers: they
// wrote the forbidden term into an ordinary dictionary and NAMED the
// structure in a comment. Every test passed and nothing was tested.
//
// So each fixture below actually carries the structure it is named
// for, and the term is placed where only the corresponding code path
// can reach it. TestTheObjectStreamFixtureIsGenuine asserts the property
// that makes them worth running at all: for a container variant, the
// term must NOT be findable in the fixture's raw bytes.

// Term is the forbidden string every fixture carries.
//
// It is deliberately not a real name: a fixture containing a real
// party's details would make the corpus itself a data-protection
// artefact.
const Term = "CONFIDENTIAL-SUBJECT-7Q"

// Build returns the document for a variant.
func Build(v Variant) ([]byte, error) {
	switch v.ID {
	// --- PDF ---------------------------------------------------------
	case "PDF-FLATE-CONTENT":
		return buildPDF(pdfOptions{compressedContent: true})
	case "PDF-UNCOMPRESSED":
		return buildPDF(pdfOptions{})
	case "PDF-METADATA":
		return buildPDF(pdfOptions{metadata: true})
	case "PDF-ANNOTATION":
		return buildPDF(pdfOptions{annotation: true})
	case "PDF-ATTACHMENT":
		return buildPDF(pdfOptions{attachment: true})
	case "PDF-ENCRYPTED":
		return buildPDF(pdfOptions{encrypted: true})
	case "PDF-LZW":
		return buildPDF(pdfOptions{lzw: true})
	case "PDF-INCREMENTAL":
		return buildIncrementalPDF()
	case "PDF-MALFORMED":
		return []byte("this is not a PDF at all, it merely claims to be one: " + Term), nil
	case "PDF-OBJECT-STREAM":
		return buildObjectStreamPDF(Term, false)
	case "PDF-XREF-STREAM":
		return buildObjectStreamPDF(Term, true)

	// --- XLSX ---------------------------------------------------------
	case "XLSX-SHARED-STRINGS":
		return buildOOXML(xlsxParts(sharedStrings(Term)))
	case "XLSX-INLINE-STRINGS":
		return buildOOXML(xlsxParts(inlineStrings(Term)))
	case "XLSX-ESCAPED-XML":
		return buildOOXML(xlsxParts(sharedStrings("A&B " + Term + " <ops>")))
	case "XLSX-UNICODE":
		return buildOOXML(xlsxParts(sharedStrings(Term + " — 海上")))
	case "XLSX-MIXED-CASE":
		return buildOOXML(xlsxParts(sharedStrings(
			strings.ToLower(Term) + " / " + Term + " / " + strings.ToTitle(Term))))
	case "XLSX-HIDDEN-SHEET":
		p := xlsxParts(sharedStrings("nothing here"))
		p["xl/worksheets/sheet2.xml"] = []byte(
			`<?xml version="1.0"?><worksheet><sheetData><row r="1"><c r="A1" t="inlineStr">` +
				`<is><t>` + Term + `</t></is></c></row></sheetData></worksheet>`)
		p["xl/workbook.xml"] = []byte(
			`<?xml version="1.0"?><workbook><sheets>` +
				`<sheet name="Visible" sheetId="1" r:id="rId1"/>` +
				`<sheet name="Hidden" sheetId="2" state="hidden" r:id="rId2"/>` +
				`</sheets></workbook>`)
		return buildOOXML(p)
	case "XLSX-COMMENTS":
		p := xlsxParts(sharedStrings("nothing here"))
		p["xl/comments1.xml"] = []byte(
			`<?xml version="1.0"?><comments><commentList><comment ref="A1" authorId="0">` +
				`<text><t>` + Term + `</t></text></comment></commentList></comments>`)
		return buildOOXML(p)
	case "XLSX-DOCPROPS":
		p := xlsxParts(sharedStrings("nothing here"))
		p["docProps/core.xml"] = []byte(
			`<?xml version="1.0"?><cp:coreProperties><dc:creator>` + Term +
				`</dc:creator><dc:title>Survey</dc:title></cp:coreProperties>`)
		return buildOOXML(p)
	case "XLSX-EMBEDDED-OBJECT":
		p := xlsxParts(sharedStrings("nothing here"))
		p["xl/embeddings/oleObject1.bin"] = binaryCarrying(Term)
		return buildOOXML(p)

	// --- PPTX ---------------------------------------------------------
	case "PPTX-SLIDE-TEXT":
		return buildOOXML(pptxParts(Term, ""))
	case "PPTX-SPEAKER-NOTES":
		return buildOOXML(pptxParts("Quarterly review", Term))
	case "PPTX-EMBEDDED-IMAGE":
		p := pptxParts("Quarterly review", "")
		p["ppt/media/image1.png"] = binaryCarrying(Term)
		return buildOOXML(p)
	}
	return nil, fmt.Errorf("corpus: no fixture for variant %q", v.ID)
}

// binaryCarrying returns bytes that are not text and that contain the
// term. A PNG signature makes it unmistakably binary, so a worker that
// "redacted" it would be corrupting an image.
func binaryCarrying(term string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	b.Write([]byte{0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'})
	b.WriteString(term)
	b.Write([]byte{0x00, 0xff, 0xfe, 0x00})
	return b.Bytes()
}

// --- PDF fixtures --------------------------------------------------------

type pdfOptions struct {
	compressedContent bool
	metadata          bool
	annotation        bool
	attachment        bool
	encrypted         bool
	lzw               bool
}

func buildPDF(o pdfOptions) ([]byte, error) {
	content := "BT /F1 12 Tf 72 720 Td (Survey performed for " + Term + ") Tj ET"
	if o.metadata || o.annotation || o.attachment {
		// Keep the term OUT of the page content for these variants, so
		// that a pass depends on the specific path being exercised
		// rather than on the content stream being cleaned anyway.
		content = "BT /F1 12 Tf 72 720 Td (Survey performed) Tj ET"
	}

	stream := []byte(content)
	filter := ""
	if o.compressedContent {
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		if _, err := zw.Write(stream); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		stream = z.Bytes()
		filter = " /Filter /FlateDecode"
	}
	if o.lzw {
		// A genuine LZW stream is not built here: the point of the
		// variant is that the worker must refuse a filter it does not
		// decode, and it decides that from the filter name. Declaring
		// the filter and carrying opaque bytes is exactly the
		// situation the refusal exists for.
		filter = " /Filter /LZWDecode"
		stream = []byte{0x80, 0x0b, 0x60, 0x50, 0x22, 0x0c, 0x0c, 0x85, 0x01}
	}

	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := map[int]int{}
	obj := func(n int, body string) {
		offsets[n] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
	}

	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")

	page := "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R " +
		"/Resources << /Font << /F1 5 0 R >> >>"
	if o.annotation {
		page += " /Annots [7 0 R]"
	}
	page += " >>"
	obj(3, page)

	offsets[4] = b.Len()
	fmt.Fprintf(&b, "4 0 obj\n<< /Length %d%s >>\nstream\n", len(stream), filter)
	b.Write(stream)
	b.WriteString("\nendstream\nendobj\n")

	obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	if o.metadata {
		obj(6, "<< /Title (Cargo survey) /Author ("+Term+") /Subject (Discharge) >>")
	} else {
		obj(6, "<< /Title (Cargo survey) /Author (Surveyor) /Subject (Discharge) >>")
	}
	if o.annotation {
		obj(7, "<< /Type /Annot /Subtype /Text /Rect [10 10 20 20] /Contents ("+
			"note: "+Term+") >>")
	}
	if o.attachment {
		att := []byte("attached memorandum concerning " + Term + "\n")
		offsets[8] = b.Len()
		fmt.Fprintf(&b, "8 0 obj\n<< /Type /EmbeddedFile /Length %d >>\nstream\n", len(att))
		b.Write(att)
		b.WriteString("\nendstream\nendobj\n")
	}

	maxNum := 0
	for n := range offsets {
		if n > maxNum {
			maxNum = n
		}
	}
	xrefAt := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", maxNum+1)
	for n := 1; n <= maxNum; n++ {
		if off, ok := offsets[n]; ok {
			fmt.Fprintf(&b, "%010d 00000 n \n", off)
			continue
		}
		b.WriteString("0000000000 65535 f \n")
	}
	trailer := fmt.Sprintf("<< /Size %d /Root 1 0 R /Info 6 0 R", maxNum+1)
	if o.encrypted {
		trailer += " /Encrypt 9 0 R"
	}
	trailer += " >>"
	fmt.Fprintf(&b, "trailer\n%s\nstartxref\n%d\n%%%%EOF\n", trailer, xrefAt)
	return b.Bytes(), nil
}

// buildIncrementalPDF appends a second revision, which is what an
// incremental update is: the first revision's objects remain in the
// file in full.
func buildIncrementalPDF() ([]byte, error) {
	base, err := buildPDF(pdfOptions{})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(base)

	// The updated page content, with the term removed from the VISIBLE
	// revision. The superseded object above still carries it -- which
	// is precisely why redacting only the current revision is unsafe.
	clean := "BT /F1 12 Tf 72 720 Td (Survey performed) Tj ET"
	at := b.Len()
	fmt.Fprintf(&b, "4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(clean), clean)
	xrefAt := b.Len()
	fmt.Fprintf(&b, "xref\n0 1\n0000000000 65535 f \n4 1\n%010d 00000 n \n", at)
	fmt.Fprintf(&b, "trailer\n<< /Size 7 /Root 1 0 R /Prev 0 >>\nstartxref\n%d\n%%%%EOF\n", xrefAt)
	return b.Bytes(), nil
}

// --- OOXML fixtures -------------------------------------------------------

func buildOOXML(parts map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	// Deterministic order: a fixture whose bytes depend on map
	// iteration order cannot be hashed or compared between runs.
	sortStrings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(parts[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

const contentTypes = `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/></Types>`

func xlsxParts(sheetAndStrings map[string][]byte) map[string][]byte {
	p := map[string][]byte{
		"[Content_Types].xml": []byte(contentTypes),
		"xl/workbook.xml": []byte(`<?xml version="1.0"?><workbook><sheets>` +
			`<sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`),
		"docProps/core.xml": []byte(`<?xml version="1.0"?><cp:coreProperties>` +
			`<dc:creator>Surveyor</dc:creator></cp:coreProperties>`),
	}
	for k, v := range sheetAndStrings {
		p[k] = v
	}
	return p
}

func sharedStrings(text string) map[string][]byte {
	esc := escapeXML(text)
	return map[string][]byte{
		"xl/sharedStrings.xml": []byte(`<?xml version="1.0"?><sst count="1" uniqueCount="1">` +
			`<si><t>` + esc + `</t></si></sst>`),
		"xl/worksheets/sheet1.xml": []byte(`<?xml version="1.0"?><worksheet><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c></row></sheetData></worksheet>`),
	}
}

func inlineStrings(text string) map[string][]byte {
	esc := escapeXML(text)
	return map[string][]byte{
		"xl/worksheets/sheet1.xml": []byte(`<?xml version="1.0"?><worksheet><sheetData>` +
			`<row r="1"><c r="A1" t="inlineStr"><is><t>` + esc + `</t></is></c></row>` +
			`</sheetData></worksheet>`),
	}
}

func pptxParts(slideText, notes string) map[string][]byte {
	p := map[string][]byte{
		"[Content_Types].xml": []byte(contentTypes),
		"ppt/presentation.xml": []byte(`<?xml version="1.0"?><presentation><sldIdLst>` +
			`<sldId id="256" r:id="rId1"/></sldIdLst></presentation>`),
		"ppt/slides/slide1.xml": []byte(`<?xml version="1.0"?><sld><cSld><spTree><sp><txBody>` +
			`<a:p><a:r><a:t>` + escapeXML(slideText) + `</a:t></a:r></a:p>` +
			`</txBody></sp></spTree></cSld></sld>`),
		"docProps/core.xml": []byte(`<?xml version="1.0"?><cp:coreProperties>` +
			`<dc:creator>Presenter</dc:creator></cp:coreProperties>`),
	}
	if notes != "" {
		p["ppt/notesSlides/notesSlide1.xml"] = []byte(`<?xml version="1.0"?><notes><cSld>` +
			`<spTree><sp><txBody><a:p><a:r><a:t>` + escapeXML(notes) +
			`</a:t></a:r></a:p></txBody></sp></spTree></cSld></notes>`)
	}
	return p
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
