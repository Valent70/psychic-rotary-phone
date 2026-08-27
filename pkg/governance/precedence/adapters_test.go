package precedence

import (
	"testing"

	"veriqo/pkg/authz"
	"veriqo/pkg/governance/hitl"
)

// TestAuthzDenyOutranksAnEarlierHITLApproval is the real-world scenario
// "Internal Gap X" names directly: a case a human already approved is
// still subject to a later, stricter authz policy DENY. Before this
// package, nothing in the repository formally said which of pkg/authz
// (ALLOW/DENY) and pkg/governance/hitl (APPROVE/REJECT/...) wins; each
// package only knew its own vocabulary. This test exercises the real
// types from both packages through the real adapters and the real
// lattice, not a stand-in.
func TestAuthzDenyOutranksAnEarlierHITLApproval(t *testing.T) {
	hitlSignal, err := FromHITLAction("hitl", hitl.ActionApprove, "reviewer approved after evidence review")
	if err != nil {
		t.Fatal(err)
	}
	authzSignal, err := FromAuthzEffect("authz", authz.Deny, "sanctions-screen@4 denies this jurisdiction pairing")
	if err != nil {
		t.Fatal(err)
	}
	v, err := Combine([]Signal{hitlSignal, authzSignal})
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != ActionDeny {
		t.Fatalf("a later authz DENY must outrank an earlier HITL APPROVE, got %s", v.Action)
	}
}

func TestHITLRejectOutranksAuthzAllowButNotDeny(t *testing.T) {
	allow, _ := FromAuthzEffect("authz", authz.Allow, "no policy objection")
	reject, _ := FromHITLAction("hitl", hitl.ActionReject, "reviewer rejected on manual grounds")
	v, err := Combine([]Signal{allow, reject})
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != ActionBlock {
		t.Fatalf("HITL REJECT must outrank authz ALLOW, got %s", v.Action)
	}

	deny, _ := FromAuthzEffect("authz", authz.Deny, "policy denies regardless")
	v2, err := Combine([]Signal{deny, reject})
	if err != nil {
		t.Fatal(err)
	}
	if v2.Action != ActionDeny {
		t.Fatalf("authz DENY must still outrank HITL REJECT (BLOCK), got %s", v2.Action)
	}
}

func TestUnknownAuthzEffectRefused(t *testing.T) {
	if _, err := FromAuthzEffect("authz", authz.Effect("MAYBE"), ""); err == nil {
		t.Fatal("an unrecognised authz effect must be refused, not silently mapped")
	}
}
