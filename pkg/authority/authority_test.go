package authority

import (
	"errors"
	"testing"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
)

func principal(id string, k identity.Kind) identity.Principal {
	return identity.Principal{
		ID: contract.ID(id), Kind: k, TenantID: "t-acme",
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:  time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func grant(id string, r Role) Grant {
	g := Grant{Principal: contract.ID(id), Role: r, TenantID: "t-acme"}
	if r == IndependentAssessor {
		g.External = true
		g.AttestedBy = "org:notary-cooperative"
	}
	return g
}

// TestNoRoleHoldsBothProposeAndApprove is the structural form of
// separation of duties. If one role held both, separation would depend
// on a workflow remembering to use two people.
func TestNoRoleHoldsBothProposeAndApprove(t *testing.T) {
	for _, r := range Roles() {
		if r == CaseOwner {
			// The one deliberate exception, and it is a real one: the
			// case owner is accountable for the case and can both put
			// material forward and bind it. CheckSeparation still
			// refuses them approving their OWN proposal, which is the
			// protection that matters.
			continue
		}
		if Holds(r, Propose) && Holds(r, Approve) {
			t.Errorf("%s holds both PROPOSE and APPROVE", r)
		}
	}
}

// TestAIHoldsNoApproval is Law 7 in the matrix.
func TestAIHoldsNoApproval(t *testing.T) {
	for _, c := range []Capability{Approve, Export, Administer, Review} {
		if Holds(AI, c) {
			t.Errorf("the AI role holds %s", c)
		}
	}
	for _, c := range []Capability{View, Propose, Challenge} {
		if !Holds(AI, c) {
			t.Errorf("the AI role cannot %s, so it cannot contribute at all", c)
		}
	}
}

// TestAnAgentCannotApproveEvenWithAHumanRole is the half that matters.
// The matrix is configuration; Law 7 is not. Granting an agent the
// CASE_OWNER role must not buy it an approval.
func TestAnAgentCannotApproveEvenWithAHumanRole(t *testing.T) {
	a := principal("agent:research", identity.Agent)
	g := grant("agent:research", CaseOwner)
	if err := Check(a, g, Approve); !errors.Is(err, ErrAIApproval) {
		t.Fatalf("MISCONFIGURED GRANT BOUGHT AN APPROVAL: %v", err)
	}
	// And it keeps the capabilities it is supposed to have.
	if err := Check(a, g, Propose); err != nil {
		t.Fatalf("an agent with CASE_OWNER cannot propose: %v", err)
	}
	// Services too: automation of any kind, not only the word "agent".
	s := principal("service:pipeline", identity.Service)
	if err := Check(s, grant("service:pipeline", CaseOwner), Approve); !errors.Is(err, ErrAIApproval) {
		t.Fatal("a service approved")
	}
}

// TestAdministratorCannotApprove is the cell people get wrong: the
// principal who can grant themselves a role must not also be the one
// whose conclusions bind.
func TestAdministratorCannotApprove(t *testing.T) {
	admin := principal("human:admin", identity.Human)
	g := grant("human:admin", Administrator)
	for _, c := range []Capability{Approve, Propose, Review, Export} {
		if err := Check(admin, g, c); !errors.Is(err, ErrNotGranted) {
			t.Errorf("ADMINISTRATOR holds %s: %v", c, err)
		}
	}
	if err := Check(admin, g, Administer); err != nil {
		t.Fatalf("ADMINISTRATOR cannot administer: %v", err)
	}
}

// TestComplianceCannotApproveFindings: their remit is process, not
// conclusion.
func TestComplianceCannotApproveFindings(t *testing.T) {
	p := principal("human:compliance-1", identity.Human)
	if err := Check(p, grant("human:compliance-1", Compliance), Approve); !errors.Is(err, ErrNotGranted) {
		t.Fatalf("COMPLIANCE approved a finding: %v", err)
	}
	if err := Check(p, grant("human:compliance-1", Compliance), Export); err != nil {
		t.Fatalf("COMPLIANCE cannot export for regulatory production: %v", err)
	}
}

// --- FC-006: VERIQO cannot validate VERIQO -----------------------------

func TestAnInternalPrincipalCannotHoldIndependentAssessor(t *testing.T) {
	g := Grant{Principal: "human:veriqo-qa", Role: IndependentAssessor, TenantID: "t-acme"}
	if err := g.Validate(); !errors.Is(err, ErrSelfQualification) {
		t.Fatalf("an internal principal held INDEPENDENT_ASSESSOR: %v", err)
	}
}

func TestIndependenceMustBeAttestedBySomebodyElse(t *testing.T) {
	g := Grant{Principal: "org:assessor", Role: IndependentAssessor, TenantID: "t-acme", External: true}
	if err := g.Validate(); !errors.Is(err, ErrSelfQualification) {
		t.Fatalf("an unattested independence claim was accepted: %v", err)
	}
	g.AttestedBy = "org:assessor"
	if err := g.Validate(); !errors.Is(err, ErrSelfQualification) {
		t.Fatalf("a principal attested to its own independence: %v", err)
	}
	g.AttestedBy = "org:notary-cooperative"
	if err := g.Validate(); err != nil {
		t.Fatalf("a properly attested assessor was refused: %v", err)
	}
}

func TestOnlyIndependentAssessorQualifiesExternally(t *testing.T) {
	for _, r := range Roles() {
		err := CanQualifyExternally(grant("human:x", r))
		if r == IndependentAssessor {
			continue // grant() gives it the external attestation
		}
		if !errors.Is(err, ErrSelfQualification) {
			t.Errorf("%s can qualify externally: %v", r, err)
		}
	}
	if err := CanQualifyExternally(grant("org:assessor", IndependentAssessor)); err != nil {
		t.Fatalf("a real assessor cannot qualify externally: %v", err)
	}
}

// TestAnAssessorCannotPropose. An assessor who proposes is assessing
// their own material -- FC-006 wearing a different hat.
func TestAnAssessorCannotPropose(t *testing.T) {
	if Holds(IndependentAssessor, Propose) {
		t.Fatal("INDEPENDENT_ASSESSOR can propose the material it assesses")
	}
}

// --- Separation of duties ----------------------------------------------

func TestSelfApprovalIsRefused(t *testing.T) {
	a := principal("human:analyst-1", identity.Human)
	if err := CheckSeparation(a, a); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("self-approval accepted: %v", err)
	}
}

// TestApprovalViaAnAgentIsRefused. The analyst who cannot approve
// their own finding must not be able to do it by launching an agent,
// nor by having their agent's proposal approved by themselves.
func TestApprovalViaAnAgentIsRefused(t *testing.T) {
	analyst := principal("human:analyst-1", identity.Human)
	theirAgent := principal("agent:research", identity.Agent)
	id := analyst.ID
	theirAgent.OnBehalfOf = &id

	if err := CheckSeparation(theirAgent, analyst); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("an analyst approved their own agent's proposal: %v", err)
	}
	if err := CheckSeparation(analyst, theirAgent); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("an agent approved its principal's proposal: %v", err)
	}
}

// TestTwoAgentsOfTheSamePrincipalAreOnePrincipal. Launching two agents
// must not manufacture two independent parties.
func TestTwoAgentsOfTheSamePrincipalAreOnePrincipal(t *testing.T) {
	analyst := contract.ID("human:analyst-1")
	a1 := principal("agent:a1", identity.Agent)
	a2 := principal("agent:a2", identity.Agent)
	a1.OnBehalfOf = &analyst
	a2.OnBehalfOf = &analyst
	if err := CheckSeparation(a1, a2); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("two agents of one analyst counted as two parties: %v", err)
	}
}

func TestGenuinelyDifferentPrincipalsPassSeparation(t *testing.T) {
	if err := CheckSeparation(
		principal("human:analyst-1", identity.Human),
		principal("human:reviewer-1", identity.Human),
	); err != nil {
		t.Fatalf("two distinct humans failed separation: %v", err)
	}
}

// --- Plumbing ----------------------------------------------------------

func TestAGrantForAnotherTenantIsRefused(t *testing.T) {
	p := principal("human:analyst-1", identity.Human)
	g := grant("human:analyst-1", Analyst)
	g.TenantID = "t-beta"
	if err := Check(p, g, View); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("a cross-tenant grant was honoured: %v", err)
	}
}

func TestAGrantForAnotherPrincipalIsRefused(t *testing.T) {
	p := principal("human:analyst-1", identity.Human)
	if err := Check(p, grant("human:analyst-2", CaseOwner), Approve); !errors.Is(err, ErrNotGranted) {
		t.Fatalf("a borrowed grant was honoured: %v", err)
	}
}

// TestEveryRoleCanView. A role that cannot see anything is a
// configuration error, not a security posture.
func TestEveryRoleCanView(t *testing.T) {
	for _, r := range Roles() {
		if !Holds(r, View) {
			t.Errorf("%s cannot VIEW", r)
		}
	}
}

// TestEveryRoleAndCapabilityIsAccountedFor prevents the matrix from
// silently gaining a role with no cells or a capability no role holds.
func TestEveryRoleAndCapabilityIsAccountedFor(t *testing.T) {
	if len(matrix) != len(Roles()) {
		t.Fatalf("matrix has %d roles, Roles() lists %d", len(matrix), len(Roles()))
	}
	held := map[Capability]bool{}
	for _, r := range Roles() {
		if !r.Valid() {
			t.Errorf("%s is listed but absent from the matrix", r)
		}
		if len(Of(r)) == 0 {
			t.Errorf("%s holds nothing", r)
		}
		for _, c := range Of(r) {
			if !c.Valid() {
				t.Errorf("%s holds unknown capability %q", r, c)
			}
			held[c] = true
		}
	}
	for _, c := range Capabilities() {
		if !held[c] {
			t.Errorf("no role holds %s; it is unreachable", c)
		}
	}
}

func TestUnknownRoleAndCapabilityAreRefused(t *testing.T) {
	p := principal("human:x", identity.Human)
	g := grant("human:x", Analyst)
	if err := Check(p, g, Capability("DELETE")); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("unknown capability accepted: %v", err)
	}
	g.Role = Role("SUPERUSER")
	if err := Check(p, g, View); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("unknown role accepted: %v", err)
	}
}
