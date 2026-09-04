// Package worker redacts documents and records what it did.
//
// # The single rule
//
// A redacted document is a NEW EVIDENCE VERSION, never a modified
// original. The original bytes are never touched, the derivative
// carries both hashes, and the manifest states the transformation --
// including transformations the redaction itself required, such as
// normalising a PDF 1.5+ structure into a classic one.
//
// # Refusal is a first-class outcome
//
// A worker that cannot see a document's text cannot assert that a term
// is absent from it. For those documents the worker REFUSES, and the
// refusal is reported as a refusal rather than as a clean result:
//
//	encrypted documents         the bytes are not readable
//	LZW-filtered streams        the filter is not decoded here
//	incremental updates         earlier revisions persist in the file,
//	                            so redacting the visible revision
//	                            leaves the superseded one intact
//	structurally malformed      partial processing of a file we cannot
//	                            parse is worse than no processing
//	binary embedded parts       a term inside a PNG is not text
//
// Every one of those is a real gap and none of them is a defect. The
// distinction is load-bearing: FC-008 was a coverage ratio being read
// as a pass rate, and the corpus reports them separately for that
// reason.
//
// # Verification is not the same as redaction
//
// After producing a derivative the worker re-derives its INSPECTABLE
// VIEW -- every stream inflated, every part extracted -- and searches
// that for the forbidden terms. Searching the raw derivative bytes
// would be FC-002 (VACUOUS_VERIFICATION): a compressed container hides
// the term from a byte search, so the search passes and the term is
// still there.
package worker

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"veriqo/pkg/canonical/jcs"
)

// Kind is a document format.
type Kind string

const (
	KindPDF  Kind = "PDF"
	KindXLSX Kind = "XLSX"
	KindPPTX Kind = "PPTX"
)

func (k Kind) Valid() bool {
	switch k {
	case KindPDF, KindXLSX, KindPPTX:
		return true
	}
	return false
}

// maxPartBytes bounds any single decompressed part. A crafted stream
// that inflates without limit is a denial-of-service primitive, and
// "we ran out of memory" is not a refusal a caller can act on.
const maxPartBytes = 64 << 20

// Redaction replaces a term with the same number of bytes, so that
// offsets inside uncompressed streams stay valid and the derivative's
// layout is not silently reflowed.
const redactionByte = 'X'

// Refusal is a designed decline, not a defect.
type Refusal struct {
	Structure string
	Reason    string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("redaction/worker: refused: %s (%s)", r.Reason, r.Structure)
}

// IsRefusal distinguishes a refusal from a failure. Callers that treat
// every non-nil error the same way reintroduce the confusion the
// corpus exists to measure.
func IsRefusal(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}

func refuse(structure, reason string) error {
	return &Refusal{Structure: structure, Reason: reason}
}

// TransformManifest records what was done to produce a derivative.
type TransformManifest struct {
	Kind             Kind   `json:"kind"`
	Algorithm        string `json:"algorithm"`
	OriginalSHA256   string `json:"original_sha256"`
	DerivativeSHA256 string `json:"derivative_sha256"`
	TermCount        int    `json:"term_count"`
	Replacements     int    `json:"replacements"`
	PartsProcessed   int    `json:"parts_processed"`

	// Normalization states structural changes the redaction required,
	// so a reader of the release chain can see that the derivative's
	// structure differs from the original's, and why.
	Normalization []string `json:"normalization,omitempty"`

	// Verified is true only when the derivative's INSPECTABLE VIEW was
	// re-derived and searched. It is not "we redacted successfully".
	Verified bool `json:"verified"`

	// Limits states what this redaction does NOT cover. An ADEQUATE
	// result with no stated limits is read as complete by every reader
	// in a hurry.
	Limits []string `json:"limits,omitempty"`
}

// Redact produces a derivative with every term replaced, and verifies
// the result against the derivative's inspectable view.
//
// The input slice is never modified.
func Redact(kind Kind, doc []byte, terms []string) ([]byte, TransformManifest, error) {
	m := TransformManifest{Kind: kind, Algorithm: "veriqo/redact/v2",
		OriginalSHA256: jcs.HashBytes(doc), TermCount: len(terms)}
	if !kind.Valid() {
		return nil, m, fmt.Errorf("redaction/worker: unknown kind %q", kind)
	}
	if len(terms) == 0 {
		return nil, m, errors.New("redaction/worker: no terms; a redaction with nothing to redact is not a redaction")
	}
	for _, t := range terms {
		if t == "" {
			return nil, m, errors.New("redaction/worker: an empty term would match everywhere")
		}
	}

	var (
		out []byte
		err error
	)
	switch kind {
	case KindPDF:
		out, err = redactPDF(doc, terms, &m)
	default:
		out, err = redactOOXML(kind, doc, terms, &m)
	}
	if err != nil {
		return nil, m, err
	}

	m.DerivativeSHA256 = jcs.HashBytes(out)

	// FC-002: verify the INSPECTABLE view, never the raw bytes.
	view, verr := Inspect(kind, out)
	if verr != nil {
		return nil, m, fmt.Errorf("redaction/worker: the derivative could not be inspected, "+
			"so its cleanliness cannot be asserted: %w", verr)
	}
	for _, t := range terms {
		if containsFold(view, []byte(t)) {
			return nil, m, fmt.Errorf(
				"redaction/worker: LEAK: the term survived into the derivative's inspectable view")
		}
	}
	m.Verified = true
	m.Limits = append(m.Limits,
		"verification searched the derivative's inspectable view; content this worker cannot "+
			"decode is outside that view and is refused rather than reported clean",
		"visual redaction of rendered page content is not attempted; this replaces text, it does "+
			"not prove a rasterised glyph is unrecoverable")
	return out, m, nil
}

// containsFold is a case-insensitive byte search. Redaction that only
// matched the exact case would leave "ACME" behind when told "Acme".
func containsFold(hay, needle []byte) bool {
	return bytes.Contains(bytes.ToLower(hay), bytes.ToLower(needle))
}

// replaceFold replaces every case-insensitive occurrence with an equal
// number of redactionByte, and reports how many.
func replaceFold(in []byte, terms []string) ([]byte, int) {
	out := append([]byte(nil), in...)
	lower := bytes.ToLower(out)
	n := 0
	for _, t := range terms {
		needle := bytes.ToLower([]byte(t))
		from := 0
		for {
			i := bytes.Index(lower[from:], needle)
			if i < 0 {
				break
			}
			at := from + i
			for j := at; j < at+len(needle); j++ {
				out[j] = redactionByte
				lower[j] = redactionByte
			}
			from = at + len(needle)
			n++
		}
	}
	return out, n
}

// --- PDF ---------------------------------------------------------------

var (
	// pdfObject matches one complete indirect object. The submatches
	// are: 2,3 = object number; 4,5 = generation; 6,7 = the body.
	//
	// The non-greedy body and the anchored "endobj" are what keep this
	// from matching across object boundaries -- the bug called out in
	// objstm.go, where a wider pattern matched one object's dictionary
	// against a later object's stream.
	pdfObject = regexp.MustCompile(`(?s)(\d+)\s+(\d+)\s+obj\b(.*?)\bendobj`)

	// pdfStreamBody matches a stream payload inside one object block.
	pdfStreamBody = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)

	pdfIsFlate = regexp.MustCompile(`/Filter\s*(/FlateDecode\b|\[\s*/FlateDecode\s*\])`)
	pdfIsLZW   = regexp.MustCompile(`/LZWDecode\b`)
	pdfLength  = regexp.MustCompile(`/Length\s+\d+`)

	// pdfObjStm and pdfXRefStream detect the 1.5+ structures at
	// document level. objstm.go re-tests each candidate against a
	// single object's body before acting on it.
	pdfObjStm     = regexp.MustCompile(`/Type\s*/ObjStm\b`)
	pdfXRefStream = regexp.MustCompile(`/Type\s*/XRef\b`)

	pdfEncrypt   = regexp.MustCompile(`/Encrypt\s+\d+\s+\d+\s+R|/Encrypt\s*<<`)
	pdfEOF       = regexp.MustCompile(`%%EOF`)
	pdfStartXref = regexp.MustCompile(`startxref`)

	// pdfLiteralString matches a (…) string in a dictionary, which is
	// where document metadata lives.
	pdfLiteralString = regexp.MustCompile(`\((?:[^()\\]|\\.)*\)`)

	pdfTrailerDict = regexp.MustCompile(`(?s)trailer\s*<<(.*?)>>`)
)

func redactPDF(doc []byte, terms []string, m *TransformManifest) ([]byte, error) {
	if !bytes.HasPrefix(doc, []byte("%PDF-")) {
		return nil, refuse("PDF-MALFORMED",
			"the file does not begin with a %PDF- header, so it cannot be parsed as a PDF")
	}
	if pdfEncrypt.Match(doc) {
		return nil, refuse("PDF-ENCRYPTED",
			"the document declares /Encrypt; its content is not readable, so absence of a term "+
				"cannot be asserted")
	}
	if pdfIsLZW.Match(doc) {
		return nil, refuse("PDF-LZW",
			"the document uses /LZWDecode, which this worker does not decode; content it cannot "+
				"read is content it cannot certify")
	}
	// Incremental updates: more than one %%EOF or more than one
	// startxref means earlier revisions of every object are still in
	// the file. Redacting the current revision leaves the superseded
	// one intact and fully recoverable.
	if len(pdfEOF.FindAll(doc, -1)) > 1 || len(pdfStartXref.FindAll(doc, -1)) > 1 {
		return nil, refuse("PDF-INCREMENTAL",
			"the document carries incremental updates; superseded revisions of the redacted "+
				"objects remain in the file and are trivially recoverable")
	}

	normalized, norm, err := normalizePDF(doc)
	if err != nil {
		return nil, refuse("PDF-MALFORMED", err.Error())
	}
	m.Normalization = norm.describe()

	locs := pdfObject.FindAllSubmatchIndex(normalized, -1)
	if len(locs) == 0 {
		return nil, refuse("PDF-MALFORMED", "no indirect objects were found in the document")
	}

	type object struct {
		num  int
		body []byte
	}
	var objects []object
	replacements := 0

	for _, loc := range locs {
		num, err := strconv.Atoi(string(normalized[loc[2]:loc[3]]))
		if err != nil {
			return nil, refuse("PDF-MALFORMED", "an object carries a non-numeric number")
		}
		body := normalized[loc[6]:loc[7]]

		sloc := pdfStreamBody.FindSubmatchIndex(body)
		if sloc == nil {
			// A plain dictionary: redact its literal strings, which is
			// where /Title, /Author and /Subject live.
			red, n := redactPDFDictionary(body, terms)
			replacements += n
			objects = append(objects, object{num, red})
			m.PartsProcessed++
			continue
		}

		dict := body[:sloc[0]]
		payload := body[sloc[2]:sloc[3]]

		redDict, n := redactPDFDictionary(dict, terms)
		replacements += n

		var newPayload []byte
		if pdfIsFlate.Match(dict) {
			plain, err := inflate(payload)
			if err != nil {
				return nil, refuse("PDF-MALFORMED",
					fmt.Sprintf("object %d declares /FlateDecode and does not inflate: %v", num, err))
			}
			red, k := replaceFold(plain, terms)
			replacements += k
			newPayload, err = deflate(red)
			if err != nil {
				return nil, fmt.Errorf("redaction/worker: recompressing object %d: %w", num, err)
			}
		} else {
			red, k := replaceFold(payload, terms)
			replacements += k
			newPayload = red
		}

		// /Length must describe the payload actually written, or every
		// reader after this one is parsing from the wrong offset.
		fixed := pdfLength.ReplaceAll(redDict,
			[]byte(fmt.Sprintf("/Length %d", len(newPayload))))
		if !pdfLength.Match(redDict) {
			fixed = append(bytes.TrimRight(fixed, " \r\n"),
				[]byte(fmt.Sprintf(" /Length %d", len(newPayload)))...)
		}

		var b bytes.Buffer
		b.Write(fixed)
		b.WriteString("\nstream\n")
		b.Write(newPayload)
		b.WriteString("\nendstream")
		objects = append(objects, object{num, b.Bytes()})
		m.PartsProcessed++
	}

	m.Replacements = replacements

	// The trailer dictionary, redacted along with everything else.
	trailer := "<< /Size %d >>"
	if tm := pdfTrailerDict.FindSubmatch(normalized); tm != nil {
		inner, n := redactPDFDictionary(tm[1], terms)
		m.Replacements += n
		trailer = "<< /Size %d " + strings.TrimSpace(string(inner)) + " >>"
	}

	sort.Slice(objects, func(i, j int) bool { return objects[i].num < objects[j].num })

	// Rebuild the file with a classic cross-reference table. Offsets
	// have all moved, so patching the old table is not an option.
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	maxNum := 0
	for _, o := range objects {
		if o.num > maxNum {
			maxNum = o.num
		}
	}
	offsets := make(map[int]int, len(objects))
	for _, o := range objects {
		offsets[o.num] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj", o.num)
		out.Write(o.body)
		out.WriteString("\nendobj\n")
	}

	xrefAt := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", maxNum+1)
	out.WriteString("0000000000 65535 f \n")
	for i := 1; i <= maxNum; i++ {
		if off, ok := offsets[i]; ok {
			fmt.Fprintf(&out, "%010d 00000 n \n", off)
		} else {
			// A number with no object: a free entry, which is what the
			// specification says a gap is.
			out.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&out, "trailer\n"+trailer+"\nstartxref\n%d\n%%%%EOF\n", maxNum+1, xrefAt)
	return out.Bytes(), nil
}

// redactPDFDictionary redacts the literal strings in a dictionary,
// leaving the structure (names, numbers, references) intact. Redacting
// a /Type name would produce an unparseable document.
func redactPDFDictionary(dict []byte, terms []string) ([]byte, int) {
	n := 0
	out := pdfLiteralString.ReplaceAllFunc(dict, func(s []byte) []byte {
		red, k := replaceFold(s, terms)
		n += k
		return red
	})
	return out, n
}

func inflate(b []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		// Some producers emit a raw deflate stream with no zlib
		// header. Trying both is correct; guessing silently is not,
		// so a failure of both is reported rather than skipped.
		fr := flate.NewReader(bytes.NewReader(b))
		defer fr.Close()
		out, ferr := io.ReadAll(io.LimitReader(fr, maxPartBytes))
		if ferr != nil {
			return nil, err
		}
		return out, nil
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, maxPartBytes))
}

func deflate(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- OOXML (XLSX, PPTX) --------------------------------------------------

// binaryPart matches a part whose content this worker cannot read as
// text. A term inside a PNG is not text, and replacing bytes that
// happen to spell it would corrupt the image without redacting
// anything.
var binaryPart = regexp.MustCompile(`(?i)^(word|xl|ppt|customXml)?/?(media|embeddings)/`)

func redactOOXML(kind Kind, doc []byte, terms []string, m *TransformManifest) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return nil, refuse(string(kind)+"-MALFORMED",
			"the file is not a readable OPC package: "+err.Error())
	}

	// Refuse the whole document if any part carries content this
	// worker cannot inspect. Processing the readable parts and
	// releasing the rest would assert cleanliness of bytes nobody read.
	for _, f := range zr.File {
		if binaryPart.MatchString(f.Name) {
			return nil, refuse(string(kind)+"-EMBEDDED-BINARY",
				fmt.Sprintf("part %q is an embedded binary object; its content cannot be read "+
					"as text, so a term inside it can neither be found nor removed", f.Name))
		}
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	replacements := 0

	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, refuse(string(kind)+"-MALFORMED",
				fmt.Sprintf("part %q could not be opened: %v", f.Name, err))
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxPartBytes))
		rc.Close()
		if err != nil {
			return nil, refuse(string(kind)+"-MALFORMED",
				fmt.Sprintf("part %q could not be read: %v", f.Name, err))
		}

		red, n := redactXMLText(content, terms)
		replacements += n

		// Deterministic entries: a fixed timestamp and a fixed
		// compression level, written raw so the zip writer cannot
		// substitute either. Two redactions of the same input must
		// produce the same bytes, or the derivative's hash is a
		// function of when it was produced rather than of what it
		// contains -- and replay comparison stops meaning anything.
		comp, crc, err := rawDeflate(red)
		if err != nil {
			return nil, err
		}
		w, err := zw.CreateRaw(&zip.FileHeader{
			Name:               f.Name,
			Method:             zip.Deflate,
			Modified:           zeroTime,
			CRC32:              crc,
			CompressedSize64:   uint64(len(comp)),
			UncompressedSize64: uint64(len(red)),
		})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(comp); err != nil {
			return nil, err
		}
		m.PartsProcessed++
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	m.Replacements = replacements
	return out.Bytes(), nil
}

// redactXMLText redacts text content and attribute values while
// leaving markup alone.
//
// Redacting inside a tag name would produce invalid XML; redacting an
// XML-escaped form is why the escaped variant exists in the corpus --
// a term written as "A&amp;B" is the same term to a reader and a
// different byte string to a naive search.
func redactXMLText(content []byte, terms []string) ([]byte, int) {
	if !bytes.Contains(content, []byte("<")) {
		return replaceFold(content, terms)
	}
	expanded := make([]string, 0, len(terms)*2)
	for _, t := range terms {
		expanded = append(expanded, t, xmlEscape(t))
	}

	var out bytes.Buffer
	n := 0
	i := 0
	for i < len(content) {
		lt := bytes.IndexByte(content[i:], '<')
		if lt < 0 {
			red, k := replaceFold(content[i:], expanded)
			n += k
			out.Write(red)
			break
		}
		red, k := replaceFold(content[i:i+lt], expanded)
		n += k
		out.Write(red)

		gt := bytes.IndexByte(content[i+lt:], '>')
		if gt < 0 {
			out.Write(content[i+lt:])
			break
		}
		tag := content[i+lt : i+lt+gt+1]
		// Attribute VALUES are content; the element and attribute
		// names are structure.
		redTag, k := redactAttributeValues(tag, expanded)
		n += k
		out.Write(redTag)
		i = i + lt + gt + 1
	}
	return out.Bytes(), n
}

var xmlAttrValue = regexp.MustCompile(`"[^"]*"|'[^']*'`)

func redactAttributeValues(tag []byte, terms []string) ([]byte, int) {
	n := 0
	out := xmlAttrValue.ReplaceAllFunc(tag, func(v []byte) []byte {
		red, k := replaceFold(v, terms)
		n += k
		return red
	})
	return out, n
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// --- Inspection ----------------------------------------------------------

// Inspect returns the document's inspectable view: every stream
// inflated and every part concatenated.
//
// This is what verification searches. Searching the raw bytes instead
// is FC-002: a compressed container hides the term, the search passes,
// and the term is still in the document.
func Inspect(kind Kind, doc []byte) ([]byte, error) {
	switch kind {
	case KindPDF:
		return inspectPDF(doc)
	case KindXLSX, KindPPTX:
		return inspectOOXML(doc)
	}
	return nil, fmt.Errorf("redaction/worker: unknown kind %q", kind)
}

func inspectPDF(doc []byte) ([]byte, error) {
	var view bytes.Buffer
	for _, loc := range pdfObject.FindAllSubmatchIndex(doc, -1) {
		body := doc[loc[6]:loc[7]]
		sloc := pdfStreamBody.FindSubmatchIndex(body)
		if sloc == nil {
			view.Write(body)
			view.WriteByte('\n')
			continue
		}
		dict := body[:sloc[0]]
		payload := body[sloc[2]:sloc[3]]
		view.Write(dict)
		view.WriteByte('\n')
		if pdfIsFlate.Match(dict) {
			plain, err := inflate(payload)
			if err != nil {
				return nil, fmt.Errorf("inspecting a /FlateDecode stream: %w", err)
			}
			view.Write(plain)
		} else {
			view.Write(payload)
		}
		view.WriteByte('\n')
	}
	// The trailer is outside any object and carries metadata.
	if tm := pdfTrailerDict.FindSubmatch(doc); tm != nil {
		view.Write(tm[1])
	}
	if view.Len() == 0 {
		return nil, errors.New("the document yielded no inspectable content")
	}
	return view.Bytes(), nil
}

func inspectOOXML(doc []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(zr.File))
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		byName[f.Name] = f
	}
	sort.Strings(names)

	var view bytes.Buffer
	for _, n := range names {
		rc, err := byName[n].Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxPartBytes))
		rc.Close()
		if err != nil {
			return nil, err
		}
		view.WriteString(n)
		view.WriteByte('\n')
		view.Write(b)
		view.WriteByte('\n')
		// The escaped form must also be searched, or a term written as
		// "A&amp;B" passes a search for "A&B".
		view.Write(unescapeXML(b))
		view.WriteByte('\n')
	}
	return view.Bytes(), nil
}

func unescapeXML(b []byte) []byte {
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'")
	return []byte(r.Replace(string(b)))
}
