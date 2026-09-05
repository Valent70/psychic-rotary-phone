package readiness

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/assurance/state"
)

func profile(t *testing.T) *Profile {
	t.Helper()
	p, err := Veriqo()
	if err != nil {
		t.Fatalf("the profile does not build: %v", err)
	}
	return p
}

// TestTheStatusNamesTheBlockingPartyRatherThanAPosition.
//
// A level invites "how much further"; that is the right question for
// work the builder can do and the wrong one for everything else,
// because the answer is not "further" but "somebody else".
func TestTheStatusNamesTheBlockingPartyRatherThanAPosition(t *testing.T) {
	want := map[Dimension]Status{
		Architecture: InternallyAssured, Semantics: InternallyAssured,
		Implementation: InternallyAssured,
		Security:       PendingExternal, Cryptography: PendingExternal,
		Legal:      PendingCounsel,
		DataRights: PendingPartner,
		Operations: NotYetProven,
		Production: NotQualified,
	}
	byDim := map[Dimension]Assessment{}
	for _, a := range profile(t).All() {
		byDim[a.Dimension] = a
	}
	for d, w := range want {
		if byDim[d].Status != w {
			t.Fatalf("%s is %s, want %s. If a party has now acted, they must be named and "+
				"this test changed deliberately", d, byDim[d].Status, w)
		}
	}
	if len(Dimensions()) != 9 {
		t.Fatalf("%d dimensions", len(Dimensions()))
	}
}

// TestThereIsNoAggregateScore. The absence is the design.
func TestThereIsNoAggregateScore(t *testing.T) {
	r := profile(t).Report()
	if strings.Contains(r, "%") {
		t.Fatalf("a percentage reached the readiness report:\n%s", r)
	}
	if strings.Contains(strings.ToLower(r), "score") {
		t.Fatal("the report offers a score")
	}
	if !strings.Contains(profile(t).Sentence(), "not more of the same work") {
		t.Fatalf("the summary does not say why no figure is offered: %s",
			profile(t).Sentence())
	}
}

// TestTheReportAnswersWhoDoWeNeed.
//
// This is the readiness answer that is actually useful. A percentage
// cannot express "the remaining work is a procurement problem".
func TestTheReportAnswersWhoDoWeNeed(t *testing.T) {
	p := profile(t)
	blocked := p.BlockedOn()
	for _, who := range []string{
		"an independent assessor", "legal counsel, per jurisdiction",
		"a commercial partner",
	} {
		if len(blocked[who]) == 0 {
			t.Fatalf("no dimension is blocked on %q", who)
		}
	}
	r := p.Report()
	if !strings.Contains(r, "WHO WE NEED") {
		t.Fatalf("the report does not group by blocking party:\n%s", r)
	}
	// Security and cryptography are separated because they are
	// different procurements, and the report must show that.
	if len(blocked["an independent assessor"]) < 2 {
		t.Fatal("security and cryptography have collapsed into one row; they are blocked " +
			"on different specialists")
	}
}

// TestADimensionNeedingAnOutsidePartyCannotBeSelfAssessedAsAssured.
func TestADimensionNeedingAnOutsidePartyCannotBeSelfAssessedAsAssured(t *testing.T) {
	for _, d := range []Dimension{Security, Cryptography, Legal, DataRights,
		Operations, Production} {
		a := Assessment{Dimension: d, Status: InternallyAssured, Basis: "we are confident",
			AssessedBy: "VERIQO engineering", Needs: []string{"nothing we can see"}}
		if err := a.Validate(); !errors.Is(err, ErrSelfAssessed) {
			t.Fatalf("%s was self-assessed as INTERNALLY_ASSURED: %v", d, err)
		}
	}
	// QUALIFIED is unreachable from inside on any dimension.
	a := Assessment{Dimension: Architecture, Status: Qualified, Basis: "done",
		AssessedBy: "VERIQO engineering"}
	if err := a.Validate(); !errors.Is(err, ErrSelfAssessed) {
		t.Fatalf("the builder recorded QUALIFIED: %v", err)
	}
	a.External = true
	a.AssessedBy = "Acme Security"
	if err := a.Validate(); err != nil {
		t.Fatalf("an external QUALIFIED assessment was refused: %v", err)
	}
}

// TestAnUnsettledStatusMustNameWhatWouldSettleIt.
//
// A status with no stated need is a complaint.
func TestAnUnsettledStatusMustNameWhatWouldSettleIt(t *testing.T) {
	a := Assessment{Dimension: Security, Status: PendingExternal,
		Basis: "waiting", AssessedBy: "VERIQO engineering"}
	if err := a.Validate(); err == nil {
		t.Fatal("an unsettled dimension named nothing that would settle it")
	}
	for _, x := range profile(t).All() {
		if x.Status.Settled() {
			continue
		}
		if len(x.Needs) == 0 {
			t.Fatalf("%s names no need", x.Dimension)
		}
		for _, n := range x.Needs {
			if len(strings.Fields(n)) < 3 {
				t.Fatalf("%s has a need too short to act on: %q", x.Dimension, n)
			}
		}
	}
}

// TestEveryDimensionIsAssessed. An omitted dimension is one nobody
// looked at, and omitting it is how the weakest axis disappears.
func TestEveryDimensionIsAssessed(t *testing.T) {
	all := profile(t).All()
	if len(all) != 9 {
		t.Fatalf("%d dimensions assessed", len(all))
	}
	if _, err := New(all[0], all[1]); err == nil {
		t.Fatal("a profile with seven dimensions missing was accepted")
	}
	if _, err := New(append(all, all[0])...); err == nil {
		t.Fatal("a dimension assessed twice was accepted")
	}
}

// TestNothingIsExternallyAssessedAndTheReportSaysSo.
func TestNothingIsExternallyAssessedAndTheReportSaysSo(t *testing.T) {
	p := profile(t)
	if p.ExternallyTouched() {
		t.Fatal("a dimension claims an external assessment; the assessor must be named " +
			"and this test changed deliberately")
	}
	if !strings.Contains(p.Report(),
		"No dimension has been assessed by anybody outside the builder") {
		t.Fatalf("the report does not state the absence:\n%s", p.Report())
	}
	for _, a := range p.All() {
		if a.MaxAssuranceState > state.InternallyAssured {
			t.Fatalf("%s reports a control above INTERNALLY_ASSURED", a.Dimension)
		}
	}
}

// TestNothingRemainingIsMovableByTheBuilderAlone.
//
// The substantive claim: every unsettled dimension needs a party. It
// should fail loudly the moment that changes in either direction.
func TestNothingRemainingIsMovableByTheBuilderAlone(t *testing.T) {
	p := profile(t)
	if r := p.SelfReachableRemaining(); len(r) != 0 {
		t.Fatalf("dimensions still movable by the builder alone: %v", r)
	}
	if !strings.Contains(p.Report(), "Nothing remaining is movable by the builder alone") {
		t.Fatalf("the report does not say so:\n%s", p.Report())
	}
}

// TestTheStatusVocabularyDistinguishesWaitingFromNotHavingRun.
//
// NOT_YET_PROVEN and the PENDING statuses are different situations:
// one is waiting on a party, the other on infrastructure and time.
func TestTheStatusVocabularyDistinguishesWaitingFromNotHavingRun(t *testing.T) {
	if NotYetProven.BlockedOn() == PendingExternal.BlockedOn() {
		t.Fatal("NOT_YET_PROVEN and PENDING_EXTERNAL name the same blocker")
	}
	if !strings.Contains(NotYetProven.BlockedOn(), "not a party") {
		t.Fatalf("NOT_YET_PROVEN does not say nobody is being waited on: %q",
			NotYetProven.BlockedOn())
	}
	for _, s := range []Status{PendingExternal, PendingCounsel, PendingPartner} {
		if s.BlockedOn() == "" {
			t.Fatalf("%s names no party", s)
		}
		if s.SelfReachable() {
			t.Fatalf("%s was marked self-reachable", s)
		}
	}
	if NotSpecified != "NOT_SPECIFIED" {
		t.Fatal("the zero-ish status is not NOT_SPECIFIED")
	}
	if len(Statuses()) != 9 {
		t.Fatalf("%d statuses", len(Statuses()))
	}
}
