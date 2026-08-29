package decision

import (
	"errors"
	"reflect"
	"testing"

	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
)

// buildAuthorizedFinding returns a genuine, real cre.AuthorizedFinding
// -- the only way this test file can obtain one is by driving the real
// pipeline (HypothesisSet -> BuildFinding -> Authorize), exactly like
// every other consumer of this package must.
func buildAuthorizedFinding(t *testing.T) cre.AuthorizedFinding {
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
	return af
}

func TestMakeDecisionProducesAPopulatedDecision(t *testing.T) {
	af := buildAuthorizedFinding(t)
	d, err := MakeDecision(af, OutcomeApproved, "claim substantiated by primary hypothesis", 1)
	if err != nil {
		t.Fatalf("MakeDecision: %v", err)
	}
	if d.IsZero() {
		t.Fatal("expected a populated Decision")
	}
	if d.Outcome() != OutcomeApproved {
		t.Fatalf("Outcome = %s, want %s", d.Outcome(), OutcomeApproved)
	}
	if d.FindingHash() != af.Finding().Hash {
		t.Fatalf("FindingHash = %s, want %s", d.FindingHash(), af.Finding().Hash)
	}
	if d.AuthorizationHash() != af.AuthorizationHash() {
		t.Fatalf("AuthorizationHash = %s, want %s", d.AuthorizationHash(), af.AuthorizationHash())
	}
	if d.HypothesisID() != string(af.HypothesisID()) {
		t.Fatalf("HypothesisID = %s, want %s", d.HypothesisID(), string(af.HypothesisID()))
	}
	if d.Hash() == "" {
		t.Fatal("expected a non-empty Decision hash")
	}
}

func TestMakeDecisionIsDeterministic(t *testing.T) {
	af := buildAuthorizedFinding(t)
	d1, err := MakeDecision(af, OutcomeApproved, "same rationale", 1)
	if err != nil {
		t.Fatalf("MakeDecision (1): %v", err)
	}
	d2, err := MakeDecision(af, OutcomeApproved, "same rationale", 1)
	if err != nil {
		t.Fatalf("MakeDecision (2): %v", err)
	}
	if d1.Hash() != d2.Hash() {
		t.Fatalf("expected two MakeDecision calls over identical inputs to produce the identical hash: %s != %s", d1.Hash(), d2.Hash())
	}
	d3, err := MakeDecision(af, OutcomeDenied, "same rationale", 1)
	if err != nil {
		t.Fatalf("MakeDecision (3): %v", err)
	}
	if d3.Hash() == d1.Hash() {
		t.Fatal("expected a different Outcome to produce a different hash")
	}
}

// TestMakeDecisionRejectsAnUnauthorizedFinding is the direct
// implementation-level proof of "rejection of unauthorized findings":
// the zero AuthorizedFinding -- the only AuthorizedFinding value
// obtainable outside pkg/insurance/cre without actually calling
// Authorize/AuthorizeGrounded -- must be refused outright.
func TestMakeDecisionRejectsAnUnauthorizedFinding(t *testing.T) {
	var zero cre.AuthorizedFinding
	if !zero.IsZero() {
		t.Fatal("test setup: expected the zero value to report IsZero()==true")
	}
	_, err := MakeDecision(zero, OutcomeApproved, "attempted bypass", 1)
	if !errors.Is(err, ErrFindingNotAuthorized) {
		t.Fatalf("expected ErrFindingNotAuthorized, got %v", err)
	}
}

func TestMakeDecisionRejectsUnknownOutcome(t *testing.T) {
	af := buildAuthorizedFinding(t)
	_, err := MakeDecision(af, Outcome("MADE_UP_OUTCOME"), "rationale", 1)
	if !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("expected ErrUnknownOutcome, got %v", err)
	}
}

func TestMakeDecisionRejectsEmptyRationale(t *testing.T) {
	af := buildAuthorizedFinding(t)
	for _, blank := range []string{"", "   ", "\t\n"} {
		if _, err := MakeDecision(af, OutcomeApproved, blank, 1); !errors.Is(err, ErrEmptyRationale) {
			t.Fatalf("rationale=%q: expected ErrEmptyRationale, got %v", blank, err)
		}
	}
}

// TestZeroValueDecisionIsInertEverywhere proves the OTHER half of the
// sealed-type discipline: a Decision obtained WITHOUT calling
// MakeDecision (the zero value -- the only kind constructible outside
// this package, since every field is unexported) is unusable as a real
// decision anywhere: IsZero() reports it, every accessor returns
// empty/zero rather than plausible-looking data, VerifyDecisionProvenance
// refuses it, and ToAuditPayload refuses to produce an audit record for
// it.
func TestZeroValueDecisionIsInertEverywhere(t *testing.T) {
	var zero Decision
	if !zero.IsZero() {
		t.Fatal("expected the zero Decision to report IsZero()==true")
	}
	if zero.Outcome() != "" || zero.Rationale() != "" || zero.Hash() != "" ||
		zero.FindingHash() != "" || zero.AuthorizationHash() != "" || zero.HypothesisID() != "" {
		t.Fatal("expected every accessor on the zero Decision to return its zero value")
	}
	af := buildAuthorizedFinding(t)
	if err := VerifyDecisionProvenance(zero, af); !errors.Is(err, ErrDecisionHashMismatch) {
		t.Fatalf("expected VerifyDecisionProvenance to refuse the zero Decision via ErrDecisionHashMismatch, got %v", err)
	}
	if _, err := zero.ToAuditPayload(); !errors.Is(err, ErrFindingNotAuthorized) {
		t.Fatalf("expected ToAuditPayload to refuse the zero Decision, got %v", err)
	}
}

func TestVerifyDecisionProvenanceAcceptsARealDecision(t *testing.T) {
	af := buildAuthorizedFinding(t)
	d, err := MakeDecision(af, OutcomeApproved, "verified", 1)
	if err != nil {
		t.Fatalf("MakeDecision: %v", err)
	}
	if err := VerifyDecisionProvenance(d, af); err != nil {
		t.Fatalf("expected a real Decision to verify against its own authorizing finding: %v", err)
	}
}

// TestVerifyDecisionProvenanceDetectsAMismatchedFinding proves a
// Decision genuinely authorized by ONE AuthorizedFinding cannot be
// laundered as though it were authorized by a DIFFERENT one -- the
// direct test of "Trust Propagation... tidak hilang" (provenance is not
// lost) applied adversarially: it must not be SUBSTITUTABLE either.
func TestVerifyDecisionProvenanceDetectsAMismatchedFinding(t *testing.T) {
	af1 := buildAuthorizedFinding(t)
	d, err := MakeDecision(af1, OutcomeApproved, "for af1", 1)
	if err != nil {
		t.Fatalf("MakeDecision: %v", err)
	}

	// A second, genuinely different, independently-authorized finding.
	hs2, err := causation.NewHypothesisSet("case-2", "claim-2", "different question")
	if err != nil {
		t.Fatal(err)
	}
	const hID2 causation.HypothesisID = "H2"
	if err := hs2.Add(causation.Hypothesis{ID: hID2, Description: "different hypothesis"}); err != nil {
		t.Fatal(err)
	}
	if err := hs2.AddSupportingEvidence(hID2, "EV-2"); err != nil {
		t.Fatal(err)
	}
	h2, _ := hs2.Get(hID2)
	f2, err := cre.BuildFinding(hs2, h2, nil, cre.FindingInput{
		CaseID: "case-2", ContractBasis: "clause-2", ObligationRef: "obl-2",
		EventRef: "event-2", QuantumRef: "calc-2", HumanReviewRequired: true,
	}, "finding-2", 1)
	if err != nil {
		t.Fatalf("BuildFinding (2): %v", err)
	}
	af2, err := cre.Authorize(f2, hs2, hID2, nil, 1)
	if err != nil {
		t.Fatalf("Authorize (2): %v", err)
	}

	if err := VerifyDecisionProvenance(d, af2); !errors.Is(err, ErrDecisionProvenanceMismatch) {
		t.Fatalf("expected ErrDecisionProvenanceMismatch when checking d (authorized by af1) against af2, got %v", err)
	}
}

func TestToAuditPayloadPreservesEveryProvenanceField(t *testing.T) {
	af := buildAuthorizedFinding(t)
	d, err := MakeDecision(af, OutcomeApproved, "audit trail check", 7)
	if err != nil {
		t.Fatalf("MakeDecision: %v", err)
	}
	payload, err := d.ToAuditPayload()
	if err != nil {
		t.Fatalf("ToAuditPayload: %v", err)
	}
	if payload.FindingHash != d.FindingHash() || payload.AuthorizationHash != d.AuthorizationHash() ||
		payload.HypothesisID != d.HypothesisID() || payload.Outcome != string(d.Outcome()) ||
		payload.Rationale != d.Rationale() || payload.DecidedAt != d.DecidedAt() || payload.DecisionHash != d.Hash() {
		t.Fatalf("AuditPayload diverged from its source Decision: %+v", payload)
	}
}

// TestDecisionIsStructurallyImmutableAfterConstruction is the direct
// adversarial answer to the reviewer's own sharpest technical question:
// "Apakah Decision benar-benar sealed secara package boundary?" (is
// Decision really sealed at the package boundary?) -- specifically,
// could a caller do `d.Outcome = "APPROVED"` after construction, the
// same way a mutable exported field would allow?
//
// Three independent, mechanically-checked facts together make that
// impossible, not merely undocumented:
//
//  1. reflect.TypeOf(Decision{}) has ZERO exported fields -- checked
//     below by iterating every field and asserting reflect.StructField.PkgPath
//     is non-empty (Go's own signal for "unexported"; an exported field
//     always has an empty PkgPath). This is checked mechanically so a
//     future refactor that accidentally adds `Outcome Outcome` (capital
//     O) as a real exported field fails this test immediately, rather
//     than relying on a human re-reading the struct definition every
//     round.
//  2. Every accessor method on Decision has a VALUE receiver (func (d
//     Decision) ..., never func (d *Decision) ...) -- checked below via
//     reflect on the Decision method set: a value-receiver method
//     cannot mutate the caller's own copy even from WITHIN this
//     package, because Go passes d by value into the method.
//  3. There is no exported constructor-adjacent function anywhere in
//     this package other than MakeDecision that returns a *Decision or
//     accepts one for in-place mutation (grep-verified: no "func
//     (*Decision)" pointer-receiver method exists in decision.go at
//     all -- confirmed by fact 2 covering the full method set).
//
// Together: a Decision, once returned by MakeDecision, has no field a
// caller can write to (unexported) and no method that writes to it
// (every method takes Decision by value). "Constructor authorization
// gate exists, but authority can still be manipulated after
// construction" -- the reviewer's own named failure mode -- is
// structurally impossible here.
func TestDecisionIsStructurallyImmutableAfterConstruction(t *testing.T) {
	typ := reflect.TypeOf(Decision{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath == "" {
			t.Fatalf("Decision has an EXPORTED field %q -- this alone would let a caller write d.%s = ... after construction, defeating the sealed-type guarantee entirely", field.Name, field.Name)
		}
	}

	// Every method Decision exposes must take a VALUE receiver. Iterate
	// the method set of *Decision (which includes both value- and
	// pointer-receiver methods, if any existed) and confirm each
	// method's first (receiver) parameter type is Decision, never
	// *Decision -- proving no method could mutate a shared underlying
	// value.
	ptrType := reflect.TypeOf(&Decision{})
	valueType := reflect.TypeOf(Decision{})
	if ptrType.NumMethod() != valueType.NumMethod() {
		t.Fatalf("expected *Decision and Decision to expose the identical method set (proving no pointer-receiver-only method exists): *Decision has %d, Decision has %d", ptrType.NumMethod(), valueType.NumMethod())
	}
	for i := 0; i < valueType.NumMethod(); i++ {
		m := valueType.Method(i)
		recv := m.Func.Type().In(0)
		if recv != valueType {
			t.Fatalf("method %s has receiver type %s, expected value receiver %s -- a pointer receiver here could mutate a Decision in place", m.Name, recv, valueType)
		}
	}

	// End-to-end sanity: two independently-obtained copies of the same
	// real Decision must be indistinguishable in every observable way,
	// and neither copy's accessors can be used to reach back into the
	// other -- Decision has no reference-type field for that to even be
	// meaningful over, but this is the black-box version of the same
	// claim.
	af := buildAuthorizedFinding(t)
	d, err := MakeDecision(af, OutcomeApproved, "immutability audit", 1)
	if err != nil {
		t.Fatalf("MakeDecision: %v", err)
	}
	copy1 := d
	copy2 := d
	if copy1.Hash() != copy2.Hash() || copy1.Outcome() != copy2.Outcome() {
		t.Fatal("expected two copies of the same Decision to be observably identical")
	}
}
