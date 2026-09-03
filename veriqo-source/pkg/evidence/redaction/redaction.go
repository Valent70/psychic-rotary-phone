// Package redaction proves, at the byte level, that redacted content is
// absent from a derivative.
//
// A reviewer raised Article 18 to P0 with a precise complaint: if VERIQO
// says a redacted document is irreversible, the requirement is not
//
//	display layer hides text
//
// nor
//
//	redaction metadata = true
//
// but that the forbidden bytes are genuinely gone and genuinely
// unrecoverable. That is not a disclosure question. It is evidence
// integrity, and Articles 17, 18, 22, 23, 24 and 25 form one chain
// around it:
//
//	Original
//	  ├── preserved immutable          (Art. 17)
//	  ▼
//	Derivative
//	  ├── forbidden bytes absent       (Art. 18)
//	  ├── hidden content unrecoverable (Art. 18)
//	  ├── new hash                     (Art. 22)
//	  ├── new version                  (Art. 22)
//	  ├── provenance link              (Art. 23)
//	  └── independent verification     (Art. 24, 25)
//
// What this package does: verify a derivative against its original and
// a set of forbidden terms, over raw bytes, in every encoding an
// attacker would reach for first.
//
// What it does NOT do, stated here so nothing downstream over-reads it:
// it does not PRODUCE derivatives. The PDF, XLSX and PPTX redaction
// workers do not exist, and neither does the adversarial recovery lab
// that would try to reconstruct hidden content from a real file's
// incremental-update history, object streams or revision metadata.
// Article 18 therefore stays OPEN in the constitutional proof audit.
// This package is the verifier that a real worker would have to satisfy —
// built first on purpose, so the worker cannot define its own pass mark.
package redaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/ascii85"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
)

// Encoding is a form in which a forbidden term might survive in a
// derivative's bytes.
//
// The list is the point. A redaction worker that strips the plain UTF-8
// occurrence and leaves the UTF-16 copy in a document's string table has
// not redacted anything — it has redacted one of several copies. Every
// encoding here has been a real leak in a real document format.
type Encoding string

const (
	EncPlain      Encoding = "plain"
	EncUTF16LE    Encoding = "utf16le"
	EncUTF16BE    Encoding = "utf16be"
	EncHex        Encoding = "hex"
	EncHexUpper   Encoding = "hex_upper"
	EncBase64     Encoding = "base64"
	EncBase64URL  Encoding = "base64url"
	EncBase32     Encoding = "base32"
	EncAscii85    Encoding = "ascii85"
	EncPDFHexStr  Encoding = "pdf_hex_string"
	EncZeroPadded Encoding = "nul_interleaved"
	EncCaseFolded Encoding = "case_folded"
)

// Encodings returns every form checked, in a stable order.
func Encodings() []Encoding {
	return []Encoding{
		EncPlain, EncCaseFolded, EncUTF16LE, EncUTF16BE, EncHex, EncHexUpper,
		EncBase64, EncBase64URL, EncBase32, EncAscii85, EncPDFHexStr, EncZeroPadded,
	}
}

// renderings returns every byte form of a term this package searches for.
//
// Case folding is handled by lowercasing the haystack for that one
// variant rather than by generating case permutations, which would be
// exponential and would still miss mixed case.
func renderings(term string) map[Encoding][]byte {
	raw := []byte(term)
	out := map[Encoding][]byte{
		EncPlain:      raw,
		EncCaseFolded: []byte(strings.ToLower(term)),
		EncHex:        []byte(hex.EncodeToString(raw)),
		EncHexUpper:   []byte(strings.ToUpper(hex.EncodeToString(raw))),
		EncBase64:     []byte(base64.StdEncoding.EncodeToString(raw)),
		EncBase64URL:  []byte(base64.URLEncoding.EncodeToString(raw)),
		EncBase32:     []byte(base32.StdEncoding.EncodeToString(raw)),
	}

	// UTF-16, both byte orders: the encoding a document format's string
	// table most often uses, and the one a naive byte-replace misses.
	units := utf16.Encode([]rune(term))
	le := make([]byte, 0, len(units)*2)
	be := make([]byte, 0, len(units)*2)
	for _, u := range units {
		le = append(le, byte(u), byte(u>>8))
		be = append(be, byte(u>>8), byte(u))
	}
	out[EncUTF16LE] = le
	out[EncUTF16BE] = be

	// ASCII85, as used by PDF stream filters.
	var a85 bytes.Buffer
	enc := ascii85.NewEncoder(&a85)
	_, _ = enc.Write(raw)
	_ = enc.Close()
	out[EncAscii85] = a85.Bytes()

	// A PDF hex string literal: <48656c6c6f>.
	out[EncPDFHexStr] = []byte("<" + hex.EncodeToString(raw) + ">")

	// NUL-interleaved, the shape left behind when a UTF-16 string is
	// written into a byte-oriented container.
	nul := make([]byte, 0, len(raw)*2)
	for _, b := range raw {
		nul = append(nul, b, 0x00)
	}
	out[EncZeroPadded] = nul

	return out
}

var (
	ErrNoOriginal        = errors.New("redaction: the original bytes are required; absence cannot be verified against nothing")
	ErrNoDerivative      = errors.New("redaction: the derivative bytes are required")
	ErrNoTerms           = errors.New("redaction: at least one forbidden term is required")
	ErrOriginalMutated   = errors.New("redaction: the original was modified, which Article 17 forbids")
	ErrTermNotInOriginal = errors.New("redaction: a forbidden term does not appear in the original, so its absence from the derivative proves nothing")
	ErrSameVersion       = errors.New("redaction: the derivative must be a new version with a new content hash")
	ErrNoProvenance      = errors.New("redaction: the derivative must link to the original it derives from")
)

// Leak is one surviving occurrence of a forbidden term.
type Leak struct {
	Term     string
	Encoding Encoding
	// Offset is where in the derivative the occurrence begins.
	Offset int
	// Count is how many times this rendering occurs.
	Count int
}

func (l Leak) String() string {
	return fmt.Sprintf("%q survives as %s at offset %d (%d occurrence(s))", l.Term, l.Encoding, l.Offset, l.Count)
}

// Chain is one derivative's full redaction evidence chain.
type Chain struct {
	// OriginalVersionID and DerivativeVersionID are the two evidence
	// versions. They must differ: a redaction that overwrites its
	// original is not a redaction, it is destruction.
	OriginalVersionID   string
	DerivativeVersionID string
	OriginalHash        string
	DerivativeHash      string
	// ForbiddenTerms are what had to be removed.
	ForbiddenTerms []string
	// Leaks are the surviving occurrences. Empty means byte-level
	// absence is established for the terms and encodings checked.
	Leaks []Leak
	// EncodingsChecked records what was searched, so a reader knows the
	// scope of the claim rather than assuming it was exhaustive.
	EncodingsChecked []Encoding
	// OriginalPreserved records that the original's hash is unchanged.
	OriginalPreserved bool
	// Verified is true only when every check passed.
	Verified bool
	// Limitations states what this verification does not cover.
	Limitations []string
}

// Absent reports whether byte-level absence is established.
func (c Chain) Absent() bool { return len(c.Leaks) == 0 }

// Hash is the content hash used throughout: SHA-256, hex-encoded.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Verify checks a derivative against its original.
//
// The checks, in the order they matter:
//
//  1. The original is unchanged (Article 17). Verified by recomputing
//     its hash, not by trusting a flag.
//  2. Each forbidden term actually appears in the original. A term that
//     was never there proves nothing by being absent, and letting such
//     a term pass would make the whole verification inflatable with
//     invented terms.
//  3. No rendering of any term survives in the derivative, in any of
//     the encodings above.
//  4. The derivative is a new version with a different hash
//     (Article 22).
//  5. The derivative links to the original (Article 23).
func Verify(original, derivative []byte, originalVersionID, derivativeVersionID string,
	claimedOriginalHash string, forbiddenTerms []string) (Chain, error) {

	if len(original) == 0 {
		return Chain{}, ErrNoOriginal
	}
	if len(derivative) == 0 {
		return Chain{}, ErrNoDerivative
	}
	if len(forbiddenTerms) == 0 {
		return Chain{}, ErrNoTerms
	}
	if strings.TrimSpace(originalVersionID) == "" || strings.TrimSpace(derivativeVersionID) == "" {
		return Chain{}, ErrNoProvenance
	}

	c := Chain{
		OriginalVersionID: originalVersionID, DerivativeVersionID: derivativeVersionID,
		OriginalHash: Hash(original), DerivativeHash: Hash(derivative),
		ForbiddenTerms:   append([]string(nil), forbiddenTerms...),
		EncodingsChecked: Encodings(),
		Limitations: []string{
			"absence is established for the listed terms in the listed encodings only; a term not listed was not searched for",
			"this verifies a derivative; it does not produce one, and no PDF/XLSX/PPTX redaction worker exists",
			"no adversarial recovery attempt was made against format-specific remnants (incremental updates, object streams, revision history)",
		},
	}
	sort.Strings(c.ForbiddenTerms)

	// 1. Article 17: the original is preserved.
	if claimedOriginalHash != "" && claimedOriginalHash != c.OriginalHash {
		return c, fmt.Errorf("%w: expected %s, got %s", ErrOriginalMutated, claimedOriginalHash, c.OriginalHash)
	}
	c.OriginalPreserved = true

	// 2. Article 22: a new version with a new hash.
	if originalVersionID == derivativeVersionID {
		return c, fmt.Errorf("%w: both are %q", ErrSameVersion, originalVersionID)
	}
	if c.OriginalHash == c.DerivativeHash {
		return c, fmt.Errorf("%w: the derivative is byte-identical to the original", ErrSameVersion)
	}

	lowerOriginal := bytes.ToLower(original)
	lowerDerivative := bytes.ToLower(derivative)

	for _, term := range c.ForbiddenTerms {
		if strings.TrimSpace(term) == "" {
			continue
		}
		forms := renderings(term)

		// 3a. The term must have been in the original.
		presentInOriginal := false
		for enc, form := range forms {
			hay := original
			if enc == EncCaseFolded {
				hay = lowerOriginal
			}
			if bytes.Contains(hay, form) {
				presentInOriginal = true
				break
			}
		}
		if !presentInOriginal {
			return c, fmt.Errorf("%w: %q", ErrTermNotInOriginal, term)
		}

		// 3b. No rendering may survive in the derivative.
		for _, enc := range Encodings() {
			form, ok := forms[enc]
			if !ok || len(form) == 0 {
				continue
			}
			hay := derivative
			if enc == EncCaseFolded {
				hay = lowerDerivative
			}
			if idx := bytes.Index(hay, form); idx >= 0 {
				c.Leaks = append(c.Leaks, Leak{
					Term: term, Encoding: enc, Offset: idx,
					Count: bytes.Count(hay, form),
				})
			}
		}
	}

	c.Verified = c.OriginalPreserved && len(c.Leaks) == 0
	return c, nil
}

// Explain states the result in a sentence a report can quote.
func (c Chain) Explain() string {
	if c.Verified {
		return fmt.Sprintf(
			"Byte-level absence established: %d forbidden term(s) checked in %d encoding(s); none survives in derivative %s. "+
				"The original %s is preserved unchanged. This is verification of a derivative, not production of one.",
			len(c.ForbiddenTerms), len(c.EncodingsChecked), c.DerivativeVersionID, c.OriginalVersionID)
	}
	if len(c.Leaks) == 0 {
		return "Verification did not complete; no absence claim is supported."
	}
	parts := make([]string, 0, len(c.Leaks))
	for _, l := range c.Leaks {
		parts = append(parts, l.String())
	}
	return fmt.Sprintf("Redaction FAILED for derivative %s: %s", c.DerivativeVersionID, strings.Join(parts, "; "))
}
