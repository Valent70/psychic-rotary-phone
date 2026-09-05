// Command veriqo-verify checks a VERIQO evidence bundle without asking
// VERIQO anything.
//
// It is deliberately a separate binary from veriqoctl. veriqoctl
// reports what VERIQO believes about itself; this reports what a bundle
// can be made to support. Somebody who does not trust VERIQO should
// not have to run VERIQO's own reporting tool to find out.
//
//	veriqo-verify [flags] <bundle-directory>
//
// Exit status is 0 only when every step that could run, passed, AND
// the qualification the bundle claims matches the one derived from its
// contents. A bundle that claims more than it carries exits non-zero
// even when nothing in it is forged.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"veriqo/pkg/verification"
)

func main() {
	var (
		keysPath string
		revPath  string
		atStr    string
		asJSON   bool
	)
	flag.StringVar(&keysPath, "keys", "", "JSON file mapping key id to hex ed25519 public "+
		"key, obtained from a channel that is NOT the bundle. Without it the bundle's own "+
		"keys are used, which establishes nothing about whose keys they are")
	flag.StringVar(&revPath, "revocations", "", "JSON array of revoked finding ids obtained "+
		"independently. Without it, revocation is reported as NOT CHECKED rather than as "+
		"not revoked")
	flag.StringVar(&atStr, "at", "", "RFC3339 instant to check validity against (default: now)")
	flag.BoolVar(&asJSON, "json", false, "emit the report as JSON")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}

	b, err := verification.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-verify: %v\n", err)
		fmt.Fprintln(os.Stderr, "\nThe bundle does not match its own manifest, or is not a "+
			"bundle. Nothing further can be checked.")
		os.Exit(1)
	}

	opts := verification.Options{}
	if keysPath != "" {
		opts.TrustedKeys, err = loadKeys(keysPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "veriqo-verify: %v\n", err)
			os.Exit(2)
		}
	}
	if revPath != "" {
		opts.Revocations, err = loadRevocations(revPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "veriqo-verify: %v\n", err)
			os.Exit(2)
		}
	}
	if atStr != "" {
		opts.At, err = time.Parse(time.RFC3339, atStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "veriqo-verify: -at: %v\n", err)
			os.Exit(2)
		}
	}

	r := verification.Verify(b, opts)

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			fmt.Fprintf(os.Stderr, "veriqo-verify: %v\n", err)
			os.Exit(2)
		}
	} else {
		fmt.Print(r.Render())
	}

	if !r.Verified() {
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `veriqo-verify <bundle-directory>

Checks a VERIQO evidence bundle by recomputing everything in it. No
value is read from the bundle and reported as fact: artefact digests
are recomputed from the bytes, the ledger is rehashed from genesis,
the passport digest is recomputed from its payload before the
signature is checked, and the qualification state is DERIVED and then
compared with what the bundle claims.

Exit status 0 means every step that could run, passed, and the claimed
and derived qualifications agree.

Three things this tool cannot establish, and says so on every run:

  key authenticity      a bundle produced by an impostor is internally
                        perfect; pass -keys with keys you obtained
                        elsewhere
  existence in time     without an external anchor a hash chain proves
                        only its own consistency
  absent evidence       nothing is said about what was left out

Flags:
`)
	flag.PrintDefaults()
}

func loadKeys(path string) (map[string]ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]ed25519.PublicKey{}
	for id, h := range m {
		b, err := hex.DecodeString(strings.TrimSpace(h))
		if err != nil {
			return nil, fmt.Errorf("%s: key %q is not hex: %w", path, id, err)
		}
		if len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%s: key %q is %d bytes, not an ed25519 public key",
				path, id, len(b))
		}
		out[id] = ed25519.PublicKey(b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s contains no keys", path)
	}
	return out, nil
}

func loadRevocations(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// A deliberately empty list is a real answer -- "I checked and it
	// is not on the list" -- and must not become "I did not check".
	if out == nil {
		out = []string{}
	}
	return out, nil
}
