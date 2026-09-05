package boundary

import (
	"errors"
	"strings"
	"testing"
)

// TestAVerifierForTheVerifierIsRefused.
//
// The exact proposal this package exists to stop, and the one that
// will be made again next round with a good argument attached.
func TestAVerifierForTheVerifierIsRefused(t *testing.T) {
	d, err := Propose(Proposal{
		Name: "a verifier for veriqo-verify", Layer: IndependentVerification,
		Rationale: "the verifier-of-the-verifier found a real bug, so another layer " +
			"would find another one",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Verdict != Refused {
		t.Fatalf("verdict %s; the runaway this package names is permitted", d.Verdict)
	}
	if !strings.Contains(d.Instead, "engage") {
		t.Errorf("the refusal does not name who should do it instead: %q", d.Instead)
	}
}

// TestAGoodRationaleDoesNotBuyAWayPastTheBoundary.
//
// Every rung on a runaway ladder has a good rationale -- that is what
// makes it a runaway. So the rationale is recorded and must not be
// weighed.
func TestAGoodRationaleDoesNotBuyAWayPastTheBoundary(t *testing.T) {
	for _, r := range []string{
		"a customer asked for it",
		"it would take two days",
		"the last three layers each found a defect",
		"it is a direct dependency of gate G4",
	} {
		d, err := Propose(Proposal{
			Name: "another verification layer", Layer: ExternalAssessment, Rationale: r,
			ClosesExternalGate: "G4",
		})
		if err != nil {
			t.Fatal(err)
		}
		if d.Verdict != Refused {
			t.Errorf("rationale %q bought a way past the boundary", r)
		}
	}
}

// TestWorkAtAClosedLayerIsRedundantNotForbidden.
//
// The distinction matters. REFUSED means VERIQO cannot do it at all;
// REDUNDANT means VERIQO can and it would not help. Collapsing them
// would make the boundary look like a prohibition on engineering.
func TestWorkAtAClosedLayerIsRedundantNotForbidden(t *testing.T) {
	d, err := Propose(Proposal{
		Name: "a second assurance mutation suite", Layer: AssuranceOfAssurance,
		Rationale: "more operators would kill more mutants",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Verdict != Redundant {
		t.Fatalf("verdict %s, want REDUNDANT", d.Verdict)
	}
	if !strings.Contains(d.Instead, "procurement") {
		t.Errorf("the alternative does not point at what is actually blocking: %q", d.Instead)
	}
}

// TestAnExternalGateDependencyIsAllowedEvenAtAClosedLayer.
//
// The constraint is what the work unblocks, not which layer it sits
// on. A rule that refused all work at closed layers would refuse the
// evidence packaging an assessor asks for.
func TestAnExternalGateDependencyIsAllowedEvenAtAClosedLayer(t *testing.T) {
	d, err := Propose(Proposal{
		Name: "evidence export the assessor asked for", Layer: Assurance,
		Rationale:          "the pentest firm cannot start without it",
		ClosesExternalGate: "G4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Verdict != Allowed {
		t.Fatalf("verdict %s; work an external party is waiting on was refused", d.Verdict)
	}
}

// TestNoLayerAboveTheBoundaryIsSelfBuildable.
func TestNoLayerAboveTheBoundaryIsSelfBuildable(t *testing.T) {
	for _, l := range Layers() {
		want := l <= Ours
		if l.SelfBuildable() != want {
			t.Errorf("%s.SelfBuildable()=%v, want %v", l, l.SelfBuildable(), want)
		}
		if !l.SelfBuildable() && !strings.Contains(strings.ToLower(l.Party()), "party") {
			t.Errorf("%s does not name the party who must do it: %q", l, l.Party())
		}
	}
}

// TestEveryLayerStatesWhatItDoesNotEstablish.
//
// The second half of each sentence is the part that gets dropped, and
// dropping it is how "the verifier passed" becomes "the system is
// verified".
func TestEveryLayerStatesWhatItDoesNotEstablish(t *testing.T) {
	for _, l := range Layers() {
		e := l.Establishes()
		if strings.TrimSpace(e) == "" {
			t.Errorf("%s establishes nothing stated", l)
			continue
		}
		if l.SelfBuildable() && !strings.Contains(e, ";") && !strings.Contains(e, "nothing") {
			t.Errorf("%s states what it settles and not what it leaves open: %q", l, e)
		}
	}
}

// TestThisPackageIsTheLastAssuranceComponent.
//
// Deliberately uncomfortable. The argument for adding this package is
// the same argument that justifies every other rung; the only
// difference is that it terminates the ladder. If a later round cites
// it as precedent for a layer 4, it has failed, and this test is where
// that failure shows up.
func TestThisPackageIsTheLastAssuranceComponent(t *testing.T) {
	if Depth() != Ours {
		t.Fatalf("the repository occupies layer %s, above the boundary at %s", Depth(), Ours)
	}
	if _, closed := KernelClosed[Ours]; !closed {
		t.Fatal("the top kernel layer is not marked closed, so more may be added to it")
	}
	if len(KernelClosed) != int(Ours)+1 {
		t.Fatalf("%d kernel layers are closed, want %d; an open kernel layer is an "+
			"invitation", len(KernelClosed), int(Ours)+1)
	}
}

// TestAProposalWithoutARationaleIsRefusedOutright.
func TestAProposalWithoutARationaleIsRefusedOutright(t *testing.T) {
	if _, err := Propose(Proposal{Name: "x", Layer: Assurance}); err == nil {
		t.Fatal("a proposal with no rationale was weighed")
	}
	if _, err := Propose(Proposal{Layer: Assurance, Rationale: "y"}); err == nil {
		t.Fatal("a proposal with no name was weighed")
	}
	if _, err := Propose(Proposal{Name: "x", Layer: Layer(99), Rationale: "y"}); !errors.Is(err, ErrUnknownLayer) {
		t.Fatalf("layer 99 was accepted: %v", err)
	}
}

// TestTheReportDrawsTheBoundaryWhereItIs.
func TestTheReportDrawsTheBoundaryWhereItIs(t *testing.T) {
	r := Report()
	if !strings.Contains(r, "THE BOUNDARY") {
		t.Error("the report does not mark the boundary")
	}
	if !strings.Contains(r, "reachable by writing Go") {
		t.Error("the report does not say why the line cannot be moved by engineering")
	}
	for _, l := range Layers() {
		if !strings.Contains(r, l.String()) {
			t.Errorf("the report omits %s", l)
		}
	}
	if Report() != Report() {
		t.Error("Report() is not deterministic")
	}
	for _, line := range strings.Split(r, "\n") {
		if len([]rune(line)) > 78 {
			t.Errorf("a %d-column line will wrap: %q", len([]rune(line)), line)
		}
	}
}
