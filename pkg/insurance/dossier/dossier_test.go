package dossier

import (
	"reflect"
	"strings"
	"testing"

	insurancecase "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/gap"
	"veriqo/pkg/insurance/timeline"
)

func TestGenerateRejectsNilCase(t *testing.T) {
	if _, err := Generate(nil, Inputs{}); err != ErrNilCase {
		t.Fatalf("expected ErrNilCase, got %v", err)
	}
}

func TestGenerateCapturesAuditTrailFromStateLog(t *testing.T) {
	c, _ := insurancecase.New("CASE-1", 0)
	c.Advance(insurancecase.StatePartiesIdentified, 10)
	c.Advance(insurancecase.StatePolicyRegistered, 20)

	d, err := Generate(c, Inputs{ClaimID: "CLM-1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(d.LifecycleAuditTrail) != 2 {
		t.Fatalf("expected 2 audit entries mirroring StateLog, got %d", len(d.LifecycleAuditTrail))
	}
	if d.LifecycleAuditTrail[0].To != string(insurancecase.StatePartiesIdentified) {
		t.Fatalf("unexpected first audit entry: %+v", d.LifecycleAuditTrail[0])
	}
	if d.CaseID != "CASE-1" || d.ClaimID != "CLM-1" {
		t.Fatalf("unexpected identity fields: %+v", d)
	}
}

// TestHumanReviewRequiredIsRealAggregationNotPlaceholder proves
// Section 18 is computed from real input signals: a case with no
// review-triggering input must report HumanReviewRequired=false and
// an EMPTY question list, and adding a single flagged input must both
// set the flag AND append the corresponding, traceable question text.
func TestHumanReviewRequiredIsRealAggregationNotPlaceholder(t *testing.T) {
	c, _ := insurancecase.New("CASE-1", 0)

	clean, err := Generate(c, Inputs{})
	if err != nil {
		t.Fatalf("Generate (clean): %v", err)
	}
	if clean.HumanReviewRequired {
		t.Fatal("expected HumanReviewRequired=false with no flagged inputs")
	}
	if len(clean.HumanReviewQuestions) != 0 {
		t.Fatalf("expected zero human review questions with no flagged inputs, got %v", clean.HumanReviewQuestions)
	}

	assessment := gap.NewAssessment("CLM-1")
	insufficient, err := gap.ComputeSufficiency("required_evidence", 3, 0, false)
	if err != nil {
		t.Fatalf("ComputeSufficiency: %v", err)
	}
	if err := assessment.Set(insufficient); err != nil {
		t.Fatalf("Assessment.Set: %v", err)
	}

	flagged, err := Generate(c, Inputs{GapAssessment: assessment})
	if err != nil {
		t.Fatalf("Generate (flagged): %v", err)
	}
	if !flagged.HumanReviewRequired {
		t.Fatal("expected HumanReviewRequired=true once an INSUFFICIENT gap.Sufficiency is present")
	}
	if len(flagged.HumanReviewQuestions) != 1 {
		t.Fatalf("expected exactly 1 review question, got %v", flagged.HumanReviewQuestions)
	}
	if !strings.Contains(flagged.HumanReviewQuestions[0], "required_evidence") {
		t.Fatalf("expected the review question to name the flagged issue, got %q", flagged.HumanReviewQuestions[0])
	}
}

func TestTimelineConflictAlwaysTriggersReview(t *testing.T) {
	c, _ := insurancecase.New("CASE-1", 0)
	conflict := timeline.Conflict{
		ConflictID:          "CFL-1",
		Kind:                timeline.ConflictKind("IMPOSSIBLE_SEQUENCE"),
		EventIDs:            []string{"EVT-1", "EVT-2"},
		Description:         "event B precedes event A despite depending on it",
		HumanReviewRequired: true,
	}
	d, err := Generate(c, Inputs{TimelineConflicts: []timeline.Conflict{conflict}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !d.HumanReviewRequired {
		t.Fatal("expected a timeline conflict to force HumanReviewRequired=true")
	}
	if len(d.HumanReviewQuestions) != 1 || !strings.Contains(d.HumanReviewQuestions[0], "CFL-1") {
		t.Fatalf("expected the conflict ID to be traceable in the review question, got %v", d.HumanReviewQuestions)
	}
}

// TestDossierHasNoVerdictField is the structural, reflection-based
// enforcement of the blueprint's own repeated golden rule: VICE never
// renders a coverage/liability/settlement verdict. This checks the
// ACTUAL Go type, not a comment someone could forget to update.
func TestDossierHasNoVerdictField(t *testing.T) {
	forbidden := []string{
		"verdict", "decision", "approved", "rejected", "denied",
		"settlement", "payable", "liable", "finalamount", "finaldecision",
	}
	typ := reflect.TypeOf(Dossier{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, f := range forbidden {
			if strings.Contains(name, f) {
				t.Fatalf("Dossier field %q looks like a forbidden verdict field (matches %q)", typ.Field(i).Name, f)
			}
		}
	}
}

// TestGenerateIsReproducible: generating a Dossier twice from
// identical case state and inputs must produce an identical result
// (blueprint §37 deterministic-replay requirement, extended to the
// dossier assembly step itself).
func TestGenerateIsReproducible(t *testing.T) {
	build := func() *insurancecase.Case {
		c, _ := insurancecase.New("CASE-1", 0)
		c.Advance(insurancecase.StatePartiesIdentified, 10)
		c.Advance(insurancecase.StatePolicyRegistered, 20)
		return c
	}
	assessment := gap.NewAssessment("CLM-1")
	partial, _ := gap.ComputeSufficiency("survey_report", 2, 1, false)
	assessment.Set(partial)

	d1, err := Generate(build(), Inputs{ClaimID: "CLM-1", GapAssessment: assessment})
	if err != nil {
		t.Fatalf("Generate 1: %v", err)
	}
	d2, err := Generate(build(), Inputs{ClaimID: "CLM-1", GapAssessment: assessment})
	if err != nil {
		t.Fatalf("Generate 2: %v", err)
	}
	if !reflect.DeepEqual(d1, d2) {
		t.Fatalf("expected byte-identical Dossier across two Generate calls with identical inputs:\n%+v\nvs\n%+v", d1, d2)
	}
}
