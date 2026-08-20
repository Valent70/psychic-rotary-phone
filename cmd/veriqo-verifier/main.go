// Command veriqo-verifier is external audit item A16's Independent
// Verification Tool — marked by the audit as its single most important
// deliverable ("Ini sangat penting... salah satu bukti terkuat untuk
// audit").
//
// It is a genuinely, structurally standalone verifier: its only
// non-stdlib import is veriqo/pkg/verification, and that package itself
// imports nothing else from this repository (confirmed by
// docs/AUDIT_A1_A18_GAP_MAPPING.md's A16 entry — every file in
// pkg/verification was checked, zero veriqo/ internal imports found).
// This binary therefore does not link against pkg/canonical,
// pkg/execution, pkg/lifecycle, veriqo/kernel, or any domain engine —
// it cannot be influenced by, and does not need to trust, anything the
// live VERIQO runtime does. Anyone who can compile this one small
// package and pkg/verification can independently check a certificate
// VERIQO issued, without running VERIQO at all.
//
// This is a narrower, more literal answer to the audit's own input/
// output spec than cmd/veriqo-verify (which does genuine domain replay
// via pkg/engine, at the honest cost of importing domain packages — see
// that command's own doc comment on "process independence, not
// zero-shared-code independence"). veriqo-verifier instead verifies
// exactly the audit's five named inputs — manifest, signature, public
// key, evidence root, artifact hashes — plus a trust registry, and
// produces exactly the audit's four named output states. It does not
// replay domain-specific arbitration/decision logic; that check remains
// cmd/veriqo-verify's job. A caller wanting both independence properties
// runs both tools.
//
// Usage:
//
//	veriqo-verifier \
//	  -certificate cert.json \       (required: verification.SignedCertificate JSON)
//	  -trust-registry registry.json \ (required: verification.TrustRegistry JSON)
//	  -bundle bundle.json \          (optional: verification.VerificationBundle JSON;
//	                                   cross-checks the certificate's VerifiedRoot against
//	                                   an independent recomputation from raw evidence)
//	  -evidence-root <hex> \          (optional: caller's own independently-expected root
//	                                   hash, cross-checked against the certificate's)
//	  -artifacts artifacts.json       (optional: {"name": {"path": "...", "expected_sha256": "..."}})
//
// Exit codes: 0=VALID, 1=INVALID, 2=INCOMPLETE, 3=TRUST_REVOKED, 4=usage/input error.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"veriqo/pkg/verification"
)

// Outcome is the audit's exact required 4-state output vocabulary.
type Outcome string

const (
	OutcomeValid        Outcome = "VALID"
	OutcomeInvalid      Outcome = "INVALID"
	OutcomeIncomplete   Outcome = "INCOMPLETE"
	OutcomeTrustRevoked Outcome = "TRUST_REVOKED"
)

// Report is what this command prints — every field a real auditor would
// want to see, not just the bare outcome word.
type Report struct {
	Outcome        Outcome  `json:"outcome"`
	Reasons        []string `json:"reasons,omitempty"`
	Subject        string   `json:"subject,omitempty"`
	VerifiedRoot   string   `json:"verified_root,omitempty"`
	PublicKey      string   `json:"public_key,omitempty"`
	TrustState     string   `json:"trust_state,omitempty"`
	BundleRootMatch *bool   `json:"bundle_root_match,omitempty"`
	EvidenceRootMatch *bool `json:"evidence_root_match,omitempty"`
	ArtifactResults map[string]string `json:"artifact_results,omitempty"` // name -> "match" | "mismatch" | "unreadable: <err>"
}

type artifactSpec struct {
	Path           string `json:"path"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

func main() {
	os.Exit(run())
}

func run() int {
	certPath := flag.String("certificate", "", "path to a verification.SignedCertificate JSON file (required)")
	registryPath := flag.String("trust-registry", "", "path to a verification.TrustRegistry JSON file (required)")
	bundlePath := flag.String("bundle", "", "optional path to a verification.VerificationBundle JSON file")
	evidenceRoot := flag.String("evidence-root", "", "optional caller-supplied expected root hash (hex)")
	artifactsPath := flag.String("artifacts", "", "optional path to a {name: {path, expected_sha256}} JSON file")
	flag.Parse()

	if *certPath == "" || *registryPath == "" {
		fmt.Fprintln(os.Stderr, "usage: veriqo-verifier -certificate cert.json -trust-registry registry.json [-bundle bundle.json] [-evidence-root hex] [-artifacts artifacts.json]")
		return 4
	}

	report, outcome := verify(*certPath, *registryPath, *bundlePath, *evidenceRoot, *artifactsPath)
	report.Outcome = outcome

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verifier: marshal report:", err)
		return 4
	}
	fmt.Println(string(out))

	switch outcome {
	case OutcomeValid:
		return 0
	case OutcomeInvalid:
		return 1
	case OutcomeIncomplete:
		return 2
	case OutcomeTrustRevoked:
		return 3
	default:
		return 4
	}
}

func verify(certPath, registryPath, bundlePath, evidenceRoot, artifactsPath string) (Report, Outcome) {
	var report Report

	certData, err := os.ReadFile(certPath)
	if err != nil {
		report.Reasons = append(report.Reasons, "cannot read certificate file: "+err.Error())
		return report, OutcomeIncomplete
	}
	var cert verification.SignedCertificate
	if err := json.Unmarshal(certData, &cert); err != nil {
		report.Reasons = append(report.Reasons, "cannot parse certificate JSON: "+err.Error())
		return report, OutcomeIncomplete
	}
	report.Subject = cert.Certificate.Subject
	report.VerifiedRoot = cert.Certificate.VerifiedRoot
	report.PublicKey = cert.PublicKey

	registryData, err := os.ReadFile(registryPath)
	if err != nil {
		report.Reasons = append(report.Reasons, "cannot read trust registry file: "+err.Error())
		return report, OutcomeIncomplete
	}
	registry, err := verification.LoadTrustRegistry(registryData)
	if err != nil {
		report.Reasons = append(report.Reasons, "cannot parse trust registry: "+err.Error())
		return report, OutcomeIncomplete
	}

	// Trust state is checked BEFORE the signature is even verified: a
	// revoked key is refused regardless of whether it would otherwise
	// produce a cryptographically valid signature — revocation exists
	// precisely to invalidate a key's past-valid signing capability.
	state := registry.StateOf(cert.PublicKey)
	report.TrustState = string(state)
	if state == verification.TrustRevoked {
		report.Reasons = append(report.Reasons, "public key is explicitly REVOKED in the trust registry")
		return report, OutcomeTrustRevoked
	}

	if err := verification.VerifySigned(cert); err != nil {
		report.Reasons = append(report.Reasons, "signature verification failed: "+err.Error())
		return report, OutcomeInvalid
	}

	if state != verification.TrustActive {
		// A cryptographically valid signature from a key this registry
		// never vouched for is NOT the same as a valid certificate — an
		// attacker's own freshly-generated keypair signs perfectly fine.
		report.Reasons = append(report.Reasons, "public key is not ACTIVE in the trust registry (state="+string(state)+")")
		return report, OutcomeInvalid
	}

	valid := true

	if bundlePath != "" {
		bundleData, err := os.ReadFile(bundlePath)
		if err != nil {
			report.Reasons = append(report.Reasons, "cannot read bundle file: "+err.Error())
			return report, OutcomeIncomplete
		}
		bundle, err := verification.Unmarshal(bundleData)
		if err != nil {
			report.Reasons = append(report.Reasons, "cannot parse bundle JSON: "+err.Error())
			return report, OutcomeIncomplete
		}
		recomputed, err := verification.BuildManifest(bundle.ReplayPackage)
		if err != nil {
			report.Reasons = append(report.Reasons, "cannot recompute manifest from bundle: "+err.Error())
			return report, OutcomeIncomplete
		}
		match := recomputed.RootHash == cert.Certificate.VerifiedRoot
		report.BundleRootMatch = &match
		if !match {
			valid = false
			report.Reasons = append(report.Reasons, fmt.Sprintf("bundle's independently-recomputed root %s does not match certificate's VerifiedRoot %s", recomputed.RootHash, cert.Certificate.VerifiedRoot))
		}
	}

	if evidenceRoot != "" {
		match := evidenceRoot == cert.Certificate.VerifiedRoot
		report.EvidenceRootMatch = &match
		if !match {
			valid = false
			report.Reasons = append(report.Reasons, fmt.Sprintf("caller-supplied evidence root %s does not match certificate's VerifiedRoot %s", evidenceRoot, cert.Certificate.VerifiedRoot))
		}
	}

	if artifactsPath != "" {
		artifactsData, err := os.ReadFile(artifactsPath)
		if err != nil {
			report.Reasons = append(report.Reasons, "cannot read artifacts file: "+err.Error())
			return report, OutcomeIncomplete
		}
		var specs map[string]artifactSpec
		if err := json.Unmarshal(artifactsData, &specs); err != nil {
			report.Reasons = append(report.Reasons, "cannot parse artifacts JSON: "+err.Error())
			return report, OutcomeIncomplete
		}
		report.ArtifactResults = make(map[string]string, len(specs))
		for name, spec := range specs {
			data, err := os.ReadFile(spec.Path)
			if err != nil {
				report.ArtifactResults[name] = "unreadable: " + err.Error()
				return report, OutcomeIncomplete
			}
			sum := sha256.Sum256(data)
			got := hex.EncodeToString(sum[:])
			if got == spec.ExpectedSHA256 {
				report.ArtifactResults[name] = "match"
			} else {
				report.ArtifactResults[name] = "mismatch"
				valid = false
				report.Reasons = append(report.Reasons, fmt.Sprintf("artifact %q: computed hash %s does not match expected %s", name, got, spec.ExpectedSHA256))
			}
		}
	}

	if !valid {
		return report, OutcomeInvalid
	}
	return report, OutcomeValid
}
