package corpus

import (
	"bytes"
	"compress/zlib"
	"fmt"
)

// Real PDF 1.5+ fixtures.
//
// The earlier versions of PDF-OBJECT-STREAM and PDF-XREF-STREAM were
// built by injecting the string "/Type /ObjStm" into a catalog
// dictionary. That was adequate while the worker refused on a regex
// match, and it became worthless the moment the worker started actually
// processing these structures: a document that merely MENTIONS an
// object stream exercises none of the unpacking code.
//
// This is the overfitting trap the review named -- a test that goes
// green because the test is weak. So these build genuine containers:
// a real /ObjStm holding real objects with a real pair table, and a
// real /XRef cross-reference stream with the binary entry encoding.
//
// The forbidden term is placed INSIDE the object stream, so the only
// way a derivative can be clean is if the container was genuinely
// unpacked, redacted and rebuilt.

// buildObjectStreamPDF returns a PDF 1.5 document whose catalog, pages,
// page and font objects live inside a compressed object stream, and
// whose cross-reference is a stream rather than a table.
//
// The term goes into the page's /Title-like metadata inside the object
// stream AND into the top-level content stream, so both paths matter.
func buildObjectStreamPDF(term string, withXRefStream bool) ([]byte, error) {
	// The objects that will live inside the container. Streams cannot
	// go in an object stream, so the content stream stays top-level --
	// which is exactly how a real producer lays this out.
	inner := []struct {
		num  int
		body string
	}{
		{1, "<< /Type /Catalog /Pages 2 0 R >>"},
		{2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R " +
			"/Resources << /Font << /F1 5 0 R >> >> /PieceInfo (surveyed by " + term + ") >>"},
		{5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"},
	}

	// The pair table and the object data, per the specification: /N
	// pairs of "number offset", then the bodies starting at /First.
	var pairs, bodies bytes.Buffer
	for _, o := range inner {
		fmt.Fprintf(&pairs, "%d %d ", o.num, bodies.Len())
		bodies.WriteString(o.body)
		bodies.WriteByte(' ')
	}
	first := pairs.Len()
	var plain bytes.Buffer
	plain.Write(pairs.Bytes())
	plain.Write(bodies.Bytes())

	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(plain.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	objStm := z.Bytes()

	// The top-level content stream also carries the term.
	content := "BT /F1 12 Tf 72 720 Td (Claim by " + term + ") Tj ET"
	var cz bytes.Buffer
	czw := zlib.NewWriter(&cz)
	if _, err := czw.Write([]byte(content)); err != nil {
		return nil, err
	}
	if err := czw.Close(); err != nil {
		return nil, err
	}
	contentStream := cz.Bytes()

	var body bytes.Buffer
	body.WriteString("%PDF-1.5\n")
	offsets := map[int]int{}

	// Object 4: the page content stream, top level.
	offsets[4] = body.Len()
	fmt.Fprintf(&body, "4 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(contentStream))
	body.Write(contentStream)
	body.WriteString("\nendstream\nendobj\n")

	// Object 6: the object stream container.
	offsets[6] = body.Len()
	fmt.Fprintf(&body, "6 0 obj\n<< /Type /ObjStm /N %d /First %d /Length %d /Filter /FlateDecode >>\nstream\n",
		len(inner), first, len(objStm))
	body.Write(objStm)
	body.WriteString("\nendstream\nendobj\n")

	if !withXRefStream {
		// A 1.5 file that keeps a classic table: object streams
		// without a cross-reference stream is unusual but legal, and
		// separating the two lets each variant test one thing.
		xrefAt := body.Len()
		body.WriteString("xref\n0 7\n0000000000 65535 f \n")
		for n := 1; n <= 6; n++ {
			if off, ok := offsets[n]; ok {
				fmt.Fprintf(&body, "%010d 00000 n \n", off)
				continue
			}
			body.WriteString("0000000000 65535 f \n")
		}
		fmt.Fprintf(&body, "trailer\n<< /Size 7 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefAt)
		return body.Bytes(), nil
	}

	// Object 7: a real cross-reference stream.
	//
	// /W [1 4 2]: one byte of type, four of offset, two of generation
	// or object-stream index. Type 1 is a top-level object at an
	// offset; type 2 is an object inside an object stream.
	var xref bytes.Buffer
	entry := func(kind byte, a uint32, b uint16) {
		xref.WriteByte(kind)
		xref.Write([]byte{byte(a >> 24), byte(a >> 16), byte(a >> 8), byte(a)})
		xref.Write([]byte{byte(b >> 8), byte(b)})
	}
	entry(0, 0, 65535)              // object 0, the free head
	entry(2, 6, 0)                  // object 1: in stream 6, index 0
	entry(2, 6, 1)                  // object 2: in stream 6, index 1
	entry(2, 6, 2)                  // object 3: in stream 6, index 2
	entry(1, uint32(offsets[4]), 0) // object 4: top level
	entry(2, 6, 3)                  // object 5: in stream 6, index 3
	entry(1, uint32(offsets[6]), 0) // object 6: the container
	xrefSelf := body.Len()
	entry(1, uint32(xrefSelf), 0) // object 7: this stream

	var xz bytes.Buffer
	xzw := zlib.NewWriter(&xz)
	if _, err := xzw.Write(xref.Bytes()); err != nil {
		return nil, err
	}
	if err := xzw.Close(); err != nil {
		return nil, err
	}
	xrefStream := xz.Bytes()

	fmt.Fprintf(&body, "7 0 obj\n<< /Type /XRef /Size 8 /W [1 4 2] /Root 1 0 R "+
		"/Length %d /Filter /FlateDecode >>\nstream\n", len(xrefStream))
	body.Write(xrefStream)
	body.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&body, "startxref\n%d\n%%%%EOF\n", xrefSelf)
	return body.Bytes(), nil
}
