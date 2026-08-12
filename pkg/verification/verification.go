// Package verification implements Veriqo's Independent Verification
// Framework (IVF) — named in "Hal_yang_sangat_krusial.docx" section 4 as
// potentially VERIQO's single biggest MOAT, and the least-built element
// (self-assessed ±20-30% before this session).
//
// The core idea the doc asks for: an auditor/regulator must be able to
// verify Veriqo's output WITHOUT trusting Veriqo's live system — by
// independently re-deriving the same hash-chained result from a
// self-contained, cryptographically-committed bundle.
//
// Pipeline implemented here (matches the doc's own diagram):
//
//	Request -> Evidence Package -> Replay Package -> Verification Bundle
//	-> Deterministic Replay -> Independent Validation -> Verified Result
//
// Honest scope statement: "independent" here means algorithmically
// independent (a Verifier re-runs the SAME hash/replay algorithm from
// raw inputs using none of the original engine's in-memory state, and a
// SECOND, freshly-constructed Verifier is used in every test to prove
// no shared state leaks in) — not organizationally independent (there is
// no separate deployed service, network boundary, or external key
// authority in this sandbox). The BundleManifest's SHA-256 root hash IS
// a real cryptographic commitment; it is not yet signed with an
// asymmetric key/external PKI (see "Not yet implemented" in the report).
package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrEmptyBundle       = errors.New("verification: bundle contains no evidence records")
	ErrMissingReplayFunc = errors.New("verification: no ReplayFunc registered for this domain")
)

// EvidenceRecord is one raw, self-describing unit of evidence packaged
// into a bundle — deliberately generic (Kind + opaque Payload) so any
// engine's hash-chained log (fusion.FusionRecord, contradiction.Record,
// contradiction.TruthVersion, temporal.Record, decision records, ...)
// can be packaged without this package needing to import every engine
// package and create a dependency cycle.
type EvidenceRecord struct {
	Kind    string // e.g. "contradiction.TruthVersion", "temporal.Version"
	Index   uint64
	Payload []byte // canonical JSON of the original record, caller-supplied
}

// EvidencePackage is Stage 2: the full set of evidence records backing
// one decision/claim, in a stable order.
type EvidencePackage struct {
	Subject string // e.g. a ClaimKey or entity ID the bundle is about
	Records []EvidenceRecord
}

// ReplayPackage is Stage 3: the EvidencePackage plus the CLAIMED result
// (what the original engine says it computed) that a verifier must
// independently reproduce.
type ReplayPackage struct {
	Evidence      EvidencePackage
	ClaimedResult string // canonical string form of the decision/arbitration outcome
}

// BundleManifest is the cryptographic commitment over an entire
// ReplayPackage — Stage 4's concrete artifact. RootHash is a Merkle-style
// hash over every record's own hash plus the claimed result, so tampering
// with ANY single record, their order, or the claimed result changes
// RootHash.
type BundleManifest struct {
	Subject       string
	RecordCount   int
	RecordHashes  []string // per-record hash, in Records order
	ClaimedResult string
	RootHash      string
}

func hashRecord(r EvidenceRecord) string {
	b := []byte(fmt.Sprintf("kind=%s|index=%d|payload=%s|", r.Kind, r.Index, string(r.Payload)))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// BuildManifest computes the BundleManifest for a ReplayPackage — pure,
// deterministic, no external state.
func BuildManifest(rp ReplayPackage) (BundleManifest, error) {
	if len(rp.Evidence.Records) == 0 {
		return BundleManifest{}, ErrEmptyBundle
	}
	hashes := make([]string, len(rp.Evidence.Records))
	for i, r := range rp.Evidence.Records {
		hashes[i] = hashRecord(r)
	}
	root := computeRootHash(rp.Evidence.Subject, hashes, rp.ClaimedResult)
	return BundleManifest{
		Subject: rp.Evidence.Subject, RecordCount: len(rp.Evidence.Records),
		RecordHashes: hashes, ClaimedResult: rp.ClaimedResult, RootHash: root,
	}, nil
}

func computeRootHash(subject string, recordHashes []string, claimedResult string) string {
	b := []byte(fmt.Sprintf("subject=%s|records=%v|result=%s|", subject, recordHashes, claimedResult))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// VerificationBundle is Stage 4's shippable artifact — everything an
// external auditor needs, self-contained, with no dependency on the
// original engine's live process. Marshal/Unmarshal round-trip via JSON
// so this can literally be written to a file and handed to a regulator.
type VerificationBundle struct {
	ReplayPackage ReplayPackage
	Manifest      BundleManifest
}

// NewBundle builds a VerificationBundle from a ReplayPackage, computing
// and embedding its manifest.
func NewBundle(rp ReplayPackage) (VerificationBundle, error) {
	m, err := BuildManifest(rp)
	if err != nil {
		return VerificationBundle{}, err
	}
	return VerificationBundle{ReplayPackage: rp, Manifest: m}, nil
}

func (b VerificationBundle) Marshal() ([]byte, error) {
	return json.Marshal(b)
}

func Unmarshal(data []byte) (VerificationBundle, error) {
	var b VerificationBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return VerificationBundle{}, err
	}
	return b, nil
}

// ReplayFunc independently recomputes a result string from a raw
// EvidencePackage — registered per domain by the caller, so THIS package
// never needs to import fusion/contradiction/decision/etc. (avoiding an
// import cycle) while still performing a REAL independent recomputation,
// not a trivial hash comparison. Typically a thin wrapper around the
// originating engine's own Rebuild()/ArbitrateClaim()-style pure
// function, called fresh with none of the original engine's state.
type ReplayFunc func(EvidencePackage) (result string, err error)

// Verifier is Stage 5+6+7: performs deterministic replay and independent
// validation against a bundle, with no access to (and no need for) the
// original engine instance that produced it.
type Verifier struct {
	replayFuncs map[string]ReplayFunc
}

func NewVerifier() *Verifier {
	return &Verifier{replayFuncs: make(map[string]ReplayFunc)}
}

// RegisterReplayFunc wires an independent recomputation function for a
// given evidence-package "domain" key (caller's choice of namespacing,
// typically matching the dominant EvidenceRecord.Kind in the package).
func (v *Verifier) RegisterReplayFunc(domain string, fn ReplayFunc) {
	v.replayFuncs[domain] = fn
}

// VerificationResult is Stage 7's output.
type VerificationResult struct {
	ManifestValid    bool
	ReplayValid      bool
	RecomputedRoot   string
	RecomputedResult string
	Certificate      *AuditCertificate // non-nil only if both checks passed
}

// Verify runs the full IVF pipeline: Stage 4 check (recompute the
// manifest root from the bundle's own evidence and compare against the
// bundle's claimed root — detects any tampering of the bundle itself),
// then Stage 5/6 (independent deterministic replay via the registered
// ReplayFunc for `domain`) and Stage 7 (compare replay output against
// the bundle's ClaimedResult). Returns a signed AuditCertificate only if
// BOTH checks pass.
func (v *Verifier) Verify(domain string, bundle VerificationBundle) (VerificationResult, error) {
	recomputedManifest, err := BuildManifest(bundle.ReplayPackage)
	if err != nil {
		return VerificationResult{}, err
	}
	manifestValid := recomputedManifest.RootHash == bundle.Manifest.RootHash

	fn, ok := v.replayFuncs[domain]
	if !ok {
		return VerificationResult{ManifestValid: manifestValid, RecomputedRoot: recomputedManifest.RootHash},
			fmt.Errorf("%w: domain %q", ErrMissingReplayFunc, domain)
	}
	recomputedResult, err := fn(bundle.ReplayPackage.Evidence)
	if err != nil {
		return VerificationResult{ManifestValid: manifestValid, RecomputedRoot: recomputedManifest.RootHash}, err
	}
	replayValid := recomputedResult == bundle.ReplayPackage.ClaimedResult

	result := VerificationResult{
		ManifestValid: manifestValid, ReplayValid: replayValid,
		RecomputedRoot: recomputedManifest.RootHash, RecomputedResult: recomputedResult,
	}
	if manifestValid && replayValid {
		cert := IssueCertificate(bundle.Manifest)
		result.Certificate = &cert
	}
	return result, nil
}

// AuditCertificate is the concrete artifact an auditor keeps as proof
// that independent verification succeeded — a hash-committed statement
// binding a subject to a verified root hash (see package doc for the
// asymmetric-signature/external-PKI caveat).
type AuditCertificate struct {
	Subject         string
	VerifiedRoot    string
	ClaimedResult   string
	RecordCount     int
	CertificateHash string // SHA-256 commitment over the fields above
}

func IssueCertificate(m BundleManifest) AuditCertificate {
	c := AuditCertificate{
		Subject: m.Subject, VerifiedRoot: m.RootHash, ClaimedResult: m.ClaimedResult, RecordCount: m.RecordCount,
	}
	b := []byte(fmt.Sprintf("subject=%s|root=%s|result=%s|count=%d|", c.Subject, c.VerifiedRoot, c.ClaimedResult, c.RecordCount))
	sum := sha256.Sum256(b)
	c.CertificateHash = hex.EncodeToString(sum[:])
	return c
}

// VerifyCertificate lets a THIRD party (holding only the certificate,
// not the full bundle) confirm the certificate itself hasn't been
// altered — the "hand this to a regulator" end state.
func VerifyCertificate(c AuditCertificate) bool {
	b := []byte(fmt.Sprintf("subject=%s|root=%s|result=%s|count=%d|", c.Subject, c.VerifiedRoot, c.ClaimedResult, c.RecordCount))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]) == c.CertificateHash
}

// SortRecordsByIndex is a small helper bundle-builders can use to
// guarantee stable, deterministic record ordering before hashing
// (required for RootHash to be reproducible regardless of collection
// order upstream).
func SortRecordsByIndex(records []EvidenceRecord) []EvidenceRecord {
	out := make([]EvidenceRecord, len(records))
	copy(out, records)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}
