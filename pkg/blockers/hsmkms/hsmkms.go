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
)

var allFailModes = []FailMode{FailUnavailable, FailTimeout, FailPermissionDenied, FailWrongKey}

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
