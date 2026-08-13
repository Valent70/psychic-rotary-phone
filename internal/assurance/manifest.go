package assurance

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// --- PHASE 34: acceptance suite governance ---------------------------

// AcceptanceCategory is one required category of permanent acceptance
// tests.
type AcceptanceCategory struct {
	Name    string `json:"name"`
	Minimum int    `json:"minimum"`
	Actual  int    `json:"actual"`
}

// AcceptanceManifest encodes the audit's rule that the acceptance suite
// may only ever GROW. CI fails when a count decreases, a category
// disappears, or the central lifecycle test goes missing.
type AcceptanceManifest struct {
	Categories []AcceptanceCategory `json:"categories"`
	// MandatoryTests must all be present by name. Deleting one is a
	// release-blocking event, not a refactor.
	MandatoryTests []string `json:"mandatory_tests"`
	PresentTests   []string `json:"present_tests"`
	TotalMinimum   int      `json:"total_minimum"`
	Hash           string   `json:"hash"`
}

// Errors for manifest governance.
var (
	ErrCategoryBelowMinimum = errors.New("assurance: acceptance category below its permanent minimum")
	ErrCategoryMissing      = errors.New("assurance: acceptance category disappeared from the suite")
	ErrMandatoryTestMissing = errors.New("assurance: mandatory acceptance test is missing")
	ErrSuiteShrank          = errors.New("assurance: acceptance suite total decreased")
)

// Validate enforces every governance rule at once and reports ALL
// violations, not just the first — a CI run should tell you everything
// that is wrong in one pass.
func (m AcceptanceManifest) Validate() error {
	var problems []string
	total := 0
	for _, c := range m.Categories {
		total += c.Actual
		if c.Actual == 0 && c.Minimum > 0 {
			problems = append(problems, fmt.Sprintf("%v: category %q", ErrCategoryMissing, c.Name))
			continue
		}
		if c.Actual < c.Minimum {
			problems = append(problems, fmt.Sprintf("%v: %q has %d, minimum %d",
				ErrCategoryBelowMinimum, c.Name, c.Actual, c.Minimum))
		}
	}
	present := map[string]bool{}
	for _, t := range m.PresentTests {
		present[t] = true
	}
	for _, t := range m.MandatoryTests {
		if !present[t] {
			problems = append(problems, fmt.Sprintf("%v: %s", ErrMandatoryTestMissing, t))
		}
	}
	if total < m.TotalMinimum {
		problems = append(problems, fmt.Sprintf("%v: %d < %d", ErrSuiteShrank, total, m.TotalMinimum))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("acceptance manifest violations:\n  - " + joinLines(problems))
	}
	return nil
}

func joinLines(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += "\n  - "
		}
		out += v
	}
	return out
}

// ComputeHash content-addresses the manifest.
func (m AcceptanceManifest) ComputeHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.acceptance_manifest/v1|min=%d|", m.TotalMinimum)
	cats := append([]AcceptanceCategory(nil), m.Categories...)
	sort.Slice(cats, func(i, j int) bool { return cats[i].Name < cats[j].Name })
	for _, c := range cats {
		fmt.Fprintf(h, "%s=%d/%d|", c.Name, c.Actual, c.Minimum)
	}
	mand := append([]string(nil), m.MandatoryTests...)
	sort.Strings(mand)
	for _, t := range mand {
		fmt.Fprintf(h, "must=%s|", t)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- PHASE 55: release certificate -----------------------------------

// ReleaseCertificate is the signed artifact every VERIQO release
// produces. Its fields are exactly those the audit enumerated.
type ReleaseCertificate struct {
	Version                string     `json:"version"`
	GitCommit              string     `json:"git_commit"`
	BuildHash              string     `json:"build_hash"`
	SourceHash             string     `json:"source_hash"`
	SBOMHash               string     `json:"sbom_hash"`
	TestManifestHash       string     `json:"test_manifest_hash"`
	AcceptanceManifestHash string     `json:"acceptance_manifest_hash"`
	SecurityManifestHash   string     `json:"security_manifest_hash"`
	BenchmarkManifestHash  string     `json:"benchmark_manifest_hash"`
	ChaosManifestHash      string     `json:"chaos_manifest_hash"`
	ReplayManifestHash     string     `json:"replay_manifest_hash"`
	Operator               string     `json:"operator"`
	Timestamp              uint64     `json:"timestamp"`
	SigningKeyID           string     `json:"signing_key_id"`
	GoVersion              string     `json:"go_version"`
	Verdict                Verdict    `json:"verdict"`
	Assessment             Assessment `json:"assessment"`

	CertificateHash string `json:"certificate_hash"`
	Signature       string `json:"signature,omitempty"`
	PublicKey       string `json:"public_key,omitempty"`
}

// canonical is the deterministic byte form used for hash and signature.
func (c ReleaseCertificate) canonical() []byte {
	h := fmt.Sprintf(
		"veriqo.release_certificate/v1\nversion=%s\ncommit=%s\nbuild=%s\nsource=%s\nsbom=%s\n"+
			"test=%s\nacceptance=%s\nsecurity=%s\nbenchmark=%s\nchaos=%s\nreplay=%s\n"+
			"operator=%s\nts=%d\nkey=%s\ngo=%s\nverdict=%s\nmandatory=%d/%d\n",
		c.Version, c.GitCommit, c.BuildHash, c.SourceHash, c.SBOMHash,
		c.TestManifestHash, c.AcceptanceManifestHash, c.SecurityManifestHash,
		c.BenchmarkManifestHash, c.ChaosManifestHash, c.ReplayManifestHash,
		c.Operator, c.Timestamp, c.SigningKeyID, c.GoVersion, c.Verdict,
		c.Assessment.MandatoryPassing, c.Assessment.MandatoryTotal)
	return []byte(h)
}

// Finalize computes the certificate hash. The verdict is taken from
// the assessment, never set by the caller — a release certificate
// cannot claim a verdict the gates did not produce.
func (c ReleaseCertificate) Finalize(a Assessment) ReleaseCertificate {
	c.Assessment = a
	c.Verdict = a.Verdict
	sum := sha256.Sum256(c.canonical())
	c.CertificateHash = hex.EncodeToString(sum[:])
	return c
}

// Sign attaches an Ed25519 signature over the canonical bytes.
func (c ReleaseCertificate) Sign(priv ed25519.PrivateKey, keyID string) ReleaseCertificate {
	c.SigningKeyID = keyID
	sum := sha256.Sum256(c.canonical())
	c.CertificateHash = hex.EncodeToString(sum[:])
	sig := ed25519.Sign(priv, c.canonical())
	c.Signature = hex.EncodeToString(sig)
	c.PublicKey = hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	return c
}

// Verify checks the hash and, when present, that the embedded
// signature is internally self-consistent (it was produced by
// whichever key PublicKey names). This is necessary but not
// sufficient: it catches a certificate whose hash or signature was
// edited after Sign, but it does NOT catch a forger who regenerates a
// brand-new keypair, signs a different certificate with it, and embeds
// their own PublicKey to match — a self-consistent document is not
// automatically an authorized one. Verify exists for that narrower
// tamper-detection use; production and release-gate callers should use
// VerifyTrusted instead.
func (c ReleaseCertificate) Verify() error {
	sum := sha256.Sum256(c.canonical())
	if hex.EncodeToString(sum[:]) != c.CertificateHash {
		return ErrManifestTampered
	}
	if c.Signature == "" {
		return nil
	}
	sig, err := hex.DecodeString(c.Signature)
	if err != nil {
		return ErrManifestTampered
	}
	pub, err := hex.DecodeString(c.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ErrManifestTampered
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), c.canonical(), sig) {
		return ErrManifestTampered
	}
	return nil
}

// ErrUntrustedSigningKey is returned by VerifyTrusted when a
// certificate's embedded key is not — or no longer — in the caller's
// trust anchor set, whether or not the embedded signature is
// internally consistent.
var ErrUntrustedSigningKey = errors.New("assurance: signing key is not a trusted anchor")

// TrustedKeys maps a signing key ID (as named in ReleaseCertificate.
// SigningKeyID) to the public key an operator has independently
// decided to trust for that ID — never read from the certificate being
// verified itself. This is the trust anchor VerifyTrusted checks
// against, closing the audit's "Internal Gap III": a signature is
// verified against a trust anchor, not merely checked for internal
// self-consistency.
type TrustedKeys map[string]ed25519.PublicKey

// VerifyTrusted performs every check Verify does, and additionally
// requires that c.SigningKeyID is present in trusted and that
// trusted[c.SigningKeyID] — not c.PublicKey — is the key whose
// signature validates. A certificate signed by an unlisted or revoked
// key fails here even if its embedded PublicKey and Signature are
// perfectly self-consistent, because self-consistency is exactly what
// a forger who mints a fresh keypair can always produce.
func (c ReleaseCertificate) VerifyTrusted(trusted TrustedKeys) error {
	if c.Signature == "" {
		return fmt.Errorf("%w: certificate is unsigned", ErrManifestTampered)
	}
	sum := sha256.Sum256(c.canonical())
	if hex.EncodeToString(sum[:]) != c.CertificateHash {
		return ErrManifestTampered
	}
	anchor, ok := trusted[c.SigningKeyID]
	if !ok {
		return fmt.Errorf("%w: key id %q", ErrUntrustedSigningKey, c.SigningKeyID)
	}
	sig, err := hex.DecodeString(c.Signature)
	if err != nil {
		return ErrManifestTampered
	}
	if !ed25519.Verify(anchor, c.canonical(), sig) {
		return ErrManifestTampered
	}
	return nil
}

// ReadinessManifest is the machine-readable "production ready is not
// an opinion" artifact (PHASE 52) — the whole assurance plane in one
// serializable document.
type ReadinessManifest struct {
	SchemaVersion string             `json:"schema_version"`
	Release       ReleaseCertificate `json:"release"`
	Acceptance    AcceptanceManifest `json:"acceptance"`
	Gates         []Gate             `json:"gates"`
	Assessment    Assessment         `json:"assessment"`
}

// ManifestSchemaVersion identifies the readiness manifest contract.
const ManifestSchemaVersion = "veriqo.readiness_manifest/v1"

// BuildReadinessManifest assembles the manifest from live state.
func BuildReadinessManifest(r *Registry, acc AcceptanceManifest, cert ReleaseCertificate) ReadinessManifest {
	a := r.Assess()
	acc.Hash = acc.ComputeHash()
	cert.AcceptanceManifestHash = acc.Hash
	cert = cert.Finalize(a)
	return ReadinessManifest{
		SchemaVersion: ManifestSchemaVersion, Release: cert,
		Acceptance: acc, Gates: r.Gates(), Assessment: a,
	}
}

// JSON renders the manifest deterministically.
func (m ReadinessManifest) JSON() ([]byte, error) { return json.MarshalIndent(m, "", "  ") }
