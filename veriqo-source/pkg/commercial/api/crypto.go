// This file answers Commercialization Sprint P0-D directly: "D.
// Cryptographic trust: Implement HSM/KMS abstraction -> manifest
// signing -> dossier signing -> ledger/Merkle anchor -> verification."
//
// The HSM/KMS abstraction already exists and is already real:
// pkg/platform/security/keys.KeyProvider (the audit's own required
// interface, "Jangan hardcode HSM. Buat interface."), with a working
// key lifecycle (PENDING -> ACTIVE -> RETIRED -> REVOKED, retroactive
// revocation) via keys.Manager, and MemoryKeyProvider/FileKeyProvider/
// AWSKeyProvider-shaped implementations already shipped and tested.
// The ledger/Merkle anchor also already exists (pkg/platform/audit's
// Checkpoint/MerkleRoot, already read by cmd/veriqo-commercial-verify).
// What did not exist is this file: wiring that real key manager into
// the Commercial API so evidence manifests and dossier packages are
// actually signed, closing the honest SKIP this engagement's own
// earlier round named ("verifier sekarang secara jujur SKIP signature
// verification, karena signing scheme belum ada").
package commercialapi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/platform/security/keys"
)

// ErrSigningNotEnabled is returned by any explicitly signing-required
// call made before EnableSigning.
var ErrSigningNotEnabled = errors.New("commercialapi: signing is not enabled on this Store -- call EnableSigning first")

// EnableSigning turns on real Ed25519 evidence and dossier signing for
// this Store, using mgr's currently-ACTIVE keys.PurposeEvidence key.
// mgr must already have at least one such key registered AND activated
// (see keys.NewManager, Manager.Register, Manager.Activate) --
// EnableSigning itself never creates, registers, or activates a key:
// key provisioning is a real operational/security decision this Store
// does not make unasked, mirroring pkg/blockers/hsmkms's own
// "no silent fallback" discipline. Evidence submitted and dossiers
// generated AFTER this call are signed; anything from before is not
// retroactively signed.
func (s *Store) EnableSigning(mgr *keys.Manager) error {
	if mgr == nil {
		return fmt.Errorf("commercialapi: EnableSigning: a nil key manager cannot sign anything")
	}
	if _, err := mgr.ActiveKeyFor(keys.PurposeEvidence); err != nil {
		return fmt.Errorf("commercialapi: EnableSigning: no active evidence-signing key: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyManager = mgr
	return nil
}

// SigningEnabled reports whether this Store currently signs evidence
// and dossiers.
func (s *Store) SigningEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keyManager != nil
}

// signDigestLocked signs digest with the Store's currently active
// EVIDENCE-purpose key. Must be called with s.mu already held. Returns
// (nil error, zero KeyMetadata, nil signature) semantics are NOT used
// here -- signing is either fully enabled and succeeds, or the caller
// must not have reached this method at all (see SigningEnabled checks
// at every call site) -- so ErrSigningNotEnabled is the one sentinel
// this returns when s.keyManager is nil, rather than a silent no-op
// that could be mistaken for "signed, but empty."
func (s *Store) signDigestLocked(ctx context.Context, digest []byte, tick uint64) (sig []byte, keyID string, keyVersion int, err error) {
	if s.keyManager == nil {
		return nil, "", 0, ErrSigningNotEnabled
	}
	key, err := s.keyManager.ActiveKeyFor(keys.PurposeEvidence)
	if err != nil {
		return nil, "", 0, fmt.Errorf("no active evidence-signing key: %w", err)
	}
	sig, err = s.keyManager.Sign(ctx, key.KeyID, digest, tick)
	if err != nil {
		return nil, "", 0, fmt.Errorf("signing: %w", err)
	}
	return sig, key.KeyID, key.Version, nil
}

// signEvidenceIfEnabledLocked signs manifestHash and returns a real
// evidencefabric.EvidenceSignature, or (nil, nil) -- not an error --
// when signing is disabled: an unsigned evidence item is this
// reference build's honest, named default absent EnableSigning, never
// a failure. Must be called with s.mu already held.
func (s *Store) signEvidenceIfEnabledLocked(manifestHash string, tick uint64) (*evidencefabric.EvidenceSignature, error) {
	if s.keyManager == nil {
		return nil, nil
	}
	sig, keyID, keyVersion, err := s.signDigestLocked(context.Background(), []byte(manifestHash), tick)
	if err != nil {
		return nil, err
	}
	return &evidencefabric.EvidenceSignature{
		Algorithm: "Ed25519", KeyID: keyID, KeyVersion: keyVersion,
		Signature: hex.EncodeToString(sig), SignedManifestHash: manifestHash, SignedAtTick: tick,
	}, nil
}
