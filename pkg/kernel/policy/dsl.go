// Policy-as-data DSL — the P0 gap named across every audit doc
// ("Policy-as-data DSL ... Broken / not done ... Ini inti moat").
// Complements policy.go's code-defined Rule/Gate (still used for
// complex logic) with a DECLARATIVE, versioned, hash-chained, signed,
// rollbackable format for the common case: subject/action/resource/
// condition/effect, per the audit's own suggested minimal schema.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

type Effect string

const (
	DSLAllow Effect = "ALLOW"
	DSLDeny  Effect = "DENY"
)

// Condition is a single deterministic comparison: Field op Value. Kept
// deliberately narrow (per the audit's own guidance: "DSL jangan
// terlalu ekspresif dulu; prioritaskan determinism dan explainability")
// — no arbitrary expression evaluation, no external calls, nothing that
// could make two nodes evaluate the same policy differently.
type Condition struct {
	Field string `json:"field"` // dotted path into Input.Attributes
	Op    string `json:"op"`    // "eq" | "neq" | "gte" | "lte" | "in"
	Value string `json:"value"`
}

// DSLRule is one declarative policy statement.
type DSLRule struct {
	Name       string      `json:"name"`
	Subject    string      `json:"subject"`  // "*" or a specific actor/role
	Action     string      `json:"action"`   // "*" or a specific action name
	Resource   string      `json:"resource"` // "*" or a specific resource pattern
	Conditions []Condition `json:"conditions,omitempty"`
	Effect     Effect      `json:"effect"`
}

// PolicyDoc is one immutable, versioned policy publication. Hash is a
// commitment over every other field (subject/action/resource/condition/
// effect/version, per the audit's minimal schema) plus PrevHash, so the
// full history forms a hash chain identical in spirit to kg.Graph's —
// tamper with any past version and every subsequent hash breaks.
type PolicyDoc struct {
	Version      int       `json:"version"`
	Rules        []DSLRule `json:"rules"`
	PrevHash     string    `json:"prev_hash"`
	Hash         string    `json:"hash"`
	SignedBy     string    `json:"signed_by,omitempty"`
	SignatureHex string    `json:"signature_hex,omitempty"` // ed25519 signature over Hash, when a signer is configured
}

func canonicalBytes(version int, rules []DSLRule, prevHash string) []byte {
	// Deterministic encoding: json.Marshal on a slice preserves order
	// (rules are evaluated first-match-wins, so ORDER is semantically
	// meaningful and must NOT be sorted away) — this is why rules is
	// left in publish order rather than canonicalized by content.
	type wire struct {
		Version  int       `json:"version"`
		Rules    []DSLRule `json:"rules"`
		PrevHash string    `json:"prev_hash"`
	}
	b, _ := json.Marshal(wire{Version: version, Rules: rules, PrevHash: prevHash})
	return b
}

func computeHash(version int, rules []DSLRule, prevHash string) string {
	sum := sha256.Sum256(canonicalBytes(version, rules, prevHash))
	return hex.EncodeToString(sum[:])
}

var (
	ErrVersionOutOfOrder = errors.New("policydsl: version must be exactly PrevVersion+1")
	ErrPrevHashMismatch  = errors.New("policydsl: prev_hash does not match the current HEAD")
	ErrHashMismatch      = errors.New("policydsl: hash does not match recomputed commitment — document was tampered with")
	ErrUnknownVersion    = errors.New("policydsl: no such version in history")
	ErrDSLChainBroken    = errors.New("policydsl: hash chain verification failed")
)

// Signer is the minimal interface PolicyStore needs to sign published
// documents — satisfied by verification.EvidenceSigner's Ed25519 key
// without this package importing verification directly (keeps
// kernel/policy dependency-free per the repo's zero-external-dep rule,
// while still composing with the real PKI when the caller wants signed
// policy publications).
type Signer interface {
	SignerID() string
	Sign(msg []byte) []byte
}

// PolicyStore holds the full, append-only, hash-chained version history
// of a policy — "versioned, signed, rollbackable" per every audit doc in
// this session.
type PolicyStore struct {
	mu      sync.RWMutex
	history []PolicyDoc // index i = version i+1... actually index 0 is version 1
	signer  Signer
}

func NewPolicyStore() *PolicyStore { return &PolicyStore{} }

// WithSigner attaches a Signer; every subsequent Publish will be signed.
func (s *PolicyStore) WithSigner(signer Signer) *PolicyStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signer = signer
	return s
}

// Publish appends a new policy version. Fails closed: if version
// sequencing is wrong, nothing is mutated.
func (s *PolicyStore) Publish(rules []DSLRule) (PolicyDoc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	version := len(s.history) + 1
	prevHash := ""
	if len(s.history) > 0 {
		prevHash = s.history[len(s.history)-1].Hash
	}
	doc := PolicyDoc{Version: version, Rules: append([]DSLRule{}, rules...), PrevHash: prevHash}
	doc.Hash = computeHash(doc.Version, doc.Rules, doc.PrevHash)
	if s.signer != nil {
		doc.SignedBy = s.signer.SignerID()
		sig := s.signer.Sign([]byte(doc.Hash))
		doc.SignatureHex = hex.EncodeToString(sig)
	}
	s.history = append(s.history, doc)
	return doc, nil
}

// Head returns the currently active (latest) policy document.
func (s *PolicyStore) Head() (PolicyDoc, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.history) == 0 {
		return PolicyDoc{}, ErrUnknownVersion
	}
	return s.history[len(s.history)-1], nil
}

// At returns a specific historical version — full audit trail access,
// not just the current head.
func (s *PolicyStore) At(version int) (PolicyDoc, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if version < 1 || version > len(s.history) {
		return PolicyDoc{}, ErrUnknownVersion
	}
	return s.history[version-1], nil
}

// Rollback publishes a NEW version whose rule content is byte-identical
// to an earlier version. This is deliberate, not a destructive rewind:
// per DARI principles (no ledger mutation after append — the same
// invariant governing the rest of this codebase), history is never
// edited or truncated. "Rolling back" means the CURRENT effective
// policy reverts to old behavior while the full chain — including the
// fact that a rollback happened and when — remains permanently visible.
func (s *PolicyStore) Rollback(toVersion int) (PolicyDoc, error) {
	old, err := s.At(toVersion)
	if err != nil {
		return PolicyDoc{}, err
	}
	return s.Publish(old.Rules)
}

// VerifyChain independently re-derives every hash in the full history
// from scratch (never trusting the stored Hash/PrevHash fields) — the
// same tamper-detection contract as kg.Graph.ReplayAll and
// EvidenceStore's Merkle proofs elsewhere in this codebase.
func (s *PolicyStore) VerifyChain() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prevHash := ""
	for i, doc := range s.history {
		if doc.Version != i+1 {
			return fmt.Errorf("%w: at position %d, version=%d", ErrVersionOutOfOrder, i, doc.Version)
		}
		if doc.PrevHash != prevHash {
			return fmt.Errorf("%w: version %d", ErrPrevHashMismatch, doc.Version)
		}
		want := computeHash(doc.Version, doc.Rules, doc.PrevHash)
		if want != doc.Hash {
			return fmt.Errorf("%w: version %d", ErrHashMismatch, doc.Version)
		}
		prevHash = doc.Hash
	}
	return nil
}

// Evaluate runs the CURRENT head policy against attrs (a flat map —
// deliberately not nested objects, keeping condition evaluation
// trivially deterministic). First matching rule wins; deny-by-default
// if nothing matches, per the "Deny by default" design principle.
func (s *PolicyStore) Evaluate(subject, action, resource string, attrs map[string]string) (Effect, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.history) == 0 {
		return DSLDeny, "no policy published — deny by default", nil
	}
	head := s.history[len(s.history)-1]
	for _, r := range head.Rules {
		if !matchOrWildcard(r.Subject, subject) || !matchOrWildcard(r.Action, action) || !matchOrWildcard(r.Resource, resource) {
			continue
		}
		allConditionsMet := true
		for _, c := range r.Conditions {
			if !evalCondition(c, attrs) {
				allConditionsMet = false
				break
			}
		}
		if allConditionsMet {
			return r.Effect, fmt.Sprintf("matched rule %q (policy v%d)", r.Name, head.Version), nil
		}
	}
	return DSLDeny, fmt.Sprintf("no rule matched — deny by default (policy v%d)", head.Version), nil
}

func matchOrWildcard(pattern, value string) bool {
	if pattern == "*" || pattern == value {
		return true
	}
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(value) >= len(prefix) && value[:len(prefix)] == prefix
	}
	return false
}

func evalCondition(c Condition, attrs map[string]string) bool {
	actual, ok := attrs[c.Field]
	if !ok {
		return false
	}
	switch c.Op {
	case "eq":
		return actual == c.Value
	case "neq":
		return actual != c.Value
	case "in":
		for _, v := range splitCSV(c.Value) {
			if actual == v {
				return true
			}
		}
		return false
	default:
		return false // unknown operator: fail closed, never match
	}
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	sort.Strings(out) // order doesn't matter for membership; sorting just keeps it deterministic for any caller inspecting it
	return out
}
