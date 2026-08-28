// Package authz closes PHASE 14 and PHASE 15 of the v7.10.3
// production-readiness audit: policy-as-DATA (not Go closures) and a
// real RBAC + ABAC + ReBAC authorization model.
//
// Why the two phases are one package: they are the same decision. An
// authorization answer that cannot name the policy VERSION that
// produced it is not auditable, and a policy engine that cannot
// express relationships ("the analyst assigned to this case") forces
// every real rule back into code. Splitting them would guarantee they
// drift.
//
// The three models are complementary, not alternatives:
//
//	RBAC  — actor has role R, role R grants permission P.
//	        Coarse, cheap, and what most audits actually ask for.
//	ABAC  — the request's ATTRIBUTES must satisfy conditions
//	        (classification, jurisdiction, tenant, time window).
//	ReBAC — the actor must stand in a RELATIONSHIP to the specific
//	        resource (owner, assignee, member-of-org-that-owns-it),
//	        including transitively. This is what RBAC cannot express
//	        and what every "why could Bob read Alice's case?" incident
//	        is actually about.
//
// Decision procedure, fixed and deterministic:
//
//  1. Evaluate every matching rule in the active policy version.
//  2. DENY wins over ALLOW, always, regardless of specificity or order.
//  3. No matching ALLOW  ->  deny (default-deny, never default-allow).
//  4. Emit an Explanation naming every rule considered and why it
//     matched or did not.
//
// Rule 2 is deliberate: "most specific wins" schemes are how
// production authorization bugs happen, because specificity is a
// judgement call and DENY is not.
package authz

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/telemetry"
)

// Effect is a rule outcome.
type Effect string

const (
	Allow Effect = "ALLOW"
	Deny  Effect = "DENY"
)

// Errors.
var (
	ErrNoPolicy           = errors.New("authz: no policy version is active")
	ErrUnknownVersion     = errors.New("authz: unknown policy version")
	ErrPolicyInvalid      = errors.New("authz: policy document failed validation")
	ErrPolicyExpired      = errors.New("authz: policy version is past its expiry tick")
	ErrPolicyNotYetActive = errors.New("authz: policy version has not reached its activation tick")
	ErrApprovalRequired   = errors.New("authz: policy version requires approval before activation")
	ErrChainBroken        = errors.New("authz: policy version chain is broken")
	ErrSignatureInvalid   = errors.New("authz: policy signature does not verify")
	ErrRelationCycle      = errors.New("authz: relationship graph depth limit exceeded")
)

// Condition is one ABAC attribute predicate.
type Condition struct {
	Attribute string   `json:"attribute"`
	Operator  string   `json:"operator"` // eq, neq, in, not_in, gte, lte, prefix, exists
	Values    []string `json:"values"`
}

// Rule is one policy rule, expressed as data.
type Rule struct {
	ID     string `json:"id"`
	Effect Effect `json:"effect"`
	// Roles, Actions, Resources support "*" and "prefix*" wildcards.
	Roles     []string `json:"roles,omitempty"`
	Actions   []string `json:"actions"`
	Resources []string `json:"resources"`
	// Conditions are ABAC predicates, ANDed.
	Conditions []Condition `json:"conditions,omitempty"`
	// Relation, when set, requires the actor to stand in this ReBAC
	// relation to the resource (directly or transitively).
	Relation string `json:"relation,omitempty"`
	// Purposes, when set, requires the request to declare one of these
	// purposes (VTECP-001 Capability 3: "Purpose Binding" -- data-use
	// limitation, not just who/under-what-conditions/in-what-relation
	// but for-what-declared-reason). Supports the same "*"/"prefix*"
	// wildcards as Roles/Actions/Resources. An empty Purposes is
	// purpose-agnostic and matches any request, including one with no
	// declared purpose -- this is additive: a Document written before
	// this field existed evaluates identically.
	Purposes []string `json:"purposes,omitempty"`
	// Obligations are side conditions the caller MUST honour when the
	// rule allows (e.g. "redact_pii", "log_to_audit").
	Obligations []string `json:"obligations,omitempty"`
	// Priority orders rule evaluation within one effect class: a higher
	// Priority rule is considered before a lower one when multiple
	// ALLOW rules could match the same request, deciding which rule's
	// ID/Obligations get attributed in the Explanation. Priority is
	// data, not a safety override: it cannot make a DENY lose to an
	// ALLOW, because Can() still returns the instant ANY matching DENY
	// is found, at whatever position it falls in the (now
	// priority-ordered) evaluation sequence -- see
	// TestPriorityCannotOverrideDenyWins. Ties (equal Priority, the
	// zero-value default) fall back to the pre-existing deterministic
	// ID-order tie-break, so this field is purely additive: a document
	// with every rule at Priority 0 evaluates identically to before
	// this field existed.
	Priority int `json:"priority,omitempty"`
}

// Document is a versioned, hashable, signable policy artifact.
type Document struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
	// RequiresApproval marks a version that cannot be activated until
	// an approver signs off — the audit's approval workflow.
	RequiresApproval bool   `json:"requires_approval"`
	ApprovedBy       string `json:"approved_by,omitempty"`
	ExpiresAtTick    uint64 `json:"expires_at_tick"` // 0 = no expiry
	// ActivatesAtTick is the effective-from tick: a request evaluated
	// before it is default-denied exactly like a request evaluated
	// after ExpiresAtTick. 0 = active immediately (the default,
	// matching every document written before this field existed).
	ActivatesAtTick uint64 `json:"activates_at_tick"`
	PrevHash        string `json:"prev_hash"`
	Hash            string `json:"hash"`
	Signature       string `json:"signature,omitempty"`
	SigningKeyID    string `json:"signing_key_id,omitempty"`
	PublicKey       string `json:"public_key,omitempty"`
}

// canonicalBytes is the deterministic serialization for hash+signature.
func (d Document) canonicalBytes() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.policy/v1\nid=%s\nversion=%d\nprev=%s\napproval=%v\napprover=%s\nexpires=%d\nactivates=%d\n",
		d.ID, d.Version, d.PrevHash, d.RequiresApproval, d.ApprovedBy, d.ExpiresAtTick, d.ActivatesAtTick)
	rules := append([]Rule(nil), d.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	for _, r := range rules {
		fmt.Fprintf(&b, "rule=%s|effect=%s|priority=%d|roles=%s|actions=%s|resources=%s|relation=%s|obligations=%s|purposes=%s",
			r.ID, r.Effect, r.Priority, join(r.Roles), join(r.Actions), join(r.Resources), r.Relation, join(r.Obligations), join(r.Purposes))
		conds := append([]Condition(nil), r.Conditions...)
		sort.Slice(conds, func(i, j int) bool {
			if conds[i].Attribute != conds[j].Attribute {
				return conds[i].Attribute < conds[j].Attribute
			}
			return conds[i].Operator < conds[j].Operator
		})
		for _, c := range conds {
			fmt.Fprintf(&b, "|cond=%s %s %s", c.Attribute, c.Operator, join(c.Values))
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func join(s []string) string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return strings.Join(c, ",")
}

// Validate rejects structurally broken policy data.
func (d Document) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("%w: empty policy ID", ErrPolicyInvalid)
	}
	seen := map[string]bool{}
	for _, r := range d.Rules {
		switch {
		case strings.TrimSpace(r.ID) == "":
			return fmt.Errorf("%w: a rule has no ID", ErrPolicyInvalid)
		case seen[r.ID]:
			return fmt.Errorf("%w: duplicate rule ID %s", ErrPolicyInvalid, r.ID)
		case r.Effect != Allow && r.Effect != Deny:
			return fmt.Errorf("%w: rule %s has effect %q", ErrPolicyInvalid, r.ID, r.Effect)
		case len(r.Actions) == 0 || len(r.Resources) == 0:
			return fmt.Errorf("%w: rule %s must name actions and resources", ErrPolicyInvalid, r.ID)
		}
		for _, c := range r.Conditions {
			if !validOperator(c.Operator) {
				return fmt.Errorf("%w: rule %s has unknown operator %q", ErrPolicyInvalid, r.ID, c.Operator)
			}
		}
		seen[r.ID] = true
	}
	return nil
}

func validOperator(op string) bool {
	switch op {
	case "eq", "neq", "in", "not_in", "gte", "lte", "prefix", "exists":
		return true
	}
	return false
}

// ParseDocument loads a policy from JSON bytes — policy as DATA,
// loadable from a file, a config map or an API call, never compiled in.
func ParseDocument(raw []byte) (Document, error) {
	var d Document
	if err := json.Unmarshal(raw, &d); err != nil {
		return Document{}, fmt.Errorf("%w: %v", ErrPolicyInvalid, err)
	}
	if err := d.Validate(); err != nil {
		return Document{}, err
	}
	return d, nil
}

// Request is one authorization question.
type Request struct {
	Actor      string
	Roles      []string
	Action     string
	Resource   string
	Attributes map[string]string
	Tick       uint64
	// Tenant scopes this request for PolicyDecision recordkeeping (§ below).
	// It is not itself evaluated by matches() -- a rule that needs to
	// gate on tenant does so via an ABAC Condition on Attributes["tenant_id"]
	// exactly as before this field existed; Tenant only travels through
	// to the audit-facing PolicyDecision so a decision record is
	// self-describing without the caller having to duplicate the value
	// into Attributes.
	Tenant string
	// Purpose is the declared reason this request is being made (e.g.
	// "claims_investigation", "regulatory_audit", "case_resolution") --
	// matched against a rule's Purposes. Empty means no purpose was
	// declared; a rule with a non-empty Purposes will then fail to
	// match (see matches()), same as an empty Roles/Attributes would
	// fail an RBAC/ABAC rule.
	Purpose string
}

// Explanation is the auditable answer.
type Explanation struct {
	Allowed        bool     `json:"allowed"`
	PolicyID       string   `json:"policy_id"`
	PolicyVersion  int      `json:"policy_version"`
	PolicyHash     string   `json:"policy_hash"`
	MatchedRule    string   `json:"matched_rule,omitempty"`
	DecidingEffect Effect   `json:"deciding_effect"`
	Obligations    []string `json:"obligations,omitempty"`
	Considered     []string `json:"considered"`
	Reason         string   `json:"reason"`
}

// Relationship is one ReBAC tuple: subject --relation--> object.
type Relationship struct {
	Subject  string
	Relation string
	Object   string
}

// Engine holds policy versions and the relationship graph.
type Engine struct {
	mu       sync.RWMutex
	versions []Document
	active   int // index into versions; -1 when none
	rels     map[string][]Relationship
	// MaxRelationDepth bounds transitive relationship traversal.
	MaxRelationDepth int
	// AuditStore optionally mirrors every CanRecorded decision into a
	// canonical, hash-chained audit ledger -- opt-in, exactly like
	// ontology.Registry.AuditStore: nil performs no mirroring.
	AuditStore *audit.AuditStore
}

// NewEngine constructs an empty authorization engine.
func NewEngine() *Engine {
	return &Engine{active: -1, rels: map[string][]Relationship{}, MaxRelationDepth: 8}
}

// AttachAuditStore opts this engine into audit mirroring for every
// CanRecorded call made afterwards.
func (e *Engine) AttachAuditStore(store *audit.AuditStore) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.AuditStore = store
}

// Publish appends a new policy version, chained to its predecessor.
// It does NOT activate it — activation is a separate, approvable act
// (hot reload with a dry-run window).
func (e *Engine) Publish(d Document) (Document, error) {
	_, span := telemetry.StartSpan(context.Background(), "authz.Publish")
	defer span.End()

	if err := d.Validate(); err != nil {
		return Document{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	d.Version = len(e.versions) + 1
	if len(e.versions) > 0 {
		d.PrevHash = e.versions[len(e.versions)-1].Hash
	}
	sum := sha256.Sum256(d.canonicalBytes())
	d.Hash = hex.EncodeToString(sum[:])
	e.versions = append(e.versions, d)
	return d, nil
}

// Sign attaches a signature to a published version.
func (e *Engine) Sign(version int, priv ed25519.PrivateKey, keyID string) (Document, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if version < 1 || version > len(e.versions) {
		return Document{}, fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}
	d := &e.versions[version-1]
	sig := ed25519.Sign(priv, d.canonicalBytes())
	d.Signature = hex.EncodeToString(sig)
	d.SigningKeyID = keyID
	d.PublicKey = hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	return *d, nil
}

// VerifySignature checks a version's signature if one is present.
func (e *Engine) VerifySignature(version int) error {
	d, err := e.At(version)
	if err != nil {
		return err
	}
	if d.Signature == "" {
		return nil
	}
	sig, err1 := hex.DecodeString(d.Signature)
	pub, err2 := hex.DecodeString(d.PublicKey)
	if err1 != nil || err2 != nil || len(pub) != ed25519.PublicKeySize {
		return ErrSignatureInvalid
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), d.canonicalBytes(), sig) {
		return ErrSignatureInvalid
	}
	return nil
}

// Approve records sign-off on a version that requires it.
func (e *Engine) Approve(version int, approver string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if version < 1 || version > len(e.versions) {
		return fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}
	if strings.TrimSpace(approver) == "" {
		return ErrApprovalRequired
	}
	d := &e.versions[version-1]
	d.ApprovedBy = approver
	sum := sha256.Sum256(d.canonicalBytes())
	d.Hash = hex.EncodeToString(sum[:])
	return nil
}

// Activate hot-reloads a version as the live policy.
func (e *Engine) Activate(version int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if version < 1 || version > len(e.versions) {
		return fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}
	d := e.versions[version-1]
	if d.RequiresApproval && d.ApprovedBy == "" {
		return fmt.Errorf("%w: version %d", ErrApprovalRequired, version)
	}
	e.active = version - 1
	return nil
}

// Rollback re-activates an earlier version. Rolling back is an
// activation, not a deletion: the newer version stays in the chain and
// stays auditable.
func (e *Engine) Rollback(toVersion int) error { return e.Activate(toVersion) }

// At returns a specific version.
func (e *Engine) At(version int) (Document, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if version < 1 || version > len(e.versions) {
		return Document{}, fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}
	return e.versions[version-1], nil
}

// Active returns the live policy version.
func (e *Engine) Active() (Document, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.active < 0 {
		return Document{}, ErrNoPolicy
	}
	return e.versions[e.active], nil
}

// VerifyChain re-derives every version hash and its linkage.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for i, d := range e.versions {
		if d.PrevHash != prev {
			return fmt.Errorf("%w at version %d", ErrChainBroken, i+1)
		}
		sum := sha256.Sum256(d.canonicalBytes())
		if hex.EncodeToString(sum[:]) != d.Hash {
			return fmt.Errorf("%w: version %d hash mismatch", ErrChainBroken, i+1)
		}
		prev = d.Hash
	}
	return nil
}

// Relate records a ReBAC tuple.
func (e *Engine) Relate(subject, relation, object string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rels[subject] = append(e.rels[subject], Relationship{subject, relation, object})
}

// hasRelation walks the relationship graph transitively, bounded.
func (e *Engine) hasRelation(subject, relation, object string) bool {
	type node struct {
		id    string
		depth int
	}
	seen := map[string]bool{subject: true}
	queue := []node{{subject, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= e.MaxRelationDepth {
			continue
		}
		rels := append([]Relationship(nil), e.rels[cur.id]...)
		sort.Slice(rels, func(i, j int) bool {
			if rels[i].Relation != rels[j].Relation {
				return rels[i].Relation < rels[j].Relation
			}
			return rels[i].Object < rels[j].Object
		})
		for _, r := range rels {
			if r.Object == object && (relation == "" || r.Relation == relation) {
				return true
			}
			if !seen[r.Object] {
				seen[r.Object] = true
				queue = append(queue, node{r.Object, cur.depth + 1})
			}
		}
	}
	return false
}

// Can is the authorization decision. Deterministic, default-deny,
// DENY-wins, always explained.
func (e *Engine) Can(req Request) (Explanation, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.active < 0 {
		return Explanation{Allowed: false, Reason: "no active policy: default deny"}, ErrNoPolicy
	}
	d := e.versions[e.active]
	ex := Explanation{PolicyID: d.ID, PolicyVersion: d.Version, PolicyHash: d.Hash, DecidingEffect: Deny}
	if d.ExpiresAtTick != 0 && req.Tick > d.ExpiresAtTick {
		ex.Reason = fmt.Sprintf("policy version %d expired at tick %d: default deny", d.Version, d.ExpiresAtTick)
		return ex, ErrPolicyExpired
	}
	if d.ActivatesAtTick != 0 && req.Tick < d.ActivatesAtTick {
		ex.Reason = fmt.Sprintf("policy version %d does not activate until tick %d: default deny", d.Version, d.ActivatesAtTick)
		return ex, ErrPolicyNotYetActive
	}

	rules := append([]Rule(nil), d.Rules...)
	// Priority orders evaluation (higher first); equal priority (the
	// zero-value default for every rule written before this field
	// existed) falls back to the original ID-order tie-break. This
	// changes only WHICH allow rule gets attributed when several would
	// match -- see Rule.Priority's doc comment for why it cannot affect
	// the deny-wins outcome itself.
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		return rules[i].ID < rules[j].ID
	})

	var allowRule *Rule
	for i := range rules {
		r := rules[i]
		why, matched := e.matches(r, req)
		ex.Considered = append(ex.Considered, fmt.Sprintf("%s(%s): %s", r.ID, r.Effect, why))
		if !matched {
			continue
		}
		if r.Effect == Deny {
			// DENY wins immediately and unconditionally.
			ex.Allowed, ex.MatchedRule, ex.DecidingEffect = false, r.ID, Deny
			ex.Reason = fmt.Sprintf("rule %s denies: %s", r.ID, why)
			return ex, nil
		}
		if allowRule == nil {
			cp := r
			allowRule = &cp
		}
	}
	if allowRule != nil {
		ex.Allowed, ex.MatchedRule, ex.DecidingEffect = true, allowRule.ID, Allow
		ex.Obligations = append([]string(nil), allowRule.Obligations...)
		ex.Reason = fmt.Sprintf("rule %s allows and no rule denies", allowRule.ID)
		return ex, nil
	}
	ex.Reason = "no rule matched: default deny"
	return ex, nil
}

// Reason codes for PolicyDecision.ReasonCodes -- a small, stable
// vocabulary a downstream consumer can match on directly, never free
// text (Explanation.Reason remains the human-readable prose).
const (
	ReasonNoActivePolicy     = "NO_ACTIVE_POLICY"
	ReasonPolicyExpired      = "POLICY_EXPIRED"
	ReasonPolicyNotYetActive = "POLICY_NOT_YET_ACTIVE"
	ReasonRuleDeny           = "RULE_DENY"
	ReasonRuleAllow          = "RULE_ALLOW"
	ReasonDefaultDeny        = "DEFAULT_DENY"
)

// PolicyDecision is the canonical, hashable, auditable record of one
// authorization evaluation (VTECP-001 Capability 3) -- distinct from
// Explanation, which is the caller-facing return value. PolicyDecision
// is what gets mirrored into the audit ledger and what the Unified
// Decision Graph (Capability 3 + Capability 4's convergence point)
// links a Case/Evidence/Fact object to.
//
// DecisionID and PolicyInputsHash are both derived deterministically
// from the request + evaluation tick via jcs.Hash -- never a random
// UUID (VTECP-001 LAW-03: determinism). The consequence is intentional
// and matches this codebase's established idempotency discipline
// (see pkg/insurance/casestate's idempotencyPrelude): evaluating the
// identical request against the identical active policy version at
// the identical tick produces the identical DecisionID, because it IS
// the identical decision.
type PolicyDecision struct {
	DecisionID       string   `json:"decision_id"`
	Tenant           string   `json:"tenant,omitempty"`
	Actor            string   `json:"actor"`
	Action           string   `json:"action"`
	Resource         string   `json:"resource"`
	Purpose          string   `json:"purpose,omitempty"`
	PolicyID         string   `json:"policy_id,omitempty"`
	PolicyVersion    int      `json:"policy_version,omitempty"`
	PolicyHash       string   `json:"policy_hash,omitempty"`
	PolicyInputsHash string   `json:"policy_inputs_hash"`
	Allowed          bool     `json:"allowed"`
	DecidingEffect   Effect   `json:"deciding_effect,omitempty"`
	MatchedRule      string   `json:"matched_rule,omitempty"`
	ReasonCodes      []string `json:"reason_codes"`
	Obligations      []string `json:"obligations,omitempty"`
	EvaluatedAtTick  uint64   `json:"evaluated_at_tick"`
	DecisionHash     string   `json:"decision_hash"`
}

// policyInputs is the canonicalized subset of Request that identifies
// "what was actually asked" -- deliberately excludes nothing from
// Request except nothing (every field that can affect the decision is
// here), so PolicyInputsHash is a genuine commitment to the inputs.
type policyInputs struct {
	Actor      string            `json:"actor"`
	Roles      []string          `json:"roles,omitempty"`
	Action     string            `json:"action"`
	Resource   string            `json:"resource"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Tick       uint64            `json:"tick"`
	Tenant     string            `json:"tenant,omitempty"`
	Purpose    string            `json:"purpose,omitempty"`
	// ActivePolicyVersion disambiguates: the same request inputs
	// evaluated against two different active policy versions (e.g.
	// after a rollback) are different decisions and must hash
	// differently even though the Request itself is byte-identical.
	ActivePolicyVersion int `json:"active_policy_version"`
}

// reasonCodeFor classifies an Explanation into PolicyDecision's stable
// vocabulary. Kept separate from Can() itself so Can()'s own control
// flow is not complicated by a concern only CanRecorded needs.
func reasonCodeFor(ex Explanation, err error) string {
	switch {
	case errors.Is(err, ErrNoPolicy):
		return ReasonNoActivePolicy
	case errors.Is(err, ErrPolicyExpired):
		return ReasonPolicyExpired
	case errors.Is(err, ErrPolicyNotYetActive):
		return ReasonPolicyNotYetActive
	case ex.Allowed:
		return ReasonRuleAllow
	case ex.MatchedRule != "" && ex.DecidingEffect == Deny:
		return ReasonRuleDeny
	default:
		return ReasonDefaultDeny
	}
}

// CanRecorded is Can(), plus the canonical PolicyDecision record VTECP-
// 001 Capability 3 requires and (when AttachAuditStore was called)
// mirroring that decision into the audit ledger -- including refused
// evaluations (no active policy, expired, not yet active), because an
// authorization audit trail that only records grants is not an audit
// trail. Can() itself is untouched and remains the lighter-weight call
// for sites that do not need a recorded decision.
func (e *Engine) CanRecorded(req Request) (Explanation, PolicyDecision, error) {
	ex, err := e.Can(req)

	activeVersion := 0
	if d, aerr := e.Active(); aerr == nil {
		activeVersion = d.Version
	}
	inputsHash := jcs.MustHash(policyInputs{
		Actor: req.Actor, Roles: req.Roles, Action: req.Action, Resource: req.Resource,
		Attributes: req.Attributes, Tick: req.Tick, Tenant: req.Tenant, Purpose: req.Purpose,
		ActivePolicyVersion: activeVersion,
	})

	dec := PolicyDecision{
		DecisionID:       "decision:" + inputsHash,
		Tenant:           req.Tenant,
		Actor:            req.Actor,
		Action:           req.Action,
		Resource:         req.Resource,
		Purpose:          req.Purpose,
		PolicyID:         ex.PolicyID,
		PolicyVersion:    ex.PolicyVersion,
		PolicyHash:       ex.PolicyHash,
		PolicyInputsHash: inputsHash,
		Allowed:          ex.Allowed,
		DecidingEffect:   ex.DecidingEffect,
		MatchedRule:      ex.MatchedRule,
		ReasonCodes:      []string{reasonCodeFor(ex, err)},
		Obligations:      ex.Obligations,
		EvaluatedAtTick:  req.Tick,
	}
	dec.DecisionHash = computePolicyDecisionHash(dec)

	e.mu.RLock()
	store := e.AuditStore
	e.mu.RUnlock()
	if store != nil {
		payload, mErr := json.Marshal(dec)
		if mErr != nil {
			return ex, dec, fmt.Errorf("authz: encoding policy decision for audit: %w", mErr)
		}
		if _, aErr := store.Append("AUTHZ:"+req.Actor, "PolicyDecision", string(payload)); aErr != nil {
			return ex, dec, fmt.Errorf("authz: audit ledger append failed: %w", aErr)
		}
	}
	return ex, dec, err
}

// computePolicyDecisionHash hashes dec with its own DecisionHash field
// cleared first, mirroring pkg/evidence/manifest's computeManifestHash
// discipline for the same reason: a self-referential hash must be
// computed over "everything except itself."
func computePolicyDecisionHash(dec PolicyDecision) string {
	dec.DecisionHash = ""
	return jcs.MustHash(dec)
}

// VerifyPolicyDecisionHash re-derives dec's DecisionHash and reports
// whether it matches the recorded value -- the third-party-verifiable
// integrity check on a PolicyDecision pulled back out of the audit
// ledger, mirroring VerifyManifestHash.
func VerifyPolicyDecisionHash(dec PolicyDecision) error {
	want := computePolicyDecisionHash(dec)
	if want != dec.DecisionHash {
		return fmt.Errorf("authz: policy decision hash mismatch: recorded=%s recomputed=%s", dec.DecisionHash, want)
	}
	return nil
}

// DryRun evaluates a request against a NON-active version — the audit's
// "dry-run" requirement, so a policy change can be tested against real
// traffic shapes before it goes live.
func (e *Engine) DryRun(version int, req Request) (Explanation, error) {
	e.mu.RLock()
	saved := e.active
	if version < 1 || version > len(e.versions) {
		e.mu.RUnlock()
		return Explanation{}, fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}
	e.mu.RUnlock()

	e.mu.Lock()
	e.active = version - 1
	e.mu.Unlock()
	ex, err := e.Can(req)
	e.mu.Lock()
	e.active = saved
	e.mu.Unlock()
	return ex, err
}

// matches evaluates RBAC + ABAC + ReBAC for one rule.
func (e *Engine) matches(r Rule, req Request) (string, bool) {
	if !matchAny(r.Actions, req.Action) {
		return "action does not match", false
	}
	if !matchAny(r.Resources, req.Resource) {
		return "resource does not match", false
	}
	if len(r.Roles) > 0 {
		ok := false
		for _, role := range req.Roles {
			if matchAny(r.Roles, role) {
				ok = true
				break
			}
		}
		if !ok {
			return "actor holds none of the required roles (RBAC)", false
		}
	}
	for _, c := range r.Conditions {
		if !evalCondition(c, req.Attributes) {
			return fmt.Sprintf("attribute condition failed (ABAC): %s %s %v", c.Attribute, c.Operator, c.Values), false
		}
	}
	if r.Relation != "" {
		if !e.hasRelation(req.Actor, r.Relation, req.Resource) {
			return fmt.Sprintf("actor has no %q relationship to the resource (ReBAC)", r.Relation), false
		}
	}
	if len(r.Purposes) > 0 {
		if req.Purpose == "" || !matchAny(r.Purposes, req.Purpose) {
			return fmt.Sprintf("request purpose %q is not bound to this rule's allowed purposes (Purpose Binding)", req.Purpose), false
		}
	}
	return "all conditions satisfied", true
}

func matchAny(patterns []string, value string) bool {
	for _, p := range patterns {
		if p == "*" || p == value {
			return true
		}
		if strings.HasSuffix(p, "*") && strings.HasPrefix(value, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}

func evalCondition(c Condition, attrs map[string]string) bool {
	v, present := attrs[c.Attribute]
	switch c.Operator {
	case "exists":
		return present
	case "eq":
		return present && len(c.Values) == 1 && v == c.Values[0]
	case "neq":
		return !present || len(c.Values) != 1 || v != c.Values[0]
	case "in":
		if !present {
			return false
		}
		for _, cand := range c.Values {
			if v == cand {
				return true
			}
		}
		return false
	case "not_in":
		if !present {
			return true
		}
		for _, cand := range c.Values {
			if v == cand {
				return false
			}
		}
		return true
	case "prefix":
		return present && len(c.Values) == 1 && strings.HasPrefix(v, c.Values[0])
	case "gte", "lte":
		if !present || len(c.Values) != 1 {
			return false
		}
		if c.Operator == "gte" {
			return v >= c.Values[0]
		}
		return v <= c.Values[0]
	}
	return false
}
