// Package authority is the VERIQO authority matrix.
//
// # The two failures this package exists to prevent
//
// FC-001 AUTHORITY_DIFFUSION -- a component that should only assemble
// material was able to mint a Finding. The fix is not "check a
// permission at the call site": it is that the capability to conclude
// belongs to a small, enumerated set of roles, and no amount of
// plumbing grants it to a component that was never given it.
//
// FC-006 SELF_QUALIFICATION -- VERIQO was able to act as its own
// external validator. The fix is that INDEPENDENT_ASSESSOR is a role
// no VERIQO-internal principal can hold, and that the check is a
// property of the role rather than a convention.
//
// # Capabilities are verbs, and they do not imply one another
//
//	VIEW        read the material
//	PROPOSE     put forward a claim, resolution, hypothesis or finding
//	CHALLENGE   record a contradiction or counterexample against one
//	REVIEW      examine somebody else's proposal and record an opinion
//	APPROVE     make a proposal binding -- the qualification act
//	EXPORT      remove material from the system's boundary
//	ADMINISTER  change configuration, policy, roles, keys
//
// APPROVE does not imply EXPORT and ADMINISTER implies neither. The
// administrator who can rotate a key must not therefore be able to
// approve a finding, because the person who can grant themselves a
// role must not also be the person whose conclusions are binding.
//
// # Separation of duties is enforced here, not remembered elsewhere
//
// A proposal and its approval must come from different principals.
// That is checked by CheckSeparation rather than left as a code review
// convention, because the failure mode -- an analyst approving their
// own finding -- looks exactly like a correct workflow in every log.
package authority

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
)

var (
	ErrUnknownRole       = errors.New("authority: unknown role")
	ErrUnknownCapability = errors.New("authority: unknown capability")
	ErrNotGranted        = errors.New("authority: role does not hold this capability")
	ErrSelfApproval      = errors.New("authority: a principal may not approve its own proposal")
	ErrSelfQualification = errors.New("authority: VERIQO may not act as its own independent assessor")
	ErrAIApproval        = errors.New("authority: an automated principal may not approve; it may only propose")
	ErrNoRole            = errors.New("authority: principal holds no role")
)

// Capability is a verb.
type Capability string

const (
	View       Capability = "VIEW"
	Propose    Capability = "PROPOSE"
	Challenge  Capability = "CHALLENGE"
	Review     Capability = "REVIEW"
	Approve    Capability = "APPROVE"
	Export     Capability = "EXPORT"
	Administer Capability = "ADMINISTER"
)

// Capabilities returns every capability, in a fixed order.
func Capabilities() []Capability {
	return []Capability{View, Propose, Challenge, Review, Approve, Export, Administer}
}

func (c Capability) Valid() bool {
	for _, k := range Capabilities() {
		if k == c {
			return true
		}
	}
	return false
}

// Role is a named position in the matrix.
type Role string

const (
	AI                  Role = "AI"
	Analyst             Role = "ANALYST"
	SeniorAnalyst       Role = "SENIOR_ANALYST"
	Reviewer            Role = "REVIEWER"
	CaseOwner           Role = "CASE_OWNER"
	DomainExpert        Role = "DOMAIN_EXPERT"
	Compliance          Role = "COMPLIANCE"
	Administrator       Role = "ADMINISTRATOR"
	IndependentAssessor Role = "INDEPENDENT_ASSESSOR"
)

// Roles returns every role, in a fixed order.
func Roles() []Role {
	return []Role{AI, Analyst, SeniorAnalyst, Reviewer, CaseOwner,
		DomainExpert, Compliance, Administrator, IndependentAssessor}
}

func (r Role) Valid() bool {
	_, ok := matrix[r]
	return ok
}

// matrix is the authority matrix, written out rather than computed.
//
// It is a literal because every cell is a decision somebody must be
// able to read and disagree with. A rule that generated it would hide
// the four cells that matter behind an abstraction.
var matrix = map[Role][]Capability{
	// AI proposes and challenges. It never approves (Law 7) and never
	// exports (an agent that can export is an exfiltration path with
	// a policy engine in front of it).
	AI: {View, Propose, Challenge},

	Analyst:       {View, Propose, Challenge},
	SeniorAnalyst: {View, Propose, Challenge, Review},

	// A reviewer reviews and approves; a reviewer does NOT propose,
	// because a role that can do both makes separation of duties a
	// matter of who remembers to check.
	Reviewer: {View, Challenge, Review, Approve},

	// The case owner is accountable for the case: they approve and
	// they export. They do not administer.
	CaseOwner: {View, Propose, Challenge, Review, Approve, Export},

	DomainExpert: {View, Propose, Challenge, Review},

	// Compliance reviews and exports (regulatory production) and does
	// not approve findings: their remit is whether the process was
	// followed, not whether the conclusion is right.
	Compliance: {View, Challenge, Review, Export},

	// The administrator can change the system and cannot conclude
	// anything with it. This is the cell people most often get wrong.
	Administrator: {View, Administer},

	// The independent assessor reviews and approves from outside. It
	// cannot propose: an assessor who proposes is assessing their own
	// material, which is what FC-006 was.
	IndependentAssessor: {View, Challenge, Review, Approve},
}

// Grant is a role held by a principal, optionally scoped to a case.
type Grant struct {
	Principal contract.ID `json:"principal"`
	Role      Role        `json:"role"`
	TenantID  string      `json:"tenant_id"`

	// CaseID scopes the grant. An empty CaseID is a tenant-wide grant,
	// which is correct for ADMINISTRATOR and a finding for ANALYST.
	CaseID string `json:"case_id,omitempty"`

	// External marks a grant held by a party outside VERIQO and
	// outside the customer's own staff. Only an external grant may
	// carry INDEPENDENT_ASSESSOR.
	External bool `json:"external,omitempty"`

	// AttestedBy names who verified the holder is who they claim.
	// An INDEPENDENT_ASSESSOR grant with no attestation is a claim of
	// independence made by the party that benefits from it.
	AttestedBy string `json:"attested_by,omitempty"`
}

// Validate refuses the two grants that would reopen FC-006.
func (g Grant) Validate() error {
	if g.Principal == "" {
		return fmt.Errorf("%w: grant names no principal", ErrNoRole)
	}
	if err := g.Principal.Validate(); err != nil {
		return err
	}
	if !g.Role.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownRole, g.Role)
	}
	if strings.TrimSpace(g.TenantID) == "" {
		return errors.New("authority: grant is not anchored to a tenant")
	}
	if g.Role == IndependentAssessor {
		if !g.External {
			return fmt.Errorf("%w: %s holds INDEPENDENT_ASSESSOR without being external",
				ErrSelfQualification, g.Principal)
		}
		if strings.TrimSpace(g.AttestedBy) == "" {
			return fmt.Errorf("%w: %s claims independence with no attesting party",
				ErrSelfQualification, g.Principal)
		}
		if strings.EqualFold(g.AttestedBy, string(g.Principal)) {
			return fmt.Errorf("%w: %s attests to its own independence",
				ErrSelfQualification, g.Principal)
		}
	}
	if g.External && g.Role != IndependentAssessor {
		return fmt.Errorf("authority: %s is external but holds %s; external parties hold only INDEPENDENT_ASSESSOR",
			g.Principal, g.Role)
	}
	return nil
}

// Holds reports whether a role carries a capability.
func Holds(r Role, c Capability) bool {
	for _, k := range matrix[r] {
		if k == c {
			return true
		}
	}
	return false
}

// Of returns a role's capabilities.
func Of(r Role) []Capability {
	out := append([]Capability(nil), matrix[r]...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Check is the authorisation decision for one principal, one grant and
// one capability.
//
// It layers three refusals, and the order matters for the error a
// caller sees:
//
//  1. the grant is malformed or not this principal's;
//  2. the principal is automated and the capability is APPROVE
//     (Law 7 -- this is checked even if the role were to hold it,
//     so a misconfigured grant cannot buy an agent an approval);
//  3. the role simply does not hold the capability.
func Check(p identity.Principal, g Grant, c Capability) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if g.Principal != p.ID {
		return fmt.Errorf("%w: grant belongs to %s, not %s", ErrNotGranted, g.Principal, p.ID)
	}
	if g.TenantID != p.TenantID {
		return fmt.Errorf("%w: grant is for %s, principal is in %s",
			contract.ErrCrossTenant, g.TenantID, p.TenantID)
	}
	if !c.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownCapability, c)
	}
	// Law 7, enforced independently of the matrix. An automated
	// principal cannot approve even if somebody grants it a role that
	// can, because the matrix is configuration and this is a law.
	if p.Kind.IsAutomated() && (c == Approve || c == Administer) {
		return fmt.Errorf("%w: %s is %s", ErrAIApproval, p.ID, p.Kind)
	}
	if !Holds(g.Role, c) {
		return fmt.Errorf("%w: %s does not hold %s", ErrNotGranted, g.Role, c)
	}
	return nil
}

// CheckSeparation refuses an approval by the principal that proposed.
//
// It also refuses an approval by a principal acting on behalf of the
// proposer: an analyst who cannot approve their own finding must not
// be able to approve it by launching an agent, and an agent's approval
// is already refused by Check -- this closes the other direction,
// where a reviewer is acting on behalf of the proposer.
func CheckSeparation(proposer, approver identity.Principal) error {
	if proposer.ID == approver.ID {
		return fmt.Errorf("%w: %s", ErrSelfApproval, approver.ID)
	}
	if approver.OnBehalfOf != nil && *approver.OnBehalfOf == proposer.ID {
		return fmt.Errorf("%w: %s acts on behalf of the proposer %s",
			ErrSelfApproval, approver.ID, proposer.ID)
	}
	if proposer.OnBehalfOf != nil && *proposer.OnBehalfOf == approver.ID {
		return fmt.Errorf("%w: the proposal %s was made on behalf of the approver %s",
			ErrSelfApproval, proposer.ID, approver.ID)
	}
	if proposer.OnBehalfOf != nil && approver.OnBehalfOf != nil &&
		*proposer.OnBehalfOf == *approver.OnBehalfOf {
		return fmt.Errorf("%w: both principals act on behalf of %s",
			ErrSelfApproval, *proposer.OnBehalfOf)
	}
	return nil
}

// CanQualifyExternally reports whether a grant is capable of the
// external qualification step, and says why not when it is not.
//
// This is the single function gate G10 and the qualification ladder
// both consult. It exists so "an outside party looked at this" has one
// definition rather than one per caller.
func CanQualifyExternally(g Grant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if g.Role != IndependentAssessor {
		return fmt.Errorf("%w: %s is not INDEPENDENT_ASSESSOR", ErrSelfQualification, g.Role)
	}
	return nil
}
