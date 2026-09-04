// Package domain supplies what each vertical adds to the shared
// engine, and nothing else.
//
// # What a domain package is allowed to be
//
// The specification is explicit:
//
//	Domain package hanya menyediakan: ontology, evidence adapters,
//	rules, hypothesis templates, domain terminology, qualification
//	policies. Core evidence/decision machinery tetap satu.
//
// So there is no maritime claim engine and no insurance graph. A
// domain contributes TEMPLATES -- the hypotheses an analyst should
// consider, the necessary conditions a claim of that shape has, the
// refusals that shape must always make -- and the shared engine runs
// them.
//
// # Why the templates are the valuable part
//
// A reverse proof is only as good as its decomposition, and a
// decomposition written from scratch under time pressure omits the
// condition nobody thought of. A template is the accumulated list of
// what has to hold for a claim of this kind, so the omission becomes
// visible as an unexamined condition rather than invisible as a
// question nobody asked.
//
// # The refusals are per-domain and non-negotiable
//
// Each domain declares statements VERIQO must never make. Insurance
// must not say "covered". Finance must not infer that a payment was
// executed from an instruction to execute it. These are not policy
// settings; they are what stops the system from making a determination
// it has no standing to make.
package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/hypothesis"
	"veriqo/pkg/reverseproof"
)

var (
	ErrUnknownDomain   = errors.New("domain: unknown domain")
	ErrUnknownTemplate = errors.New("domain: unknown template")
	ErrForbiddenClaim  = errors.New("domain: this domain may not make that statement")
)

// Template is a reusable claim shape.
type Template struct {
	// ID is stable and citable.
	ID string
	// Domain is the view it belongs to.
	Domain string
	// Question is what an analyst is investigating.
	Question string
	// Conditions are the necessary conditions a claim of this shape
	// has. They arrive UNEXAMINED: the template supplies the
	// questions, not the answers.
	Conditions []reverseproof.Condition
	// Hypotheses are the competing explanations that must be
	// considered. A template with one hypothesis would be a template
	// for confirming a conclusion.
	Hypotheses []hypothesis.Hypothesis
	// Refusals are statements this template's output may never make.
	Refusals []string
}

func (t Template) Validate() error {
	if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.Domain) == "" {
		return errors.New("domain: a template needs an id and a domain")
	}
	if strings.TrimSpace(t.Question) == "" {
		return fmt.Errorf("domain: template %s asks no question", t.ID)
	}
	if len(t.Conditions) < 2 {
		return fmt.Errorf("domain: template %s decomposes into %d condition(s); a claim shape "+
			"with fewer than two necessary conditions has not been decomposed", t.ID,
			len(t.Conditions))
	}
	for _, c := range t.Conditions {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("domain: template %s: %w", t.ID, err)
		}
		if c.State != reverseproof.Unexamined {
			return fmt.Errorf("domain: template %s ships condition %s in state %s; a template "+
				"supplies the questions, not the answers", t.ID, c.ID, c.State)
		}
	}
	if len(t.Hypotheses) < 2 {
		return fmt.Errorf("domain: template %s offers %d hypothesis(es); one hypothesis is a "+
			"template for confirming a conclusion", t.ID, len(t.Hypotheses))
	}
	for _, h := range t.Hypotheses {
		if err := h.Validate(); err != nil {
			return fmt.Errorf("domain: template %s: %w", t.ID, err)
		}
	}
	return nil
}

// Conditions returns a fresh copy, so a caller filling one in does not
// mutate the template every later case will use.
func (t Template) NewConditions() []reverseproof.Condition {
	out := make([]reverseproof.Condition, len(t.Conditions))
	copy(out, t.Conditions)
	for i := range out {
		out[i].EvidenceRefs = nil
		out[i].State = reverseproof.Unexamined
		out[i].Note = ""
	}
	return out
}

// NewHypotheses returns a fresh copy of the hypothesis set.
func (t Template) NewHypotheses() []hypothesis.Hypothesis {
	out := make([]hypothesis.Hypothesis, len(t.Hypotheses))
	copy(out, t.Hypotheses)
	return out
}

// cond is a shorthand for a template condition.
func cond(id, must, expected string, diag, cost float64, lawful bool, sources ...string) reverseproof.Condition {
	return reverseproof.Condition{ID: id, Must: must, Expected: expected,
		Sources: sources, Diagnosticity: diag, AcquisitionCost: cost,
		LegallyAccessible: lawful}
}

func hyp(id, statement string, expected []string, excluded ...string) hypothesis.Hypothesis {
	return hypothesis.Hypothesis{ID: id, Statement: statement,
		Expected: expected, Excluded: excluded}
}

// Registry holds every domain's templates.
type Registry struct {
	byID     map[string]Template
	byDomain map[string][]Template
	refusals map[string][]string
}

// NewRegistry builds and validates the registry.
func NewRegistry(ts []Template, refusals map[string][]string) (*Registry, error) {
	r := &Registry{byID: map[string]Template{}, byDomain: map[string][]Template{},
		refusals: map[string][]string{}}
	for _, t := range ts {
		if err := t.Validate(); err != nil {
			return nil, err
		}
		if _, dup := r.byID[t.ID]; dup {
			return nil, fmt.Errorf("domain: duplicate template %s", t.ID)
		}
		r.byID[t.ID] = t
		r.byDomain[t.Domain] = append(r.byDomain[t.Domain], t)
	}
	for d, rs := range refusals {
		if len(rs) == 0 {
			return nil, fmt.Errorf("domain: %s declares no refusals; every domain has "+
				"statements it may not make", d)
		}
		r.refusals[d] = append([]string(nil), rs...)
	}
	return r, nil
}

// Template returns one by id.
func (r *Registry) Template(id string) (Template, error) {
	t, ok := r.byID[id]
	if !ok {
		return Template{}, fmt.Errorf("%w: %s", ErrUnknownTemplate, id)
	}
	return t, nil
}

// ForDomain returns a domain's templates, sorted.
func (r *Registry) ForDomain(d string) []Template {
	out := append([]Template(nil), r.byDomain[d]...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Domains returns every domain with templates.
func (r *Registry) Domains() []string {
	var out []string
	for d := range r.byDomain {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Refusals returns a domain's forbidden statements.
func (r *Registry) Refusals(d string) []string {
	return append([]string(nil), r.refusals[d]...)
}

// CheckStatement refuses a conclusion a domain may not make.
//
// It is a substring check over the declared refusals, deliberately
// blunt. A cleverer matcher would have gaps; a blunt one produces
// false positives that a reviewer resolves by rewording, which is the
// safe direction to be wrong in.
func (r *Registry) CheckStatement(domain, statement string) error {
	s := strings.ToLower(statement)
	for _, forbidden := range r.refusals[domain] {
		if strings.Contains(s, strings.ToLower(forbidden)) {
			return fmt.Errorf("%w: %s output may not state %q. VERIQO qualifies evidence; "+
				"it does not make that determination", ErrForbiddenClaim, domain, forbidden)
		}
	}
	return nil
}

// Veriqo builds the registry with every domain's templates.
func Veriqo() (*Registry, error) {
	var ts []Template
	ts = append(ts, MaritimeTemplates()...)
	ts = append(ts, InsuranceTemplates()...)
	ts = append(ts, CommodityTemplates()...)
	ts = append(ts, SupplyChainTemplates()...)
	ts = append(ts, FinanceTemplates()...)
	return NewRegistry(ts, Refusals())
}

// Refusals are the statements each domain may never make.
func Refusals() map[string][]string {
	return map[string][]string{
		"maritime": {
			"the vessel was smuggling",
			"the crew falsified",
		},
		"insurance": {
			// The central one. VERIQO reports applicability and the
			// evidence for and against it; the coverage determination
			// belongs to the insurer and, ultimately, to a tribunal.
			"is covered",
			"is not covered",
			"coverage is denied",
			"the policy responds",
			"the claim is fraudulent",
		},
		"commodity": {
			"the seller breached",
			"the cargo was stolen",
		},
		"supplychain": {
			"the supplier is insolvent",
		},
		"finance": {
			// Never infer execution from instruction.
			"the payment was executed",
			"the payment settled",
			"the funds were received",
			"money laundering occurred",
			"the beneficiary is a front",
		},
		"dispute": {
			"is liable",
			"is not liable",
			"breached the contract",
		},
	}
}
