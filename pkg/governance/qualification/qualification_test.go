package qualification

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"
)

const (
	releaseCommit = "abc123commit"
	releaseSource = "sourcehash-abc123"
)

// testTrust builds a trust registry with one active provider and one
// active reviewer, returning their private keys so tests can sign
// evidence as that identity.
func testTrust(t *testing.T, gateID string) (*TrustRegistry, ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	trust := NewTrustRegistry()
	provPub, provPriv, _ := ed25519.GenerateKey(nil)
	revPub, revPriv, _ := ed25519.GenerateKey(nil)
	if err := trust.RegisterProvider(Provider{
		ProviderID: "provider-1", LegalName: "Acme Security Labs", ProviderType: "penetration_testing_vendor",
		PublicKeyHex: hex.EncodeToString(provPub), ValidFromTick: 0, ValidUntilTick: 1_000_000,
		AuthorizedGateTypes: []string{gateID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := trust.RegisterReviewer(Reviewer{
		ReviewerID: "reviewer-1", Name: "CISO", Role: "security_lead",
		PublicKeyHex: hex.EncodeToString(revPub), Authority: "release_governance",
		ValidFromTick: 0, ValidUntilTick: 1_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	return trust, provPriv, revPriv
}

func signedEvidence(gate string, provPriv, revPriv ed25519.PrivateKey) ExternalEvidence {
	e := ExternalEvidence{
		GateID: gate, ProviderID: "provider-1", ReviewerID: "reviewer-1",
		ReportID: "ASL-2026-0442", Scope: "REST, gRPC, plugin boundary, storage", Environment: "staging-mirror",
		Commit: releaseCommit, SourceHash: releaseSource,
		Measurement: map[string]string{"critical_findings": "0", "high_findings": "0"},
		StartTick:   100, EndTick: 200, ExpiresAtTick: 100000, SubmittedAtTick: 250,
	}
	e = NewEvidence(e)
	sig := ed25519.Sign(provPriv, []byte(e.ArtifactHash))
	e.ProviderSignature = hex.EncodeToString(sig)
	sig = ed25519.Sign(revPriv, []byte(e.ArtifactHash))
	e.ReviewerSignature = hex.EncodeToString(sig)
	return e
}

func TestBlockedGateCannotBeSatisfiedWithoutEvidence(t *testing.T) {
	trust, _, _ := testTrust(t, "pentest")
	r := NewRegistry(trust)
	if err := r.RegisterBlocked("pentest", "independent pentest", "needs external vendor"); err != nil {
		t.Fatal(err)
	}
	rec, ok := r.Get("pentest")
	if !ok {
		t.Fatal("expected gate to be registered")
	}
	if rec.Status != StatusBlockedExternal {
		t.Fatalf("a freshly registered gate must be BLOCKED_EXTERNAL, got %s", rec.Status)
	}
	if rec.Satisfied(0) {
		t.Fatal("a blocked gate with no evidence must never be Satisfied")
	}
}

func TestDuplicateRegistrationRefused(t *testing.T) {
	r := NewRegistry(nil)
	_ = r.RegisterBlocked("pentest", "d", "b")
	if err := r.RegisterBlocked("pentest", "d", "b"); !errors.Is(err, ErrDuplicateGate) {
		t.Fatalf("expected ErrDuplicateGate, got %v", err)
	}
}

func TestFullLifecycleReachesVerified(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "supply_chain_scan")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("supply_chain_scan", "scanners in CI", "needs network egress")
	ev := signedEvidence("supply_chain_scan", provPriv, revPriv)
	if err := r.SubmitEvidence("supply_chain_scan", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("supply_chain_scan", 300, releaseCommit, releaseSource); err != nil {
		t.Fatal(err)
	}
	if err := r.Qualify("supply_chain_scan", 300); err != nil {
		t.Fatal(err)
	}
	rec, _ := r.Get("supply_chain_scan")
	if !rec.Satisfied(300) {
		t.Fatal("a qualified gate with unexpired evidence must be Satisfied")
	}
	if err := r.VerifyGate("supply_chain_scan", 300); err != nil {
		t.Fatal(err)
	}
	rec, _ = r.Get("supply_chain_scan")
	if rec.Status != StatusVerified {
		t.Fatalf("expected VERIFIED, got %s", rec.Status)
	}
	if len(rec.History) != 4 {
		t.Fatalf("expected 4 recorded transitions (submit, validate, qualify, verify), got %d: %+v",
			len(rec.History), rec.History)
	}
}

// --- Adversarial "no false green" campaign (V7.12.1 audit section 25) ---
// Every attack below must be rejected: the gate must never reach
// QUALIFIED/VERIFIED as a result.

func TestAttack_TamperedArtifactHashRejected(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	ev := signedEvidence("pentest", provPriv, revPriv)
	ev.Measurement["critical_findings"] = "1" // mutate content after hashing+signing
	if err := r.SubmitEvidence("pentest", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("pentest", 300, releaseCommit, releaseSource); !errors.Is(err, ErrArtifactHashInvalid) {
		t.Fatalf("expected ErrArtifactHashInvalid, got %v", err)
	}
}

func TestAttack_FakeSignatureRejected(t *testing.T) {
	trust, _, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	_, attackerPriv, _ := ed25519.GenerateKey(nil) // NOT the registered provider key
	ev := ExternalEvidence{
		GateID: "pentest", ProviderID: "provider-1", ReviewerID: "reviewer-1",
		ReportID: "r1", Scope: "s", Environment: "e", Commit: releaseCommit, SourceHash: releaseSource,
		Measurement: map[string]string{"critical": "0"}, StartTick: 1, EndTick: 2, ExpiresAtTick: 100000,
	}
	ev = NewEvidence(ev)
	ev.ProviderSignature = hex.EncodeToString(ed25519.Sign(attackerPriv, []byte(ev.ArtifactHash)))
	ev.ReviewerSignature = hex.EncodeToString(ed25519.Sign(revPriv, []byte(ev.ArtifactHash)))
	_ = r.SubmitEvidence("pentest", ev)
	if err := r.Validate("pentest", 300, releaseCommit, releaseSource); !errors.Is(err, ErrProviderSignature) {
		t.Fatalf("a signature from an unregistered key must be rejected, got %v", err)
	}
}

func TestAttack_WrongPublicKeyRejected(t *testing.T) {
	// Same as fake signature but framed as "attacker knows the provider
	// ID, just not the private key" -- the realistic attack shape.
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	ev := signedEvidence("pentest", provPriv, revPriv)
	_, wrongKey, _ := ed25519.GenerateKey(nil)
	ev.ReviewerSignature = hex.EncodeToString(ed25519.Sign(wrongKey, []byte(ev.ArtifactHash)))
	_ = r.SubmitEvidence("pentest", ev)
	if err := r.Validate("pentest", 300, releaseCommit, releaseSource); !errors.Is(err, ErrReviewerSignature) {
		t.Fatalf("expected ErrReviewerSignature, got %v", err)
	}
}

func TestAttack_UnknownProviderRejected(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	ev := signedEvidence("pentest", provPriv, revPriv)
	ev.ProviderID = "provider-does-not-exist"
	// Re-sign so the hash is internally consistent; the identity itself is the attack.
	ev.ArtifactHash = computeArtifactHash(ev)
	ev.ProviderSignature = hex.EncodeToString(ed25519.Sign(provPriv, []byte(ev.ArtifactHash)))
	ev.ReviewerSignature = hex.EncodeToString(ed25519.Sign(revPriv, []byte(ev.ArtifactHash)))
	_ = r.SubmitEvidence("pentest", ev)
	if err := r.Validate("pentest", 300, releaseCommit, releaseSource); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("expected ErrUnknownProvider, got %v", err)
	}
}

func TestAttack_RevokedProviderRejected(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	if err := trust.RevokeProvider("provider-1"); err != nil {
		t.Fatal(err)
	}
	ev := signedEvidence("pentest", provPriv, revPriv)
	_ = r.SubmitEvidence("pentest", ev)
	if err := r.Validate("pentest", 300, releaseCommit, releaseSource); !errors.Is(err, ErrProviderRevoked) {
		t.Fatalf("expected ErrProviderRevoked, got %v", err)
	}
}

func TestAttack_RevokedReviewerRejected(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	if err := trust.RevokeReviewer("reviewer-1"); err != nil {
		t.Fatal(err)
	}
	ev := signedEvidence("pentest", provPriv, revPriv)
	_ = r.SubmitEvidence("pentest", ev)
	if err := r.Validate("pentest", 300, releaseCommit, releaseSource); !errors.Is(err, ErrReviewerRevoked) {
		t.Fatalf("expected ErrReviewerRevoked, got %v", err)
	}
}

func TestAttack_ProviderNotAuthorizedForThisGateRejected(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest") // provider authorized only for "pentest"
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("hsm_kms", "d", "b")
	ev := signedEvidence("hsm_kms", provPriv, revPriv) // signed for a gate the provider isn't authorized for
	_ = r.SubmitEvidence("hsm_kms", ev)
	if err := r.Validate("hsm_kms", 300, releaseCommit, releaseSource); !errors.Is(err, ErrProviderNotAuthorized) {
		t.Fatalf("expected ErrProviderNotAuthorized, got %v", err)
	}
}

func TestAttack_WrongReleaseCommitRejected(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	ev := signedEvidence("pentest", provPriv, revPriv)
	_ = r.SubmitEvidence("pentest", ev)
	if err := r.Validate("pentest", 300, "some-other-commit", releaseSource); !errors.Is(err, ErrReleaseMismatch) {
		t.Fatalf("evidence bound to a different commit must be rejected, got %v", err)
	}
}

func TestAttack_WrongSourceHashRejected(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	ev := signedEvidence("pentest", provPriv, revPriv)
	_ = r.SubmitEvidence("pentest", ev)
	if err := r.Validate("pentest", 300, releaseCommit, "some-other-source-hash"); !errors.Is(err, ErrReleaseMismatch) {
		t.Fatalf("evidence bound to a different source hash must be rejected, got %v", err)
	}
}

func TestAttack_ExpiredEvidenceRejectedAtValidate(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	ev := ExternalEvidence{
		GateID: "pentest", ProviderID: "provider-1", ReviewerID: "reviewer-1",
		ReportID: "r1", Scope: "s", Environment: "e", Commit: releaseCommit, SourceHash: releaseSource,
		Measurement: map[string]string{"critical": "0"}, StartTick: 1, EndTick: 2, ExpiresAtTick: 400,
	}
	ev = NewEvidence(ev)
	ev.ProviderSignature = hex.EncodeToString(ed25519.Sign(provPriv, []byte(ev.ArtifactHash)))
	ev.ReviewerSignature = hex.EncodeToString(ed25519.Sign(revPriv, []byte(ev.ArtifactHash)))
	_ = r.SubmitEvidence("pentest", ev)
	if err := r.Validate("pentest", 500, releaseCommit, releaseSource); !errors.Is(err, ErrAlreadyExpired) {
		t.Fatalf("expected ErrAlreadyExpired, got %v", err)
	}
}

func TestAttack_ReplayedEvidenceAcrossGatesRejected(t *testing.T) {
	// Evidence signed for gate A must not validate against gate B: GateID
	// is inside the signed artifact hash, so relabeling it after signing
	// (the one thing an attacker without the private key can still try)
	// breaks the hash the signatures were made over.
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("scale_qualification", "d", "b")
	ev := signedEvidence("pentest", provPriv, revPriv) // signed for "pentest"
	ev.GateID = "scale_qualification"                  // then relabeled for a different gate
	if err := r.SubmitEvidence("scale_qualification", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("scale_qualification", 300, releaseCommit, releaseSource); err == nil {
		t.Fatal("evidence replayed onto a different gate must be rejected")
	}
}

func TestAttack_EvidenceFromAnotherReleaseCannotQualifyThisOne(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "pentest")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("pentest", "d", "b")
	// Evidence genuinely signed for release v7.12.0.
	ev := ExternalEvidence{
		GateID: "pentest", ProviderID: "provider-1", ReviewerID: "reviewer-1",
		ReportID: "r1", Scope: "s", Environment: "e", Commit: "old-commit-v7.12.0", SourceHash: "old-source-hash",
		Measurement: map[string]string{"critical": "0"}, StartTick: 1, EndTick: 2, ExpiresAtTick: 100000,
	}
	ev = NewEvidence(ev)
	ev.ProviderSignature = hex.EncodeToString(ed25519.Sign(provPriv, []byte(ev.ArtifactHash)))
	ev.ReviewerSignature = hex.EncodeToString(ed25519.Sign(revPriv, []byte(ev.ArtifactHash)))
	_ = r.SubmitEvidence("pentest", ev)
	// Attempting to validate against the CURRENT release (v7.12.1) must fail.
	if err := r.Validate("pentest", 300, releaseCommit, releaseSource); !errors.Is(err, ErrReleaseMismatch) {
		t.Fatalf("evidence from a prior release must not qualify this one, got %v", err)
	}
}

func TestAttack_IncompleteEvidenceRejected(t *testing.T) {
	r := NewRegistry(NewTrustRegistry())
	_ = r.RegisterBlocked("hsm_kms", "d", "b")
	ev := NewEvidence(ExternalEvidence{GateID: "hsm_kms", ProviderID: "x"})
	if err := r.SubmitEvidence("hsm_kms", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("hsm_kms", 1, releaseCommit, releaseSource); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("expected ErrEvidenceIncomplete, got %v", err)
	}
}

func TestExpireStaleDemotesLapsedQualificationsAndRevokedTrust(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "scale_qualification")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("scale_qualification", "d", "b")
	ev := signedEvidence("scale_qualification", provPriv, revPriv)
	_ = r.SubmitEvidence("scale_qualification", ev)
	_ = r.Validate("scale_qualification", 300, releaseCommit, releaseSource)
	_ = r.Qualify("scale_qualification", 300)

	// Case 1: evidence expiry lapses.
	r.ExpireStale(200000)
	rec, _ := r.Get("scale_qualification")
	if rec.Status != StatusExpired {
		t.Fatalf("expected ExpireStale to demote a lapsed QUALIFIED record on expiry, got %s", rec.Status)
	}

	// Case 2: trust anchor revoked after qualification, before expiry.
	trust2, provPriv2, revPriv2 := testTrust(t, "hsm_kms")
	r2 := NewRegistry(trust2)
	_ = r2.RegisterBlocked("hsm_kms", "d", "b")
	ev2 := signedEvidence("hsm_kms", provPriv2, revPriv2)
	_ = r2.SubmitEvidence("hsm_kms", ev2)
	_ = r2.Validate("hsm_kms", 300, releaseCommit, releaseSource)
	_ = r2.Qualify("hsm_kms", 300)
	_ = trust2.RevokeProvider("provider-1")
	r2.ExpireStale(300)
	rec2, _ := r2.Get("hsm_kms")
	if rec2.Status != StatusExpired {
		t.Fatalf("a QUALIFIED gate whose provider was later revoked must be demoted, got %s", rec2.Status)
	}
}

func TestVerifyGateRejectsRevocationSinceQualification(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "spire_mtls")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("spire_mtls", "d", "b")
	ev := signedEvidence("spire_mtls", provPriv, revPriv)
	_ = r.SubmitEvidence("spire_mtls", ev)
	_ = r.Validate("spire_mtls", 300, releaseCommit, releaseSource)
	_ = r.Qualify("spire_mtls", 300)
	_ = trust.RevokeReviewer("reviewer-1")
	if err := r.VerifyGate("spire_mtls", 300); !errors.Is(err, ErrReviewerRevoked) {
		t.Fatalf("VerifyGate must re-check trust anchors, got %v", err)
	}
}

func TestRejectedEvidenceCanBeResubmitted(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "multi_region_dr")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("multi_region_dr", "d", "b")
	bad := NewEvidence(ExternalEvidence{GateID: "multi_region_dr", ProviderID: "x"})
	_ = r.SubmitEvidence("multi_region_dr", bad)
	if err := r.Validate("multi_region_dr", 1, releaseCommit, releaseSource); err == nil {
		t.Fatal("expected the incomplete submission to be rejected")
	}
	good := signedEvidence("multi_region_dr", provPriv, revPriv)
	if err := r.SubmitEvidence("multi_region_dr", good); err != nil {
		t.Fatalf("resubmission after rejection must be allowed: %v", err)
	}
	if err := r.Validate("multi_region_dr", 300, releaseCommit, releaseSource); err != nil {
		t.Fatal(err)
	}
}

func TestCannotSubmitEvidenceMidValidation(t *testing.T) {
	trust, provPriv, revPriv := testTrust(t, "live_data")
	r := NewRegistry(trust)
	_ = r.RegisterBlocked("live_data", "d", "b")
	_ = r.SubmitEvidence("live_data", signedEvidence("live_data", provPriv, revPriv))
	_ = r.Validate("live_data", 300, releaseCommit, releaseSource)
	if err := r.SubmitEvidence("live_data", signedEvidence("live_data", provPriv, revPriv)); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("expected ErrIllegalTransition, got %v", err)
	}
}

func TestUnknownGateOperationsFail(t *testing.T) {
	r := NewRegistry(NewTrustRegistry())
	if err := r.SubmitEvidence("nonexistent", ExternalEvidence{}); !errors.Is(err, ErrUnknownGate) {
		t.Fatalf("expected ErrUnknownGate, got %v", err)
	}
	if err := r.Validate("nonexistent", 1, releaseCommit, releaseSource); !errors.Is(err, ErrUnknownGate) {
		t.Fatalf("expected ErrUnknownGate, got %v", err)
	}
	if err := r.Qualify("nonexistent", 1); !errors.Is(err, ErrUnknownGate) {
		t.Fatalf("expected ErrUnknownGate, got %v", err)
	}
	if err := r.VerifyGate("nonexistent", 1); !errors.Is(err, ErrUnknownGate) {
		t.Fatalf("expected ErrUnknownGate, got %v", err)
	}
}

func TestMalformedPublicKeyRefusedAtRegistration(t *testing.T) {
	trust := NewTrustRegistry()
	err := trust.RegisterProvider(Provider{ProviderID: "bad", PublicKeyHex: "not-hex-and-wrong-length"})
	if err == nil {
		t.Fatal("a provider with a malformed public key must be refused at registration")
	}
}
