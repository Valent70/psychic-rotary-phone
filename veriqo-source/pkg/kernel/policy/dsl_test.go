package policy

import "testing"

type fakeSigner struct{ id string }

func (f fakeSigner) SignerID() string { return f.id }
func (f fakeSigner) Sign(msg []byte) []byte {
	out := make([]byte, len(msg))
	for i, b := range msg {
		out[i] = b ^ 0x5A // toy signature — just needs to be deterministic for this test
	}
	return out
}

func TestPolicyDSL_VersionedSignedRollbackable(t *testing.T) {
	store := NewPolicyStore().WithSigner(fakeSigner{id: "policy-admin-1"})

	v1, err := store.Publish([]DSLRule{
		{Name: "deny-sanctioned-flag", Subject: "*", Action: "trade", Resource: "vessel/*",
			Conditions: []Condition{{Field: "flag_state", Op: "in", Value: "KP,IR"}}, Effect: DSLDeny},
		{Name: "allow-verified-trade", Subject: "*", Action: "trade", Resource: "vessel/*", Effect: DSLAllow},
	})
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	if v1.Version != 1 || v1.SignedBy != "policy-admin-1" || v1.SignatureHex == "" {
		t.Fatalf("v1 not properly versioned/signed: %+v", v1)
	}

	// Deny-by-default: nothing published yet for a DIFFERENT action.
	eff, _, _ := store.Evaluate("trader-1", "delete-evidence", "vessel/IMO123", nil)
	if eff != DSLDeny {
		t.Fatalf("expected deny-by-default for unmatched action, got %s", eff)
	}

	// Sanctioned flag must be denied.
	eff, reason, _ := store.Evaluate("trader-1", "trade", "vessel/IMO123", map[string]string{"flag_state": "KP"})
	if eff != DSLDeny {
		t.Fatalf("sanctioned-flag trade should be denied, got %s (%s)", eff, reason)
	}
	// Non-sanctioned trade allowed.
	eff, _, _ = store.Evaluate("trader-1", "trade", "vessel/IMO123", map[string]string{"flag_state": "PA"})
	if eff != DSLAllow {
		t.Fatalf("non-sanctioned trade should be allowed, got %s", eff)
	}

	// Publish v2 that (mistakenly) allows everything.
	v2, err := store.Publish([]DSLRule{{Name: "allow-all", Subject: "*", Action: "*", Resource: "*", Effect: DSLAllow}})
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if v2.PrevHash != v1.Hash {
		t.Fatal("v2.PrevHash does not chain to v1.Hash")
	}
	eff, _, _ = store.Evaluate("trader-1", "trade", "vessel/IMO123", map[string]string{"flag_state": "KP"})
	if eff != DSLAllow {
		t.Fatal("v2 should now allow everything including sanctioned flags (demonstrating the bad policy took effect)")
	}

	// Roll back to v1's behavior.
	v3, err := store.Rollback(1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if v3.Version != 3 {
		t.Fatalf("rollback must publish a NEW version (append-only history), got version %d", v3.Version)
	}
	eff, _, _ = store.Evaluate("trader-1", "trade", "vessel/IMO123", map[string]string{"flag_state": "KP"})
	if eff != DSLDeny {
		t.Fatal("after rollback, sanctioned-flag trade should be denied again")
	}

	// Full chain, all 3 versions, must independently re-verify.
	if err := store.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}

	// Tamper detection: mutate history in place (simulating a compromised
	// store) and confirm VerifyChain catches it.
	store.mu.Lock()
	store.history[0].Rules[0].Effect = DSLAllow // flip a historical rule's effect
	store.mu.Unlock()
	if err := store.VerifyChain(); err == nil {
		t.Fatal("VerifyChain did not detect tampering with historical policy content")
	}
}
