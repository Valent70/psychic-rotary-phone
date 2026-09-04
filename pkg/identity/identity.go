// Package identity gives every actor in VERIQO a name that a policy
// can be written against.
//
// # Zero trust, stated concretely
//
// NIST SP 800-207 removes implicit trust from network position: being
// inside the perimeter, or inside the process, grants nothing. For
// VERIQO that has one practical consequence, and it is the reason this
// package exists at Wave 1 rather than being added later:
//
//	a human, a service, an AI agent, a device, a connector and a data
//	source are all principals, and none of them is trusted for being
//	one kind rather than another.
//
// The kind is not a permission. It is an input to a policy decision,
// and it matters mostly because some rules are written specifically
// about agents (an agent may propose and may not qualify) and some
// specifically about sources (a source may assert and may not
// conclude).
//
// # Why an agent is a first-class principal
//
// If an agent acts under the identity of the human who launched it,
// then every audit record of an agent's action is a record of that
// human's action, and the tool firewall has nobody to refuse. The
// agent gets its own identity, and its authority is bounded
// independently of, and never above, its principal's.
package identity

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"veriqo/pkg/contract"
)

var (
	ErrNoPrincipal      = errors.New("identity: no principal")
	ErrUnknownKind      = errors.New("identity: unknown principal kind")
	ErrNoTenant         = errors.New("identity: principal is not anchored to a tenant")
	ErrCredentialStale  = errors.New("identity: credential is outside its validity window")
	ErrDelegationWidens = errors.New("identity: a delegation may not exceed the delegator")
	ErrSelfDelegation   = errors.New("identity: a principal may not delegate to itself")
	ErrBadSPIFFEID      = errors.New("identity: malformed SPIFFE ID")
)

// Kind is what sort of actor a principal is.
type Kind string

const (
	Human     Kind = "HUMAN"
	Service   Kind = "SERVICE"
	Agent     Kind = "AGENT"
	Device    Kind = "DEVICE"
	Connector Kind = "CONNECTOR"
	Source    Kind = "SOURCE"
)

func (k Kind) Valid() bool {
	switch k {
	case Human, Service, Agent, Device, Connector, Source:
		return true
	}
	return false
}

// IsAutomated reports whether no human is in the loop for this
// principal's own actions. Several policies key off this rather than
// off Kind, so that adding a new automated kind does not require
// finding every rule that listed AGENT and SERVICE.
func (k Kind) IsAutomated() bool {
	switch k {
	case Agent, Service, Connector, Source:
		return true
	}
	return false
}

// RequiresHumanReviewOfOutput reports whether this kind's output may
// never be qualified without a human looking at it. Law 7: AI may
// extract, classify, propose, link, hypothesise and summarise; AI may
// not, alone, qualify a fact.
func (k Kind) RequiresHumanReviewOfOutput() bool {
	return k == Agent
}

// Principal is an actor with a name, a tenant anchor and a validity
// window.
type Principal struct {
	ID       contract.ID `json:"id"`
	Kind     Kind        `json:"kind"`
	TenantID string      `json:"tenant_id"`
	Display  string      `json:"display,omitempty"`

	// SPIFFE is the workload identity for non-human principals. It is
	// optional in a development environment and required in
	// production; gate G7 is the one that checks the difference.
	SPIFFE string `json:"spiffe,omitempty"`

	// NotBefore/NotAfter bound the credential. Short-lived credentials
	// are the zero-trust posture: a principal whose window has closed
	// is not a principal, whatever it still holds.
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`

	// OnBehalfOf names the principal this one acts for, when it does.
	// An agent launched by an analyst records that analyst here, and
	// the agent's authority is intersected with theirs -- never
	// unioned.
	OnBehalfOf *contract.ID `json:"on_behalf_of,omitempty"`
}

// Validate checks the principal is well-formed. It does not check
// whether the credential is currently live -- that is Active, and the
// two are separate because a historical audit record legitimately
// names a principal whose credential has since expired.
func (p Principal) Validate() error {
	if p.ID == "" {
		return ErrNoPrincipal
	}
	if err := p.ID.Validate(); err != nil {
		return err
	}
	if !p.Kind.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownKind, p.Kind)
	}
	if strings.TrimSpace(p.TenantID) == "" {
		return fmt.Errorf("%w: %s", ErrNoTenant, p.ID)
	}
	if p.SPIFFE != "" {
		if err := ValidateSPIFFEID(p.SPIFFE); err != nil {
			return err
		}
	}
	if !p.NotBefore.IsZero() && !p.NotAfter.IsZero() && !p.NotBefore.Before(p.NotAfter) {
		return fmt.Errorf("identity: %s has an empty validity window", p.ID)
	}
	if p.OnBehalfOf != nil && *p.OnBehalfOf == p.ID {
		return fmt.Errorf("%w: %s", ErrSelfDelegation, p.ID)
	}
	return nil
}

// Active reports whether the credential is live at t.
//
// A zero NotBefore/NotAfter means unbounded, which is acceptable for a
// human account and is a finding for a workload: G7 requires
// short-lived workload credentials in production, and Unbounded()
// exists so that gate can be checked rather than assumed.
func (p Principal) Active(t time.Time) error {
	if !p.NotBefore.IsZero() && t.Before(p.NotBefore) {
		return fmt.Errorf("%w: %s is not valid before %s", ErrCredentialStale, p.ID, p.NotBefore.Format(time.RFC3339))
	}
	if !p.NotAfter.IsZero() && !t.Before(p.NotAfter) {
		return fmt.Errorf("%w: %s expired at %s", ErrCredentialStale, p.ID, p.NotAfter.Format(time.RFC3339))
	}
	return nil
}

// Unbounded reports whether the credential never expires.
func (p Principal) Unbounded() bool { return p.NotAfter.IsZero() }

// Lifetime returns the credential's duration, or 0 if unbounded.
func (p Principal) Lifetime() time.Duration {
	if p.NotBefore.IsZero() || p.NotAfter.IsZero() {
		return 0
	}
	return p.NotAfter.Sub(p.NotBefore)
}

// SameTenant reports whether two principals share a tenant anchor.
// Every cross-tenant path in the system calls this and fails closed.
func (p Principal) SameTenant(o Principal) bool {
	return p.TenantID != "" && p.TenantID == o.TenantID
}

// spiffeIDPattern: spiffe://trust-domain/path
var spiffeIDPattern = regexp.MustCompile(`^spiffe://[a-z0-9]([a-z0-9.-]*[a-z0-9])?(/[A-Za-z0-9._~%!$&'()*+,;=:@-]+)+$`)

// ValidateSPIFFEID checks the shape of a SPIFFE ID (SPIFFE-ID spec:
// a trust domain and a non-empty path, no query, no fragment).
//
// This validates the string only. Whether the workload actually holds
// an SVID for it is an attestation question, answered by the SPIRE
// integration and gated by G7 -- and this package deliberately does
// not pretend to answer it, because a syntactic check that reads like
// an attestation is exactly the sort of overclaim VERIQO is built to
// refuse.
func ValidateSPIFFEID(s string) error {
	if !spiffeIDPattern.MatchString(s) {
		return fmt.Errorf("%w: %q", ErrBadSPIFFEID, s)
	}
	if strings.Contains(s, "?") || strings.Contains(s, "#") {
		return fmt.Errorf("%w: %q carries a query or fragment", ErrBadSPIFFEID, s)
	}
	return nil
}

// TrustDomain extracts the SPIFFE trust domain.
func TrustDomain(spiffeID string) (string, error) {
	if err := ValidateSPIFFEID(spiffeID); err != nil {
		return "", err
	}
	rest := strings.TrimPrefix(spiffeID, "spiffe://")
	td, _, _ := strings.Cut(rest, "/")
	return td, nil
}

// Delegate builds a principal acting on behalf of another.
//
// The returned principal's validity window is the INTERSECTION of the
// two, never the union. An agent cannot outlive the analyst's session
// that launched it, which is the practical form of "a delegation may
// not exceed the delegator".
func Delegate(delegator Principal, agent Principal) (Principal, error) {
	if err := delegator.Validate(); err != nil {
		return Principal{}, fmt.Errorf("identity: delegator: %w", err)
	}
	if err := agent.Validate(); err != nil {
		return Principal{}, fmt.Errorf("identity: delegate: %w", err)
	}
	if agent.ID == delegator.ID {
		return Principal{}, fmt.Errorf("%w: %s", ErrSelfDelegation, agent.ID)
	}
	if !agent.SameTenant(delegator) {
		return Principal{}, fmt.Errorf("%w: %s delegating to %s", contract.ErrCrossTenant, delegator.ID, agent.ID)
	}
	out := agent
	out.OnBehalfOf = &delegator.ID

	// Intersect the windows.
	if delegator.NotBefore.After(out.NotBefore) {
		out.NotBefore = delegator.NotBefore
	}
	switch {
	case out.NotAfter.IsZero():
		out.NotAfter = delegator.NotAfter
	case !delegator.NotAfter.IsZero() && delegator.NotAfter.Before(out.NotAfter):
		out.NotAfter = delegator.NotAfter
	}
	if !out.NotBefore.IsZero() && !out.NotAfter.IsZero() && !out.NotBefore.Before(out.NotAfter) {
		return Principal{}, fmt.Errorf("%w: the intersected window is empty", ErrDelegationWidens)
	}
	return out, nil
}
