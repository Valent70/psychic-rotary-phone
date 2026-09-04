package corpus

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
	"time"

	"veriqo/pkg/evidence/redaction/worker"
)

// The generators.
//
// Each builds a real container carrying the forbidden term in the place
// the variant names. They are not toy fixtures: the OOXML packages are
// genuine zip archives with deflated parts, and the PDFs carry real
// FlateDecode streams and a valid cross-reference table.
//
// The point of building them in code rather than checking in binaries
// is that a reader can see exactly where the term was put. A checked-in
// .xlsx proves nothing about what the worker did, because nobody can
// tell whether the fixture contained the term in the first place.

const fixedEpoch = "1980-01-01"

var modTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Build returns a container exercising the named variant, carrying the
// term in the place the variant describes.
func Build(v Variant, term string) ([]byte, error) {
	switch v.ID {
	case "XLSX-SHARED-STRINGS":
		return xlsx(map[string]string{
			"xl/sharedStrings.xml":     sst(term, "Parcel A, sampled pre-loading"),
			"xl/worksheets/sheet1.xml": sheetRefs(2),
		}), nil
	case "XLSX-INLINE-STRINGS":
		return xlsx(map[string]string{
			"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
				`<row r="1"><c r="A1" t="inlineStr"><is><t>` + term + `</t></is></c></row>` +
				`</sheetData></worksheet>`,
		}), nil
	case "XLSX-ESCAPED-XML":
		return xlsx(map[string]string{
			"xl/sharedStrings.xml": sst("Report by R&amp;D Partners", "ordinary"),
		}), nil
	case "XLSX-UNICODE":
		return xlsx(map[string]string{
			"xl/sharedStrings.xml": sst(term, "Bergensfjord Rederi AS, Ålesund"),
		}), nil
	case "XLSX-MIXED-CASE":
		return xlsx(map[string]string{
			"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="3" uniqueCount="3">` +
				`<si><t>` + term + `</t></si>` +
				`<si><t>` + upper(term) + `</t></si>` +
				`<si><t>` + lower(term) + `</t></si></sst>`,
		}), nil
	case "XLSX-HIDDEN-SHEET":
		return xlsx(map[string]string{
			"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets>` +
				`<sheet name="Visible" sheetId="1"/><sheet name="Hidden" sheetId="2" state="hidden"/>` +
				`</sheets></workbook>`,
			"xl/worksheets/sheet2.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
				`<row r="1"><c r="A1" t="inlineStr"><is><t>` + term + `</t></is></c></row>` +
				`</sheetData></worksheet>`,
		}), nil
	case "XLSX-COMMENTS":
		return xlsx(map[string]string{
			"xl/comments1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<comments xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><commentList>` +
				`<comment ref="A1"><text><t>Chase ` + term + ` for the survey</t></text></comment>` +
				`</commentList></comments>`,
		}), nil
	case "XLSX-DOCPROPS":
		return xlsx(map[string]string{
			"docProps/core.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"` +
				` xmlns:dc="http://purl.org/dc/elements/1.1/">` +
				`<dc:creator>` + term + `</dc:creator><dc:title>Claim file</dc:title></cp:coreProperties>`,
		}), nil
	case "XLSX-EMBEDDED-OBJECT":
		return xlsx(map[string]string{
			"xl/sharedStrings.xml":      sst(term, "ordinary"),
			"xl/embeddings/object1.bin": "\x00\x01OLE\x00" + term + "\x00",
		}), nil

	case "PPTX-SLIDE-TEXT":
		return pptx(map[string]string{"ppt/slides/slide1.xml": slide("Claim by " + term)}), nil
	case "PPTX-SPEAKER-NOTES":
		return pptx(map[string]string{
			"ppt/slides/slide1.xml": slide("Overview"),
			"ppt/notesSlides/notesSlide1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<p:notes xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"` +
				` xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
				`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Do not name ` + term +
				`</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:notes>`,
		}), nil
	case "PPTX-EMBEDDED-IMAGE":
		return pptx(map[string]string{
			"ppt/slides/slide1.xml": slide("Claim by " + term),
			"ppt/media/image1.png":  "\x89PNG\r\n\x1a\n" + term + "\x00",
		}), nil

	case "PDF-FLATE-CONTENT":
		return pdf(pdfSpec{content: "BT /F1 12 Tf 72 720 Td (Claim by " + term + ") Tj ET", flate: true})
	case "PDF-UNCOMPRESSED":
		return pdf(pdfSpec{content: "BT /F1 12 Tf 72 720 Td (Claim by " + term + ") Tj ET", flate: false})
	case "PDF-METADATA":
		return pdf(pdfSpec{content: "BT /F1 12 Tf 72 720 Td (No names here) Tj ET", flate: true,
			extraObject: "<< /Type /Info /Author (" + term + ") /Title (Claim file) >>"})
	case "PDF-ANNOTATION":
		return pdf(pdfSpec{content: "BT /F1 12 Tf 72 720 Td (See note) Tj ET", flate: true,
			extraObject: "<< /Type /Annot /Subtype /Text /Contents (Raised by " + term + ") >>"})
	case "PDF-ENCRYPTED":
		return pdfMutated(term, "/Root 1 0 R", "/Root 1 0 R /Encrypt 9 0 R")
	case "PDF-INCREMENTAL":
		b, err := pdf(pdfSpec{content: "BT /F1 12 Tf 72 720 Td (Claim by " + term + ") Tj ET", flate: true})
		if err != nil {
			return nil, err
		}
		return append(b, []byte("\n7 0 obj\n<< >>\nendobj\ntrailer\n<< /Size 8 >>\nstartxref\n0\n%%EOF\n")...), nil
	case "PDF-OBJECT-STREAM":
		return buildObjectStreamPDF(term, false)
	case "PDF-XREF-STREAM":
		return buildObjectStreamPDF(term, true)
	case "PDF-LZW":
		return pdfMutated(term, "/Filter /FlateDecode", "/Filter /LZWDecode")
	case "PDF-ATTACHMENT":
		return pdf(pdfSpec{content: "BT /F1 12 Tf 72 720 Td (See attachment) Tj ET", flate: true,
			extraObject: "<< /Type /Filespec /F (survey.txt) /EF << /F 9 0 R >> >>",
			attachment:  "Survey prepared by " + term + " on 3 March"})
	case "PDF-MALFORMED":
		b, err := pdf(pdfSpec{content: "BT /F1 12 Tf 72 720 Td (Claim by " + term + ") Tj ET", flate: true})
		if err != nil {
			return nil, err
		}
		// Remove the trailer: a file no parser can walk.
		return bytes.Replace(b, []byte("trailer"), []byte("trailr!"), 1), nil
	}
	return nil, fmt.Errorf("corpus: no generator for variant %q", v.ID)
}

// --- OOXML helpers ----------------------------------------------------

func sst(a, b string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2">` +
		`<si><t>` + a + `</t></si><si><t>` + b + `</t></si></sst>`
}

func sheetRefs(n int) string {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<row r="%d"><c r="A%d" t="s"><v>%d</v></c></row>`, i+1, i+1, i)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

func slide(text string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"` +
		` xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` + text +
		`</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
}

func xlsx(extra map[string]string) []byte {
	parts := map[string]string{
		"[Content_Types].xml": contentTypes,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<sheets><sheet name="Sheet1" sheetId="1"/></sheets></workbook>`,
		"xl/worksheets/sheet1.xml": sheetRefs(1),
	}
	for k, v := range extra {
		parts[k] = v
	}
	return zipOf(parts)
}

func pptx(extra map[string]string) []byte {
	parts := map[string]string{
		"[Content_Types].xml": contentTypes,
		"ppt/presentation.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`,
	}
	for k, v := range extra {
		parts[k] = v
	}
	return zipOf(parts)
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Default Extension="bin" ContentType="application/vnd.openxmlformats-officedocument.oleObject"/>` +
	`<Default Extension="png" ContentType="image/png"/></Types>`

func zipOf(parts map[string]string) []byte {
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		hdr := &zip.FileHeader{Name: n, Method: zip.Deflate}
		hdr.Modified = modTime
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil
		}
		if _, err := w.Write([]byte(parts[n])); err != nil {
			return nil
		}
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

func upper(s string) string { return toCase(s, true) }
func lower(s string) string { return toCase(s, false) }

func toCase(s string, up bool) string {
	out := []rune(s)
	for i, r := range out {
		if up && r >= 'a' && r <= 'z' {
			out[i] = r - 32
		}
		if !up && r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}

// --- PDF helpers -------------------------------------------------------

type pdfSpec struct {
	content     string
	flate       bool
	extraObject string
	// attachment, when non-empty, is written as a real embedded file
	// stream. An attachment declared but left empty would make the
	// variant vacuous: the worker would "handle" a file with no
	// content in it.
	attachment string
}

func pdf(spec pdfSpec) ([]byte, error) {
	stream := []byte(spec.content)
	filter := ""
	if spec.flate {
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		if _, err := zw.Write([]byte(spec.content)); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		stream = z.Bytes()
		filter = " /Filter /FlateDecode"
	}

	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n")
	offsets := map[int]int{}
	write := func(n int, s string) {
		offsets[n] = body.Len()
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", n, s)
	}
	write(1, "<< /Type /Catalog /Pages 2 0 R >>")
	write(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	write(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R "+
		"/Resources << /Font << /F1 5 0 R >> >> >>")
	offsets[4] = body.Len()
	fmt.Fprintf(&body, "4 0 obj\n<< /Length %d%s >>\nstream\n", len(stream), filter)
	body.Write(stream)
	body.WriteString("\nendstream\nendobj\n")
	write(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	highest := 5
	if spec.extraObject != "" {
		write(6, spec.extraObject)
		highest = 6
	}
	if spec.attachment != "" {
		offsets[9] = body.Len()
		fmt.Fprintf(&body, "9 0 obj\n<< /Type /EmbeddedFile /Length %d >>\nstream\n%s\nendstream\nendobj\n",
			len(spec.attachment), spec.attachment)
		highest = 9
	}

	xrefAt := body.Len()
	fmt.Fprintf(&body, "xref\n0 %d\n0000000000 65535 f \n", highest+1)
	for n := 1; n <= highest; n++ {
		off, ok := offsets[n]
		if !ok {
			body.WriteString("0000000000 65535 f \n")
			continue
		}
		fmt.Fprintf(&body, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&body, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", highest+1, xrefAt)
	return body.Bytes(), nil
}

// pdfMutated builds the standard document then applies one textual
// substitution, which is how the structural refusal cases are made:
// the document is real, and one marker turns it into the shape the
// worker must decline.
func pdfMutated(term, from, to string) ([]byte, error) {
	b, err := pdf(pdfSpec{content: "BT /F1 12 Tf 72 720 Td (Claim by " + term + ") Tj ET", flate: true})
	if err != nil {
		return nil, err
	}
	out := bytes.Replace(b, []byte(from), []byte(to), 1)
	if bytes.Equal(out, b) {
		return nil, fmt.Errorf("corpus: mutation %q did not apply", from)
	}
	return out, nil
}

// KindsCovered reports which worker kinds the taxonomy exercises.
func KindsCovered() []worker.Kind {
	seen := map[worker.Kind]bool{}
	var out []worker.Kind
	for _, v := range Variants {
		if !seen[v.Kind] {
			seen[v.Kind] = true
			out = append(out, v.Kind)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
