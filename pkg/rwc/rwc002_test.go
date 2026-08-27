package rwc

import (
	"context"
	"testing"

	"veriqo/veriqo/kernel"
)

// TestRWC002ProvenanceSeparation runs every RWC-002 claim category
// through the real native path and checks the provenance status EMERGES
// correctly from what was actually submitted. No broker statement may
// become a verified fact, and — corrected in round R23 — no offline
// arithmetic over a claimed identifier may become corroboration of it.
func TestRWC002ProvenanceSeparation(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown() //nolint:errcheck // test teardown
	ctx := context.Background()

	// This subtest previously asserted StatusCorroborated and was named
	// "vessel_identity_corroborated_by_structural_check" — a name that
	// states the error outright once you read it slowly: a structural
	// check is not corroboration. The R23 audit corrected both the
	// classifier and this assertion. The test now pins the honest status
	// AND explicitly forbids the old one, so a regression toward
	// overclaiming fails rather than passing quietly.
	t.Run("vessel_identity_is_structurally_validated_not_corroborated", func(t *testing.T) {
		req, notes, err := BuildRWC002VesselIdentityCase(1)
		if err != nil {
			t.Fatalf("BuildRWC002VesselIdentityCase: %v", err)
		}
		res, err := Run(ctx, k, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		status := ClassifyProvenance(res.Lifecycle.Canonical, req.Submissions)
		if status == StatusCorroborated {
			t.Fatalf("vessel identity reported CORROBORATED. Nothing outside the claim was "+
				"consulted: the second source is an IMO check digit computed from the claimed "+
				"IMO number itself (notes=%v)", notes)
		}
		if status != StatusStructurallyValidated {
			t.Errorf("got provenance=%s, want STRUCTURALLY_VALIDATED (notes=%v decision=%s)",
				status, notes, res.DecisionAction)
		}
		// The reason this is not corroboration, asserted against the
		// native engine's own output rather than restated in prose: the
		// provenance assessment for this case never reached
		// DECLARED_INDEPENDENT. It is UNKNOWN — "no source declared any
		// ancestry, so there was nothing to check" — while its Score is a
		// trivial 1.0. Reading that Score without that Status is exactly
		// the bug R23 removed.
		if got := res.Lifecycle.Canonical.Provenance.Status; got == "DECLARED_INDEPENDENT" {
			t.Errorf("provenance status is %s; if the engine now genuinely assesses these two "+
				"sources as independent, the CORROBORATED rule needs re-examining rather than "+
				"this test being relaxed", got)
		}
	})

	singleSourceCases := map[string]func() CaseRequest{
		"voyage_position":    func() CaseRequest { return BuildRWC002VoyagePositionCase(1) },
		"cargo_identity":     func() CaseRequest { return BuildRWC002CargoIdentityCase(1) },
		"document_existence": func() CaseRequest { return BuildRWC002DocumentExistenceCase(1) },
	}
	for name, build := range singleSourceCases {
		name, build := name, build
		t.Run(name+"_unverified_not_corroborated", func(t *testing.T) {
			req := build()
			res, err := Run(ctx, k, req)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			status := ClassifyProvenance(res.Lifecycle.Canonical, req.Submissions)
			if status == StatusCorroborated {
				t.Errorf("single-source broker claim %q must never be reported CORROBORATED, got %s", name, status)
			}
			if status != StatusUnverified {
				t.Errorf("got provenance=%s, want UNVERIFIED (single uncorroborated broker source)", status)
			}
		})
	}

	t.Run("transaction_sequence_redflags_detected", func(t *testing.T) {
		req, matched := BuildRWC002TransactionCase(1)
		if len(matched) == 0 {
			t.Fatalf("expected at least one red-flag phrase match from the corpus's own supplied claim text")
		}
		res, err := Run(ctx, k, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.RiskScore <= 0 {
			t.Errorf("expected non-zero native risk score once red-flag phrases were matched, got %.4f", res.RiskScore)
		}
		status := ClassifyProvenance(res.Lifecycle.Canonical, req.Submissions)
		if status == StatusCorroborated {
			t.Errorf("single-source transaction claim must never be reported CORROBORATED, got %s", status)
		}
	})
}
