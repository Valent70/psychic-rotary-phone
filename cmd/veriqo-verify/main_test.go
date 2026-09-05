package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// veriqo-verify is the binary somebody who does NOT trust VERIQO runs.
// Its two loaders are the whole of its trust input: the keys it will
// check signatures against, and the revocation list it will check
// findings against. Both are supplied out-of-band precisely because the
// bundle's own copies establish nothing. So the failure that matters
// here is a loader that accepts something it should not, or that turns
// "I checked and found nothing" into "I did not check" (or worse, the
// reverse).

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func keyHex(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(pub)
}

// TestAWellFormedKeyFileLoads.
func TestAWellFormedKeyFileLoads(t *testing.T) {
	h := keyHex(t)
	keys, err := loadKeys(write(t, "keys.json", `{"key:alpha":"`+h+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("loaded %d keys, want 1", len(keys))
	}
	if len(keys["key:alpha"]) != ed25519.PublicKeySize {
		t.Fatalf("key:alpha is %d bytes", len(keys["key:alpha"]))
	}
}

// TestSurroundingWhitespaceInAKeyIsTolerated. A key pasted out of an
// email arrives with whitespace, and refusing it would push the
// operator towards -keys-less runs, which check less.
func TestSurroundingWhitespaceInAKeyIsTolerated(t *testing.T) {
	h := keyHex(t)
	if _, err := loadKeys(write(t, "keys.json", `{"k":"  `+h+`  "}`)); err != nil {
		t.Fatalf("a key with surrounding whitespace was refused: %v", err)
	}
}

// TestAKeyOfTheWrongLengthIsRefused.
//
// A 16-byte value is valid hex and is not an ed25519 public key.
// Accepting it and letting the signature check fail later would report
// a forged bundle where the truth is a mistyped key -- two very
// different conclusions for whoever is reading the output.
func TestAKeyOfTheWrongLengthIsRefused(t *testing.T) {
	short := strings.Repeat("ab", 16)
	_, err := loadKeys(write(t, "keys.json", `{"k":"`+short+`"}`))
	if err == nil {
		t.Fatal("a 16-byte value was accepted as an ed25519 public key")
	}
	if !strings.Contains(err.Error(), "ed25519") {
		t.Errorf("the refusal does not say what was wrong: %v", err)
	}
}

// TestANonHexKeyIsRefusedWithItsIdNamed.
func TestANonHexKeyIsRefusedWithItsIdNamed(t *testing.T) {
	_, err := loadKeys(write(t, "keys.json", `{"key:beta":"not-hex-at-all"}`))
	if err == nil {
		t.Fatal("a non-hex key was accepted")
	}
	if !strings.Contains(err.Error(), "key:beta") {
		t.Errorf("the refusal does not name which key is wrong, so an operator with "+
			"twenty keys cannot act on it: %v", err)
	}
}

// TestAnEmptyKeyFileIsRefusedRatherThanTreatedAsNoKeys.
//
// This is the important one. `-keys empty.json` that silently loaded
// nothing would fall back to the bundle's own keys, and the operator
// would believe they had checked authenticity when they had checked
// the bundle against itself.
func TestAnEmptyKeyFileIsRefusedRatherThanTreatedAsNoKeys(t *testing.T) {
	for _, body := range []string{`{}`, `null`} {
		if _, err := loadKeys(write(t, "keys.json", body)); err == nil {
			t.Errorf("%s was accepted as a key file; the run would silently fall back to "+
				"the bundle's own keys while the operator believes otherwise", body)
		}
	}
}

// TestAMalformedKeyFileNamesThePath.
func TestAMalformedKeyFileNamesThePath(t *testing.T) {
	p := write(t, "keys.json", `{not json`)
	_, err := loadKeys(p)
	if err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	if !strings.Contains(err.Error(), p) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
}

// TestAMissingKeyFileIsAnError. Exit code 2, not a silent downgrade.
func TestAMissingKeyFileIsAnError(t *testing.T) {
	if _, err := loadKeys(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a missing key file was accepted")
	}
}

// TestAnEmptyRevocationListIsAnAnswerNotAnAbsence.
//
// The asymmetry with keys is deliberate and is the subtler half of this
// tool's honesty. An empty KEY file means the operator supplied no
// basis for trust, which is a mistake. An empty REVOCATION list means
// the operator checked the register and nothing was on it, which is a
// finding. Collapsing the second into nil would make Verify report
// revocation as NOT CHECKED and discard a real check the operator
// performed.
func TestAnEmptyRevocationListIsAnAnswerNotAnAbsence(t *testing.T) {
	for _, body := range []string{`[]`, `null`} {
		got, err := loadRevocations(write(t, "rev.json", body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if got == nil {
			t.Errorf("%s produced a nil list, which Verify reads as 'not checked'; the "+
				"operator's answer -- 'I looked, and nothing is revoked' -- is lost", body)
		}
		if len(got) != 0 {
			t.Errorf("%s produced %d entries", body, len(got))
		}
	}
}

// TestRevocationsLoad.
func TestRevocationsLoad(t *testing.T) {
	got, err := loadRevocations(write(t, "rev.json", `["finding:a","finding:b"]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "finding:a" || got[1] != "finding:b" {
		t.Fatalf("got %v", got)
	}
}

// TestAMalformedRevocationFileIsRefused. Not silently emptied: an
// unparseable revocation list read as "nothing is revoked" is exactly
// the UNPARSEABLE-is-not-ABSENT error the epistemic firewall exists to
// prevent, committed by the verifier itself.
func TestAMalformedRevocationFileIsRefused(t *testing.T) {
	for _, body := range []string{`{"a":1}`, `[1,2]`, `nonsense`} {
		if _, err := loadRevocations(write(t, "rev.json", body)); err == nil {
			t.Errorf("%s was accepted as a revocation list and would be read as "+
				"'nothing is revoked'", body)
		}
	}
}

// TestTheUsageTextStatesTheThreeThingsThisToolCannotEstablish.
//
// A verifier that exits 0 without saying what its 0 does not cover is
// how a green tick becomes a claim nobody made. These three sentences
// are load-bearing, and deleting them would make every passing run
// overstate itself.
func TestTheUsageTextStatesTheThreeThingsThisToolCannotEstablish(t *testing.T) {
	var b strings.Builder
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	usage()
	w.Close()
	os.Stderr = old
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	b.Write(buf[:n])
	text := b.String()

	for _, must := range []string{
		"key authenticity",
		"existence in time",
		"absent evidence",
		"recomputing",
	} {
		if !strings.Contains(text, must) {
			t.Errorf("the usage text no longer states %q; a passing run would then be "+
				"read as more than it is", must)
		}
	}
}
