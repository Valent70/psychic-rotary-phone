// Command veriqo-verify-release is the independent counterpart to
// cmd/veriqo-release-keygen: it checks a READINESS_MANIFEST.json's
// release certificate against a trust-anchor file, never against the
// certificate's own embedded public key. Anyone who has the trust file
// (public keys only — safe to hand to a regulator, an investor's
// engineer, or a CI job that never sees the private key) can run this
// against a manifest Veriqo produced and get a verdict Veriqo cannot
// influence after the fact: internal/assurance.ReleaseCertificate.
// VerifyTrusted closes the audit's "Internal Gap III" ("signature
// benar-benar diverifikasi dengan public key/trust anchor, bukan
// sekadar signature != ”") for exactly this call path.
//
// Exit 0 = signature verified against a trusted key. Exit 1 = failed
// (untrusted key, tampered certificate, or unsigned manifest). Exit 2
// = usage/input error.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"veriqo/internal/assurance"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("veriqo-verify-release", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "READINESS_MANIFEST.json", "path to a READINESS_MANIFEST.json")
	trustPath := fs.String("trust-file", "docs/governance/TRUSTED_SIGNING_KEYS.json", "public trust-anchor file (key id -> hex public key)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verify-release: read manifest:", err)
		return 2
	}
	var m assurance.ReadinessManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verify-release: parse manifest:", err)
		return 2
	}

	trustRaw, err := os.ReadFile(*trustPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verify-release: read trust file:", err)
		return 2
	}
	var trustHex map[string]string
	if err := json.Unmarshal(trustRaw, &trustHex); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verify-release: parse trust file:", err)
		return 2
	}
	trust := assurance.TrustedKeys{}
	for id, hx := range trustHex {
		pub, err := hex.DecodeString(hx)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			fmt.Fprintf(os.Stderr, "veriqo-verify-release: trust file entry %q is not a valid ed25519 public key\n", id)
			return 2
		}
		trust[id] = ed25519.PublicKey(pub)
	}

	if err := m.Release.VerifyTrusted(trust); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verify-release: FAILED:", err)
		return 1
	}
	fmt.Printf("veriqo-verify-release: PASSED — release certificate for %s (commit %s) verified against trusted key %s\n",
		m.Release.Version, m.Release.GitCommit, m.Release.SigningKeyID)
	return 0
}
