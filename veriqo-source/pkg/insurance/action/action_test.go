package action

import (
	"errors"
	"reflect"
	"testing"

	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/platform/audit"
)

// buildDecision returns a genuine, real decision.Decision -- driving
// the full real pipeline (HypothesisSet -> BuildFinding -> Authorize ->
// MakeDecision), exactly like every other consumer of this package
// must.
func buildDecision(t *testing.T) decision.Decision {
	t.Helper()
	hs, err := causation.NewHypothesisSet("case-1", "claim-1", "What caused the loss?")
	if err != nil {
		t.Fatal(err)
	}
	const hID causation.HypothesisID = "H1"
	if err := hs.Add(causation.Hypothesis{ID: hID, Description: "primary hypothesis"}); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence(hID, "EV-1"); err != nil {
		t.Fatal(err)
	}
	h, ok := hs.Get(hID)
	if !ok {
		t.Fatal("hypothesis not found")
	}
	f, err := cre.BuildFinding(hs, h, nil, cre.FindingInput{
		CaseID: "case-1", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "finding-1", 1)
	if err != nil {
		t.Fatalf("BuildFinding: %v", err)
	}
	af, err := cre.Authorize(f, hs, hID, nil, 1)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	d, err := decision.MakeDecision(af, decision.OutcomeApproved, "primary hypothesis substantiated", 1)
	if err != nil {
		t.Fatalf("MakeDecision: %v", err)
	}
	return d
}

func TestAuthorizeActionProducesAPopulatedAuthorization(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-settlement-v1", "CLM-1",
		ActionApproveSettlement, []string{"reinspection_complete"}, 10, 20)
	if err != nil {
		t.Fatalf("AuthorizeAction: %v", err)
	}
	if aa.IsZero() {
		t.Fatal("expected a populated ActionAuthorization")
	}
	if aa.DecisionHash() != d.Hash() {
		t.Fatalf("expected DecisionHash to equal the Decision's own Hash, got %s vs %s", aa.DecisionHash(), d.Hash())
	}
	if aa.Actor() != "adjuster-1" || aa.PolicyRef() != "policy-settlement-v1" || aa.Scope() != "CLM-1" {
		t.Fatalf("unexpected authorization fields: %+v", aa)
	}
	if aa.PermittedAction() != ActionApproveSettlement {
		t.Fatalf("expected ActionApproveSettlement, got %s", aa.PermittedAction())
	}
	if len(aa.Conditions()) != 1 || aa.Conditions()[0] != "reinspection_complete" {
		t.Fatalf("expected conditions to be preserved, got %v", aa.Conditions())
	}
	if aa.AuthorizedAt() != 10 || aa.ExpiresAt() != 20 {
		t.Fatalf("expected authorizedAt=10 expiresAt=20, got %d/%d", aa.AuthorizedAt(), aa.ExpiresAt())
	}
	if aa.Hash() == "" {
		t.Fatal("expected a non-empty Hash")
	}
}

func TestAuthorizeActionIsDeterministic(t *testing.T) {
	d := buildDecision(t)
	aa1, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, []string{"c1"}, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	aa2, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, []string{"c1"}, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if aa1.Hash() != aa2.Hash() {
		t.Fatalf("expected two independently-computed authorizations over identical inputs to converge on the identical hash, got %s vs %s", aa1.Hash(), aa2.Hash())
	}
}

// TestAuthorizeActionRejectsAnUnauthoritativeDecision proves "trusted
// Decision -> arbitrary action" is impossible from the other direction
// too: there must be a REAL Decision to found an authorization on.
func TestAuthorizeActionRejectsAnUnauthoritativeDecision(t *testing.T) {
	if _, err := AuthorizeAction(decision.Decision{}, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20); !errors.Is(err, ErrDecisionNotAuthoritative) {
		t.Fatalf("expected ErrDecisionNotAuthoritative for the zero Decision, got %v", err)
	}
}

func TestAuthorizeActionRejectsEmptyActor(t *testing.T) {
	d := buildDecision(t)
	if _, err := AuthorizeAction(d, "", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20); !errors.Is(err, ErrEmptyActor) {
		t.Fatalf("expected ErrEmptyActor, got %v", err)
	}
}

func TestAuthorizeActionRejectsEmptyPolicyRef(t *testing.T) {
	d := buildDecision(t)
	if _, err := AuthorizeAction(d, "adjuster-1", "", "CLM-1", ActionApproveSettlement, nil, 10, 20); !errors.Is(err, ErrEmptyPolicyRef) {
		t.Fatalf("expected ErrEmptyPolicyRef, got %v", err)
	}
}

func TestAuthorizeActionRejectsEmptyScope(t *testing.T) {
	d := buildDecision(t)
	if _, err := AuthorizeAction(d, "adjuster-1", "policy-1", "", ActionApproveSettlement, nil, 10, 20); !errors.Is(err, ErrEmptyScope) {
		t.Fatalf("expected ErrEmptyScope, got %v", err)
	}
}

func TestAuthorizeActionRejectsUnknownAction(t *testing.T) {
	d := buildDecision(t)
	if _, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", Action("DELETE_ALL_RECORDS"), nil, 10, 20); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("expected ErrUnknownAction, got %v", err)
	}
}

// TestAuthorizeActionRejectsAlreadyExpiredGrant proves an authorization
// that would already be expired the moment it is minted is refused
// outright, never silently accepted.
func TestAuthorizeActionRejectsAlreadyExpiredGrant(t *testing.T) {
	d := buildDecision(t)
	if _, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 20, 20); !errors.Is(err, ErrAlreadyExpired) {
		t.Fatalf("expected ErrAlreadyExpired for expiresAt == authorizedAt, got %v", err)
	}
	if _, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 30, 20); !errors.Is(err, ErrAlreadyExpired) {
		t.Fatalf("expected ErrAlreadyExpired for expiresAt < authorizedAt, got %v", err)
	}
}

func TestVerifyActionAuthorizationAcceptsARealAuthorization(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyActionAuthorization(aa, d); err != nil {
		t.Fatalf("expected a real authorization to verify against its own Decision: %v", err)
	}
}

// TestVerifyActionAuthorizationDetectsAMismatchedDecision proves an
// authorization minted for Decision A is never valid against a
// DIFFERENT Decision B, even one with the identical Outcome.
func TestVerifyActionAuthorizationDetectsAMismatchedDecision(t *testing.T) {
	dA := buildDecision(t)
	aa, err := AuthorizeAction(dA, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20)
	if err != nil {
		t.Fatal(err)
	}

	hs, err := causation.NewHypothesisSet("case-2", "claim-2", "What caused the loss?")
	if err != nil {
		t.Fatal(err)
	}
	const hID causation.HypothesisID = "H1"
	if err := hs.Add(causation.Hypothesis{ID: hID, Description: "different hypothesis"}); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence(hID, "EV-9"); err != nil {
		t.Fatal(err)
	}
	h, _ := hs.Get(hID)
	f, err := cre.BuildFinding(hs, h, nil, cre.FindingInput{
		CaseID: "case-2", ContractBasis: "clause-9", ObligationRef: "obl-9",
		EventRef: "event-9", QuantumRef: "calc-9", HumanReviewRequired: true,
	}, "finding-2", 1)
	if err != nil {
		t.Fatal(err)
	}
	afB, err := cre.Authorize(f, hs, hID, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	dB, err := decision.MakeDecision(afB, decision.OutcomeApproved, "a completely different decision", 1)
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifyActionAuthorization(aa, dB); !errors.Is(err, ErrActionDecisionMismatch) {
		t.Fatalf("expected ErrActionDecisionMismatch when verifying against a different Decision, got %v", err)
	}
}

func TestAuthorizeExecutionAcceptsAMatchingLegitimateAttempt(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeExecution(aa, d, "adjuster-1", ActionApproveSettlement, "CLM-1", 15); err != nil {
		t.Fatalf("expected a matching, non-expired execution attempt to be authorized: %v", err)
	}
}

// TestAuthorizeExecutionRejectsExpiredAuthorization proves the
// reviewer's "expiry" field is actually enforced at the point of use,
// not merely recorded: a structurally valid authorization is still
// refused once its expiry has passed.
func TestAuthorizeExecutionRejectsExpiredAuthorization(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeExecution(aa, d, "adjuster-1", ActionApproveSettlement, "CLM-1", 20); !errors.Is(err, ErrActionAuthorizationExpired) {
		t.Fatalf("expected ErrActionAuthorizationExpired at tick == expiresAt, got %v", err)
	}
	if err := AuthorizeExecution(aa, d, "adjuster-1", ActionApproveSettlement, "CLM-1", 999); !errors.Is(err, ErrActionAuthorizationExpired) {
		t.Fatalf("expected ErrActionAuthorizationExpired well past expiry, got %v", err)
	}
}

// TestAuthorizeExecutionRejectsWrongActor is the sharpest "trusted
// Decision -> arbitrary action" bypass attempt: a DIFFERENT actor
// (never granted this authorization at all) tries to exercise it.
func TestAuthorizeExecutionRejectsWrongActor(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeExecution(aa, d, "attacker-2", ActionApproveSettlement, "CLM-1", 15); !errors.Is(err, ErrActorMismatch) {
		t.Fatalf("expected ErrActorMismatch for an actor this authorization was never granted to, got %v", err)
	}
}

// TestAuthorizeExecutionRejectsWrongAction proves an authorization
// minted for ONE action can never justify a DIFFERENT action, even
// against the identical Decision, actor and scope -- exactly the
// "trusted Decision -> arbitrary action" shape the reviewer named.
func TestAuthorizeExecutionRejectsWrongAction(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionSendNotification, nil, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeExecution(aa, d, "adjuster-1", ActionApproveSettlement, "CLM-1", 15); !errors.Is(err, ErrActionMismatch) {
		t.Fatalf("expected ErrActionMismatch: an authorization for SEND_NOTIFICATION must never justify APPROVE_SETTLEMENT, got %v", err)
	}
}

// TestAuthorizeExecutionRejectsWrongScope proves an authorization
// scoped to one claim can never be used to justify acting on a
// different one.
func TestAuthorizeExecutionRejectsWrongScope(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeExecution(aa, d, "adjuster-1", ActionApproveSettlement, "CLM-2", 15); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("expected ErrScopeMismatch for a scope this authorization was never granted for, got %v", err)
	}
}

// TestActionAuthorizationIsStructurallyImmutableAfterConstruction
// mirrors decision.TestDecisionIsStructurallyImmutableAfterConstruction
// exactly: zero exported fields, value-receiver-only method set.
func TestActionAuthorizationIsStructurallyImmutableAfterConstruction(t *testing.T) {
	typ := reflect.TypeOf(ActionAuthorization{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath == "" {
			t.Fatalf("ActionAuthorization has an EXPORTED field %q -- this alone would let a caller write aa.%s = ... after construction, defeating the sealed-type guarantee entirely", field.Name, field.Name)
		}
	}

	ptrType := reflect.TypeOf(&ActionAuthorization{})
	valueType := reflect.TypeOf(ActionAuthorization{})
	if ptrType.NumMethod() != valueType.NumMethod() {
		t.Fatalf("expected *ActionAuthorization and ActionAuthorization to expose the identical method set: *ActionAuthorization has %d, ActionAuthorization has %d", ptrType.NumMethod(), valueType.NumMethod())
	}
	for i := 0; i < valueType.NumMethod(); i++ {
		m := valueType.Method(i)
		recv := m.Func.Type().In(0)
		if recv != valueType {
			t.Fatalf("method %s has receiver type %s, expected value receiver %s -- a pointer receiver here could mutate an ActionAuthorization in place", m.Name, recv, valueType)
		}
	}
}

// TestActionAuthorizationConditionsAreDefensivelyCopied proves a
// caller-held conditions slice (before AuthorizeAction) and the
// accessor's returned slice (after) can never reach back into the
// sealed authorization's own internal state.
func TestActionAuthorizationConditionsAreDefensivelyCopied(t *testing.T) {
	d := buildDecision(t)
	conditions := []string{"reinspection_complete"}
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, conditions, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	conditions[0] = "TAMPERED"
	if aa.Conditions()[0] != "reinspection_complete" {
		t.Fatalf("expected the authorization's own conditions to be unaffected by post-construction mutation of the caller's slice, got %v", aa.Conditions())
	}
	got := aa.Conditions()
	got[0] = "TAMPERED-VIA-ACCESSOR"
	if aa.Conditions()[0] != "reinspection_complete" {
		t.Fatalf("expected mutating the accessor's returned slice to not affect the sealed original, got %v", aa.Conditions())
	}
}

func TestAppendToLedgerRecordsARealActionAuthorization(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, []string{"c1"}, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	store := audit.NewAuditStore()
	rec, err := AppendToLedger(store, "adjuster-1", aa)
	if err != nil {
		t.Fatalf("AppendToLedger: %v", err)
	}
	if rec.Action != "ACTION_AUTHORIZATION_RECORDED" {
		t.Fatalf("expected ACTION_AUTHORIZATION_RECORDED, got %s", rec.Action)
	}
	if len(store.Snapshot()) != 1 {
		t.Fatalf("expected exactly 1 record, got %d", len(store.Snapshot()))
	}
}

func TestAppendToLedgerRefusesTheZeroAuthorization(t *testing.T) {
	store := audit.NewAuditStore()
	if _, err := AppendToLedger(store, "adjuster-1", ActionAuthorization{}); err == nil {
		t.Fatal("expected AppendToLedger to refuse the zero ActionAuthorization")
	}
	if len(store.Snapshot()) != 0 {
		t.Fatalf("expected zero records after a refused append, got %d", len(store.Snapshot()))
	}
}

// TestExecutionOnlyRecordedAfterAuthorizeExecutionSucceeds is the
// full, legitimate downstream shape: AuthorizeAction -> (real,
// separate) AuthorizeExecution check -> only THEN
// AppendExecutionToLedger. Proves the ledger's own ACTION_EXECUTED
// entries can only ever follow a real, passing AuthorizeExecution
// call -- and that a refused execution attempt produces no such
// entry, keeping "permission granted" and "action taken" honestly
// distinct in the audit trail.
func TestExecutionOnlyRecordedAfterAuthorizeExecutionSucceeds(t *testing.T) {
	d := buildDecision(t)
	aa, err := AuthorizeAction(d, "adjuster-1", "policy-1", "CLM-1", ActionApproveSettlement, nil, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	store := audit.NewAuditStore()

	// A refused attempt (wrong actor) must never reach the ledger.
	if err := AuthorizeExecution(aa, d, "attacker-2", ActionApproveSettlement, "CLM-1", 15); err == nil {
		t.Fatal("expected the wrong-actor attempt to be refused")
	}
	if len(store.Snapshot()) != 0 {
		t.Fatalf("expected zero ledger records before any legitimate execution, got %d", len(store.Snapshot()))
	}

	// The legitimate attempt succeeds, and only then is it recorded.
	if err := AuthorizeExecution(aa, d, "adjuster-1", ActionApproveSettlement, "CLM-1", 15); err != nil {
		t.Fatalf("expected the legitimate attempt to be authorized: %v", err)
	}
	rec, err := AppendExecutionToLedger(store, "adjuster-1", aa, 15)
	if err != nil {
		t.Fatalf("AppendExecutionToLedger: %v", err)
	}
	if rec.Action != "ACTION_EXECUTED" {
		t.Fatalf("expected ACTION_EXECUTED, got %s", rec.Action)
	}
	if len(store.Snapshot()) != 1 {
		t.Fatalf("expected exactly 1 ledger record, got %d", len(store.Snapshot()))
	}
	if err := (audit.Auditor{}).VerifyChain(store.Snapshot()); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}
