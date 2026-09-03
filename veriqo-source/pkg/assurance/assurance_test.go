package assurance

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/constitution"
)

// full is a trace with every link satisfied.
func full() Trace {
	return Trace{
		Article: 1, Control: "a fully traced control",
		Code: true, CodeRef: "pkg/x",
		Called: true, CalledRef: "pkg/y",
		Test: true, TestRef: "TestX",
		Evidence: true, EvidenceRef: "event family x",
		Replay: true, ReplayRef: "pkg/replay",
		Qualification: true, QualificationRef: "docs/assessment.md",
		ExternalProof: true, ExternalProofRef: "assessor-ltd ref 99",
	}
}

// --- The two axes ----------------------------------------------------

// TestTheAxesDoNotCollapse is the property the package exists for:
// there is no way to reduce a Status to one figure.
func TestTheAxesDoNotCollapse(t *testing.T) {
	s := Status{Capability: "x", Engineering: ReplayVerified, Assurance: Unproved, Blocker: "nobody has looked"}
	str := s.String()
	if !strings.Contains(str, "engineering=") || !strings.Contains(str, "assurance=") {
		t.Fatalf("String must render both axes, got %q", str)
	}
	if !s.EngineeringCompleteButUnassured() {
		t.Fatal("fully engineered and unexamined is exactly the case this flags")
	}
}

func TestZeroLevelsAreTheHonestDefaults(t *testing.T) {
	var e EngineeringLevel
	var a AssuranceLevel
	if e != NotStarted || e.String() != "NOT_STARTED" {
		t.Fatalf("the zero EngineeringLevel must be NOT_STARTED, got %s", e)
	}
	if a != Unproved || a.String() != "UNPROVED" {
		t.Fatalf("the zero AssuranceLevel must be UNPROVED, got %s", a)
	}
	if a.RequiresOutsideParty() {
		t.Fatal("UNPROVED does not require an outside party; it requires anything at all")
	}
}

// TestOnlyExternalLevelsRequireAnOutsideParty is the honest boundary:
// no amount of further engineering reaches them.
func TestOnlyExternalLevelsRequireAnOutsideParty(t *testing.T) {
	for _, a := range []AssuranceLevel{Unproved, SelfAsserted, InternallyProved} {
		if a.RequiresOutsideParty() {
			t.Fatalf("%s is reachable by VERIQO alone", a)
		}
	}
	for _, a := range []AssuranceLevel{ExternallyValidated, ProductionQualified} {
		if !a.RequiresOutsideParty() {
			t.Fatalf("%s requires somebody who is not VERIQO", a)
		}
	}
}

// TestUnassuredStatusMustNameItsBlocker: "not yet" and "nobody exists to
// ask" are different situations and must be distinguished.
func TestUnassuredStatusMustNameItsBlocker(t *testing.T) {
	if err := (Status{Capability: "x", Engineering: ReplayVerified, Assurance: InternallyProved}).Validate(); err == nil {
		t.Fatal("an internally proved status with no named blocker must be refused")
	}
	if err := (Status{Capability: "", Assurance: ExternallyValidated}).Validate(); err == nil {
		t.Fatal("a status naming no capability must be refused")
	}
	if err := (Status{Capability: "x", Assurance: ExternallyValidated}).Validate(); err != nil {
		t.Fatalf("an externally validated status needs no blocker: %v", err)
	}
}

func TestEveryDeclaredCapabilityIsValid(t *testing.T) {
	for _, s := range Capabilities() {
		if err := s.Validate(); err != nil {
			t.Fatalf("capability %q: %v", s.Capability, err)
		}
	}
}

// TestNoCapabilityClaimsExternalValidation is this round's honest
// headline, asserted so it cannot drift without a test failing.
func TestNoCapabilityClaimsExternalValidation(t *testing.T) {
	for _, s := range Capabilities() {
		if s.Assurance >= ExternallyValidated {
			t.Fatalf("capability %q claims %s: no outside party has examined anything, so this needs a reference",
				s.Capability, s.Assurance)
		}
	}
}

func TestAxisReportStatesBothAxesAndRefusesOneFigure(t *testing.T) {
	r := AxisReport()
	if !strings.Contains(r, "ENGINEERING") || !strings.Contains(r, "ASSURANCE") {
		t.Fatal("the axis report must show both axes")
	}
	if !strings.Contains(r, "neither substitutes for the other") {
		t.Fatal("the axis report must say the axes do not substitute")
	}
	if strings.Contains(r, "%") {
		t.Fatal("the axis report must not express readiness as a percentage")
	}
}

// --- Verdicts --------------------------------------------------------

// TestZeroVerdictIsOpen: a control nobody traced is open, never
// qualified.
func TestZeroVerdictIsOpen(t *testing.T) {
	var v Verdict
	if v != Open || v.String() != "OPEN" {
		t.Fatalf("the zero Verdict must be OPEN, got %s", v)
	}
	if v.Closed() {
		t.Fatal("OPEN is not closed")
	}
}

// TestOnlyQualifiedIsClosed: EXTERNAL_QUALIFICATION is blocked, not done.
func TestOnlyQualifiedIsClosed(t *testing.T) {
	for _, v := range []Verdict{Open, IntegrationGap, AssuranceGap, ExternalQualification} {
		if v.Closed() {
			t.Fatalf("%s must not read as closed", v)
		}
	}
	if !Qualified.Closed() {
		t.Fatal("QUALIFIED is closed")
	}
}

func TestVerdictRules(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Trace)
		want Verdict
	}{
		{"no code at all", func(tr *Trace) { *tr = Trace{Article: 1, Control: "c"} }, Open},
		{"code but nothing calls it", func(tr *Trace) {
			tr.Called, tr.CalledRef = false, ""
			tr.Test, tr.Evidence, tr.Replay, tr.Qualification, tr.ExternalProof = false, false, false, false, false
		}, IntegrationGap},
		{"called but untested", func(tr *Trace) {
			tr.Test, tr.TestRef = false, ""
			tr.Evidence, tr.Replay, tr.Qualification, tr.ExternalProof = false, false, false, false
		}, AssuranceGap},
		{"tested but no production evidence", func(tr *Trace) {
			tr.Evidence, tr.EvidenceRef = false, ""
			tr.Replay, tr.Qualification, tr.ExternalProof = false, false, false
		}, AssuranceGap},
		{"recorded but not replayable", func(tr *Trace) {
			tr.Replay, tr.ReplayRef = false, ""
			tr.Qualification, tr.ExternalProof = false, false
		}, AssuranceGap},
		{"replayable but never assessed", func(tr *Trace) {
			tr.Qualification, tr.QualificationRef = false, ""
			tr.ExternalProof, tr.ExternalProofRef = false, ""
		}, AssuranceGap},
		{"assessed but nobody outside looked", func(tr *Trace) {
			tr.ExternalProof, tr.ExternalProofRef = false, ""
			tr.ExternalDependency = "an accredited assessor is required"
		}, ExternalQualification},
		{"complete", func(tr *Trace) {}, Qualified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := full()
			tc.mut(&tr)
			got, err := Assess(tr)
			if err != nil {
				t.Fatalf("Assess: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, got)
			}
		})
	}
}

// TestExternalQualificationMustNameItsDependency: "waiting on someone"
// with no name is itself a gap.
func TestExternalQualificationMustNameItsDependency(t *testing.T) {
	tr := full()
	tr.ExternalProof, tr.ExternalProofRef = false, ""
	v, err := Assess(tr)
	if v != ExternalQualification {
		t.Fatalf("expected EXTERNAL_QUALIFICATION, got %s", v)
	}
	if !errors.Is(err, ErrNoDependency) {
		t.Fatalf("an unnamed external dependency must be reported, got %v", err)
	}
	if !strings.Contains(Explain(tr), "not named") {
		t.Fatalf("Explain must say the dependency is unnamed, got %q", Explain(tr))
	}
}

// --- Trace validation ------------------------------------------------

// TestAssertedLinkNeedsAReference is what keeps the matrix from becoming
// a spreadsheet of good intentions.
func TestAssertedLinkNeedsAReference(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Trace)
	}{
		{"code", func(tr *Trace) { tr.CodeRef = "" }},
		{"called", func(tr *Trace) { tr.CalledRef = "" }},
		{"test", func(tr *Trace) { tr.TestRef = "" }},
		{"evidence", func(tr *Trace) { tr.EvidenceRef = "" }},
		{"replay", func(tr *Trace) { tr.ReplayRef = "" }},
		{"qualification", func(tr *Trace) { tr.QualificationRef = "" }},
		{"external proof", func(tr *Trace) { tr.ExternalProofRef = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := full()
			tc.mut(&tr)
			if _, err := Assess(tr); !errors.Is(err, ErrUnreferenced) {
				t.Fatalf("an unreferenced %s link must be refused, got %v", tc.name, err)
			}
		})
	}
}

// TestImpossibleChainsAreRefused: production evidence for a control
// nothing calls is a contradiction, not a strong result.
func TestImpossibleChainsAreRefused(t *testing.T) {
	tr := full()
	tr.Code, tr.CodeRef = false, ""
	if _, err := Assess(tr); !errors.Is(err, ErrImpossibleChain) {
		t.Fatalf("downstream links with no code must be refused, got %v", err)
	}

	tr2 := full()
	tr2.Called, tr2.CalledRef = false, ""
	if _, err := Assess(tr2); !errors.Is(err, ErrImpossibleChain) {
		t.Fatalf("production evidence for an uncalled control must be refused, got %v", err)
	}
}

func TestTraceRequiresAKnownArticleAndAControl(t *testing.T) {
	tr := full()
	tr.Article = 31
	if _, err := Assess(tr); !errors.Is(err, ErrUnknownArticle) {
		t.Fatalf("expected ErrUnknownArticle, got %v", err)
	}
	tr2 := full()
	tr2.Control = "  "
	if _, err := Assess(tr2); !errors.Is(err, ErrNoControl) {
		t.Fatalf("expected ErrNoControl, got %v", err)
	}
}

// --- The matrix ------------------------------------------------------

// TestMatrixCoversEveryArticle is the completeness guarantee: an article
// nobody traced appears as OPEN rather than vanishing from the report.
func TestMatrixCoversEveryArticle(t *testing.T) {
	rows, err := Assemble()
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(rows) != len(constitution.Articles()) {
		t.Fatalf("expected %d rows, got %d", len(constitution.Articles()), len(rows))
	}
	for i, r := range rows {
		if r.Article != i+1 {
			t.Fatalf("row %d is article %d: the matrix must be ordered and complete", i, r.Article)
		}
		if r.Title == "" || r.Explanation == "" {
			t.Fatalf("article %d has an empty row: %+v", r.Article, r)
		}
	}
}

// TestEveryMatrixTraceIsWellFormed catches an unreferenced or
// contradictory claim in the shipped matrix itself.
func TestEveryMatrixTraceIsWellFormed(t *testing.T) {
	for _, tr := range Matrix() {
		if err := tr.Validate(); err != nil {
			t.Fatalf("article %d: %v", tr.Article, err)
		}
		if _, err := Assess(tr); err != nil && !errors.Is(err, ErrNoDependency) {
			t.Fatalf("article %d: %v", tr.Article, err)
		}
	}
}

// TestEveryIncompleteTraceNamesItsBlocker: no row may say "not done"
// without saying what is in the way.
func TestEveryIncompleteTraceNamesItsBlocker(t *testing.T) {
	for _, tr := range Matrix() {
		v, _ := Assess(tr)
		if v == Qualified {
			continue
		}
		if strings.TrimSpace(tr.ExternalDependency) == "" {
			t.Fatalf("article %d is %s and names no dependency or blocker", tr.Article, v)
		}
	}
}

// TestNothingIsQualified records the round's honest position. It is
// asserted rather than written in prose so it cannot quietly change:
// the day an outside party examines a control, this test fails and
// somebody has to update the claim deliberately.
func TestNothingIsQualified(t *testing.T) {
	rows, _ := Assemble()
	s := Summarize(rows)
	if s.Qualified != 0 {
		t.Fatalf("%d articles claim QUALIFIED: an outside party's examination must be referenced", s.Qualified)
	}
	if s.Total != 30 {
		t.Fatalf("expected 30 articles, got %d", s.Total)
	}
	if s.Open+s.IntegrationGap+s.AssuranceGap+s.ExternalQualification+s.Qualified != s.Total {
		t.Fatal("the verdict counts must partition the articles")
	}
}

// TestArticlesWithNoRuntimeEnforcementAreOpen names the three the
// architecture knows are not enforced, so the count cannot drift
// silently in either direction.
func TestArticlesWithNoRuntimeEnforcementAreOpen(t *testing.T) {
	rows, _ := Assemble()
	open := map[int]bool{}
	for _, r := range rows {
		if r.Verdict == Open {
			open[r.Article] = true
		}
	}
	for _, n := range []int{9, 15, 18} {
		if !open[n] {
			t.Fatalf("article %d has no runtime enforcement and must be OPEN", n)
		}
	}
	if len(open) != 3 {
		t.Fatalf("expected exactly three OPEN articles, got %d: %v", len(open), open)
	}
}

func TestHeadlineIsNotAPercentage(t *testing.T) {
	rows, _ := Assemble()
	h := Summarize(rows).Headline()
	if strings.Contains(h, "%") {
		t.Fatalf("the headline must not be a completion percentage: %q", h)
	}
	if !strings.Contains(h, "QUALIFIED requires an outside party") {
		t.Fatalf("the headline must state what QUALIFIED means: %q", h)
	}
}

func TestRenderProducesOneLinePerArticle(t *testing.T) {
	rows, _ := Assemble()
	out := Render(rows)
	for _, r := range rows {
		if !strings.Contains(out, r.Verdict.String()) {
			t.Fatalf("article %d's verdict is missing from the render", r.Article)
		}
	}
	if strings.Count(out, "\n") < len(rows) {
		t.Fatal("the render must have at least one line per article")
	}
}

// TestExplainDistinguishesTheReasons proves the explanation earns its
// place: two AssuranceGap rows for different reasons read differently.
func TestExplainDistinguishesTheReasons(t *testing.T) {
	noTest := full()
	noTest.Test, noTest.TestRef = false, ""
	noTest.Evidence, noTest.Replay, noTest.Qualification, noTest.ExternalProof = false, false, false, false

	noReplay := full()
	noReplay.Replay, noReplay.ReplayRef = false, ""
	noReplay.Qualification, noReplay.ExternalProof = false, false

	a, b := Explain(noTest), Explain(noReplay)
	if a == b {
		t.Fatal("two assurance gaps with different causes must explain differently")
	}
	if !strings.Contains(a, "nothing demonstrates") {
		t.Fatalf("the untested case should say so, got %q", a)
	}
	if !strings.Contains(b, "cannot be reproduced") {
		t.Fatalf("the unreplayable case should say so, got %q", b)
	}
}
