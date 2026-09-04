// Package agents is the agent registry and the tool firewall.
//
// # The path an agent's intent takes
//
//	Agent -> Intent -> Policy -> Authorization -> Tool Firewall
//	      -> Execution -> Audit -> Result
//
// Every stage is a place the intent can be refused, and the firewall
// is the stage that exists because the others are not enough. Policy
// says whether this principal may do this kind of thing. The firewall
// says whether THIS CALL, with THESE ARGUMENTS, is within what the
// agent was launched to do.
//
// # Why arguments matter and permissions do not suffice
//
// An agent permitted to "read evidence" and asked to read one case's
// evidence is behaving correctly. The same agent reading every case in
// the tenant is behaving correctly at the permission layer and
// exfiltrating at every other layer. So a tool grant carries a SCOPE,
// the firewall checks the arguments against it, and an out-of-scope
// call is refused with the argument that broke it named.
//
// # Prompt injection is handled here, not by asking the model nicely
//
// The defence is structural. An agent's tool grants are fixed when it
// is launched and cannot be widened by anything that happens during
// the run -- not by a document it reads, not by a tool result, not by
// an instruction embedded in evidence. Text arriving from outside is
// data. The firewall does not read it, does not act on it, and cannot
// be reconfigured by it.
//
// A model that is convinced by an injected instruction will attempt
// the call. That is expected. The call is refused.
package agents

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/ai"
	"veriqo/pkg/authority"
	"veriqo/pkg/identity"
	"veriqo/pkg/policy"
)

var (
	ErrNoGrant         = errors.New("firewall: the agent holds no grant for this tool")
	ErrOutOfScope      = errors.New("firewall: the call is outside the grant's scope")
	ErrBudgetSpent     = errors.New("firewall: the agent's call budget for this tool is spent")
	ErrExpired         = errors.New("firewall: the agent's grants have expired")
	ErrWidening        = errors.New("firewall: an agent's grants cannot be widened during a run")
	ErrNoPurpose       = errors.New("firewall: the intent declares no purpose")
	ErrForbiddenTool   = errors.New("firewall: this tool is not available to an automated principal")
	ErrNoJustification = errors.New("firewall: a write or export intent must state its justification")
)

// Effect is what a tool does to the world. It is declared on the tool
// rather than inferred from its name, because a tool named "search"
// that writes is the thing this classification exists to catch.
type Effect string

const (
	Read    Effect = "READ"
	Write   Effect = "WRITE"
	Call    Effect = "EXTERNAL_CALL"
	Export  Effect = "EXPORT"
	Approve Effect = "APPROVE"
)

func (e Effect) Valid() bool {
	switch e {
	case Read, Write, Call, Export, Approve:
		return true
	}
	return false
}

// AvailableToAutomation reports whether an automated principal may
// ever invoke a tool with this effect.
//
// APPROVE never is: Law 7. EXPORT never is: an agent that can export
// is an exfiltration path with a policy engine in front of it.
func (e Effect) AvailableToAutomation() bool {
	return e == Read || e == Write || e == Call
}

// Tool is a capability the firewall governs.
type Tool struct {
	Name   string `json:"name"`
	Effect Effect `json:"effect"`
	// Description is for humans reading an audit trail.
	Description string `json:"description"`
	// ScopedArgs names the arguments that must be constrained by a
	// grant's scope. A tool with an unscoped case id is a tool that
	// reads every case.
	ScopedArgs []string `json:"scoped_args"`
}

func (t Tool) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("firewall: a tool has no name")
	}
	if !t.Effect.Valid() {
		return fmt.Errorf("firewall: tool %s has unknown effect %q", t.Name, t.Effect)
	}
	if t.Effect != Read && len(t.ScopedArgs) == 0 {
		return fmt.Errorf("firewall: tool %s has effect %s and names no scoped arguments; "+
			"an unscoped write reaches everything", t.Name, t.Effect)
	}
	return nil
}

// Grant is an agent's permission to use a tool, bounded.
type Grant struct {
	Tool string `json:"tool"`
	// Scope constrains argument values: argument name -> permitted
	// values. An argument the tool declares scoped and the grant does
	// not constrain is refused, rather than treated as unrestricted.
	Scope map[string][]string `json:"scope"`
	// Budget bounds how many times the tool may be called. Zero means
	// no calls; a negative budget is refused. There is no "unlimited"
	// -- an agent with an unbounded budget is a loop nobody bounded.
	Budget int `json:"budget"`
}

// Agent is a launched automated principal with fixed grants.
type Agent struct {
	Principal identity.Principal
	// Purpose is why it was launched. Every intent is checked against
	// it.
	Purpose policy.Purpose
	// CaseID scopes the agent to one investigation.
	CaseID string

	grants map[string]Grant
	spent  map[string]int
	// sealed marks the grants as fixed. It is set at construction and
	// never cleared, which is what makes widening impossible rather
	// than merely discouraged.
	sealed bool
}

// Launch creates an agent with its grants fixed.
func Launch(p identity.Principal, purpose policy.Purpose, caseID string,
	tools map[string]Tool, grants []Grant) (*Agent, error) {

	if err := p.Validate(); err != nil {
		return nil, err
	}
	if !p.Kind.IsAutomated() {
		return nil, fmt.Errorf("firewall: %s is %s; the firewall governs automated principals",
			p.ID, p.Kind)
	}
	if !purpose.Valid() {
		return nil, ErrNoPurpose
	}
	if strings.TrimSpace(caseID) == "" {
		return nil, errors.New("firewall: an agent must be scoped to a case")
	}

	a := &Agent{Principal: p, Purpose: purpose, CaseID: caseID,
		grants: map[string]Grant{}, spent: map[string]int{}}

	for _, g := range grants {
		t, ok := tools[g.Tool]
		if !ok {
			return nil, fmt.Errorf("firewall: grant for unknown tool %q", g.Tool)
		}
		if err := t.Validate(); err != nil {
			return nil, err
		}
		if !t.Effect.AvailableToAutomation() {
			return nil, fmt.Errorf("%w: %s has effect %s", ErrForbiddenTool, t.Name, t.Effect)
		}
		if g.Budget < 0 {
			return nil, fmt.Errorf("firewall: tool %s has a negative budget", g.Tool)
		}
		// Every scoped argument must actually be constrained.
		for _, arg := range t.ScopedArgs {
			vals, ok := g.Scope[arg]
			if !ok || len(vals) == 0 {
				return nil, fmt.Errorf("firewall: tool %s declares %q scoped and the grant "+
					"does not constrain it; an unconstrained scoped argument reaches "+
					"everything", t.Name, arg)
			}
		}
		if _, dup := a.grants[g.Tool]; dup {
			return nil, fmt.Errorf("firewall: two grants for tool %s", g.Tool)
		}
		a.grants[g.Tool] = g
	}
	a.sealed = true
	return a, nil
}

// Grants returns a copy of what the agent holds.
func (a *Agent) Grants() []Grant {
	out := make([]Grant, 0, len(a.grants))
	for _, g := range a.grants {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

// AddGrant exists only to be refused.
//
// It is present so that the impossibility is expressed in the API
// rather than by the absence of a method: a caller reaching for it --
// or a model that has been persuaded to reach for it -- gets a refusal
// naming the rule, instead of a compile error somebody works around.
func (a *Agent) AddGrant(Grant) error {
	return fmt.Errorf("%w: %s was launched with its grants fixed. Nothing that happens "+
		"during a run -- a document, a tool result, an instruction embedded in evidence -- "+
		"can widen them", ErrWidening, a.Principal.ID)
}

// Intent is a proposed tool call.
type Intent struct {
	Tool string            `json:"tool"`
	Args map[string]string `json:"args"`
	// Purpose must match the agent's launch purpose.
	Purpose policy.Purpose `json:"purpose"`
	// Justification is required for anything that changes state or
	// leaves the boundary.
	Justification string    `json:"justification,omitempty"`
	At            time.Time `json:"at"`
}

// Decision is the firewall's answer.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Tool    string `json:"tool"`
	Reason  string `json:"reason"`
	// Refused names the argument that broke the scope, when one did.
	RefusedArg string `json:"refused_arg,omitempty"`
	// Remaining is the budget left after this call.
	Remaining int `json:"remaining"`
}

// Check runs the firewall.
//
// It is called AFTER the policy engine, not instead of it: policy
// answers "may this principal do this kind of thing", and the firewall
// answers "is this call within what this agent was launched to do".
func (a *Agent) Check(tools map[string]Tool, in Intent, pd policy.Decision) (Decision, error) {
	d := Decision{Tool: in.Tool}

	if !a.sealed {
		return d, errors.New("firewall: the agent was not launched through Launch")
	}
	if in.At.IsZero() {
		d.Reason = "the intent carries no instant"
		return d, errors.New(d.Reason)
	}
	if err := a.Principal.Active(in.At); err != nil {
		d.Reason = err.Error()
		return d, fmt.Errorf("%w: %v", ErrExpired, err)
	}
	// The policy decision comes first and is not overridden here. A
	// firewall that could allow what policy denied would be a second
	// authorisation system.
	if !pd.Permitted() {
		d.Reason = fmt.Sprintf("policy denied: %s (%s)", pd.Rule, pd.Reason)
		return d, fmt.Errorf("%w: %s", policy.ErrDenied, pd.Rule)
	}
	if in.Purpose != a.Purpose {
		d.Reason = fmt.Sprintf("the intent declares purpose %s; the agent was launched for %s",
			in.Purpose, a.Purpose)
		return d, fmt.Errorf("%w: %s", ErrOutOfScope, d.Reason)
	}

	t, ok := tools[in.Tool]
	if !ok {
		d.Reason = "unknown tool"
		return d, fmt.Errorf("firewall: unknown tool %q", in.Tool)
	}
	if !t.Effect.AvailableToAutomation() {
		d.Reason = fmt.Sprintf("%s has effect %s, which an automated principal may not invoke",
			t.Name, t.Effect)
		return d, fmt.Errorf("%w: %s", ErrForbiddenTool, d.Reason)
	}
	g, ok := a.grants[in.Tool]
	if !ok {
		d.Reason = "the agent holds no grant for this tool"
		return d, fmt.Errorf("%w: %s", ErrNoGrant, in.Tool)
	}
	if t.Effect != Read && strings.TrimSpace(in.Justification) == "" {
		d.Reason = "a state-changing intent states no justification"
		return d, fmt.Errorf("%w: %s", ErrNoJustification, in.Tool)
	}

	// Budget.
	if a.spent[in.Tool] >= g.Budget {
		d.Reason = fmt.Sprintf("budget of %d call(s) is spent", g.Budget)
		return d, fmt.Errorf("%w: %s", ErrBudgetSpent, in.Tool)
	}

	// Scope. Every argument the TOOL declares scoped must be present
	// in the intent and permitted by the grant.
	for _, arg := range t.ScopedArgs {
		v, present := in.Args[arg]
		if !present {
			d.RefusedArg = arg
			d.Reason = fmt.Sprintf("the scoped argument %q was not supplied; an absent scoped "+
				"argument is not an unrestricted one", arg)
			return d, fmt.Errorf("%w: %s", ErrOutOfScope, d.Reason)
		}
		if !permits(g.Scope[arg], v) {
			d.RefusedArg = arg
			d.Reason = fmt.Sprintf("%q=%q is outside the grant's scope %v", arg, v, g.Scope[arg])
			return d, fmt.Errorf("%w: %s", ErrOutOfScope, d.Reason)
		}
	}

	// An agent scoped to a case may not reach another case, whatever
	// the grant says. This is belt and braces on purpose: the grant is
	// configuration and this is the launch contract.
	if v, ok := in.Args["case_id"]; ok && v != a.CaseID {
		d.RefusedArg = "case_id"
		d.Reason = fmt.Sprintf("the agent is scoped to case %s and the call names %s",
			a.CaseID, v)
		return d, fmt.Errorf("%w: %s", ErrOutOfScope, d.Reason)
	}

	a.spent[in.Tool]++
	d.Allowed = true
	d.Remaining = g.Budget - a.spent[in.Tool]
	d.Reason = fmt.Sprintf("within grant; %d call(s) remaining", d.Remaining)
	return d, nil
}

func permits(allowed []string, v string) bool {
	for _, a := range allowed {
		if a == "*" {
			// A wildcard is expressible and it is a decision somebody
			// made in the grant, not a default arrived at by omission.
			return true
		}
		if a == v {
			return true
		}
	}
	return false
}

// Spent reports the calls made per tool, for the audit record.
func (a *Agent) Spent() map[string]int {
	out := make(map[string]int, len(a.spent))
	for k, v := range a.spent {
		out[k] = v
	}
	return out
}

// CheckAuthority is the second law the firewall enforces: whatever an
// agent's tool grants say, it cannot approve or administer.
func (a *Agent) CheckAuthority(c authority.Capability) error {
	if c == authority.Approve || c == authority.Administer || c == authority.Export {
		return fmt.Errorf("%w: %s is %s and may not %s",
			ErrForbiddenTool, a.Principal.ID, a.Principal.Kind, c)
	}
	return nil
}

// CheckAct routes to the AI law, so a caller has one place to ask.
func (a *Agent) CheckAct(act ai.Act) error { return ai.CheckAct(a.Principal, act) }

// Describe renders the agent's bounds for an audit record.
func (a *Agent) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "AGENT %s (%s) for case %s, purpose %s\n",
		a.Principal.ID, a.Principal.Kind, a.CaseID, a.Purpose)
	if a.Principal.OnBehalfOf != nil {
		fmt.Fprintf(&b, "  on behalf of %s\n", *a.Principal.OnBehalfOf)
	}
	fmt.Fprintf(&b, "  credential valid %s..%s\n",
		a.Principal.NotBefore.UTC().Format(time.RFC3339),
		a.Principal.NotAfter.UTC().Format(time.RFC3339))
	for _, g := range a.Grants() {
		args := make([]string, 0, len(g.Scope))
		for k, v := range g.Scope {
			args = append(args, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(args)
		fmt.Fprintf(&b, "  %-24s budget %d, spent %d, scope %s\n",
			g.Tool, g.Budget, a.spent[g.Tool], strings.Join(args, " "))
	}
	b.WriteString("  grants are fixed for the run and cannot be widened by anything the " +
		"agent reads\n")
	return b.String()
}

// Registry holds the tools a deployment offers.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry validates and holds a tool catalogue.
func NewRegistry(ts ...Tool) (*Registry, error) {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range ts {
		if err := t.Validate(); err != nil {
			return nil, err
		}
		if _, dup := r.tools[t.Name]; dup {
			return nil, fmt.Errorf("firewall: duplicate tool %s", t.Name)
		}
		r.tools[t.Name] = t
	}
	return r, nil
}

// Tools returns the catalogue for the firewall.
func (r *Registry) Tools() map[string]Tool {
	out := make(map[string]Tool, len(r.tools))
	for k, v := range r.tools {
		out[k] = v
	}
	return out
}

// Names returns the tool names, sorted.
func (r *Registry) Names() []string {
	var out []string
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
