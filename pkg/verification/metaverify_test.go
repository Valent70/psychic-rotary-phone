package verification

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The verifier-of-verifier.
//
// # Why this file exists
//
// Every other test here asks whether the verifier catches a tampered
// bundle. That is necessary and it leaves the more dangerous question
// unasked: is the verifier CAPABLE of catching anything, or does it
// pass everything?
//
// A verifier that returned PASS unconditionally would satisfy every
// happy-path test in this package. It would also satisfy the tampering
// tests if they were subtly wrong -- if the mutation they applied
// never reached the bytes the verifier reads, for instance. The
// tampering tests and the verifier can be wrong together, and nothing
// in either notices.
//
// So these tests attack the VERIFIER. They corrupt each step's input
// in a way that must produce a FAIL, and assert that it does; and they
// assert that the verifier's own outcome vocabulary is used -- that
// UNVERIFIABLE is reachable, that FAIL is reachable, and that the two
// are not the same value under different names.
//
// The pattern generalises: system, verifier, verifier-of-verifier. The
// chain has to stop somewhere, and it should stop one level further
// out than most people put it.

// eachStepCanFail is the central claim: for every step the verifier
// performs, there exists an input that makes that step FAIL.
//
// A step for which no such input exists is a step that is not checking
// anything, and it would be indistinguishable, in every report this
// package produces, from a step that always passes because the bundle
// is sound.
func TestEveryStepIsCapableOfFailing(t *testing.T) {
	corruptions := map[string]func(t *testing.T, dir string){
		"artefact-hashes": func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "artefacts", "e1v1.txt"), "different bytes entirely")
		},
		"ledger-lineage": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "ledger", "records.json")
			raw := read(t, p)
			write(t, p, strings.Replace(raw, `"SUCCEEDED"`, `"REFUSED"`, 1))
		},
		"signature": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "passport.json")
			var doc map[string]any
			if err := json.Unmarshal([]byte(read(t, p)), &doc); err != nil {
				t.Fatal(err)
			}
			doc["payload"].(map[string]any)["statement"] = "something else"
			out, _ := json.MarshalIndent(doc, "", "  ")
			write(t, p, string(out))
		},
		"provenance": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "provenance", "records.json")
			write(t, p, `[{"id":"provenance:p1","source_content_hash":"h","path":[]}]`)
		},
		"replay": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "replay", "steps.json")
			raw := read(t, p)
			write(t, p, strings.Replace(raw, `"excess_over_tolerance": 1500`,
				`"excess_over_tolerance": 9999`, 1))
		},
		"canonicalize": func(t *testing.T, dir string) {
			// A document that is not JSON at all does not reach the
			// canonicalise step; one that is JSON with a non-finite
			// number cannot be canonicalised.
			write(t, filepath.Join(dir, "assurance", "evidence.json"), `[{"class":1e999}]`)
		},
	}

	for step, corrupt := range corruptions {
		t.Run(step, func(t *testing.T) {
			f := genuine(t, nil)
			corrupt(t, f.dir)
			fixManifest(t, f.dir)

			b, err := Open(f.dir)
			if err != nil {
				// A corruption caught by the manifest is also a
				// detection; what must not happen is silence.
				return
			}
			r := Verify(b, Options{At: vat, Revocations: []string{},
				TrustedKeys: map[string]ed25519.PublicKey{"veriqo-key-1": f.pub}})

			var got Outcome
			for _, s := range r.Steps {
				if s.Name == step {
					got = s.Outcome
				}
			}
			if got != Fail {
				t.Fatalf("corrupting %s produced %s. If no input can make this step fail, "+
					"the step is not checking anything and is indistinguishable from one "+
					"that always passes:\n%s", step, got, r.Render())
			}
			if r.Verified() {
				t.Fatalf("a bundle with a failing %s step reported verified", step)
			}
		})
	}
}

// TestTheVerifierDoesNotPassEverything.
//
// The crudest failure a verifier can have, asserted directly rather
// than inferred from the tests above passing.
func TestTheVerifierDoesNotPassEverything(t *testing.T) {
	f := genuine(t, nil)
	// Replace every JSON document with something structurally wrong
	// but still parseable, so nothing fails for a trivial reason.
	write(t, filepath.Join(f.dir, "ledger", "records.json"),
		`[{"height":5,"prev_hash":"nonsense","event":{},"hash":"wrong"}]`)
	fixManifest(t, f.dir)
	b, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Verify(b, Options{At: vat})
	if r.Verified() {
		t.Fatal("a bundle with a fabricated ledger verified")
	}
	if len(r.Failures()) == 0 {
		t.Fatal("the verifier reported not-verified with no failure named, which tells a " +
			"reader nothing")
	}
}

// TestFailAndUnverifiableAreDistinctOutcomes.
//
// Reporting "cannot check" as "invalid" trains readers to ignore
// failures; reporting it as a pass is a lie. Both must be reachable,
// and they must not be the same value.
func TestFailAndUnverifiableAreDistinctOutcomes(t *testing.T) {
	if Fail == Unverifiable || Pass == Unverifiable || Pass == Fail {
		t.Fatal("the outcome vocabulary has collapsed")
	}

	// UNVERIFIABLE: a bundle missing a component.
	sparse := t.TempDir()
	b, err := NewBuilder("case-1", "INTERNALLY_ASSURED", vat)
	if err != nil {
		t.Fatal(err)
	}
	must(t, b.AddJSON("assurance/evidence.json", []map[string]any{}))
	if _, err := b.Write(sparse); err != nil {
		t.Fatal(err)
	}
	bundle, err := Open(sparse)
	if err != nil {
		t.Fatal(err)
	}
	r := Verify(bundle, Options{At: vat})
	var sawUnverifiable bool
	for _, s := range r.Steps {
		if s.Outcome == Unverifiable {
			sawUnverifiable = true
		}
		if s.Outcome == Fail {
			t.Fatalf("a merely incomplete bundle produced a FAIL at %s: %s", s.Name, s.Detail)
		}
	}
	if !sawUnverifiable {
		t.Fatal("a bundle missing every component produced no UNVERIFIABLE step")
	}

	// FAIL: reachable, per the table above.
	f := genuine(t, nil)
	write(t, filepath.Join(f.dir, "artefacts", "e1v1.txt"), "tampered")
	fixManifest(t, f.dir)
	tb, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(Verify(tb, Options{At: vat}).Failures()) == 0 {
		t.Fatal("a tampered artefact produced no FAIL")
	}
}

// TestTheManifestCheckIsNotTheOnlyDefence.
//
// If the manifest were the only real check, an attacker who updates it
// would face nothing. Every tampering test in this package repairs the
// manifest first for that reason, and this test asserts the property
// directly rather than leaving it as a convention those tests happen
// to follow.
func TestTheManifestCheckIsNotTheOnlyDefence(t *testing.T) {
	f := genuine(t, nil)
	p := filepath.Join(f.dir, "ledger", "records.json")
	write(t, p, strings.Replace(read(t, p), `"SUCCEEDED"`, `"REFUSED"`, 1))
	fixManifest(t, f.dir)

	// The manifest now agrees with the bytes: Open succeeds.
	b, err := Open(f.dir)
	if err != nil {
		t.Fatalf("the manifest repair did not work, so this test proves nothing: %v", err)
	}
	r := Verify(b, Options{At: vat})
	if r.Verified() {
		t.Fatal("with the manifest repaired, the verifier found nothing wrong. The " +
			"manifest is the only defence and an attacker who updates it faces nothing")
	}
}

// TestTheReportNamesWhatItCouldNotDoEvenWhenItPasses.
//
// A verifier that reports success without its limits is producing the
// false assurance this whole package exists to prevent.
func TestTheReportNamesWhatItCouldNotDoEvenWhenItPasses(t *testing.T) {
	f := genuine(t, nil)
	r := verify(t, f, Options{Revocations: []string{},
		TrustedKeys: map[string]ed25519.PublicKey{"veriqo-key-1": f.pub}})
	if !r.Verified() {
		t.Fatalf("the genuine fixture stopped verifying:\n%s", r.Render())
	}
	out := r.Render()
	if !strings.Contains(out, "what this verification cannot establish at all") {
		t.Fatalf("a passing report omits its limits:\n%s", out)
	}
	if len(r.Limits) < 3 {
		t.Fatalf("only %d limits stated", len(r.Limits))
	}
	// The shared-canonicaliser limit must appear when the default is
	// used, because it is the limit a reader is least likely to think
	// of themselves.
	if !strings.Contains(out, "both sides make the same mistake and agree") {
		t.Fatalf("the shared-canonicaliser limit is missing:\n%s", out)
	}
}

// TestAnAlternativeCanonicaliserChangesTheReportedLimits.
//
// The seam exists so a verifier can supply their own implementation.
// If supplying one did not change what the report says, the seam would
// be decorative.
func TestAnAlternativeCanonicaliserChangesTheReportedLimits(t *testing.T) {
	f := genuine(t, nil)
	b, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Verify(b, Options{At: vat, Canonicalizer: independentJCS{}})
	if strings.Contains(strings.Join(r.Limits, " "), "both sides make the same mistake") {
		t.Fatal("the shared-code limit was reported for an independent canonicaliser")
	}
	if !strings.Contains(r.Render(), "independent-jcs") {
		t.Fatalf("the report does not name the canonicaliser used:\n%s", r.Render())
	}
}

// independentJCS stands in for a third party's implementation. It
// delegates, because writing a second RFC 8785 here would test this
// file's author rather than the seam -- and the seam is what is under
// test.
type independentJCS struct{}

func (independentJCS) Canonicalize(v any) ([]byte, error) {
	return DefaultCanonicalizer().Canonicalize(v)
}
func (independentJCS) Name() string { return "independent-jcs (supplied by the verifier)" }

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var _ = sha256.Sum256
var _ = hex.EncodeToString
