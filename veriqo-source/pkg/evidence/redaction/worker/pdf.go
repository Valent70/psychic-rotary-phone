package worker

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The PDF worker.
//
// # What it handles
//
// A PDF whose text lives in content streams that are either
// uncompressed or FlateDecode-compressed, in a file with a single
// cross-reference table. It rewrites the text inside those streams,
// re-deflates them, and rebuilds the xref so the result is a structurally
// valid PDF rather than a corrupted one that happens not to contain the
// term.
//
// # What it refuses, and why refusing is the feature
//
// PDF is a container format with many places to hide a string, and a
// redactor that silently skips the ones it does not understand is worse
// than no redactor: it produces a document with a provenance record
// saying the content was removed. So this worker enumerates the
// structures it recognises but cannot process, and any of them present
// with a forbidden term in reach produces ErrRefused:
//
//   - Encrypted documents (/Encrypt). The worker cannot read them, and
//     must not report absence for content it never decrypted.
//   - Incremental updates (more than one %%EOF). Earlier revisions of
//     the document remain in the file and a term redacted in the latest
//     revision can still be recovered from a previous one.
//   - Object streams (/ObjStm). Objects compressed inside another
//     object; this worker does not unpack them.
//   - Cross-reference streams (/XRef) rather than a table.
//   - Non-Flate stream filters (LZW, RunLength, DCT, CCITT, JBIG2 and
//     the ASCII filters), which it does not decode.
//
// This list is not a claim to be exhaustive about PDF. It is the set of
// structures this worker can detect. A structure it cannot detect is a
// real residual risk and is stated as a limitation on every chain it
// produces.

type pdfRedactor struct{}

// NewPDFRedactor returns the PDF worker.
func NewPDFRedactor() Redactor { return pdfRedactor{} }

func (pdfRedactor) Kind() Kind { return KindPDF }

var (
	pdfEOF          = []byte("%%EOF")
	pdfEncrypt      = regexp.MustCompile(`/Encrypt\b`)
	pdfObjStm       = regexp.MustCompile(`/Type\s*/ObjStm\b`)
	pdfXRefStream   = regexp.MustCompile(`/Type\s*/XRef\b`)
	pdfUnsafeFilter = regexp.MustCompile(`/(LZWDecode|RunLengthDecode|DCTDecode|CCITTFaxDecode|JBIG2Decode|ASCIIHexDecode|ASCII85Decode|Crypt)\b`)
	// pdfObject matches "N 0 obj ... endobj".
	pdfObject = regexp.MustCompile(`(?s)(\d+)\s+(\d+)\s+obj\b(.*?)endobj`)
	// pdfStreamBody captures a stream's raw bytes inside an object.
	pdfStreamBody = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	pdfIsFlate    = regexp.MustCompile(`/Filter\s*/FlateDecode\b`)
	pdfLength     = regexp.MustCompile(`/Length\s+\d+`)
)

func (p pdfRedactor) Redact(original []byte, terms []string, marker string) ([]byte, TransformManifest, error) {
	m := TransformManifest{
		Kind:            KindPDF,
		Worker:          "veriqo/pkg/evidence/redaction/worker.pdfRedactor",
		Replacements:    map[string]int{},
		RedactionMarker: marker,
	}

	if !bytes.HasPrefix(original, []byte("%PDF-")) {
		return nil, m, fmt.Errorf("redaction/worker: not a PDF: no %%PDF- header")
	}

	// Structural refusals first. These are checked before any
	// rewriting, because a document this worker cannot fully account
	// for must not be partially processed and then released.
	if pdfEncrypt.Match(original) {
		m.Unaccounted = append(m.Unaccounted, "the document is encrypted (/Encrypt); this worker cannot read its content streams")
	}
	if n := bytes.Count(original, pdfEOF); n > 1 {
		m.Unaccounted = append(m.Unaccounted, fmt.Sprintf(
			"the document has %d %%%%EOF markers, so it carries incremental updates: "+
				"a term removed from the latest revision remains recoverable from an earlier one", n))
	}
	if pdfObjStm.Match(original) {
		m.Unaccounted = append(m.Unaccounted, "the document uses object streams (/ObjStm); this worker does not unpack them")
	}
	if pdfXRefStream.Match(original) {
		m.Unaccounted = append(m.Unaccounted, "the document uses a cross-reference stream (/XRef) rather than a table")
	}
	if f := pdfUnsafeFilter.Find(original); f != nil {
		m.Unaccounted = append(m.Unaccounted, fmt.Sprintf(
			"the document uses the stream filter %s, which this worker does not decode", string(f)))
	}
	if len(m.Unaccounted) > 0 {
		sort.Strings(m.Unaccounted)
		return nil, m, nil // Pipeline.Run turns a non-empty Unaccounted into ErrRefused.
	}

	out := original
	objects := pdfObject.FindAllSubmatchIndex(original, -1)
	// Rewriting shifts offsets, so objects are processed from the end
	// backwards and the xref is rebuilt from scratch afterwards.
	for i := len(objects) - 1; i >= 0; i-- {
		loc := objects[i]
		objNum := string(original[loc[2]:loc[3]])
		bodyStart, bodyEnd := loc[6], loc[7]
		body := out[bodyStart:bodyEnd]

		rewritten, counts, err := p.rewriteObject(body, terms, marker)
		if err != nil {
			return nil, m, err
		}
		m.PartsInspected = append(m.PartsInspected, "object "+objNum)
		total := 0
		for term, n := range counts {
			m.Replacements[term] += n
			total += n
		}
		if total > 0 {
			m.PartsModified = append(m.PartsModified, "object "+objNum)
			out = append(append(append([]byte{}, out[:bodyStart]...), rewritten...), out[bodyEnd:]...)
		}
	}

	sort.Strings(m.PartsInspected)
	sort.Strings(m.PartsModified)

	if len(m.PartsModified) == 0 {
		return out, m, nil
	}
	rebuilt, err := rebuildXref(out)
	if err != nil {
		return nil, m, err
	}
	return rebuilt, m, nil
}

// rewriteObject redacts one PDF object, decompressing and recompressing
// a FlateDecode stream if it has one.
func (p pdfRedactor) rewriteObject(body []byte, terms []string, marker string) ([]byte, map[string]int, error) {
	counts := map[string]int{}

	loc := pdfStreamBody.FindSubmatchIndex(body)
	if loc == nil {
		// No stream: the object is a dictionary. Redact its literal
		// strings the same way, since names and metadata live here.
		out, c := replaceAll(body, terms, marker)
		return out, c, nil
	}

	dict := body[:loc[0]]
	raw := body[loc[2]:loc[3]]
	tail := body[loc[1]:]

	var plain []byte
	flate := pdfIsFlate.Match(dict)
	if flate {
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			// A stream declared Flate that will not inflate is a
			// structure this worker cannot account for.
			return nil, nil, fmt.Errorf("redaction/worker: a FlateDecode stream would not inflate: %w", err)
		}
		plain, err = io.ReadAll(io.LimitReader(zr, maxPartBytes))
		_ = zr.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("redaction/worker: reading an inflated stream: %w", err)
		}
	} else {
		plain = append([]byte(nil), raw...)
	}

	redacted, c := replaceAll(plain, terms, marker)
	dictOut, dictCounts := replaceAll(dict, terms, marker)
	for k, v := range c {
		counts[k] += v
	}
	for k, v := range dictCounts {
		counts[k] += v
	}
	if len(counts) == 0 {
		return body, counts, nil
	}

	stored := redacted
	if flate {
		var zbuf bytes.Buffer
		zw := zlib.NewWriter(&zbuf)
		if _, err := zw.Write(redacted); err != nil {
			return nil, nil, fmt.Errorf("redaction/worker: recompressing a stream: %w", err)
		}
		if err := zw.Close(); err != nil {
			return nil, nil, fmt.Errorf("redaction/worker: recompressing a stream: %w", err)
		}
		stored = zbuf.Bytes()
	}

	// /Length must match the stored bytes or the file is malformed.
	dictOut = pdfLength.ReplaceAll(dictOut, []byte("/Length "+strconv.Itoa(len(stored))))

	var out bytes.Buffer
	out.Write(dictOut)
	out.WriteString("stream\n")
	out.Write(stored)
	out.WriteString("\nendstream")
	out.Write(tail)
	return out.Bytes(), counts, nil
}

// rebuildXref replaces the cross-reference table and trailer so that
// offsets match the rewritten body.
//
// Without this the derivative would be a file that no reader can open.
// A redaction that destroys the document removes the term and the
// evidence together, which is not redaction.
func rebuildXref(doc []byte) ([]byte, error) {
	// The trailer is located first, then the xref table before it.
	// Searching for "xref" directly finds "startxref", which sits after
	// the trailer -- an off-by-one-keyword that produces a file no
	// reader can open.
	trailerAt := bytes.LastIndex(doc, []byte("trailer"))
	if trailerAt < 0 {
		return nil, fmt.Errorf("redaction/worker: the document has no trailer")
	}
	cut := bytes.LastIndex(doc[:trailerAt], []byte("xref"))
	if cut < 0 {
		return nil, fmt.Errorf("redaction/worker: the document has no cross-reference table to rebuild")
	}
	trailerEnd := bytes.Index(doc[trailerAt:], []byte("startxref"))
	if trailerEnd < 0 {
		return nil, fmt.Errorf("redaction/worker: the trailer is not followed by startxref")
	}
	trailer := strings.TrimSpace(string(doc[trailerAt : trailerAt+trailerEnd]))

	bodyEnd := cut
	body := doc[:bodyEnd]

	// Collect object offsets from the rewritten body.
	type objAt struct {
		num int
		off int
	}
	var objs []objAt
	for _, loc := range pdfObject.FindAllSubmatchIndex(body, -1) {
		n, err := strconv.Atoi(string(body[loc[2]:loc[3]]))
		if err != nil {
			continue
		}
		objs = append(objs, objAt{num: n, off: loc[0]})
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].num < objs[j].num })
	if len(objs) == 0 {
		return nil, fmt.Errorf("redaction/worker: the rewritten document contains no objects")
	}
	highest := objs[len(objs)-1].num

	var out bytes.Buffer
	out.Write(body)
	xrefOffset := out.Len()
	out.WriteString("xref\n")
	fmt.Fprintf(&out, "0 %d\n", highest+1)
	out.WriteString("0000000000 65535 f \n")
	byNum := map[int]int{}
	for _, o := range objs {
		byNum[o.num] = o.off
	}
	for n := 1; n <= highest; n++ {
		off, ok := byNum[n]
		if !ok {
			out.WriteString("0000000000 65535 f \n")
			continue
		}
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	out.WriteString(trailer)
	out.WriteString("\nstartxref\n")
	fmt.Fprintf(&out, "%d\n", xrefOffset)
	out.WriteString("%%EOF\n")
	return out.Bytes(), nil
}
