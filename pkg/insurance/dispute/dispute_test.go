package dispute

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func validForum() Forum {
	return Forum{
		GoverningLaw:    "the law of Jurisdiction A",
		Jurisdiction:    "the courts of Jurisdiction A",
		ArbitrationSeat: "Seat City, Jurisdiction A",
		SourceDocument:  "CHARTERPARTY-FIC-2026-01",
		SourceClause:    "cl. 41",
		SourceVersion:   "rev-2",
	}
}

func mustMatter(t *testing.T) *Matter {
	t.Helper()
	m, err := NewMatter("DISP-1", "CASE-INS-007", "CLM-1", validForum(), 10)
	if err != nil {
		t.Fatalf("NewMatter: %v", err)
	}
	return m
}

// ---- Forum ----------------------------------------------------------

func TestForumRequiresACitedSource(t *testing.T) {
	f := validForum()
	f.SourceDocument = ""
	if err := f.Validate(); !errors.Is(err, ErrForumNoSource) {
		t.Fatalf("expected ErrForumNoSource, got %v", err)
	}
	f = validForum()
	f.SourceClause = ""
	if err := f.Validate(); !errors.Is(err, ErrForumNoSource) {
		t.Fatalf("expected ErrForumNoSource for a missing clause, got %v", err)
	}
	f = validForum()
	f.GoverningLaw = "  "
	if err := f.Validate(); !errors.Is(err, ErrForumNoGoverningLaw) {
		t.Fatalf("expected ErrForumNoGoverningLaw, got %v", err)
	}
}

// TestForumRestatesNoDurations proves the forum carries deadline RULE
// IDs rather than periods. Restating a limitation period as a number in
// this struct is exactly how "all maritime claims = 1 year" gets
// hard-coded — the deadline package already models source_clause,
// duration, calendar_rule and timezone, and this one must not shadow it.
func TestForumRestatesNoDurations(t *testing.T) {
	ft := reflect.TypeOf(Forum{})
	for i := 0; i < ft.NumField(); i++ {
		f := ft.Field(i)
		switch f.Type.Kind() {
		case reflect.Uint64, reflect.Int64, reflect.Int, reflect.Uint, reflect.Float64:
			t.Fatalf("Forum has numeric field %q (%s) — a limitation or notice period must be a "+
				"deadline.Rule reference, never a number restated here", f.Name, f.Type)
		}
	}
}

// TestNewMatterRefusesAnUngroundedForum: opening a cross-border dispute
// without a recorded governing law and jurisdiction is refused.
func TestNewMatterRefusesAnUngroundedForum(t *testing.T) {
	if _, err := NewMatter("DISP-X", "CASE-1", "CLM-1", Forum{}, 1); err == nil {
		t.Fatal("expected an ungrounded forum to be refused")
	}
	if _, err := NewMatter("", "CASE-1", "CLM-1", validForum(), 1); !errors.Is(err, ErrEmptyMatterID) {
		t.Fatalf("expected ErrEmptyMatterID, got %v", err)
	}
	if _, err := NewMatter("DISP-X", "", "CLM-1", validForum(), 1); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

// ---- Stage machine --------------------------------------------------

// TestSkippingIsPermittedAndRecorded: real disputes skip mediation. The
// skip must be visible in the log, not lost.
func TestSkippingIsPermittedAndRecorded(t *testing.T) {
	m := mustMatter(t)
	if err := m.Advance(StageArbitration, "parties agreed to go straight to arbitration", 100); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if m.Stage() != StageArbitration {
		t.Fatalf("Stage = %q, want ARBITRATION", m.Stage())
	}
	log := m.StageLog()
	last := log[len(log)-1]
	skipped := map[Stage]bool{}
	for _, s := range last.Skipped {
		skipped[s] = true
	}
	for _, want := range []Stage{StageEvidenceHold, StageLegalReview, StageNegotiation, StageMediation} {
		if !skipped[want] {
			t.Fatalf("stage %q was skipped but not recorded as skipped: %v", want, last.Skipped)
		}
	}
}

func TestBackwardStageMovesAreRefused(t *testing.T) {
	m := mustMatter(t)
	if err := m.Advance(StageCourt, "proceedings issued", 100); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := m.Advance(StageNegotiation, "trying again", 110); !errors.Is(err, ErrStageBackward) {
		t.Fatalf("expected ErrStageBackward, got %v", err)
	}
}

func TestAdvanceRequiresAReason(t *testing.T) {
	m := mustMatter(t)
	if err := m.Advance(StageLegalReview, "  ", 100); !errors.Is(err, ErrEmptyStageReason) {
		t.Fatalf("expected ErrEmptyStageReason, got %v", err)
	}
	if err := m.Advance(Stage("VIBES"), "because", 100); !errors.Is(err, ErrUnknownStage) {
		t.Fatalf("expected ErrUnknownStage, got %v", err)
	}
}

// TestStageCarriesNoMeritsFinding: reaching COURT says nothing about
// who is right, and no type in the stage machine may express one.
func TestStageCarriesNoMeritsFinding(t *testing.T) {
	forbidden := []string{"liable", "liability", "guilt", "fault", "winner", "prevail", "merit", "verdict"}
	for _, typ := range []reflect.Type{reflect.TypeOf(StageTransition{}), reflect.TypeOf(Matter{})} {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, bad := range forbidden {
				if strings.Contains(name, bad) {
					t.Fatalf("%s has field %q containing forbidden token %q", typ.Name(), typ.Field(i).Name, bad)
				}
			}
		}
	}
}

// ---- Issues: positions recorded, never adjudicated -------------------

func TestTwoRecordedPositionsMakeAnIssueContestedNotResolved(t *testing.T) {
	m := mustMatter(t)
	iss, err := NewIssue("ISS-1", "Was the vessel's readiness effective under the charterparty?")
	if err != nil {
		t.Fatalf("NewIssue: %v", err)
	}
	if err := m.AddIssue(iss); err != nil {
		t.Fatalf("AddIssue: %v", err)
	}
	if err := m.RecordPosition("ISS-1", Position{
		Party: "PTY-OWNER", Contention: "Readiness was effective at 13:35 per the terminal record",
		ReliedOnEvidence: []string{"EV-TERMINAL"}, RecordedAtTick: 100,
	}); err != nil {
		t.Fatalf("RecordPosition: %v", err)
	}
	got, _ := m.Issue("ISS-1")
	if got.Status != StatusOpen {
		t.Fatalf("one position should leave the issue OPEN, got %q", got.Status)
	}
	if err := m.RecordPosition("ISS-1", Position{
		Party: "PTY-CHARTERER", Contention: "Readiness was not effective until 13:40 per the statement of facts",
		ReliedOnEvidence: []string{"EV-SOF"}, RecordedAtTick: 110,
	}); err != nil {
		t.Fatalf("RecordPosition: %v", err)
	}
	got, _ = m.Issue("ISS-1")
	if got.Status != StatusContested {
		t.Fatalf("two positions must make the issue CONTESTED, got %q", got.Status)
	}
	if len(got.Positions) != 2 {
		t.Fatalf("both positions must be kept side by side, got %d", len(got.Positions))
	}
}

// TestIssueStatusVocabularyExpressesNoWinner is the central guardrail
// for this type: every value describes the state of the argument.
func TestIssueStatusVocabularyExpressesNoWinner(t *testing.T) {
	forbidden := []string{"FAVOUR", "FAVOR", "WON", "LOST", "UPHELD", "REJECTED_ON_MERITS", "LIABLE"}
	for s := range knownIssueStatuses {
		up := strings.ToUpper(string(s))
		for _, bad := range forbidden {
			if strings.Contains(up, bad) {
				t.Fatalf("IssueStatus %q expresses an outcome; the vocabulary must describe the argument only", s)
			}
		}
	}
}

// TestNoOpaqueConfidenceScore is the Final Design §39 rule ("membuat
// satu opaque confidence score") enforced by reflection: no exported
// type in this package may carry a float score, and Issue must expose
// the three-way decomposition instead.
func TestNoOpaqueConfidenceScore(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Issue{}), reflect.TypeOf(Position{}), reflect.TypeOf(LegalQuestion{}),
		reflect.TypeOf(Outcome{}), reflect.TypeOf(AllegationOutcome{}), reflect.TypeOf(Forum{}),
		reflect.TypeOf(LegalHold{}), reflect.TypeOf(EvidenceDecomposition{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Type.Kind() == reflect.Float64 || f.Type.Kind() == reflect.Float32 {
				t.Fatalf("%s.%s is a float — this domain decomposes evidence, it never scores it",
					typ.Name(), f.Name)
			}
			lower := strings.ToLower(f.Name)
			if lower == "confidence" || lower == "score" || lower == "probability" {
				t.Fatalf("%s.%s is an opaque confidence field", typ.Name(), f.Name)
			}
		}
	}
	// And the honest replacement genuinely exists.
	i := Issue{
		IssueID:               "ISS-1",
		SupportingEvidence:    []string{"A", "B", "C"},
		ContradictingEvidence: []string{"D"},
		MissingEvidence:       []string{"terminal operational record"},
	}
	d := i.Decompose()
	if d.SupportingCount != 3 || d.ContradictingCount != 1 || d.MissingCount != 1 {
		t.Fatalf("Decompose must report the three counts separately, got %+v", d)
	}
}

func TestAddIssueEvidenceKeepsTheThreeListsApart(t *testing.T) {
	m := mustMatter(t)
	iss, _ := NewIssue("ISS-1", "What caused the shortfall?")
	if err := m.AddIssue(iss); err != nil {
		t.Fatalf("AddIssue: %v", err)
	}
	if err := m.AddIssueEvidence("ISS-1", []string{"EV-1", "EV-2"}, []string{"EV-3"}, []string{"joint survey"}); err != nil {
		t.Fatalf("AddIssueEvidence: %v", err)
	}
	got, _ := m.Issue("ISS-1")
	if len(got.SupportingEvidence) != 2 || len(got.ContradictingEvidence) != 1 || len(got.MissingEvidence) != 1 {
		t.Fatalf("the three lists must stay apart, got %+v", got)
	}
}

// ---- Legal questions: one status, no answer -------------------------

// TestLegalQuestionHasExactlyOneStatus proves there is no second value
// for a code path to reach — the structural form of "no AI legal
// verdict".
func TestLegalQuestionHasExactlyOneStatus(t *testing.T) {
	q, err := NewLegalQuestion("LQ-1",
		"Does the war-risk insurance arrangement displace the general-average contribution?")
	if err != nil {
		t.Fatalf("NewLegalQuestion: %v", err)
	}
	if q.Status != StatusLegalInterpretationRequired {
		t.Fatalf("Status = %q, want LEGAL_INTERPRETATION_REQUIRED", q.Status)
	}
	// A caller cannot smuggle a different status in through the matter.
	m := mustMatter(t)
	q.Status = LegalQuestionStatus("DISPLACED_YES")
	if err := m.AddLegalQuestion(q); err != nil {
		t.Fatalf("AddLegalQuestion: %v", err)
	}
	stored := m.LegalQuestions()
	if len(stored) != 1 || stored[0].Status != StatusLegalInterpretationRequired {
		t.Fatalf("a caller-supplied status must be overwritten with the only legal value, got %+v", stored)
	}
}

// TestHistoricalReferenceCannotBecomeARule: a real decision may be
// attached as reading material and must carry no binding/weight field
// through which it could act as a rule.
func TestHistoricalReferenceCannotBecomeARule(t *testing.T) {
	ht := reflect.TypeOf(HistoricalReference{})
	forbidden := []string{"binding", "weight", "authorityweight", "applies", "controls", "rule", "precedentvalue"}
	for i := 0; i < ht.NumField(); i++ {
		name := strings.ToLower(ht.Field(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Fatalf("HistoricalReference has field %q — a real decision is reference material for a "+
					"human, never a rule this system applies", ht.Field(i).Name)
			}
		}
		if ht.Field(i).Type.Kind() != reflect.String {
			t.Fatalf("HistoricalReference.%s is %s — every field must be recorded text a human reads",
				ht.Field(i).Name, ht.Field(i).Type)
		}
	}
}

// TestLegalQuestionMovesRelatedIssuesToAwaitingInterpretation: the
// honest status for a factual issue that turns on an unanswered legal
// question.
func TestLegalQuestionMovesRelatedIssuesToAwaitingInterpretation(t *testing.T) {
	m := mustMatter(t)
	iss, _ := NewIssue("ISS-GA", "Is a general-average contribution due from the cargo interest?")
	if err := m.AddIssue(iss); err != nil {
		t.Fatalf("AddIssue: %v", err)
	}
	q, _ := NewLegalQuestion("LQ-GA", "Does the recorded war-risk arrangement displace the contribution?")
	q.RelatedIssueIDs = []string{"ISS-GA"}
	if err := m.AddLegalQuestion(q); err != nil {
		t.Fatalf("AddLegalQuestion: %v", err)
	}
	got, _ := m.Issue("ISS-GA")
	if got.Status != StatusAwaitingLegalInterpretation {
		t.Fatalf("Status = %q, want AWAITING_LEGAL_INTERPRETATION", got.Status)
	}
	if !m.RequiresLegalInterpretation() {
		t.Fatal("a matter with a recorded legal question must report that interpretation is required")
	}
}

// ---- Settlement ≠ every allegation proven ---------------------------

// TestSettlementNeverImpliesAllegationsProven is the headline guardrail
// of this package. It checks the rule from three directions: the
// constructor, a hand-built struct, and the aggregate's own recording
// path.
func TestSettlementNeverImpliesAllegationsProven(t *testing.T) {
	alleged := []string{
		"misdeclaration of cargo quantity",
		"failure to notify within the contractual period",
		"breach of the cooperation clause",
	}

	out, err := RecordSettlement("OUT-1", "the parties", "SETTLEMENT-AGREEMENT-FIC-1", alleged, nil, 500)
	if err != nil {
		t.Fatalf("RecordSettlement: %v", err)
	}
	if got := out.ProvenAllegations(); len(got) != 0 {
		t.Fatalf("a settlement proved %d allegations; it must always prove none: %+v", len(got), got)
	}
	if got := out.UndeterminedAllegations(); len(got) != len(alleged) {
		t.Fatalf("expected all %d allegations NOT_DETERMINED, got %d", len(alleged), len(got))
	}

	// Direction 2: a hand-built settlement Outcome claiming a proven
	// allegation must not validate.
	bad := Outcome{
		OutcomeID: "OUT-2", Kind: OutcomeSettlement, Authority: "the parties",
		SourceDocument: "SETTLEMENT-AGREEMENT-FIC-2",
		Allegations: []AllegationOutcome{{
			Allegation: "misdeclaration", Result: AllegationProven,
			DeterminedBy: "the parties", SourceDocument: "cl. 3",
		}},
	}
	if err := bad.Validate(); !errors.Is(err, ErrSettlementCannotProve) {
		t.Fatalf("expected ErrSettlementCannotProve, got %v", err)
	}

	// Direction 3: the aggregate re-validates, so the same struct cannot
	// be smuggled onto a matter.
	m := mustMatter(t)
	if err := m.RecordOutcome(bad, 600); !errors.Is(err, ErrSettlementCannotProve) {
		t.Fatalf("Matter.RecordOutcome must apply the same rule, got %v", err)
	}
}

// TestWithdrawnAndDiscontinuedAlsoProveNothing: the same reasoning
// applies to every ending that involves no authority determining
// anything.
func TestWithdrawnAndDiscontinuedAlsoProveNothing(t *testing.T) {
	for _, kind := range []OutcomeKind{OutcomeWithdrawn, OutcomeDiscontinued} {
		o := Outcome{
			OutcomeID: "OUT-W", Kind: kind, Authority: "the claimant",
			SourceDocument: "NOTICE-OF-DISCONTINUANCE-FIC-1",
			Allegations: []AllegationOutcome{{
				Allegation: "breach", Result: AllegationProven,
				DeterminedBy: "nobody", SourceDocument: "n/a",
			}},
		}
		if err := o.Validate(); !errors.Is(err, ErrSettlementCannotProve) {
			t.Fatalf("%s must determine nothing, got %v", kind, err)
		}
	}
}

// TestAwardCanDetermineButOnlyWithACitedAuthority: the complement — a
// real determination is recordable, but never without its source.
func TestAwardCanDetermineButOnlyWithACitedAuthority(t *testing.T) {
	_, err := RecordAward("OUT-3", "the arbitral tribunal", "AWARD-FIC-1", []AllegationOutcome{{
		Allegation: "failure to notify within the contractual period",
		Result:     AllegationProven,
	}}, 700)
	if !errors.Is(err, ErrDeterminationNeedsAuthority) {
		t.Fatalf("expected ErrDeterminationNeedsAuthority for an uncited determination, got %v", err)
	}

	out, err := RecordAward("OUT-4", "the arbitral tribunal", "AWARD-FIC-1", []AllegationOutcome{{
		Allegation:   "failure to notify within the contractual period",
		Result:       AllegationProven,
		DeterminedBy: "the arbitral tribunal",
		// SourceDocument names the paragraph, not just the instrument.
		SourceDocument: "AWARD-FIC-1 para. 88",
	}, {
		Allegation:     "breach of the cooperation clause",
		Result:         AllegationNotProven,
		DeterminedBy:   "the arbitral tribunal",
		SourceDocument: "AWARD-FIC-1 para. 104",
	}}, 700)
	if err != nil {
		t.Fatalf("RecordAward: %v", err)
	}
	if len(out.ProvenAllegations()) != 1 {
		t.Fatalf("expected exactly one proven allegation, got %d", len(out.ProvenAllegations()))
	}
}

func TestOutcomeRequiresAuthorityAndSource(t *testing.T) {
	if _, err := RecordSettlement("OUT-5", "", "DOC", nil, nil, 1); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("expected ErrNoAuthority, got %v", err)
	}
	if _, err := RecordSettlement("OUT-5", "the parties", "", nil, nil, 1); !errors.Is(err, ErrNoSourceDocument) {
		t.Fatalf("expected ErrNoSourceDocument, got %v", err)
	}
	if _, err := RecordJudgment("", "a court", "DOC", nil, 1); !errors.Is(err, ErrEmptyOutcomeID) {
		t.Fatalf("expected ErrEmptyOutcomeID, got %v", err)
	}
}

// TestOutcomeRestatesNoMoneyFigures: the settlement figure lives where
// evidence-backed money lives, and a second copy of a number is a
// second number.
func TestOutcomeRestatesNoMoneyFigures(t *testing.T) {
	ot := reflect.TypeOf(Outcome{})
	for i := 0; i < ot.NumField(); i++ {
		f := ot.Field(i)
		switch f.Type.Kind() {
		case reflect.Int64, reflect.Int, reflect.Float64:
			if f.Name != "RecordedTick" {
				t.Fatalf("Outcome has numeric field %q — monetary terms are referenced, never restated", f.Name)
			}
		}
	}
}

func TestRecordOutcomeIsOnceOnly(t *testing.T) {
	m := mustMatter(t)
	out, err := RecordSettlement("OUT-6", "the parties", "SETTLEMENT-FIC-3", []string{"a"}, nil, 500)
	if err != nil {
		t.Fatalf("RecordSettlement: %v", err)
	}
	if err := m.RecordOutcome(out, 510); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if m.Stage() != StageOutcomeRecorded {
		t.Fatalf("Stage = %q, want OUTCOME_RECORDED", m.Stage())
	}
	if err := m.RecordOutcome(out, 520); !errors.Is(err, ErrOutcomeAlready) {
		t.Fatalf("expected ErrOutcomeAlready, got %v", err)
	}
}

// ---- Legal hold ------------------------------------------------------

func TestLegalHoldIsRecordedAndReleasedNeverDeleted(t *testing.T) {
	m := mustMatter(t)
	h := LegalHold{
		HoldID: "HOLD-1", Scope: "all voyage and cargo documentation for the disputed call",
		InstructedBy: "legal counsel", PreservationOrderID: "PRES-1",
		EvidenceInScope: []string{"EV-1", "EV-2"}, StartTick: 100,
	}
	if err := m.PlaceHold(h); err != nil {
		t.Fatalf("PlaceHold: %v", err)
	}
	if len(m.ActiveHolds()) != 1 {
		t.Fatal("expected one active hold")
	}
	if err := m.ReleaseHold("HOLD-1", "legal counsel", "matter concluded", 900); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if len(m.ActiveHolds()) != 0 {
		t.Fatal("a released hold must not be active")
	}
	all := m.Holds()
	if len(all) != 1 {
		t.Fatalf("the released hold must survive as history, got %d", len(all))
	}
	if !all[0].ReleasedStatus || all[0].ReleasedBy != "legal counsel" || all[0].StartTick != 100 {
		t.Fatalf("release metadata wrong or original mutated: %+v", all[0])
	}
	if err := m.ReleaseHold("HOLD-1", "someone", "again", 950); err == nil {
		t.Fatal("releasing an already-released hold must be refused")
	}
	if err := m.ReleaseHold("HOLD-NOPE", "someone", "reason", 950); !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("expected ErrHoldNotFound, got %v", err)
	}
}

func TestLegalHoldRequiresScopeAndInstructor(t *testing.T) {
	m := mustMatter(t)
	if err := m.PlaceHold(LegalHold{HoldID: "H", InstructedBy: "x"}); !errors.Is(err, ErrEmptyScope) {
		t.Fatalf("expected ErrEmptyScope, got %v", err)
	}
	if err := m.PlaceHold(LegalHold{HoldID: "H", Scope: "everything"}); !errors.Is(err, ErrEmptyInstruct) {
		t.Fatalf("expected ErrEmptyInstruct, got %v", err)
	}
}

// ---- Whole-package structural guardrail ------------------------------

// TestNoTypeInThisPackageCarriesAVerdictField extends the
// TestDossierHasNoVerdictField pattern across every exported type this
// package adds.
func TestNoTypeInThisPackageCarriesAVerdictField(t *testing.T) {
	forbidden := []string{
		"verdict", "liable", "liability", "guilty", "guilt", "fault",
		"approved", "denied", "denial", "covered", "payable", "winner", "atfault",
	}
	types := []reflect.Type{
		reflect.TypeOf(Forum{}), reflect.TypeOf(Issue{}), reflect.TypeOf(Position{}),
		reflect.TypeOf(LegalQuestion{}), reflect.TypeOf(HistoricalReference{}),
		reflect.TypeOf(LegalHold{}), reflect.TypeOf(Outcome{}), reflect.TypeOf(AllegationOutcome{}),
		reflect.TypeOf(StageTransition{}), reflect.TypeOf(EvidenceDecomposition{}), reflect.TypeOf(Matter{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, bad := range forbidden {
				if strings.Contains(name, bad) {
					t.Fatalf("%s has field %q containing forbidden token %q", typ.Name(), typ.Field(i).Name, bad)
				}
			}
		}
	}
}
