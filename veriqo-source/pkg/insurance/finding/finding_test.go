package finding

import (
	"reflect"
	"strings"
	"testing"

	"veriqo/pkg/insurance/causation"
)

func completeFinding() Finding {
	return Finding{
		FindingID:                "f1",
		CaseID:                   "case-1",
		SupportedBy:              []string{"ev-1", "ev-2"},
		ContradictedBy:           nil,
		ContradictionsConsidered: true,
		ContractBasis:            "clause-4.2",
		ObligationRef:            "obl-1",
		EventRef:                 "event-1",
		Causation:                "The evidence is consistent with hull breach preceding flooding, though an alternative mechanical-failure explanation could not be ruled out.",
		QuantumRef:               "calc-1",
		ConfidenceBasis:          causation.StatusPartiallySupported,
		Alternatives:             []string{"hyp-2"},
		AlternativesConsidered:   true,
		HumanReviewRequired:      true,
		HumanReviewDecided:       true,
		Tick:                     100,
	}
}

func TestEvaluateProducesCandidateWhenFieldsMissing(t *testing.T) {
	f := Finding{FindingID: "f1", CaseID: "case-1"}
	got := Evaluate(f)
	if got.Status != StatusCandidate {
		t.Fatalf("expected StatusCandidate for an empty Finding, got %s", got.Status)
	}
	if len(MissingFields(f)) == 0 {
		t.Fatal("expected an empty Finding to have missing fields")
	}
}

func TestEvaluateProducesFindingWhenAllFieldsPresent(t *testing.T) {
	got := Evaluate(completeFinding())
	if got.Status != StatusFinding {
		t.Fatalf("expected StatusFinding, got %s: missing=%v", got.Status, MissingFields(completeFinding()))
	}
	if got.Hash == "" {
		t.Fatal("expected a non-empty Hash")
	}
}

func TestEachRequiredFieldIndividuallyBlocksTheGate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f Finding) Finding
	}{
		{"supported_by", func(f Finding) Finding { f.SupportedBy = nil; return f }},
		{"contradictions_considered", func(f Finding) Finding { f.ContradictionsConsidered = false; return f }},
		{"contract_basis", func(f Finding) Finding { f.ContractBasis = ""; return f }},
		{"obligation_ref", func(f Finding) Finding { f.ObligationRef = ""; return f }},
		{"event_ref", func(f Finding) Finding { f.EventRef = ""; return f }},
		{"causation", func(f Finding) Finding { f.Causation = ""; return f }},
		{"quantum_ref", func(f Finding) Finding { f.QuantumRef = ""; return f }},
		{"confidence_basis_empty", func(f Finding) Finding { f.ConfidenceBasis = ""; return f }},
		{"confidence_basis_unknown", func(f Finding) Finding { f.ConfidenceBasis = "NOT_A_REAL_STATUS"; return f }},
		{"alternatives_considered", func(f Finding) Finding { f.AlternativesConsidered = false; return f }},
		{"human_review_decided", func(f Finding) Finding { f.HumanReviewDecided = false; return f }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.mutate(completeFinding())
			got := Evaluate(f)
			if got.Status != StatusCandidate {
				t.Fatalf("expected removing %s to keep the Finding at CANDIDATE, got %s", tc.name, got.Status)
			}
		})
	}
}

func TestContradictedByCanLegitimatelyBeEmptyOnceConsidered(t *testing.T) {
	f := completeFinding()
	f.ContradictedBy = nil
	f.ContradictionsConsidered = true
	got := Evaluate(f)
	if got.Status != StatusFinding {
		t.Fatalf("expected a Finding with no contradicting evidence (but genuinely considered) to still reach FINDING, got %s", got.Status)
	}
}

func TestAlternativesCanLegitimatelyBeEmptyOnceConsidered(t *testing.T) {
	f := completeFinding()
	f.Alternatives = nil
	f.AlternativesConsidered = true
	got := Evaluate(f)
	if got.Status != StatusFinding {
		t.Fatalf("expected a Finding with no alternatives found (but genuinely considered) to still reach FINDING, got %s", got.Status)
	}
}

func TestHumanReviewNotRequiredIsAValidDecidedState(t *testing.T) {
	f := completeFinding()
	f.HumanReviewRequired = false
	f.HumanReviewDecided = true
	got := Evaluate(f)
	if got.Status != StatusFinding {
		t.Fatalf("expected HumanReviewRequired=false (but decided) to still reach FINDING, got %s", got.Status)
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	f := completeFinding()
	g1 := Evaluate(f)
	g2 := Evaluate(f)
	if g1.Hash != g2.Hash {
		t.Fatal("expected evaluating the identical Finding twice to produce the identical hash")
	}
}

func TestEvaluateOverwritesCallerSuppliedStatus(t *testing.T) {
	f := Finding{FindingID: "f1"} // incomplete
	f.Status = StatusFinding      // caller lies
	got := Evaluate(f)
	if got.Status != StatusCandidate {
		t.Fatalf("expected Evaluate to overwrite a dishonest caller-supplied Status, got %s", got.Status)
	}
}

func TestVerifyFindingHashDetectsTampering(t *testing.T) {
	f := Evaluate(completeFinding())
	if err := VerifyFindingHash(f); err != nil {
		t.Fatalf("expected a freshly evaluated Finding to verify: %v", err)
	}
	f.ConfidenceBasis = causation.StatusContradicted // tamper without recomputing hash
	if err := VerifyFindingHash(f); err == nil {
		t.Fatal("expected tampering with ConfidenceBasis to invalidate the Finding hash")
	}
}

// TestFindingHasNoVerdictField is the CRE "6 MUST NOT" list's structural
// enforcement: a Finding must never carry a field that could be read as
// a legally binding verdict, liability determination, or payable
// amount, mirroring pkg/insurance/dossier's own
// TestDossierHasNoVerdictField.
func TestFindingHasNoVerdictField(t *testing.T) {
	forbidden := []string{"verdict", "liable", "liability", "guilty", "winner", "approvedamount", "payable", "settlementamount"}
	typ := reflect.TypeOf(Finding{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Fatalf("Finding field %q looks like a verdict/liability field (matched %q) -- CRE forbids this", typ.Field(i).Name, bad)
			}
		}
	}
}

func TestMissingFieldsNamesAreStable(t *testing.T) {
	f := Finding{}
	missing := MissingFields(f)
	if len(missing) != 10 {
		t.Fatalf("expected all 10 required-field checks to fire on an empty Finding, got %d: %v", len(missing), missing)
	}
}
