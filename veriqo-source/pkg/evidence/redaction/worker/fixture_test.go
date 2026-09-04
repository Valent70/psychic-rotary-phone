package worker

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// Real containers, built here rather than checked in as binaries.
//
// Building them in code is what makes the tests meaningful: a checked-in
// .xlsx is opaque, and a reviewer cannot tell whether the test proves
// the worker removed a term or proves the fixture never had one. These
// builders put the term in a known place, in a genuinely compressed
// part, so that a worker which did nothing would be caught.

// buildXLSX returns a minimal but structurally real workbook whose cell
// text lives in the shared string table, which is where Excel actually
// puts it.
func buildXLSX(t *testing.T, sharedStrings []string) []byte {
	t.Helper()
	var ss bytes.Buffer
	fmt.Fprintf(&ss, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`,
		len(sharedStrings), len(sharedStrings))
	for _, s := range sharedStrings {
		fmt.Fprintf(&ss, `<si><t>%s</t></si>`, s)
	}
	ss.WriteString(`</sst>`)

	var sheet bytes.Buffer
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for i := range sharedStrings {
		fmt.Fprintf(&sheet, `<row r="%d"><c r="A%d" t="s"><v>%d</v></c></row>`, i+1, i+1, i)
	}
	sheet.WriteString(`</sheetData></worksheet>`)

	return zipParts(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/sharedStrings.xml":     ss.String(),
		"xl/worksheets/sheet1.xml": sheet.String(),
	})
}

// buildPPTX returns a minimal presentation with the given slide texts.
func buildPPTX(t *testing.T, slideTexts []string) []byte {
	t.Helper()
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/></Types>`,
		"ppt/presentation.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`,
	}
	for i, text := range slideTexts {
		parts["ppt/slides/slide"+strconv.Itoa(i+1)+".xml"] =
			`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"` +
				` xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
				`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` + text +
				`</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	}
	return zipParts(t, parts)
}

// buildPPTXWithImage adds a binary part carrying the term, to exercise
// the refusal path.
func buildPPTXWithImage(t *testing.T, slideText, imagePayload string) []byte {
	t.Helper()
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types/>`,
		"ppt/slides/slide1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"` +
			` xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
			`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` + slideText +
			`</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`,
		"ppt/media/image1.png": imagePayload,
	}
	return zipParts(t, parts)
}

func zipParts(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := sortedKeys(parts)
	for _, n := range names {
		hdr := &zip.FileHeader{Name: n, Method: zip.Deflate}
		hdr.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip %q: %v", n, err)
		}
		if _, err := w.Write([]byte(parts[n])); err != nil {
			t.Fatalf("zip write %q: %v", n, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// buildPDF returns a small, structurally valid PDF whose page content
// stream is FlateDecode-compressed, so the text is genuinely not
// present in the file's bytes.
func buildPDF(t *testing.T, text string) []byte {
	t.Helper()
	content := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatalf("deflate: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("deflate close: %v", err)
	}
	stream := z.Bytes()

	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n")
	offsets := map[int]int{}

	writeObj := func(n int, s string) {
		offsets[n] = body.Len()
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", n, s)
	}
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")

	offsets[4] = body.Len()
	fmt.Fprintf(&body, "4 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(stream))
	body.Write(stream)
	body.WriteString("\nendstream\nendobj\n")

	writeObj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	xrefAt := body.Len()
	body.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for n := 1; n <= 5; n++ {
		fmt.Fprintf(&body, "%010d 00000 n \n", offsets[n])
	}
	body.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n")
	fmt.Fprintf(&body, "%d\n%%%%EOF\n", xrefAt)
	return body.Bytes()
}
