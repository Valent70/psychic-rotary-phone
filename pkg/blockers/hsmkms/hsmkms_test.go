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
