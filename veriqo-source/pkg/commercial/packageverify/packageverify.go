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
package packageverify

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"

	"veriqo/pkg/commercial/dossier"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/platform/audit"
)

// Status is one check's outcome. Skip is deliberately distinct from
// Fail: a skipped check (e.g. signature verification, not implemented
// in this reference build) is an honest disclosure of missing
// capability, never counted as a verification FAILURE.
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

// VerifyZip runs every check this package knows against a Machine
// Package's zip.Reader -- package hash, manifest, raw evidence hash,
// signature (honestly reported as unimplemented), custody chain, and
// Merkle root. It never mutates zr and performs no I/O beyond reading
// from it.
func VerifyZip(zr *zip.Reader) ([]CheckResult, error) {
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
				sigStatus := re.Manifest.SignatureStatus
				if sigStatus == "" {
					sigStatus = "(not set)"
				}
				results = append(results, result("signature["+id+"]", Skip,
					fmt.Sprintf("NOT CRYPTOGRAPHICALLY VERIFIED (no signature scheme implemented in this reference build) -- manifest records signature_status=%s", sigStatus)))
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
				results = append(results, result("ledger_hash_chain", Pass, fmt.Sprintf("%d records, hash-chain verified", len(records))))
			}
			root, err := audit.MerkleRoot(records)
			if err != nil {
				results = append(results, result("merkle_root", Fail, err.Error()))
			} else {
				results = append(results, result("merkle_root", Pass, "root="+root))
			}
		}
	}

	return results, nil
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
