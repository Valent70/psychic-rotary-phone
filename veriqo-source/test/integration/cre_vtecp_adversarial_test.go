// Negative/adversarial tests for the CRE + VTECP-001 work this round.
// Each proves a specific refusal the "6 MUST NOT" list (never declare a
// legally binding verdict, never invent evidence, never convert
// uncertainty into certainty, never claim integrity-check-passed means
// factually true, never claim automatic court-admissibility, plus
// VTECP-001's own "AI never becomes evidence authority") actually holds
// under adversarial pressure -- not merely documented as an intention.
package integration

import (
	"errors"
	"testing"

	"veriqo/pkg/authz"
	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
	"veriqo/pkg/ontology"
	"veriqo/pkg/platform/audit"
)

// TestAdversarial_UnprovenHypothesisCanNeverReachFinding proves MUST-NOT
// "never convert uncertainty into certainty": a hypothesis with zero
// evidence cannot be forced into a Finding through the mechanical
// engine, no matter how the caller tries.
func TestAdversarial_UnprovenHypothesisCanNeverReachFinding(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-adv-1", "claim-adv-1", "question")
	if err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H1", Description: "a theory with no evidence at all"}); err != nil {
		t.Fatal(err)
	}
	if got := cre.CandidateHypotheses(hs); len(got) != 0 {
		t.Fatalf("expected zero candidates for a zero-evidence hypothesis, got %v", got)
	}
	dg := evidence.NewDependencyGraph()
	findings, err := cre.GenerateFindings(hs, dg, cre.FindingInput{CaseID: "case-adv-1"}, "adv1", 1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(findings) != 0 {
		t.Fatal("expected zero findings from an entirely unevidenced hypothesis set")
	}
}

// TestAdversarial_HandForgedFindingCaughtByHypothesisReverification proves
// that even a caller who bypasses BuildFinding entirely and hand-crafts a
// finding.Finding (recomputing its own hash to stay internally
// consistent, exactly as a real attacker capable of forging one would)
// is still caught, because VerifyFindingAgainstHypothesis re-derives the
// truth from the real HypothesisSet rather than trusting the Finding.
func TestAdversarial_HandForgedFindingCaughtByHypothesisReverification(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-adv-2", "claim-adv-2", "question")
	if err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H1", Description: "genuinely unproven"}); err != nil {
		t.Fatal(err)
	}
	forged := finding.Finding{
		FindingID: "forged-1", CaseID: "case-adv-2",
		SupportedBy: []string{"ev-fabricated-1", "ev-fabricated-2"}, ContradictionsConsidered: true,
		ContractBasis: "clause-1", ObligationRef: "obl-1", EventRef: "event-1",
		Causation: "This was definitely caused by X.", QuantumRef: "calc-1",
		ConfidenceBasis: causation.StatusSupported, // fabricated: H1 is actually UNPROVEN
		Alternatives:    nil, AlternativesConsidered: true,
		HumanReviewRequired: false, HumanReviewDecided: true, Tick: 1,
	}
	forged = finding.Evaluate(forged)
	if forged.Status != finding.StatusFinding {
		t.Fatalf("expected the hand-forged Finding to structurally reach FINDING status (it has every field populated) -- got %s; the point of this test is that reaching FINDING status alone is NOT sufficient proof", forged.Status)
	}
	if err := cre.VerifyFindingAgainstHypothesis(forged, hs, "H1"); !errors.Is(err, cre.ErrFindingDoesNotMatchHypothesis) {
		t.Fatalf("expected the forged Finding to be caught by re-derivation against the real hypothesis, got %v", err)
	}
}

// TestAdversarial_InferenceFromNonActiveModelNeverEntersAuditTrail proves
// VTECP-001's core Capability 5 rule: AI output never becomes evidence
// authority. A model still in DRAFT (or DEPRECATED/RETIRED) cannot have
// its output recorded as a trusted inference at all -- not "recorded but
// flagged," genuinely refused.
func TestAdversarial_InferenceFromNonActiveModelNeverEntersAuditTrail(t *testing.T) {
	reg := lifecycle.NewRegistry()
	ev, err := reg.RegisterModel(lifecycle.Model{ModelID: "shadow-model", Version: "0.1", Type: "x"}, "attacker", 1)
	if err != nil {
		t.Fatal(err)
	}
	ledger := audit.NewAuditStore()
	recorder := inference.NewRecorder(reg)
	recorder.AttachAuditStore(ledger)
	_, err = recorder.Record(ev.Key, "inputhash", "a confident-sounding but ungoverned answer", 0.99, "attacker", "", 1)
	if !errors.Is(err, inference.ErrModelNotActive) {
		t.Fatalf("expected ErrModelNotActive, got %v", err)
	}
	if len(ledger.Snapshot()) != 0 {
		t.Fatal("expected NOTHING to be mirrored to the audit ledger for a refused, ungoverned inference")
	}
}

// TestAdversarial_NoDeclaredPurposeIsDeniedEvenWithCorrectRole proves
// Purpose Binding cannot be bypassed merely by holding the right role:
// RBAC alone is not sufficient once a rule also binds a purpose.
func TestAdversarial_NoDeclaredPurposeIsDeniedEvenWithCorrectRole(t *testing.T) {
	e := authz.NewEngine()
	published, err := e.Publish(authz.Document{ID: "p", Rules: []authz.Rule{
		{ID: "r1", Effect: authz.Allow, Roles: []string{"claims_analyst"}, Actions: []string{"read"},
			Resources: []string{"case/*"}, Purposes: []string{"case_resolution"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Activate(published.Version); err != nil {
		t.Fatal(err)
	}
	// Correct role, correct action/resource, WRONG purpose.
	ex, err := e.Can(authz.Request{Actor: "insider", Roles: []string{"claims_analyst"}, Action: "read",
		Resource: "case/1", Purpose: "curiosity"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Allowed {
		t.Fatal("expected a request with the correct role but the wrong declared purpose to be denied")
	}
}

// TestAdversarial_OntologyRefusesActionOnPolicyDenial proves the
// ontology's mandated 5-stage pipeline (Policy -> Validation ->
// Execution -> State Transition -> Audit) genuinely stops at the policy
// stage: neither the object mutation NOR the audit entry happens when
// policy denies.
func TestAdversarial_OntologyRefusesActionOnPolicyDenial(t *testing.T) {
	reg := ontology.NewRegistry()
	ledger := audit.NewAuditStore()
	reg.AttachAuditStore(ledger)
	deny := func() error { return errors.New("insufficient clearance") }
	_, err := reg.CreateObject(ontology.Object{ObjectType: ontology.ObjectCase, ObjectID: "case-x", TenantID: "t1"},
		"attacker", 1, deny)
	if !errors.Is(err, ontology.ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}
	if _, ok := reg.Get(ontology.ObjectCase, "case-x", "t1"); ok {
		t.Fatal("expected the object to NOT exist after a policy-denied CreateObject")
	}
	if len(ledger.Snapshot()) != 0 {
		t.Fatal("expected no audit entry for a policy-denied action")
	}
}

// TestAdversarial_TamperedAuditLedgerDetectedAcrossSubsystems proves
// tampering ANY single record in a ledger fed by multiple subsystems
// (not just one package's own isolated ledger) is still caught.
func TestAdversarial_TamperedAuditLedgerDetectedAcrossSubsystems(t *testing.T) {
	ledger := audit.NewAuditStore()
	ont := ontology.NewRegistry()
	ont.AttachAuditStore(ledger)
	if _, err := ont.CreateObject(ontology.Object{ObjectType: ontology.ObjectCase, ObjectID: "c1", TenantID: "t1"}, "a", 1, nil); err != nil {
		t.Fatal(err)
	}
	az := authz.NewEngine()
	az.AttachAuditStore(ledger)
	pub, err := az.Publish(authz.Document{ID: "p", Rules: []authz.Rule{
		{ID: "r1", Effect: authz.Allow, Actions: []string{"*"}, Resources: []string{"*"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := az.Activate(pub.Version); err != nil {
		t.Fatal(err)
	}
	if _, _, err := az.CanRecorded(authz.Request{Actor: "a", Action: "read", Resource: "case/1"}); err != nil {
		t.Fatal(err)
	}

	records := ledger.Snapshot()
	if len(records) != 2 {
		t.Fatalf("expected 2 records across the two subsystems, got %d", len(records))
	}
	records[0].Payload = "tampered payload" // tamper the ontology-originated record
	if err := (audit.Auditor{}).VerifyChain(records); err == nil {
		t.Fatal("expected tampering a record from ONE subsystem to break the shared ledger's verification")
	}
}
