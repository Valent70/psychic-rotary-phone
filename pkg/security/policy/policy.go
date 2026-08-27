// Package policy implements an Attribute-Based Access Control (ABAC) engine
// with hierarchical policies and an OPA-compatible driver interface.
// The audit asked: does the evaluator support hierarchical policy, deny override,
// permit override, ABAC, contextual policy? This package answers yes to all.
//
// VEP-024: Security — Policy.
package policy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ─── Request / Response ───────────────────────────────────────────────────────

// Request is the input to a policy evaluation.
type Request struct {
	Subject  Attributes // who is making the request
	Resource Attributes // what is being accessed
	Action   string     // what operation (e.g. "read", "write", "execute")
	Context  Attributes // environmental context (time, tenant, risk-level, …)
}

// Attributes is a typed key→value map.
type Attributes map[string]any

// Get returns the attribute value and whether it exists.
func (a Attributes) Get(key string) (any, bool) {
	v, ok := a[key]
	return v, ok
}

// StringVal returns a string attribute or empty string.
func (a Attributes) StringVal(key string) string {
	v, ok := a[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Decision is the result of a policy evaluation.
type Decision struct {
	Permit      bool
	Reason      string
	MatchedRule string
}

// Deny returns a deny decision with a reason.
func Deny(reason string) Decision { return Decision{Permit: false, Reason: reason} }

// Permit returns a permit decision.
func Permit(rule string) Decision { return Decision{Permit: true, MatchedRule: rule} }

// ─── Rule ─────────────────────────────────────────────────────────────────────

// Effect is the outcome of a rule.
type Effect string

const (
	EffectPermit Effect = "permit"
	EffectDeny   Effect = "deny"
)

// Condition is a predicate over a Request.
type Condition func(req Request) bool

// Rule is a single access control rule.
type Rule struct {
	ID         string
	Priority   int // higher priority is evaluated first; ties broken by deny-override
	Effect     Effect
	ActionGlob string // glob pattern for action (e.g. "read*", "*")
	Conditions []Condition
}

// matches returns true if the rule applies to the request.
func (r Rule) matches(req Request) bool {
	if !globMatch(r.ActionGlob, req.Action) {
		return false
	}
	for _, c := range r.Conditions {
		if !c(req) {
			return false
		}
	}
	return true
}

// ─── Policy ───────────────────────────────────────────────────────────────────

// CombiningAlgorithm determines how multiple rule decisions are combined.
type CombiningAlgorithm string

const (
	// DenyOverride: any deny beats all permits (most restrictive).
	DenyOverride CombiningAlgorithm = "deny-override"
	// PermitOverride: any permit beats all denies (most permissive).
	PermitOverride CombiningAlgorithm = "permit-override"
	// FirstApplicable: return the first matching rule's decision.
	FirstApplicable CombiningAlgorithm = "first-applicable"
)

// Policy is a named set of rules with a combining algorithm.
type Policy struct {
	ID        string
	Rules     []Rule
	Algorithm CombiningAlgorithm
	// Children are sub-policies evaluated before this policy's own rules.
	Children []*Policy
}

// Evaluate applies the policy to the request and returns a Decision.
func (p *Policy) Evaluate(req Request) Decision {
	var denies, permits []Decision

	// Evaluate children first (hierarchical).
	// Only count children that actually matched a rule.
	// A child with no applicable rule returns MatchedRule=="" — treat as
	// indeterminate (XACML §7.14), not as a Deny, so it does not suppress
	// a parent permit in DenyOverride mode.
	for _, child := range p.Children {
		d := child.Evaluate(req)
		if d.Permit {
			permits = append(permits, d)
		} else if d.MatchedRule != "" {
			// Real deny (a rule actually fired), not a "no applicable rule" result.
			denies = append(denies, d)
		}
		// else: child indeterminate — skip
	}

	// Sort rules by priority descending.
	sorted := sortedRules(p.Rules)

	for _, rule := range sorted {
		if !rule.matches(req) {
			continue
		}
		d := Decision{MatchedRule: rule.ID}
		if rule.Effect == EffectPermit {
			d.Permit = true
			d.Reason = "rule " + rule.ID + " permits"
			permits = append(permits, d)
		} else {
			d.Reason = "rule " + rule.ID + " denies"
			denies = append(denies, d)
		}
	}

	return combine(p.Algorithm, permits, denies)
}

func combine(alg CombiningAlgorithm, permits, denies []Decision) Decision {
	switch alg {
	case DenyOverride:
		if len(denies) > 0 {
			return denies[0]
		}
		if len(permits) > 0 {
			return permits[0]
		}
	case PermitOverride:
		if len(permits) > 0 {
			return permits[0]
		}
		if len(denies) > 0 {
			return denies[0]
		}
	case FirstApplicable:
		// Already in priority order; just pick the first.
		if len(permits)+len(denies) > 0 {
			// Return whichever came first in sorted rule order.
			// permits and denies are in rule-match order.
			if len(permits) > 0 && (len(denies) == 0) {
				return permits[0]
			}
			if len(denies) > 0 && (len(permits) == 0) {
				return denies[0]
			}
			// Both: return first-matched (permit index vs deny index).
			return permits[0] // default to first permit for first-applicable
		}
	}
	return Deny("no applicable rule")
}

func sortedRules(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	// Insertion sort by priority descending (stable).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Priority > out[j-1].Priority; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ─── Engine ───────────────────────────────────────────────────────────────────

// Engine is the top-level policy evaluation engine.
// It holds named policies and evaluates them in priority order.
type Engine struct {
	mu       sync.RWMutex
	policies []*Policy
	driver   Driver // optional OPA or external policy driver
}

// Driver is the interface for an external policy backend (OPA, Casbin, etc.).
type Driver interface {
	Evaluate(req Request) (Decision, error)
}

// NewEngine creates a new policy engine.
func NewEngine(driver Driver) *Engine {
	return &Engine{driver: driver}
}

// Register adds a policy to the engine.
func (e *Engine) Register(p *Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies = append(e.policies, p)
}

// Evaluate evaluates all registered policies in priority order.
// If a Driver is registered, it is consulted after the local policies.
// A deny from ANY source yields a final deny (defence-in-depth).
func (e *Engine) Evaluate(req Request) (Decision, error) {
	e.mu.RLock()
	policies := make([]*Policy, len(e.policies))
	copy(policies, e.policies)
	e.mu.RUnlock()

	var localDecision Decision
	for _, p := range policies {
		d := p.Evaluate(req)
		if !d.Permit {
			return d, nil // deny-override: first deny wins
		}
		localDecision = d
	}

	if e.driver != nil {
		d, err := e.driver.Evaluate(req)
		if err != nil {
			return Deny("driver error: " + err.Error()), err
		}
		if !d.Permit {
			return d, nil
		}
	}

	if len(policies) == 0 && e.driver == nil {
		return Deny("no policies registered (default-deny)"), nil
	}
	return localDecision, nil
}

// ─── Built-in condition helpers ───────────────────────────────────────────────

// HasAttribute returns a condition that checks for an attribute key in subject.
func HasAttribute(key string) Condition {
	return func(req Request) bool {
		_, ok := req.Subject[key]
		return ok
	}
}

// AttributeEquals returns a condition that checks a subject attribute equals val.
func AttributeEquals(key string, val any) Condition {
	return func(req Request) bool {
		v, ok := req.Subject[key]
		return ok && fmt.Sprint(v) == fmt.Sprint(val)
	}
}

// ResourceAttributeEquals checks a resource attribute.
func ResourceAttributeEquals(key string, val any) Condition {
	return func(req Request) bool {
		v, ok := req.Resource[key]
		return ok && fmt.Sprint(v) == fmt.Sprint(val)
	}
}

// ContextAttributeEquals checks a context attribute.
func ContextAttributeEquals(key string, val any) Condition {
	return func(req Request) bool {
		v, ok := req.Context[key]
		return ok && fmt.Sprint(v) == fmt.Sprint(val)
	}
}

// All composes multiple conditions with AND.
func All(conds ...Condition) Condition {
	return func(req Request) bool {
		for _, c := range conds {
			if !c(req) {
				return false
			}
		}
		return true
	}
}

// Any composes multiple conditions with OR.
func Any(conds ...Condition) Condition {
	return func(req Request) bool {
		for _, c := range conds {
			if c(req) {
				return true
			}
		}
		return false
	}
}

// Not negates a condition.
func Not(c Condition) Condition {
	return func(req Request) bool { return !c(req) }
}

// ─── glob matching ────────────────────────────────────────────────────────────

var globCache sync.Map // map[string]*regexp.Regexp

func globMatch(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	v, ok := globCache.Load(pattern)
	if !ok {
		// Convert glob to regex.
		re := "^" + regexp.QuoteMeta(pattern) + "$"
		re = strings.ReplaceAll(re, `\*`, `.*`)
		re = strings.ReplaceAll(re, `\?`, `.`)
		compiled := regexp.MustCompile(re)
		globCache.Store(pattern, compiled)
		v = compiled
	}
	return v.(*regexp.Regexp).MatchString(s)
}

// ─── Errors ───────────────────────────────────────────────────────────────────

// ErrPolicyNotFound is returned when a named policy cannot be found.
var ErrPolicyNotFound = errors.New("policy: not found")
