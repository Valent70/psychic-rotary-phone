package modelregistry

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func cutoff() *time.Time {
	t := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	return &t
}

func model() Model {
	return Model{
		ID: "extract", Version: "v3", Provider: "acme-ai",
		WeightsRef:    "acme-ai/extract-v3@sha256:abc",
		PromptVersion: version(1), Temperature: 0.0,
		TrainingCutoff: cutoff(), Stage: Development,
	}
}

func version(rev uint64) contract.Version {
	return contract.Version{Component: "extract-prompt", Revision: rev}
}

func externalEval() *Evaluation {
	return &Evaluation{ID: "eval-1", Dataset: "customer-held-out-2026",
		DatasetExternal: true,
		Metrics:         map[string]float64{"field_f1": 0.94},
		Limitations: []string{"covers English-language bills of lading only",
			"does not cover handwritten annotations"},
		At: now, By: "human:ml-lead"}
}

func registry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(model()); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestAModelEntersAtDevelopment. It cannot arrive already qualified.
func TestAModelEntersAtDevelopment(t *testing.T) {
	r := NewRegistry()
	m := model()
	m.Stage = Production
	if err := r.Register(m); err == nil {
		t.Fatal("a model was registered directly at PRODUCTION")
	}
}

// TestQualificationRequiresAnEvaluationOverDataVeriqoDidNotCreate.
//
// The same boundary the assurance ladder draws, applied to models: a
// model qualified on its author's own data has been shown to work,
// not shown to hold.
func TestQualificationRequiresAnEvaluationOverExternalData(t *testing.T) {
	r := registry(t)
	if err := r.Advance("extract", "v3", Validation, "human:ml-lead", now, "ready to trial", nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Advance("extract", "v3", Qualified, "human:ml-lead", now, "looks good", nil); !errors.Is(err, ErrNoEvaluation) {
		t.Fatalf("QUALIFIED was reached with no evaluation: %v", err)
	}

	internal := externalEval()
	internal.DatasetExternal = false
	internal.Dataset = "veriqo-synthetic-2026"
	err := r.Advance("extract", "v3", Qualified, "human:ml-lead", now, "internal eval", internal)
	if !errors.Is(err, ErrNoEvaluation) {
		t.Fatalf("a model was qualified on VERIQO's own data: %v", err)
	}
	if !strings.Contains(err.Error(), "shown to work, not shown to hold") {
		t.Fatalf("the refusal does not state the distinction: %v", err)
	}

	if err := r.Advance("extract", "v3", Qualified, "human:ml-lead", now,
		"external evaluation passed", externalEval()); err != nil {
		t.Fatalf("a properly evaluated model was refused: %v", err)
	}
	m, _ := r.Get("extract", "v3")
	if m.Stage != Qualified || len(m.Evaluations) != 1 {
		t.Fatalf("stage = %s, evaluations = %d", m.Stage, len(m.Evaluations))
	}
}

// TestAModelAdvancesOneStageAtATime.
func TestAModelAdvancesOneStageAtATime(t *testing.T) {
	r := registry(t)
	if err := r.Advance("extract", "v3", Qualified, "human:x", now, "r", externalEval()); !errors.Is(err, ErrSkippedStage) {
		t.Fatalf("DEVELOPMENT advanced straight to QUALIFIED: %v", err)
	}
}

// TestADevelopmentModelCannotRunAgainstCaseMaterial.
func TestADevelopmentModelCannotRunAgainstCaseMaterial(t *testing.T) {
	r := registry(t)
	if err := r.CheckUsable("extract", "v3"); !errors.Is(err, ErrNotUsable) {
		t.Fatalf("a DEVELOPMENT model was usable: %v", err)
	}
	if err := r.RecordUse("extract", "v3", "ai:a1"); !errors.Is(err, ErrNotUsable) {
		t.Fatalf("a DEVELOPMENT model produced an artefact: %v", err)
	}
	r.Advance("extract", "v3", Validation, "human:x", now, "trial", nil)
	if err := r.CheckUsable("extract", "v3"); err != nil {
		t.Fatalf("a VALIDATION model was refused: %v", err)
	}
}

// TestValidationOutputCannotBeQualified. It may run on a case; its
// output stays DRAFT.
func TestValidationOutputCannotBeQualified(t *testing.T) {
	if Validation.OutputMayBeQualified() {
		t.Fatal("VALIDATION output may be qualified")
	}
	if !Validation.UsableOnACase() {
		t.Fatal("VALIDATION is not usable on a case")
	}
	for _, s := range []Stage{Qualified, Production} {
		if !s.OutputMayBeQualified() {
			t.Errorf("%s output may not be qualified", s)
		}
	}
}

// TestRevocationEnumeratesWhatTheModelTouched.
//
// "We stopped using it" and "we know what it touched" are different
// positions, and only one lets a customer be told what to re-examine.
func TestRevocationEnumeratesWhatTheModelTouched(t *testing.T) {
	r := registry(t)
	r.Advance("extract", "v3", Validation, "human:x", now, "trial", nil)
	for _, a := range []string{"ai:a3", "ai:a1", "ai:a2"} {
		if err := r.RecordUse("extract", "v3", a); err != nil {
			t.Fatal(err)
		}
	}
	affected, err := r.Revoke("extract", "v3", "human:ml-lead", now,
		"a systematic extraction error was found in quantity fields")
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 3 {
		t.Fatalf("affected = %v", affected)
	}
	if affected[0] != "ai:a1" {
		t.Fatalf("the list is not sorted: %v", affected)
	}
	// And the model is now unusable, including for new work.
	if err := r.CheckUsable("extract", "v3"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("a revoked model was usable: %v", err)
	}
	if !strings.Contains(r.Report(), "REVOKED") {
		t.Fatalf("the report does not show the revocation:\n%s", r.Report())
	}
}

// TestARevocationMustStateWhy.
func TestARevocationMustStateWhy(t *testing.T) {
	r := registry(t)
	if _, err := r.Revoke("extract", "v3", "human:x", now, ""); err == nil {
		t.Fatal("an unreasoned revocation was accepted")
	}
}

// TestDeprecationIsNotRevocation. A deprecated model stays usable for
// replay; a revoked one does not.
func TestDeprecationIsNotRevocation(t *testing.T) {
	r := registry(t)
	r.Advance("extract", "v3", Validation, "human:x", now, "trial", nil)
	if err := r.Deprecate("extract", "v3", "human:x", now, "superseded by v4"); err != nil {
		t.Fatal(err)
	}
	m, _ := r.Get("extract", "v3")
	if !m.Stage.UsableForReplay() {
		t.Fatal("a deprecated model cannot be consulted for replay")
	}
	if m.Stage.UsableOnACase() {
		t.Fatal("a deprecated model is usable for new work")
	}
	if Revoked.UsableForReplay() {
		t.Fatal("a revoked model is usable for replay")
	}
}

// TestAnUnknownTrainingCutoffMustBeStatedAsUnknown.
//
// A zero time meaning "unknown" and a zero time meaning "not
// recorded" are indistinguishable, so the distinction is a field.
func TestAnUnknownTrainingCutoffMustBeStatedAsUnknown(t *testing.T) {
	m := model()
	m.TrainingCutoff = nil
	if err := m.Validate(); !errors.Is(err, ErrNoCutoff) {
		t.Fatalf("a model with no cutoff and no unknown marker was accepted: %v", err)
	}
	m.CutoffUnknown = true
	if err := m.Validate(); err != nil {
		t.Fatalf("a model declaring its cutoff unknown was refused: %v", err)
	}
	// And both together is a contradiction.
	m.TrainingCutoff = cutoff()
	if err := m.Validate(); err == nil {
		t.Fatal("a model stating a cutoff AND marking it unknown was accepted")
	}
}

// TestKnowledgeCoverageForcesTheUnknownCaseToBeHandled.
func TestKnowledgeCoverageForcesTheUnknownCaseToBeHandled(t *testing.T) {
	m := model()
	covered, known := m.KnowledgeCovers(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if !known || !covered {
		t.Fatalf("an event before the cutoff: covered=%v known=%v", covered, known)
	}
	covered, known = m.KnowledgeCovers(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if !known || covered {
		t.Fatalf("an event after the cutoff: covered=%v known=%v", covered, known)
	}
	m.TrainingCutoff = nil
	m.CutoffUnknown = true
	covered, known = m.KnowledgeCovers(now)
	if known {
		t.Fatal("an unknown cutoff reported as known")
	}
	if covered {
		t.Fatal("an unknown cutoff returned a confident coverage answer")
	}
}

// TestThePromptIsPartOfTheModelsIdentity. The same weights with a
// different prompt is a different behaviour.
func TestThePromptIsPartOfTheModelsIdentity(t *testing.T) {
	m := model()
	m.PromptVersion = contract.Version{}
	if err := m.Validate(); err == nil {
		t.Fatal("a model with no prompt version was accepted")
	}
}

// TestAModelIsItsIdAndItsVersion. Two versions are two models.
func TestAModelIsItsIdAndItsVersion(t *testing.T) {
	r := registry(t)
	v4 := model()
	v4.Version = "v4"
	if err := r.Register(v4); err != nil {
		t.Fatal(err)
	}
	if len(r.Models()) != 2 {
		t.Fatalf("%d models registered", len(r.Models()))
	}
	if err := r.Register(model()); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("the same version was registered twice: %v", err)
	}
	// Advancing one does not advance the other.
	r.Advance("extract", "v3", Validation, "human:x", now, "trial", nil)
	other, _ := r.Get("extract", "v4")
	if other.Stage != Development {
		t.Fatalf("v4 moved to %s when v3 advanced", other.Stage)
	}
}

// TestAnEvaluationMustStateItsLimitations.
func TestAnEvaluationMustStateItsLimitations(t *testing.T) {
	e := externalEval()
	e.Limitations = nil
	if err := e.Validate(); err == nil {
		t.Fatal("an evaluation with no stated limitations was accepted")
	}
	e = externalEval()
	e.Metrics = nil
	if err := e.Validate(); err == nil {
		t.Fatal("an evaluation with no metrics was accepted")
	}
}

// TestAStageChangeNamesItsApproverAndReason.
func TestAStageChangeNamesItsApproverAndReason(t *testing.T) {
	r := registry(t)
	if err := r.Advance("extract", "v3", Validation, "", now, "r", nil); !errors.Is(err, ErrNoApprover) {
		t.Fatalf("an unapproved stage change was accepted: %v", err)
	}
	if err := r.Advance("extract", "v3", Validation, "human:x", now, "", nil); err == nil {
		t.Fatal("an unreasoned stage change was accepted")
	}
}

// TestARevokedModelCannotBeAdvanced.
func TestARevokedModelCannotBeAdvanced(t *testing.T) {
	r := registry(t)
	r.Revoke("extract", "v3", "human:x", now, "compromised weights")
	if err := r.Advance("extract", "v3", Validation, "human:x", now, "r", nil); !errors.Is(err, ErrRevoked) {
		t.Fatalf("a revoked model was advanced: %v", err)
	}
	if err := r.Deprecate("extract", "v3", "human:x", now, "r"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("a revoked model was deprecated: %v", err)
	}
}

// TestAnUnknownModelIsRefused.
func TestAnUnknownModelIsRefused(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("nope", "v1"); !errors.Is(err, ErrUnknownModel) {
		t.Fatal("an unknown model resolved")
	}
	if err := r.CheckUsable("nope", "v1"); !errors.Is(err, ErrUnknownModel) {
		t.Fatal("an unknown model was checked as usable")
	}
}
