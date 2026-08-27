package spiffe

import (
	"context"
	"testing"
	"time"

	"veriqo/pkg/blockers"
)

func goodContract() *blockers.Contract {
	c := &blockers.Contract{
		ID: "spire_mtls", Name: "Production SPIRE/mTLS workload identity", Version: "v1",
		RequiredCapabilities:      []string{"spiffe_identity_abstraction", "svid_rotation"},
		RealWorldDependencies:     []string{"production_spire_cluster"},
		TestProfile:               blockers.TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements:      []string{"attestation_log", "rotation_log"},
		FailureConditions:         []string{"expired_svid_accepted", "untrusted_bundle_accepted"},
		AcceptanceCriteria:        []string{"all_peer_validation_rejects_untrusted_bundles"},
		RealQualificationRequired: true,
	}
	c.MarkImplemented()
	c.MarkIntegrationTested()
	return c
}

func TestRunQualificationFullMatrix(t *testing.T) {
	c := goodContract()
	result, err := RunQualification(c)
	if err != nil {
		t.Fatalf("RunQualification: %v (measurements=%v)", err, result.Measurements)
	}
	if !result.Pass {
		t.Fatalf("expected pass, got failure: %s (measurements=%v)", result.FailureReason, result.Measurements)
	}
	for _, key := range []string{
		"fetch_then_validate", "validate_peer_returns_actual_id", "rotation_observed_via_watch",
		"old_svid_still_structurally_valid_until_revoked", "expired_svid_rejected",
		"revoked_svid_rejected", "untrusted_bundle_rejected", "claimed_id_mismatch_rejected",
	} {
		if got := result.Measurements[key]; got != "blocked_or_passed=true" {
			t.Errorf("%s: expected true, got %q", key, got)
		}
	}
	if c.Status != blockers.StatusQualificationTested {
		t.Fatalf("expected QUALIFICATION_TESTED, got %s", c.Status)
	}
}

func TestValidateSVIDRejectsExpired(t *testing.T) {
	p, err := NewTestIdentityProvider("veriqo.test")
	if err != nil {
		t.Fatal(err)
	}
	svid, err := p.IssueWithTTL("spiffe://veriqo.test/w/a", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateSVID(context.Background(), svid); err == nil {
		t.Fatal("expected expired SVID to be rejected")
	}
}

func TestValidateSVIDRejectsForeignCA(t *testing.T) {
	local, err := NewTestIdentityProvider("veriqo.test")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := NewTestIdentityProvider("veriqo.test") // same trust domain string, different root key
	if err != nil {
		t.Fatal(err)
	}
	svid, err := foreign.IssueWithTTL("spiffe://veriqo.test/w/a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.ValidateSVID(context.Background(), svid); err == nil {
		t.Fatal("expected an SVID from an untrusted root to be rejected even with a matching trust domain name")
	}
}

func TestWatchRotationDeliversNewSVID(t *testing.T) {
	p, err := NewTestIdentityProvider("veriqo.test")
	if err != nil {
		t.Fatal(err)
	}
	id := "spiffe://veriqo.test/w/a"
	ctx := context.Background()
	if _, err := p.FetchSVID(ctx, id); err != nil {
		t.Fatal(err)
	}
	ch, cancel := p.WatchRotation(id)
	defer cancel()
	refreshed, err := p.RefreshSVID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch:
		if got.Serial != refreshed.Serial {
			t.Fatalf("watch delivered serial %s, expected %s", got.Serial, refreshed.Serial)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rotation notification")
	}
}

func TestCancelStopsWatching(t *testing.T) {
	p, err := NewTestIdentityProvider("veriqo.test")
	if err != nil {
		t.Fatal(err)
	}
	id := "spiffe://veriqo.test/w/a"
	ch, cancel := p.WatchRotation(id)
	cancel()
	if _, err := p.RefreshSVID(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Fatal("expected no notification after cancel")
	case <-time.After(100 * time.Millisecond):
	}
}
