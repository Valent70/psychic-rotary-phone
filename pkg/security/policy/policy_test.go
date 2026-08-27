package policy_test

import (
	"testing"

	"veriqo/pkg/security/policy"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func adminReq(action string) policy.Request {
	return policy.Request{
		Subject:  policy.Attributes{"role": "admin", "tenant": "veriqo"},
		Resource: policy.Attributes{"type": "cluster", "sensitivity": "high"},
		Action:   action,
		Context:  policy.Attributes{},
	}
}

func guestReq(action string) policy.Request {
	return policy.Request{
		Subject:  policy.Attributes{"role": "guest"},
		Resource: policy.Attributes{"type": "cluster"},
		Action:   action,
		Context:  policy.Attributes{},
	}
}

// ─── Basic evaluation ────────────────────────────────────────────────────────

func TestEngine_DefaultDeny(t *testing.T) {
	e := policy.NewEngine(nil)
	d, err := e.Evaluate(adminReq("read"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Permit {
		t.Fatal("expected default-deny with no policies")
	}
}

func TestEngine_PermitRule(t *testing.T) {
	e := policy.NewEngine(nil)
	e.Register(&policy.Policy{
		ID:        "admin-access",
		Algorithm: policy.DenyOverride,
		Rules: []policy.Rule{
			{
				ID:         "allow-admin-read",
				Priority:   10,
				Effect:     policy.EffectPermit,
				ActionGlob: "read",
				Conditions: []policy.Condition{
					policy.AttributeEquals("role", "admin"),
				},
			},
		},
	})
	d, _ := e.Evaluate(adminReq("read"))
	if !d.Permit {
		t.Fatalf("expected permit, got deny: %s", d.Reason)
	}
}

func TestEngine_DenyRule(t *testing.T) {
	e := policy.NewEngine(nil)
	e.Register(&policy.Policy{
		ID:        "block-guest",
		Algorithm: policy.DenyOverride,
		Rules: []policy.Rule{
			{
				ID:         "deny-guest",
				Priority:   20,
				Effect:     policy.EffectDeny,
				ActionGlob: "*",
				Conditions: []policy.Condition{
					policy.AttributeEquals("role", "guest"),
				},
			},
		},
	})
	d, _ := e.Evaluate(guestReq("read"))
	if d.Permit {
		t.Fatal("expected deny for guest")
	}
}

func TestEngine_DenyOverrideBeatsPerm(t *testing.T) {
	e := policy.NewEngine(nil)
	e.Register(&policy.Policy{
		ID:        "mixed",
		Algorithm: policy.DenyOverride,
		Rules: []policy.Rule{
			{ID: "permit-all", Priority: 5, Effect: policy.EffectPermit, ActionGlob: "*"},
			{ID: "deny-guest", Priority: 10, Effect: policy.EffectDeny, ActionGlob: "*",
				Conditions: []policy.Condition{policy.AttributeEquals("role", "guest")}},
		},
	})
	d, _ := e.Evaluate(guestReq("write"))
	if d.Permit {
		t.Fatal("deny-override: deny should beat permit")
	}
}

func TestEngine_PermitOverride(t *testing.T) {
	e := policy.NewEngine(nil)
	e.Register(&policy.Policy{
		ID:        "permit-override-test",
		Algorithm: policy.PermitOverride,
		Rules: []policy.Rule{
			{ID: "deny-all", Priority: 5, Effect: policy.EffectDeny, ActionGlob: "*"},
			{ID: "permit-admin", Priority: 10, Effect: policy.EffectPermit, ActionGlob: "*",
				Conditions: []policy.Condition{policy.AttributeEquals("role", "admin")}},
		},
	})
	d, _ := e.Evaluate(adminReq("delete"))
	if !d.Permit {
		t.Fatal("permit-override: permit should beat deny")
	}
}

// ─── Hierarchical policies ───────────────────────────────────────────────────

func TestPolicy_Hierarchical(t *testing.T) {
	child := &policy.Policy{
		ID:        "child",
		Algorithm: policy.DenyOverride,
		Rules: []policy.Rule{
			{ID: "child-deny-guest", Priority: 10, Effect: policy.EffectDeny, ActionGlob: "*",
				Conditions: []policy.Condition{policy.AttributeEquals("role", "guest")}},
		},
	}
	parent := &policy.Policy{
		ID:        "parent",
		Algorithm: policy.DenyOverride,
		Children:  []*policy.Policy{child},
		Rules: []policy.Rule{
			{ID: "parent-permit-admin", Priority: 5, Effect: policy.EffectPermit, ActionGlob: "*",
				Conditions: []policy.Condition{policy.AttributeEquals("role", "admin")}},
		},
	}
	e := policy.NewEngine(nil)
	e.Register(parent)

	// Admin should be permitted by parent rule.
	d, _ := e.Evaluate(adminReq("read"))
	if !d.Permit {
		t.Fatal("expected admin permit in hierarchical policy")
	}
	// Guest should be denied by child rule (deny-override propagates up).
	d, _ = e.Evaluate(guestReq("read"))
	if d.Permit {
		t.Fatal("expected guest deny from child policy")
	}
}

// ─── ABAC conditions ─────────────────────────────────────────────────────────

func TestCondition_AttributeEquals(t *testing.T) {
	cond := policy.AttributeEquals("role", "admin")
	if !cond(adminReq("read")) {
		t.Fatal("expected condition to match admin")
	}
	if cond(guestReq("read")) {
		t.Fatal("expected condition not to match guest")
	}
}

func TestCondition_All(t *testing.T) {
	cond := policy.All(
		policy.AttributeEquals("role", "admin"),
		policy.AttributeEquals("tenant", "veriqo"),
	)
	if !cond(adminReq("read")) {
		t.Fatal("All: expected match")
	}
}

func TestCondition_Any(t *testing.T) {
	cond := policy.Any(
		policy.AttributeEquals("role", "admin"),
		policy.AttributeEquals("role", "operator"),
	)
	if !cond(adminReq("read")) {
		t.Fatal("Any: expected match for admin")
	}
	if cond(guestReq("read")) {
		t.Fatal("Any: should not match guest")
	}
}

func TestCondition_Not(t *testing.T) {
	notGuest := policy.Not(policy.AttributeEquals("role", "guest"))
	if !notGuest(adminReq("read")) {
		t.Fatal("Not: admin should pass not-guest")
	}
	if notGuest(guestReq("read")) {
		t.Fatal("Not: guest should fail not-guest")
	}
}

func TestCondition_ResourceAttribute(t *testing.T) {
	cond := policy.ResourceAttributeEquals("sensitivity", "high")
	if !cond(adminReq("read")) {
		t.Fatal("ResourceAttributeEquals: should match high-sensitivity resource")
	}
}

// ─── Glob matching ───────────────────────────────────────────────────────────

func TestGlob_WildcardAction(t *testing.T) {
	e := policy.NewEngine(nil)
	e.Register(&policy.Policy{
		ID:        "wildcard",
		Algorithm: policy.PermitOverride,
		Rules: []policy.Rule{
			{ID: "permit-all-reads", Priority: 1, Effect: policy.EffectPermit,
				ActionGlob: "read*",
				Conditions: []policy.Condition{policy.AttributeEquals("role", "admin")}},
		},
	})
	for _, action := range []string{"read", "read:objects", "readAll"} {
		d, _ := e.Evaluate(policy.Request{
			Subject: policy.Attributes{"role": "admin"},
			Action:  action,
		})
		if !d.Permit {
			t.Errorf("glob read* should match %q", action)
		}
	}
}

// ─── Context-based policy ────────────────────────────────────────────────────

func TestPolicy_ContextualDeny(t *testing.T) {
	e := policy.NewEngine(nil)
	e.Register(&policy.Policy{
		ID:        "context-deny",
		Algorithm: policy.DenyOverride,
		Rules: []policy.Rule{
			{
				ID: "deny-high-risk-context", Priority: 100,
				Effect:     policy.EffectDeny,
				ActionGlob: "*",
				Conditions: []policy.Condition{
					policy.ContextAttributeEquals("risk_level", "critical"),
				},
			},
			{
				ID: "permit-admin", Priority: 10,
				Effect:     policy.EffectPermit,
				ActionGlob: "*",
				Conditions: []policy.Condition{policy.AttributeEquals("role", "admin")},
			},
		},
	})
	// Even admin is denied in critical-risk context.
	req := policy.Request{
		Subject:  policy.Attributes{"role": "admin"},
		Resource: policy.Attributes{},
		Action:   "execute",
		Context:  policy.Attributes{"risk_level": "critical"},
	}
	d, _ := e.Evaluate(req)
	if d.Permit {
		t.Fatal("expected denial in critical risk context")
	}
}

// ─── Fuzz ─────────────────────────────────────────────────────────────────────

func FuzzPolicyEval(f *testing.F) {
	f.Add("admin", "read", "cluster")
	f.Add("guest", "write", "ledger")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, role, action, resourceType string) {
		e := policy.NewEngine(nil)
		e.Register(&policy.Policy{
			ID:        "fuzz-policy",
			Algorithm: policy.DenyOverride,
			Rules: []policy.Rule{
				{ID: "r1", Priority: 1, Effect: policy.EffectPermit, ActionGlob: "*",
					Conditions: []policy.Condition{policy.AttributeEquals("role", "admin")}},
				{ID: "r2", Priority: 5, Effect: policy.EffectDeny, ActionGlob: "*",
					Conditions: []policy.Condition{policy.AttributeEquals("role", "blocked")}},
			},
		})
		req := policy.Request{
			Subject:  policy.Attributes{"role": role},
			Resource: policy.Attributes{"type": resourceType},
			Action:   action,
		}
		d, err := e.Evaluate(req)
		if err != nil {
			t.Fatalf("fuzz: unexpected error: %v", err)
		}
		_ = d.Permit
	})
}

// ─── Benchmarks ───────────────────────────────────────────────────────────────

func BenchmarkEngine_Evaluate(b *testing.B) {
	e := policy.NewEngine(nil)
	for i := range 10 {
		var rules []policy.Rule
		for j := range 20 {
			rules = append(rules, policy.Rule{
				ID:         "r" + string(rune('a'+j)),
				Priority:   j,
				Effect:     policy.EffectPermit,
				ActionGlob: "*",
				Conditions: []policy.Condition{policy.AttributeEquals("tier", string(rune('A'+i)))},
			})
		}
		e.Register(&policy.Policy{ID: "p" + string(rune('a'+i)), Algorithm: policy.DenyOverride, Rules: rules})
	}
	req := adminReq("read")
	b.ResetTimer()
	for range b.N {
		e.Evaluate(req)
	}
}
