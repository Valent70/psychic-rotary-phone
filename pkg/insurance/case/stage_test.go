package caseinsurance

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestStageMappingIsTotalAndSingleValued proves every internal
// lifecycle state — all fifteen sequence members plus both terminals —
// maps to exactly one external stage, and that every mapped stage is
// one of the nine the design document freezes. A state added to
// `sequence` without a stage fails here rather than silently reporting
// as INTAKE.
func TestStageMappingIsTotalAndSingleValued(t *testing.T) {
	all := append(Sequence(), StateCaseClosed, StateOpenIssues)
	for _, s := range all {
		stage, err := StageForState(s)
		if err != nil {
			t.Fatalf("internal state %q has no external stage mapping: %v", s, err)
		}
		if !IsKnownStage(stage) {
			t.Fatalf("internal state %q maps to %q, which is not one of the nine frozen stages", s, stage)
		}
	}
	if len(stageOf) != len(all) {
		t.Fatalf("stageOf has %d entries but there are %d internal states — the mapping must be exactly total",
			len(stageOf), len(all))
	}
}

// TestEveryStageIsReachable proves the nine-stage vocabulary is not
// padded: each of the nine genuinely covers at least one internal
// state. A stage no state maps to would be a vocabulary word that can
// never be reported, which is exactly the kind of decorative enum this
// repository's governance forbids.
func TestEveryStageIsReachable(t *testing.T) {
	for _, stage := range StageOrder() {
		states := StatesForStage(stage)
		if len(states) == 0 {
			t.Fatalf("stage %q covers no internal state — it can never be reported", stage)
		}
	}
}

// TestStageIsMonotonicAlongTheInternalSequence proves the derived stage
// never goes backwards as the internal machine moves forward. If it
// could, the external view would show a case regressing while the real
// lifecycle advanced.
func TestStageIsMonotonicAlongTheInternalSequence(t *testing.T) {
	prev := -1
	for _, s := range Sequence() {
		stage, err := StageForState(s)
		if err != nil {
			t.Fatalf("StageForState(%q): %v", s, err)
		}
		rank := StageRank(stage)
		if rank < prev {
			t.Fatalf("internal state %q reports stage %q (rank %d) after a higher-ranked stage (rank %d) — "+
				"the external stage must never move backwards", s, stage, rank, prev)
		}
		prev = rank
	}
}

// TestStageAdvancesInLockstepWithTheRealLifecycle drives a case through
// the entire internal sequence and checks the derived stage at each
// step against the mapping — not against a second copy of it.
func TestStageAdvancesInLockstepWithTheRealLifecycle(t *testing.T) {
	c, err := New("CASE-STAGE-1", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Stage() != StageIntake {
		t.Fatalf("a fresh case must report INTAKE, got %q", c.Stage())
	}
	for i, s := range Sequence() {
		if i == 0 {
			continue // already in CASE_CREATED
		}
		if err := c.Advance(s, uint64(i)*10); err != nil {
			t.Fatalf("Advance(%q): %v", s, err)
		}
		want, err := StageForState(s)
		if err != nil {
			t.Fatalf("StageForState(%q): %v", s, err)
		}
		if got := c.Stage(); got != want {
			t.Fatalf("after advancing to %q the case reports stage %q, want %q", s, got, want)
		}
	}
	if c.Stage() != StageResolved {
		t.Fatalf("a case at DOSSIER_GENERATED must report RESOLVED, got %q", c.Stage())
	}
	if err := c.Advance(StateOpenIssues, 999); err != nil {
		t.Fatalf("Advance(OPEN_ISSUES): %v", err)
	}
	if c.Stage() != StageClosed {
		t.Fatalf("OPEN_ISSUES is a terminal PROCESS state and must report CLOSED, got %q", c.Stage())
	}
}

// TestStageIsDerivedNotSettable proves by reflection that Case exposes
// no field or method by which an external stage could be asserted. The
// reconciliation decision depends on this: the moment a stage becomes
// settable, it can desynchronise from the real lifecycle position.
func TestStageIsDerivedNotSettable(t *testing.T) {
	ct := reflect.TypeOf(Case{})
	for i := 0; i < ct.NumField(); i++ {
		f := ct.Field(i)
		if f.Type == reflect.TypeOf(Stage("")) {
			t.Fatalf("Case has a field %q of type Stage — the external stage must be derived, never stored", f.Name)
		}
	}
	pt := reflect.TypeOf(&Case{})
	for i := 0; i < pt.NumMethod(); i++ {
		name := pt.Method(i).Name
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "setstage") || lower == "advancestage" || lower == "markstage" {
			t.Fatalf("Case exposes %q — there must be no way to set the external stage", name)
		}
	}
}

// ---- Exception states -----------------------------------------------

// TestExceptionNeverMovesTheLifecycle is the central invariant of the
// reconciliation: exception states are an orthogonal overlay. Raising
// and clearing every one of the five must leave the internal state, the
// derived stage and the state log completely unchanged.
func TestExceptionNeverMovesTheLifecycle(t *testing.T) {
	c, err := New("CASE-EXC-1", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, s := range []State{StatePartiesIdentified, StatePolicyRegistered, StateClaimRegistered,
		StateEvidenceIngested, StateEvidenceVerified, StateTimelineReconstructed} {
		if err := c.Advance(s, 10); err != nil {
			t.Fatalf("Advance(%q): %v", s, err)
		}
	}
	stateBefore := c.State()
	stageBefore := c.Stage()
	logBefore := len(c.StateLog())

	for _, e := range ExceptionOrder() {
		if err := c.RaiseException(e, "raised by the reconciliation test", "ARTIFACT-"+string(e), 100); err != nil {
			t.Fatalf("RaiseException(%q): %v", e, err)
		}
		if c.State() != stateBefore {
			t.Fatalf("raising %q moved the internal state from %q to %q", e, stateBefore, c.State())
		}
		if c.Stage() != stageBefore {
			t.Fatalf("raising %q moved the derived stage from %q to %q", e, stageBefore, c.Stage())
		}
		if len(c.StateLog()) != logBefore {
			t.Fatalf("raising %q appended to the lifecycle state log", e)
		}
	}
	if got := len(c.ActiveExceptions()); got != len(ExceptionOrder()) {
		t.Fatalf("expected all %d exceptions active, got %d", len(ExceptionOrder()), got)
	}

	for _, e := range ExceptionOrder() {
		if err := c.ClearException(e, "reviewer-1", "resolved during the test", 200); err != nil {
			t.Fatalf("ClearException(%q): %v", e, err)
		}
		if c.State() != stateBefore || c.Stage() != stageBefore || len(c.StateLog()) != logBefore {
			t.Fatalf("clearing %q disturbed the lifecycle", e)
		}
	}
	if got := c.ActiveExceptions(); len(got) != 0 {
		t.Fatalf("expected no active exceptions after clearing them all, got %v", got)
	}
}

// TestExceptionsAreOrthogonalToStage proves a case can hold an
// exception at ANY stage — the property that made modelling them as
// sequence members wrong.
func TestExceptionsAreOrthogonalToStage(t *testing.T) {
	for i, target := range Sequence() {
		c, err := New("CASE-ORTHO", 0)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		for j := 1; j <= i; j++ {
			if err := c.Advance(Sequence()[j], uint64(j)); err != nil {
				t.Fatalf("Advance: %v", err)
			}
		}
		if err := c.RaiseException(ExceptionOnLegalHold, "preservation order in force", "PRES-1", 50); err != nil {
			t.Fatalf("RaiseException at %q: %v", target, err)
		}
		if !c.HasException(ExceptionOnLegalHold) {
			t.Fatalf("ON_LEGAL_HOLD did not hold at stage %q", c.Stage())
		}
		if c.State() != target {
			t.Fatalf("raising an exception changed the state at %q", target)
		}
	}
}

// TestExceptionRequiresAReasonAndACitedArtifact: an exception nothing
// points at is prose, and this package refuses to store prose as state.
func TestExceptionRequiresAReasonAndACitedArtifact(t *testing.T) {
	c, err := New("CASE-EXC-2", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.RaiseException(ExceptionContradicted, "", "CX-1", 10); !errors.Is(err, ErrEmptyReason) {
		t.Fatalf("expected ErrEmptyReason, got %v", err)
	}
	if err := c.RaiseException(ExceptionContradicted, "sources disagree", "", 10); !errors.Is(err, ErrEmptyEvidence) {
		t.Fatalf("expected ErrEmptyEvidence, got %v", err)
	}
	if err := c.RaiseException(Exception("VIBES_ARE_OFF"), "hunch", "X", 10); !errors.Is(err, ErrUnknownException) {
		t.Fatalf("expected ErrUnknownException, got %v", err)
	}
}

// TestClearingAnExceptionPreservesTheHistory: the fact that a case WAS
// contradicted survives the contradiction being resolved.
func TestClearingAnExceptionPreservesTheHistory(t *testing.T) {
	c, err := New("CASE-EXC-3", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.RaiseException(ExceptionContradicted, "NOR and AIS disagree", "CX-001", 10); err != nil {
		t.Fatalf("RaiseException: %v", err)
	}
	if err := c.ClearException(ExceptionContradicted, "reviewer-2", "terminal record produced", 60); err != nil {
		t.Fatalf("ClearException: %v", err)
	}
	log := c.ExceptionLog()
	if len(log) != 1 {
		t.Fatalf("expected the raising to survive as history, got %d records", len(log))
	}
	r := log[0]
	if !r.ClearedState || r.ClearedTick != 60 || r.ClearedBy != "reviewer-2" {
		t.Fatalf("clearing metadata not recorded: %+v", r)
	}
	if r.RaisedBy != "CX-001" || r.RaisedTick != 10 || r.Reason != "NOR and AIS disagree" {
		t.Fatalf("the original raising was mutated: %+v", r)
	}
	if c.HasException(ExceptionContradicted) {
		t.Fatal("a cleared exception must no longer be active")
	}
}

func TestClearingAnUnraisedExceptionIsRefused(t *testing.T) {
	c, err := New("CASE-EXC-4", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.ClearException(ExceptionDisputed, "reviewer", "never raised", 10); err == nil {
		t.Fatal("clearing an exception that was never raised must be refused, not silently ignored")
	}
}

// TestStatusComposesStageAndExceptions checks the externally-rendered
// object carries both halves and hides neither.
func TestStatusComposesStageAndExceptions(t *testing.T) {
	c, err := New("CASE-STATUS-1", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, s := range []State{StatePartiesIdentified, StatePolicyRegistered, StateClaimRegistered, StateEvidenceIngested} {
		if err := c.Advance(s, 10); err != nil {
			t.Fatalf("Advance: %v", err)
		}
	}
	if err := c.RaiseException(ExceptionInsufficient, "survey report missing", "GAP-survey", 20); err != nil {
		t.Fatalf("RaiseException: %v", err)
	}
	st := c.Status()
	if st.CaseID != "CASE-STATUS-1" {
		t.Fatalf("Status.CaseID = %q", st.CaseID)
	}
	if st.Stage != StagePreserved {
		t.Fatalf("Status.Stage = %q, want PRESERVED", st.Stage)
	}
	if st.InternalState != StateEvidenceIngested {
		t.Fatalf("Status.InternalState = %q, want EVIDENCE_INGESTED", st.InternalState)
	}
	if len(st.Exceptions) != 1 || st.Exceptions[0] != ExceptionInsufficient {
		t.Fatalf("Status.Exceptions = %v, want [INSUFFICIENT]", st.Exceptions)
	}
	if st.Terminal {
		t.Fatal("a mid-lifecycle case must not report Terminal")
	}
}

// TestStatusHasNoVerdictField extends the TestDossierHasNoVerdictField
// pattern to the externally-reported status object: nothing in it may
// express a coverage, liability or settlement outcome.
func TestStatusHasNoVerdictField(t *testing.T) {
	forbidden := []string{
		"verdict", "liable", "liability", "approved", "denied", "denial",
		"covered", "coverage", "settlement", "payout", "payable", "guilt", "fault",
	}
	st := reflect.TypeOf(Status{})
	for i := 0; i < st.NumField(); i++ {
		name := strings.ToLower(st.Field(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Fatalf("Status has field %q containing forbidden token %q", st.Field(i).Name, bad)
			}
		}
	}
}

func TestSortedExceptionsIsDeterministic(t *testing.T) {
	in := []Exception{ExceptionSuperseded, ExceptionDisputed, ExceptionOnLegalHold}
	got := sortedExceptions(in)
	want := []Exception{ExceptionDisputed, ExceptionOnLegalHold, ExceptionSuperseded}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedExceptions = %v, want %v", got, want)
	}
	if in[0] != ExceptionSuperseded {
		t.Fatal("sortedExceptions must not mutate its input")
	}
}
