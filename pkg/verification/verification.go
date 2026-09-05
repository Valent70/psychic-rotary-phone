// Package verification is the Independent Verification Kit.
//
// # The principle
//
//	The verifier must not trust the system being verified.
//
// That sentence rules out the obvious design. A verifier that asks
// VERIQO for a qualification status and reports what it is told has
// verified nothing: it has relayed. A verifier that fetches an
// expected digest from VERIQO and compares it to a digest VERIQO also
// computed has confirmed only that VERIQO is self-consistent, which a
// compromised or simply mistaken system also is.
//
// So this package takes a BUNDLE -- a sealed set of files a third
// party is handed -- and RECOMPUTES everything from it:
//
//	Artefact -> Canonicalize -> Hash -> Verify Signature
//	         -> Verify Provenance -> Verify Ledger Lineage
//	         -> Replay -> Compare Output -> Verify Qualification State
//
// Nothing in here reads a status. The qualification state is DERIVED
// from the evidence in the bundle and then compared against what the
// bundle claims, and a disagreement is a finding rather than a
// correction.
//
// # What this kit cannot establish, stated plainly
//
// Three things, and a verifier that does not say so is overclaiming:
//
//  1. KEY AUTHENTICITY. The bundle carries public keys. Nothing in the
//     bundle can establish that those keys belong to whom they claim
//     -- a bundle produced entirely by an impostor is internally
//     perfect. Key trust is an out-of-band problem and the report says
//     so on every run.
//
//  2. EXISTENCE IN TIME. Without an external anchor, a ledger proves
//     its own internal consistency. It does not prove it existed
//     before the moment it was handed over, so a wholesale rewrite
//     between two observations is invisible from the artefact alone.
//
//  3. SHARED-CODE BLINDNESS. By default this kit canonicalises with
//     the same implementation the system used. A defect in that
//     implementation is therefore invisible: both sides make the same
//     mistake and agree. A verifier that wants real independence
//     supplies its own Canonicalizer, and the report states which was
//     used.
package verification

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/canonical/jcs"
)

// Outcome is one step's result.
//
// UNVERIFIABLE is not a failure. It is the answer when the bundle does
// not contain what a step needs -- and reporting it as a failure would
// train readers to ignore failures, while reporting it as a pass would
// be a lie.
type Outcome string

const (
	Pass         Outcome = "PASS"
	Fail         Outcome = "FAIL"
	Unverifiable Outcome = "UNVERIFIABLE"
)

// Step is one stage of the verification chain.
type Step struct {
	Name    string  `json:"name"`
	Outcome Outcome `json:"outcome"`
	// Detail says what was actually computed and compared, in enough
	// detail that a reader can repeat it by hand.
	Detail string `json:"detail"`
	// Caveats are true regardless of the outcome.
	Caveats []string `json:"caveats,omitempty"`
}

// Report is the verification result.
type Report struct {
	BundleDigest string `json:"bundle_digest"`
	Steps        []Step `json:"steps"`
	// ClaimedQualification is what the bundle says.
	ClaimedQualification string `json:"claimed_qualification"`
	// DerivedQualification is what this verifier computed from the
	// evidence. Where they differ, the derived one is what a reader
	// should believe -- and the difference is itself the finding.
	DerivedQualification string `json:"derived_qualification"`
	// Canonicalizer names which implementation was used, because
	// sharing the system's own is a limitation on the whole report.
	Canonicalizer string `json:"canonicalizer"`
	// Limits are what this verification cannot establish at all.
	Limits []string  `json:"limits"`
	At     time.Time `json:"at"`
}

// Verified reports whether every step that could run, passed.
//
// An UNVERIFIABLE step does not make the report false; it makes it
// narrower, which is why Verified is paired with Unverifiable() in
// every rendering.
func (r Report) Verified() bool {
	for _, s := range r.Steps {
		if s.Outcome == Fail {
			return false
		}
	}
	return r.ClaimedQualification == r.DerivedQualification
}

// Unverifiable returns the steps the bundle could not support.
func (r Report) Unverifiable() []string {
	var out []string
	for _, s := range r.Steps {
		if s.Outcome == Unverifiable {
			out = append(out, s.Name+": "+s.Detail)
		}
	}
	return out
}

// Failures returns the steps that failed.
func (r Report) Failures() []string {
	var out []string
	for _, s := range r.Steps {
		if s.Outcome == Fail {
			out = append(out, s.Name+": "+s.Detail)
		}
	}
	return out
}

// Canonicalizer is the pluggable seam that makes real independence
// possible.
//
// Supplying an implementation written by somebody else is the whole
// point: with the default, a canonicalisation defect is invisible
// because both sides make the same mistake.
type Canonicalizer interface {
	// Canonicalize returns the canonical byte form of a decoded JSON
	// value.
	Canonicalize(v any) ([]byte, error)
	// Name identifies the implementation in the report.
	Name() string
}

type veriqoJCS struct{}

func (veriqoJCS) Canonicalize(v any) ([]byte, error) { return jcs.Canonicalize(v) }
func (veriqoJCS) Name() string {
	return "veriqo/pkg/canonical/jcs (SHARED WITH THE SYSTEM UNDER VERIFICATION)"
}

// DefaultCanonicalizer is VERIQO's own. It is correct as far as anyone
// here knows, and using it means a defect in it cannot be detected by
// this kit.
func DefaultCanonicalizer() Canonicalizer { return veriqoJCS{} }

// Options configure a verification.
type Options struct {
	// Canonicalizer, if nil, is VERIQO's own -- with the limitation
	// that implies, stated in the report.
	Canonicalizer Canonicalizer
	// TrustedKeys maps key id to public key, supplied OUT OF BAND by
	// the verifier. Keys taken from the bundle itself are used only
	// when this map is empty, and the report says which happened.
	TrustedKeys map[string]ed25519.PublicKey
	// At is the instant validity is checked against.
	At time.Time
	// Revocations the verifier obtained independently. Nil means the
	// verifier did not check, which is reported rather than assumed.
	Revocations []string
}

// Bundle is the sealed set of files handed to a third party.
//
// It is a plain directory so that a verifier can read every byte with
// tools they already trust, rather than through a format only VERIQO
// can parse.
type Bundle struct {
	Dir string
	// files maps the bundle-relative path to its content.
	files map[string][]byte
	// manifest is the bundle's own claim about its contents.
	manifest Manifest
}

// Manifest is the bundle's index.
type Manifest struct {
	Schema  string `json:"schema"`
	Subject string `json:"subject"`
	// Files maps a bundle-relative path to its SHA-256, so a verifier
	// can detect that the bundle they hold is not the one described.
	Files map[string]string `json:"files"`
	// ClaimedQualification is what VERIQO says the subject reached.
	// It is recorded so that it can be CONTRADICTED, not adopted.
	ClaimedQualification string `json:"claimed_qualification"`
	// PublicKeys maps key id to hex-encoded ed25519 public key. See
	// the package doc: these establish nothing on their own.
	PublicKeys map[string]string `json:"public_keys,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

const ManifestSchema = "veriqo.verification.bundle/v1"

var (
	ErrNoManifest    = errors.New("verification: the bundle has no manifest")
	ErrManifestMatch = errors.New("verification: a file does not match the manifest")
	ErrBadBundle     = errors.New("verification: the bundle is malformed")
)

// Open reads a bundle from disk and checks it against its own
// manifest.
//
// This first check is deliberately narrow: it establishes that the
// bytes on disk are the bytes the manifest describes. It says nothing
// about whether the manifest is honest -- that is what every later
// step is for.
func Open(dir string) (*Bundle, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoManifest, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: manifest: %v", ErrBadBundle, err)
	}
	if m.Schema != ManifestSchema {
		return nil, fmt.Errorf("%w: manifest schema %q, expected %q",
			ErrBadBundle, m.Schema, ManifestSchema)
	}
	b := &Bundle{Dir: dir, files: map[string][]byte{}, manifest: m}
	for path, want := range m.Files {
		if strings.Contains(path, "..") || filepath.IsAbs(path) {
			return nil, fmt.Errorf("%w: manifest names %q, which escapes the bundle",
				ErrBadBundle, path)
		}
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrBadBundle, path, err)
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != want {
			return nil, fmt.Errorf("%w: %s is %s, the manifest says %s",
				ErrManifestMatch, path, short(got), short(want))
		}
		b.files[path] = content
	}
	return b, nil
}

// Manifest returns the bundle's index.
func (b *Bundle) Manifest() Manifest { return b.manifest }

// File returns a bundle file's bytes.
func (b *Bundle) File(path string) ([]byte, bool) {
	c, ok := b.files[path]
	return c, ok
}

// Digest is the digest over the whole bundle: every path and every
// content hash, in sorted order. Two bundles with the same digest hold
// the same bytes under the same names.
func (b *Bundle) Digest() string {
	paths := make([]string, 0, len(b.files))
	for p := range b.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%d:%s", len(p), p)
		sum := sha256.Sum256(b.files[p])
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
