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
)

// Builder assembles a bundle.
//
// It is the producing side of the kit, and it is deliberately dumb: it
// writes what it is given and records the digests. It does not decide
// what the bundle should claim, because a builder that computed the
// claim would be the system marking its own work -- which is the
// arrangement this whole package exists to replace.
type Builder struct {
	subject string
	claimed string
	at      time.Time
	files   map[string][]byte
	pubKeys map[string]string
}

// NewBuilder starts a bundle.
//
// claimedQualification is what VERIQO asserts the subject reached. It
// is recorded so that a verifier can CONTRADICT it.
func NewBuilder(subject, claimedQualification string, at time.Time) (*Builder, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, errors.New("verification: a bundle must name its subject")
	}
	if strings.TrimSpace(claimedQualification) == "" {
		return nil, errors.New("verification: a bundle must state what it claims, so that a " +
			"verifier has something to disagree with")
	}
	if at.IsZero() {
		return nil, errors.New("verification: a bundle carries no instant")
	}
	return &Builder{subject: subject, claimed: claimedQualification, at: at,
		files: map[string][]byte{}, pubKeys: map[string]string{}}, nil
}

// Add puts a file in the bundle at a slash-separated path.
func (b *Builder) Add(path string, content []byte) error {
	p := strings.TrimSpace(path)
	if p == "" || strings.Contains(p, "..") || strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: %q is not a bundle-relative path", ErrBadBundle, path)
	}
	if p == "manifest.json" {
		return fmt.Errorf("%w: the manifest is written by the builder", ErrBadBundle)
	}
	if _, dup := b.files[p]; dup {
		return fmt.Errorf("%w: %s is already in the bundle", ErrBadBundle, p)
	}
	b.files[p] = append([]byte(nil), content...)
	return nil
}

// Files returns a copy of what the builder holds, so a caller can
// compute digests over the bundle's contents before it is written.
func (b *Builder) Files() map[string][]byte {
	out := make(map[string][]byte, len(b.files))
	for k, v := range b.files {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

// AddJSON marshals a value into the bundle.
func (b *Builder) AddJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return b.Add(path, raw)
}

// AddPublicKey records a verification key.
//
// The doc comment on Manifest says what this is worth on its own:
// nothing. It is included so that a verifier who has obtained the key
// out of band can confirm the bundle names the same one, and so that a
// verifier who has not can see exactly what they are missing.
func (b *Builder) AddPublicKey(keyID string, pub ed25519.PublicKey) error {
	if strings.TrimSpace(keyID) == "" {
		return errors.New("verification: a public key has no id")
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("verification: %s is %d bytes, not an ed25519 public key",
			keyID, len(pub))
	}
	b.pubKeys[keyID] = hex.EncodeToString(pub)
	return nil
}

// Write materialises the bundle into dir.
//
// The output is deterministic: the same inputs produce byte-identical
// files, so two builds of the same bundle have the same digest and a
// difference is a real difference.
func (b *Builder) Write(dir string) (Manifest, error) {
	if len(b.files) == 0 {
		return Manifest{}, fmt.Errorf("%w: a bundle with no files verifies nothing", ErrBadBundle)
	}
	m := Manifest{
		Schema: ManifestSchema, Subject: b.subject, Files: map[string]string{},
		ClaimedQualification: b.claimed, CreatedAt: b.at.UTC(),
	}
	if len(b.pubKeys) > 0 {
		m.PublicKeys = map[string]string{}
		for k, v := range b.pubKeys {
			m.PublicKeys[k] = v
		}
	}

	paths := make([]string, 0, len(b.files))
	for p := range b.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return Manifest{}, err
		}
		if err := os.WriteFile(full, b.files[p], 0o644); err != nil {
			return Manifest{}, err
		}
		sum := sha256.Sum256(b.files[p])
		m.Files[p] = hex.EncodeToString(sum[:])
	}

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Manifest{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// README is the text a bundle carries for whoever receives it. It is
// written into the bundle so the instructions cannot be separated from
// the thing they describe.
const README = `VERIQO EVIDENCE BUNDLE

You have been handed this bundle so that you can check VERIQO's claims
without asking VERIQO anything.

  veriqo-verify <this directory>

Everything the verifier prints was recomputed from the files here. It
does not read a status from any of them; where the bundle states a
qualification, the verifier derives its own and reports the difference.

Three things this bundle cannot establish, whatever the verifier says:

  1. That the public keys in the manifest belong to whom they claim.
     A bundle produced entirely by an impostor is internally perfect.
     Obtain the keys from a channel that is not this bundle and pass
     them to the verifier.

  2. That the ledger existed before you received it. Without an
     external anchor, a hash chain proves only its own consistency.

  3. Anything about evidence that was never put in the bundle.

If the verifier's default canonicaliser is used, it is the same code
the system used to produce these digests, so a defect in it would be
invisible. Supplying your own is the point of the seam.
`
