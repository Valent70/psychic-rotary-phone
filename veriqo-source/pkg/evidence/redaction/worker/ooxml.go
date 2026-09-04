package worker

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
)

// The OOXML workers.
//
// An .xlsx or .pptx is a zip of XML parts. Cell and slide text lives in
// those parts, and in an .xlsx most of it lives in one shared string
// table rather than in the sheets. Redaction therefore means rewriting
// parts and rebuilding the package, not editing a document object.
//
// What this worker does NOT attempt, and refuses rather than pretends:
// embedded objects it cannot read as text (images, OLE, embedded
// workbooks), and any part whose content type it does not recognise but
// which contains a forbidden term. See unaccountedParts.

// ooxmlRedactor is shared by the workbook and presentation workers.
// They differ only in the format they declare and the parts they expect
// to carry text; the mechanism is identical, and giving each its own
// copy of the mechanism would be a duplicate authority on what
// redacting an OOXML package means.
type ooxmlRedactor struct {
	kind Kind
	name string
}

// NewXLSXRedactor returns the workbook worker.
func NewXLSXRedactor() Redactor {
	return ooxmlRedactor{kind: KindXLSX, name: "veriqo/pkg/evidence/redaction/worker.ooxmlRedactor(XLSX)"}
}

// NewPPTXRedactor returns the presentation worker.
func NewPPTXRedactor() Redactor {
	return ooxmlRedactor{kind: KindPPTX, name: "veriqo/pkg/evidence/redaction/worker.ooxmlRedactor(PPTX)"}
}

func (r ooxmlRedactor) Kind() Kind { return r.kind }

// textualPart reports whether a part is one whose bytes this worker can
// read and rewrite as text.
func textualPart(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".xml", ".rels", ".txt", ".vml"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// opaquePart reports whether a part is a known binary attachment that
// this worker deliberately does not parse. Listing them explicitly is
// what lets the worker distinguish "a picture, which I will refuse to
// certify if it contains the term" from "something I have never seen".
func opaquePart(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{
		".png", ".jpg", ".jpeg", ".gif", ".bmp", ".tiff", ".emf", ".wmf",
		".bin", ".xlsx", ".docx", ".pptx", ".zip", ".mp3", ".mp4", ".wav",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func (r ooxmlRedactor) Redact(original []byte, terms []string, marker string) ([]byte, TransformManifest, error) {
	m := TransformManifest{
		Kind:            r.kind,
		Worker:          r.name,
		Replacements:    map[string]int{},
		RedactionMarker: marker,
	}

	zr, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		return nil, m, fmt.Errorf("redaction/worker: reading the %s package: %w", r.kind, err)
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)

	for _, f := range zr.File {
		m.PartsInspected = append(m.PartsInspected, f.Name)

		rc, err := f.Open()
		if err != nil {
			return nil, m, fmt.Errorf("redaction/worker: opening part %q: %w", f.Name, err)
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxPartBytes))
		closeErr := rc.Close()
		if err != nil {
			return nil, m, fmt.Errorf("redaction/worker: reading part %q: %w", f.Name, err)
		}
		if closeErr != nil {
			return nil, m, fmt.Errorf("redaction/worker: closing part %q: %w", f.Name, closeErr)
		}

		switch {
		case textualPart(f.Name):
			replaced, counts := replaceAll(body, terms, marker)
			total := 0
			for term, n := range counts {
				m.Replacements[term] += n
				total += n
			}
			if total > 0 {
				m.PartsModified = append(m.PartsModified, f.Name)
			}
			body = replaced
		default:
			// A part this worker cannot rewrite. If it carries a
			// forbidden term, the worker refuses: releasing it would
			// mean claiming absence for content that was never
			// examined.
			if hit := containsAnyTerm(body, terms); hit != "" {
				what := "an unrecognised part"
				if opaquePart(f.Name) {
					what = "a binary attachment"
				}
				m.Unaccounted = append(m.Unaccounted,
					fmt.Sprintf("part %q is %s carrying the forbidden term %q; this worker cannot redact it", f.Name, what, hit))
			}
		}

		hdr, err := zip.FileInfoHeader(f.FileInfo())
		if err != nil {
			return nil, m, fmt.Errorf("redaction/worker: header for %q: %w", f.Name, err)
		}
		hdr.Name = f.Name
		hdr.Method = zip.Deflate
		// A fixed modified time keeps the derivative deterministic:
		// two runs over the same original must produce the same bytes,
		// or the derivative's hash is not a property of its content.
		hdr.Modified = fixedModTime
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, m, fmt.Errorf("redaction/worker: writing %q: %w", f.Name, err)
		}
		if _, err := w.Write(body); err != nil {
			return nil, m, fmt.Errorf("redaction/worker: writing %q: %w", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, m, fmt.Errorf("redaction/worker: closing the %s package: %w", r.kind, err)
	}

	sort.Strings(m.PartsInspected)
	sort.Strings(m.PartsModified)
	return out.Bytes(), m, nil
}
