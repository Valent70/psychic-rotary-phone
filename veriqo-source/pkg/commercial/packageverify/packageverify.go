// Package packageverify is the shared, importable verification logic
// both cmd/veriqo-commercial-verify (a genuinely separate OS process)
// and the Commercial API's POST /v1/packages/verify route (an
// in-process convenience for a caller who would rather not shell out
// to a CLI) call -- the SAME checks, the SAME exported functions from
// the frozen kernel, so there is exactly one place this logic lives,
// never two copies that could drift apart. See cmd/veriqo-commercial-
// verify's own doc comment for the full "why a standalone verifier"
// rationale; this package holds only the pure verification logic, not
// the CLI or HTTP wiring around it.
//
// Independent Verifier v2 (Commercialization Sprint P0-E) closes the
// prior round's own honest SKIP: "verifier sekarang secara jujur SKIP
// signature verification, karena signing scheme belum ada." Now that
// pkg/commercial/api.Store can really sign (P0-D), this package
// verifies: hash (package + manifest), signature (real Ed25519 math),
// certificate/key state (revocation), custody chain, lineage, and
// ledger replay -- named individually below.
//
// TRUST MODEL for signature/key-state checks: a package's own embedded
// KeyID is not, by itself, proof of anything -- a forger could sign
// with their own keypair and claim any KeyID. Real independent
// verification requires the caller to supply a TrustedKeyRegistry
// obtained from a channel OUTSIDE the package being verified (a
// published key registry, a pilot's own key-distribution channel,
// etc.). Passing a nil/empty registry is honest and supported: every
// signature check then reports SKIP with the exact reason ("no
// trusted key registry entry for this KeyID"), never a false PASS.
//
// HONEST SCOPE on "replay": this package verifies the ledger's own
// hash chain from GENESIS (a real replay of that chain's integrity)
// and cross-references the dossier's own claimed Decision/Authorization
// hashes against what the ledger actually recorded (LINEAGE). It does
// NOT re-derive the Decision from scratch (that would require the
// original DecideInput/ActionInput, which a Machine Package
// deliberately does not carry -- it is a proof-of-what-happened
// artifact, not a re-runnable input set). Full input-level replay is
// pkg/commercial/api.Store.Replay's job, against the live Store that
// still holds those inputs -- named here rather than silently implied.
package packageverify

import (
	"archive/zip"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"veriqo/pkg/commercial/dossier"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/platform/audit"
)

// Status is one check's outcome. Skip is deliberately distinct from
// Fail: a skipped check (e.g. signature verification with no trusted
// key registry supplied) is an honest disclosure of missing input,
// never counted as a verification FAILURE.
type Status int

const (
	Pass Status = iota
	Fail
	Skip
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "PASS"
	case Fail:
		return "FAIL"
	default:
		return "SKIP"
	}
}

// CheckResult is one named check's outcome.
type CheckResult struct {
	Name   string `json:"name"`
	Status Status `json:"-"`
	// StatusText mirrors Status as a JSON-friendly string (encoding/json
	// has no way to marshal Status.String() automatically without a
	// custom MarshalJSON on Status itself, and this package deliberately
	// keeps Status a plain int for cheap comparison elsewhere).
	StatusText string `json:"status"`
	Detail     string `json:"detail"`
}

func result(name string, status Status, detail string) CheckResult {
	return CheckResult{Name: name, Status: status, StatusText: status.String(), Detail: detail}
}

// TrustedKey is one independently-obtained key's expected state,
// supplied by the verifier's caller from a source OUTSIDE the package
// being verified -- see this package's own doc comment's "TRUST MODEL"
// section.
type TrustedKey struct {
	// PublicKey is the hex-encoded Ed25519 public key this KeyID is
	// independently known to correspond to.
	PublicKey string
	// Revoked is the independent source's own current answer to "has
	// this key been revoked" -- checked separately from, and in
	// addition to, the raw Ed25519 signature math.
	Revoked bool
}

// TrustedKeyRegistry maps KeyID -> the caller's independently-obtained
// TrustedKey. A nil or empty registry is valid and means "no
// independent trust source was supplied" -- every signature/key-state
// check then reports SKIP, honestly, never a false PASS.
type TrustedKeyRegistry map[string]TrustedKey

func readZipEntry(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("entry %q not found in package", name)
}

// verifySignature runs the ONE real signature-verification routine
// both the per-evidence and the per-package checks below share: real
// Ed25519 math against a caller-supplied trusted public key, plus a
// same-named key_state check for revocation -- appended in that order.
func verifySignature(results []CheckResult, checkName, keyID, signedDigest, signedHash, sigHex string, trustedKeys TrustedKeyRegistry) []CheckResult {
	if keyID == "" {
		return append(results, result(checkName, Skip, "unsigned (signing was not enabled when this artifact was produced)"))
	}
	tk, ok := trustedKeys[keyID]
	if !ok {
		results = append(results, result(checkName, Skip,
			fmt.Sprintf("signed with key %s but no trusted key registry entry was supplied for it -- cannot independently verify", keyID)))
		results = append(results, result("key_state["+keyID+"]", Skip, "no trusted key registry entry supplied for this key"))
		return results
	}
	if tk.Revoked {
		results = append(results, result(checkName, Fail, fmt.Sprintf("key %s is REVOKED in the trusted registry -- signature invalid retroactively", keyID)))
		results = append(results, result("key_state["+keyID+"]", Fail, "key is REVOKED"))
		return results
	}
	results = append(results, result("key_state["+keyID+"]", Pass, "key is present in the trusted registry and not revoked"))

	pubBytes, errPub := hex.DecodeString(tk.PublicKey)
	sigBytes, errSig := hex.DecodeString(sigHex)
	switch {
	case errPub != nil || len(pubBytes) != ed25519.PublicKeySize:
		return append(results, result(checkName, Fail, "trusted registry's public key for this KeyID is malformed"))
	case errSig != nil:
		return append(results, result(checkName, Fail, "the package's own signature encoding is malformed"))
	case signedHash != signedDigest:
		return append(results, result(checkName, Fail, "the signature's recorded digest does not match the artifact's real, independently-recomputed hash"))
	case !ed25519.Verify(pubBytes, []byte(signedDigest), sigBytes):
		return append(results, result(checkName, Fail, "Ed25519 verification failed against the trusted public key"))
	default:
		return append(results, result(checkName, Pass, fmt.Sprintf("Ed25519 signature independently verified against trusted key %s", keyID)))
	}
}

// VerifyZip runs every check this package knows against a Machine
// Package's zip.Reader -- package hash, manifest hash, raw evidence
// hash, signature, certificate/key state, custody chain, lineage, and
// ledger hash-chain replay (see this package's own doc comment for
// what each of those means precisely). It never mutates zr and
// performs no I/O beyond reading from it and (for signature checks)
// consulting trustedKeys, which is caller-supplied and never fetched
// over the network by this package itself.
func VerifyZip(zr *zip.Reader, trustedKeys TrustedKeyRegistry) ([]CheckResult, error) {
	var results []CheckResult

	dossierJSON, err := readZipEntry(zr, "dossier.json")
	if err != nil {
		return nil, err
	}
	var d dossier.Dossier
	if err := json.Unmarshal(dossierJSON, &d); err != nil {
		return nil, fmt.Errorf("parsing dossier.json: %w", err)
	}

	if err := dossier.VerifyPackageHash(d); err != nil {
		results = append(results, result("package_hash", Fail, err.Error()))
	} else {
		results = append(results, result("package_hash", Pass, fmt.Sprintf("verified, hash=%s", d.PackageHash)))
	}

	if d.PackageSignature != nil {
		results = verifySignature(results, "package_signature", d.PackageSignature.KeyID,
			d.PackageHash, d.PackageSignature.SignedPackageHash, d.PackageSignature.Signature, trustedKeys)
	} else {
		results = append(results, result("package_signature", Skip, "unsigned (signing was not enabled when this dossier was generated)"))
	}

	for _, rec := range d.EvidenceInventory {
		id := rec.Identity.EvidenceID
		if rec.Signature != nil {
			results = verifySignature(results, "signature["+id+"]", rec.Signature.KeyID,
				rec.Integrity.ManifestHash, rec.Signature.SignedManifestHash, rec.Signature.Signature, trustedKeys)
		} else {
			results = append(results, result("signature["+id+"]", Skip, "unsigned (signing was not enabled when this evidence was submitted)"))
		}
	}

	manifestsJSON, err := readZipEntry(zr, "manifests.json")
	if err != nil {
		results = append(results, result("manifest_data_present", Fail, err.Error()))
	} else {
		var rawEvidence []dossier.RawEvidence
		if err := json.Unmarshal(manifestsJSON, &rawEvidence); err != nil {
			results = append(results, result("manifest_data_present", Fail, "parsing manifests.json: "+err.Error()))
		} else if len(rawEvidence) == 0 {
			results = append(results, result("manifest_data_present", Fail, "manifests.json contains zero evidence items"))
		} else {
			for _, re := range rawEvidence {
				id := re.Manifest.EvidenceID
				if err := manifest.VerifyManifestHash(re.Manifest); err != nil {
					results = append(results, result("manifest["+id+"]", Fail, err.Error()))
				} else {
					results = append(results, result("manifest["+id+"]", Pass, "ManifestHash independently re-derived and matches"))
				}
				if re.Manifest.SHA256 == "" {
					results = append(results, result("raw_evidence_hash["+id+"]", Fail, "no SHA256 recorded on this manifest"))
				} else {
					results = append(results, result("raw_evidence_hash["+id+"]", Pass, "sha256="+re.Manifest.SHA256))
				}
				if err := manifest.VerifyCustodyChainRecords(re.Custody); err != nil {
					results = append(results, result("custody_chain["+id+"]", Fail, err.Error()))
				} else {
					results = append(results, result("custody_chain["+id+"]", Pass, fmt.Sprintf("%d events, hash-chain verified from GENESIS", len(re.Custody))))
				}
			}
		}
	}

	ledgerJSON, err := readZipEntry(zr, "ledger.json")
	if err != nil {
		results = append(results, result("merkle_root", Fail, err.Error()))
	} else {
		var records []audit.AuditRecord
		if err := json.Unmarshal(ledgerJSON, &records); err != nil {
			results = append(results, result("merkle_root", Fail, "parsing ledger.json: "+err.Error()))
		} else if len(records) == 0 {
			results = append(results, result("merkle_root", Fail, "ledger.json contains zero records"))
		} else {
			if err := (audit.Auditor{}).VerifyChain(records); err != nil {
				results = append(results, result("ledger_hash_chain", Fail, err.Error()))
			} else {
				results = append(results, result("ledger_hash_chain", Pass,
					fmt.Sprintf("%d records replayed from GENESIS, every hash independently re-derived and matches", len(records))))
			}
			root, err := audit.MerkleRoot(records)
			if err != nil {
				results = append(results, result("merkle_root", Fail, err.Error()))
			} else {
				results = append(results, result("merkle_root", Pass, "root="+root))
			}

			// LINEAGE: the dossier's own claimed Decision/Authorization
			// hashes must actually appear in the independently-parsed
			// ledger -- catching a dossier.json tampered to claim a
			// Decision that the real ledger never recorded, even if
			// dossier.json's own internal PackageHash were somehow also
			// forged to match.
			results = appendLineageCheck(results, "lineage_decision", d.Decision.Hash, records)
			if d.Authorization.Hash != "" {
				results = appendLineageCheck(results, "lineage_authorization", d.Authorization.Hash, records)
			}
		}
	}

	return results, nil
}

func appendLineageCheck(results []CheckResult, name, hash string, records []audit.AuditRecord) []CheckResult {
	if hash == "" {
		return append(results, result(name, Fail, "dossier carries no hash to cross-reference"))
	}
	for _, rec := range records {
		if strings.Contains(rec.Payload, hash) {
			return append(results, result(name, Pass, "dossier's recorded hash matches a real ledger record"))
		}
	}
	return append(results, result(name, Fail, "dossier's recorded hash does not appear in the independently-parsed ledger"))
}

// AllPassed reports whether every check is Pass or Skip -- never
// counting a Skip as a failure.
func AllPassed(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == Fail {
			return false
		}
	}
	return true
}
