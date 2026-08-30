// Command veriqo-commercial-verify answers Commercialization Sprint
// item 9 directly: "Independent verifier -- buat versi minimal dulu ...
// Phase 1: standalone Go verifier harus bisa memverifikasi: package
// hash, manifest, raw evidence hash, signature, custody chain, Merkle
// root."
//
// This is a genuinely separate OS process, in the same spirit as
// cmd/veriqo-verify: it takes NOTHING but a Machine Package .zip file
// from disk (the one pkg/commercial/dossier.WriteMachinePackage
// produces) -- no shared memory, no import of any live server process.
// All verification logic lives in pkg/commercial/packageverify, the
// SAME package the Commercial API's POST /v1/packages/verify route
// calls in-process, so there is exactly one implementation of these
// checks, never two that could drift out of sync.
//
// Usage:
//
//	veriqo-commercial-verify -package path/to/dossier-package.zip
//
// Exit code 0 = ALL CHECKS PASSED (Skip lines noted but not counted as
// failures). Exit code 1 = one or more checks FAILED. Exit code 2 =
// usage/input error.
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"os"

	"veriqo/pkg/commercial/packageverify"
)

func main() {
	packagePath := flag.String("package", "", "path to a Machine Package .zip produced by dossier.WriteMachinePackage")
	flag.Parse()

	if *packagePath == "" {
		fmt.Fprintln(os.Stderr, "veriqo-commercial-verify: -package is required")
		os.Exit(2)
	}

	r, err := zip.OpenReader(*packagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-commercial-verify: opening package: %v\n", err)
		os.Exit(2)
	}
	defer r.Close()

	results, err := packageverify.VerifyZip(&r.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-commercial-verify: %v\n", err)
		os.Exit(2)
	}

	for _, res := range results {
		fmt.Printf("[%s] %-32s %s\n", res.StatusText, res.Name, res.Detail)
	}
	fmt.Println()
	if packageverify.AllPassed(results) {
		fmt.Println("VERDICT: ALL CHECKS PASSED (see SKIP lines above for checks this reference build does not yet implement)")
		os.Exit(0)
	}
	fmt.Println("VERDICT: ONE OR MORE CHECKS FAILED")
	os.Exit(1)
}
