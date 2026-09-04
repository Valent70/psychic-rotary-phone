// Package policy is the VERIQO authorization decision point.
//
// # Why authorization is not middleware
//
// Law 10: security is part of semantics. An API middleware answers
// "may this caller reach this endpoint". The question VERIQO has to
// answer is narrower and harder:
//
//	may THIS principal perform THIS action on THIS property of THIS
//	object, in THIS case, for THIS declared purpose, given the
//	classification of the material and the rights under which it was
//	acquired?
//
// That decision cannot be made at the edge, because at the edge the
// material has not been fetched yet and its classification is not
// known. So the decision point lives here and is called by the layers
// that hold the object -- and the request carries a purpose, because
// purpose limitation is not derivable from the principal.
//
// # Fail closed, and say which rule closed it
//
// The combining algorithm is deny-overrides with a default of DENY.
// An empty policy set denies everything; a rule that errors denies;
// a request with no purpose denies. Every denial names the rule that
// produced it, because "denied" with no reason is indistinguishable
// from a bug and gets worked around rather than fixed.
package policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/authority"
	"veriqo/pkg/contract"
	"veriqo/pkg/governance/classification"
	"veriqo/pkg/identity"
)

var (
	ErrNoPurpose = errors.New("policy: request declares no purpose")
	ErrDenied    = errors.New("policy: denied")
	ErrNoRules   = errors.New("policy: no rules loaded; an empty policy set denies everything")
	ErrBadRule   = errors.New("policy: malformed rule")
)

// Purpose is the declared reason a request is being made.
//
// It is an enumeration rather than free text because purpose
// limitation only works if the set of purposes is fixed and the rights
// attached to data can name them. "Because I need it" is not a purpose.
type Purpose string

const (
	CaseInvestigation    Purpose = "CASE_INVESTIGATION"
	QualityAssurance     Purpose = "QUALITY_ASSURANCE"
	RegulatoryProduction Purpose = "REGULATORY_PRODUCTION"
	CustomerExport       Purpose = "CUSTOMER_EXPORT"
	SecurityIncident     Purpose = "SECURITY_INCIDENT"
	SystemMaintenance    Purpose = "SYSTEM_MAINTENANCE"
	ModelTraining        Purpose = "MODEL_TRAINING"
	ResearchDiscovery    Purpose = "RESEARCH_DISCOVERY"
)

func Purposes() []Purpose {
	return []Purpose{CaseInvestigation, QualityAssurance, RegulatoryProduction,
		CustomerExport, SecurityIncident, SystemMaintenance, ModelTraining, ResearchDiscovery}
}

func (p Purpose) Valid() bool {
	for _, k := range Purposes() {
		if k == p {
			return true
		}
	}
	return false
}

// Effect is what a rule says.
type Effect string

const (
	Permit        Effect = "PERMIT"
	Deny          Effect = "DENY"
	NotApplicable Effect = "NOT_APPLICABLE"
)

// Request is the tuple a decision is made over.
type Request struct {
	Principal identity.Principal
	Grants    []authority.Grant

	Action  authority.Capability
	Purpose Purpose

	// Resource attributes.
	TenantID       string
	CaseID         string
	ObjectType     string
	PropertyPath   string // empty means the whole object
	Classification classification.Marking

	// At is the decision instant. It is supplied, never read from the
	// wall clock, so a replay makes the same decision.
	At time.Time

	// Attributes carries domain-specific ABAC facts (jurisdiction,
	// residency, source licence class). Rules read them by name.
	Attributes map[string]string
}

func (r Request) attr(k string) string { return r.Attributes[k] }

// Decision is the answer, with its reason.
type Decision struct {
	Effect Effect `json:"effect"`
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
	// Obligations are things the caller MUST do having been permitted:
	// redact a property, watermark an export, write an audit record at
	// a particular level. A permit whose obligations are ignored is a
	// denial that did not happen.
	Obligations []Obligation `json:"obligations,omitempty"`
}

func (d Decision) Permitted() bool { return d.Effect == Permit }

// Obligation is a mandatory follow-on action.
type Obligation struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

const (
	ObligationRedactProperty = "REDACT_PROPERTY"
	ObligationWatermark      = "WATERMARK"
	ObligationAuditElevated  = "AUDIT_ELEVATED"
	ObligationHumanReview    = "HUMAN_REVIEW"
	ObligationRecordPurpose  = "RECORD_PURPOSE"
)

// Rule is one named policy rule.
type Rule struct {
	Name string
	// Applies narrows the rule. A rule that applies to everything is
	// usually a mistake, and one that applies to nothing is dead.
	Applies func(Request) bool
	// Evaluate returns PERMIT, DENY or NOT_APPLICABLE with a reason.
	Evaluate func(Request) Decision
}

func (r Rule) validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("%w: unnamed rule", ErrBadRule)
	}
	if r.Applies == nil || r.Evaluate == nil {
		return fmt.Errorf("%w: %s has no Applies or no Evaluate", ErrBadRule, r.Name)
	}
	return nil
}

// Engine holds an ordered rule set and its version.
type Engine struct {
	version contract.Version
	rules   []Rule
}

// New builds an engine. It refuses an empty rule set at construction
// rather than denying silently at every call, so a misconfiguration
// surfaces at startup.
func New(version contract.Version, rules ...Rule) (*Engine, error) {
	if len(rules) == 0 {
		return nil, ErrNoRules
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if err := r.validate(); err != nil {
			return nil, err
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("%w: duplicate rule name %q", ErrBadRule, r.Name)
		}
		seen[r.Name] = true
	}
	if version.Zero() {
		return nil, fmt.Errorf("%w: an unversioned policy set cannot be replayed", ErrBadRule)
	}
	return &Engine{version: version, rules: append([]Rule(nil), rules...)}, nil
}

// Version returns the policy version, which every decision made under
// it must be recorded with.
func (e *Engine) Version() contract.Version { return e.version }

// Decide evaluates the request.
//
// Deny-overrides: a single DENY from any applicable rule ends it. If
// no rule denies and at least one permits, the result is PERMIT with
// the union of the obligations. If nothing applies, the result is DENY
// -- an unregulated request is not an allowed one.
func (e *Engine) Decide(req Request) Decision {
	if !req.Purpose.Valid() {
		return Decision{Effect: Deny, Rule: "core/purpose-required",
			Reason: ErrNoPurpose.Error()}
	}
	if req.At.IsZero() {
		return Decision{Effect: Deny, Rule: "core/instant-required",
			Reason: "the decision instant was not supplied, so this decision cannot be replayed"}
	}
	if d := e.core(req); d.Effect != NotApplicable {
		return d
	}

	permitted := false
	var obligations []Obligation
	var permittingRule string

	for _, r := range e.rules {
		if !r.Applies(req) {
			continue
		}
		d := r.Evaluate(req)
		switch d.Effect {
		case Deny:
			d.Rule = r.Name
			return d
		case Permit:
			permitted = true
			if permittingRule == "" {
				permittingRule = r.Name
			}
			obligations = append(obligations, d.Obligations...)
		}
	}
	if !permitted {
		return Decision{Effect: Deny, Rule: "core/default-deny",
			Reason: "no rule permitted this request; an unregulated request is not an allowed one"}
	}
	return Decision{Effect: Permit, Rule: permittingRule,
		Reason:      "permitted",
		Obligations: normaliseObligations(obligations)}
}

// core holds the checks no configurable rule may override.
//
// These are laws, not policy. A customer cannot switch off tenant
// isolation or Law 7 by editing a rule set, and putting them in the
// same list as configurable rules would let a PERMIT earlier in the
// order shadow them.
func (e *Engine) core(req Request) Decision {
	na := Decision{Effect: NotApplicable}

	if err := req.Principal.Validate(); err != nil {
		return Decision{Effect: Deny, Rule: "core/principal", Reason: err.Error()}
	}
	if err := req.Principal.Active(req.At); err != nil {
		return Decision{Effect: Deny, Rule: "core/credential-window", Reason: err.Error()}
	}
	if req.TenantID == "" || req.TenantID != req.Principal.TenantID {
		return Decision{Effect: Deny, Rule: "core/tenant-isolation",
			Reason: fmt.Sprintf("%v: principal is in %q, resource is in %q",
				contract.ErrCrossTenant, req.Principal.TenantID, req.TenantID)}
	}

	// The principal must hold the capability under some grant that
	// covers this case.
	var lastErr error
	authorised := false
	for _, g := range req.Grants {
		if g.CaseID != "" && req.CaseID != "" && g.CaseID != req.CaseID {
			continue
		}
		if err := authority.Check(req.Principal, g, req.Action); err != nil {
			lastErr = err
			continue
		}
		authorised = true
		break
	}
	if !authorised {
		reason := "principal holds no grant carrying this capability"
		if lastErr != nil {
			reason = lastErr.Error()
		}
		return Decision{Effect: Deny, Rule: "core/authority", Reason: reason}
	}

	// Purpose limitation on training data is a law, not a preference:
	// data acquired for investigation is not training data because
	// somebody selected MODEL_TRAINING in a dropdown.
	if req.Purpose == ModelTraining && req.Classification.Has(classification.NoTraining) {
		return Decision{Effect: Deny, Rule: "core/no-training",
			Reason: "the material carries NO_TRAINING"}
	}
	if req.Action == authority.Export && req.Classification.Has(classification.NoExport) {
		return Decision{Effect: Deny, Rule: "core/no-export",
			Reason: "the material carries NO_EXPORT"}
	}
	if req.Classification.Has(classification.UnderLegalHold) &&
		req.Purpose == SystemMaintenance {
		return Decision{Effect: Deny, Rule: "core/legal-hold",
			Reason: "material under legal hold is not available for maintenance operations"}
	}
	return na
}

func normaliseObligations(in []Obligation) []Obligation {
	seen := map[Obligation]bool{}
	var out []Obligation
	for _, o := range in {
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

// --- The baseline rule set ---------------------------------------------

// Baseline returns the rules every deployment starts with. A
// deployment may add rules; the core checks above are not among them
// and cannot be removed.
func Baseline() []Rule {
	return []Rule{
		{
			Name: "baseline/clearance",
			Applies: func(r Request) bool {
				return r.Classification.Valid()
			},
			Evaluate: func(r Request) Decision {
				clearance, _ := classification.New(clearanceLevel(r), clearanceCaveats(r)...)
				if err := classification.Readable(clearance, r.Classification); err != nil {
					return Decision{Effect: Deny, Reason: err.Error()}
				}
				var ob []Obligation
				if r.Classification.Level >= classification.Restricted {
					ob = append(ob, Obligation{Kind: ObligationAuditElevated,
						Detail: r.Classification.String()})
				}
				return Decision{Effect: Permit, Obligations: ob}
			},
		},
		{
			Name: "baseline/agent-output-requires-review",
			Applies: func(r Request) bool {
				return r.Principal.Kind.RequiresHumanReviewOfOutput() && r.Action == authority.Propose
			},
			Evaluate: func(r Request) Decision {
				return Decision{Effect: Permit, Obligations: []Obligation{
					{Kind: ObligationHumanReview, Detail: "agent proposal"},
				}}
			},
		},
		{
			Name:    "baseline/export-is-watermarked-and-recorded",
			Applies: func(r Request) bool { return r.Action == authority.Export },
			Evaluate: func(r Request) Decision {
				return Decision{Effect: Permit, Obligations: []Obligation{
					{Kind: ObligationWatermark},
					{Kind: ObligationRecordPurpose, Detail: string(r.Purpose)},
					{Kind: ObligationAuditElevated, Detail: "export"},
				}}
			},
		},
		{
			Name: "baseline/personal-data-property-redaction",
			Applies: func(r Request) bool {
				return r.Classification.Has(classification.PersonalData) &&
					r.Purpose != RegulatoryProduction
			},
			Evaluate: func(r Request) Decision {
				return Decision{Effect: Permit, Obligations: []Obligation{
					{Kind: ObligationRedactProperty, Detail: "personal identifiers"},
				}}
			},
		},
		{
			Name: "baseline/residency",
			Applies: func(r Request) bool {
				return r.attr("data_residency") != "" && r.attr("processing_region") != ""
			},
			Evaluate: func(r Request) Decision {
				if r.attr("data_residency") != r.attr("processing_region") {
					return Decision{Effect: Deny, Reason: fmt.Sprintf(
						"material must remain in %s; this request processes in %s",
						r.attr("data_residency"), r.attr("processing_region"))}
				}
				return Decision{Effect: Permit}
			},
		},
	}
}

// clearanceLevel reads the principal's clearance from attributes. A
// principal with no stated clearance gets PUBLIC, not the level of
// whatever they asked for.
func clearanceLevel(r Request) classification.Level {
	l, err := classification.ParseLevel(r.attr("clearance"))
	if err != nil {
		return classification.Public
	}
	return l
}

func clearanceCaveats(r Request) []classification.Handling {
	raw := r.attr("clearance_caveats")
	if raw == "" {
		return nil
	}
	var out []classification.Handling
	for _, s := range strings.Split(raw, ",") {
		h := classification.Handling(strings.TrimSpace(s))
		if h.Valid() {
			out = append(out, h)
		}
	}
	return out
}
