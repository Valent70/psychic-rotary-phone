package obligation

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"veriqo/pkg/insurance/deadline"
)

func noticeRule(t *testing.T, duration uint64) deadline.Rule {
	t.Helper()
	r, err := deadline.New("DR-NOTICE", deadline.SourceTypePolicy, "cl. 7.2", "POLICY-FIC-1", "v1",
		"DAMAGE_DISCOVERY", duration, deadline.CalendarRuleCalendarDays, "UTC")
	if err != nil {
		t.Fatalf("deadline.New: %v", err)
	}
	return r
}

func noticeObligation(t *testing.T) Obligation {
	t.Helper()
	return Obligation{
		ObligationID:     "OBL-NOTICE-1",
		CaseID:           "CASE-INS-002",
		Duty:             "notify the insurer of the loss",
		SourceClause:     "cl. 7.2",
		SourceDocument:   "POLICY-FIC-1",
		SourceVersion:    "v1",
		TriggerEvent:     "DAMAGE_DISCOVERY",
		TriggerBasis:     TriggerFromDiscovery,
		RequiredEvidence: []string{"damage photograph", "proof of delivery"},
		DeadlineRuleID:   "DR-NOTICE",
		ComplianceBasis:  ComplianceByReceived,
		ResponsibleParty: "PTY-CLAIMS-OFFICER",
		Status:           StatusOpen,
	}
}

// ================= LATE NOTICE != COVERAGE DENIED =====================

// TestComplianceVocabularyCannotExpressDenial scans the compliance
// vocabulary itself. If a future author adds a value like
// COVERAGE_FORFEITED, this fails — which is the point: the vocabulary
// is about the notice, and only about the notice.
func TestComplianceVocabularyCannotExpressDenial(t *testing.T) {
	forbidden := []string{
		"DENIED", "DENIAL", "FORFEIT", "COVERAGE", "VOID", "BARRED",
		"REJECTED", "LOST", "INVALID", "DEFEAT",
	}
	for _, c := range ComplianceVocabulary() {
		up := strings.ToUpper(string(c))
		for _, bad := range forbidden {
			if strings.Contains(up, bad) {
				t.Fatalf("Compliance value %q expresses a claim consequence; this vocabulary describes "+
					"the notice only — LATE NOTICE != COVERAGE DENIED", c)
			}
		}
	}
	if len(ComplianceVocabulary()) != len(knownCompliance) {
		t.Fatal("ComplianceVocabulary must list every modelled value, or the scan above proves nothing")
	}
}

// TestCoverageEffectHasExactlyOneValue proves the one-valued type. A
// notice assessment never determines coverage, and there is no second
// value for a code path to reach.
func TestCoverageEffectHasExactlyOneValue(t *testing.T) {
	// Every assessment, in every branch, carries the same single value.
	rule := noticeRule(t, 2)
	o := noticeObligation(t)

	cases := []struct {
		name   string
		notice Notice
		now    uint64
	}{
		{"timely", Notice{NoticeID: "N", CaseID: "C", DiscoveryTime: 100, NoticeReceivedTime: 101}, 200},
		{"late", Notice{NoticeID: "N", CaseID: "C", DiscoveryTime: 100, NoticeReceivedTime: 150}, 200},
		{"not given", Notice{NoticeID: "N", CaseID: "C", DiscoveryTime: 100}, 200},
		{"not yet due", Notice{NoticeID: "N", CaseID: "C", DiscoveryTime: 100}, 101},
		{"undetermined", Notice{NoticeID: "N", CaseID: "C"}, 200},
	}
	for _, tc := range cases {
		a, err := Assess(tc.notice, o, rule, tc.now)
		if err != nil {
			t.Fatalf("%s: Assess: %v", tc.name, err)
		}
		if a.CoverageEffect != EffectNotDetermined {
			t.Fatalf("%s: CoverageEffect = %q, want the only value %q",
				tc.name, a.CoverageEffect, EffectNotDetermined)
		}
	}
}

// TestLateNoticeProducesReviewNotDenial is the headline test for this
// package: a genuinely late notice yields the factual value LATE, a
// recorded delay, and a review requirement naming the policy wording
// and the applicable law — and nothing stronger anywhere in the output.
func TestLateNoticeProducesReviewNotDenial(t *testing.T) {
	rule := noticeRule(t, 2) // notice due 2 ticks after discovery
	o := noticeObligation(t)
	n := Notice{
		NoticeID: "NOT-1", CaseID: "CASE-INS-002",
		IncidentTime: 90, DiscoveryTime: 100,
		NoticeSentTime: 118, NoticeReceivedTime: 120,
		NoticeRecipient: "PTY-INSURER", NoticeMethod: "email",
		NoticeAcknowledgement: "ACK-1", AcknowledgedTime: 121,
		NoticeEvidence: []string{"EV-NOTICE-EMAIL"},
	}

	a, err := Assess(n, o, rule, 200)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Compliance != ComplianceLate {
		t.Fatalf("Compliance = %q, want LATE", a.Compliance)
	}
	if a.NoticeDeadlineTick != 102 {
		t.Fatalf("NoticeDeadlineTick = %d, want 102 (discovery 100 + 2)", a.NoticeDeadlineTick)
	}
	if a.DelayTicks != 18 {
		t.Fatalf("DelayTicks = %d, want 18 (received 120 - deadline 102)", a.DelayTicks)
	}
	if a.CoverageEffect != EffectNotDetermined {
		t.Fatalf("CoverageEffect = %q", a.CoverageEffect)
	}
	if len(a.ReviewRequirements) != 1 {
		t.Fatalf("expected exactly one review requirement, got %d", len(a.ReviewRequirements))
	}
	rr := a.ReviewRequirements[0]
	if len(rr.GovernedBy) == 0 {
		t.Fatal("the review requirement must name what governs the answer")
	}
	joined := strings.ToLower(strings.Join(rr.GovernedBy, " ") + " " + rr.Requirement)
	if !strings.Contains(joined, "policy wording") {
		t.Fatalf("the review requirement must point at the policy wording, got %q", rr.Requirement)
	}

	// And nowhere in the whole rendered assessment does a denial appear.
	assertNoDenialLanguage(t, a)
}

// TestNoticeNotGivenAlsoProducesOnlyReview: the same rule applies to a
// notice that never arrived at all.
func TestNoticeNotGivenAlsoProducesOnlyReview(t *testing.T) {
	rule := noticeRule(t, 2)
	o := noticeObligation(t)
	n := Notice{NoticeID: "NOT-2", CaseID: "CASE-INS-002", DiscoveryTime: 100}

	a, err := Assess(n, o, rule, 500)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Compliance != ComplianceNotGiven {
		t.Fatalf("Compliance = %q, want NOT_GIVEN", a.Compliance)
	}
	if a.CoverageEffect != EffectNotDetermined {
		t.Fatalf("CoverageEffect = %q", a.CoverageEffect)
	}
	if len(a.ReviewRequirements) != 1 {
		t.Fatalf("expected one review requirement, got %d", len(a.ReviewRequirements))
	}
	assertNoDenialLanguage(t, a)
}

// TestAssessmentHasNoDenialField proves by reflection that no field on
// the Assessment type could carry a claim outcome.
func TestAssessmentHasNoDenialField(t *testing.T) {
	forbidden := []string{
		"denied", "denial", "covered", "coverage", "forfeit", "void", "barred",
		"liable", "liability", "verdict", "approved", "payable", "rejected",
	}
	at := reflect.TypeOf(Assessment{})
	for i := 0; i < at.NumField(); i++ {
		name := strings.ToLower(at.Field(i).Name)
		for _, bad := range forbidden {
			// CoverageEffect is the deliberate exception: it exists
			// precisely to say the effect is NOT determined, and its type
			// has exactly one value. Everything else is forbidden.
			if strings.Contains(name, bad) && at.Field(i).Name != "CoverageEffect" {
				t.Fatalf("Assessment has field %q containing forbidden token %q", at.Field(i).Name, bad)
			}
		}
	}
	// And the exception really is one-valued.
	f, ok := at.FieldByName("CoverageEffect")
	if !ok {
		t.Fatal("Assessment must carry the explicit CoverageEffect marker")
	}
	if f.Type != reflect.TypeOf(EffectNotDetermined) {
		t.Fatalf("CoverageEffect must be the one-valued CoverageEffect type, got %s", f.Type)
	}
}

func assertNoDenialLanguage(t *testing.T, a Assessment) {
	t.Helper()
	var b strings.Builder
	b.WriteString(string(a.Compliance))
	for _, u := range a.Uncertainty {
		b.WriteString(" " + u)
	}
	for _, rr := range a.ReviewRequirements {
		b.WriteString(" " + rr.Requirement + " " + rr.Reviewer + " " + strings.Join(rr.GovernedBy, " "))
	}
	lower := strings.ToLower(b.String())
	for _, bad := range []string{
		"claim is denied", "coverage is denied", "coverage denied",
		"claim denied", "cover is lost", "claim fails", "coverage forfeited",
	} {
		if strings.Contains(lower, bad) {
			t.Fatalf("assessment output contains denial language %q", bad)
		}
	}
}

// ================= Notice assessment mechanics ========================

func TestTimelyNoticeRecordsNoDelayAndNoReview(t *testing.T) {
	rule := noticeRule(t, 10)
	o := noticeObligation(t)
	n := Notice{
		NoticeID: "NOT-3", CaseID: "CASE-INS-002", DiscoveryTime: 100,
		NoticeSentTime: 104, NoticeReceivedTime: 105,
		NoticeAcknowledgement: "ACK-9",
	}
	a, err := Assess(n, o, rule, 200)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Compliance != ComplianceTimely {
		t.Fatalf("Compliance = %q, want TIMELY", a.Compliance)
	}
	if a.DelayTicks != 0 {
		t.Fatalf("DelayTicks = %d, want 0 — 'how early' is never reported as a delay", a.DelayTicks)
	}
	if len(a.ReviewRequirements) != 0 {
		t.Fatalf("a timely notice needs no review requirement, got %v", a.ReviewRequirements)
	}
}

func TestNotYetDueIsDistinctFromNotGiven(t *testing.T) {
	rule := noticeRule(t, 10)
	o := noticeObligation(t)
	n := Notice{NoticeID: "NOT-4", CaseID: "CASE-INS-002", DiscoveryTime: 100}

	early, err := Assess(n, o, rule, 105) // before the 110 deadline
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if early.Compliance != ComplianceNotYetDue {
		t.Fatalf("before the deadline, Compliance = %q, want NOT_YET_DUE", early.Compliance)
	}
	late, err := Assess(n, o, rule, 500)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if late.Compliance != ComplianceNotGiven {
		t.Fatalf("after the deadline with no notice, Compliance = %q, want NOT_GIVEN", late.Compliance)
	}
}

// TestNoDeadlineRuleYieldsUndeterminedNotAGuess: the golden rule —
// when the input is missing, say so.
func TestNoDeadlineRuleYieldsUndeterminedNotAGuess(t *testing.T) {
	o := noticeObligation(t)
	o.DeadlineRuleID = ""
	n := Notice{NoticeID: "NOT-5", CaseID: "CASE-INS-002", DiscoveryTime: 100, NoticeReceivedTime: 900}

	a, err := Assess(n, o, deadline.Rule{}, 1000)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Compliance != ComplianceUndetermined {
		t.Fatalf("Compliance = %q, want UNDETERMINED with no deadline rule", a.Compliance)
	}
	if a.NoticeDeadlineTick != 0 {
		t.Fatalf("no deadline could be computed, but one was reported: %d", a.NoticeDeadlineTick)
	}
	if len(a.Uncertainty) == 0 {
		t.Fatal("the missing deadline rule must be named in the uncertainty list")
	}
	found := false
	for _, u := range a.Uncertainty {
		if strings.Contains(u, "deadline rule") {
			found = true
		}
	}
	if !found {
		t.Fatalf("uncertainty must name the missing deadline rule, got %v", a.Uncertainty)
	}
	if len(a.ReviewRequirements) != 1 {
		t.Fatalf("an undetermined assessment must hand work to a human, got %d", len(a.ReviewRequirements))
	}
}

// TestMissingTriggerTimeIsNamedNotAssumed: if the clause runs from
// discovery and no discovery time is recorded, say so rather than
// silently falling back to the incident time.
func TestMissingTriggerTimeIsNamedNotAssumed(t *testing.T) {
	rule := noticeRule(t, 5)
	o := noticeObligation(t) // TriggerFromDiscovery
	n := Notice{NoticeID: "NOT-6", CaseID: "CASE-INS-002", IncidentTime: 100, NoticeReceivedTime: 200}

	a, err := Assess(n, o, rule, 300)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.TriggerTick != 0 {
		t.Fatalf("TriggerTick = %d — the incident time must not be silently substituted for discovery", a.TriggerTick)
	}
	if a.Compliance != ComplianceUndetermined {
		t.Fatalf("Compliance = %q, want UNDETERMINED", a.Compliance)
	}
	joined := strings.Join(a.Uncertainty, " | ")
	if !strings.Contains(joined, string(TriggerFromDiscovery)) {
		t.Fatalf("uncertainty must name the missing trigger basis, got %v", a.Uncertainty)
	}
}

// TestClauseBasisIsHonouredNotChosen: a clause measuring by SENT time
// gets the sent time, even when a received time is also recorded.
func TestClauseBasisIsHonouredNotChosen(t *testing.T) {
	rule := noticeRule(t, 10)
	o := noticeObligation(t)
	o.ComplianceBasis = ComplianceBySent
	n := Notice{
		NoticeID: "NOT-7", CaseID: "CASE-INS-002", DiscoveryTime: 100,
		NoticeSentTime: 109, NoticeReceivedTime: 130,
	}
	a, err := Assess(n, o, rule, 200)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.GivenTick != 109 {
		t.Fatalf("GivenTick = %d, want the SENT time 109 as the clause specifies", a.GivenTick)
	}
	if a.Compliance != ComplianceTimely {
		t.Fatalf("Compliance = %q, want TIMELY under the sent-time basis", a.Compliance)
	}

	// The same facts under a received-time clause come out LATE. Same
	// evidence, different clause, different answer — traceably.
	o.ComplianceBasis = ComplianceByReceived
	b, err := Assess(n, o, rule, 200)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if b.Compliance != ComplianceLate {
		t.Fatalf("Compliance = %q, want LATE under the received-time basis", b.Compliance)
	}
}

// TestUnspecifiedBasisRecordsTheAmbiguity: when the clause does not
// say, the ambiguity is reported rather than silently resolved.
func TestUnspecifiedBasisRecordsTheAmbiguity(t *testing.T) {
	rule := noticeRule(t, 10)
	o := noticeObligation(t)
	o.ComplianceBasis = ComplianceBasisUnspecified
	n := Notice{
		NoticeID: "NOT-8", CaseID: "CASE-INS-002", DiscoveryTime: 100,
		NoticeSentTime: 105, NoticeReceivedTime: 108,
	}
	a, err := Assess(n, o, rule, 200)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	joined := strings.Join(a.Uncertainty, " | ")
	if !strings.Contains(joined, "does not state") {
		t.Fatalf("the clause's silence must be recorded as uncertainty, got %v", a.Uncertainty)
	}
}

// TestMissingAcknowledgementIsUncertaintyNotAFinding: absence of an
// acknowledgement is not evidence that notice was not received.
func TestMissingAcknowledgementIsUncertaintyNotAFinding(t *testing.T) {
	rule := noticeRule(t, 10)
	o := noticeObligation(t)
	n := Notice{NoticeID: "NOT-9", CaseID: "CASE-INS-002", DiscoveryTime: 100, NoticeReceivedTime: 105}
	a, err := Assess(n, o, rule, 200)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Compliance != ComplianceTimely {
		t.Fatalf("a missing acknowledgement must not change the compliance value, got %q", a.Compliance)
	}
	joined := strings.Join(a.Uncertainty, " | ")
	if !strings.Contains(joined, "acknowledgement") {
		t.Fatalf("the missing acknowledgement must be recorded as uncertainty, got %v", a.Uncertainty)
	}
	if !strings.Contains(joined, "not evidence") {
		t.Fatalf("the uncertainty entry must say what it does NOT prove, got %v", a.Uncertainty)
	}
}

// TestLateNoticeStatusIsCompletedNotOverdue: an obligation performed
// late is discharged, not still outstanding. Conflating the two would
// misreport what remains to be done.
func TestObligationStatusForDistinguishesLateFromOutstanding(t *testing.T) {
	if got := ObligationStatusFor(Assessment{Compliance: ComplianceLate}); got != StatusCompleted {
		t.Fatalf("a late-but-given notice discharges the duty; got %q", got)
	}
	if got := ObligationStatusFor(Assessment{Compliance: ComplianceNotGiven}); got != StatusOverdue {
		t.Fatalf("a notice never given leaves the duty OVERDUE; got %q", got)
	}
	if got := ObligationStatusFor(Assessment{Compliance: ComplianceNotYetDue}); got != StatusOpen {
		t.Fatalf("got %q", got)
	}
	if got := ObligationStatusFor(Assessment{Compliance: ComplianceUndetermined}); got != StatusUndetermind {
		t.Fatalf("got %q", got)
	}
}

// ================= The obligation graph ================================

// TestObligationRequiresItsClause: the Final Design §12 graph is only
// worth anything if every obligation traces to a clause.
func TestObligationRequiresItsClause(t *testing.T) {
	o := noticeObligation(t)
	o.SourceClause = ""
	if err := o.Validate(); !errors.Is(err, ErrNoClauseSource) {
		t.Fatalf("expected ErrNoClauseSource, got %v", err)
	}
	o = noticeObligation(t)
	o.SourceVersion = ""
	if err := o.Validate(); !errors.Is(err, ErrNoClauseSource) {
		t.Fatalf("expected ErrNoClauseSource for a missing version, got %v", err)
	}
	o = noticeObligation(t)
	o.ResponsibleParty = ""
	if err := o.Validate(); !errors.Is(err, ErrNoResponsibleParty) {
		t.Fatalf("expected ErrNoResponsibleParty, got %v", err)
	}
	o = noticeObligation(t)
	o.TriggerEvent = "  "
	if err := o.Validate(); !errors.Is(err, ErrEmptyTrigger) {
		t.Fatalf("expected ErrEmptyTrigger, got %v", err)
	}
}

// TestObligationRestatesNoDuration proves the graph references a
// deadline.Rule rather than carrying a period, which is how "notice
// within 24 hours" stays traceable to a clause instead of becoming a
// constant.
func TestObligationRestatesNoDuration(t *testing.T) {
	ot := reflect.TypeOf(Obligation{})
	for i := 0; i < ot.NumField(); i++ {
		f := ot.Field(i)
		if f.Name == "CompletedAtTick" {
			continue // a recorded fact, not a period
		}
		switch f.Type.Kind() {
		case reflect.Uint64, reflect.Int64, reflect.Int, reflect.Float64:
			t.Fatalf("Obligation has numeric field %q — the period must be a deadline.Rule reference, "+
				"never a duration restated here", f.Name)
		}
	}
	if _, ok := ot.FieldByName("DeadlineRuleID"); !ok {
		t.Fatal("Obligation must reference a deadline.Rule by ID")
	}
}

// TestExplainAnswersWhyFromRecordedFieldsOnly: "why is the system
// asking for this?" must be answerable with a clause reference.
func TestExplainAnswersWhyFromRecordedFieldsOnly(t *testing.T) {
	o := noticeObligation(t)
	got := o.Explain()
	for _, want := range []string{
		"cl. 7.2", "POLICY-FIC-1", "v1", "notify the insurer of the loss",
		"DAMAGE_DISCOVERY", "PTY-CLAIMS-OFFICER", "damage photograph",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Explain() omitted %q: %s", want, got)
		}
	}
}

func TestRegistryLifecycle(t *testing.T) {
	r, err := NewRegistry("CASE-INS-002")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	o := noticeObligation(t)
	if err := r.Register(o); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Register(o); !errors.Is(err, ErrDuplicateObligation) {
		t.Fatalf("expected ErrDuplicateObligation, got %v", err)
	}
	foreign := noticeObligation(t)
	foreign.ObligationID = "OBL-OTHER"
	foreign.CaseID = "CASE-SOMETHING-ELSE"
	if err := r.Register(foreign); err == nil {
		t.Fatal("an obligation from another case must be refused")
	}
	if len(r.Outstanding()) != 1 {
		t.Fatal("the open obligation must be outstanding")
	}
	if err := r.Discharge("OBL-NOTICE-1", 120, nil); err == nil {
		t.Fatal("discharging with no evidence must be refused")
	}
	if err := r.Discharge("OBL-NOTICE-1", 120, []string{"EV-NOTICE-EMAIL"}); err != nil {
		t.Fatalf("Discharge: %v", err)
	}
	got, _ := r.Get("OBL-NOTICE-1")
	if got.Status != StatusCompleted || got.CompletedAtTick != 120 || len(got.DischargingEvidence) != 1 {
		t.Fatalf("discharge not recorded properly: %+v", got)
	}
	if len(r.Outstanding()) != 0 {
		t.Fatal("a discharged obligation is not outstanding")
	}
	if len(r.ByResponsibleParty("PTY-CLAIMS-OFFICER")) != 1 {
		t.Fatal("ByResponsibleParty must find the obligation")
	}
}

// TestStatusVocabularyIsAboutTheDutyNotTheClaim.
func TestStatusVocabularyIsAboutTheDutyNotTheClaim(t *testing.T) {
	forbidden := []string{"DENIED", "COVERED", "APPROVED", "LIABLE", "PAID", "REJECTED"}
	for _, s := range StatusVocabulary() {
		up := strings.ToUpper(string(s))
		for _, bad := range forbidden {
			if strings.Contains(up, bad) {
				t.Fatalf("Status %q describes a claim outcome; this vocabulary is about the duty", s)
			}
		}
	}
	if len(StatusVocabulary()) != len(knownStatuses) {
		t.Fatal("StatusVocabulary must list every modelled status")
	}
}

// TestAssessRejectsAnInvalidObligation: an assessment against an
// ungrounded obligation is refused rather than produced.
func TestAssessRejectsAnInvalidObligation(t *testing.T) {
	o := noticeObligation(t)
	o.SourceDocument = ""
	n := Notice{NoticeID: "N", CaseID: "C", DiscoveryTime: 100}
	if _, err := Assess(n, o, noticeRule(t, 5), 200); !errors.Is(err, ErrNoClauseSource) {
		t.Fatalf("expected ErrNoClauseSource, got %v", err)
	}
}

func TestNewNoticeValidatesIdentity(t *testing.T) {
	if _, err := NewNotice("", "C"); !errors.Is(err, ErrEmptyNoticeID) {
		t.Fatalf("expected ErrEmptyNoticeID, got %v", err)
	}
	if _, err := NewNotice("N", ""); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

// TestNoOpaqueConfidenceScore mirrors the other new packages.
func TestNoOpaqueConfidenceScore(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Notice{}), reflect.TypeOf(Obligation{}),
		reflect.TypeOf(Assessment{}), reflect.TypeOf(ReviewRequirement{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			lower := strings.ToLower(f.Name)
			if f.Type.Kind() == reflect.Float64 || lower == "confidence" || lower == "score" {
				t.Fatalf("%s.%s is an opaque score", typ.Name(), f.Name)
			}
		}
	}
}
