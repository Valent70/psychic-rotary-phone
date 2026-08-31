package commercialapi

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"veriqo/pkg/platform/security/keys"
)

// mustEvidenceKeyManager builds a real, working keys.Manager backed by
// keys.MemoryKeyProvider with exactly one ACTIVE keys.PurposeEvidence
// key -- the minimum EnableSigning requires.
func mustEvidenceKeyManager(t *testing.T) (*keys.Manager, string) {
	t.Helper()
	provider := keys.NewMemoryKeyProvider()
	md, err := provider.Generate("evidence-key-1", keys.PurposeEvidence, 0, 0)
	if err != nil {
		t.Fatalf("provider.Generate: %v", err)
	}
	mgr := keys.NewManager(provider)
	if err := mgr.Register(md); err != nil {
		t.Fatalf("mgr.Register: %v", err)
	}
	if err := mgr.Activate(md.KeyID); err != nil {
		t.Fatalf("mgr.Activate: %v", err)
	}
	return mgr, md.KeyID
}

func TestEnableSigningRequiresAnActiveEvidenceKey(t *testing.T) {
	s := NewStore()
	if err := s.EnableSigning(nil); err == nil {
		t.Fatal("expected EnableSigning(nil) to be refused")
	}

	provider := keys.NewMemoryKeyProvider()
	md, err := provider.Generate("evidence-key-pending", keys.PurposeEvidence, 0, 0)
	if err != nil {
		t.Fatalf("provider.Generate: %v", err)
	}
	mgr := keys.NewManager(provider)
	if err := mgr.Register(md); err != nil {
		t.Fatalf("mgr.Register: %v", err)
	}
	// Deliberately never Activate -- the key stays PENDING.
	if err := s.EnableSigning(mgr); err == nil {
		t.Fatal("expected EnableSigning to refuse a manager with no ACTIVE evidence key")
	}
	if s.SigningEnabled() {
		t.Fatal("expected SigningEnabled to remain false after a refused EnableSigning")
	}
}

func TestSubmitEvidenceSignsWhenEnabled(t *testing.T) {
	s := NewStore()
	mgr, keyID := mustEvidenceKeyManager(t)
	if err := s.EnableSigning(mgr); err != nil {
		t.Fatalf("EnableSigning: %v", err)
	}
	if !s.SigningEnabled() {
		t.Fatal("expected SigningEnabled to be true after EnableSigning")
	}

	const tenant, caseID = "tenant-crypto-A", "CASE-CRYPTO-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	rec := mustSubmitEvidence(t, s, tenant, caseID, "EV-CRYPTO-1", 10)

	if rec.Signature == nil {
		t.Fatal("expected a non-nil Signature once signing is enabled")
	}
	if rec.Signature.Algorithm != "Ed25519" || rec.Signature.KeyID != keyID {
		t.Fatalf("unexpected signature metadata: %+v", rec.Signature)
	}
	if rec.Signature.SignedManifestHash != rec.Integrity.ManifestHash {
		t.Fatalf("expected the signature to cover the real ManifestHash, got signed=%s actual=%s",
			rec.Signature.SignedManifestHash, rec.Integrity.ManifestHash)
	}

	// Independently verify the signature the same way a customer would:
	// via the real keys.Manager.Verify, over the exact digest recorded.
	sigBytes, err := hex.DecodeString(rec.Signature.Signature)
	if err != nil {
		t.Fatalf("decoding hex signature: %v", err)
	}
	if err := mgr.Verify(context.Background(), keyID, []byte(rec.Signature.SignedManifestHash), sigBytes); err != nil {
		t.Fatalf("independent signature verification failed: %v", err)
	}

	// A single-bit tamper must be detected.
	tampered := append([]byte(nil), sigBytes...)
	tampered[0] ^= 0xFF
	if err := mgr.Verify(context.Background(), keyID, []byte(rec.Signature.SignedManifestHash), tampered); err == nil {
		t.Fatal("expected a tampered signature to fail verification")
	}
}

func TestSubmitEvidenceUnsignedWhenSigningNotEnabled(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-crypto-unsigned", "CASE-CRYPTO-UNSIGNED-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	rec := mustSubmitEvidence(t, s, tenant, caseID, "EV-CRYPTO-UNSIGNED-1", 10)
	if rec.Signature != nil {
		t.Fatalf("expected a nil Signature when signing was never enabled, got %+v", rec.Signature)
	}
}

func TestGetEvidenceReturnsTheSameStoredSignature(t *testing.T) {
	s := NewStore()
	mgr, _ := mustEvidenceKeyManager(t)
	if err := s.EnableSigning(mgr); err != nil {
		t.Fatalf("EnableSigning: %v", err)
	}
	const tenant, caseID = "tenant-crypto-get", "CASE-CRYPTO-GET-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	submitted := mustSubmitEvidence(t, s, tenant, caseID, "EV-CRYPTO-GET-1", 10)

	fetched, err := s.GetEvidence(tenant, "EV-CRYPTO-GET-1")
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	if fetched.Signature == nil || fetched.Signature.Signature != submitted.Signature.Signature {
		t.Fatalf("expected GetEvidence to return the same signature SubmitEvidence produced, submitted=%+v fetched=%+v",
			submitted.Signature, fetched.Signature)
	}
}

func TestGenerateDossierSignsThePackageHashWhenEnabled(t *testing.T) {
	s := NewStore()
	mgr, keyID := mustEvidenceKeyManager(t)
	if err := s.EnableSigning(mgr); err != nil {
		t.Fatalf("EnableSigning: %v", err)
	}
	const tenant, caseID = "tenant-crypto-dossier", "CASE-CRYPTO-DOSSIER-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, "EV-API-1", 10)
	if _, err := s.DecideCase(decideInput(tenant, caseID)); err != nil {
		t.Fatalf("DecideCase: %v", err)
	}

	d, err := s.GenerateDossier(tenant, caseID)
	if err != nil {
		t.Fatalf("GenerateDossier: %v", err)
	}
	if d.PackageSignature == nil {
		t.Fatal("expected a non-nil PackageSignature once signing is enabled")
	}
	if d.PackageSignature.SignedPackageHash != d.PackageHash {
		t.Fatalf("expected the signature to cover the real PackageHash, got signed=%s actual=%s",
			d.PackageSignature.SignedPackageHash, d.PackageHash)
	}

	sigBytes, err := hex.DecodeString(d.PackageSignature.Signature)
	if err != nil {
		t.Fatalf("decoding hex signature: %v", err)
	}
	if err := mgr.Verify(context.Background(), keyID, []byte(d.PackageHash), sigBytes); err != nil {
		t.Fatalf("independent dossier signature verification failed: %v", err)
	}

	// Every EvidenceRecord in the inventory should carry its own real
	// per-item signature too, not just the package as a whole.
	for _, rec := range d.EvidenceInventory {
		if rec.Signature == nil {
			t.Fatalf("expected every evidence item in the dossier to be signed, got unsigned: %+v", rec.Identity)
		}
	}
}

func TestRevokedKeyInvalidatesPastSignaturesRetroactively(t *testing.T) {
	s := NewStore()
	mgr, keyID := mustEvidenceKeyManager(t)
	if err := s.EnableSigning(mgr); err != nil {
		t.Fatalf("EnableSigning: %v", err)
	}
	const tenant, caseID = "tenant-crypto-revoke", "CASE-CRYPTO-REVOKE-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	rec := mustSubmitEvidence(t, s, tenant, caseID, "EV-CRYPTO-REVOKE-1", 10)
	sigBytes, err := hex.DecodeString(rec.Signature.Signature)
	if err != nil {
		t.Fatalf("decoding hex signature: %v", err)
	}
	if err := mgr.Verify(context.Background(), keyID, []byte(rec.Signature.SignedManifestHash), sigBytes); err != nil {
		t.Fatalf("expected the signature to verify before revocation: %v", err)
	}

	if err := mgr.Revoke(keyID, "compromised", 100); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := mgr.Verify(context.Background(), keyID, []byte(rec.Signature.SignedManifestHash), sigBytes); !errors.Is(err, keys.ErrKeyRevoked) {
		t.Fatalf("expected a revoked key's past signature to now fail verification with ErrKeyRevoked, got %v", err)
	}
}
