package selfdoubt

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/qualification/ledger"
)

// TestTheRegisterValidates. Every claim must carry both paths.
func TestTheRegisterValidates(t *testing.T) {
	if _, err := NewRegister(Claims...); err != nil {
		t.Fatal(err)
	}
}

// TestNoClaimMayLackADisproofPath is the principle itself.
func TestNoClaimMayLackADisproofPath(t *testing.T) {
	c := Claim{ID: "X", Assertion: "something holds", ProofPath: "a test passes"}
	if err := c.Validate(); !errors.Is(err, ErrNoDisproofPath) {
		t.Fatalf("want ErrNoDisproofPath, got %v", err)
	}
}

// TestADisproofPathThatRepeatsTheProofPathIsRefused. Restating the
// proof is the most natural way to satisfy this requirement without
// meeting it, and it would leave the claim unattacked while looking
// attacked.
func TestADisproofPathThatRepeatsTheProofPathIsRefused(t *testing.T) {
	c := Claim{ID: "X", Assertion: "something holds",
		ProofPath: "the integration test passes", DisproofPath: "The Integration Test  Passes"}
	if err := c.Validate(); !errors.Is(err, ErrPathsIdentical) {
		t.Fatalf("want ErrPathsIdentical, got %v", err)
	}
}

// TestACounterexampleMustDemote is the half of the diagram most systems
// leave out: finding one and carrying on.
func TestACounterexampleMustDemote(t *testing.T) {
	c := Claim{ID: "X", Assertion: "a", ProofPath: "p", DisproofPath: "d",
		Counterexample: "a case where it fails", Outcome: Established, Level: ledger.Assured}
	if err := c.Validate(); !errors.Is(err, ErrDemotedButHeld) {
		t.Fatalf("want ErrDemotedButHeld for a counterexample with an ESTABLISHED outcome, got %v", err)
	}
	c.Outcome = Refuted
	if err := c.Validate(); !errors.Is(err, ErrDemotedButHeld) {
		t.Fatalf("want ErrDemotedButHeld for a refuted claim still at ASSURED, got %v", err)
	}
	c.Level = ledger.Implemented
	if err := c.Validate(); err != nil {
		t.Fatalf("a properly demoted claim must validate: %v", err)
	}
}

// TestARefutedClaimMustNameItsCounterexample. "It failed" with nothing
// to point at cannot be acted on or disputed.
func TestARefutedClaimMustNameItsCounterexample(t *testing.T) {
	c := Claim{ID: "X", Assertion: "a", ProofPath: "p", DisproofPath: "d",
		Outcome: Refuted, Level: ledger.Implemented}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "names no counterexample") {
		t.Fatalf("want a counterexample refusal, got %v", err)
	}
}

// TestUnsettledIsNotEstablished. A claim nobody tried to break has been
// shown to work, which is a different thing from having been shown to
// hold.
func TestUnsettledIsNotEstablished(t *testing.T) {
	c := Claim{ID: "X", Assertion: "a", ProofPath: "p", DisproofPath: "d", Outcome: Unsettled}
	if c.IsEstablished() {
		t.Fatal("an unsettled claim reports as established")
	}
	var notRun Claim
	notRun.ID, notRun.Assertion, notRun.ProofPath, notRun.DisproofPath = "Y", "a", "p", "d"
	if notRun.IsEstablished() {
		t.Fatal("a claim whose paths were never run reports as established")
	}
}

// TestTheUnsettledClaimsAreTheHonestOnes. Three claims cannot be
// settled here, and each must stay unsettled: irrecoverability,
// real-world coverage, and the five-fabric composition on real data.
func TestTheUnsettledClaimsAreTheHonestOnes(t *testing.T) {
	r, err := NewRegister(Claims...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	unsettled := map[string]bool{}
	for _, id := range r.Unsettled() {
		unsettled[id] = true
	}
	for _, id := range []string{
		"CLAIM-REDACTION-IRREVERSIBLE",
		"CLAIM-REDACTION-REAL-WORLD-COVERAGE",
		"CLAIM-FIVE-FABRICS-COMPOSE",
	} {
		if !unsettled[id] {
			t.Errorf("%s is recorded as settled. It cannot be settled in this repository: "+
				"its disproof path needs a party or a corpus VERIQO does not have", id)
		}
	}
	if len(r.Refuted()) > 0 {
		t.Fatalf("claims are recorded as refuted and the code still ships them: %v", r.Refuted())
	}
}

// TestEveryEstablishedClaimWasAttackedByVeriqoOnly is the statement
// that must accompany every one of them.
func TestEveryEstablishedClaimWasAttackedByVeriqoOnly(t *testing.T) {
	r, err := NewRegister(Claims...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	for _, c := range r.All() {
		if c.Outcome != Established {
			continue
		}
		if !c.SelfAttacked() {
			t.Fatalf("%s claims to have been attacked from outside VERIQO by %q. "+
				"If that is now true, say who and when; if not, the register is overstating",
				c.ID, c.DisproofRunner)
		}
	}
	rep := r.Report()
	if !strings.Contains(rep, "Surviving one's own attack") {
		t.Fatal("the report does not state that self-attack is the weakest form of survival")
	}
}

// TestTheReportShowsBothPathsForEveryClaim. A register that printed
// only the proof path would be a claims list, which is what this
// replaces.
func TestTheReportShowsBothPathsForEveryClaim(t *testing.T) {
	r, err := NewRegister(Claims...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	rep := r.Report()
	if strings.Count(rep, "disproof:") != len(Claims) {
		t.Fatalf("the report shows %d disproof paths for %d claims",
			strings.Count(rep, "disproof:"), len(Claims))
	}
	if strings.Count(rep, "proof:") < len(Claims) {
		t.Fatal("the report omits a proof path")
	}
}
