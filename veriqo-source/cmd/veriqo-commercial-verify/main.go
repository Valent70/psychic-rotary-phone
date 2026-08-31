// Command veriqo-commercial-verify answers Commercialization Sprint
// item 9 directly: "Independent verifier -- buat versi minimal dulu ...
// Phase 1: standalone Go verifier harus bisa memverifikasi: package
// hash, manifest, raw evidence hash, signature, custody chain, Merkle
// root," and, per this round's own P0-E follow-up ("Independent
// verifier v2 ... harus memverifikasi: hash, manifest, signature,
// certificate/key state, custody chain, Merkle proof, lineage, replay
// -- dan bukan SKIP signature lagi"), real Ed25519 signature and
// key-state verification against a caller-supplied trusted key
// registry, plus lineage cross-referencing.
//
// This is a genuinely separate OS process, in the same spirit as
// cmd/veriqo-verify: it takes NOTHING but a Machine Package .zip file
// from disk (the one pkg/commercial/dossier.WriteMachinePackage
// produces) and, optionally, a trusted-key registry file the caller
// obtained from a channel OUTSIDE the package itself -- no shared
// memory, no import of any live server process. All verification
// logic lives in pkg/commercial/packageverify, the SAME package the
// Commercial API's POST /v1/packages/verify route calls in-process,
// so there is exactly one implementation of these checks, never two
// that could drift out of sync.
//
// Usage:
//
//	veriqo-commercial-verify -package path/to/dossier-package.zip [-trusted-keys path/to/keys.json]
//
// The trusted-keys file is a JSON object: {"<key_id>": {"public_key":
// "<hex>", "revoked": false}, ...}. Omitting -trusted-keys is honest
// and supported -- every signature/key-state check then reports SKIP
// with the exact reason, never a false PASS.
//
// Exit code 0 = ALL CHECKS PASSED (Skip lines noted but not counted as
// failures). Exit code 1 = one or more checks FAILED. Exit code 2 =
// usage/input error.
package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"veriqo/pkg/commercial/packageverify"
)

func loadTrustedKeys(path string) (packageverify.TrustedKeyRegistry, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is an operator-supplied CLI flag, not external input
	if err != nil {
		return nil, fmt.Errorf("reading trusted-keys file: %w", err)
	}
	var raw map[string]struct {
		PublicKey string `json:"public_key"`
		Revoked   bool   `json:"revoked"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing trusted-keys file: %w", err)
	}
	reg := make(packageverify.TrustedKeyRegistry, len(raw))
	for keyID, v := range raw {
		reg[keyID] = packageverify.TrustedKey{PublicKey: v.PublicKey, Revoked: v.Revoked}
	}
	return reg, nil
}

func main() {
	packagePath := flag.String("package", "", "path to a Machine Package .zip produced by dossier.WriteMachinePackage")
	trustedKeysPath := flag.String("trusted-keys", "", "path to a JSON trusted-key registry (optional; omitting it means signature/key-state checks honestly SKIP)")
	flag.Parse()

	if *packagePath == "" {
		fmt.Fprintln(os.Stderr, "veriqo-commercial-verify: -package is required")
		os.Exit(2)
	}

	trustedKeys, err := loadTrustedKeys(*trustedKeysPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-commercial-verify: %v\n", err)
		os.Exit(2)
	}

	r, err := zip.OpenReader(*packagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-commercial-verify: opening package: %v\n", err)
		os.Exit(2)
	}
	defer r.Close()

	results, err := packageverify.VerifyZip(&r.Reader, trustedKeys)
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-commercial-verify: %v\n", err)
		os.Exit(2)
	}

	for _, res := range results {
		fmt.Printf("[%s] %-32s %s\n", res.StatusText, res.Name, res.Detail)
	}
	fmt.Println()
	if packageverify.AllPassed(results) {
		fmt.Println("VERDICT: ALL CHECKS PASSED (see SKIP lines above for checks this reference build could not independently verify)")
		os.Exit(0)
	}
	fmt.Println("VERDICT: ONE OR MORE CHECKS FAILED")
	os.Exit(1)
}
