// Command veriqo-release-keygen generates a real Ed25519 keypair for
// signing READINESS_MANIFEST.json / ReleaseCertificate artifacts, and
// closes the v7.12.1 audit's explicit rule for HSM/KMS and release
// signing alike:
//
//	"Private signing key: tidak boleh masuk repository. Tidak boleh:
//	.env, config, Docker image, CI artifact, test artifact,
//	READINESS_MANIFEST."
//
// So this command writes the private key to a local file the caller
// names (never a path under the repo by default, and never printed to
// stdout) and writes only the PUBLIC half — keyed by a deterministic
// key ID derived from the public key itself — into
// docs/governance/TRUSTED_SIGNING_KEYS.json, which is safe to commit:
// it is a trust anchor, not a secret. cmd/veriqo-readiness's
// -signing-key flag reads the private key file this command produces;
// internal/assurance.ReleaseCertificate.VerifyTrusted reads the public
// trust-anchor file, never the certificate's own self-declared key.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	keyOut := flag.String("out", "", "path to write the PRIVATE key to (required; keep this outside the repo and out of version control)")
	trustFile := flag.String("trust-file", "docs/governance/TRUSTED_SIGNING_KEYS.json", "public trust-anchor file to add this key's public half to (safe to commit)")
	flag.Parse()

	if *keyOut == "" {
		fmt.Fprintln(os.Stderr, "veriqo-release-keygen: -out is required (where to write the private key file)")
		os.Exit(2)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-release-keygen: generate:", err)
		os.Exit(1)
	}
	keyID := deriveKeyID(pub)

	if err := os.WriteFile(*keyOut, []byte(hex.EncodeToString(priv)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-release-keygen: write private key:", err)
		os.Exit(1)
	}

	trust := map[string]string{}
	if raw, err := os.ReadFile(*trustFile); err == nil {
		_ = json.Unmarshal(raw, &trust)
	}
	trust[keyID] = hex.EncodeToString(pub)
	out, err := json.MarshalIndent(trust, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-release-keygen: marshal trust file:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*trustFile, out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-release-keygen: write trust file:", err)
		os.Exit(1)
	}

	fmt.Printf("veriqo-release-keygen: generated key id %s\n", keyID)
	fmt.Printf("  private key : %s (0600, keep out of version control)\n", *keyOut)
	fmt.Printf("  trust anchor: %s (public key only, commit this)\n", *trustFile)
}

// deriveKeyID matches internal/assurance's expectation that
// SigningKeyID identifies a public key deterministically, so a
// verifier never has to trust a caller-supplied label.
func deriveKeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}
