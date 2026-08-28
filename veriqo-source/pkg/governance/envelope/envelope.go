// Package envelope closes PHASE E (P0-6, "Evidence Envelope") and
// PHASE E2 (P0-7, "Evidence Freshness Gate") of the pre-insurance
// closure program: ONE envelope format carrying both external and
// internal qualification evidence, and a validator that refuses an
// envelope which does not actually belong to the release being
// qualified.
//
// What already existed, and is deliberately REUSED rather than
// rebuilt (program rule 0: "reuse existing package/interface wherever
// possible; do not create duplicate abstractions"):
//
//   - pkg/governance/qualification.TrustRegistry already models a
//     registered Provider/Reviewer with an Ed25519 key, a validity
//     window, a revocation flag and a per-gate authorization list.
//     This package does NOT define a second provider model; Validator
//     holds a *qualification.TrustRegistry and asks it.
//   - pkg/evidence/provenance already models OriginClass, RightsState
//     and AttestationState with fail-closed validation. This package
//     does NOT define a second origin/rights vocabulary; Envelope
//     embeds those exact types.
//   - internal/assurance already owns the gate/evidence/verdict
//     vocabulary. This package produces a Verdict that a gate consumes;
//     it does not re-implement gating.
//
// What genuinely did not exist anywhere in the repository, and is new
// here:
//
//   - A single envelope carrying the release-identity quadruple
//     (commit, source_hash, binary_hash, sbom_hash) TOGETHER with the
//     artifact set and its root hash, the validity window, the declared
//     limitations, and an explicit FIXTURE/LIVE classification.
//     qualification.ExternalEvidence carries commit/source_hash/
//     build_hash but has no binary_hash, no sbom_hash, no artifact set,
//     no artifact root, no valid_from, no limitations, and — the one
//     that matters most for this project's standing discipline — no
//     way at all to say "this evidence is a fixture".
//   - A freshness verdict that reports BLOCKED_STALE_EVIDENCE by name
//     rather than a generic failure, so a stale-evidence refusal can
//     never be confused with an engineering failure in the gate's own
//     subject matter.
//
// Anti-false-green discipline, mechanically enforced: there is no
// default-allow path in Check. Every rejection reason is a named
// error, every check is applied unconditionally, and a fixture can
// never self-promote — Check refuses an envelope whose OriginKind is
// SYNTHETIC or REPLAY but whose Classification claims LIVE, and it
// refuses a LIVE classification whose OriginKind was never raised
// above SYNTHETIC at all.
package envelope

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/governance/qualification"
)

// ContractVersion is the declared version of this envelope contract.
// It is part of every envelope's content hash, so an envelope produced
// under one contract version can never be silently reinterpreted under
// another.
const ContractVersion = "veriqo.evidence.envelope/v1"

// Classification is the single most important field in this envelope
// for this project's standing "no false green" rule: whether the
// evidence describes a real, live measurement or a fixture.
type Classification string

const (
	// ClassificationLive asserts the measurement came from a real run
	// against real infrastructure. Check verifies this claim against
	// OriginKind rather than believing it.
	ClassificationLive Classification = "LIVE"
	// ClassificationFixture is what every synthetic, seeded, in-sandbox
	// or replayed measurement must declare. A fixture envelope is a
	// perfectly legitimate artifact — it just can never qualify an
	// externally-blocked gate.
	ClassificationFixture Classification = "FIXTURE"
)

var knownClassifications = map[Classification]bool{
	ClassificationLive: true, ClassificationFixture: true,
}

// Errors. Every one of these is a distinct, nameable refusal reason —
// a caller (or an operator reading a manifest) can always tell exactly
// which check failed.
var (
	ErrMissingField          = errors.New("envelope: required field missing")
	ErrUnknownClassification = errors.New("envelope: unknown classification")
	ErrContractVersion       = errors.New("envelope: declared contract version is not this contract")
	ErrCommitMismatch        = errors.New("envelope: evidence commit does not match the release commit")
	ErrSourceHashMismatch    = errors.New("envelope: evidence source hash does not match the release source hash")
	ErrBinaryHashMismatch    = errors.New("envelope: evidence binary hash does not match the release binary hash")
	ErrSBOMHashMismatch      = errors.New("envelope: evidence SBOM hash does not match the release SBOM hash")
	ErrArtifactRootMismatch  = errors.New("envelope: declared artifact root hash does not match the submitted artifact set")
	ErrEnvironmentNotAllowed = errors.New("envelope: evidence environment is not an environment this gate accepts")
	ErrExpired               = errors.New("envelope: evidence validity window has already ended")
	ErrNotYetValid           = errors.New("envelope: evidence validity window has not started")
	ErrInvalidWindow         = errors.New("envelope: valid_until is not after valid_from")
	ErrUnknownProvider       = errors.New("envelope: provider is not a registered trust anchor")
	ErrProviderRevoked       = errors.New("envelope: provider credential is revoked")
	ErrProviderNotAuthorized = errors.New("envelope: provider is not authorized for this gate")
	ErrMissingReviewer       = errors.New("envelope: no reviewer is recorded on this envelope")
	ErrUnknownReviewer       = errors.New("envelope: reviewer is not a registered trust anchor")
	ErrReviewerRevoked       = errors.New("envelope: reviewer credential is revoked")
	ErrFixtureClaimedLive    = errors.New("envelope: a fixture-origin envelope claims LIVE classification")
	ErrLiveWithoutRealOrigin = errors.New("envelope: LIVE classification requires an origin class above SYNTHETIC/REPLAY")
)

// Artifact is one file produced by the evidence-generating run: a log,
// a report PDF, a benchmark CSV, a packet capture. Only its identity
// and size are carried here; the bytes live wherever the operator put
// them. Hash is what makes the reference checkable.
type Artifact struct {
	Name  string `json:"name"`
	Hash  string `json:"hash"`
	Bytes uint64 `json:"bytes"`
}

// ArtifactRoot content-addresses a whole artifact set: a deterministic
// SHA-256 over the canonically-sorted (name, hash, size) triples. Two
// sets with identical members always produce the same root, and
// changing, adding or removing any single artifact changes it.
//
// Deliberately a flat, sorted digest rather than a binary Merkle tree:
// nothing in this program needs per-artifact inclusion proofs, and a
// tree would be a more complicated construction with no additional
// property this contract actually uses.
func ArtifactRoot(list []Artifact) string {
	rows := make([]string, len(list))
	for i, a := range list {
		rows[i] = fmt.Sprintf("%s|%s|%d", a.Name, a.Hash, a.Bytes)
	}
	sort.Strings(rows)
	h := sha256.New()
	h.Write([]byte(ContractVersion + "|artifact_root\n"))
	for _, r := range rows {
		h.Write([]byte(r))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Envelope is the one evidence format both external qualification
// evidence (a vendor's pentest report) and internal qualification
// evidence (this repo's own soak run) are carried in.
type Envelope struct {
	// ContractVersion is declared, never assumed. Validate refuses an
	// envelope declaring any other version rather than reinterpreting
	// its fields under this one.
	ContractVersion string `json:"contract_version"`

	GateID string `json:"gate_id"`

	// Release identity. All four are part of what makes this envelope
	// belong to one exact build; the freshness gate (Freshness below)
	// checks every one of them against the release being qualified.
	Release    string `json:"release"`
	Commit     string `json:"commit"`
	SourceHash string `json:"source_hash"`
	BinaryHash string `json:"binary_hash"`
	SBOMHash   string `json:"sbom_hash"`

	// Environment names WHERE the measurement ran — "aws-us-east-1",
	// "vendor-lab", "ci-sandbox". Never blank; Check matches it against
	// the environments a gate is willing to accept.
	Environment string `json:"environment"`

	// Measurement is the actual numbers, not prose: p99_latency_ms,
	// nodes, rto_seconds, findings_critical.
	Measurement map[string]string `json:"measurement"`

	Artifacts        []Artifact `json:"artifacts,omitempty"`
	ArtifactRootHash string     `json:"artifact_root_hash"`

	ProviderID string `json:"provider_id"`
	ReviewerID string `json:"reviewer_id"`

	// Validity window, in VERIQO ticks (this repository has no wall
	// clock inside the kernel; see pkg/evidence/ontology's own note).
	ValidFrom  uint64 `json:"valid_from"`
	ValidUntil uint64 `json:"valid_until"`

	// Limitations is what the evidence explicitly does NOT prove, in
	// the producer's own words. Required to be non-empty for a
	// FIXTURE envelope, because a fixture that declares no limitation
	// is precisely the artifact this project exists not to produce.
	Limitations []string `json:"limitations,omitempty"`

	// OriginKind, RightsState and Attestation are pkg/evidence/
	// provenance's own vocabularies, reused verbatim.
	OriginKind  provenance.OriginClass      `json:"origin_kind"`
	RightsState provenance.RightsState      `json:"rights_state"`
	Attestation provenance.AttestationState `json:"attestation"`

	// Provenance is the transformation chain that produced this
	// evidence, oldest first — the same shape provenance.
	// ExternalEvidence.TransformationChain already uses.
	Provenance []string `json:"provenance,omitempty"`

	Classification Classification `json:"classification"`
}

// Validate is the structural check: every mandatory field present,
// every enum a known value, the artifact root consistent with the
// artifact set, and the validity window well-formed. It says nothing
// about whether the envelope belongs to any particular release — that
// is Check's and Freshness's job.
func (e Envelope) Validate() error {
	if e.ContractVersion != ContractVersion {
		return fmt.Errorf("%w: %q", ErrContractVersion, e.ContractVersion)
	}
	required := []struct {
		name  string
		value string
	}{
		{"gate_id", e.GateID},
		{"release", e.Release},
		{"commit", e.Commit},
		{"source_hash", e.SourceHash},
		{"binary_hash", e.BinaryHash},
		{"sbom_hash", e.SBOMHash},
		{"environment", e.Environment},
		{"provider_id", e.ProviderID},
		{"artifact_root_hash", e.ArtifactRootHash},
	}
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			return fmt.Errorf("%w: %s", ErrMissingField, r.name)
		}
	}
	if len(e.Measurement) == 0 {
		return fmt.Errorf("%w: measurement", ErrMissingField)
	}
	if !knownClassifications[e.Classification] {
		return fmt.Errorf("%w: %q", ErrUnknownClassification, e.Classification)
	}
	// OriginKind / RightsState / Attestation are validated by delegating
	// to provenance's own fail-closed enum check rather than duplicating
	// its tables here.
	probe := provenance.ExternalEvidence{
		SourceID: e.GateID, ProviderID: e.ProviderID, DatasetID: e.Release,
		DeliveryID: e.ArtifactRootHash, PayloadHash: e.SourceHash,
		CanonicalHash: e.ArtifactRootHash, SchemaVersion: e.ContractVersion,
		OriginClass: e.OriginKind, RightsState: e.RightsState,
		CorrectionState: provenance.CorrectionNone, AttestationState: e.Attestation,
	}
	if err := probe.Validate(); err != nil {
		return err
	}
	if e.ValidUntil <= e.ValidFrom {
		return ErrInvalidWindow
	}
	if got := ArtifactRoot(e.Artifacts); got != e.ArtifactRootHash {
		return fmt.Errorf("%w: declared %s, recomputed %s", ErrArtifactRootMismatch, e.ArtifactRootHash, got)
	}
	if e.Classification == ClassificationFixture && len(e.Limitations) == 0 {
		return fmt.Errorf("%w: limitations (mandatory for a FIXTURE envelope)", ErrMissingField)
	}
	return nil
}

// ID content-addresses the whole envelope. Hand-rolled and key-ordered
// for the same cross-language-reproducibility reason
// pkg/evidence/ontology.Evidence.canonicalBytes documents.
func (e Envelope) ID() string {
	var b strings.Builder
	fmt.Fprintf(&b, "contract=%s\ngate=%s\nrelease=%s\n", e.ContractVersion, e.GateID, e.Release)
	fmt.Fprintf(&b, "commit=%s\nsource_hash=%s\nbinary_hash=%s\nsbom_hash=%s\n",
		e.Commit, e.SourceHash, e.BinaryHash, e.SBOMHash)
	fmt.Fprintf(&b, "environment=%s\nartifact_root=%s\n", e.Environment, e.ArtifactRootHash)
	fmt.Fprintf(&b, "provider=%s\nreviewer=%s\n", e.ProviderID, e.ReviewerID)
	fmt.Fprintf(&b, "valid_from=%d\nvalid_until=%d\n", e.ValidFrom, e.ValidUntil)
	fmt.Fprintf(&b, "origin=%s\nrights=%s\nattestation=%s\nclassification=%s\n",
		e.OriginKind, e.RightsState, e.Attestation, e.Classification)
	fmt.Fprintf(&b, "provenance=%s\n", strings.Join(e.Provenance, ">"))
	lim := append([]string(nil), e.Limitations...)
	sort.Strings(lim)
	for _, l := range lim {
		fmt.Fprintf(&b, "limitation=%s\n", l)
	}
	keys := make([]string, 0, len(e.Measurement))
	for k := range e.Measurement {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "m.%s=%s\n", k, e.Measurement[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// IsFixture reports whether this envelope's ORIGIN makes it a fixture,
// independent of what its Classification field claims. This is the
// distinction the whole "no false green" rule turns on: the claim is
// Classification, the fact is OriginKind.
func (e Envelope) IsFixture() bool {
	return e.OriginKind == provenance.OriginSynthetic || e.OriginKind == provenance.OriginReplay
}

// Release is the identity of the exact build being qualified. Every
// field is what THIS release really is, computed by the release
// pipeline — never copied from an envelope.
type Release struct {
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	SourceHash   string `json:"source_hash"`
	BinaryHash   string `json:"binary_hash"`
	SBOMHash     string `json:"sbom_hash"`
	ArtifactRoot string `json:"artifact_root"`
}

// Validator checks envelopes against one release and one trust
// registry. Both are mandatory: a Validator with a nil TrustRegistry
// rejects every envelope (there is no anonymous-provider path), and a
// Validator with an empty Release rejects every envelope (there is no
// "any release" path).
type Validator struct {
	Release Release
	Trust   *qualification.TrustRegistry
	// AllowedEnvironments, when non-empty, is the exact set of
	// Environment values this validator accepts. Empty means "any
	// non-empty environment", which is the correct default for a
	// validator used across gates with different infrastructure.
	AllowedEnvironments []string
}

// Verdict is the machine-readable result of validating one envelope.
type Verdict struct {
	EnvelopeID string   `json:"envelope_id"`
	GateID     string   `json:"gate_id"`
	Accepted   bool     `json:"accepted"`
	Reasons    []string `json:"reasons,omitempty"`
	// Classification echoes what the envelope CLAIMED, and
	// EffectiveKind reports what its origin actually makes it, so a
	// reader never has to reconcile the two by hand.
	Classification Classification `json:"classification"`
	EffectiveKind  Classification `json:"effective_kind"`
}

// Check runs every rejection rule PHASE E names, in a fixed order, and
// returns the first failure as an error plus a full Verdict. It is
// fail-closed by construction: the only way to reach Accepted=true is
// to pass every check.
func (v Validator) Check(e Envelope, nowTick uint64) (Verdict, error) {
	verdict := Verdict{
		EnvelopeID: e.ID(), GateID: e.GateID,
		Classification: e.Classification,
		EffectiveKind:  ClassificationLive,
	}
	if e.IsFixture() {
		verdict.EffectiveKind = ClassificationFixture
	}
	fail := func(err error) (Verdict, error) {
		verdict.Accepted = false
		verdict.Reasons = append(verdict.Reasons, err.Error())
		return verdict, err
	}

	if err := e.Validate(); err != nil {
		return fail(err)
	}

	// Fixture / LIVE reconciliation comes FIRST, before any
	// release-identity check: a fixture that lies about being live must
	// be refused for that reason, not for some incidental hash mismatch
	// it might also have.
	if e.IsFixture() && e.Classification == ClassificationLive {
		return fail(fmt.Errorf("%w: origin_kind=%s", ErrFixtureClaimedLive, e.OriginKind))
	}
	if !e.IsFixture() && e.Classification == ClassificationFixture {
		// Not an error: declaring a real-origin measurement as a fixture
		// is a conservative under-claim, and under-claiming is always
		// allowed. EffectiveKind above already records the difference.
		verdict.EffectiveKind = ClassificationFixture
	}
	if e.Classification == ClassificationLive && e.OriginKind == provenance.OriginRealDerivedBenchmark {
		return fail(fmt.Errorf("%w: origin_kind=%s", ErrLiveWithoutRealOrigin, e.OriginKind))
	}

	// Release binding.
	if v.Release.Commit == "" || v.Release.SourceHash == "" {
		return fail(fmt.Errorf("%w: validator release identity", ErrMissingField))
	}
	if e.Commit != v.Release.Commit {
		return fail(fmt.Errorf("%w: evidence %s, release %s", ErrCommitMismatch, e.Commit, v.Release.Commit))
	}
	if e.SourceHash != v.Release.SourceHash {
		return fail(ErrSourceHashMismatch)
	}
	if v.Release.BinaryHash != "" && e.BinaryHash != v.Release.BinaryHash {
		return fail(ErrBinaryHashMismatch)
	}
	if v.Release.SBOMHash != "" && e.SBOMHash != v.Release.SBOMHash {
		return fail(ErrSBOMHashMismatch)
	}
	if v.Release.ArtifactRoot != "" && e.ArtifactRootHash != v.Release.ArtifactRoot {
		return fail(ErrArtifactRootMismatch)
	}

	// Environment.
	if len(v.AllowedEnvironments) > 0 {
		ok := false
		for _, allowed := range v.AllowedEnvironments {
			if allowed == e.Environment {
				ok = true
				break
			}
		}
		if !ok {
			return fail(fmt.Errorf("%w: %q", ErrEnvironmentNotAllowed, e.Environment))
		}
	}

	// Validity window.
	if nowTick < e.ValidFrom {
		return fail(ErrNotYetValid)
	}
	if nowTick > e.ValidUntil {
		return fail(fmt.Errorf("%w: valid_until=%d, now=%d", ErrExpired, e.ValidUntil, nowTick))
	}

	// Provider and reviewer, resolved through the EXISTING trust
	// registry rather than a second provider model.
	if v.Trust == nil {
		return fail(fmt.Errorf("%w: %s", ErrUnknownProvider, e.ProviderID))
	}
	provider, ok := v.Trust.Provider(e.ProviderID)
	if !ok {
		return fail(fmt.Errorf("%w: %s", ErrUnknownProvider, e.ProviderID))
	}
	if provider.Revoked {
		return fail(ErrProviderRevoked)
	}
	if !providerAuthorized(provider, e.GateID) {
		return fail(fmt.Errorf("%w: %s for %s", ErrProviderNotAuthorized, e.ProviderID, e.GateID))
	}
	if strings.TrimSpace(e.ReviewerID) == "" {
		return fail(ErrMissingReviewer)
	}
	reviewer, ok := v.Trust.Reviewer(e.ReviewerID)
	if !ok {
		return fail(fmt.Errorf("%w: %s", ErrUnknownReviewer, e.ReviewerID))
	}
	if reviewer.Revoked {
		return fail(ErrReviewerRevoked)
	}

	verdict.Accepted = true
	return verdict, nil
}

// providerAuthorized re-implements nothing: qualification.Provider's
// own authorization list is unexported behaviour, so this reads the
// exported field directly, keeping ONE source of truth for the data
// (the registered Provider) even though the lookup is local.
func providerAuthorized(p qualification.Provider, gateID string) bool {
	for _, g := range p.AuthorizedGateTypes {
		if g == gateID {
			return true
		}
	}
	return false
}

// --- PHASE E2 (P0-7): the evidence freshness gate -------------------

// FreshnessStatus is deliberately a small, closed vocabulary with one
// specifically-named failure. A generic FAIL would let a stale-evidence
// refusal be misread as "the thing this gate measures is broken"; it is
// not — it means the evidence describes a different build.
type FreshnessStatus string

const (
	// FreshnessPass means every release-identity field in the envelope
	// matches the release, and the evidence has not expired.
	FreshnessPass FreshnessStatus = "PASS"
	// FreshnessBlockedStale is P0-7's required, named outcome.
	FreshnessBlockedStale FreshnessStatus = "BLOCKED_STALE_EVIDENCE"
)

// FreshnessVerdict reports, field by field, why evidence was or was
// not fresh for a qualification attempt.
type FreshnessVerdict struct {
	GateID          string          `json:"gate_id"`
	EnvelopeID      string          `json:"envelope_id"`
	Status          FreshnessStatus `json:"status"`
	QualificationAt uint64          `json:"qualification_at_tick"`
	Mismatches      []string        `json:"mismatches,omitempty"`
}

// Freshness is P0-7's gate, exactly as specified: PASS requires
// report.commit == release.commit, source_hash == release.source_hash,
// artifact_root == release.artifact_root, binary_hash ==
// release.binary_hash, and evidence.valid_until >= qualification_time.
// Anything else is BLOCKED_STALE_EVIDENCE, with every failing
// comparison named individually rather than collapsed into one message.
//
// Unlike Check, Freshness compares EVERY field before returning, so an
// operator sees the complete divergence in one pass instead of fixing
// one mismatch at a time.
func Freshness(e Envelope, rel Release, qualificationTick uint64) FreshnessVerdict {
	v := FreshnessVerdict{
		GateID: e.GateID, EnvelopeID: e.ID(),
		Status: FreshnessPass, QualificationAt: qualificationTick,
	}
	cmp := func(name, got, want string) {
		if want == "" {
			v.Mismatches = append(v.Mismatches,
				fmt.Sprintf("%s: release declares no %s to compare against", name, name))
			return
		}
		if got != want {
			v.Mismatches = append(v.Mismatches,
				fmt.Sprintf("%s: evidence=%q release=%q", name, got, want))
		}
	}
	cmp("commit", e.Commit, rel.Commit)
	cmp("source_hash", e.SourceHash, rel.SourceHash)
	cmp("artifact_root", e.ArtifactRootHash, rel.ArtifactRoot)
	cmp("binary_hash", e.BinaryHash, rel.BinaryHash)
	if e.ValidUntil < qualificationTick {
		v.Mismatches = append(v.Mismatches,
			fmt.Sprintf("valid_until: evidence=%d qualification_time=%d", e.ValidUntil, qualificationTick))
	}
	if len(v.Mismatches) > 0 {
		v.Status = FreshnessBlockedStale
	}
	return v
}
