// Package decision implements Veriqo's Decision Intelligence layer — the
// gap Yang_masih_kurang.docx names directly: fusion/causal can say
// "Truth, Confidence, Explanation" but not yet "Recommendation, Action,
// Escalation, Risk, Priority", which is what operational users actually
// need.
//
// This is a declarative, config-driven weighted-factor policy engine
// (the doc's own recommendation: start with a rule engine, not a
// hand-tuned model). A Policy declares named factors and their weights;
// Decide takes observed factor values (typically fusion confidences or
// causal.AggregateSupport figures) and produces a deterministic risk
// score, an action tier, and a full explanation chain — so a decision
// is never just a number, it's an auditable trace back to the evidence
// and causal findings that produced it.
package decision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

var (
	ErrUnknownPolicy     = errors.New("decision: policy not registered")
	ErrInvalidWeight     = errors.New("decision: factor weight must be > 0")
	ErrNoFactorsInPolicy = errors.New("decision: policy must declare at least one factor")
	ErrTamperDetected    = errors.New("decision: hash chain verification failed")
)

// Action is the recommended response tier, ordered by severity.
type Action string

const (
	ActionMonitor  Action = "MONITOR"
	ActionFlag     Action = "FLAG"
	ActionEscalate Action = "ESCALATE"
)

// FactorWeight declares how much one named factor contributes to a
// policy's risk score.
type FactorWeight struct {
	Name   string
	Weight float64 // > 0; weights are normalized internally, need not sum to 1
}

// Policy is a named, versionable decision configuration — the
// "knowledge artifact" the audit doc asks for instead of hard-coded
// thresholds buried in application code.
type Policy struct {
	Name              string
	Factors           []FactorWeight
	FlagThreshold     float64 // risk score >= this -> at least FLAG
	EscalateThreshold float64 // risk score >= this -> ESCALATE
}

// Decision is the caller-facing output of Decide.
type Decision struct {
	Subject     string
	PolicyName  string
	RiskScore   float64
	Action      Action
	Explanation []string
}

// DecisionRecord is one hash-chained audit log entry.
type DecisionRecord struct {
	Index      uint64
	ActorID    string
	Subject    string
	PolicyName string
	RiskScore  float64
	Action     Action
	Factors    []string // sorted "name=value" summary, canonical
	Tick       uint64
	PrevHash   string
	Hash       string
}

// Engine is the live Decision Intelligence engine. Safe for concurrent use.
type Engine struct {
	mu       sync.RWMutex
	policies map[string]Policy
	log      []DecisionRecord
	head     string
}

func NewEngine() *Engine {
	return &Engine{policies: make(map[string]Policy)}
}

// RegisterPolicy validates and stores a policy. Re-registering an
// existing name replaces it for future Decide calls only — past
// decisions in the log are never retroactively altered.
func (e *Engine) RegisterPolicy(p Policy) error {
	if len(p.Factors) == 0 {
		return ErrNoFactorsInPolicy
	}
	for _, f := range p.Factors {
		if f.Weight <= 0 {
			return ErrInvalidWeight
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies[p.Name] = p
	return nil
}

// Decide computes a weighted risk score from observed factor values
// (each expected in [0,1] — a fusion confidence, a causal.AggregateSupport
// figure, or any other normalized signal), selects an action tier per
// the policy's thresholds, and appends a hash-chained audit record.
//
// riskScore = Σ(weight_i * value_i) / Σ(weight_i)   over factors declared
// in the policy; a factor the policy declares but the caller didn't
// supply a value for is treated as 0 and called out explicitly in the
// explanation, rather than silently ignored.
func (e *Engine) Decide(actorID, subject, policyName string, values map[string]float64, tick uint64) (Decision, error) {
	_, span := telemetry.StartSpan(context.Background(), "decision.Decide",
		telemetry.Attribute{Key: "subject", Value: subject}, telemetry.Attribute{Key: "policy", Value: policyName})
	defer span.End()

	e.mu.Lock()
	defer e.mu.Unlock()

	policy, ok := e.policies[policyName]
	if !ok {
		return Decision{}, ErrUnknownPolicy
	}

	factors := make([]FactorWeight, len(policy.Factors))
	copy(factors, policy.Factors)
	sort.Slice(factors, func(i, j int) bool { return factors[i].Name < factors[j].Name }) // deterministic order

	var weightedSum, weightTotal float64
	explanation := []string{fmt.Sprintf("policy %q: %d factor(s)", policyName, len(factors))}
	summary := make([]string, 0, len(factors))
	for _, f := range factors {
		v, present := values[f.Name]
		note := ""
		if !present {
			note = " (no signal supplied, treated as 0)"
		}
		contribution := f.Weight * v
		weightedSum += contribution
		weightTotal += f.Weight
		explanation = append(explanation, fmt.Sprintf(
			"  factor=%q weight=%.3f value=%.4f contribution=%.4f%s", f.Name, f.Weight, v, contribution, note))
		summary = append(summary, fmt.Sprintf("%s=%.4f", f.Name, v))
	}
	riskScore := 0.0
	if weightTotal > 0 {
		riskScore = weightedSum / weightTotal
	}

	action := ActionMonitor
	switch {
	case riskScore >= policy.EscalateThreshold:
		action = ActionEscalate
	case riskScore >= policy.FlagThreshold:
		action = ActionFlag
	}
	explanation = append(explanation, fmt.Sprintf(
		"risk_score=%.4f -> action=%s (flag>=%.2f, escalate>=%.2f)",
		riskScore, action, policy.FlagThreshold, policy.EscalateThreshold))

	rec := DecisionRecord{
		Index: uint64(len(e.log)), ActorID: actorID, Subject: subject,
		PolicyName: policyName, RiskScore: riskScore, Action: action,
		Factors: summary, Tick: tick, PrevHash: e.head,
	}
	rec.Hash = hashDecision(rec)
	e.log = append(e.log, rec)
	e.head = rec.Hash

	return Decision{
		Subject: subject, PolicyName: policyName, RiskScore: riskScore,
		Action: action, Explanation: explanation,
	}, nil
}

func hashDecision(r DecisionRecord) string {
	raw := fmt.Sprintf("idx=%d|actor=%s|subject=%s|policy=%s|risk=%.6f|action=%s|factors=%v|tick=%d|prev=%s",
		r.Index, r.ActorID, r.Subject, r.PolicyName, r.RiskScore, r.Action, r.Factors, r.Tick, r.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerifyChain re-derives every record's hash and confirms the chain is
// unbroken.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for _, r := range e.log {
		if r.PrevHash != prev {
			return ErrTamperDetected
		}
		if hashDecision(r) != r.Hash {
			return ErrTamperDetected
		}
		prev = r.Hash
	}
	return nil
}

// ReplayAll returns a defensive copy of the decision log.
func (e *Engine) ReplayAll() []DecisionRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]DecisionRecord, len(e.log))
	copy(out, e.log)
	return out
}

// Head returns the current chain head hash ("" if no decisions yet).
func (e *Engine) Head() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.head
}
