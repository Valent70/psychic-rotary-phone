// Package reasoning implements Veriqo's Reasoning Kernel — the open
// item named in the gap report as "Reasoning Kernel penuh (general
// inference): Ontology masih validasi struktural, bukan general
// reasoning."
//
// Honest scope: this is a real, general-purpose forward-chaining
// (Rete-lite, naive fixpoint) inference engine over ground facts and
// Horn-clause-shaped rules (conjunctions of positive triple patterns ->
// a derived triple) — the same computational model production rule
// engines (CLIPS, Drools) and Datalog use, implemented from scratch in
// ~150 lines of dependency-free Go. It is NOT a full first-order theorem
// prover (no negation-as-failure, no function symbols, no unification
// beyond ground/variable triple matching, no explanation-tree UI) — that
// is a substantially larger, separate research effort and remains
// honestly out of scope. What it DOES give Veriqo that the ontology
// package explicitly does not: deriving NEW facts from combinations of
// existing facts and declarative rules, to a computed fixpoint, with a
// full derivation trace per derived fact for auditability.
package reasoning

import (
	"fmt"
	"sort"
)

// Triple is one ground fact: (Subject, Predicate, Object). Object may
// also hold a variable reference when used inside a Rule's pattern list
// (see Var).
type Triple struct {
	Subject   string
	Predicate string
	Object    string
}

func (t Triple) String() string { return fmt.Sprintf("(%s %s %s)", t.Subject, t.Predicate, t.Object) }

// Var marks a placeholder inside a Rule pattern, e.g. Var("x") matches
// any Subject/Object and binds it for reuse across the rule's other
// patterns and its Then conclusion.
func Var(name string) string { return "?" + name }

func isVar(s string) bool { return len(s) > 0 && s[0] == '?' }

// Rule is one Horn-clause-shaped inference rule: if every pattern in
// If matches some fact (with consistent variable bindings across
// patterns), derive Then with those bindings substituted in.
type Rule struct {
	Name string
	If   []Triple
	Then Triple
}

// Derivation records why one fact was derived — which rule fired and
// which ground facts satisfied its premises — the audit trail a general
// reasoner needs to be trustworthy rather than a black box.
type Derivation struct {
	Fact     Triple
	RuleName string
	Premises []Triple
}

// KB is a knowledge base: a set of ground facts plus rules, supporting
// forward-chaining inference to a fixpoint.
type KB struct {
	facts       map[Triple]bool
	rules       []Rule
	derivations map[Triple]Derivation // only for facts derived by a rule, not asserted directly
}

func NewKB() *KB {
	return &KB{facts: make(map[Triple]bool), derivations: make(map[Triple]Derivation)}
}

// Assert adds a ground fact directly (not derived by any rule).
func (kb *KB) Assert(t Triple) {
	kb.facts[t] = true
}

// AddRule registers an inference rule.
func (kb *KB) AddRule(r Rule) {
	kb.rules = append(kb.rules, r)
}

// bindings maps variable names (without the leading '?') to bound values.
type bindings map[string]string

// matchPattern attempts to unify pattern against fact under the given
// bindings, returning extended bindings on success.
func matchPattern(pattern, fact Triple, b bindings) (bindings, bool) {
	nb := bindings{}
	for k, v := range b {
		nb[k] = v
	}
	fields := [][2]string{{pattern.Subject, fact.Subject}, {pattern.Predicate, fact.Predicate}, {pattern.Object, fact.Object}}
	for _, f := range fields {
		pv, fv := f[0], f[1]
		if isVar(pv) {
			name := pv[1:]
			if bound, ok := nb[name]; ok {
				if bound != fv {
					return nil, false
				}
			} else {
				nb[name] = fv
			}
		} else if pv != fv {
			return nil, false
		}
	}
	return nb, true
}

func substitute(t Triple, b bindings) Triple {
	sub := func(s string) string {
		if isVar(s) {
			if v, ok := b[s[1:]]; ok {
				return v
			}
		}
		return s
	}
	return Triple{Subject: sub(t.Subject), Predicate: sub(t.Predicate), Object: sub(t.Object)}
}

// solvePattern returns every (bindings, matchedFact) pair extending b
// that satisfies pattern against the current fact set.
func (kb *KB) solvePattern(pattern Triple, b bindings) []struct {
	b bindings
	f Triple
} {
	var out []struct {
		b bindings
		f Triple
	}
	for fact := range kb.facts {
		if nb, ok := matchPattern(pattern, fact, b); ok {
			out = append(out, struct {
				b bindings
				f Triple
			}{nb, fact})
		}
	}
	return out
}

// Infer runs forward-chaining to a fixpoint: repeatedly evaluate every
// rule's premises against the current fact set, add any newly derivable
// facts, and stop when a full pass adds nothing new. Returns the number
// of new facts derived. Guarded against runaway rule sets with a hard
// iteration cap, since a maliciously or accidentally cyclic rule set
// over a bounded universe still terminates (facts are deduplicated) but
// a buggy rule set producing unbounded new constants would not — the
// cap turns that into a bounded, reported condition instead of a hang.
func (kb *KB) Infer() (newFacts int, err error) {
	const maxIterations = 10000
	for iter := 0; iter < maxIterations; iter++ {
		addedThisPass := 0
		for _, rule := range kb.rules {
			combos := []bindings{{}}
			for _, pat := range rule.If {
				var next []bindings
				for _, b := range combos {
					for _, sol := range kb.solvePattern(pat, b) {
						next = append(next, sol.b)
					}
				}
				combos = next
				if len(combos) == 0 {
					break
				}
			}
			for _, b := range combos {
				derived := substitute(rule.Then, b)
				if isVar(derived.Subject) || isVar(derived.Predicate) || isVar(derived.Object) {
					continue // unbound variable in conclusion; skip (open-world safety)
				}
				if !kb.facts[derived] {
					kb.facts[derived] = true
					var premises []Triple
					for _, pat := range rule.If {
						premises = append(premises, substitute(pat, b))
					}
					kb.derivations[derived] = Derivation{Fact: derived, RuleName: rule.Name, Premises: premises}
					addedThisPass++
					newFacts++
				}
			}
		}
		if addedThisPass == 0 {
			return newFacts, nil
		}
	}
	return newFacts, fmt.Errorf("reasoning: did not reach a fixpoint within %d iterations (possible unbounded rule set)", maxIterations)
}

// Query returns every ground fact currently in the KB matching pattern
// (variables allowed), each with its resolved bindings.
func (kb *KB) Query(pattern Triple) []Triple {
	var out []Triple
	for _, sol := range kb.solvePattern(pattern, bindings{}) {
		out = append(out, sol.f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// Explain returns the derivation trace for a derived fact, or false if
// the fact was directly asserted (no rule fired for it) or is unknown.
func (kb *KB) Explain(t Triple) (Derivation, bool) {
	d, ok := kb.derivations[t]
	return d, ok
}

// Facts returns every fact (asserted or derived) currently held.
func (kb *KB) Facts() []Triple {
	out := make([]Triple, 0, len(kb.facts))
	for f := range kb.facts {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
