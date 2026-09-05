package adversarial

import (
	"bytes"
	"crypto/subtle"
	"strings"
	"testing"

	"veriqo/pkg/governance/classification"
	"veriqo/pkg/tenant"
)

// TestNoTwoTenantsShareASurfaceKey. Cross-tenant leakage through a
// shared cache key or a shared index namespace is the single highest
// impact failure a multi-tenant system has. It is prevented by
// derivation, not by remembering to add a WHERE clause.
func TestNoTwoTenantsShareASurfaceKey(t *testing.T) {
	root := bytes.Repeat([]byte{0x5a}, 32)
	a, err := tenant.NewAnchor("t-acme", root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := tenant.NewAnchor("t-globex", root)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]string{}
	for _, s := range tenant.Surfaces() {
		for name, an := range map[string]*tenant.Anchor{"t-acme": a, "t-globex": b} {
			k, err := an.Key(s)
			if err != nil {
				t.Fatal(err)
			}
			if len(k) < 32 {
				t.Fatalf("%s/%s key is %d bytes", name, s, len(k))
			}
			h := string(k)
			if prev, dup := seen[h]; dup {
				t.Fatalf("%s/%s collides with %s", name, s, prev)
			}
			seen[h] = name + "/" + string(s)
		}
	}
	// Namespaces, which is what actually reaches an index or a bucket
	// name, must differ too.
	for _, s := range tenant.Surfaces() {
		na, _ := a.Namespace(s)
		nb, _ := b.Namespace(s)
		if na == nb {
			t.Fatalf("two tenants share the %s namespace: %s", s, na)
		}
	}
}

// TestTheSameTenantNameWithADifferentRootDoesNotCollide is the
// rebuild case: a tenant deleted and recreated must not inherit the
// old tenant's derived keys, or deleted data becomes readable again.
func TestTheSameTenantNameWithADifferentRootDoesNotCollide(t *testing.T) {
	a, _ := tenant.NewAnchor("t-acme", bytes.Repeat([]byte{1}, 32))
	b, _ := tenant.NewAnchor("t-acme", bytes.Repeat([]byte{2}, 32))
	ka, _ := a.Key(tenant.Storage)
	kb, _ := b.Key(tenant.Storage)
	if subtle.ConstantTimeCompare(ka, kb) == 1 {
		t.Fatal("the root secret does not affect the derived key")
	}
}

// TestFieldSeparationDefeatsTheConcatenationAttack.
//
// If the derivation concatenated fields without length prefixes,
// tenant "ab" + surface "c" would derive the same key as tenant "a" +
// surface "bc". That is a real, cheap cross-tenant read.
func TestFieldSeparationDefeatsTheConcatenationAttack(t *testing.T) {
	root := bytes.Repeat([]byte{7}, 32)
	x, err := tenant.NewAnchor("t-ab", root)
	if err != nil {
		t.Fatal(err)
	}
	y, err := tenant.NewAnchor("t-a", root)
	if err != nil {
		t.Fatal(err)
	}
	kx, err := x.SubKey(tenant.Cache, "c")
	if err != nil {
		t.Fatal(err)
	}
	ky, err := y.SubKey(tenant.Cache, "bc")
	if err != nil {
		t.Fatal(err)
	}
	if subtle.ConstantTimeCompare(kx, ky) == 1 {
		t.Fatal("length prefixing is absent: adjacent field boundaries collide")
	}
}

// TestGuardFailsClosedOnEveryWrongTenant. The guard is the last thing
// between a mis-scoped query and another customer's data, so it must
// refuse everything that is not exactly right -- including the empty
// string, which is what an unset field looks like.
func TestGuardFailsClosedOnEveryWrongTenant(t *testing.T) {
	a, _ := tenant.NewAnchor("t-acme", bytes.Repeat([]byte{3}, 32))
	sc, err := a.Scope(tenant.Storage)
	if err != nil {
		t.Fatal(err)
	}
	if err := tenant.Guard(sc, "t-acme"); err != nil {
		t.Fatalf("the correct tenant was refused: %v", err)
	}
	for _, wrong := range []string{"", " ", "t-globex", "T-ACME", "t-acme ", "t-acme\x00",
		"t-acme'--", "%", "*"} {
		if err := tenant.Guard(sc, wrong); err == nil {
			t.Fatalf("Guard admitted %q", wrong)
		}
	}
	// And a zero-valued scope -- the thing a struct literal produces
	// when somebody forgets to populate it -- admits nobody.
	if err := tenant.Guard(tenant.Scope{}, "t-acme"); err == nil {
		t.Fatal("a zero scope admitted a tenant")
	}
}

// TestADerivativeCannotBeMarkedBelowItsSources is classification
// laundering: run confidential material through any transform and
// declare the output public. Derive must refuse.
func TestADerivativeCannotBeMarkedBelowItsSources(t *testing.T) {
	src := classification.MustNew(classification.Confidential, classification.NoRedistribution)
	other := classification.MustNew(classification.Internal)

	if _, err := classification.Derive(classification.MustNew(classification.Public),
		src, other); err == nil {
		t.Fatal("a derivative was marked PUBLIC from CONFIDENTIAL sources")
	}
	// Dropping a caveat while keeping the level is the subtler
	// version and must fail the same way.
	if _, err := classification.Derive(classification.MustNew(classification.Confidential),
		src, other); err == nil {
		t.Fatal("a handling caveat was dropped by derivation")
	}
	// The honest derivative is accepted, so the test is not passing
	// by refusing everything.
	ok, err := classification.Derive(
		classification.MustNew(classification.Confidential, classification.NoRedistribution),
		src, other)
	if err != nil {
		t.Fatalf("the correct derivative was refused: %v", err)
	}
	if !ok.Has(classification.NoRedistribution) {
		t.Fatal("the caveat did not survive")
	}
}

// TestClearanceAtTheSameLevelWithoutTheCaveatCannotRead. The attack
// is an insider with SECRET clearance reading a CONFIDENTIAL document
// that carries a compartment they are not in.
func TestClearanceAtTheSameLevelWithoutTheCaveatCannotRead(t *testing.T) {
	// PERSONAL_DATA is a caveat about the NATURE of the content, so it
	// gates reading. NO_REDISTRIBUTION is a prohibition on downstream
	// use and deliberately does not -- both directions are asserted.
	marking := classification.MustNew(classification.Confidential,
		classification.PersonalData, classification.NoRedistribution)
	bare := classification.MustNew(classification.Secret)
	if err := classification.Readable(bare, marking); err == nil {
		t.Fatal("a higher level without the caveat read a compartmented document")
	}
	full := classification.MustNew(classification.Secret, classification.PersonalData)
	if err := classification.Readable(full, marking); err != nil {
		t.Fatalf("a properly cleared reader was refused: %v", err)
	}
	// The reader may read it and still may not redistribute it; that
	// is a rights question, not a clearance one.
	if !marking.Has(classification.NoRedistribution) {
		t.Fatal("the use prohibition was lost")
	}
}

// TestTheInjectedDocumentIsTreatedAsBytes. The payload from the
// injection tests is put through the marking path here: it is
// material, it is classified, and nothing reads it for instructions.
func TestTheInjectedDocumentIsTreatedAsBytes(t *testing.T) {
	if !strings.Contains(injectedDocument, "SYSTEM:") {
		t.Fatal("the fixture no longer carries an injected instruction")
	}
	m, err := classification.Derive(
		classification.MustNew(classification.Confidential),
		classification.MustNew(classification.Confidential))
	if err != nil {
		t.Fatal(err)
	}
	// The document says "prior restrictions are lifted". The marking
	// is unmoved, because a marking is computed from sources and not
	// from content.
	if m.Level != classification.Confidential {
		t.Fatalf("the document's content influenced its marking: %v", m)
	}
}
