package redaction

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

const secret = "Alicia Fernandez"

func original() []byte {
	return []byte("SURVEY REPORT\nInspector: " + secret + "\nFindings: cargo sound on discharge.\n")
}

// clean is a derivative with the name properly removed.
func clean() []byte {
	return bytes.ReplaceAll(original(), []byte(secret), []byte("[REDACTED]"))
}

// TestProperRedactionVerifies is the happy path, and the only one that
// should ever produce an absence claim.
func TestProperRedactionVerifies(t *testing.T) {
	orig := original()
	c, err := Verify(orig, clean(), "EV-1-v1", "EV-1-v2", Hash(orig), []string{secret})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !c.Verified || !c.Absent() {
		t.Fatalf("a proper redaction must verify: %s", c.Explain())
	}
	if !c.OriginalPreserved {
		t.Fatal("the original must be recorded as preserved")
	}
	if !strings.Contains(c.Explain(), "not production of one") {
		t.Fatalf("the explanation must state the scope, got %q", c.Explain())
	}
}

// TestVisualOnlyRedactionIsCaught is Article 18 itself: covering the
// text without removing the bytes.
func TestVisualOnlyRedactionIsCaught(t *testing.T) {
	orig := original()
	// The "redaction" draws a black box and leaves the text underneath.
	visualOnly := append([]byte("%% draw black rect over inspector name\n"), orig...)

	c, err := Verify(orig, visualOnly, "EV-1-v1", "EV-1-v2", Hash(orig), []string{secret})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if c.Verified || c.Absent() {
		t.Fatal("a visual-only redaction must not verify")
	}
	if c.Leaks[0].Encoding != EncPlain {
		t.Fatalf("the plain occurrence must be reported, got %s", c.Leaks[0].Encoding)
	}
	if !strings.Contains(c.Explain(), "FAILED") {
		t.Fatalf("the explanation must say it failed, got %q", c.Explain())
	}
}

// TestEveryEncodingIsCaught is the substance of the check. Each of these
// has been a real leak in a real document format: strip the UTF-8 copy,
// leave the UTF-16 one in the string table, and the document still
// carries the name.
func TestEveryEncodingIsCaught(t *testing.T) {
	orig := original()
	base := clean()

	units := utf16.Encode([]rune(secret))
	le := make([]byte, 0, len(units)*2)
	be := make([]byte, 0, len(units)*2)
	for _, u := range units {
		le = append(le, byte(u), byte(u>>8))
		be = append(be, byte(u>>8), byte(u))
	}
	nul := make([]byte, 0, len(secret)*2)
	for _, b := range []byte(secret) {
		nul = append(nul, b, 0x00)
	}

	cases := []struct {
		name    string
		residue []byte
		want    Encoding
	}{
		{"utf16 little endian", le, EncUTF16LE},
		{"utf16 big endian", be, EncUTF16BE},
		{"hex", []byte(hex.EncodeToString([]byte(secret))), EncHex},
		{"hex upper", []byte(strings.ToUpper(hex.EncodeToString([]byte(secret)))), EncHexUpper},
		{"base64", []byte(base64.StdEncoding.EncodeToString([]byte(secret))), EncBase64},
		{"pdf hex string", []byte("<" + hex.EncodeToString([]byte(secret)) + ">"), EncPDFHexStr},
		{"nul interleaved", nul, EncZeroPadded},
		{"case folded", []byte(strings.ToUpper(secret)), EncCaseFolded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leaky := append(append([]byte(nil), base...), tc.residue...)
			c, err := Verify(orig, leaky, "EV-1-v1", "EV-1-v2", Hash(orig), []string{secret})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if c.Verified {
				t.Fatalf("a %s residue must defeat verification", tc.name)
			}
			found := false
			for _, l := range c.Leaks {
				if l.Encoding == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a %s leak, got %v", tc.want, c.Leaks)
			}
		})
	}
}

// TestTheOriginalMustBePreserved is Article 17: a redaction that
// modifies its original is not a redaction.
func TestTheOriginalMustBePreserved(t *testing.T) {
	orig := original()
	tampered := bytes.ReplaceAll(orig, []byte("cargo sound"), []byte("cargo damaged"))

	_, err := Verify(tampered, clean(), "EV-1-v1", "EV-1-v2", Hash(orig), []string{secret})
	if !errors.Is(err, ErrOriginalMutated) {
		t.Fatalf("expected ErrOriginalMutated, got %v", err)
	}
}

// TestTheDerivativeMustBeANewVersion is Article 22.
func TestTheDerivativeMustBeANewVersion(t *testing.T) {
	orig := original()
	if _, err := Verify(orig, clean(), "EV-1-v1", "EV-1-v1", Hash(orig), []string{secret}); !errors.Is(err, ErrSameVersion) {
		t.Fatalf("the same version id must be refused, got %v", err)
	}
	// A derivative identical to the original is not a derivative.
	if _, err := Verify(orig, orig, "EV-1-v1", "EV-1-v2", Hash(orig), []string{secret}); !errors.Is(err, ErrSameVersion) {
		t.Fatalf("a byte-identical derivative must be refused, got %v", err)
	}
}

// TestATermThatWasNeverThereProvesNothing closes the route that would
// otherwise make any verification inflatable: list terms the document
// never contained, and watch the "absent" count rise.
func TestATermThatWasNeverThereProvesNothing(t *testing.T) {
	orig := original()
	_, err := Verify(orig, clean(), "EV-1-v1", "EV-1-v2", Hash(orig),
		[]string{secret, "a name that was never in this document"})
	if !errors.Is(err, ErrTermNotInOriginal) {
		t.Fatalf("expected ErrTermNotInOriginal, got %v", err)
	}
}

// TestProvenanceIsRequired is Article 23: a derivative that names no
// original cannot be checked against one.
func TestProvenanceIsRequired(t *testing.T) {
	orig := original()
	for _, tc := range []struct{ origID, derivID string }{
		{"", "EV-1-v2"}, {"EV-1-v1", ""}, {"  ", "EV-1-v2"},
	} {
		if _, err := Verify(orig, clean(), tc.origID, tc.derivID, "", []string{secret}); !errors.Is(err, ErrNoProvenance) {
			t.Fatalf("expected ErrNoProvenance for %q/%q, got %v", tc.origID, tc.derivID, err)
		}
	}
}

func TestEmptyInputsAreRefused(t *testing.T) {
	orig := original()
	if _, err := Verify(nil, clean(), "a", "b", "", []string{secret}); !errors.Is(err, ErrNoOriginal) {
		t.Fatalf("expected ErrNoOriginal, got %v", err)
	}
	if _, err := Verify(orig, nil, "a", "b", "", []string{secret}); !errors.Is(err, ErrNoDerivative) {
		t.Fatalf("expected ErrNoDerivative, got %v", err)
	}
	if _, err := Verify(orig, clean(), "a", "b", "", nil); !errors.Is(err, ErrNoTerms) {
		t.Fatalf("expected ErrNoTerms, got %v", err)
	}
}

// TestMultipleTermsAndMultipleLeaksAreAllReported: a verifier that
// stops at the first leak understates the damage.
func TestMultipleLeaksAreAllReported(t *testing.T) {
	orig := []byte("Inspector: " + secret + "\nMaster: Jan Kowalski\n")
	leaky := []byte("Inspector: [REDACTED]\nMaster: [REDACTED]\n" +
		hex.EncodeToString([]byte(secret)) + "\nJan Kowalski\n")

	c, err := Verify(orig, leaky, "EV-1-v1", "EV-1-v2", Hash(orig), []string{secret, "Jan Kowalski"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(c.Leaks) < 2 {
		t.Fatalf("both leaks must be reported, got %v", c.Leaks)
	}
	terms := map[string]bool{}
	for _, l := range c.Leaks {
		terms[l.Term] = true
	}
	if !terms[secret] || !terms["Jan Kowalski"] {
		t.Fatalf("every leaking term must be named, got %v", terms)
	}
}

// TestTheChainStatesItsOwnLimitations is the honesty requirement: this
// verifies derivatives and produces none, and Article 18 stays OPEN.
func TestTheChainStatesItsOwnLimitations(t *testing.T) {
	orig := original()
	c, err := Verify(orig, clean(), "EV-1-v1", "EV-1-v2", Hash(orig), []string{secret})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	joined := strings.ToLower(strings.Join(c.Limitations, " | "))
	for _, required := range []string{"does not produce", "no adversarial recovery", "listed encodings only"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("the chain must disclose %q, got %v", required, c.Limitations)
		}
	}
}

// TestEveryEncodingIsSearched keeps the scope claim honest: the chain
// records what was checked, and it must be the full list.
func TestEveryEncodingIsSearched(t *testing.T) {
	orig := original()
	c, _ := Verify(orig, clean(), "EV-1-v1", "EV-1-v2", Hash(orig), []string{secret})
	if len(c.EncodingsChecked) != len(Encodings()) {
		t.Fatalf("expected %d encodings recorded, got %d", len(Encodings()), len(c.EncodingsChecked))
	}
	if len(Encodings()) < 10 {
		t.Fatalf("the encoding list looks thin: %d", len(Encodings()))
	}
}
