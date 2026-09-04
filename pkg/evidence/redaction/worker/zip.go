package worker

import (
	"bytes"
	"compress/flate"
	"hash/crc32"
	"time"
)

// zeroTime is the fixed modification time written into every OOXML
// part of a derivative.
//
// Zip entries carry a timestamp. If it came from the clock, two
// redactions of the same input would produce different bytes and
// therefore different hashes -- which would make the derivative's
// digest a function of when it was produced rather than of what it
// contains. That breaks replay comparison and it breaks any claim that
// two parties independently produced the same derivative.
var zeroTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// rawDeflate compresses a part and returns the payload and its CRC-32,
// for a raw zip entry written with a fixed header.
func rawDeflate(b []byte) ([]byte, uint32, error) {
	var buf bytes.Buffer
	// A fixed compression level, for the same determinism reason as
	// the fixed timestamp: the default level is allowed to change
	// between Go releases.
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, 0, err
	}
	if _, err := w.Write(b); err != nil {
		return nil, 0, err
	}
	if err := w.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), crc32.ChecksumIEEE(b), nil
}
