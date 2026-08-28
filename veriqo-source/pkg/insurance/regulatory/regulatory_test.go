package regulatory

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustMatter(t *testing.T) *Matter {
	t.Helper()
	m, err := NewMatter("REG-1", "CASE-INS-006", "the financial supervisory authority of Jurisdiction B", "Jurisdiction B", 10)
	if err != nil {
		t.Fatalf("NewMatter: %v", err)
	}
	return m
}

func addAllegations(t *testing.T, m *Matter, descriptions ...string) {
	t.Helper()
	for i, d := range descriptions {
		a, err := NewAllegation(idFor(i), d)
		if err != nil {
			t.Fatalf("NewAllegation: %v", err)
		}
		if err := m.AddAllegation(a); err != nil {
			t.Fatalf("AddAllegation: %v", err)
		}
	}
}

func idFor(i int) string { return "ALG-" + string(rune('A'+i)) }

func TestNewMatterRequiresAnAuthority(t *testing.T) {
	if _, err := NewMatter("REG-X", "CASE-1", "  ", "J", 1); !errors.Is(err, ErrEmptyAuthority) {
		t.Fatalf("expected ErrEmptyAuthority, got %v", err)
	}
	if _, err := NewMatter("", "CASE-1", "auth", "J", 1); !errors.Is(err, ErrEmptyMatterID) {
		t.Fatalf("expected ErrEmptyMatterID, got %v", err)
	}
}

func TestNewAllegationStartsAlleged(t *testing.T) {
	a, err := NewAllegation("ALG-1", "failure to maintain adequate transaction records")
	if err != nil {
		t.Fatalf("NewAllegation: %v", err)
	}
	if a.Result != ResultAlleged {
		t.Fatalf("Result = %q, want ALLEGED — the only honest starting state", a.Result)
	}
}

// ---- Settlement ≠ every allegation proven ---------------------------

// TestSettlementProvesNothing is the headline guardrail. A matter with
// three live allegations that settles must end with ZERO proven
// allegations, whatever the caller hoped.
func TestSettlementProvesNothing(t *testing.T) {
	m := mustMatter(t)
	addAllegations(t, m,
		"inadequate transaction record-keeping",
		"failure to escalate a flagged counterparty",
		"insufficient third-party due diligence")

	if err := m.RecordSettlement("SETTLEMENT-ORDER-FIC-2026-11", 500); err != nil {
		t.Fatalf("RecordSettlement: %v", err)
	}
	if !m.Settled() {
		t.Fatal("expected the matter to report settled")
	}
	if got := m.ProvenAllegations(); len(got) != 0 {
		t.Fatalf("a settlement proved %d allegations; it must always prove none: %+v", len(got), got)
	}
	if got := m.UndeterminedAllegations(); len(got) != 3 {
		t.Fatalf("expected all three allegations NOT_DETERMINED, got %d", len(got))
	}
	for _, a := range m.Allegations() {
		if a.DeterminedByKind != FindingSettlementOnly {
			t.Fatalf("allegation %s should be marked SETTLEMENT_ONLY, got %q", a.AllegationID, a.DeterminedByKind)
		}
		if a.Note == "" {
			t.Fatalf("allegation %s should record that it was not adjudicated", a.AllegationID)
		}
	}
	if m.Stage() != StageSettlement {
		t.Fatalf("Stage = %q, want SETTLEMENT", m.Stage())
	}
}

// TestSettlementOnlyFindingCannotProve: the rule holds at the
// finding-recording door too, not only at settlement time.
func TestSettlementOnlyFindingCannotProve(t *testing.T) {
	m := mustMatter(t)
	addAllegations(t, m, "inadequate record-keeping")
	err := m.RecordFinding("ALG-A", FindingSettlementOnly, ResultProven, "the parties", "SETTLEMENT-FIC-1")
	if !errors.Is(err, ErrSettlementProves) {
		t.Fatalf("expected ErrSettlementProves, got %v", err)
	}
	// And through Validate, for a hand-built Allegation.
	bad := Allegation{
		AllegationID: "ALG-Z", Description: "x", Result: ResultProven,
		DeterminedByKind: FindingSettlementOnly, DeterminedBy: "the parties", SourceDocument: "doc",
	}
	if err := bad.Validate(); !errors.Is(err, ErrSettlementProves) {
		t.Fatalf("Validate must apply the same rule, got %v", err)
	}
	if err := m.AddAllegation(bad); !errors.Is(err, ErrSettlementProves) {
		t.Fatalf("AddAllegation must re-validate, got %v", err)
	}
}

// TestAPriorRealFindingSurvivesASubsequentSettlement: settling after a
// regulator has actually found something does not erase the finding.
// The rule is "settlement proves nothing", not "settlement unproves
// everything".
func TestAPriorRealFindingSurvivesASubsequentSettlement(t *testing.T) {
	m := mustMatter(t)
	addAllegations(t, m, "inadequate record-keeping", "failure to escalate")

	if err := m.RecordFinding("ALG-A", FindingRegulatory, ResultProven,
		"the financial supervisory authority of Jurisdiction B",
		"FINDING-NOTICE-FIC-1 para. 12"); err != nil {
		t.Fatalf("RecordFinding: %v", err)
	}
	if err := m.RecordSettlement("SETTLEMENT-ORDER-FIC-2", 600); err != nil {
		t.Fatalf("RecordSettlement: %v", err)
	}
	proven := m.ProvenAllegations()
	if len(proven) != 1 || proven[0].AllegationID != "ALG-A" {
		t.Fatalf("the prior regulatory finding must survive the settlement, got %+v", proven)
	}
	undetermined := m.UndeterminedAllegations()
	if len(undetermined) != 1 || undetermined[0].AllegationID != "ALG-B" {
		t.Fatalf("the un-adjudicated allegation must be NOT_DETERMINED, got %+v", undetermined)
	}
}

func TestRealFindingRequiresAuthorityAndSource(t *testing.T) {
	m := mustMatter(t)
	addAllegations(t, m, "inadequate record-keeping")
	if err := m.RecordFinding("ALG-A", FindingRegulatory, ResultProven, "", "doc"); !errors.Is(err, ErrDeterminationNeedsSource) {
		t.Fatalf("expected ErrDeterminationNeedsSource, got %v", err)
	}
	if err := m.RecordFinding("ALG-A", FindingRegulatory, ResultProven, "authority", ""); !errors.Is(err, ErrDeterminationNeedsSource) {
		t.Fatalf("expected ErrDeterminationNeedsSource for a missing document, got %v", err)
	}
	if err := m.RecordFinding("ALG-NOPE", FindingTribunal, ResultNotProven, "a", "b"); !errors.Is(err, ErrAllegationNotFound) {
		t.Fatalf("expected ErrAllegationNotFound, got %v", err)
	}
}

// ---- Monitor requirement ≠ monitor completed -------------------------

// TestMonitorRequirementIsNotMonitorCompletion is the second headline
// guardrail. Imposing a monitorship must never, by any path, make it
// report completed.
func TestMonitorRequirementIsNotMonitorCompletion(t *testing.T) {
	m := mustMatter(t)
	mr := MonitorRequirement{
		MonitorID:            "MON-1",
		Requirement:          "independent compliance monitor to report annually for three years",
		ImposedBy:            "the financial supervisory authority of Jurisdiction B",
		SourceDocument:       "SETTLEMENT-ORDER-FIC-2026-11 s. 7",
		ImposedTick:          500,
		ExpectedDurationNote: "three annual reports",
	}
	if err := m.ImposeMonitor(mr); err != nil {
		t.Fatalf("ImposeMonitor: %v", err)
	}

	monitors := m.Monitors()
	if len(monitors) != 1 {
		t.Fatalf("expected one monitorship, got %d", len(monitors))
	}
	if monitors[0].Completed() {
		t.Fatal("an imposed monitorship must NEVER report completed")
	}
	if monitors[0].Status() != "MONITOR_REQUIRED_NOT_CERTIFIED_COMPLETE" {
		t.Fatalf("Status = %q", monitors[0].Status())
	}
	if len(m.OutstandingMonitors()) != 1 {
		t.Fatal("the imposed monitorship must be outstanding")
	}
	blockers := m.CompletionBlockers()
	if len(blockers) == 0 {
		t.Fatal("an outstanding monitorship must block completion")
	}

	// Only a real certification changes that.
	if err := m.CertifyMonitor("MON-1", Certification{
		CertifiedBy:    "the appointed independent monitor",
		SourceDocument: "MONITOR-CERTIFICATION-FIC-3",
		CertifiedTick:  2000,
		Scope:          "the three annual reports required by s. 7",
	}); err != nil {
		t.Fatalf("CertifyMonitor: %v", err)
	}
	if !m.Monitors()[0].Completed() {
		t.Fatal("a certified monitorship must report completed")
	}
	if len(m.OutstandingMonitors()) != 0 {
		t.Fatal("a certified monitorship must not be outstanding")
	}
}

// TestMonitorRequirementHasNoCompletedField proves the rule
// structurally: there is no boolean anyone could set at imposition time.
func TestMonitorRequirementHasNoCompletedField(t *testing.T) {
	mt := reflect.TypeOf(MonitorRequirement{})
	for i := 0; i < mt.NumField(); i++ {
		f := mt.Field(i)
		lower := strings.ToLower(f.Name)
		if f.Type.Kind() == reflect.Bool {
			t.Fatalf("MonitorRequirement has boolean field %q — completion must be DERIVED from a real "+
				"certification, never a settable flag", f.Name)
		}
		if strings.Contains(lower, "complete") || strings.Contains(lower, "done") || strings.Contains(lower, "discharged") {
			t.Fatalf("MonitorRequirement has field %q, which reads as a completion flag", f.Name)
		}
	}
}

// TestImposeMonitorRefusesPreAttachedCertifications: imposing and
// completing must stay distinct events, so a monitorship cannot arrive
// already certified.
func TestImposeMonitorRefusesPreAttachedCertifications(t *testing.T) {
	m := mustMatter(t)
	mr := MonitorRequirement{
		MonitorID: "MON-2", Requirement: "monitor", ImposedBy: "authority", SourceDocument: "ORDER-FIC-4",
		Certifications: []Certification{{CertifiedBy: "someone", SourceDocument: "doc"}},
	}
	if err := m.ImposeMonitor(mr); err == nil {
		t.Fatal("a monitorship must not arrive already certified")
	}
}

func TestCertificationRequiresACertifierAndSource(t *testing.T) {
	m := mustMatter(t)
	if err := m.ImposeMonitor(MonitorRequirement{
		MonitorID: "MON-3", Requirement: "monitor", ImposedBy: "authority", SourceDocument: "ORDER-FIC-5",
	}); err != nil {
		t.Fatalf("ImposeMonitor: %v", err)
	}
	if err := m.CertifyMonitor("MON-3", Certification{CertifiedBy: "x"}); !errors.Is(err, ErrCertificationIncomplete) {
		t.Fatalf("expected ErrCertificationIncomplete, got %v", err)
	}
	if err := m.CertifyMonitor("MON-NOPE", Certification{CertifiedBy: "x", SourceDocument: "y"}); !errors.Is(err, ErrMonitorNotFound) {
		t.Fatalf("expected ErrMonitorNotFound, got %v", err)
	}
	// An invalid certification must not have been attached.
	if m.Monitors()[0].Completed() {
		t.Fatal("a rejected certification must not make the monitorship complete")
	}
}

func TestMonitorRequirementNeedsItsSource(t *testing.T) {
	m := mustMatter(t)
	if err := m.ImposeMonitor(MonitorRequirement{MonitorID: "MON-4", Requirement: "r"}); !errors.Is(err, ErrMonitorNoSource) {
		t.Fatalf("expected ErrMonitorNoSource, got %v", err)
	}
	if err := m.ImposeMonitor(MonitorRequirement{Requirement: "r", ImposedBy: "a", SourceDocument: "d"}); !errors.Is(err, ErrEmptyMonitorID) {
		t.Fatalf("expected ErrEmptyMonitorID, got %v", err)
	}
}

// ---- Money -----------------------------------------------------------

func TestFineAndDisgorgementAreDistinctAndSourced(t *testing.T) {
	m := mustMatter(t)
	if err := m.RecordMonetaryOutcome(MonetaryOutcome{
		Kind: MonetaryFine, AmountMinor: 250_000_00, Currency: "USD",
		ImposedBy: "the authority", SourceDocument: "ORDER-FIC-6 s. 4", RecordedTick: 600,
	}); err != nil {
		t.Fatalf("RecordMonetaryOutcome(fine): %v", err)
	}
	if err := m.RecordMonetaryOutcome(MonetaryOutcome{
		Kind: MonetaryDisgorgement, AmountMinor: 90_000_00, Currency: "USD",
		ImposedBy: "the authority", SourceDocument: "ORDER-FIC-6 s. 5", RecordedTick: 600,
	}); err != nil {
		t.Fatalf("RecordMonetaryOutcome(disgorgement): %v", err)
	}
	got := m.MonetaryOutcomes()
	if len(got) != 2 || got[0].Kind == got[1].Kind {
		t.Fatalf("a fine and a disgorgement must stay distinct, got %+v", got)
	}
	if err := m.RecordMonetaryOutcome(MonetaryOutcome{
		Kind: MonetaryFine, AmountMinor: 1, Currency: "USD", ImposedBy: "", SourceDocument: "",
	}); !errors.Is(err, ErrMonetaryNoSource) {
		t.Fatalf("expected ErrMonetaryNoSource, got %v", err)
	}
}

// TestMoneyIsNeverFloat: the same exactness rationale
// pkg/insurance/quantum documents.
func TestMoneyIsNeverFloat(t *testing.T) {
	mt := reflect.TypeOf(MonetaryOutcome{})
	for i := 0; i < mt.NumField(); i++ {
		if k := mt.Field(i).Type.Kind(); k == reflect.Float64 || k == reflect.Float32 {
			t.Fatalf("MonetaryOutcome.%s is a float — money is exact integer minor units", mt.Field(i).Name)
		}
	}
}

// ---- Stage machine ---------------------------------------------------

func TestRegulatoryStageSkippingIsRecordedAndBackwardRefused(t *testing.T) {
	m := mustMatter(t)
	if err := m.Advance(StageMonitor, "monitor imposed under the settlement order", 700); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	log := m.StageLog()
	if len(log[len(log)-1].Skipped) == 0 {
		t.Fatal("skipped stages must be recorded")
	}
	if err := m.Advance(StageInvestigation, "reopening", 800); !errors.Is(err, ErrStageBackward) {
		t.Fatalf("expected ErrStageBackward, got %v", err)
	}
	if err := m.Advance(StageCompletion, "", 900); !errors.Is(err, ErrEmptyReason) {
		t.Fatalf("expected ErrEmptyReason, got %v", err)
	}
}

// TestCompletionBlockersNamesEveryUnfinishedObligation: reaching the
// COMPLETION stage is not the same as being complete, and the blockers
// list is what makes that visible.
func TestCompletionBlockersNamesEveryUnfinishedObligation(t *testing.T) {
	m := mustMatter(t)
	addAllegations(t, m, "still under investigation")
	if err := m.ImposeMonitor(MonitorRequirement{
		MonitorID: "MON-5", Requirement: "annual reporting", ImposedBy: "the authority",
		SourceDocument: "ORDER-FIC-7", ImposedTick: 500,
	}); err != nil {
		t.Fatalf("ImposeMonitor: %v", err)
	}
	if err := m.Advance(StageCompletion, "administrative file closure requested", 900); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	blockers := m.CompletionBlockers()
	if len(blockers) != 2 {
		t.Fatalf("expected two completion blockers (one monitor, one undetermined allegation), got %v", blockers)
	}
	joined := strings.Join(blockers, " | ")
	if !strings.Contains(joined, "MON-5") || !strings.Contains(joined, "ALG-A") {
		t.Fatalf("blockers must name the specific obligations, got %v", blockers)
	}
}

// ---- Whole-package structural guardrail ------------------------------

func TestNoTypeInThisPackageCarriesAVerdictField(t *testing.T) {
	forbidden := []string{
		"verdict", "liable", "liability", "guilty", "guilt", "fault",
		"approved", "denied", "denial", "covered", "payable", "winner",
	}
	types := []reflect.Type{
		reflect.TypeOf(Allegation{}), reflect.TypeOf(MonetaryOutcome{}),
		reflect.TypeOf(MonitorRequirement{}), reflect.TypeOf(Certification{}),
		reflect.TypeOf(StageTransition{}), reflect.TypeOf(Matter{}),
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

// TestNoOpaqueConfidenceScore mirrors the dispute package's own check.
func TestNoOpaqueConfidenceScore(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Allegation{}), reflect.TypeOf(MonitorRequirement{}),
		reflect.TypeOf(Certification{}), reflect.TypeOf(MonetaryOutcome{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			lower := strings.ToLower(f.Name)
			if f.Type.Kind() == reflect.Float64 || lower == "confidence" || lower == "score" || lower == "risk" {
				t.Fatalf("%s.%s is an opaque score — this domain decomposes evidence, it never scores it",
					typ.Name(), f.Name)
			}
		}
	}
	// The three-way decomposition is present instead.
	at := reflect.TypeOf(Allegation{})
	for _, want := range []string{"SupportingEvidence", "ContradictingEvidence", "MissingEvidence"} {
		if _, ok := at.FieldByName(want); !ok {
			t.Fatalf("Allegation is missing the %q half of the evidence decomposition", want)
		}
	}
}
