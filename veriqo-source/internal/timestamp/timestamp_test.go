package timestamp

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func TestIssueChainsAndVerifies(t *testing.T) {
	pub, priv := genKey(t)
	a := NewAuthority(priv, "tsa-1")
	e1, err := a.Issue("cert-hash-1", 1000)
	if err != nil {
		t.Fatalf("Issue 1: %v", err)
	}
	e2, err := a.Issue("cert-hash-2", 1010)
	if err != nil {
		t.Fatalf("Issue 2: %v", err)
	}
	e3, err := a.Issue("cert-hash-3", 1020)
	if err != nil {
		t.Fatalf("Issue 3: %v", err)
	}
	if e1.Seq != 1 || e2.Seq != 2 || e3.Seq != 3 {
		t.Fatalf("sequence numbers must increment: %d %d %d", e1.Seq, e2.Seq, e3.Seq)
	}
	if e2.PrevHash != e1.Hash || e3.PrevHash != e2.Hash {
		t.Fatal("each entry must chain onto the hash of the one before it")
	}
	if err := VerifyChain([]Entry{e1, e2, e3}, pub); err != nil {
		t.Fatalf("a genuinely well-formed chain must verify: %v", err)
	}
}

func TestIssueRefusesToBackdate(t *testing.T) {
	_, priv := genKey(t)
	a := NewAuthority(priv, "tsa-1")
	if _, err := a.Issue("first", 1000); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := a.Issue("second-but-earlier", 500); !errors.Is(err, ErrNotMonotonic) {
		t.Fatalf("issuing an earlier timestamp than the chain's head must be refused, got %v", err)
	}
}

func TestVerifyChainDetectsReordering(t *testing.T) {
	pub, priv := genKey(t)
	a := NewAuthority(priv, "tsa-1")
	e1, _ := a.Issue("a", 100)
	e2, _ := a.Issue("b", 200)
	if err := VerifyChain([]Entry{e2, e1}, pub); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("a reordered chain must fail with ErrChainBroken, got %v", err)
	}
}

func TestVerifyChainDetectsEntryRemoval(t *testing.T) {
	pub, priv := genKey(t)
	a := NewAuthority(priv, "tsa-1")
	e1, _ := a.Issue("a", 100)
	_, _ = a.Issue("b", 200)
	e3, _ := a.Issue("c", 300)
	// Drop the middle entry: e3.PrevHash no longer matches e1.Hash.
	if err := VerifyChain([]Entry{e1, e3}, pub); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("removing a middle entry must break the chain, got %v", err)
	}
}

func TestVerifyChainDetectsContentTamperAfterIssuance(t *testing.T) {
	pub, priv := genKey(t)
	a := NewAuthority(priv, "tsa-1")
	e1, _ := a.Issue("original-subject", 100)
	tampered := e1
	tampered.Timestamp = 1 // attacker tries to claim it was issued near-epoch
	if err := VerifyChain([]Entry{tampered}, pub); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("editing an entry's timestamp without re-signing must be caught as a hash mismatch, got %v", err)
	}
}

func TestVerifyChainDetectsWrongSigningKey(t *testing.T) {
	_, priv := genKey(t)
	otherPub, _ := genKey(t)
	a := NewAuthority(priv, "tsa-1")
	e1, _ := a.Issue("a", 100)
	if err := VerifyChain([]Entry{e1}, otherPub); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("verifying against a different authority's public key must fail, got %v", err)
	}
}

func TestResumeContinuesAnExistingChainAndPreservesMonotonicity(t *testing.T) {
	pub, priv := genKey(t)
	a := NewAuthority(priv, "tsa-1")
	e1, _ := a.Issue("a", 100)

	resumed := Resume(priv, "tsa-1", e1)
	e2, err := resumed.Issue("b", 200)
	if err != nil {
		t.Fatalf("Issue after Resume: %v", err)
	}
	if err := VerifyChain([]Entry{e1, e2}, pub); err != nil {
		t.Fatalf("a chain continued via Resume must still verify end to end: %v", err)
	}

	resumedAgain := Resume(priv, "tsa-1", e1)
	if _, err := resumedAgain.Issue("backdated", 50); !errors.Is(err, ErrNotMonotonic) {
		t.Fatalf("Resume must preserve the tail's timestamp as the new monotonic floor, got %v", err)
	}
}
