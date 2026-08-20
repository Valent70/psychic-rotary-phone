// Package hsmkms is the hsm_kms blocker's qualification machinery: a
// FailingKeyProvider test double that injects the failure modes a real
// HSM/KMS call can return, plus a failure-matrix runner proving every
// one of them is fail-closed -- no signature is ever produced, no
// silent fallback to another key or provider ever happens, and a
// revoked key's provider is never even touched. It builds directly on
// pkg/platform/security/keys, which already ships the real
// KeyProvider interface, Manager lifecycle, and (as of this package)
// the SoftwareBacked production guard; this package adds nothing to
// the trust model itself, only proof that its failure paths hold.
package hsmkms

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"veriqo/pkg/blockers"
	"veriqo/pkg/platform/security/keys"
)

// FailMode enumerates the ways a real HSM/KMS call can fail.
type FailMode string

const (
	FailUnavailable      FailMode = "UNAVAILABLE"
	FailTimeout          FailMode = "TIMEOUT"
	FailPermissionDenied FailMode = "PERMISSION_DENIED"
	FailWrongKey         FailMode = "WRONG_KEY"
	// FailWrongAlgorithm models the HSM/KMS rejecting a Sign call because
	// the requested key does not support the algorithm the caller asked
	// for (e.g. an EC-only key handle asked to produce an RSA signature).
	// This is the provider-side half of "wrong algorithm"; the
	// verification-side half -- a signature of the wrong shape being fed
	// to Manager.Verify -- is covered separately below as its own
	// RunFailureMatrix scenario, since that is a distinct failure
	// surface from anything FailingKeyProvider's per-key injection can
	// model.
	FailWrongAlgorithm FailMode = "WRONG_ALGORITHM"
)

var allFailModes = []FailMode{FailUnavailable, FailTimeout, FailPermissionDenied, FailWrongKey, FailWrongAlgorithm}

func injectedErr(mode FailMode) error {
	switch mode {
	case FailUnavailable:
		return errors.New("hsmkms: fixture: HSM/KMS endpoint unavailable")
	case FailTimeout:
		return errors.New("hsmkms: fixture: HSM/KMS call timed out")
	case FailPermissionDenied:
		return errors.New("hsmkms: fixture: permission denied by HSM/KMS access policy")
	case FailWrongKey:
		return errors.New("hsmkms: fixture: requested key does not match the HSM/KMS key handle")
	case FailWrongAlgorithm:
		return errors.New("hsmkms: fixture: requested signing algorithm is not supported by this HSM/KMS key handle")
	default:
		return fmt.Errorf("hsmkms: fixture: unknown fail mode %q", mode)
	}
}

// FailingKeyProvider wraps a real keys.KeyProvider and, for any key ID
// listed in FailFor, returns the configured failure instead of
// delegating -- so a qualification run can prove the caller fails
// closed instead of only ever exercising the happy path.
type FailingKeyProvider struct {
	Inner   keys.KeyProvider
	FailFor map[string]FailMode
}

// SoftwareBacked marks this as never production-safe: it exists only to
// inject faults in a qualification run.
func (f *FailingKeyProvider) SoftwareBacked() bool { return true }

func (f *FailingKeyProvider) Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error) {
	if mode, hit := f.FailFor[keyID]; hit {
		return nil, injectedErr(mode)
	}
	return f.Inner.Sign(ctx, keyID, digest)
}

func (f *FailingKeyProvider) PublicKey(ctx context.Context, keyID string) ([]byte, error) {
	if mode, hit := f.FailFor[keyID]; hit {
		return nil, injectedErr(mode)
	}
	return f.Inner.PublicKey(ctx, keyID)
}

// touchTrackingProvider records whether Sign/PublicKey were ever called,
// so a revoked-key scenario can prove the Manager refused to even touch
// the underlying provider -- the strongest form of fail-closed, since it
// means a revoked key's material is never handed to the HSM/KMS layer
// at all, not merely that the returned signature gets discarded.
type touchTrackingProvider struct {
	signCalled bool
}

func (p *touchTrackingProvider) Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error) {
	p.signCalled = true
	return nil, errors.New("hsmkms: fixture: Sign must never be reached for a revoked key")
}

func (p *touchTrackingProvider) PublicKey(ctx context.Context, keyID string) ([]byte, error) {
	return []byte("00"), nil
}

func (p *touchTrackingProvider) SoftwareBacked() bool { return true }

// RunFailureMatrix exercises every HSM/KMS failure mode -- unavailable,
// timeout, permission-denied, wrong-key, and revoked -- against a real
// keys.Manager and confirms each one fails closed: an error is
// returned, no signature bytes are ever produced, and (for the revoked
// case specifically) the underlying provider is never even called. It
// records the outcome on contract via RecordFixtureRun.
func RunFailureMatrix(contract *blockers.Contract) (blockers.RunResult, error) {
	ctx := context.Background()
	measurements := map[string]string{}
	pass := true
	var failures []string

	for _, mode := range allFailModes {
		base := keys.NewMemoryKeyProvider()
		keyID := "hsm-test-" + string(mode)
		md, err := base.Generate(keyID, keys.PurposeEvidence, 0, 0)
		if err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: generate for %s: %w", mode, err)
		}
		fp := &FailingKeyProvider{Inner: base, FailFor: map[string]FailMode{keyID: mode}}
		mgr := keys.NewManager(fp)
		if err := mgr.Register(md); err != nil {
			return blockers.RunResult{}, err
		}
		if err := mgr.Activate(keyID); err != nil {
			return blockers.RunResult{}, err
		}
		sig, err := mgr.Sign(ctx, keyID, []byte("qualification-digest"), 1)
		blocked := err != nil && len(sig) == 0
		measurements[string(mode)] = fmt.Sprintf("blocked=%v", blocked)
		if !blocked {
			pass = false
			failures = append(failures, fmt.Sprintf("%s did not fail closed", mode))
		}
	}

	// Revoked-key scenario: the provider must never be touched at all.
	tracker := &touchTrackingProvider{}
	revokedKeyID := "hsm-test-revoked"
	revokedMD := keys.KeyMetadata{KeyID: revokedKeyID, Purpose: keys.PurposeEvidence, State: keys.StateActive, PublicKey: "00"}
	mgr := keys.NewManager(tracker)
	if err := mgr.Register(revokedMD); err != nil {
		return blockers.RunResult{}, err
	}
	if err := mgr.Revoke(revokedKeyID, "hsmkms qualification", 1); err != nil {
		return blockers.RunResult{}, err
	}
	sig, err := mgr.Sign(ctx, revokedKeyID, []byte("qualification-digest"), 1)
	revokedBlocked := errors.Is(err, keys.ErrKeyRevoked) && len(sig) == 0 && !tracker.signCalled
	measurements["REVOKED"] = fmt.Sprintf("blocked=%v provider_touched=%v", revokedBlocked, tracker.signCalled)
	if !revokedBlocked {
		pass = false
		failures = append(failures, "revoked key was not fail-closed")
	}

	// Production-safety guard: a software-backed provider must be
	// refused outright under env=production, before any signing is
	// attempted.
	prodErr := keys.RequireProductionSafe(keys.NewMemoryKeyProvider(), "production")
	prodGuardHolds := errors.Is(prodErr, keys.ErrSoftwareProviderInProduction)
	measurements["PRODUCTION_GUARD"] = fmt.Sprintf("blocked=%v", prodGuardHolds)
	if !prodGuardHolds {
		pass = false
		failures = append(failures, "software-backed provider was not refused under env=production")
	}

	// Wrong-algorithm scenario: a signature's *shape* doesn't match what
	// the verifier expects -- e.g. an RSA or ECDSA signature (or simply
	// a corrupted ed25519 one) checked against Manager.Verify, which is
	// hardcoded to ed25519. This models a signature produced by one
	// algorithm being handed to a verifier that expects another.
	// Manager.Verify must fail closed: return ErrBadSignature, never
	// panic, never silently accept.
	{
		base := keys.NewMemoryKeyProvider()
		keyID := "hsm-test-wrong-algo-verify"
		md, err := base.Generate(keyID, keys.PurposeEvidence, 0, 0)
		if err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: generate for wrong-algorithm scenario: %w", err)
		}
		mgr := keys.NewManager(base)
		if err := mgr.Register(md); err != nil {
			return blockers.RunResult{}, err
		}
		if err := mgr.Activate(keyID); err != nil {
			return blockers.RunResult{}, err
		}
		digest := []byte("qualification-digest-wrong-algo")
		sig, err := mgr.Sign(ctx, keyID, digest, 1)
		if err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: sign for wrong-algorithm scenario: %w", err)
		}
		wrongAlgoBlocked := true
		func() {
			defer func() {
				if r := recover(); r != nil {
					wrongAlgoBlocked = false
					failures = append(failures, fmt.Sprintf("wrong-algorithm-shaped signature panicked Verify: %v", r))
				}
			}()
			// A signature shaped like a different algorithm's output --
			// e.g. a 256-byte RSA-2048 signature or a DER-encoded ECDSA
			// signature -- is simply the wrong length for ed25519.
			wrongShaped := bytes.Repeat([]byte{0x30}, 256)
			if verr := mgr.Verify(ctx, keyID, digest, wrongShaped); !errors.Is(verr, keys.ErrBadSignature) {
				wrongAlgoBlocked = false
				failures = append(failures, fmt.Sprintf("wrong-algorithm-shaped signature did not return ErrBadSignature: %v", verr))
			}
			// A same-length but corrupted signature must also fail
			// closed -- proves this isn't merely a length check.
			corrupted := append([]byte(nil), sig...)
			corrupted[0] ^= 0xFF
			if verr := mgr.Verify(ctx, keyID, digest, corrupted); !errors.Is(verr, keys.ErrBadSignature) {
				wrongAlgoBlocked = false
				failures = append(failures, fmt.Sprintf("corrupted same-length signature did not return ErrBadSignature: %v", verr))
			}
		}()
		measurements["WRONG_ALGORITHM_VERIFY"] = fmt.Sprintf("blocked=%v", wrongAlgoBlocked)
		if !wrongAlgoBlocked {
			pass = false
		}
	}

	// Tampered-manifest scenario: sign a real digest, then verify a
	// DIFFERENT digest against the ORIGINAL signature -- simulating a
	// manifest altered after signing. Must fail closed.
	{
		base := keys.NewMemoryKeyProvider()
		keyID := "hsm-test-tampered-manifest"
		md, err := base.Generate(keyID, keys.PurposeEvidence, 0, 0)
		if err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: generate for tampered-manifest scenario: %w", err)
		}
		mgr := keys.NewManager(base)
		if err := mgr.Register(md); err != nil {
			return blockers.RunResult{}, err
		}
		if err := mgr.Activate(keyID); err != nil {
			return blockers.RunResult{}, err
		}
		originalDigest := []byte("manifest-v1-content-hash")
		sig, err := mgr.Sign(ctx, keyID, originalDigest, 1)
		if err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: sign for tampered-manifest scenario: %w", err)
		}
		tamperedDigest := []byte("manifest-v2-content-hash-ATTACKER-MODIFIED")
		tamperedBlocked := errors.Is(mgr.Verify(ctx, keyID, tamperedDigest, sig), keys.ErrBadSignature)
		measurements["TAMPERED_MANIFEST"] = fmt.Sprintf("blocked=%v", tamperedBlocked)
		if !tamperedBlocked {
			pass = false
			failures = append(failures, "tampered manifest (altered digest, original signature) was not fail-closed")
		}
		// The original, untampered digest must still verify -- otherwise
		// this scenario would prove nothing specific to tampering, only
		// that Verify always fails.
		if err := mgr.Verify(ctx, keyID, originalDigest, sig); err != nil {
			pass = false
			failures = append(failures, fmt.Sprintf("original untampered manifest failed to verify: %v", err))
		}
	}

	// Rotation scenario: a retired key's past signature stays valid, a
	// successor key's new signatures verify, and the retired key can no
	// longer sign new data. This is existing, already-correct behavior
	// in keys.Manager -- proved here, not re-implemented.
	{
		base := keys.NewMemoryKeyProvider()
		keyA := "hsm-test-rotation-a"
		mdA, err := base.Generate(keyA, keys.PurposeEvidence, 0, 0)
		if err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: generate key A for rotation scenario: %w", err)
		}
		mgr := keys.NewManager(base)
		if err := mgr.Register(mdA); err != nil {
			return blockers.RunResult{}, err
		}
		if err := mgr.Activate(keyA); err != nil {
			return blockers.RunResult{}, err
		}
		d1 := []byte("rotation-digest-pre-rotation")
		s1, err := mgr.Sign(ctx, keyA, d1, 1)
		if err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: sign D1 with key A: %w", err)
		}

		keyB := "hsm-test-rotation-b"
		mdB, err := base.Generate(keyB, keys.PurposeEvidence, 0, 0)
		if err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: generate key B for rotation scenario: %w", err)
		}
		if err := mgr.Rotate(keyA, mdB); err != nil {
			return blockers.RunResult{}, fmt.Errorf("hsmkms: rotate A to B: %w", err)
		}

		oldStillVerifies := mgr.Verify(ctx, keyA, d1, s1) == nil
		measurements["rotation_old_signature_still_verifies"] = fmt.Sprintf("ok=%v", oldStillVerifies)
		if !oldStillVerifies {
			pass = false
			failures = append(failures, "retired key's pre-rotation signature no longer verifies")
		}

		d2 := []byte("rotation-digest-post-rotation")
		s2, signErr2 := mgr.Sign(ctx, keyB, d2, 1)
		newVerifies := signErr2 == nil && mgr.Verify(ctx, keyB, d2, s2) == nil
		measurements["rotation_new_signature_verifies"] = fmt.Sprintf("ok=%v", newVerifies)
		if !newVerifies {
			pass = false
			failures = append(failures, "successor key's new signature does not verify")
		}

		_, signErr := mgr.Sign(ctx, keyA, []byte("post-rotation-attempt-with-retired-key"), 1)
		retiredCannotSign := errors.Is(signErr, keys.ErrKeyNotActive)
		measurements["rotation_retired_key_cannot_sign_new"] = fmt.Sprintf("ok=%v", retiredCannotSign)
		if !retiredCannotSign {
			pass = false
			failures = append(failures, "retired key was still able to sign new data")
		}
	}

	result := blockers.RunResult{
		BlockerID:    contract.ID,
		Mode:         "FIXTURE",
		Pass:         pass,
		Measurements: measurements,
	}
	if !pass {
		result.FailureReason = fmt.Sprintf("%v", failures)
	}
	if err := contract.RecordFixtureRun(result); err != nil {
		return result, err
	}
	return result, nil
}

// LocalTestKeyProvider is a thin wrapper around keys.MemoryKeyProvider
// for this package's own test and qualification fixtures. It does not
// reimplement key storage; every call delegates to the embedded
// keys.MemoryKeyProvider (Generate/Sign/PublicKey/SoftwareBacked all
// promote through unchanged).
//
// LocalTestKeyProvider is explicitly NOT a production KMS/HSM. Its
// private key material lives in this process's memory for the lifetime
// of the test run only, it satisfies keys.SoftwareBacked (so
// keys.RequireProductionSafe refuses it under env=production, same as
// the MemoryKeyProvider it wraps), and it must never be wired into
// anything that touches production key custody. The name is
// deliberately blunt about the one mistake this type exists to prevent:
// LocalTestKeyProvider is not, and can never become, a production KMS.
type LocalTestKeyProvider struct {
	*keys.MemoryKeyProvider
}

// NewLocalTestKeyProvider constructs an empty LocalTestKeyProvider.
func NewLocalTestKeyProvider() *LocalTestKeyProvider {
	return &LocalTestKeyProvider{MemoryKeyProvider: keys.NewMemoryKeyProvider()}
}

// realModeProvider is the capability interface a REAL-mode
// keys.KeyProvider satisfies -- AWSKMSProvider in this package is the
// one implementation. It mirrors the Mode() string capability already
// used throughout pkg/blockers (supplychain.VulnerabilityProvider,
// dr's region provider, livedata.FeedConnector) so the
// never-silently-run-REAL-in-a-fixture-path invariant is enforced the
// same way everywhere in this codebase. keys.KeyProvider implementations
// that declare no Mode() method -- keys.MemoryKeyProvider,
// keys.FileKeyProvider, LocalTestKeyProvider -- are, by construction,
// never REAL, so RefuseRealProvider is a no-op for them.
type realModeProvider interface {
	Mode() string
}

// RefuseRealProvider returns a non-nil error if p is a REAL-mode
// keys.KeyProvider. RunFailureMatrix above never needs to call this
// itself -- it only ever constructs keys.MemoryKeyProvider and
// FailingKeyProvider internally, neither of which can be REAL -- but
// any future call site that accepts a caller-supplied keys.KeyProvider
// into a fixture qualification path (rather than
// pkg/governance/qualification, where real evidence belongs) must call
// this first and refuse to proceed on a non-nil error, exactly as
// supplychain.RunQualification and dr.RunQualification already do for
// their own REAL-mode providers.
func RefuseRealProvider(p keys.KeyProvider) error {
	if rp, ok := p.(realModeProvider); ok && rp.Mode() == "REAL" {
		return errors.New("hsmkms: refusing a REAL-mode key provider in a fixture qualification path; submit that evidence through pkg/governance/qualification instead")
	}
	return nil
}
