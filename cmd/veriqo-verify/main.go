// Command veriqo-verify is Veriqo's External Validator — the concrete
// artifact closing the "External Validator API" gap named across all
// three consolidated audit documents (Hal_yang_sangat_krusial.docx,
// Hasil_analisa_Palantir_dll.docx, Rekomendasi_prioritas_next_step.docx):
// an Independent Verification Framework that only ever ran inside the
// same process that produced the evidence is not independent in any
// way an outside auditor would accept.
//
// This binary is a genuinely separate OS process. It takes NOTHING but
// a bundle file (and, for the decision domain, a disclosed policy
// file) from disk — no shared memory, no shared globals, no import of
// cmd/veriqo-demo or any live Veriqo process. Anyone who can compile
// this repository — an investor's own engineer, a regulator's auditor,
// a court-appointed expert — can run it against a bundle Veriqo hands
// them and get a verdict Veriqo cannot influence after the fact. It
// calls the EXACT SAME pkg/engine.Replay{Contradiction,Fusion,Decision}
// functions the in-process path uses (see pkg/engine/replay.go), so
// there is no hand-maintained duplicate replay logic to drift out of
// sync between "the check Veriqo runs on itself" and "the check an
// outsider runs".
//
// It does still import the domain packages (via pkg/engine) to perform
// real algorithmic replay rather than merely checking hashes — the
// honest independence claim here is PROCESS independence (no shared
// runtime state with the run being checked), not zero-shared-code
// independence; see pkg/verification's own doc comment for the
// parallel caveat about organizational independence.
//
// Usage:
//
//	veriqo-verify -bundle path/to/bundle.json -domain contradiction
//	veriqo-verify -bundle path/to/bundle.json -domain fusion
//	veriqo-verify -bundle path/to/bundle.json -domain decision -policy path/to/policy.json
//
// Exit code 0 = PASSED (manifest valid, replay valid, certificate
// issued). Exit code 1 = FAILED. Exit code 2 = usage/input error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"veriqo/pkg/engine"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/verification"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("veriqo-verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bundlePath := fs.String("bundle", "", "path to a VerificationBundle JSON file")
	domain := fs.String("domain", "", "evidence domain: contradiction | decision | fusion")
	policyPath := fs.String("policy", "", "path to a disclosed decision.Policy JSON file (required when -domain=decision)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *bundlePath == "" || *domain == "" {
		fmt.Fprintln(stderr, "usage: veriqo-verify -bundle <file> -domain <contradiction|decision|fusion> [-policy <file>]")
		return 2
	}

	raw, err := os.ReadFile(*bundlePath)
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-verify: reading bundle: %v\n", err)
		return 2
	}
	var bundle verification.VerificationBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		fmt.Fprintf(stderr, "veriqo-verify: parsing bundle: %v\n", err)
		return 2
	}

	v := verification.NewVerifier()
	switch *domain {
	case "contradiction":
		v.RegisterReplayFunc("contradiction", engine.ReplayContradiction)
	case "fusion":
		v.RegisterReplayFunc("fusion", engine.ReplayFusion)
	case "decision":
		if *policyPath == "" {
			fmt.Fprintln(stderr, "veriqo-verify: -domain=decision requires -policy <file> (the disclosed rule being checked against)")
			return 2
		}
		praw, err := os.ReadFile(*policyPath)
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-verify: reading policy: %v\n", err)
			return 2
		}
		var policy decision.Policy
		if err := json.Unmarshal(praw, &policy); err != nil {
			fmt.Fprintf(stderr, "veriqo-verify: parsing policy: %v\n", err)
			return 2
		}
		v.RegisterReplayFunc("decision", engine.ReplayDecision(policy))
	default:
		fmt.Fprintf(stderr, "veriqo-verify: unknown -domain %q\n", *domain)
		return 2
	}

	result, err := v.Verify(*domain, bundle)
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-verify: VERIFICATION ERROR: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "veriqo-verify: external independent verification report\n")
	fmt.Fprintf(stdout, "  subject          : %s\n", bundle.ReplayPackage.Evidence.Subject)
	fmt.Fprintf(stdout, "  record count     : %d\n", bundle.Manifest.RecordCount)
	fmt.Fprintf(stdout, "  claimed result   : %s\n", bundle.ReplayPackage.ClaimedResult)
	fmt.Fprintf(stdout, "  recomputed result: %s\n", result.RecomputedResult)
	fmt.Fprintf(stdout, "  manifest valid   : %v\n", result.ManifestValid)
	fmt.Fprintf(stdout, "  replay valid     : %v\n", result.ReplayValid)

	if result.ManifestValid && result.ReplayValid && result.Certificate != nil {
		fmt.Fprintf(stdout, "  VERDICT          : PASSED\n")
		fmt.Fprintf(stdout, "  certificate hash : %s\n", result.Certificate.CertificateHash)
		fmt.Fprintf(stdout, "  self-verifies    : %v\n", verification.VerifyCertificate(*result.Certificate))
		return 0
	}
	fmt.Fprintf(stdout, "  VERDICT          : FAILED — evidence does not independently reproduce the claimed result\n")
	return 1
}
