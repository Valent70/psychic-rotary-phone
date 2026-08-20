package hsmkms

import (
	"context"
	"errors"
	"testing"

	"veriqo/pkg/blockers"
	"veriqo/pkg/platform/security/keys"
)

func goodContract() *blockers.Contract {
	c := &blockers.Contract{
		ID: "hsm_kms", Name: "HSM/KMS production key custody", Version: "v1",
		RequiredCapabilities:      []string{"key_provider_abstraction", "fail_closed_enforcement"},
		RealWorldDependencies:     []string{"procured_hsm_or_cloud_kms_tenancy"},
		TestProfile:               blockers.TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements:      []string{"failure_matrix_results"},
		FailureConditions:         []string{"silent_fallback_to_software_keys"},
		AcceptanceCriteria:        []string{"all_failure_modes_fail_closed"},
		RealQualificationRequired: true,
	}
	c.MarkImplemented()
	c.MarkIntegrationTested()
	return c
}

func TestRunFailureMatrixAllFailClosed(t *testing.T) {
	c := goodContract()
	result, err := RunFailureMatrix(c)
	if err != nil {
		t.Fatalf("RunFailureMatrix: %v (measurements=%v)", err, result.Measurements)
	}
	if !result.Pass {
		t.Fatalf("expected all failure modes to fail closed: %s", result.FailureReason)
	}
	for _, mode := range allFailModes {
		if got := result.Measurements[string(mode)]; got != "blocked=true" {
			t.Errorf("mode %s: expected blocked=true, got %q", mode, got)
		}
	}
	if result.Measurements["REVOKED"] != "blocked=true provider_touched=false" {
		t.Errorf("expected revoked key to never touch the provider, got %q", result.Measurements["REVOKED"])
	}
	if result.Measurements["PRODUCTION_GUARD"] != "blocked=true" {
		t.Errorf("expected production guard to hold, got %q", result.Measurements["PRODUCTION_GUARD"])
	}
	if c.Status != blockers.StatusQualificationTested {
		t.Fatalf("expected QUALIFICATION_TESTED, got %s", c.Status)
	}
}

func TestFailingKeyProviderInjectsConfiguredError(t *testing.T) {
	base := keys.NewMemoryKeyProvider()
	md, err := base.Generate("k1", keys.PurposeEvidence, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fp := &FailingKeyProvider{Inner: base, FailFor: map[string]FailMode{"k1": FailPermissionDenied}}
	mgr := keys.NewManager(fp)
	if err := mgr.Register(md); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Activate("k1"); err != nil {
		t.Fatal(err)
	}
	sig, err := mgr.Sign(context.Background(), "k1", []byte("d"), 1)
	if err == nil {
		t.Fatal("expected an injected permission-denied error")
	}
	if len(sig) != 0 {
		t.Fatal("expected no signature bytes on a failed sign")
	}
}

func TestFailingKeyProviderPassesThroughUnaffectedKeys(t *testing.T) {
	base := keys.NewMemoryKeyProvider()
	md, err := base.Generate("k-good", keys.PurposeEvidence, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fp := &FailingKeyProvider{Inner: base, FailFor: map[string]FailMode{"k-other": FailTimeout}}
	mgr := keys.NewManager(fp)
	if err := mgr.Register(md); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Activate("k-good"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Sign(context.Background(), "k-good", []byte("d"), 1); err != nil {
		t.Fatalf("expected an unaffected key to sign normally, got %v", err)
	}
}

func TestProductionGuardRejectsSoftwareProviders(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider keys.KeyProvider
	}{
		{"memory", keys.NewMemoryKeyProvider()},
		{"failing", &FailingKeyProvider{Inner: keys.NewMemoryKeyProvider()}},
	} {
		if err := keys.RequireProductionSafe(tc.provider, "production"); !errors.Is(err, keys.ErrSoftwareProviderInProduction) {
			t.Errorf("%s: expected ErrSoftwareProviderInProduction, got %v", tc.name, err)
		}
		if err := keys.RequireProductionSafe(tc.provider, "development"); err != nil {
			t.Errorf("%s: expected no error outside production, got %v", tc.name, err)
		}
	}
}

func TestRunFailureMatrixNewScenariosPass(t *testing.T) {
	c := goodContract()
	result, err := RunFailureMatrix(c)
	if err != nil {
		t.Fatalf("RunFailureMatrix: %v (measurements=%v)", err, result.Measurements)
	}
	if !result.Pass {
		t.Fatalf("expected all scenarios to pass: %s", result.FailureReason)
	}
	if got := result.Measurements[string(FailWrongAlgorithm)]; got != "blocked=true" {
		t.Errorf("WRONG_ALGORITHM (generic Sign-side injection): expected blocked=true, got %q", got)
	}
	if got := result.Measurements["WRONG_ALGORITHM_VERIFY"]; got != "blocked=true" {
		t.Errorf("WRONG_ALGORITHM_VERIFY: expected blocked=true, got %q", got)
	}
	if got := result.Measurements["TAMPERED_MANIFEST"]; got != "blocked=true" {
		t.Errorf("TAMPERED_MANIFEST: expected blocked=true, got %q", got)
	}
	for _, key := range []string{
		"rotation_old_signature_still_verifies",
		"rotation_new_signature_verifies",
		"rotation_retired_key_cannot_sign_new",
	} {
		if got := result.Measurements[key]; got != "ok=true" {
			t.Errorf("%s: expected ok=true, got %q", key, got)
		}
	}
}

// TestManagerVerifyFailsClosedOnWrongShapedSignature exercises
// keys.Manager.Verify directly (outside RunFailureMatrix) to prove the
// wrong-algorithm scenario's core claim in isolation: a signature of the
// wrong shape/length for ed25519 is rejected with ErrBadSignature, never
// a panic and never a false accept.
func TestManagerVerifyFailsClosedOnWrongShapedSignature(t *testing.T) {
	base := keys.NewMemoryKeyProvider()
	md, err := base.Generate("wrong-shape-key", keys.PurposeEvidence, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	mgr := keys.NewManager(base)
	if err := mgr.Register(md); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Activate("wrong-shape-key"); err != nil {
		t.Fatal(err)
	}
	digest := []byte("some-digest")

	for name, sig := range map[string][]byte{
		"empty":              {},
		"too_short":          {0x01, 0x02, 0x03},
		"rsa2048_shaped_256": bytesN(256, 0xAB),
		"ecdsa_der_shaped":   bytesN(72, 0x30),
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Verify panicked on a %s-shaped signature: %v", name, r)
				}
			}()
			if err := mgr.Verify(context.Background(), "wrong-shape-key", digest, sig); !errors.Is(err, keys.ErrBadSignature) {
				t.Fatalf("expected ErrBadSignature for a %s-shaped signature, got %v", name, err)
			}
		})
	}
}

func bytesN(n int, b byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// TestLocalTestKeyProviderDelegatesToMemoryProvider proves
// LocalTestKeyProvider is a thin wrapper -- not a reimplementation -- by
// exercising Generate/Sign/PublicKey/SoftwareBacked through it exactly
// as a caller of the embedded keys.MemoryKeyProvider would.
func TestLocalTestKeyProviderDelegatesToMemoryProvider(t *testing.T) {
	p := NewLocalTestKeyProvider()
	if !p.SoftwareBacked() {
		t.Fatal("expected LocalTestKeyProvider to report SoftwareBacked() == true")
	}
	md, err := p.Generate("ltkp-key", keys.PurposeEvidence, 0, 0)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mgr := keys.NewManager(p)
	if err := mgr.Register(md); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Activate("ltkp-key"); err != nil {
		t.Fatal(err)
	}
	digest := []byte("ltkp-digest")
	sig, err := mgr.Sign(context.Background(), "ltkp-key", digest, 1)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := mgr.Verify(context.Background(), "ltkp-key", digest, sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := keys.RequireProductionSafe(p, "production"); !errors.Is(err, keys.ErrSoftwareProviderInProduction) {
		t.Fatalf("expected LocalTestKeyProvider to be refused in production, got %v", err)
	}
}

func TestRefuseRealProviderAcceptsLocalTestKeyProviderDirectly(t *testing.T) {
	if err := RefuseRealProvider(NewLocalTestKeyProvider()); err != nil {
		t.Fatalf("expected RefuseRealProvider to accept LocalTestKeyProvider, got %v", err)
	}
}

func TestProductionGuardAcceptsNonSoftwareProvider(t *testing.T) {
	var plain keys.KeyProvider = &touchTrackingProvider{}
	// touchTrackingProvider deliberately DOES implement SoftwareBacked
	// (it is itself a fixture), so use a provider that satisfies only
	// keys.KeyProvider to model what a real HSM/KMS adapter looks like.
	type bareProvider struct{ keys.KeyProvider }
	var real keys.KeyProvider = bareProvider{plain}
	if err := keys.RequireProductionSafe(real, "production"); err != nil {
		t.Fatalf("a provider with no SoftwareBacked marker must be accepted in production, got %v", err)
	}
}
