package worker

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
)

// PDF 1.5+ normalization: object streams and cross-reference streams.
//
// # Why this exists
//
// The corpus run reported PDF-OBJECT-STREAM and PDF-XREF-STREAM as
// REFUSED, both marked UBIQUITOUS. The review's response was the right
// one:
//
//	Object streams = ubiquitous, XRef streams = ubiquitous, tetapi
//	VERIQO = REFUSED. Maka secara praktis VERIQO belum bisa menyatakan
//	redaction assurance untuk bagian penting dari PDF ecosystem.
//	Safety = strong. Coverage = incomplete.
//
// Refusing was safe and it was not enough. Since PDF 1.5 the dominant
// output shape puts most non-stream objects INSIDE a compressed object
// stream and replaces the cross-reference table with a cross-reference
// stream. A redactor that declines both declines most modern PDFs.
//
// # The approach: normalize, then redact
//
// Rather than teach the redactor to rewrite objects in place inside a
// compressed container -- which would mean recomputing an xref stream's
// binary offsets after every substitution, a fragile design -- this
// normalizes the document first:
//
//	1. every /ObjStm is inflated and its contained objects are lifted
//	   out as ordinary top-level "N 0 obj ... endobj" objects;
//	2. the /ObjStm and /XRef stream objects are dropped, since after
//	   lifting they describe nothing;
//	3. the trailer dictionary is reconstructed from the xref stream's
//	   own dictionary, which is where /Root and /Info live in a 1.5+
//	   file.
//
// The result is a structurally classic PDF carrying the same objects.
// The existing redactor then works on it unchanged, and rebuildXref
// emits a normal table.
//
// # What this changes about the derivative, stated plainly
//
// The derivative is NOT byte-structurally the same shape as the
// original: a 1.5+ input produces a 1.4-shaped output. That is a real
// transformation and it is recorded in the manifest rather than hidden.
// It is the honest trade: a reader gets a document whose redaction can
// be verified, instead of no document at all.
//
// It also has a security property worth stating: lifting objects out of
// a compressed container means the redactor SEES them. An object left
// inside an ObjStm the worker could not open is an object whose content
// nobody checked, which is the condition that made refusal correct in
// the first place.

var (
	pdfNKey     = regexp.MustCompile(`/N\s+(\d+)`)
	pdfFirstKey = regexp.MustCompile(`/First\s+(\d+)`)
	pdfRootKey  = regexp.MustCompile(`/Root\s+(\d+\s+\d+\s+R)`)
	pdfInfoKey  = regexp.MustCompile(`/Info\s+(\d+\s+\d+\s+R)`)

	// The two type markers, matched against ONE object's body rather
	// than across the file. See the loop in normalizePDF for why that
	// distinction is load-bearing.
	pdfTypeObjStm = regexp.MustCompile(`/Type\s*/ObjStm\b`)
	pdfTypeXRef   = regexp.MustCompile(`/Type\s*/XRef\b`)
)

// normalization records what the normalizer did, for the manifest.
type normalization struct {
	// applied is true when the document needed normalizing at all.
	applied bool
	// objectStreams is how many /ObjStm containers were unpacked.
	objectStreams int
	// lifted is how many objects were lifted out of them.
	lifted int
	// xrefStreams is how many /XRef stream objects were replaced.
	xrefStreams int
	// notes carries per-container detail for the manifest.
	notes []string
}

// describe renders the normalization for a transformation manifest.
func (n normalization) describe() []string {
	if !n.applied {
		return nil
	}
	out := []string{fmt.Sprintf(
		"normalized a PDF 1.5+ document: unpacked %d object stream(s) lifting %d object(s), "+
			"replaced %d cross-reference stream(s) with a classic table; the derivative is "+
			"structurally a 1.4-shaped file carrying the same objects",
		n.objectStreams, n.lifted, n.xrefStreams)}
	return append(out, n.notes...)
}

// normalizePDF lifts object-stream contents to top level and removes
// the container and cross-reference stream objects.
//
// It returns the rewritten document and a description of what changed.
// A document with neither structure is returned unchanged with
// applied=false, so the common 1.4 path costs nothing.
func normalizePDF(doc []byte) ([]byte, normalization, error) {
	var n normalization
	hasObjStm := pdfObjStm.Match(doc)
	hasXRefStm := pdfXRefStream.Match(doc)
	if !hasObjStm && !hasXRefStm {
		return doc, n, nil
	}
	n.applied = true

	// Collect the objects lifted out of every object stream, and the
	// byte ranges of the container objects to drop.
	type dropRange struct{ from, to int }
	var drops []dropRange
	var lifted bytes.Buffer

	// Walk COMPLETE objects rather than pattern-matching across the
	// file. An earlier version used one regex spanning
	// "obj ... stream ... endstream endobj", which happily matched from
	// one object's dictionary to a LATER object's stream -- so a
	// document merely MENTIONING /ObjStm looked like an object stream,
	// and the container it "found" was somebody else's content stream.
	// The object boundary is the unit; anything wider guesses.
	root, info := "", ""
	for _, loc := range pdfObject.FindAllSubmatchIndex(doc, -1) {
		objNum := string(doc[loc[2]:loc[3]])
		block := doc[loc[0]:loc[1]]
		inner := doc[loc[6]:loc[7]]

		isObjStm := pdfTypeObjStm.Match(inner)
		isXRef := pdfTypeXRef.Match(inner)
		if !isObjStm && !isXRef {
			continue
		}

		if isXRef {
			// The cross-reference stream carries /Root and /Info.
			// Capture them before dropping it: a derivative with no
			// /Root is not a document.
			if m := pdfRootKey.FindSubmatch(inner); m != nil && root == "" {
				root = string(m[1])
			}
			if m := pdfInfoKey.FindSubmatch(inner); m != nil && info == "" {
				info = string(m[1])
			}
			n.xrefStreams++
			drops = append(drops, dropRange{loc[0], loc[1]})
			continue
		}

		sloc := pdfStreamBody.FindSubmatchIndex(block)
		if sloc == nil {
			return nil, n, fmt.Errorf(
				"redaction/worker: object %s declares /Type /ObjStm but carries no stream", objNum)
		}
		dict := block[:sloc[0]]
		body := block[sloc[2]:sloc[3]]

		objects, err := unpackObjectStream(dict, body)
		if err != nil {
			// A container this worker cannot open holds objects nobody
			// examined. Dropping it deletes them from the derivative;
			// keeping it leaves unchecked content in a document VERIQO
			// would be asserting is clean.
			return nil, n, fmt.Errorf("redaction/worker: object stream %s: %w", objNum, err)
		}
		for _, o := range objects {
			fmt.Fprintf(&lifted, "%d 0 obj\n%s\nendobj\n", o.num, o.body)
			n.lifted++
		}
		n.objectStreams++
		n.notes = append(n.notes, fmt.Sprintf(
			"object stream %s contained %d object(s), all lifted to top level", objNum, len(objects)))
		drops = append(drops, dropRange{loc[0], loc[1]})
	}

	// Remove the container objects, from the end backwards so earlier
	// offsets stay valid.
	sort.Slice(drops, func(i, j int) bool { return drops[i].from > drops[j].from })
	out := append([]byte(nil), doc...)
	for _, d := range drops {
		out = append(out[:d.from], out[d.to:]...)
	}

	// Splice the lifted objects in before the trailer region.
	cut := bytes.LastIndex(out, []byte("startxref"))
	if cut < 0 {
		cut = len(out)
	}
	if t := bytes.LastIndex(out[:cut], []byte("trailer")); t >= 0 {
		cut = t
	}
	if x := bytes.LastIndex(out[:cut], []byte("xref")); x >= 0 {
		cut = x
	}
	var rebuilt bytes.Buffer
	rebuilt.Write(out[:cut])
	rebuilt.Write(lifted.Bytes())

	// A 1.5+ file has no trailer keyword; synthesise one so that
	// rebuildXref has the dictionary it needs.
	if !bytes.Contains(out, []byte("trailer")) {
		if root == "" {
			return nil, n, fmt.Errorf("redaction/worker: the cross-reference stream names no /Root, " +
				"so a classic trailer cannot be reconstructed")
		}
		fmt.Fprintf(&rebuilt, "trailer\n<< /Root %s", root)
		if info != "" {
			fmt.Fprintf(&rebuilt, " /Info %s", info)
		}
		rebuilt.WriteString(" >>\nstartxref\n0\n%%EOF\n")
	} else {
		rebuilt.Write(out[cut:])
	}
	return rebuilt.Bytes(), n, nil
}

// liftedObject is one object taken out of an object stream.
type liftedObject struct {
	num  int
	body string
}

// unpackObjectStream inflates a /ObjStm body and splits it into the
// objects it contains.
//
// The layout is fixed by the PDF specification: /N pairs of
// "objectNumber offset" integers, then /First bytes into the stream the
// object data begins, each object's data starting at its declared
// offset. Nothing here is heuristic.
func unpackObjectStream(dict, body []byte) ([]liftedObject, error) {
	nm := pdfNKey.FindSubmatch(dict)
	fm := pdfFirstKey.FindSubmatch(dict)
	if nm == nil || fm == nil {
		return nil, fmt.Errorf("the dictionary lacks /N or /First")
	}
	count, err := strconv.Atoi(string(nm[1]))
	if err != nil {
		return nil, fmt.Errorf("/N is not a number: %w", err)
	}
	first, err := strconv.Atoi(string(fm[1]))
	if err != nil {
		return nil, fmt.Errorf("/First is not a number: %w", err)
	}

	plain := body
	if pdfIsFlate.Match(dict) {
		zr, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("the container will not inflate: %w", err)
		}
		plain, err = io.ReadAll(io.LimitReader(zr, maxPartBytes))
		_ = zr.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the inflated container: %w", err)
		}
	}
	if first > len(plain) {
		return nil, fmt.Errorf("/First %d is past the end of a %d-byte container", first, len(plain))
	}

	// The header is the pair table.
	fields := bytes.Fields(plain[:first])
	if len(fields) < count*2 {
		return nil, fmt.Errorf("the pair table declares %d objects but holds %d integers", count, len(fields))
	}
	type pair struct{ num, off int }
	pairs := make([]pair, 0, count)
	for i := 0; i < count; i++ {
		num, err := strconv.Atoi(string(fields[i*2]))
		if err != nil {
			return nil, fmt.Errorf("object number %q is not an integer", fields[i*2])
		}
		off, err := strconv.Atoi(string(fields[i*2+1]))
		if err != nil {
			return nil, fmt.Errorf("offset %q is not an integer", fields[i*2+1])
		}
		pairs = append(pairs, pair{num: num, off: off})
	}

	data := plain[first:]
	out := make([]liftedObject, 0, count)
	for i, p := range pairs {
		start := p.off
		end := len(data)
		if i+1 < len(pairs) {
			end = pairs[i+1].off
		}
		if start < 0 || start > len(data) || end < start || end > len(data) {
			return nil, fmt.Errorf("object %d declares the byte range [%d,%d] in a %d-byte body",
				p.num, start, end, len(data))
		}
		out = append(out, liftedObject{num: p.num, body: string(bytes.TrimSpace(data[start:end]))})
	}
	return out, nil
}

// NormalizeForTest exposes the normalizer so a corpus fixture can prove
// it is genuine: that its object stream really contains the forbidden
// term, and that unpacking is what makes the term visible.
//
// It exists because the alternative is a fixture nobody can check. A
// container that turned out to be empty would make every ACCEPTED
// result over it meaningless, and the test asserting otherwise would
// pass.
func NormalizeForTest(doc []byte) ([]byte, bool, error) {
	out, n, err := normalizePDF(doc)
	return out, n.applied, err
}
