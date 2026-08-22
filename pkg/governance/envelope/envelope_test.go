package envelope

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"

	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/governance/qualification"
)

const (
	testCommit     = "8a085c7deadbeefcafe0123456789abcdef01234"
	testSourceHash = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testBinaryHash = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testSBOMHash   = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

func testArtifacts() []Artifact {
	return []Artifact{
		{Name: "scale-run.log", Hash: "sha256:aaaa", Bytes: 4096},
		{Name: "latency.csv", Hash: "sha256:bbbb", Bytes: 128},
	}
}

// liveEnvelope is a well-formed envelope claiming a real, external
// measurement. Tests mutate one field at a time from this baseline so
// each failure is attributable to exactly one cause.
func liveEnvelope() Envelope {
	arts := testArtifacts()
	return Envelope{
		ContractVersion:  ContractVersion,
		GateID:           "scale_qualification",
		Release:          "v7.12.0",
		Commit:           testCommit,
		SourceHash:       testSourceHash,
		BinaryHash:       testBinaryHash,
		SBOMHash:         testSBOMHash,
		Environment:      "vendor-lab",
		Measurement:      map[string]string{"nodes": "100", "records": "1000000", "lost": "0"},
		Artifacts:        arts,
		ArtifactRootHash: ArtifactRoot(arts),
		ProviderID:       "prov-benchlab",
		ReviewerID:       "rev-eng-lead",
		ValidFrom:        100,
		ValidUntil:       900,
		OriginKind:       provenance.OriginRealExternalAuthorized,
		RightsState:      provenance.RightsInternalOnly,
		Attestation:      provenance.AttestationThirdPartyAttested,
		Provenance:       []string{"benchlab.harness/v3", "veriqo.normalize/v1"},
		Classification:   ClassificationLive,
	}
}

// fixtureEnvelope is what every in-sandbox measurement in this
// repository must honestly declare itself as.
func fixtureEnvelope() Envelope {
	e := liveEnvelope()
	e.OriginKind = provenance.OriginSynthetic
	e.Classification = ClassificationFixture
	e.Environment = "ci-sandbox"
	e.Limitations = []string{"seeded synthetic node provider; single physical host; not a real 100-node deployment"}
	return e
}

func testRelease() Release {
	return Release{
		Version: "v7.12.0", Commit: testCommit, SourceHash: testSourceHash,
		BinaryHash: testBinaryHash, SBOMHash: testSBOMHash,
		ArtifactRoot: ArtifactRoot(testArtifacts()),
	}
}

// testTrust registers one provider authorized for scale_qualification
// and one reviewer, using the EXISTING qualification.TrustRegistry --
// this package deliberately defines no second provider model.
func testTrust(t *testing.T) *qualification.TrustRegistry {
	t.Helper()
	tr := qualification.NewTrustRegistry()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate provider key: %v", err)
	}
	rpub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate reviewer key: %v", err)
	}
	if err := tr.RegisterProvider(qualification.Provider{
		ProviderID: "prov-benchlab", LegalName: "Bench Lab GmbH", ProviderType: "benchmark_lab",
		PublicKeyHex: hex.EncodeToString(pub), ValidFromTick: 0, ValidUntilTick: 1 << 40,
		AuthorizedGateTypes: []string{"scale_qualification"},
	}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	if err := tr.RegisterReviewer(qualification.Reviewer{
		ReviewerID: "rev-eng-lead", Name: "Engineering Lead", Role: "reviewer",
		PublicKeyHex: hex.EncodeToString(rpub), ValidFromTick: 0, ValidUntilTick: 1 << 40,
	}); err != nil {
		t.Fatalf("register reviewer: %v", err)
	}
	return tr
}

func testValidator(t *testing.T) Validator {
	t.Helper()
	return Validator{Release: testRelease(), Trust: testTrust(t)}
}

func TestWellFormedLiveEnvelopeIsAccepted(t *testing.T) {
	v := testValidator(t)
	verdict, err := v.Check(liveEnvelope(), 500)
	if err != nil {
		t.Fatalf("Check: %v (reasons %v)", err, verdict.Reasons)
	}
	if !verdict.Accepted {
		t.Fatalf("verdict not accepted: %v", verdict.Reasons)
	}
	if verdict.EffectiveKind != ClassificationLive {
		t.Fatalf("EffectiveKind = %q, want LIVE", verdict.EffectiveKind)
	}
}

// TestValidatorRejectsEveryNamedFailureMode is PHASE E's own acceptance
// list, one subtest per named rejection: wrong commit, wrong source
// hash, wrong artifact hash, wrong environment, expired evidence,
// revoked provider, unauthorized provider, missing reviewer, a fixture
// labeled LIVE, and stale evidence.
func TestValidatorRejectsEveryNamedFailureMode(t *testing.T) {
	base := testValidator(t)

	cases := []struct {
		name    string
		mutate  func(*Envelope)
		valid   func(*Validator)
		now     uint64
		wantErr error
	}{
		{
			name:    "wrong commit",
			mutate:  func(e *Envelope) { e.Commit = "0000000000000000000000000000000000000000" },
			now:     500,
			wantErr: ErrCommitMismatch,
		},
		{
			name:    "wrong source hash",
			mutate:  func(e *Envelope) { e.SourceHash = "sha256:deadbeef" },
			now:     500,
			wantErr: ErrSourceHashMismatch,
		},
		{
			name:    "wrong binary hash",
			mutate:  func(e *Envelope) { e.BinaryHash = "sha256:deadbeef" },
			now:     500,
			wantErr: ErrBinaryHashMismatch,
		},
		{
			name: "wrong artifact hash",
			mutate: func(e *Envelope) {
				e.Artifacts[0].Hash = "sha256:tampered"
				e.ArtifactRootHash = ArtifactRoot(e.Artifacts)
			},
			now:     500,
			wantErr: ErrArtifactRootMismatch,
		},
		{
			name:    "wrong environment",
			mutate:  func(e *Envelope) { e.Environment = "someone-laptop" },
			valid:   func(v *Validator) { v.AllowedEnvironments = []string{"vendor-lab", "aws-us-east-1"} },
			now:     500,
			wantErr: ErrEnvironmentNotAllowed,
		},
		{
			name:    "expired evidence",
			mutate:  func(e *Envelope) {},
			now:     1000, // past ValidUntil=900
			wantErr: ErrExpired,
		},
		{
			name:    "not yet valid",
			mutate:  func(e *Envelope) {},
			now:     10, // before ValidFrom=100
			wantErr: ErrNotYetValid,
		},
		{
			name:   "revoked provider",
			mutate: func(e *Envelope) {},
			valid: func(v *Validator) {
				if err := v.Trust.RevokeProvider("prov-benchlab"); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			},
			now:     500,
			wantErr: ErrProviderRevoked,
		},
		{
			name:    "unauthorized provider",
			mutate:  func(e *Envelope) { e.GateID = "pentest" },
			now:     500,
			wantErr: ErrProviderNotAuthorized,
		},
		{
			name:    "unknown provider",
			mutate:  func(e *Envelope) { e.ProviderID = "prov-nobody" },
			now:     500,
			wantErr: ErrUnknownProvider,
		},
		{
			name:    "missing reviewer",
			mutate:  func(e *Envelope) { e.ReviewerID = "" },
			now:     500,
			wantErr: ErrMissingReviewer,
		},
		{
			name:    "unknown reviewer",
			mutate:  func(e *Envelope) { e.ReviewerID = "rev-ghost" },
			now:     500,
			wantErr: ErrUnknownReviewer,
		},
		{
			name: "fixture labeled LIVE",
			mutate: func(e *Envelope) {
				e.OriginKind = provenance.OriginSynthetic
				e.Classification = ClassificationLive
			},
			now:     500,
			wantErr: ErrFixtureClaimedLive,
		},
		{
			name: "replay origin labeled LIVE",
			mutate: func(e *Envelope) {
				e.OriginKind = provenance.OriginReplay
				e.Classification = ClassificationLive
			},
			now:     500,
			wantErr: ErrFixtureClaimedLive,
		},
		{
			name: "derived benchmark labeled LIVE",
			mutate: func(e *Envelope) {
				e.OriginKind = provenance.OriginRealDerivedBenchmark
			},
			now:     500,
			wantErr: ErrLiveWithoutRealOrigin,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := liveEnvelope()
			tc.mutate(&e)
			v := Validator{Release: base.Release, Trust: testTrust(t)}
			if tc.valid != nil {
				tc.valid(&v)
			}
			verdict, err := v.Check(e, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Check err = %v, want %v", err, tc.wantErr)
			}
			if verdict.Accepted {
				t.Fatal("verdict accepted despite a named rejection reason")
			}
			if len(verdict.Reasons) == 0 {
				t.Fatal("rejected verdict carries no reason -- a refusal must always be nameable")
			}
		})
	}
}

// TestFixtureCanNeverSelfPromote is the hard-gate item "a fixture can
// self-promote to VERIFIED = true" stated as a test: no mutation of
// Classification alone changes what OriginKind makes the envelope.
func TestFixtureCanNeverSelfPromote(t *testing.T) {
	v := testValidator(t)
	e := fixtureEnvelope()

	verdict, err := v.Check(e, 500)
	if err != nil {
		t.Fatalf("an honest FIXTURE envelope must still validate: %v", err)
	}
	if verdict.EffectiveKind != ClassificationFixture {
		t.Fatalf("EffectiveKind = %q, want FIXTURE", verdict.EffectiveKind)
	}

	// The only thing a fixture-producing caller can edit is the claim.
	e.Classification = ClassificationLive
	if _, err := v.Check(e, 500); !errors.Is(err, ErrFixtureClaimedLive) {
		t.Fatalf("editing Classification promoted a fixture: err = %v", err)
	}
}

func TestFixtureEnvelopeMustDeclareLimitations(t *testing.T) {
	e := fixtureEnvelope()
	e.Limitations = nil
	if err := e.Validate(); !errors.Is(err, ErrMissingField) {
		t.Fatalf("a FIXTURE envelope with no declared limitation was accepted: %v", err)
	}
}

func TestArtifactRootIsOrderIndependentAndTamperEvident(t *testing.T) {
	a := testArtifacts()
	reversed := []Artifact{a[1], a[0]}
	if ArtifactRoot(a) != ArtifactRoot(reversed) {
		t.Fatal("artifact root depends on submission order")
	}
	tampered := []Artifact{{Name: a[0].Name, Hash: a[0].Hash, Bytes: a[0].Bytes + 1}, a[1]}
	if ArtifactRoot(a) == ArtifactRoot(tampered) {
		t.Fatal("artifact root did not change when an artifact's size changed")
	}
	if ArtifactRoot(nil) == ArtifactRoot(a) {
		t.Fatal("an empty artifact set hashes identically to a populated one")
	}
}

func TestEnvelopeIDIsContentAddressedAndDetectsEveryFieldChange(t *testing.T) {
	base := liveEnvelope()
	id := base.ID()
	if id != liveEnvelope().ID() {
		t.Fatal("envelope ID is not deterministic")
	}
	mutations := map[string]func(*Envelope){
		"gate":           func(e *Envelope) { e.GateID = "pentest" },
		"release":        func(e *Envelope) { e.Release = "v9" },
		"commit":         func(e *Envelope) { e.Commit = "x" },
		"source_hash":    func(e *Envelope) { e.SourceHash = "x" },
		"binary_hash":    func(e *Envelope) { e.BinaryHash = "x" },
		"sbom_hash":      func(e *Envelope) { e.SBOMHash = "x" },
		"environment":    func(e *Envelope) { e.Environment = "x" },
		"measurement":    func(e *Envelope) { e.Measurement["nodes"] = "99" },
		"artifact_root":  func(e *Envelope) { e.ArtifactRootHash = "x" },
		"provider":       func(e *Envelope) { e.ProviderID = "x" },
		"reviewer":       func(e *Envelope) { e.ReviewerID = "x" },
		"valid_from":     func(e *Envelope) { e.ValidFrom = 101 },
		"valid_until":    func(e *Envelope) { e.ValidUntil = 901 },
		"limitations":    func(e *Envelope) { e.Limitations = []string{"one"} },
		"origin":         func(e *Envelope) { e.OriginKind = provenance.OriginSynthetic },
		"rights":         func(e *Envelope) { e.RightsState = provenance.RightsRevoked },
		"attestation":    func(e *Envelope) { e.Attestation = provenance.AttestationUnattested },
		"provenance":     func(e *Envelope) { e.Provenance = []string{"other"} },
		"classification": func(e *Envelope) { e.Classification = ClassificationFixture },
	}
	for name, mutate := range mutations {
		e := liveEnvelope()
		mutate(&e)
		if e.ID() == id {
			t.Errorf("changing %s did not change the envelope ID", name)
		}
	}
}

func TestValidateRefusesAForeignContractVersion(t *testing.T) {
	e := liveEnvelope()
	e.ContractVersion = "veriqo.evidence.envelope/v2"
	if err := e.Validate(); !errors.Is(err, ErrContractVersion) {
		t.Fatalf("Validate accepted a foreign contract version: %v", err)
	}
}

func TestValidateRefusesUnknownEnums(t *testing.T) {
	for name, mutate := range map[string]func(*Envelope){
		"classification": func(e *Envelope) { e.Classification = "PROBABLY_REAL" },
		"origin":         func(e *Envelope) { e.OriginKind = "VERY_REAL" },
		"rights":         func(e *Envelope) { e.RightsState = "SURE_WHY_NOT" },
		"attestation":    func(e *Envelope) { e.Attestation = "TRUST_ME" },
	} {
		e := liveEnvelope()
		mutate(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("Validate accepted an unknown %s value", name)
		}
	}
}

func TestValidatorWithoutTrustRegistryRejectsEverything(t *testing.T) {
	v := Validator{Release: testRelease()}
	if _, err := v.Check(liveEnvelope(), 500); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("a validator with no trust registry accepted an envelope: %v", err)
	}
}

func TestValidatorWithoutReleaseIdentityRejectsEverything(t *testing.T) {
	v := Validator{Trust: testTrust(t)}
	if _, err := v.Check(liveEnvelope(), 500); !errors.Is(err, ErrMissingField) {
		t.Fatalf("a validator with no release identity accepted an envelope: %v", err)
	}
}

// --- PHASE E2 (P0-7) freshness ---------------------------------------

func TestFreshnessPassesOnAnExactReleaseMatch(t *testing.T) {
	v := Freshness(liveEnvelope(), testRelease(), 500)
	if v.Status != FreshnessPass {
		t.Fatalf("Status = %q, mismatches %v", v.Status, v.Mismatches)
	}
	if len(v.Mismatches) != 0 {
		t.Fatalf("unexpected mismatches: %v", v.Mismatches)
	}
}

// TestFreshnessReportsBlockedStaleEvidenceByName is P0-7's exact
// requirement: a stale envelope must produce the named
// BLOCKED_STALE_EVIDENCE status, never a generic failure that could be
// misread as an engineering defect in the gate's own subject.
func TestFreshnessReportsBlockedStaleEvidenceByName(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Envelope)
		qualAt uint64
		field  string
	}{
		{"stale commit", func(e *Envelope) { e.Commit = "older0000" }, 500, "commit"},
		{"stale source hash", func(e *Envelope) { e.SourceHash = "sha256:old" }, 500, "source_hash"},
		{"stale artifact root", func(e *Envelope) { e.ArtifactRootHash = "sha256:old" }, 500, "artifact_root"},
		{"stale binary hash", func(e *Envelope) { e.BinaryHash = "sha256:old" }, 500, "binary_hash"},
		{"expired before qualification", func(e *Envelope) {}, 5000, "valid_until"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := liveEnvelope()
			tc.mutate(&e)
			v := Freshness(e, testRelease(), tc.qualAt)
			if v.Status != FreshnessBlockedStale {
				t.Fatalf("Status = %q, want %q", v.Status, FreshnessBlockedStale)
			}
			found := false
			for _, m := range v.Mismatches {
				if len(m) >= len(tc.field) && m[:len(tc.field)] == tc.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("mismatches %v do not name the %s divergence", v.Mismatches, tc.field)
			}
		})
	}
}

// TestFreshnessReportsEveryDivergenceAtOnce distinguishes Freshness
// from Check: an operator fixing a stale-evidence problem sees the full
// list, not one mismatch per run.
func TestFreshnessReportsEveryDivergenceAtOnce(t *testing.T) {
	e := liveEnvelope()
	e.Commit = "old"
	e.SourceHash = "old"
	e.BinaryHash = "old"
	e.ArtifactRootHash = "old"
	v := Freshness(e, testRelease(), 5000)
	if len(v.Mismatches) != 5 {
		t.Fatalf("got %d mismatches, want 5 (4 identity fields + expiry): %v", len(v.Mismatches), v.Mismatches)
	}
}

// TestFreshnessRefusesAReleaseThatDeclaresNothingToCompareAgainst is
// the fail-closed direction: a release with no declared binary hash
// must NOT silently make the binary_hash check pass.
func TestFreshnessRefusesAReleaseThatDeclaresNothingToCompareAgainst(t *testing.T) {
	rel := testRelease()
	rel.BinaryHash = ""
	v := Freshness(liveEnvelope(), rel, 500)
	if v.Status != FreshnessBlockedStale {
		t.Fatalf("a release with no binary hash silently passed freshness: %+v", v)
	}
}
