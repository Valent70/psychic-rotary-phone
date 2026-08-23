package rwc

import (
	"context"
	"testing"

	"veriqo/veriqo/kernel"
)

// TestRWC002ProvenanceSeparation runs every RWC-002 claim category
// through the real native path and checks the provenance status
// EMERGES correctly from what was actually submitted: the vessel
// identity claim has a real independent (algorithmic) second source and
// should reach CORROBORATED; every other claim category is single-
// source-only (broker-supplied, no reachable independent corroboration
// in this environment — see baseline doc §1) and must NOT be reported as
// CORROBORATED or verified truth, per the brief's explicit §5
// instruction ("Do NOT convert broker statements into verified facts").
func TestRWC002ProvenanceSeparation(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown()
	ctx := context.Background()

	t.Run("vessel_identity_corroborated_by_structural_check", func(t *testing.T) {
		req, notes, err := BuildRWC002VesselIdentityCase(1)
		if err != nil {
			t.Fatalf("BuildRWC002VesselIdentityCase: %v", err)
		}
		res, err := Run(ctx, k, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		status := ClassifyProvenance(res.Lifecycle.Canonical, len(req.Submissions))
		if status != StatusCorroborated {
			t.Errorf("got provenance=%s, want CORROBORATED (notes=%v decision=%s)", status, notes, res.DecisionAction)
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
			status := ClassifyProvenance(res.Lifecycle.Canonical, len(req.Submissions))
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
		status := ClassifyProvenance(res.Lifecycle.Canonical, len(req.Submissions))
		if status == StatusCorroborated {
			t.Errorf("single-source transaction claim must never be reported CORROBORATED, got %s", status)
		}
	})
}
