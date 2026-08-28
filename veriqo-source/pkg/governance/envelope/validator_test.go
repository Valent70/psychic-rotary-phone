package envelope

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"

	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/governance/qualification"
)

// signingTrust builds a trust registry AND returns the private keys, so
// a test can produce genuinely valid signatures. This is what makes the
// suite meaningful: every rejection below is a rejection of properly
// signed evidence for a specific, named reason, not a rejection of
// garbage.
func signingTrust(t *testing.T, gateID string) (*qualification.TrustRegistry, ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	tr := qualification.NewTrustRegistry()
	ppub, ppriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("provider keygen: %v", err)
	}
	rpub, rpriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("reviewer keygen: %v", err)
	}
	if err := tr.RegisterProvider(qualification.Provider{
		ProviderID: "prov-benchlab", LegalName: "Bench Lab GmbH", ProviderType: "benchmark_lab",
		PublicKeyHex: hex.EncodeToString(ppub), ValidFromTick: 0, ValidUntilTick: 1 << 40,
		AuthorizedGateTypes: []string{gateID},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	if err := tr.RegisterReviewer(qualification.Reviewer{
		ReviewerID: "rev-eng-lead", Name: "Engineering Lead", Role: "reviewer",
		PublicKeyHex: hex.EncodeToString(rpub), ValidFromTick: 0, ValidUntilTick: 1 << 40,
	}); err != nil {
		t.Fatalf("RegisterReviewer: %v", err)
	}
	return tr, ppriv, rpriv
}

// signedSubmission prepares a fully, genuinely signed submission for e.
func signedSubmission(t *testing.T, e Envelope, ppriv, rpriv ed25519.PrivateKey, nowTick uint64) (string, string) {
	t.Helper()
	// The artifact hash both signatures must cover is the one the
	// registry itself computes, so build the evidence with empty
	// signatures first and read the hash back.
	unsigned := e.ToQualificationEvidence("", "", nowTick)
	psig := ed25519.Sign(ppriv, []byte(unsigned.ArtifactHash))
	rsig := ed25519.Sign(rpriv, []byte(unsigned.ArtifactHash))
	return hex.EncodeToString(psig), hex.EncodeToString(rsig)
}

func registryFor(t *testing.T, tr *qualification.TrustRegistry, gateID string) *qualification.Registry {
	t.Helper()
	reg := qualification.NewRegistry(tr)
	if err := reg.RegisterBlocked(gateID, "100-node / 1M-envelope physical benchmark",
		"requires physical multi-host infrastructure at production scale"); err != nil {
		t.Fatalf("RegisterBlocked: %v", err)
	}
	return reg
}

// TestRealSignedLiveEvidencePassesEveryCheck is the positive control.
// Without it, a validator that rejected everything would pass every
// negative test below and be useless.
func TestRealSignedLiveEvidencePassesEveryCheck(t *testing.T) {
	e := liveEnvelope()
	tr, ppriv, rpriv := signingTrust(t, e.GateID)
	reg := registryFor(t, tr, e.GateID)
	v := Validator{Release: testRelease(), Trust: tr}
	psig, rsig := signedSubmission(t, e, ppriv, rpriv, 500)

	out, err := v.Submit(reg, e, psig, rsig, 500)
	if err != nil {
		t.Fatalf("Submit: %v (reasons %v)", err, out.Reasons)
	}
	if !out.Accepted {
		t.Fatalf("verdict not accepted: %v", out.Reasons)
	}
	if out.Freshness.Status != FreshnessPass {
		t.Errorf("freshness = %q", out.Freshness.Status)
	}
	if out.QualificationStatus != qualification.StatusEvidenceValidated {
		t.Fatalf("qualification status = %q, want EVIDENCE_VALIDATED", out.QualificationStatus)
	}
}

// TestSubmitNeverPromotesAGateOnItsOwn is this phase's most important
// assertion. The program says the validator "only makes their (still-
// external) evidence checkable" and must not promote any of the eight.
func TestSubmitNeverPromotesAGateOnItsOwn(t *testing.T) {
	e := liveEnvelope()
	tr, ppriv, rpriv := signingTrust(t, e.GateID)
	reg := registryFor(t, tr, e.GateID)
	v := Validator{Release: testRelease(), Trust: tr}
	psig, rsig := signedSubmission(t, e, ppriv, rpriv, 500)

	if _, err := v.Submit(reg, e, psig, rsig, 500); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	rec, ok := reg.Get(e.GateID)
	if !ok {
		t.Fatal("gate vanished from the registry")
	}
	// EVIDENCE_VALIDATED and no further. QUALIFIED and VERIFIED are
	// operator decisions with a named person behind them.
	if rec.Status == qualification.StatusQualified || rec.Status == qualification.StatusVerified {
		t.Fatalf("Submit advanced the gate to %s on its own -- promotion must be an explicit operator act", rec.Status)
	}
	if !rec.Satisfied(500) {
		t.Log("gate is not satisfied, which is correct: validated evidence is not a qualified gate")
	}
}

// TestValidatorRejectsEveryExternalEvidenceFailureMode walks the ten
// checks P1-16 enumerates. Every case starts from PROPERLY SIGNED
// evidence and breaks exactly one thing.
func TestValidatorRejectsEveryExternalEvidenceFailureMode(t *testing.T) {
	cases := []struct {
		name string
		// mutate changes the envelope BEFORE signing when preSign is
		// true (so the signature is valid over the mutated content), or
		// AFTER signing when false (so the signature no longer matches).
		mutate  func(*Envelope)
		preSign bool
		// trust mutates the trust registry after registration.
		trust func(*testing.T, *qualification.TrustRegistry)
		// tamper corrupts the signatures directly.
		tamper func(psig, rsig string) (string, string)
		now    uint64
	}{
		{name: "bad provider signature", preSign: true, mutate: func(*Envelope) {},
			tamper: func(_, rsig string) (string, string) { return hex.EncodeToString(make([]byte, 64)), rsig }, now: 500},
		{name: "bad reviewer signature", preSign: true, mutate: func(*Envelope) {},
			tamper: func(psig, _ string) (string, string) { return psig, hex.EncodeToString(make([]byte, 64)) }, now: 500},
		{name: "content edited after signing", preSign: false,
			mutate: func(e *Envelope) { e.Measurement["nodes"] = "1" }, now: 500},
		{name: "unknown provider", preSign: true,
			mutate: func(e *Envelope) { e.ProviderID = "prov-nobody" }, now: 500},
		{name: "revoked provider", preSign: true, mutate: func(*Envelope) {},
			trust: func(t *testing.T, tr *qualification.TrustRegistry) {
				if err := tr.RevokeProvider("prov-benchlab"); err != nil {
					t.Fatalf("RevokeProvider: %v", err)
				}
			}, now: 500},
		{name: "unauthorized provider for this gate", preSign: true,
			mutate: func(e *Envelope) { e.GateID = "pentest" }, now: 500},
		{name: "missing reviewer", preSign: true,
			mutate: func(e *Envelope) { e.ReviewerID = "" }, now: 500},
		{name: "revoked reviewer", preSign: true, mutate: func(*Envelope) {},
			trust: func(t *testing.T, tr *qualification.TrustRegistry) {
				if err := tr.RevokeReviewer("rev-eng-lead"); err != nil {
					t.Fatalf("RevokeReviewer: %v", err)
				}
			}, now: 500},
		{name: "wrong commit", preSign: true,
			mutate: func(e *Envelope) { e.Commit = "0000000000000000000000000000000000000000" }, now: 500},
		{name: "wrong source hash", preSign: true,
			mutate: func(e *Envelope) { e.SourceHash = "sha256:wrong" }, now: 500},
		{name: "wrong environment", preSign: true,
			mutate: func(e *Envelope) { e.Environment = "someone-laptop" }, now: 500},
		{name: "wrong artifact hash", preSign: true,
			mutate: func(e *Envelope) {
				e.Artifacts[0].Hash = "sha256:tampered"
				e.ArtifactRootHash = ArtifactRoot(e.Artifacts)
			}, now: 500},
		{name: "expired evidence", preSign: true, mutate: func(*Envelope) {}, now: 1000},
		{name: "fixture claiming LIVE", preSign: true,
			mutate: func(e *Envelope) { e.OriginKind = provenance.OriginSynthetic }, now: 500},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := liveEnvelope()
			if tc.preSign {
				tc.mutate(&e)
			}
			tr, ppriv, rpriv := signingTrust(t, "scale_qualification")
			// The gate the evidence claims must exist in the registry, so
			// the "unauthorized provider" case reaches the authorization
			// check rather than an unknown-gate error.
			reg := registryFor(t, tr, e.GateID)
			psig, rsig := signedSubmission(t, e, ppriv, rpriv, tc.now)
			if !tc.preSign {
				tc.mutate(&e)
			}
			if tc.tamper != nil {
				psig, rsig = tc.tamper(psig, rsig)
			}
			if tc.trust != nil {
				tc.trust(t, tr)
			}

			v := Validator{
				Release: testRelease(), Trust: tr,
				AllowedEnvironments: []string{"vendor-lab", "aws-us-east-1"},
			}
			out, err := v.Submit(reg, e, psig, rsig, tc.now)
			if err == nil {
				t.Fatalf("%s was ACCEPTED", tc.name)
			}
			if out.Accepted {
				t.Fatalf("%s produced an accepted verdict alongside an error", tc.name)
			}
			if len(out.Reasons) == 0 {
				t.Fatalf("%s was rejected with no stated reason", tc.name)
			}
			// And, critically, the gate did not advance.
			if rec, ok := reg.Get(e.GateID); ok {
				if rec.Status == qualification.StatusQualified || rec.Status == qualification.StatusVerified {
					t.Fatalf("%s left the gate at %s", tc.name, rec.Status)
				}
			}
		})
	}
}

// TestStaleEvidenceIsRefusedWithTheNamedReason keeps PHASE E2's named
// outcome distinguishable all the way through the submission chain: a
// stale-evidence refusal must never be confused with an untrustworthy
// one.
func TestStaleEvidenceIsRefusedWithTheNamedReason(t *testing.T) {
	e := liveEnvelope()
	e.BinaryHash = "sha256:a-binary-from-a-previous-release"
	tr, ppriv, rpriv := signingTrust(t, e.GateID)
	reg := registryFor(t, tr, e.GateID)
	psig, rsig := signedSubmission(t, e, ppriv, rpriv, 500)

	// The release binding still matches on commit and source hash, so
	// the ONLY thing wrong is that the binary is from another build.
	rel := testRelease()
	v := Validator{Release: rel, Trust: tr}
	out, err := v.Submit(reg, e, psig, rsig, 500)
	if err == nil {
		t.Fatal("evidence describing a different binary was accepted")
	}
	if !errors.Is(err, ErrBinaryHashMismatch) && !errors.Is(err, ErrNotFresh) {
		t.Fatalf("err = %v, want a binary-hash or freshness refusal", err)
	}
	if out.Accepted {
		t.Fatal("stale evidence produced an accepted verdict")
	}
}

// TestEnvelopeOnlyFieldsAreCoveredByTheSignature closes the gap that
// motivated the projection: binary_hash, the artifact root and the
// fixture/LIVE classification have no home in the registry's own
// ExternalEvidence, so they are folded into the measurement map, which
// IS covered by the artifact hash both signatures are made over.
func TestEnvelopeOnlyFieldsAreCoveredByTheSignature(t *testing.T) {
	base := liveEnvelope()
	baseHash := base.ToQualificationEvidence("", "", 500).ArtifactHash

	for name, mutate := range map[string]func(*Envelope){
		"binary_hash":        func(e *Envelope) { e.BinaryHash = "sha256:different" },
		"sbom_hash":          func(e *Envelope) { e.SBOMHash = "sha256:different" },
		"artifact_root_hash": func(e *Envelope) { e.ArtifactRootHash = "sha256:different" },
		"classification":     func(e *Envelope) { e.Classification = ClassificationFixture },
		"origin_kind":        func(e *Envelope) { e.OriginKind = provenance.OriginSynthetic },
		"rights_state":       func(e *Envelope) { e.RightsState = provenance.RightsRevoked },
		"attestation":        func(e *Envelope) { e.Attestation = provenance.AttestationUnattested },
		"limitations":        func(e *Envelope) { e.Limitations = []string{"a new caveat"} },
	} {
		e := liveEnvelope()
		mutate(&e)
		if e.ToQualificationEvidence("", "", 500).ArtifactHash == baseHash {
			t.Errorf("changing %s did not change the hash the signatures cover -- it could be edited after signing", name)
		}
	}
}

// TestSubmitRequiresARegistry is the fail-closed check on the bridge
// itself.
func TestSubmitRequiresARegistry(t *testing.T) {
	v := Validator{Release: testRelease(), Trust: qualification.NewTrustRegistry()}
	if _, err := v.Submit(nil, liveEnvelope(), "a", "b", 500); err == nil {
		t.Fatal("Submit accepted a nil qualification registry")
	}
}

// TestFixtureEvidenceIsRefusedBeforeAnySignatureWork records the
// deliberate ordering: a fixture claiming to be LIVE is refused for
// THAT reason, not for some incidental downstream failure.
func TestFixtureEvidenceIsRefusedBeforeAnySignatureWork(t *testing.T) {
	e := liveEnvelope()
	e.OriginKind = provenance.OriginReplay // still claims Classification LIVE

	tr, _, _ := signingTrust(t, e.GateID)
	reg := registryFor(t, tr, e.GateID)
	v := Validator{Release: testRelease(), Trust: tr}

	// Signatures are deliberately garbage: the fixture check must fire
	// before anything looks at them.
	out, err := v.Submit(reg, e, "not-a-signature", "also-not-a-signature", 500)
	if !errors.Is(err, ErrFixtureClaimedLive) {
		t.Fatalf("err = %v, want ErrFixtureClaimedLive", err)
	}
	if out.Accepted {
		t.Fatal("a fixture claiming LIVE was accepted")
	}
	// Nothing should have been submitted to the registry at all.
	if rec, ok := reg.Get(e.GateID); ok && rec.Status != qualification.StatusBlockedExternal {
		t.Fatalf("gate moved to %s on a submission that never should have reached the registry", rec.Status)
	}
}

// TestHonestFixtureSubmissionIsStillRefusedForTheEightBlockers is the
// program's boundary restated as a test: a correctly-labelled FIXTURE
// envelope is a legitimate artifact, and it still cannot qualify an
// externally-blocked gate, because its classification says what it is.
func TestHonestFixtureSubmissionIsStillRefusedForTheEightBlockers(t *testing.T) {
	e := fixtureEnvelope()
	tr, ppriv, rpriv := signingTrust(t, e.GateID)
	reg := registryFor(t, tr, e.GateID)
	psig, rsig := signedSubmission(t, e, ppriv, rpriv, 500)

	v := Validator{
		Release: testRelease(), Trust: tr,
		// The gate accepts only real external infrastructure.
		AllowedEnvironments: []string{"vendor-lab", "aws-us-east-1"},
	}
	out, err := v.Submit(reg, e, psig, rsig, 500)
	if err == nil {
		t.Fatal("an in-sandbox fixture qualified an externally-blocked gate")
	}
	if !errors.Is(err, ErrEnvironmentNotAllowed) {
		t.Fatalf("err = %v, want the environment refusal", err)
	}
	if out.Accepted {
		t.Fatal("fixture evidence produced an accepted verdict")
	}
	if rec, _ := reg.Get(e.GateID); rec.Status != qualification.StatusBlockedExternal {
		t.Fatalf("the gate moved to %s; it must stay BLOCKED_EXTERNAL", rec.Status)
	}
}
