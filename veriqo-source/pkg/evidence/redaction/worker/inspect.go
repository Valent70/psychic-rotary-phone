package worker

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"sort"
)

// Inspectable returns the bytes a redaction claim must actually be
// verified against.
//
// This function is the reason this package exists rather than a
// three-line call to redaction.Verify. Every format here compresses its
// content. A forbidden term sitting in plain sight inside a spreadsheet
// cell does not appear anywhere in the .xlsx file's bytes, because
// those bytes are deflated. Verifying the container would therefore
// report absence for a document where nothing was removed -- a
// verification that cannot fail, which is worse than no verification,
// because it produces a signed record saying the check passed.
//
// The view returned is the concatenation of:
//
//   - every decompressed part of the container, and
//   - the container's own bytes.
//
// The second half matters as much as the first. A zip entry can be
// STOREd rather than deflated, PDF metadata sits outside content
// streams, and a term can hide in a filename. Concatenating both means
// a term has to be absent from the content AND from the envelope.
func Inspectable(kind Kind, container []byte) ([]byte, error) {
	switch kind {
	case KindXLSX, KindPPTX:
		return inspectableOOXML(container)
	case KindPDF:
		return inspectablePDF(container)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedKind, kind)
	}
}

// inspectableOOXML decompresses every part of an Office Open XML
// package. Part names are included as well as part contents: a term in
// a filename is still a term in the document.
func inspectableOOXML(container []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(container), int64(len(container)))
	if err != nil {
		return nil, fmt.Errorf("reading the OOXML package: %w", err)
	}
	var buf bytes.Buffer
	names := make([]string, 0, len(zr.File))
	parts := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("opening part %q: %w", f.Name, err)
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxPartBytes))
		closeErr := rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading part %q: %w", f.Name, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing part %q: %w", f.Name, closeErr)
		}
		names = append(names, f.Name)
		parts[f.Name] = body
	}
	// Deterministic order: two runs over the same package must produce
	// the same view, or the hash of the view means nothing.
	sort.Strings(names)
	for _, n := range names {
		buf.WriteString(n)
		buf.WriteByte('\n')
		buf.Write(parts[n])
		buf.WriteByte('\n')
	}
	buf.Write(container)
	return buf.Bytes(), nil
}

// maxPartBytes caps a single decompressed part. A zip bomb is a denial
// of service, and a redaction worker that can be made to allocate
// without bound is a redaction worker an adversary controls.
const maxPartBytes = 64 << 20

// flateStream matches a PDF stream declared FlateDecode, capturing the
// compressed body.
var flateStream = regexp.MustCompile(`(?s)/Filter\s*/FlateDecode.*?stream\r?\n(.*?)\r?\nendstream`)

// inspectablePDF decompresses every FlateDecode stream in a PDF and
// appends the file itself.
//
// A stream that will not inflate is not an error here. This function's
// job is to widen what the verifier can see, never to narrow it: if a
// stream cannot be decompressed, its compressed bytes are still in the
// view via the container, and the worker -- not this function --
// decides whether a document it cannot fully read may be released.
func inspectablePDF(container []byte) ([]byte, error) {
	var buf bytes.Buffer
	for _, m := range flateStream.FindAllSubmatch(container, -1) {
		if len(m) < 2 {
			continue
		}
		zr, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(zr, maxPartBytes))
		_ = zr.Close()
		if err != nil {
			continue
		}
		buf.Write(body)
		buf.WriteByte('\n')
	}
	buf.Write(container)
	return buf.Bytes(), nil
}
