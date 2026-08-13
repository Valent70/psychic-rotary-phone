package authz

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

// These tests close V7.11.1 P0-6 for pkg/authz. The audit was explicit
// that the target is not a coverage percentage but the untested
// branches that carry SECURITY meaning: signature verification, the
// approval gate, expiry, chain integrity, deny precedence, and the
// relationship-depth bound. Each test below exercises a path where a
// wrong answer is an authorization bypass rather than a wrong number.

func doc(version int, rules []Rule) Document {
	return Document{ID: "veriqo-authz", Version: version, Rules: rules}
}

func allowRule(id, action, resource string, roles ...string) Rule {
	return Rule{ID: id, Effect: Allow, Roles: roles,
		Actions: []string{action}, Resources: []string{resource}}
}

func publishActive(t *testing.T, e *Engine, d Document) Document {
	t.Helper()
	out, err := e.Publish(d)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := e.Activate(out.Version); err != nil {
		t.Fatalf("activate: %v", err)
	}
	return out
}

func TestNoActivePolicyDeniesRatherThanDefaultingOpen(t *testing.T) {
	e := NewEngine()
	_, err := e.Can(Request{Actor: "a", Action: "read", Resource: "case/1"})
	if !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("an engine with no policy must refuse, got %v", err)
	}
}

func TestDenyBeatsAllowRegardlessOfRuleOrder(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{
		allowRule("r-allow", "read", "case/*", "analyst"),
		{ID: "r-deny", Effect: Deny, Roles: []string{"analyst"},
			Actions: []string{"read"}, Resources: []string{"case/sealed*"}},
	}))
	ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"},
		Action: "read", Resource: "case/sealed-9"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Allowed {
		t.Fatalf("an explicit deny must win over a matching allow: %+v", ex)
	}
	if ex.MatchedRule != "r-deny" {
		t.Fatalf("the deciding rule must be named: %+v", ex)
	}
}

func TestNoMatchingRuleDeniesByDefault(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"},
		Action: "delete", Resource: "case/1"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Allowed {
		t.Fatal("an unmatched action must be denied, not permitted")
	}
	if ex.Reason == "" {
		t.Fatal("a default deny must still explain itself")
	}
}

func TestRoleIsRequiredWhenTheRuleNamesOne(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	ex, _ := e.Can(Request{Actor: "a", Roles: []string{"guest"},
		Action: "read", Resource: "case/1"})
	if ex.Allowed {
		t.Fatal("a role-scoped rule must not match an actor without the role")
	}
}

func TestExpiredPolicyIsRefusedRatherThanHonoured(t *testing.T) {
	e := NewEngine()
	d := doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")})
	d.ExpiresAtTick = 100
	publishActive(t, e, d)

	if _, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"},
		Action: "read", Resource: "case/1", Tick: 50}); err != nil {
		t.Fatalf("inside its window the policy must apply: %v", err)
	}
	_, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"},
		Action: "read", Resource: "case/1", Tick: 500})
	if !errors.Is(err, ErrPolicyExpired) {
		t.Fatalf("an expired policy must fail closed, got %v", err)
	}
}

func TestApprovalGateBlocksActivation(t *testing.T) {
	e := NewEngine()
	d := doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")})
	d.RequiresApproval = true
	published, err := e.Publish(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Activate(published.Version); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("an unapproved policy must not activate, got %v", err)
	}
	if err := e.Approve(published.Version, "compliance-officer"); err != nil {
		t.Fatal(err)
	}
	if err := e.Activate(published.Version); err != nil {
		t.Fatalf("an approved policy must activate: %v", err)
	}
}

func TestUnknownVersionOperationsAreRefused(t *testing.T) {
	e := NewEngine()
	if err := e.Activate(99); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("activate: expected unknown version, got %v", err)
	}
	if err := e.Approve(99, "x"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("approve: expected unknown version, got %v", err)
	}
	if _, err := e.At(99); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("at: expected unknown version, got %v", err)
	}
	if err := e.VerifySignature(99); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("verify: expected unknown version, got %v", err)
	}
}

func TestSignatureVerificationRejectsATamperedDocument(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	e := NewEngine()
	published, err := e.Publish(doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sign(published.Version, priv, "key-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.VerifySignature(published.Version); err != nil {
		t.Fatalf("a freshly signed document must verify: %v", err)
	}

	// Tamper with the stored rule set: the signature must stop verifying.
	e.mu.Lock()
	e.versions[0].Rules[0].Effect = Deny
	e.mu.Unlock()
	if err := e.VerifySignature(published.Version); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("a tampered signed policy must fail verification, got %v", err)
	}
}

// TestUnsignedPolicyIsNotMistakenForASignedOne pins a contract that is
// easy to misread. VerifySignature returns nil for an UNSIGNED document
// — it verifies a signature if one is present, it does not assert that
// one exists. A caller that treats "no error" as "signed by a trusted
// key" would accept an unsigned policy, so the presence check belongs at
// the call site and is asserted here so the distinction stays visible.
func TestUnsignedPolicyIsNotMistakenForASignedOne(t *testing.T) {
	e := NewEngine()
	published, _ := e.Publish(doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	if err := e.VerifySignature(published.Version); err != nil {
		t.Fatalf("an unsigned document has nothing to fail on: %v", err)
	}
	d, err := e.At(published.Version)
	if err != nil {
		t.Fatal(err)
	}
	if d.Signature != "" || d.SigningKeyID != "" {
		t.Fatal("an unsigned document must not carry signature material")
	}
}

func TestPolicyChainDetectsTampering(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	if _, err := e.Publish(doc(2, []Rule{allowRule("r2", "write", "case/*", "analyst")})); err != nil {
		t.Fatal(err)
	}
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("an untouched chain must verify: %v", err)
	}
	e.mu.Lock()
	e.versions[1].PrevHash = "0000"
	e.mu.Unlock()
	if err := e.VerifyChain(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("a broken chain must be detected, got %v", err)
	}
}

func TestInvalidDocumentIsRejectedAtPublish(t *testing.T) {
	e := NewEngine()
	cases := map[string]Document{
		"no id": {Version: 1, Rules: []Rule{allowRule("r", "read", "*", "a")}},
		"unnamed rule": {ID: "p", Version: 1, Rules: []Rule{{Effect: Allow,
			Actions: []string{"read"}, Resources: []string{"*"}}}},
		"duplicate rule id": {ID: "p", Version: 1, Rules: []Rule{
			allowRule("dup", "read", "*", "a"), allowRule("dup", "write", "*", "a")}},
		"rule without resources": {ID: "p", Version: 1, Rules: []Rule{{ID: "r",
			Effect: Allow, Actions: []string{"read"}}}},
		"bad effect": {ID: "p", Version: 1, Rules: []Rule{{ID: "r", Effect: "MAYBE", Actions: []string{"read"}, Resources: []string{"*"}}}},
		"bad operator": {ID: "p", Version: 1, Rules: []Rule{{ID: "r", Effect: Allow,
			Actions: []string{"read"}, Resources: []string{"*"},
			Conditions: []Condition{{Attribute: "a", Operator: "regex", Values: []string{"x"}}}}}},
	}
	for name, d := range cases {
		if _, err := e.Publish(d); !errors.Is(err, ErrPolicyInvalid) {
			t.Fatalf("%s must be rejected at publish, got %v", name, err)
		}
	}

	// A rule-less document is NOT invalid: it is a valid deny-everything
	// policy, which is the correct fail-closed shape for a kill switch.
	empty, err := e.Publish(Document{ID: "p", Version: 1})
	if err != nil {
		t.Fatalf("an empty policy is a legitimate deny-all: %v", err)
	}
	if err := e.Activate(empty.Version); err != nil {
		t.Fatal(err)
	}
	ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"},
		Action: "read", Resource: "case/1"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Allowed {
		t.Fatal("a policy with no rules must deny everything")
	}
}

func TestABACConditionsAreEnforced(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{{ID: "r1", Effect: Allow,
		Roles: []string{"analyst"}, Actions: []string{"read"}, Resources: []string{"case/*"},
		Conditions: []Condition{{Attribute: "tenant", Operator: "eq", Values: []string{"acme"}}}}}))

	ok, _ := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read",
		Resource: "case/1", Attributes: map[string]string{"tenant": "acme"}})
	if !ok.Allowed {
		t.Fatal("a satisfied condition must allow")
	}
	cross, _ := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read",
		Resource: "case/1", Attributes: map[string]string{"tenant": "other"}})
	if cross.Allowed {
		t.Fatal("cross-tenant access must be denied by the ABAC condition")
	}
	missing, _ := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read",
		Resource: "case/1"})
	if missing.Allowed {
		t.Fatal("a missing attribute must not satisfy a condition")
	}
}

func TestReBACRelationIsRequiredAndTransitive(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{{ID: "r1", Effect: Allow,
		Actions: []string{"read"}, Resources: []string{"case/*"}, Relation: "member"}}))

	denied, _ := e.Can(Request{Actor: "alice", Action: "read", Resource: "case/1"})
	if denied.Allowed {
		t.Fatal("without the relation the request must be denied")
	}
	e.Relate("alice", "member", "team/ops")
	e.Relate("team/ops", "member", "case/1")
	allowed, err := e.Can(Request{Actor: "alice", Action: "read", Resource: "case/1"})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Allowed {
		t.Fatalf("a transitive relation must satisfy the rule: %+v", allowed)
	}
}

func TestRelationshipCycleDoesNotHang(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{{ID: "r1", Effect: Allow,
		Actions: []string{"read"}, Resources: []string{"case/*"}, Relation: "member"}}))
	// A cycle: a -> b -> c -> a. Traversal must terminate rather than
	// recurse forever, and must not accidentally grant access.
	e.Relate("a", "member", "b")
	e.Relate("b", "member", "c")
	e.Relate("c", "member", "a")
	ex, err := e.Can(Request{Actor: "a", Action: "read", Resource: "case/1"})
	if err != nil && !errors.Is(err, ErrRelationCycle) {
		t.Fatalf("a cyclic graph must terminate cleanly, got %v", err)
	}
	if ex.Allowed {
		t.Fatal("a cycle that never reaches the resource must not grant access")
	}
}

func TestDryRunEvaluatesAnInactiveVersionWithoutActivatingIt(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	v2, err := e.Publish(doc(2, []Rule{{ID: "r2", Effect: Deny,
		Roles: []string{"analyst"}, Actions: []string{"read"}, Resources: []string{"case/*"}}}))
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Actor: "a", Roles: []string{"analyst"}, Action: "read", Resource: "case/1"}

	live, _ := e.Can(req)
	if !live.Allowed {
		t.Fatal("the active version must still allow")
	}
	dry, err := e.DryRun(v2.Version, req)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Allowed {
		t.Fatal("the candidate version must deny in the dry run")
	}
	after, _ := e.Can(req)
	if !after.Allowed {
		t.Fatal("a dry run must not change the active policy")
	}
}

func TestRollbackRestoresAPreviousVersion(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	v2, _ := e.Publish(doc(2, []Rule{{ID: "r2", Effect: Deny, Roles: []string{"analyst"},
		Actions: []string{"read"}, Resources: []string{"case/*"}}}))
	if err := e.Activate(v2.Version); err != nil {
		t.Fatal(err)
	}
	req := Request{Actor: "a", Roles: []string{"analyst"}, Action: "read", Resource: "case/1"}
	if ex, _ := e.Can(req); ex.Allowed {
		t.Fatal("version 2 must deny")
	}
	if err := e.Rollback(1); err != nil {
		t.Fatal(err)
	}
	if ex, _ := e.Can(req); !ex.Allowed {
		t.Fatal("rollback must restore the earlier behaviour")
	}
}

func TestExplanationNamesTheDecidingPolicy(t *testing.T) {
	e := NewEngine()
	published := publishActive(t, e, doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"},
		Action: "read", Resource: "case/1"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.PolicyHash != published.Hash || ex.PolicyVersion != published.Version {
		t.Fatalf("the decision must commit to the exact policy artifact: %+v", ex)
	}
	if len(ex.Considered) == 0 {
		t.Fatal("the explanation must list the rules it considered")
	}
}

func TestParseDocumentRejectsGarbage(t *testing.T) {
	if _, err := ParseDocument([]byte("{not json")); err == nil {
		t.Fatal("malformed policy JSON must be rejected")
	}
	if _, err := ParseDocument([]byte(`{"id":"","version":0}`)); err == nil {
		t.Fatal("a structurally valid but semantically empty policy must be rejected")
	}
}

// --- Audit item P1-04: Priority and ActivatesAtTick -------------------

// TestHigherPriorityAllowRuleIsAttributedOverLowerPriorityMatch proves
// Priority actually orders evaluation: two ALLOW rules both match the
// same request, and the higher-Priority one must be the one named in
// the Explanation, regardless of ID order.
func TestHigherPriorityAllowRuleIsAttributedOverLowerPriorityMatch(t *testing.T) {
	e := NewEngine()
	low := allowRule("z-low-priority", "read", "case/*", "analyst")
	low.Priority = 1
	high := allowRule("a-high-priority", "read", "case/*", "analyst")
	high.Priority = 10
	publishActive(t, e, doc(1, []Rule{low, high}))

	ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read", Resource: "case/1"})
	if err != nil {
		t.Fatal(err)
	}
	if !ex.Allowed {
		t.Fatalf("expected allow: %+v", ex)
	}
	if ex.MatchedRule != "a-high-priority" {
		t.Fatalf("expected the higher-Priority rule to be attributed despite losing on ID order, got %q", ex.MatchedRule)
	}
}

// TestZeroPriorityRulesFallBackToIDOrder confirms a document where
// every rule is left at the Priority zero-value (every document
// written before this field existed) evaluates identically to the
// original ID-only tie-break.
func TestZeroPriorityRulesFallBackToIDOrder(t *testing.T) {
	e := NewEngine()
	publishActive(t, e, doc(1, []Rule{
		allowRule("z-rule", "read", "case/*", "analyst"),
		allowRule("a-rule", "read", "case/*", "analyst"),
	}))
	ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read", Resource: "case/1"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.MatchedRule != "a-rule" {
		t.Fatalf("expected ID-order tie-break (a-rule before z-rule) when priorities are equal, got %q", ex.MatchedRule)
	}
}

// TestPriorityCannotOverrideDenyWins is the safety-critical adversarial
// test named in Rule.Priority's own doc comment: a maximum-Priority
// ALLOW rule must still lose to a minimum-Priority DENY rule that
// matches the same request. Priority orders WHICH allow is attributed;
// it must never be usable to rank an allow above a deny.
func TestPriorityCannotOverrideDenyWins(t *testing.T) {
	e := NewEngine()
	allow := allowRule("z-allow-max-priority", "read", "case/sealed*", "analyst")
	allow.Priority = 1000
	deny := Rule{ID: "a-deny-min-priority", Effect: Deny, Priority: -1000,
		Roles: []string{"analyst"}, Actions: []string{"read"}, Resources: []string{"case/sealed*"}}
	publishActive(t, e, doc(1, []Rule{allow, deny}))

	ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read", Resource: "case/sealed-9"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Allowed {
		t.Fatalf("a maximum-Priority ALLOW rule overrode a minimum-Priority DENY rule: %+v", ex)
	}
	if ex.MatchedRule != "a-deny-min-priority" {
		t.Fatalf("expected the deny rule to be the deciding rule regardless of priority ordering: %+v", ex)
	}
}

// TestPolicyNotYetActiveDeniesRatherThanHonouringIt is
// ActivatesAtTick's mirror of the existing ExpiresAtTick test: a
// request evaluated before the policy's effective-from tick must be
// denied, not silently evaluated against rules that are not supposed
// to be live yet.
func TestPolicyNotYetActiveDeniesRatherThanHonouringIt(t *testing.T) {
	e := NewEngine()
	d := doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")})
	d.ActivatesAtTick = 100
	publishActive(t, e, d)

	ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read", Resource: "case/1", Tick: 50})
	if !errors.Is(err, ErrPolicyNotYetActive) {
		t.Fatalf("expected ErrPolicyNotYetActive before the activation tick, got %v", err)
	}
	if ex.Allowed {
		t.Fatal("a not-yet-active policy version must not be honoured")
	}

	ex2, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read", Resource: "case/1", Tick: 100})
	if err != nil {
		t.Fatalf("expected the policy to be active at its own activation tick: %v", err)
	}
	if !ex2.Allowed {
		t.Fatal("expected allow once the policy has reached its activation tick")
	}
}

// TestActivatesAtTickIsCryptographicallyBound confirms tampering with
// ActivatesAtTick after signing invalidates the signature -- the field
// is folded into canonicalBytes, not a decorative addition.
func TestActivatesAtTickIsCryptographicallyBound(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	e := NewEngine()
	published, err := e.Publish(doc(1, []Rule{allowRule("r1", "read", "case/*", "analyst")}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sign(published.Version, priv, "test-key"); err != nil {
		t.Fatal(err)
	}
	if err := e.VerifySignature(published.Version); err != nil {
		t.Fatalf("a freshly signed document must verify: %v", err)
	}

	e.mu.Lock()
	e.versions[0].ActivatesAtTick = 999999
	e.mu.Unlock()
	if err := e.VerifySignature(published.Version); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("tampering with ActivatesAtTick after signing must invalidate the signature, got %v", err)
	}
}
