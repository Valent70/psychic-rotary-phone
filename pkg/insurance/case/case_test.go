package caseinsurance

import (
	"errors"
	"testing"

	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
)

func TestNewRejectsEmptyCaseID(t *testing.T) {
	if _, err := New("", 0); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestNewStartsAtCaseCreated(t *testing.T) {
	c, err := New("CASE-1", 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.State() != StateCaseCreated {
		t.Fatalf("expected initial state CASE_CREATED, got %s", c.State())
	}
}

func TestAdvanceOneStepSucceeds(t *testing.T) {
	c, _ := New("CASE-1", 0)
	if err := c.Advance(StatePartiesIdentified, 10); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if c.State() != StatePartiesIdentified {
		t.Fatalf("expected PARTIES_IDENTIFIED, got %s", c.State())
	}
}

// TestCannotSkipLifecycleStages is the case-level generalisation of
// the blueprint's own named prohibition: "Tidak boleh langsung CLAIM
// -> APPROVED atau CLAIM -> REJECTED". Attempting to jump from
// CASE_CREATED straight to, say, QUANTUM_ANALYZED (skipping every
// intake/verification/analysis stage in between) must be refused.
func TestCannotSkipLifecycleStages(t *testing.T) {
	c, _ := New("CASE-1", 0)
	if err := c.Advance(StateQuantumAnalyzed, 10); !errors.Is(err, ErrCannotSkipState) {
		t.Fatalf("expected ErrCannotSkipState, got %v", err)
	}
	// Case must remain unchanged.
	if c.State() != StateCaseCreated {
		t.Fatalf("expected state to remain CASE_CREATED after a rejected skip, got %s", c.State())
	}
}

func TestCannotMoveBackward(t *testing.T) {
	c, _ := New("CASE-1", 0)
	c.Advance(StatePartiesIdentified, 10)
	c.Advance(StatePolicyRegistered, 20)
	if err := c.Advance(StatePartiesIdentified, 30); !errors.Is(err, ErrCannotGoBackward) {
		t.Fatalf("expected ErrCannotGoBackward, got %v", err)
	}
}

func TestTerminalStateOnlyReachableFromDossierGenerated(t *testing.T) {
	c, _ := New("CASE-1", 0)
	c.Advance(StatePartiesIdentified, 10)
	if err := c.Advance(StateCaseClosed, 20); !errors.Is(err, ErrInvalidTerminal) {
		t.Fatalf("expected ErrInvalidTerminal when closing from a non-dossier state, got %v", err)
	}
}

func TestCannotAdvanceATerminalCase(t *testing.T) {
	c, _ := New("CASE-1", 0)
	walkToDossierGenerated(t, c)
	if err := c.Advance(StateCaseClosed, 100); err != nil {
		t.Fatalf("closing from DOSSIER_GENERATED: %v", err)
	}
	if err := c.Advance(StatePartiesIdentified, 110); !errors.Is(err, ErrTerminalState) {
		t.Fatalf("expected ErrTerminalState on an already-closed case, got %v", err)
	}
}

func TestFullLifecycleWalkthroughSucceeds(t *testing.T) {
	c, _ := New("CASE-1", 0)
	walkToDossierGenerated(t, c)
	if c.State() != StateDossierGenerated {
		t.Fatalf("expected DOSSIER_GENERATED after full walkthrough, got %s", c.State())
	}
	if err := c.Advance(StateOpenIssues, 999); err != nil {
		t.Fatalf("Advance to OPEN_ISSUES: %v", err)
	}
	log := c.StateLog()
	if len(log) != len(Sequence())-1+1 { // every forward step + the terminal step
		t.Fatalf("unexpected state log length %d", len(log))
	}
}

func walkToDossierGenerated(t *testing.T, c *Case) {
	t.Helper()
	seq := Sequence()
	for i := 1; i < len(seq); i++ { // seq[0] is CASE_CREATED, the starting state
		if err := c.Advance(seq[i], uint64(i*10)); err != nil {
			t.Fatalf("Advance to %s: %v", seq[i], err)
		}
	}
}

func TestAdvanceRejectsUnknownState(t *testing.T) {
	c, _ := New("CASE-1", 0)
	if err := c.Advance(State("NOT_A_REAL_STATE"), 10); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("expected ErrUnknownState, got %v", err)
	}
}

func TestRegisterPolicyAndEffectiveAt(t *testing.T) {
	c, _ := New("CASE-1", 0)
	h, _ := policy.NewHistory("POL-1")
	h.Add(policy.Version{PolicyID: "POL-1", VersionID: "V1", Kind: policy.KindOriginal, EffectiveFrom: 1000})
	if err := c.RegisterPolicy("POL-1", h); err != nil {
		t.Fatalf("RegisterPolicy: %v", err)
	}
	v, err := c.PolicyEffectiveAt("POL-1", 2000)
	if err != nil {
		t.Fatalf("PolicyEffectiveAt: %v", err)
	}
	if v.VersionID != "V1" {
		t.Fatalf("unexpected version %s", v.VersionID)
	}
}

func TestRegisterPolicyRejectsDuplicate(t *testing.T) {
	c, _ := New("CASE-1", 0)
	h, _ := policy.NewHistory("POL-1")
	c.RegisterPolicy("POL-1", h)
	if err := c.RegisterPolicy("POL-1", h); !errors.Is(err, ErrDuplicatePolicyID) {
		t.Fatalf("expected ErrDuplicatePolicyID, got %v", err)
	}
}

func TestPolicyEffectiveAtRejectsUnknownPolicy(t *testing.T) {
	c, _ := New("CASE-1", 0)
	if _, err := c.PolicyEffectiveAt("NOPE", 1000); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("expected ErrPolicyNotFound, got %v", err)
	}
}

func TestRegisterClaimAndLookup(t *testing.T) {
	c, _ := New("CASE-1", 0)
	c.Parties.Register("PTY-001", "Claimant Co", party.RoleClaimant)
	cl, _ := claim.New("CLM-1", "CASE-1", claim.TypeCargoDamage, "PTY-001", nil)
	if err := c.RegisterClaim(cl); err != nil {
		t.Fatalf("RegisterClaim: %v", err)
	}
	got, err := c.ClaimByID("CLM-1")
	if err != nil {
		t.Fatalf("ClaimByID: %v", err)
	}
	if got.ClaimID != "CLM-1" {
		t.Fatalf("unexpected claim %v", got)
	}
}

func TestRegisterClaimRejectsDuplicate(t *testing.T) {
	c, _ := New("CASE-1", 0)
	cl, _ := claim.New("CLM-1", "CASE-1", claim.TypeCargoDamage, "PTY-001", nil)
	c.RegisterClaim(cl)
	if err := c.RegisterClaim(cl); !errors.Is(err, ErrDuplicateClaimID) {
		t.Fatalf("expected ErrDuplicateClaimID, got %v", err)
	}
}

func TestClaimByIDRejectsUnknownClaim(t *testing.T) {
	c, _ := New("CASE-1", 0)
	if _, err := c.ClaimByID("NOPE"); !errors.Is(err, ErrClaimNotFound) {
		t.Fatalf("expected ErrClaimNotFound, got %v", err)
	}
}

// TestNoDirectClaimApprovalPath is the blueprint's own literal
// prohibition, verified as code rather than merely as a comment:
// there is no State value meaning "APPROVED" or "REJECTED" anywhere
// in this package's vocabulary at all.
func TestNoDirectClaimApprovalPath(t *testing.T) {
	for _, s := range Sequence() {
		if s == State("APPROVED") || s == State("REJECTED") {
			t.Fatalf("found a forbidden direct-decision state in the lifecycle sequence: %s", s)
		}
	}
	if StateCaseClosed == State("APPROVED") || StateOpenIssues == State("REJECTED") {
		t.Fatal("terminal states must not encode a coverage/legal decision")
	}
}

func TestSequenceHasFifteenStagesPerBlueprint(t *testing.T) {
	// blueprint §2's chain from CASE_CREATED to DOSSIER_GENERATED
	// (terminal CASE_CLOSED/OPEN_ISSUES excluded, they branch off
	// DOSSIER_GENERATED rather than extending the linear count).
	if got := len(Sequence()); got != 15 {
		t.Fatalf("expected 15 lifecycle stages per VICE blueprint §2, got %d", got)
	}
}
