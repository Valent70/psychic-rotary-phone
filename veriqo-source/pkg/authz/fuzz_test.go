package authz

import "testing"

// FuzzPolicyParse (PHASE 38) drives the policy-as-data parser with
// arbitrary bytes. Invariant: ParseDocument never panics, and anything
// it accepts must survive its own Validate.
func FuzzPolicyParse(f *testing.F) {
	f.Add([]byte(`{"id":"p","rules":[{"id":"r","effect":"ALLOW","actions":["read"],"resources":["*"]}]}`))
	f.Add([]byte(`{"id":"","rules":[]}`))
	f.Add([]byte(`not json`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		d, err := ParseDocument(raw)
		if err != nil {
			return
		}
		if err := d.Validate(); err != nil {
			t.Fatalf("ParseDocument accepted a document its own Validate rejects: %v", err)
		}
	})
}

// FuzzDenyAlwaysWins asserts the central authorization invariant for
// any rule shape: if any matching rule denies, the answer is deny.
func FuzzDenyAlwaysWins(f *testing.F) {
	f.Add("read", "case:1", "actor")
	f.Fuzz(func(t *testing.T, action, resource, actor string) {
		if action == "" || resource == "" {
			return
		}
		e := NewEngine()
		if _, err := e.Publish(Document{ID: "p", Rules: []Rule{
			{ID: "a", Effect: Allow, Actions: []string{"*"}, Resources: []string{"*"}},
			{ID: "b", Effect: Deny, Actions: []string{action}, Resources: []string{resource}},
		}}); err != nil {
			return
		}
		if err := e.Activate(1); err != nil {
			return
		}
		ex, err := e.Can(Request{Actor: actor, Action: action, Resource: resource})
		if err != nil {
			return
		}
		if ex.Allowed {
			t.Fatalf("a matching DENY was overridden by ALLOW for %q/%q", action, resource)
		}
	})
}
