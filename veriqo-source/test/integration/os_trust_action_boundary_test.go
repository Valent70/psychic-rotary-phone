// This file answers the reviewer's item 17/G directly: "Decision ->
// Action Authorization Boundary." It extends buildOSTrustPipeline's
// already-proven Evidence -> Manifest -> Hypothesis -> Finding ->
// AuthorizedFinding -> Decision -> Ledger chain ONE step further --
// Decision -> ActionAuthorization -> AuthorizeExecution -> Ledger --
// system-level, using the SAME real ledger the Decision itself was
// anchored to, proving "trusted Decision -> arbitrary action" is
// structurally impossible end to end, not just inside
// pkg/insurance/action's own package tests.
package integration

import (
	"testing"

	"veriqo/pkg/insurance/action"
	"veriqo/pkg/platform/audit"
)

// TestOSTrustDecisionToActionAuthorizationBoundary drives the full,
// real chain from Evidence all the way to a permitted, executed
// downstream action, on the SAME ledger the Decision itself was
// recorded to -- then proves four independent bypass attempts (wrong
// actor, wrong action, wrong scope, expired authorization) are each
// refused, with no ACTION_EXECUTED entry ever appearing on the ledger
// for a refused attempt.
func TestOSTrustDecisionToActionAuthorizationBoundary(t *testing.T) {
	result := buildOSTrustPipeline(t, "EV-ACTION-1", "case-action-1", 40)

	aa, err := action.AuthorizeAction(result.d, "adjuster-os-trust", "policy-settlement-v1", "case-action-1",
		action.ActionApproveSettlement, []string{"reinspection_complete"}, 40, 50)
	if err != nil {
		t.Fatalf("AuthorizeAction: %v", err)
	}
	if err := action.VerifyActionAuthorization(aa, result.d); err != nil {
		t.Fatalf("VerifyActionAuthorization: %v", err)
	}

	authRec, err := action.AppendToLedger(result.ledger, "adjuster-os-trust", aa)
	if err != nil {
		t.Fatalf("AppendToLedger: %v", err)
	}
	if authRec.Action != "ACTION_AUTHORIZATION_RECORDED" {
		t.Fatalf("expected ACTION_AUTHORIZATION_RECORDED, got %s", authRec.Action)
	}

	t.Run("bypass_wrong_actor_refused_and_unledgered", func(t *testing.T) {
		before := len(result.ledger.Snapshot())
		if err := action.AuthorizeExecution(aa, result.d, "attacker", action.ActionApproveSettlement, "case-action-1", 45); err == nil {
			t.Fatal("expected the wrong-actor attempt to be refused")
		}
		if len(result.ledger.Snapshot()) != before {
			t.Fatalf("expected no new ledger record for a refused execution attempt, before=%d after=%d", before, len(result.ledger.Snapshot()))
		}
	})

	t.Run("bypass_wrong_action_refused_and_unledgered", func(t *testing.T) {
		before := len(result.ledger.Snapshot())
		if err := action.AuthorizeExecution(aa, result.d, "adjuster-os-trust", action.ActionInitiateTradeFinance, "case-action-1", 45); err == nil {
			t.Fatal("expected the wrong-action attempt to be refused")
		}
		if len(result.ledger.Snapshot()) != before {
			t.Fatalf("expected no new ledger record for a refused execution attempt, before=%d after=%d", before, len(result.ledger.Snapshot()))
		}
	})

	t.Run("bypass_wrong_scope_refused_and_unledgered", func(t *testing.T) {
		before := len(result.ledger.Snapshot())
		if err := action.AuthorizeExecution(aa, result.d, "adjuster-os-trust", action.ActionApproveSettlement, "case-action-DIFFERENT", 45); err == nil {
			t.Fatal("expected the wrong-scope attempt to be refused")
		}
		if len(result.ledger.Snapshot()) != before {
			t.Fatalf("expected no new ledger record for a refused execution attempt, before=%d after=%d", before, len(result.ledger.Snapshot()))
		}
	})

	t.Run("bypass_expired_authorization_refused_and_unledgered", func(t *testing.T) {
		before := len(result.ledger.Snapshot())
		if err := action.AuthorizeExecution(aa, result.d, "adjuster-os-trust", action.ActionApproveSettlement, "case-action-1", 50); err == nil {
			t.Fatal("expected the expired-authorization attempt to be refused")
		}
		if len(result.ledger.Snapshot()) != before {
			t.Fatalf("expected no new ledger record for a refused execution attempt, before=%d after=%d", before, len(result.ledger.Snapshot()))
		}
	})

	// The one legitimate path: matching actor, action, scope, and
	// still within expiry -- only NOW does execution reach the ledger.
	if err := action.AuthorizeExecution(aa, result.d, "adjuster-os-trust", action.ActionApproveSettlement, "case-action-1", 45); err != nil {
		t.Fatalf("expected the legitimate execution attempt to be authorized: %v", err)
	}
	execRec, err := action.AppendExecutionToLedger(result.ledger, "adjuster-os-trust", aa, 45)
	if err != nil {
		t.Fatalf("AppendExecutionToLedger: %v", err)
	}
	if execRec.Action != "ACTION_EXECUTED" {
		t.Fatalf("expected ACTION_EXECUTED, got %s", execRec.Action)
	}

	// Final, whole-chain integrity check: DECISION_RECORDED ->
	// ACTION_AUTHORIZATION_RECORDED -> ACTION_EXECUTED, all on the
	// SAME hash-chained ledger, and nothing else -- every one of the
	// four bypass attempts above left no trace.
	recs := result.ledger.Snapshot()
	if len(recs) != 3 {
		t.Fatalf("expected exactly 3 ledger records (decision, authorization, execution), got %d: %+v", len(recs), recs)
	}
	wantActions := []string{"DECISION_RECORDED", "ACTION_AUTHORIZATION_RECORDED", "ACTION_EXECUTED"}
	for i, want := range wantActions {
		if recs[i].Action != want {
			t.Fatalf("record %d: expected action %q, got %q", i, want, recs[i].Action)
		}
	}
	if err := (audit.Auditor{}).VerifyChain(recs); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}
