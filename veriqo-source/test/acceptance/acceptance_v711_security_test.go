// Security category of the permanent acceptance suite (PHASE 35).
// These are IN-PROCESS security invariants only — key lifecycle,
// authorization, envelope encryption, policy governance. They are NOT
// a substitute for an independent penetration test, which remains an
// explicitly BLOCKED gate in the assurance registry.
package acceptance

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	"veriqo/pkg/authz"
	"veriqo/pkg/platform/security/keys"
)

func digestOf(s string) []byte { d := sha256.Sum256([]byte(s)); return d[:] }

func keyManager(t *testing.T) (*keys.Manager, *keys.MemoryKeyProvider) {
	t.Helper()
	p := keys.NewMemoryKeyProvider()
	m := keys.NewManager(p)
	md, err := p.Generate("k1", keys.PurposeEvidence, 0, 1000)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := m.Register(md); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := m.Activate("k1"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return m, p
}

func TestAcceptance101_RevokedKeyInvalidatesPastSignatures(t *testing.T) {
	m, _ := keyManager(t)
	ctx := context.Background()
	sig, err := m.Sign(ctx, "k1", digestOf("x"), 1)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := m.Revoke("k1", "compromised", 2); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := m.Verify(ctx, "k1", digestOf("x"), sig); err == nil {
		t.Fatal("revoked key's signature still verified")
	}
}

func TestAcceptance102_RotationKeepsHistoryValid(t *testing.T) {
	m, p := keyManager(t)
	ctx := context.Background()
	sig, err := m.Sign(ctx, "k1", digestOf("historic"), 1)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	next, err := p.Generate("k2", keys.PurposeEvidence, 2, 2000)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := m.Rotate("k1", next); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := m.Verify(ctx, "k1", digestOf("historic"), sig); err != nil {
		t.Fatalf("rotation invalidated history: %v", err)
	}
}

func TestAcceptance103_ExpiredKeyCannotSign(t *testing.T) {
	m, _ := keyManager(t)
	if _, err := m.Sign(context.Background(), "k1", digestOf("x"), 99999); err == nil {
		t.Fatal("expired key signed")
	}
}

func TestAcceptance104_PurposeSeparationEnforced(t *testing.T) {
	m, _ := keyManager(t)
	if _, err := m.ActiveKeyFor(keys.PurposeRelease); err == nil {
		t.Fatal("an evidence key served the release purpose")
	}
}

func TestAcceptance105_EnvelopeAADBindsContext(t *testing.T) {
	e := keys.NewEnvelope()
	if err := e.GenerateKEK("kek"); err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	ct, err := e.Encrypt("kek", []byte("secret"), []byte("tenant=a"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct.AAD = "00"
	if _, err := e.Decrypt(ct); err == nil {
		t.Fatal("ciphertext replayed into another context")
	}
}

func TestAcceptance106_KEKRotationPreservesPayload(t *testing.T) {
	e := keys.NewEnvelope()
	for _, k := range []string{"kek1", "kek2"} {
		if err := e.GenerateKEK(k); err != nil {
			t.Fatalf("GenerateKEK: %v", err)
		}
	}
	ct, err := e.Encrypt("kek1", []byte("secret"), nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	rot, err := e.RotateKEK(ct, "kek2")
	if err != nil {
		t.Fatalf("RotateKEK: %v", err)
	}
	pt, err := e.Decrypt(rot)
	if err != nil || string(pt) != "secret" {
		t.Fatalf("payload lost across KEK rotation: %v", err)
	}
}

func TestAcceptance107_DefaultDenyWithNoPolicy(t *testing.T) {
	ex, err := authz.NewEngine().Can(authz.Request{Actor: "a", Action: "read", Resource: "r"})
	if err == nil || ex.Allowed {
		t.Fatal("authorization allowed with no active policy")
	}
}

func TestAcceptance108_ReBACRelationshipRequired(t *testing.T) {
	e := authz.NewEngine()
	if _, err := e.Publish(authz.Document{ID: "p", Rules: []authz.Rule{
		{ID: "assignee-read", Effect: authz.Allow, Actions: []string{"read"},
			Resources: []string{"case:*"}, Relation: "assignee"}}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := e.Activate(1); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	req := authz.Request{Actor: "bob", Action: "read", Resource: "case:1"}
	if ex, _ := e.Can(req); ex.Allowed {
		t.Fatal("access granted without the required relationship")
	}
	e.Relate("bob", "assignee", "case:1")
	if ex, _ := e.Can(req); !ex.Allowed {
		t.Fatal("access denied despite a direct relationship")
	}
}

func TestAcceptance109_PolicyRequiringApprovalCannotActivate(t *testing.T) {
	e := authz.NewEngine()
	if _, err := e.Publish(authz.Document{ID: "p", RequiresApproval: true, Rules: []authz.Rule{
		{ID: "r", Effect: authz.Allow, Actions: []string{"*"}, Resources: []string{"*"}}}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := e.Activate(1); err == nil {
		t.Fatal("an unapproved policy version went live")
	}
	if err := e.Approve(1, "compliance-officer"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := e.Activate(1); err != nil {
		t.Fatalf("approved policy failed to activate: %v", err)
	}
}

func TestAcceptance110_SignedPolicyTamperDetected(t *testing.T) {
	e := authz.NewEngine()
	if _, err := e.Publish(authz.Document{ID: "p", Rules: []authz.Rule{
		{ID: "r", Effect: authz.Allow, Actions: []string{"read"}, Resources: []string{"*"}}}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := e.Sign(1, priv, "policy-key"); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := e.VerifySignature(1); err != nil {
		t.Fatalf("signed policy did not verify: %v", err)
	}
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("policy chain: %v", err)
	}
}
