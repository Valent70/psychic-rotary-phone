package kms

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func root(t *testing.T) *SoftwareRoot {
	t.Helper()
	r, err := NewSoftwareRoot("test", bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func hierarchy(t *testing.T, production bool) *Hierarchy {
	t.Helper()
	h, err := NewHierarchy(root(t), production)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestTheSoftwareRootIsRefusedInProduction.
//
// This is the difference between a gate that is checked and a gate
// that is documented.
func TestTheSoftwareRootIsRefusedInProduction(t *testing.T) {
	_, err := NewHierarchy(root(t), true)
	if !errors.Is(err, ErrTestDoubleInProduction) {
		t.Fatalf("a software root was accepted in production: %v", err)
	}
	if !strings.Contains(err.Error(), "G1") {
		t.Fatalf("the refusal does not name the gate: %v", err)
	}
	// And it names itself unmistakably in every audit record.
	if !strings.Contains(root(t).Identifier(), "TEST-DOUBLE") {
		t.Fatalf("the test double does not identify itself: %s", root(t).Identifier())
	}
	if root(t).Attested() {
		t.Fatal("the software root reports itself attested")
	}
}

// TestTheReportWarnsWhenTheRootIsNotAttested.
func TestTheReportWarnsWhenTheRootIsNotAttested(t *testing.T) {
	h := hierarchy(t, false)
	h.Create("t-acme", "", EvidenceEncryption, now)
	r := h.Report()
	if !strings.Contains(r, "Gate G1 is not satisfied") {
		t.Fatalf("the report does not disclose the unattested root:\n%s", r)
	}
}

// TestEveryLevelAndPurposeDerivesADistinctKey.
func TestEveryLevelAndPurposeDerivesADistinctKey(t *testing.T) {
	h := hierarchy(t, false)
	var ids []string
	for _, spec := range []struct {
		tenant, kase string
		p            Purpose
	}{
		{"t-acme", "", EvidenceEncryption},
		{"t-acme", "", LedgerSigning},
		{"t-acme", "case-1", EvidenceEncryption},
		{"t-acme", "case-2", EvidenceEncryption},
		{"t-beta", "", EvidenceEncryption},
		{"t-beta", "case-1", EvidenceEncryption},
	} {
		k, err := h.Create(spec.tenant, spec.kase, spec.p, now)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, k.ID)
	}
	seen := map[string]string{}
	for _, id := range ids {
		m, err := h.Material(id, false)
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := seen[string(m)]; dup {
			t.Fatalf("%s and %s derive the same key material", prev, id)
		}
		seen[string(m)] = id
	}
	if len(seen) != len(ids) {
		t.Fatalf("%d distinct keys for %d specifications", len(seen), len(ids))
	}
}

// TestCompromisingACaseKeyYieldsNothingAboveOrBeside.
func TestCompromisingACaseKeyYieldsNothingAboveOrBeside(t *testing.T) {
	h := hierarchy(t, false)
	tenantKey, _ := h.Create("t-acme", "", EvidenceEncryption, now)
	case1, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	case2, _ := h.Create("t-acme", "case-2", EvidenceEncryption, now)

	c1, _ := h.Material(case1.ID, false)
	c2, _ := h.Material(case2.ID, false)
	tk, _ := h.Material(tenantKey.ID, false)

	if bytes.Equal(c1, c2) || bytes.Equal(c1, tk) {
		t.Fatal("a case key equals another case's or the tenant's")
	}
	// Nothing about c1 lets an attacker produce c2 or tk: the
	// derivation is one-way. Asserted here as inequality, which is
	// what a test can check.
	if bytes.Contains(tk, c1[:8]) {
		t.Fatal("the tenant key contains the case key's prefix")
	}
}

// TestASupersededKeyDecryptsHistoryAndDoesNotEncryptNewMaterial.
//
// Reading history must be a visible choice, not a default.
func TestASupersededKeyDecryptsHistoryAndDoesNotEncryptNewMaterial(t *testing.T) {
	h := hierarchy(t, false)
	k, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	before, _ := h.Material(k.ID, false)

	if _, _, err := h.Rotate(k.ID, Scheduled, "annual rotation", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	oldID := k.ID + "#gen1"
	old, err := h.Get(oldID)
	if err != nil {
		t.Fatal(err)
	}
	if old.State != Superseded {
		t.Fatalf("the old key is %s after a scheduled rotation", old.State)
	}
	// It decrypts history.
	hist, err := h.Material(oldID, true)
	if err != nil {
		t.Fatalf("a superseded key cannot decrypt history: %v", err)
	}
	if !bytes.Equal(hist, before) {
		t.Fatal("the superseded key derives different material than before rotation")
	}
	// It does not encrypt new material, and the refusal says so.
	if _, err := h.Material(oldID, false); err == nil {
		t.Fatal("a superseded key encrypted new material")
	}
	// The new generation is different.
	after, _ := h.Material(k.ID, false)
	if bytes.Equal(after, before) {
		t.Fatal("rotation did not change the key material")
	}
}

// TestARevocationDestroysAndEnumerates.
func TestARevocationDestroysAndEnumerates(t *testing.T) {
	h := hierarchy(t, false)
	k, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	for _, a := range []string{"ev:3", "ev:1", "ev:2"} {
		if err := h.RecordUse(k.ID, a); err != nil {
			t.Fatal(err)
		}
	}
	_, affected, err := h.Rotate(k.ID, Revocation,
		"the key was exposed in a support bundle", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 3 || affected[0] != "ev:1" {
		t.Fatalf("affected = %v", affected)
	}
	old, _ := h.Get(k.ID + "#gen1")
	if old.State != Destroyed {
		t.Fatalf("the revoked key is %s", old.State)
	}
	if old.UsableForDecryption() {
		t.Fatal("a destroyed key can still decrypt")
	}
	if _, err := h.Material(k.ID+"#gen1", true); !errors.Is(err, ErrRevoked) {
		t.Fatalf("a destroyed key produced material: %v", err)
	}
}

// TestAnEmergencyRotationQuarantinesRatherThanDestroys.
//
// A suspected compromise is not a confirmed one, and destroying the
// key would make the investigation impossible.
func TestAnEmergencyRotationQuarantinesRatherThanDestroys(t *testing.T) {
	h := hierarchy(t, false)
	k, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	h.RecordUse(k.ID, "ev:1")

	_, affected, err := h.Rotate(k.ID, Emergency,
		"anomalous access pattern under investigation", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 1 {
		t.Fatalf("an emergency rotation did not enumerate what the key protected: %v", affected)
	}
	old, _ := h.Get(k.ID + "#gen1")
	if old.State != Quarantined {
		t.Fatalf("an emergency rotation left the key %s", old.State)
	}
	if _, err := h.Material(k.ID+"#gen1", true); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("a quarantined key produced material: %v", err)
	}
	// The distinction is real: the four kinds do not collapse.
	if Emergency.OldKeyState() == Revocation.OldKeyState() {
		t.Fatal("EMERGENCY and REVOCATION leave the key in the same state")
	}
	if Scheduled.OldKeyState() != Superseded || Event.OldKeyState() != Superseded {
		t.Fatal("a routine rotation does not leave the key merely superseded")
	}
}

// TestARotationMustStateWhy.
func TestARotationMustStateWhy(t *testing.T) {
	h := hierarchy(t, false)
	k, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	if _, _, err := h.Rotate(k.ID, Scheduled, "", now); !errors.Is(err, ErrNoReason) {
		t.Fatalf("an unreasoned rotation was accepted: %v", err)
	}
}

// TestKeyMaterialIsNotStored. A dump of the register yields no keys.
func TestKeyMaterialIsNotStored(t *testing.T) {
	h := hierarchy(t, false)
	k, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	material, _ := h.Material(k.ID, false)

	rendered := h.Report()
	if bytes.Contains([]byte(rendered), material) {
		t.Fatal("the report contains key material")
	}
	for _, key := range h.Keys() {
		// The struct itself must carry no material field.
		if strings.Contains(key.ID, string(material)) {
			t.Fatal("key material leaked into an id")
		}
	}
	// A fingerprint lets two parties confirm they hold the same key
	// without either disclosing it.
	fp, err := h.Fingerprint(k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fp) != 16 {
		t.Fatalf("fingerprint = %q", fp)
	}
	if bytes.Contains([]byte(fp), material[:4]) {
		t.Fatal("the fingerprint discloses key material")
	}
}

// TestAQuarantinedOrDestroyedKeyCannotProtectNewMaterial.
func TestAQuarantinedOrDestroyedKeyCannotProtectNewMaterial(t *testing.T) {
	h := hierarchy(t, false)
	k, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	h.Rotate(k.ID, Revocation, "exposed", now.Add(time.Hour))
	if err := h.RecordUse(k.ID+"#gen1", "ev:new"); err == nil {
		t.Fatal("a destroyed key protected new material")
	}
}

// TestACrossTenantKeyIsRefused.
func TestACrossTenantKeyIsRefused(t *testing.T) {
	h := hierarchy(t, false)
	k, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	if err := h.GuardTenant(k.ID, "t-beta"); !errors.Is(err, ErrWrongTenant) {
		t.Fatalf("a key was used across tenants: %v", err)
	}
	if err := h.GuardTenant(k.ID, "t-acme"); err != nil {
		t.Fatalf("a key was refused to its own tenant: %v", err)
	}
}

// TestPurposesDoNotShareKeys. An evidence-encryption key must not be
// able to sign a ledger checkpoint.
func TestPurposesDoNotShareKeys(t *testing.T) {
	h := hierarchy(t, false)
	enc, _ := h.Create("t-acme", "", EvidenceEncryption, now)
	sign, _ := h.Create("t-acme", "", LedgerSigning, now)
	a, _ := h.Material(enc.ID, false)
	b, _ := h.Material(sign.ID, false)
	if bytes.Equal(a, b) {
		t.Fatal("two purposes derive the same key")
	}
	if _, err := h.Create("t-acme", "", Purpose("WHATEVER"), now); !errors.Is(err, ErrBadPurpose) {
		t.Fatal("an unknown purpose was accepted")
	}
}

// TestAShortSeedIsRefused.
func TestAShortSeedIsRefused(t *testing.T) {
	if _, err := NewSoftwareRoot("test", []byte("short")); err == nil {
		t.Fatal("a short seed was accepted")
	}
	if _, err := NewHierarchy(nil, false); !errors.Is(err, ErrNoRoot) {
		t.Fatal("a nil root was accepted")
	}
}

// TestDerivationIsStableAcrossCalls, or a key rotates itself.
func TestDerivationIsStableAcrossCalls(t *testing.T) {
	h := hierarchy(t, false)
	k, _ := h.Create("t-acme", "case-1", EvidenceEncryption, now)
	first, _ := h.Material(k.ID, false)
	for i := 0; i < 50; i++ {
		got, err := h.Material(k.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, first) {
			t.Fatal("the same key derived different material")
		}
	}
}
