package keys

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

// flipLastHexByte returns hexStr with its last encoded byte's bits all
// inverted (XOR 0xFF), guaranteeing a different byte regardless of the
// original value -- unlike overwriting the last byte with a fixed
// literal (this test's own prior approach), which is a no-op roughly
// 1-in-256 times, whenever the real ciphertext already happens to end
// in that exact literal, silently turning an intended tamper-detection
// test into "decrypt an untampered ciphertext and expect an error" --
// a real, previously-latent test bug this exact flakiness reproduced,
// not a defect in AEAD authentication itself.
func flipLastHexByte(t *testing.T, hexStr string) string {
	t.Helper()
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("decode hex for tamper: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("cannot tamper an empty ciphertext")
	}
	raw[len(raw)-1] ^= 0xFF
	return hex.EncodeToString(raw)
}

func setup(t *testing.T) (*Manager, *MemoryKeyProvider) {
	t.Helper()
	p := NewMemoryKeyProvider()
	m := NewManager(p)
	md, err := p.Generate("evidence-1", PurposeEvidence, 0, 1000)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := m.Register(md); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := m.Activate("evidence-1"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return m, p
}

func digest(s string) []byte { d := sha256.Sum256([]byte(s)); return d[:] }

func TestSignVerifyRoundTrip(t *testing.T) {
	m, _ := setup(t)
	ctx := context.Background()
	sig, err := m.Sign(ctx, "evidence-1", digest("hello"), 10)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := m.Verify(ctx, "evidence-1", digest("hello"), sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := m.Verify(ctx, "evidence-1", digest("tampered"), sig); !errors.Is(err, ErrBadSignature) {
		t.Fatal("signature verified over different content")
	}
}

func TestPendingKeyCannotSign(t *testing.T) {
	p := NewMemoryKeyProvider()
	m := NewManager(p)
	md, _ := p.Generate("k", PurposeEvidence, 0, 0)
	if err := m.Register(md); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := m.Sign(context.Background(), "k", digest("x"), 1); !errors.Is(err, ErrKeyNotActive) {
		t.Fatal("a PENDING key signed")
	}
}

// TestRegisterForcesPendingRegardlessOfCallerSuppliedState is the direct
// adversarial proof for Register's own defensive reset: a caller who
// hand-builds KeyMetadata{State: StateActive} (or StateRevoked) and
// Registers it directly must NOT thereby skip Activate() -- the key
// must land at StatePending like any other freshly registered key, and
// must refuse to sign until a real Activate() call.
func TestRegisterForcesPendingRegardlessOfCallerSuppliedState(t *testing.T) {
	for _, forged := range []State{StateActive, StateRevoked, StateRetired} {
		p := NewMemoryKeyProvider()
		m := NewManager(p)
		forgedMD := KeyMetadata{KeyID: "forged-key", Purpose: PurposeEvidence, State: forged, PublicKey: "00"}
		if err := m.Register(forgedMD); err != nil {
			t.Fatalf("Register(%s): %v", forged, err)
		}
		got, err := m.Metadata("forged-key")
		if err != nil {
			t.Fatalf("Metadata: %v", err)
		}
		if got.State != StatePending {
			t.Fatalf("caller claimed State=%s: expected Register to force StatePending, got %s", forged, got.State)
		}
		if _, err := m.Sign(context.Background(), "forged-key", digest("x"), 1); !errors.Is(err, ErrKeyNotActive) {
			t.Fatalf("caller claimed State=%s: expected a forged key to still refuse to sign before a real Activate(), got %v", forged, err)
		}
	}
}

func TestExpiredKeyCannotSign(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.Sign(context.Background(), "evidence-1", digest("x"), 5000); !errors.Is(err, ErrKeyExpired) {
		t.Fatal("an expired key signed")
	}
}

// TestRotationPreservesHistoricalSignatures is the invariant that
// makes rotation safe: rotating must not invalidate the past.
func TestRotationPreservesHistoricalSignatures(t *testing.T) {
	m, p := setup(t)
	ctx := context.Background()
	sig, err := m.Sign(ctx, "evidence-1", digest("historic"), 10)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	next, err := p.Generate("evidence-2", PurposeEvidence, 20, 2000)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := m.Rotate("evidence-1", next); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := m.Verify(ctx, "evidence-1", digest("historic"), sig); err != nil {
		t.Fatalf("rotation invalidated a historical signature: %v", err)
	}
	if _, err := m.Sign(ctx, "evidence-1", digest("new"), 25); !errors.Is(err, ErrKeyNotActive) {
		t.Fatal("a RETIRED key produced a new signature")
	}
	active, err := m.ActiveKeyFor(PurposeEvidence)
	if err != nil {
		t.Fatalf("ActiveKeyFor: %v", err)
	}
	if active.KeyID != "evidence-2" || active.Version != 2 || active.Predecessor != "evidence-1" {
		t.Fatalf("key chain not linked: %+v", active)
	}
}

// TestRevocationInvalidatesRetroactively is the opposite invariant.
func TestRevocationInvalidatesRetroactively(t *testing.T) {
	m, _ := setup(t)
	ctx := context.Background()
	sig, err := m.Sign(ctx, "evidence-1", digest("historic"), 10)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := m.Revoke("evidence-1", "private key exposed in a leaked backup", 50); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := m.Verify(ctx, "evidence-1", digest("historic"), sig); !errors.Is(err, ErrKeyRevoked) {
		t.Fatal("a revoked key's past signature still verified")
	}
	if err := m.Activate("evidence-1"); !errors.Is(err, ErrKeyRevoked) {
		t.Fatal("a revoked key was reactivated")
	}
}

func TestPurposeSeparation(t *testing.T) {
	p := NewMemoryKeyProvider()
	m := NewManager(p)
	for id, purpose := range map[string]Purpose{"ev": PurposeEvidence, "rel": PurposeRelease} {
		md, _ := p.Generate(id, purpose, 0, 0)
		if err := m.Register(md); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := m.Activate(id); err != nil {
			t.Fatalf("Activate: %v", err)
		}
	}
	ev, err := m.ActiveKeyFor(PurposeEvidence)
	if err != nil {
		t.Fatalf("ActiveKeyFor: %v", err)
	}
	rel, err := m.ActiveKeyFor(PurposeRelease)
	if err != nil {
		t.Fatalf("ActiveKeyFor: %v", err)
	}
	if ev.KeyID == rel.KeyID {
		t.Fatal("one key serves two purposes")
	}
	if _, err := m.ActiveKeyFor(PurposePolicy); !errors.Is(err, ErrNoActiveKey) {
		t.Fatal("a purpose with no key returned one")
	}
}

func TestFileKeyProviderRoundTrip(t *testing.T) {
	p, err := NewFileKeyProvider(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileKeyProvider: %v", err)
	}
	if _, err := p.Generate("k1", PurposeEvidence, 0, 0); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := p.Generate("k1", PurposeEvidence, 0, 0); !errors.Is(err, ErrKeyExists) {
		t.Fatal("duplicate key ID accepted on disk")
	}
	ctx := context.Background()
	sig, err := p.Sign(ctx, "k1", digest("x"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	pub, err := p.PublicKey(ctx, "k1")
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	m := NewManager(p)
	md := KeyMetadata{KeyID: "k1", Purpose: PurposeEvidence, State: StateActive}
	if err := m.Register(md); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := m.Verify(ctx, "k1", digest("x"), sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(pub) == 0 {
		t.Fatal("empty public key")
	}
	if _, err := p.Sign(ctx, "missing", digest("x")); !errors.Is(err, ErrUnknownKey) {
		t.Fatal("signed with a nonexistent key")
	}
}

func TestEnvelopeEncryptDecryptAndAADBinding(t *testing.T) {
	e := NewEnvelope()
	if err := e.GenerateKEK("kek-1"); err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	plaintext := []byte("beneficial owner: REDACTED-SUBJECT")
	aad := []byte("tenant=acme;classification=RESTRICTED")
	ct, err := e.Encrypt("kek-1", plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains([]byte(ct.Ciphertext), []byte("REDACTED-SUBJECT")) {
		t.Fatal("plaintext leaked into ciphertext")
	}
	got, err := e.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatal("round trip corrupted the payload")
	}
	replayed := ct
	replayed.AAD = "00"
	if _, err := e.Decrypt(replayed); !errors.Is(err, ErrCiphertextBad) {
		t.Fatal("ciphertext replayed into a different context")
	}
	tampered := ct
	tampered.Ciphertext = flipLastHexByte(t, tampered.Ciphertext)
	if _, err := e.Decrypt(tampered); !errors.Is(err, ErrCiphertextBad) {
		t.Fatal("tampered ciphertext authenticated")
	}
}

func TestKEKRotationDoesNotReencryptPayload(t *testing.T) {
	e := NewEnvelope()
	if err := e.GenerateKEK("kek-1"); err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	if err := e.GenerateKEK("kek-2"); err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	pt := []byte("sensitive")
	ct, err := e.Encrypt("kek-1", pt, []byte("aad"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	rotated, err := e.RotateKEK(ct, "kek-2")
	if err != nil {
		t.Fatalf("RotateKEK: %v", err)
	}
	if rotated.Ciphertext != ct.Ciphertext {
		t.Fatal("KEK rotation re-encrypted the payload — the point of envelope encryption is that it does not")
	}
	got, err := e.Decrypt(rotated)
	if err != nil {
		t.Fatalf("Decrypt after rotation: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatal("payload corrupted by KEK rotation")
	}
}

func TestShortKEKRejected(t *testing.T) {
	if err := NewEnvelope().AddKEK("k", []byte("too short")); err == nil {
		t.Fatal("a non-256-bit KEK was accepted")
	}
}
