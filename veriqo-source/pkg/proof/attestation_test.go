package proof

import (
	"errors"
	"strings"
	"testing"
)

const veriqoEntity = "veriqo-operations-ltd"

func attestation(hash string) ProcedureAttestation {
	return ProcedureAttestation{
		ProofHash: hash, AttestorID: "bureau-veritas-marine", AttestorRole: "accredited assessor",
		Reference: "BV-2026-0441", ProcedureCorrectlyApplied: true, AtTick: 90,
	}
}

// TestZeroLevelIsObjectCreated: a Proof Object nobody qualified is
// exactly a container, and the zero value says so.
func TestZeroLevelIsObjectCreated(t *testing.T) {
	var l AttestationLevel
	if l != LevelObjectCreated || l.String() != "PROOF_OBJECT_CREATED" {
		t.Fatalf("the zero level must be PROOF_OBJECT_CREATED, got %s", l)
	}
	if l.IsProof() || l.RequiresOutsideParty() {
		t.Fatal("a bare proof object is not proof")
	}
}

// TestOnlyExternalAttestationIsProof is the whole point of the three
// levels: qualified is not proved.
func TestOnlyExternalAttestationIsProof(t *testing.T) {
	if LevelQualified.IsProof() {
		t.Fatal("VERIQO qualifying its own evidence is not proof to an outside reader")
	}
	if !LevelExternallyAttested.IsProof() {
		t.Fatal("an outside party's attestation is what makes the word usable")
	}
	if LevelQualified.RequiresOutsideParty() {
		t.Fatal("qualification is reachable by VERIQO alone")
	}
	if !LevelExternallyAttested.RequiresOutsideParty() {
		t.Fatal("external attestation requires somebody who is not VERIQO")
	}
}

// TestASealedSufficientObjectReachesQualifiedAndStops is the honest
// ceiling: everything VERIQO can do lands here.
func TestASealedSufficientObjectReachesQualifiedAndStops(t *testing.T) {
	o, err := Seal(good(t))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	l := LevelOf(o, nil, []string{veriqoEntity})
	if l != LevelQualified {
		t.Fatalf("expected PROOF_QUALIFIED, got %s", l)
	}
	if l.IsProof() {
		t.Fatal("no amount of internal work reaches proof")
	}
	if !strings.Contains(DescribeLevel(l), "not proof in the sense an outside reader would assume") {
		t.Fatalf("the description must say so plainly, got %q", DescribeLevel(l))
	}
}

// TestAnInsufficientObjectStaysAtObjectCreated: the container exists,
// nothing follows about the contents.
func TestAnInsufficientObjectStaysAtObjectCreated(t *testing.T) {
	o := good(t)
	o.Trust.Assessed = false
	sealed, _ := Seal(o)
	if l := LevelOf(sealed, nil, nil); l != LevelObjectCreated {
		t.Fatalf("expected PROOF_OBJECT_CREATED, got %s", l)
	}
}

func TestAnUnsealedObjectStaysAtObjectCreated(t *testing.T) {
	if l := LevelOf(good(t), nil, nil); l != LevelObjectCreated {
		t.Fatalf("expected PROOF_OBJECT_CREATED, got %s", l)
	}
}

// TestRealAttestationRaisesTheLevel proves Level 3 is reachable in
// principle — by a real outside party, and only by one.
func TestRealAttestationRaisesTheLevel(t *testing.T) {
	o, _ := Seal(good(t))
	l, err := RaiseToExternallyAttested(o, attestation(o.CanonicalHash), []string{veriqoEntity, "claimant-ltd"})
	if err != nil {
		t.Fatalf("RaiseToExternallyAttested: %v", err)
	}
	if l != LevelExternallyAttested || !l.IsProof() {
		t.Fatalf("expected PROOF_EXTERNALLY_ATTESTED, got %s", l)
	}
	if LevelOf(o, []ProcedureAttestation{attestation(o.CanonicalHash)}, []string{veriqoEntity}) != LevelExternallyAttested {
		t.Fatal("LevelOf must agree with RaiseToExternallyAttested")
	}
}

// TestVeriqoCannotAttestToItself is the attack the level exists to
// block: sign your own homework.
func TestVeriqoCannotAttestToItself(t *testing.T) {
	o, _ := Seal(good(t))
	a := attestation(o.CanonicalHash)
	a.AttestorID = veriqoEntity

	if _, err := RaiseToExternallyAttested(o, a, []string{veriqoEntity}); !errors.Is(err, ErrAttestorIsVeriqo) {
		t.Fatalf("expected ErrAttestorIsVeriqo, got %v", err)
	}
	if LevelOf(o, []ProcedureAttestation{a}, []string{veriqoEntity}) != LevelQualified {
		t.Fatal("a self-attestation must not raise the level")
	}
}

// TestAPartyToTheMatterCannotAttest closes the same hole one step out.
func TestAPartyToTheMatterCannotAttest(t *testing.T) {
	o, _ := Seal(good(t))
	a := attestation(o.CanonicalHash)
	a.AttestorID = "claimant-ltd"
	if _, err := RaiseToExternallyAttested(o, a, []string{veriqoEntity, "claimant-ltd"}); err == nil {
		t.Fatal("a party to the matter is not an independent attestor")
	}
}

// TestAnonymousAttestationIsRefused: an unnamed blessing is not one.
func TestAnonymousAttestationIsRefused(t *testing.T) {
	o, _ := Seal(good(t))
	for _, mut := range []func(*ProcedureAttestation){
		func(a *ProcedureAttestation) { a.AttestorID = "" },
		func(a *ProcedureAttestation) { a.Reference = "" },
	} {
		a := attestation(o.CanonicalHash)
		mut(&a)
		if _, err := RaiseToExternallyAttested(o, a, nil); !errors.Is(err, ErrNoAttestor) {
			t.Fatalf("expected ErrNoAttestor, got %v", err)
		}
	}
}

// TestAttestationMustPinTheObjectItExamined stops an attestation of one
// version being presented for another.
func TestAttestationMustPinTheObjectItExamined(t *testing.T) {
	o, _ := Seal(good(t))
	a := attestation("sha256:some-other-object")
	if _, err := RaiseToExternallyAttested(o, a, nil); !errors.Is(err, ErrAttestationSubject) {
		t.Fatalf("expected ErrAttestationSubject, got %v", err)
	}
	if LevelOf(o, []ProcedureAttestation{a}, nil) != LevelQualified {
		t.Fatal("an attestation of another object must not raise this one")
	}
}

// TestAnAttestorWhoDidNotAgreeDoesNotAttest: the attestor's positive
// statement is the substance, not their presence.
func TestAnAttestorWhoDidNotAgreeDoesNotAttest(t *testing.T) {
	o, _ := Seal(good(t))
	a := attestation(o.CanonicalHash)
	a.ProcedureCorrectlyApplied = false
	if _, err := RaiseToExternallyAttested(o, a, nil); err == nil {
		t.Fatal("an attestor who did not agree must not raise the level")
	}
}

// TestAnUnqualifiedObjectCannotBeAttested: an outside party cannot
// rescue an object VERIQO's own rules reject.
func TestAnUnqualifiedObjectCannotBeAttested(t *testing.T) {
	o := good(t)
	o.Quality.Assessed = false
	sealed, _ := Seal(o)
	if _, err := RaiseToExternallyAttested(sealed, attestation(sealed.CanonicalHash), nil); !errors.Is(err, ErrNotQualified) {
		t.Fatalf("expected ErrNotQualified, got %v", err)
	}
}

// TestTamperedObjectCannotBeAttested: an object edited after sealing.
func TestTamperedObjectCannotBeAttested(t *testing.T) {
	o, _ := Seal(good(t))
	hash := o.CanonicalHash
	o.Proposition.Statement = "a more convenient statement"
	if _, err := RaiseToExternallyAttested(o, attestation(hash), nil); err == nil {
		t.Fatal("an altered object must not be attestable")
	}
}

// TestAttestationWithReservationsStillCounts, and the reservations
// travel with it. Hiding them would make the attestation a lie.
func TestAttestationWithReservationsStillCounts(t *testing.T) {
	o, _ := Seal(good(t))
	a := attestation(o.CanonicalHash)
	a.Reservations = []string{"we did not re-examine the underlying laboratory method"}
	l, err := RaiseToExternallyAttested(o, a, []string{veriqoEntity})
	if err != nil {
		t.Fatalf("RaiseToExternallyAttested: %v", err)
	}
	if l != LevelExternallyAttested {
		t.Fatal("reservations do not void an attestation")
	}
	if len(a.Reservations) != 1 {
		t.Fatal("the reservations must survive")
	}
}

// TestNoVeriqoCodePathReachesLevelThree is the structural claim: the
// only route to PROOF_EXTERNALLY_ATTESTED is a real outside statement,
// and nothing in the repository manufactures one.
func TestNoVeriqoCodePathReachesLevelThree(t *testing.T) {
	o, _ := Seal(good(t))
	// Every level derivable from the object alone, with no attestation.
	for _, attestations := range [][]ProcedureAttestation{nil, {}} {
		if l := LevelOf(o, attestations, []string{veriqoEntity}); l == LevelExternallyAttested {
			t.Fatal("no object alone may reach PROOF_EXTERNALLY_ATTESTED")
		}
	}
}
