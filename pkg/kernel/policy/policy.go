// Package policy implements Veriqo's centralized, OPA-style action-gating
// layer — the open item named directly in the gap report: "Policy Engine
// bergaya OPA (action gating end-to-end): Decision policy ada, belum ada
// gating allow/deny/approve terpusat."
//
// Rather than vendoring a Rego interpreter (a large external dependency
// this sandbox cannot fetch), Gate implements the same *contract* real
// OPA-fronted systems expose — evaluate a named action against a bundle
// of inputs and return one of Allow/Deny/RequireApproval with a
// deterministic, auditable trail of which rule fired — using small,
// composable Go predicates instead of a Rego AST. This is the honest
// trade-off: no general-purpose policy query language, but the same
// centralized-gate semantics, fully hash-chained for tamper-evidence
// like every other MOAT ledger in this repo.
//
// Gate composes the three engines the audit named as fragmented:
//   - trust.Kernel: "is this subject trustworthy enough" (TrustScore)
//   - decision.Engine: "what does risk policy say the action should be"
//   - ontology.Manager: "is the acting subject/instance well-formed"
//
// into ONE call: Gate.Evaluate(action, subject, ...) -> Verdict.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Verdict is the terminal outcome of gating one action.
type Verdict string

const (
	Allow           Verdict = "ALLOW"
	Deny            Verdict = "DENY"
	RequireApproval Verdict = "REQUIRE_APPROVAL"
)

// Rule is one named predicate a Gate evaluates, in registration order.
// The first Rule to return a non-empty Verdict short-circuits evaluation
// (first-match-wins, the same semantics as an OPA "deny[msg]" set
// evaluated in a fixed priority order) — Rules later in the list act as
// fallbacks/defaults.
type Rule struct {
	Name string
	// Eval inspects Input and returns a Verdict to short-circuit on, or
	// "" to defer to the next rule.
	Eval func(in Input) (v Verdict, reason string)
}

// Input is everything one gating decision needs. Callers populate only
// the fields relevant to the rules they've registered; a nil/zero field
// simply means "no trust score available" etc., and rules must handle
// that explicitly (matching OPA's "undefined is not an error" posture).
type Input struct {
	Action     string
	Subject    string
	ActorID    string
	TrustScore *float64 // 0..1, from trust.Kernel.Evaluate, if computed
	RiskScore  *float64 // 0..1, from decision.Engine.Decide, if computed
	RiskAction string   // decision.Action string form, if computed
	OntologyOK *bool    // ontology.Manager.Validate result, if checked
	Attributes map[string]string
}

// Record is one hash-chained gating decision, appended to Gate's audit
// trail on every Evaluate call — so "who was allowed to do what, and
// under which rule" is itself independently verifiable, consistent with
// every other MOAT engine in this repo.
type Record struct {
	Index    uint64
	Action   string
	Subject  string
	Verdict  Verdict
	RuleName string
	Reason   string
	PrevHash string
	Hash     string
}

func (r Record) computeHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s|%s|%s|%s|%s", r.Index, r.Action, r.Subject, r.Verdict, r.RuleName, r.Reason, r.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

var ErrNoDefaultVerdict = errors.New("policy: no rule matched and no default verdict configured")
var ErrChainBroken = errors.New("policy: hash chain verification failed")

// Gate is the centralized action-gating engine.
type Gate struct {
	mu    sync.Mutex
	rules []Rule
	deflt Verdict // returned if no rule matches; "" means ErrNoDefaultVerdict
	log   []Record
}

// NewGate builds a Gate with a default verdict applied when no
// registered Rule matches — pass Deny for a fail-closed posture (the
// recommended default for production) or Allow for fail-open.
func NewGate(defaultVerdict Verdict) *Gate {
	return &Gate{deflt: defaultVerdict}
}

// RegisterRule appends a Rule, evaluated in registration order.
func (g *Gate) RegisterRule(r Rule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rules = append(g.rules, r)
}

// Evaluate runs every registered Rule in order against in, stopping at
// the first non-empty Verdict (or falling back to the Gate's default),
// and appends a hash-chained Record of the outcome.
func (g *Gate) Evaluate(in Input) (Verdict, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	verdict := g.deflt
	ruleName := "<default>"
	reason := "no rule matched; applied default verdict"
	for _, r := range g.rules {
		if v, why := r.Eval(in); v != "" {
			verdict, ruleName, reason = v, r.Name, why
			break
		}
	}
	if verdict == "" {
		return "", ErrNoDefaultVerdict
	}

	prev := ""
	if n := len(g.log); n > 0 {
		prev = g.log[n-1].Hash
	}
	rec := Record{
		Index: uint64(len(g.log)) + 1, Action: in.Action, Subject: in.Subject,
		Verdict: verdict, RuleName: ruleName, Reason: reason, PrevHash: prev,
	}
	rec.Hash = rec.computeHash()
	g.log = append(g.log, rec)
	return verdict, nil
}

// Ledger returns a copy of every gating decision made, oldest first.
func (g *Gate) Ledger() []Record {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Record, len(g.log))
	copy(out, g.log)
	return out
}

// VerifyChain independently recomputes every Record's hash and confirms
// PrevHash linkage, proving the log was not edited or reordered after
// the fact.
func (g *Gate) VerifyChain() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	prev := ""
	for i, rec := range g.log {
		want := rec
		want.Hash = ""
		recomputed := (Record{
			Index: rec.Index, Action: rec.Action, Subject: rec.Subject,
			Verdict: rec.Verdict, RuleName: rec.RuleName, Reason: rec.Reason, PrevHash: prev,
		}).computeHash()
		if recomputed != rec.Hash {
			return fmt.Errorf("%w: record %d (index %d)", ErrChainBroken, i, rec.Index)
		}
		prev = rec.Hash
	}
	return nil
}

// RestoreLedger reloads a previously-persisted gating log into a
// freshly constructed Gate so decisions logged in an earlier process
// continue the SAME hash chain rather than starting a new one. The
// full chain is independently re-verified (same recomputation as
// VerifyChain) before it is adopted.
func (g *Gate) RestoreLedger(records []Record) error {
	prev := ""
	for i, rec := range records {
		if rec.Index != uint64(i)+1 {
			return fmt.Errorf("policy: restore: index gap at position %d (index=%d)", i, rec.Index)
		}
		recomputed := (Record{
			Index: rec.Index, Action: rec.Action, Subject: rec.Subject,
			Verdict: rec.Verdict, RuleName: rec.RuleName, Reason: rec.Reason, PrevHash: prev,
		}).computeHash()
		if recomputed != rec.Hash {
			return fmt.Errorf("policy: restore: hash mismatch at index %d", rec.Index)
		}
		prev = rec.Hash
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.log = append([]Record{}, records...)
	return nil
}

// --- Standard rule constructors --------------------------------------
//
// These are convenience constructors turning trust/decision/ontology
// outputs into Gate Rules, so callers don't hand-roll predicate
// closures for the common cases.

// RuleMinTrustScore denies any action whose TrustScore is present and
// below min.
func RuleMinTrustScore(name string, min float64) Rule {
	return Rule{Name: name, Eval: func(in Input) (Verdict, string) {
		if in.TrustScore == nil {
			return "", ""
		}
		if *in.TrustScore < min {
			return Deny, fmt.Sprintf("trust score %.3f below minimum %.3f", *in.TrustScore, min)
		}
		return "", ""
	}}
}

// RuleEscalatedRequiresApproval routes any decision.Engine ESCALATE
// action to RequireApproval instead of a hard allow/deny, matching the
// human-in-the-loop posture the audit's decision package already models.
func RuleEscalatedRequiresApproval(name string) Rule {
	return Rule{Name: name, Eval: func(in Input) (Verdict, string) {
		if in.RiskAction == "ESCALATE" {
			return RequireApproval, "risk decision action is ESCALATE"
		}
		return "", ""
	}}
}

// RuleOntologyMustBeValid denies any action whose ontology validation
// result is present and false.
func RuleOntologyMustBeValid(name string) Rule {
	return Rule{Name: name, Eval: func(in Input) (Verdict, string) {
		if in.OntologyOK == nil {
			return "", ""
		}
		if !*in.OntologyOK {
			return Deny, "ontology validation failed for acting subject/instance"
		}
		return "", ""
	}}
}

// RuleAllowKnownActions allows actions in the given allow-list once
// every prior deny/approval rule has passed — a typical terminal
// "default allow for known-good actions" rule.
func RuleAllowKnownActions(name string, actions ...string) Rule {
	set := make(map[string]bool, len(actions))
	for _, a := range actions {
		set[a] = true
	}
	return Rule{Name: name, Eval: func(in Input) (Verdict, string) {
		if set[in.Action] {
			return Allow, fmt.Sprintf("action %q is in the known-actions allow-list", in.Action)
		}
		return "", ""
	}}
}

// SortedActionsSeen returns the distinct Action values that have been
// evaluated, sorted — a small convenience for reporting/dashboards.
func (g *Gate) SortedActionsSeen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	seen := map[string]bool{}
	for _, r := range g.log {
		seen[r.Action] = true
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
