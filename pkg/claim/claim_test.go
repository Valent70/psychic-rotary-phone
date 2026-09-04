package claim

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
)

func d(y int) time.Time         { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
func to(t time.Time) *time.Time { return &t }

func versions() contract.VersionSet {
	return contract.VersionSet{
		Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
		Policy:    contract.Version{Component: "baseline", Revision: 1},
		Algorithm: contract.Version{Component: "claim", Revision: 1},
	}
}

func base() Claim {
	return Claim{
		ID: "claim:c1", TenantID: "t-acme", CaseID: "case-1",
		Statement: "the cargo discharged was 1,800 MT short of the bill of lading quantity",
		Scope: Scope{Subject: "CargoLot L-77", Aspect: "quantity",
			Period: contract.Interval{From: d(2024), To: to(d(2025))}},
		SupportingEvidence:    []string{"ev:discharge-survey", "ev:bl"},
		AlternativeHypotheses: []string{"measurement basis differed", "loading quantity overstated"},
		DisproofPath: "a certified density measurement showing the loading and discharge " +
			"bases are not comparable would remove the arithmetic the claim rests on",
		Status:   Supported,
		Versions: versions(),
	}
}

// TestAClaimWithNoDisproofPathIsRefused is Law 4. It applies to every
// claim, not only established ones.
func TestAClaimWithNoDisproofPathIsRefused(t *testing.T) {
	c := base()
	c.DisproofPath = ""
	if err := c.Validate(); !errors.Is(err, ErrNoDisproofPath) {
		t.Fatalf("a claim nobody could falsify was accepted: %v", err)
	}
	// Including a merely unresolved one.
	c.Status = Unresolved
	c.SupportingEvidence = nil
	c.AlternativeHypotheses = nil
	if err := c.Validate(); !errors.Is(err, ErrNoDisproofPath) {
		t.Fatalf("an UNRESOLVED claim escaped the disproof requirement: %v", err)
	}
}

// TestMissingIsNotContradicting is Law 5, at the point it is most
// often violated.
func TestMissingIsNotContradicting(t *testing.T) {
	c := base()
	c.MissingEvidence = []string{"ev:loading-survey"}
	c.ContradictingEvidence = []string{"ev:loading-survey"}
	c.Status = PartiallySupported
	if err := c.Validate(); !errors.Is(err, ErrMissingIsNotContra) {
		t.Fatalf("an item was both missing and contradicting: %v", err)
	}
}

// TestNotFindingSomethingDoesNotChangeTheStatus. An absence is not a
// finding, so recording one must not move the claim.
func TestNotFindingSomethingDoesNotChangeTheStatus(t *testing.T) {
	c := base()
	before := c.Status
	c = c.RecordMissing("ev:tank-calibration-certificate")
	if c.Status != before {
		t.Fatalf("recording a missing item moved the status from %s to %s", before, c.Status)
	}
	if len(c.MissingEvidence) != 1 {
		t.Fatalf("the missing item was not recorded: %v", c.MissingEvidence)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("a claim with a recorded absence is invalid: %v", err)
	}
}

// TestContradictingEvidenceDemotesAndDoesSoAutomatically.
//
// This is the half most systems leave out: the counter-evidence is
// recorded and the conclusion stays SUPPORTED because demoting was
// somebody's job to remember.
func TestContradictingEvidenceDemotesAndDoesSoAutomatically(t *testing.T) {
	c := base()
	out, err := c.RecordContradiction("ev:port-scale-record",
		"the terminal's own weighbridge agrees with the bill of lading")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status == Supported {
		t.Fatal("A CONTRADICTION WAS RECORDED AND THE CLAIM STAYED SUPPORTED")
	}
	if out.Status != PartiallySupported {
		t.Fatalf("status = %s, want PARTIALLY_SUPPORTED", out.Status)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("the demoted claim is invalid: %v", err)
	}
	// And the limitation records why.
	if !strings.Contains(strings.Join(out.Limitations, " "), "ev:port-scale-record") {
		t.Fatalf("the demotion does not name its cause: %v", out.Limitations)
	}
}

// TestAContradictedClaimWithNoSupportBecomesContradicted.
func TestAContradictedClaimWithNoSupportBecomesContradicted(t *testing.T) {
	c := base()
	c.SupportingEvidence = []string{"ev:discharge-survey"}
	out, err := c.RecordContradiction("ev:discharge-survey", "the survey was withdrawn by its author")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Contradicted {
		t.Fatalf("status = %s; the only supporting item became contradicting", out.Status)
	}
	if len(out.SupportingEvidence) != 0 {
		t.Fatalf("the item is still listed as supporting: %v", out.SupportingEvidence)
	}
}

// TestValidateRefusesASupportedClaimCarryingContradictions, in case a
// caller constructs the struct directly rather than going through
// RecordContradiction.
func TestValidateRefusesASupportedClaimCarryingContradictions(t *testing.T) {
	c := base()
	c.ContradictingEvidence = []string{"ev:port-scale-record"}
	if err := c.Validate(); !errors.Is(err, ErrContradictedHeld) {
		t.Fatalf("a hand-built SUPPORTED claim carried contradictions: %v", err)
	}
}

// TestAnEstablishedClaimMustHaveConsideredAnAlternative. A conclusion
// nobody tried to explain differently has been assembled, not tested.
func TestAnEstablishedClaimMustHaveConsideredAnAlternative(t *testing.T) {
	c := base()
	c.AlternativeHypotheses = nil
	if err := c.Validate(); err == nil {
		t.Fatal("an established claim with no alternative hypothesis was accepted")
	}
	// An inconclusive claim need not: there is nothing to explain yet.
	c.Status = Inconclusive
	if err := c.Validate(); err != nil {
		t.Fatalf("an INCONCLUSIVE claim was required to name alternatives: %v", err)
	}
}

// TestQualifiedRequiresItsLimits. QUALIFIED without limits reads as
// SUPPORTED to every reader in a hurry.
func TestQualifiedRequiresItsLimits(t *testing.T) {
	c := base()
	c.Status = Qualified
	c.Limitations = nil
	if err := c.Validate(); !errors.Is(err, ErrUnstatedLimits) {
		t.Fatalf("QUALIFIED with no limits was accepted: %v", err)
	}
	c.Limitations = []string{"the discharge survey covers one tank of three"}
	if err := c.Validate(); err != nil {
		t.Fatalf("a properly limited QUALIFIED claim was refused: %v", err)
	}
}

// TestInconclusiveIsNotUnresolved. One is a finding after work, the
// other is an absence of work, and a customer needs to know which.
func TestInconclusiveIsNotUnresolved(t *testing.T) {
	if !Inconclusive.RestsOnWork() {
		t.Fatal("INCONCLUSIVE is classified as an absence of work")
	}
	if Unresolved.RestsOnWork() || NotDetermined.RestsOnWork() {
		t.Fatal("UNRESOLVED or NOT_DETERMINED is classified as resting on work")
	}
	if Inconclusive.Establishes() {
		t.Fatal("INCONCLUSIVE establishes a conclusion")
	}
	// And the two produce different answers to "why not supported".
	a := base()
	a.Status = Inconclusive
	b := base()
	b.Status = Unresolved
	if a.WhyNot()["why not supported"] == b.WhyNot()["why not supported"] {
		t.Fatal("INCONCLUSIVE and UNRESOLVED give the same explanation")
	}
}

// TestTheZeroStatusIsNotDetermined. A claim nobody has looked at must
// not read as an established one.
func TestTheZeroStatusIsNotDetermined(t *testing.T) {
	var s Status
	if s.Establishes() {
		t.Fatal("the zero status establishes a conclusion")
	}
	if s.String() != "NOT_DETERMINED" {
		t.Fatalf("the zero status renders as %q", s.String())
	}
}

// TestThereIsNoPromotion. A claim rises by re-qualification, not by
// relabelling.
func TestThereIsNoPromotion(t *testing.T) {
	c := base()
	c.Status = Inconclusive
	c.SupportingEvidence = nil
	c.AlternativeHypotheses = nil
	if _, err := c.Demote(Supported, "it looks right now"); err == nil {
		t.Fatal("a claim was promoted by a status assignment")
	}
	// Demotion works and records why.
	c.Status = Supported
	c.SupportingEvidence = []string{"ev:x"}
	c.AlternativeHypotheses = []string{"alt"}
	out, err := c.Demote(Inconclusive, "the survey basis could not be established")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Inconclusive {
		t.Fatalf("status = %s", out.Status)
	}
	if !strings.Contains(strings.Join(out.Limitations, " "), "demoted from SUPPORTED") {
		t.Fatalf("the demotion is not recorded: %v", out.Limitations)
	}
}

// TestWhatWouldChangeOurMindIsAlwaysAnswerable. Every claim carries a
// disproof path, so this can never be empty.
func TestWhatWouldChangeOurMindIsAlwaysAnswerable(t *testing.T) {
	c := base()
	c = c.RecordMissing("ev:tank-calibration")
	out := c.WhatWouldChangeOurMind()
	for _, want := range []string{"Current:", "Would change this conclusion:",
		"certified density", "Not found", "Alternatives considered"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output omits %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "which is not the same as absent") {
		t.Fatalf("the missing-evidence line does not preserve Law 5:\n%s", out)
	}
}

// TestAMaterialConclusionCitesEvidence is Law 1.
func TestAMaterialConclusionCitesEvidence(t *testing.T) {
	c := base()
	c.SupportingEvidence = nil
	if err := c.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("a SUPPORTED claim with no evidence was accepted: %v", err)
	}
}

// TestAnUnversionedClaimIsRefused.
func TestAnUnversionedClaimIsRefused(t *testing.T) {
	c := base()
	c.Versions = contract.VersionSet{}
	if err := c.Validate(); !errors.Is(err, contract.ErrUnversioned) {
		t.Fatalf("an unversioned claim was accepted: %v", err)
	}
}

// TestScopeIsMandatory: without it, "the cargo was short" reads as a
// statement about every voyage.
func TestScopeIsMandatory(t *testing.T) {
	c := base()
	c.Scope = Scope{}
	if err := c.Validate(); !errors.Is(err, ErrNoScope) {
		t.Fatalf("a claim with no scope was accepted: %v", err)
	}
	c.Scope = Scope{Subject: "CargoLot L-77"}
	if err := c.Validate(); !errors.Is(err, ErrNoScope) {
		t.Fatalf("a claim with no period was accepted: %v", err)
	}
}

// TestTheSameItemCannotSupportAndContradict.
func TestTheSameItemCannotSupportAndContradict(t *testing.T) {
	c := base()
	c.ContradictingEvidence = []string{"ev:bl"}
	c.Status = PartiallySupported
	if err := c.Validate(); !errors.Is(err, ErrOverlappingSets) {
		t.Fatalf("one item was listed as both supporting and contradicting: %v", err)
	}
}

// TestRecordContradictionRefusesAMissingItem: not finding something is
// not finding the opposite, even when a caller passes it in.
func TestRecordContradictionRefusesAMissingItem(t *testing.T) {
	c := base().RecordMissing("ev:loading-survey")
	if _, err := c.RecordContradiction("ev:loading-survey", "it was never produced"); !errors.Is(err, ErrMissingIsNotContra) {
		t.Fatalf("a missing item was promoted to a contradiction: %v", err)
	}
}

// TestTheDigestCoversTheContradictionsAndLimits. If they sat outside,
// a claim's counter-evidence could be edited after it was anchored.
func TestTheDigestCoversTheContradictionsAndLimits(t *testing.T) {
	c := base()
	c.Status = Qualified
	c.Limitations = []string{"one tank of three"}
	base1, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	c2 := c
	c2.Limitations = []string{"all three tanks"}
	got, err := c2.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if got == base1 {
		t.Fatal("editing the limitations did not change the claim digest")
	}
	c3, _ := c.RecordContradiction("ev:x", "reason")
	c3.AlternativeHypotheses = c.AlternativeHypotheses
	g3, err := c3.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if g3 == base1 {
		t.Fatal("recording a contradiction did not change the claim digest")
	}
}
